package subnet

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	subnetservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
)

var ticker = time.NewTicker(subnetservice.SharedSubnetRefreshInterval)

// pollSharedSubnets periodically polls NSX for shared subnet status updates
// It runs in a separate goroutine and polls every 10 minutes.
// The polling can be stopped by sending a value to the stopCh channel.
func (r *SubnetReconciler) pollSharedSubnets(stopCh chan bool) {
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.pollAllSharedSubnets()
		case <-stopCh:
			log.Info("Stopping shared Subnet polling")
			return
		}
	}
}

// pollAllSharedSubnets polls NSX for all shared subnets.
// It groups subnets by associatedResource to avoid redundant NSX API calls.
// For each unique associatedResource, it gets the NSX subnet and status only once,
// then updates all related Subnet CRs with the same information.
// It also removes subnets from the queue if they no longer exist or are being deleted.
func (r *SubnetReconciler) pollAllSharedSubnets() {
	ctx := context.Background()
	for associatedResource, namespacedNames := range r.SubnetService.GetSharedSubnetResourceMapSnapshot() {
		r.pollSharedSubnet(ctx, associatedResource, namespacedNames)
	}

	for _, associatedResource := range r.SubnetService.GetNSXSubnetCacheKeys() {
		r.removeUnsharedSubnetFromCache(ctx, associatedResource)
	}
}

// tryStartSharedSubnetSync reserves a source without blocking a reconciliation worker.
func (r *SubnetReconciler) tryStartSharedSubnetSync(associatedResource string) (chan struct{}, bool) {
	done, loaded := r.sharedSubnetSyncs.LoadOrStore(associatedResource, make(chan struct{}))
	return done.(chan struct{}), !loaded
}

// finishSharedSubnetSync is called only by the owner, after all source reads and writes finish.
func (r *SubnetReconciler) finishSharedSubnetSync(associatedResource string, done chan struct{}) {
	r.sharedSubnetSyncs.Delete(associatedResource)
	close(done)
}

// waitStartSharedSubnetSync lets polling refresh a busy source once its current sync finishes.
func (r *SubnetReconciler) waitStartSharedSubnetSync(ctx context.Context, associatedResource string) (chan struct{}, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		done, started := r.tryStartSharedSubnetSync(associatedResource)
		if started {
			return done, nil
		}
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (r *SubnetReconciler) pollSharedSubnet(ctx context.Context, associatedResource string, namespacedNames sets.Set[types.NamespacedName]) {
	done, err := r.waitStartSharedSubnetSync(ctx, associatedResource)
	if err != nil {
		return
	}
	refreshed := func() bool {
		defer r.finishSharedSubnetSync(associatedResource, done)

		log.Debug("Polling shared Subnets", "AssociatedResource", associatedResource, "SubnetCount", len(namespacedNames))
		// Refresh the parent before publishing and applying a PortSettings snapshot.
		nsxSubnet, err := r.SubnetService.GetNSXSubnetByAssociatedResource(associatedResource)
		if err != nil {
			r.handleNSXSubnetError(ctx, err, namespacedNames, associatedResource, "NSX subnet")
			return false
		}
		statusList, err := r.SubnetService.GetSubnetStatus(nsxSubnet)
		if err != nil {
			r.handleNSXSubnetError(ctx, err, namespacedNames, associatedResource, "subnet status")
			return false
		}
		r.SubnetService.UpdateNSXSubnetCache(associatedResource, nsxSubnet, statusList)
		r.pollAndSyncPortConfigs(ctx, associatedResource, namespacedNames)
		return true
	}()
	if !refreshed {
		return
	}

	// Workers may start immediately, so release the source before enqueueing them.
	for namespacedName := range namespacedNames {
		r.enqueueSubnetForReconciliation(ctx, namespacedName)
	}
}

func (r *SubnetReconciler) removeUnsharedSubnetFromCache(ctx context.Context, associatedResource string) {
	done, err := r.waitStartSharedSubnetSync(ctx, associatedResource)
	if err != nil {
		return
	}
	defer r.finishSharedSubnetSync(associatedResource, done)

	if !r.SubnetService.RemoveSubnetFromCacheIfUnshared(associatedResource) {
		return
	}
	subnetPath, err := servicecommon.GetSubnetPathFromAssociatedResource(associatedResource)
	if err != nil {
		log.Error(err, "Invalid associatedResource", "associatedResource", associatedResource)
		return
	}
	r.SubnetPortService.DeletePortCount(subnetPath)
}

// enqueueSubnetForReconciliation enqueues a subnet CR for reconciliation by the Subnet Controller
func (r *SubnetReconciler) enqueueSubnetForReconciliation(ctx context.Context, namespacedName types.NamespacedName) {
	// Get the subnet CR
	subnetCR := &v1alpha1.Subnet{}
	if err := r.Client.Get(ctx, namespacedName, subnetCR); err != nil {
		log.Error(err, "Failed to get Subnet CR during polling", "Subnet", namespacedName)
		return
	}

	// Skip if the subnet is being deleted
	if !subnetCR.DeletionTimestamp.IsZero() {
		return
	}

	// Add the subnet to the reconciliation queue
	req := reconcile.Request{
		NamespacedName: namespacedName,
	}
	r.queue.Add(req)
	log.Info("Successfully enqueued Subnet for reconciliation", "Subnet", namespacedName)
}

// updateSharedSubnetWithError updates a subnet CR with error information
// This is used when either GetNSXSubnetByAssociatedResource or GetSubnetStatus returns an error
func (r *SubnetReconciler) updateSharedSubnetWithError(ctx context.Context, namespacedName types.NamespacedName, associatedResource string, err error, errorType string) {
	// Get the subnet CR
	subnetCR := &v1alpha1.Subnet{}
	if getErr := r.APIReader.Get(ctx, namespacedName, subnetCR); getErr != nil {
		log.Error(getErr, "Failed to get Subnet CR", "Subnet", namespacedName)
		return
	}

	// Skip if the subnet is being deleted
	if !subnetCR.DeletionTimestamp.IsZero() || subnetCR.Annotations[servicecommon.AnnotationAssociatedResource] != associatedResource {
		return
	}

	// Set the subnet ready status to false with the appropriate error message
	r.clearSubnetAddresses(subnetCR)
	r.StatusUpdater.UpdateFail(ctx, subnetCR, err, "Failed to get Subnet status", setSubnetReadyStatusFalse)
	if updateErr := r.markPortConfigsNotReady(ctx, subnetCR, err); updateErr != nil {
		log.Error(updateErr, "Failed to mark SubnetPortSettings not ready", "Subnet", namespacedName)
	}
	log.Info("Set Subnet ready status to false", "errorType", errorType, "Subnet", namespacedName)
}

// updateSubnetIfNeeded updates the subnet if it has changed
func (r *SubnetReconciler) updateSubnetIfNeeded(ctx context.Context, subnetCR *v1alpha1.Subnet, nsxSubnet *model.VpcSubnet,
	statusList []model.VpcSubnetStatus, namespacedName types.NamespacedName) error {
	// Create a copy of the current status and spec before updating
	originalStatus := subnetCR.Status.DeepCopy()
	originalSpec := subnetCR.Spec.DeepCopy()

	r.SubnetService.MapNSXSubnetToSubnetCR(subnetCR, nsxSubnet)
	r.SubnetService.MapNSXSubnetStatusToSubnetCRStatus(subnetCR, statusList)

	// Check if spec has changed
	specChanged := r.hasSubnetSpecChanged(originalSpec, &subnetCR.Spec)
	// Check if the status has changed
	statusChanged := r.hasStatusChanged(originalStatus, &subnetCR.Status)

	// Update the CR if either spec or status has changed
	if specChanged {
		// Update the CR spec if there are changes
		if err := r.Client.Update(ctx, subnetCR); err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetCR, err, "Failed to update shared Subnet spec", setSubnetReadyStatusFalse)
			return fmt.Errorf("failed to update shared Subnet spec: %v", err)
		}
		log.Info("Successfully updated shared subnet spec during polling or reconciling", "Subnet", namespacedName)
	}
	if statusChanged {
		// Update the CR status only if there are changes
		if err := r.Client.Status().Update(ctx, subnetCR); err != nil {
			r.StatusUpdater.UpdateFail(ctx, subnetCR, err, "Failed to update shared Subnet status", setSubnetReadyStatusFalse)
			return fmt.Errorf("failed to update shared Subnet status: %v", err)
		}
		log.Info("Successfully updated shared Subnet status during polling", "Subnet", namespacedName)
	} else {
		log.Info("No changes in shared Subnet, skipping update", "Subnet", namespacedName)
	}
	return nil
}

// hasStatusChanged checks if the subnet status has changed
func (r *SubnetReconciler) hasStatusChanged(originalStatus, newStatus *v1alpha1.SubnetStatus) bool {
	return !reflect.DeepEqual(originalStatus.NetworkAddresses, newStatus.NetworkAddresses) ||
		!reflect.DeepEqual(originalStatus.GatewayAddresses, newStatus.GatewayAddresses) ||
		!reflect.DeepEqual(originalStatus.DHCPServerAddresses, newStatus.DHCPServerAddresses) ||
		!reflect.DeepEqual(originalStatus.VLANExtension, newStatus.VLANExtension) ||
		originalStatus.Shared != newStatus.Shared
}

// hasSubnetSpecChanged checks if the subnet spec has changed
func (r *SubnetReconciler) hasSubnetSpecChanged(originalSpec, newSpec *v1alpha1.SubnetSpec) bool {
	return !reflect.DeepEqual(originalSpec, newSpec)
}

func (r *SubnetReconciler) clearSubnetAddresses(obj client.Object) {
	subnet := obj.(*v1alpha1.Subnet)
	subnet.Status.NetworkAddresses = subnet.Status.NetworkAddresses[:0]
	subnet.Status.GatewayAddresses = subnet.Status.GatewayAddresses[:0]
	subnet.Status.DHCPServerAddresses = subnet.Status.DHCPServerAddresses[:0]
	if err := r.Client.Status().Update(context.TODO(), subnet); err != nil {
		log.Error(err, "Failed to update Subnet status", "Name", subnet.Name, "Namespace", subnet.Namespace)
	} else {
		log.Info("Cleared Subnet addresses", "Name", subnet.Name, "Namespace", subnet.Namespace)
	}
}

// handleNSXSubnetError handles errors when getting NSX subnet data
// It updates all affected subnets with the error information
func (r *SubnetReconciler) handleNSXSubnetError(ctx context.Context, err error, validSubnets sets.Set[types.NamespacedName], associatedResource,
	errorType string) {
	log.Error(err, fmt.Sprintf("Failed to get %s", errorType), "AssociatedResource", associatedResource)
	r.SubnetService.RemoveSubnetFromCache(associatedResource, "NSXSubnetError")
	subnetPath, pathErr := servicecommon.GetSubnetPathFromAssociatedResource(associatedResource)
	if pathErr != nil {
		log.Error(pathErr, "Invalid associatedResource", "associatedResource", associatedResource)
	} else {
		r.SubnetPortService.DeletePortCount(subnetPath)
	}
	// Set subnet ready status to false for all valid subnets
	for namespacedName := range validSubnets {
		r.updateSharedSubnetWithError(ctx, namespacedName, associatedResource, err, errorType)
	}
}

// pollAndSyncPortConfigs regularly polls NSX PortConfigs (PortSettings) for a shared subnet
// and synchronizes SubnetPortSetting CRs across all namespaces where the subnet is shared.
func (r *SubnetReconciler) pollAndSyncPortConfigs(ctx context.Context, associatedResource string, namespacedNames sets.Set[types.NamespacedName]) {
	if !nsx.SubnetPortSettingFeatureEnabled(r.SubnetService.NSXClient, r.SubnetService.NSXConfig) {
		return
	}

	portSettings, err := r.SubnetService.GetPortSettingsFromCacheOrAPI(associatedResource, true)
	if err != nil {
		log.Warn("Failed to list PortConfigs for shared Subnet during polling", "AssociatedResource", associatedResource, "err", err)
		return
	}

	for namespacedName := range namespacedNames {
		subnetCR := &v1alpha1.Subnet{}
		if err := r.APIReader.Get(ctx, namespacedName, subnetCR); err != nil {
			log.Error(err, "Failed to get Subnet CR for PortConfig sync", "Subnet", namespacedName)
			continue
		}
		if !subnetCR.DeletionTimestamp.IsZero() || subnetCR.Annotations[servicecommon.AnnotationAssociatedResource] != associatedResource {
			continue
		}

		if err := r.syncPortConfigsForNamespaceSubnet(ctx, subnetCR, portSettings, associatedResource); err != nil {
			log.Error(err, "Failed to sync PortConfigs for Subnet", "Subnet", namespacedName)
		}
	}
}

// getPortSettingIDFromCR extracts the original portSetting ID from SubnetPortSetting CR.
func getPortSettingIDFromCR(cr *v1alpha1.SubnetPortSetting) string {
	parts := strings.SplitN(cr.Annotations[servicecommon.AnnotationAssociatedResource], ":", 4)
	if len(parts) == 4 {
		return parts[3]
	}
	return cr.Name
}

// portConfigBelongsToSubnet excludes mirrors left by an earlier Subnet with the same name.
func portConfigBelongsToSubnet(cr *v1alpha1.SubnetPortSetting, subnetCR *v1alpha1.Subnet) bool {
	if subnetCR.UID == "" {
		return false
	}
	for _, owner := range cr.OwnerReferences {
		if owner.APIVersion == v1alpha1.GroupVersion.String() && owner.Kind == "Subnet" &&
			owner.Name == subnetCR.Name && owner.UID == subnetCR.UID {
			return true
		}
	}
	return false
}

// listPortConfigs reads directly from the API server so recently created CRs are visible.
func (r *SubnetReconciler) listPortConfigs(ctx context.Context, subnetCR *v1alpha1.Subnet, associatedResource string) ([]v1alpha1.SubnetPortSetting, error) {
	if associatedResource == "" {
		return nil, fmt.Errorf("associated resource must not be empty")
	}
	existingList := &v1alpha1.SubnetPortSettingList{}
	if err := r.APIReader.List(ctx, existingList, client.InNamespace(subnetCR.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list SubnetPortSetting CRs in namespace %s: %w", subnetCR.Namespace, err)
	}
	var settings []v1alpha1.SubnetPortSetting
	prefix := associatedResource + ":"
	for _, item := range existingList.Items {
		if item.Spec.SubnetName != subnetCR.Name || !item.DeletionTimestamp.IsZero() ||
			!portConfigBelongsToSubnet(&item, subnetCR) {
			continue
		}
		annotation := item.Annotations[servicecommon.AnnotationAssociatedResource]
		if !strings.HasPrefix(annotation, prefix) || len(annotation) == len(prefix) {
			continue
		}
		settings = append(settings, item)
	}
	return settings, nil
}

func (r *SubnetReconciler) updatePortConfigReady(ctx context.Context, cr *v1alpha1.SubnetPortSetting, status corev1.ConditionStatus, reason, message string) error {
	for i := range cr.Status.Conditions {
		condition := &cr.Status.Conditions[i]
		if condition.Type != v1alpha1.Ready {
			continue
		}
		if condition.Status == status && condition.Reason == reason && condition.Message == message {
			return nil
		}
		if condition.Status != status {
			condition.LastTransitionTime = metav1.Now()
		}
		condition.Status, condition.Reason, condition.Message = status, reason, message
		return r.Client.Status().Update(ctx, cr)
	}
	cr.Status.Conditions = append(cr.Status.Conditions, v1alpha1.Condition{
		Type: v1alpha1.Ready, Status: status, LastTransitionTime: metav1.Now(),
		Reason: reason, Message: message,
	})
	return r.Client.Status().Update(ctx, cr)
}

// markPortConfigsNotReady retains the mirror CRs while their NSX source cannot be queried.
func (r *SubnetReconciler) markPortConfigsNotReady(ctx context.Context, subnetCR *v1alpha1.Subnet, sourceErr error) error {
	if !nsx.SubnetPortSettingFeatureEnabled(r.SubnetService.NSXClient, r.SubnetService.NSXConfig) {
		return nil
	}

	settings, err := r.listPortConfigs(ctx, subnetCR, subnetCR.Annotations[servicecommon.AnnotationAssociatedResource])
	if err != nil {
		return err
	}
	var errs []error
	for i := range settings {
		if err := r.updatePortConfigReady(ctx, &settings[i], corev1.ConditionFalse, "PortSettingNotReady",
			fmt.Sprintf("Failed to query NSX configuration: %v", sourceErr)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// createPortConfig reuses an existing mirror on a concurrent create or stale read.
// A name occupied by another source or parent identity is a conflict.
func (r *SubnetReconciler) createPortConfig(ctx context.Context, subnetCR *v1alpha1.Subnet, psID, associatedResource, targetName string) (*v1alpha1.SubnetPortSetting, error) {
	annotation := associatedResource + ":" + psID
	newCR := &v1alpha1.SubnetPortSetting{
		ObjectMeta: metav1.ObjectMeta{
			Name: targetName, Namespace: subnetCR.Namespace,
			Annotations: map[string]string{servicecommon.AnnotationAssociatedResource: annotation},
		},
		Spec: v1alpha1.SubnetPortSettingSpec{SubnetName: subnetCR.Name},
	}
	if err := controllerutil.SetOwnerReference(subnetCR, newCR, r.Scheme); err != nil {
		return nil, err
	}
	err := r.Client.Create(ctx, newCR)
	if apierrors.IsAlreadyExists(err) {
		existing := &v1alpha1.SubnetPortSetting{}
		if getErr := r.APIReader.Get(ctx, client.ObjectKeyFromObject(newCR), existing); getErr != nil {
			return nil, getErr
		}
		if existing.Spec.SubnetName == subnetCR.Name && existing.Annotations[servicecommon.AnnotationAssociatedResource] == annotation &&
			portConfigBelongsToSubnet(existing, subnetCR) {
			if !existing.DeletionTimestamp.IsZero() {
				return nil, fmt.Errorf("SubnetPortSetting %s is being deleted", targetName)
			}
			return existing, nil
		}
		return nil, fmt.Errorf("SubnetPortSetting %s/%s conflicts with source %q or parent identity", subnetCR.Namespace, targetName, existing.Annotations[servicecommon.AnnotationAssociatedResource])
	}
	if err != nil {
		return nil, err
	}
	return newCR, nil
}

// syncPortConfigsForNamespaceSubnet reconciles a complete NSX snapshot for one shared Subnet.
// Callers serialize this with parent Subnet queries for the same associated resource.
func (r *SubnetReconciler) syncPortConfigsForNamespaceSubnet(ctx context.Context, subnetCR *v1alpha1.Subnet, portSettings []model.VpcSubnetPortSetting, associatedResource string) error {
	if associatedResource == "" {
		associatedResource = subnetCR.Annotations[servicecommon.AnnotationAssociatedResource]
	}
	// Derive existence and naming from the same complete snapshot. A missing ID
	// or contradictory duplicate makes absence unsafe to use for orphan cleanup.
	type identity struct {
		uniqueID  string
		isDefault bool
	}
	identities := make(map[string]identity)
	nsxPortSettingIDs := sets.New[string]()
	for _, ps := range portSettings {
		isDefault := ps.IsDefault != nil && *ps.IsDefault
		if ps.Id == nil || *ps.Id == "" {
			if isDefault {
				continue
			}
			return fmt.Errorf("incomplete PortSettings snapshot for %s: missing NSX ID", associatedResource)
		}
		current := identity{isDefault: isDefault}
		if ps.UniqueId != nil {
			current.uniqueID = *ps.UniqueId
		}
		if previous, exists := identities[*ps.Id]; exists && previous != current {
			return fmt.Errorf("inconsistent PortSettings snapshot for %s: conflicting identity for %q", associatedResource, *ps.Id)
		}
		identities[*ps.Id] = current
		if !isDefault {
			nsxPortSettingIDs.Insert(*ps.Id)
		}
	}
	settings, err := r.listPortConfigs(ctx, subnetCR, associatedResource)
	if err != nil {
		return err
	}
	existingCRsByPsID := make(map[string][]*v1alpha1.SubnetPortSetting)
	for i := range settings {
		item := &settings[i]
		psID := getPortSettingIDFromCR(item)
		existingCRsByPsID[psID] = append(existingCRsByPsID[psID], item)
	}
	var errs []error
	var deleteCandidates []*v1alpha1.SubnetPortSetting
	processed := sets.New[string]()
	protectedNames := sets.New[string]()
	for _, ps := range portSettings {
		if ps.IsDefault != nil && *ps.IsDefault {
			continue
		}
		psID := *ps.Id
		if processed.Has(psID) {
			continue
		}
		processed.Insert(psID)
		mirrors := existingCRsByPsID[psID]
		targetName, err := r.portSettingName(ctx, subnetCR.Namespace, associatedResource, ps)
		if err != nil {
			errs = append(errs, err)
			for _, cr := range mirrors {
				if statusErr := r.updatePortConfigReady(ctx, cr, corev1.ConditionFalse, "PortSettingIdentityError", err.Error()); statusErr != nil {
					errs = append(errs, statusErr)
				}
			}
			continue
		}
		var desired *v1alpha1.SubnetPortSetting
		for _, cr := range mirrors {
			if cr.Name == targetName {
				desired = cr
				break
			}
		}
		if desired == nil {
			desired, err = r.createPortConfig(ctx, subnetCR, psID, associatedResource, targetName)
			if err != nil {
				errs = append(errs, err)
				// Never use cleanup to free a name whose creation failed. The
				// conflicting object may belong to another backing in this snapshot.
				protectedNames.Insert(targetName)
				continue
			}
		}
		if err := r.updatePortConfigReady(ctx, desired, corev1.ConditionTrue, "PortSettingRealized",
			fmt.Sprintf("PortSetting %s is realized on NSX", psID)); err != nil {
			errs = append(errs, err)
			continue
		}
		// Only retire legacy hash/generated names after the desired mirror exists
		// and is Ready. Conflicts and failed writes preserve the old inventory.
		for _, cr := range mirrors {
			if cr.Name != desired.Name {
				deleteCandidates = append(deleteCandidates, cr)
			}
		}
	}
	for psID, mirrors := range existingCRsByPsID {
		if nsxPortSettingIDs.Has(psID) {
			continue
		}
		deleteCandidates = append(deleteCandidates, mirrors...)
	}
	// Defer all retirement until every desired name has been checked. Otherwise
	// enumeration order could remove a conflicting legacy name before noticing it.
	for _, cr := range deleteCandidates {
		if protectedNames.Has(cr.Name) {
			continue
		}
		preconditions := client.Preconditions{UID: &cr.UID, ResourceVersion: &cr.ResourceVersion}
		if err := r.Client.Delete(ctx, cr, preconditions); client.IgnoreNotFound(err) != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
