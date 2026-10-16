package subnet

import (
	"context"
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
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var ticker = time.NewTicker(10 * time.Minute)

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

	// Process each unique associatedResource
	for associatedResource, namespacedNames := range r.SubnetService.SharedSubnetResourceMap {
		log.Debug("Polling shared Subnets", "AssociatedResource", associatedResource, "SubnetCount", len(namespacedNames))

		// Update the nsxSubnetCache with the latest NSX subnet data and status list
		// This is done here to ensure the cache is updated during polling
		var nsxSubnet *model.VpcSubnet
		var statusList []model.VpcSubnetStatus
		var err error

		// Get NSX subnet from API (not from cache during polling to ensure fresh data)
		log.Debug("Fetching NSX subnet during polling", "AssociatedResource", associatedResource)
		nsxSubnet, err = r.SubnetService.GetNSXSubnetByAssociatedResource(associatedResource)
		if err != nil {
			r.handleNSXSubnetError(ctx, err, namespacedNames, associatedResource, "NSX subnet")
			continue
		}

		// Get subnet status from NSX (not from cache during polling to ensure fresh data)
		log.Debug("Fetching status list during polling", "AssociatedResource", associatedResource)
		statusList, err = r.SubnetService.GetSubnetStatus(nsxSubnet)
		if err != nil {
			r.handleNSXSubnetError(ctx, err, namespacedNames, associatedResource, "subnet status")
			continue
		}

		// Update the cache with the latest NSX subnet and status list
		r.SubnetService.UpdateNSXSubnetCache(associatedResource, nsxSubnet, statusList)

		// Regularly poll and sync PortConfigs for this shared subnet across all mapped Namespaces
		r.pollAndSyncPortConfigs(ctx, associatedResource, namespacedNames)

		// Enqueue all subnet CRs associated with this resource for reconciliation
		for namespacedName := range namespacedNames {
			log.Info("Enqueueing shared Subnet for reconciliation", "Subnet", namespacedName, "AssociatedResource", associatedResource)
			r.enqueueSubnetForReconciliation(ctx, namespacedName)
		}
	}

	for associatedResource := range r.SubnetService.NSXSubnetCache {
		if _, ok := r.SubnetService.SharedSubnetResourceMap[associatedResource]; !ok {
			log.Debug("Remove Subnet from cache", "AssociatedResource", associatedResource)
			r.SubnetService.RemoveSubnetFromCache(associatedResource, "no valid subnets")
			subnetPath, err := servicecommon.GetSubnetPathFromAssociatedResource(associatedResource)
			if err != nil {
				log.Error(err, "Invalid associatedResource", "associatedResource", associatedResource)
			} else {
				r.SubnetPortService.DeletePortCount(subnetPath)
			}
		}
	}
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
func (r *SubnetReconciler) updateSharedSubnetWithError(ctx context.Context, namespacedName types.NamespacedName, err error, errorType string) {
	// Get the subnet CR
	subnetCR := &v1alpha1.Subnet{}
	if getErr := r.Client.Get(ctx, namespacedName, subnetCR); getErr != nil {
		log.Error(getErr, "Failed to get Subnet CR", "Subnet", namespacedName)
		return
	}

	// Skip if the subnet is being deleted
	if !subnetCR.DeletionTimestamp.IsZero() {
		return
	}

	// Set the subnet ready status to false with the appropriate error message
	r.clearSubnetAddresses(subnetCR)
	r.StatusUpdater.UpdateFail(ctx, subnetCR, err, "Failed to get Subnet status", setSubnetReadyStatusFalse)
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
	subnetPath, err := servicecommon.GetSubnetPathFromAssociatedResource(associatedResource)
	if err != nil {
		log.Error(err, "Invalid associatedResource", "associatedResource", associatedResource)
	} else {
		r.SubnetPortService.DeletePortCount(subnetPath)
	}
	// Set subnet ready status to false for all valid subnets
	for namespacedName := range validSubnets {
		r.updateSharedSubnetWithError(ctx, namespacedName, err, errorType)
	}
}

// pollAndSyncPortConfigs regularly polls NSX PortConfigs (PortSettings) for a shared subnet
// and synchronizes SubnetPortSetting CRs across all namespaces where the subnet is shared.
func (r *SubnetReconciler) pollAndSyncPortConfigs(ctx context.Context, associatedResource string, namespacedNames sets.Set[types.NamespacedName]) {
	portSettings, err := r.SubnetService.ListPortSettingsByAssociatedResource(associatedResource)
	if err != nil {
		log.Warn("Failed to list PortConfigs for shared Subnet during polling", "AssociatedResource", associatedResource, "err", err)
		return
	}

	nsxPortSettingIDs := sets.New[string]()
	for _, ps := range portSettings {
		if ps.Id != nil && *ps.Id != "" {
			nsxPortSettingIDs.Insert(*ps.Id)
		}
	}

	for namespacedName := range namespacedNames {
		subnetCR := &v1alpha1.Subnet{}
		if err := r.Client.Get(ctx, namespacedName, subnetCR); err != nil {
			log.Error(err, "Failed to get Subnet CR for PortConfig sync", "Subnet", namespacedName)
			continue
		}
		if !subnetCR.DeletionTimestamp.IsZero() {
			continue
		}

		if err := r.syncPortConfigsForNamespaceSubnet(ctx, subnetCR, portSettings, nsxPortSettingIDs, associatedResource); err != nil {
			log.Error(err, "Failed to sync PortConfigs for Subnet", "Subnet", namespacedName)
		}
	}
}

// generateValidPortSettingName creates a valid Kubernetes name from portSetting ID
func generateValidPortSettingName(psID string) string {
	if len(validation.IsDNS1123Subdomain(psID)) == 0 {
		return psID
	}
	return "port-setting-" + util.TruncateUIDHash(psID)
}

// getPortSettingIDFromCR extracts the original portSetting ID from SubnetPortSetting CR
func getPortSettingIDFromCR(cr *v1alpha1.SubnetPortSetting) string {
	if cr.Annotations != nil {
		if anno, ok := cr.Annotations[servicecommon.AnnotationAssociatedResource]; ok && anno != "" {
			parts := strings.Split(anno, ":")
			if len(parts) >= 4 {
				return parts[len(parts)-1]
			}
		}
	}
	return cr.Name
}

// syncPortConfigsForNamespaceSubnet reconciles SubnetPortSetting CRs for a specific shared Subnet CR.
// It creates new CRs for NSX PortConfigs, updates existing ones, and deletes orphan CRs.
func (r *SubnetReconciler) syncPortConfigsForNamespaceSubnet(ctx context.Context, subnetCR *v1alpha1.Subnet, portSettings []model.VpcSubnetPortSetting, nsxPortSettingIDs sets.Set[string], associatedResource string) error {
	if associatedResource == "" && subnetCR.Annotations != nil {
		associatedResource = subnetCR.Annotations[servicecommon.AnnotationAssociatedResource]
	}

	existingList := &v1alpha1.SubnetPortSettingList{}
	if err := r.Client.List(ctx, existingList, client.InNamespace(subnetCR.Namespace)); err != nil {
		return fmt.Errorf("failed to list SubnetPortSetting CRs in namespace %s: %w", subnetCR.Namespace, err)
	}

	existingCRByPsID := make(map[string]*v1alpha1.SubnetPortSetting)
	for i := range existingList.Items {
		item := &existingList.Items[i]
		if item.Spec.SubnetName == subnetCR.Name {
			psID := getPortSettingIDFromCR(item)
			existingCRByPsID[psID] = item
		}
	}

	// Create new SubnetPortSetting CRs or update existing ones
	for _, ps := range portSettings {
		if ps.Id == nil || *ps.Id == "" {
			continue
		}
		psID := *ps.Id

		existingCR, exists := existingCRByPsID[psID]
		if !exists {
			targetName := generateValidPortSettingName(psID)
			psAnno := psID
			if associatedResource != "" {
				psAnno = fmt.Sprintf("%s:%s", associatedResource, psID)
			}
			newCR := &v1alpha1.SubnetPortSetting{
				ObjectMeta: metav1.ObjectMeta{
					Name:      targetName,
					Namespace: subnetCR.Namespace,
					Annotations: map[string]string{
						servicecommon.AnnotationAssociatedResource: psAnno,
					},
				},
				Spec: v1alpha1.SubnetPortSettingSpec{
					SubnetName: subnetCR.Name,
				},
			}
			if err := controllerutil.SetOwnerReference(subnetCR, newCR, r.Scheme); err != nil {
				log.Warn("Failed to set OwnerReference for SubnetPortSetting", "Namespace", subnetCR.Namespace, "Name", targetName, "err", err)
			}
			err := r.Client.Create(ctx, newCR)
			if err != nil {
				// If a CR with the same name already exists, fallback to generateName
				if apierrors.IsAlreadyExists(err) {
					log.Info("SubnetPortSetting CR with the same name already exists, using generateName",
						"Namespace", subnetCR.Namespace, "Name", targetName)
					newCR.GenerateName = targetName + "-"
					newCR.Name = ""
					err = r.Client.Create(ctx, newCR)
				}
			}
			if err != nil {
				log.Error(err, "Failed to create SubnetPortSetting CR", "Namespace", subnetCR.Namespace, "Name", targetName)
				continue
			}
			log.Info("Successfully created SubnetPortSetting CR", "Namespace", subnetCR.Namespace, "Name", newCR.Name)

			newCR.Status.Conditions = []v1alpha1.Condition{
				{
					Type:               v1alpha1.Ready,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             "PortSettingRealized",
					Message:            fmt.Sprintf("PortSetting %s is realized on NSX", psID),
				},
			}
			if err := r.Client.Status().Update(ctx, newCR); err != nil {
				log.Error(err, "Failed to update SubnetPortSetting status", "Namespace", subnetCR.Namespace, "Name", newCR.Name)
			}
		} else {
			// Update status if not marked Ready
			hasReadyCondition := false
			for _, cond := range existingCR.Status.Conditions {
				if cond.Type == v1alpha1.Ready && cond.Status == corev1.ConditionTrue {
					hasReadyCondition = true
					break
				}
			}
			if !hasReadyCondition {
				existingCR.Status.Conditions = []v1alpha1.Condition{
					{
						Type:               v1alpha1.Ready,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "PortSettingRealized",
						Message:            fmt.Sprintf("PortSetting %s is realized on NSX", psID),
					},
				}
				if err := r.Client.Status().Update(ctx, existingCR); err != nil {
					log.Error(err, "Failed to update existing SubnetPortSetting status", "Namespace", subnetCR.Namespace, "Name", existingCR.Name)
				}
			}
		}
	}

	// Delete orphan CRs that no longer exist in NSX
	for psID, cr := range existingCRByPsID {
		if !nsxPortSettingIDs.Has(psID) {
			if err := r.Client.Delete(ctx, cr); err != nil {
				log.Error(err, "Failed to delete orphan SubnetPortSetting CR", "Namespace", subnetCR.Namespace, "Name", cr.Name)
			} else {
				log.Info("Successfully deleted orphan SubnetPortSetting CR", "Namespace", subnetCR.Namespace, "Name", cr.Name)
			}
		}
	}

	return nil
}

