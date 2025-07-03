package subnet

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
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
	return originalSpec.AdvancedConfig.ConnectivityState != newSpec.AdvancedConfig.ConnectivityState ||
		originalSpec.SubnetDHCPConfig.Mode != newSpec.SubnetDHCPConfig.Mode
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
	// Set subnet ready status to false for all valid subnets
	for namespacedName := range validSubnets {
		r.updateSharedSubnetWithError(ctx, namespacedName, err, errorType)
	}
}
