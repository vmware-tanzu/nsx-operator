package subnet

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

// pollAllSharedSubnets polls NSX for all shared subnets in the queue.
// It groups subnets by associatedResource to avoid redundant NSX API calls.
// For each unique associatedResource, it gets the NSX subnet and status only once,
// then updates all related Subnet CRs with the same information.
// It also removes subnets from the queue if they no longer exist or are being deleted.
func (r *SubnetReconciler) pollAllSharedSubnets() {
	// Group namespacedNames by associatedResource
	resourceMap := make(map[string][]types.NamespacedName)

	// Create a read lock to safely iterate through the map
	r.sharedSubnetsMutex.RLock()
	for namespacedName, associatedResource := range r.sharedSubnetsMap {
		resourceMap[associatedResource] = append(resourceMap[associatedResource], namespacedName)
	}
	r.sharedSubnetsMutex.RUnlock()

	// Process each unique associatedResource
	for associatedResource, namespacedNames := range resourceMap {
		ctx := context.Background()
		log.Info("Polling shared Subnets", "AssociatedResource", associatedResource, "SubnetCount", len(namespacedNames))

		// Get the first subnet's namespace to use for the NSX API call
		// We need to get the actual subnet to determine its namespace
		var validSubnets []types.NamespacedName

		for _, namespacedName := range namespacedNames {
			subnetCR := &v1alpha1.Subnet{}
			if err := r.Client.Get(ctx, namespacedName, subnetCR); err != nil {
				r.handleSubnetGetError(err, namespacedName)
				continue
			}

			// Skip if the subnet is being deleted
			if !subnetCR.DeletionTimestamp.IsZero() {
				r.removeSubnetFromPollingQueue(namespacedName, "deleting")
				continue
			}

			validSubnets = append(validSubnets, namespacedName)
		}

		if len(validSubnets) == 0 {
			log.Info("No valid Subnets found for associated resource", "AssociatedResource", associatedResource)
			continue
		}

		// Get the NSX subnet based on the associated resource - only once per associatedResource
		nsxSubnet, err := r.SubnetService.GetNSXSubnetByAssociatedResource(associatedResource)
		if err != nil {
			log.Error(err, "Failed to get NSX Subnet during polling", "AssociatedResource", associatedResource)
			// Set subnet ready status to false for all valid subnets
			for _, namespacedName := range validSubnets {
				r.pollSingleSharedSubnetWithError(ctx, namespacedName, err, associatedResource, "NSX subnet")
			}
			continue
		}

		// Get subnet status from NSX - only once per associatedResource
		statusList, err := r.SubnetService.GetSubnetStatus(nsxSubnet)
		if err != nil {
			log.Error(err, "Failed to get Subnet status during polling", "AssociatedResource", associatedResource)
			// Set subnet ready status to false for all valid subnets
			for _, namespacedName := range validSubnets {
				r.pollSingleSharedSubnetWithError(ctx, namespacedName, err, associatedResource, "subnet status")
			}
			continue
		}

		// Update all subnet CRs associated with this resource
		for _, namespacedName := range validSubnets {
			log.Info("Updating shared Subnet", "Subnet", namespacedName, "AssociatedResource", associatedResource)
			r.pollSingleSharedSubnet(ctx, namespacedName, nsxSubnet, statusList)
		}
	}
}

// pollSingleSharedSubnet updates a subnet CR with pre-fetched NSX subnet and status data
func (r *SubnetReconciler) pollSingleSharedSubnet(ctx context.Context, namespacedName types.NamespacedName, nsxSubnet *model.VpcSubnet, statusList []model.VpcSubnetStatus) {
	// Get the subnet CR
	subnetCR := &v1alpha1.Subnet{}
	if err := r.Client.Get(ctx, namespacedName, subnetCR); err != nil {
		r.handleSubnetGetError(err, namespacedName)
		return
	}

	// Skip if the subnet is being deleted
	if !subnetCR.DeletionTimestamp.IsZero() {
		r.removeSubnetFromPollingQueue(namespacedName, "deleting")
		return
	}

	// Create a copy of namespacedName to ensure it's properly passed to the function
	nsName := types.NamespacedName{
		Namespace: namespacedName.Namespace,
		Name:      namespacedName.Name,
	}

	if err := r.updateSubnetIfNeeded(ctx, subnetCR, nsxSubnet, statusList, nsName); err != nil {
		log.Error(err, "Failed to update Subnet status", "Subnet", namespacedName)
		return
	}
}

// pollSingleSharedSubnetWithError updates a subnet CR with error information
// This is used when either GetNSXSubnetByAssociatedResource or GetSubnetStatus returns an error
func (r *SubnetReconciler) pollSingleSharedSubnetWithError(ctx context.Context, namespacedName types.NamespacedName, err error, associatedResource string, errorType string) {
	// Get the subnet CR
	subnetCR := &v1alpha1.Subnet{}
	if getErr := r.Client.Get(ctx, namespacedName, subnetCR); getErr != nil {
		r.handleSubnetGetError(getErr, namespacedName)
		return
	}

	// Skip if the subnet is being deleted
	if !subnetCR.DeletionTimestamp.IsZero() {
		r.removeSubnetFromPollingQueue(namespacedName, "deleting")
		return
	}

	// Set the subnet ready status to false with the appropriate error message
	errorMsg := fmt.Sprintf("Failed to get %s during polling for AssociatedResource %s: %v", errorType, associatedResource, err)
	r.clearSubnetAddresses(subnetCR)
	setSubnetReadyStatusFalse(r.Client, ctx, subnetCR, metav1.Now(), err, errorMsg)
	log.Info("Set Subnet ready status to false", "errorType", errorType, "Subnet", namespacedName)
}

// handleSubnetGetError handles errors when getting a subnet CR
func (r *SubnetReconciler) handleSubnetGetError(err error, namespacedName types.NamespacedName) {
	if apierrors.IsNotFound(err) {
		// Subnet CR no longer exists, remove it from the polling queue
		r.removeSubnetFromPollingQueue(namespacedName, "deleted")
	} else {
		log.Error(err, "Failed to get Subnet CR during polling", "Subnet", namespacedName)
	}
}

// removeSubnetFromPollingQueue removes a subnet from the polling queue
func (r *SubnetReconciler) removeSubnetFromPollingQueue(namespacedName types.NamespacedName, reason string) {
	r.sharedSubnetsMutex.Lock()
	defer r.sharedSubnetsMutex.Unlock()
	delete(r.sharedSubnetsMap, namespacedName)
	log.Info("Removed Subnet from polling queue", "reason", reason, "Subnet", namespacedName)
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
	// TODO other fields to check?
	return originalSpec.AdvancedConfig.ConnectivityState != newSpec.AdvancedConfig.ConnectivityState ||
		originalSpec.SubnetDHCPConfig.Mode != newSpec.SubnetDHCPConfig.Mode
}

// addSubnetToPollingQueue adds a subnet to the polling queue if it's not already there
func (r *SubnetReconciler) addSubnetToPollingQueue(namespacedName types.NamespacedName, associatedResource string) {
	r.sharedSubnetsMutex.Lock()
	defer r.sharedSubnetsMutex.Unlock()

	if _, exists := r.sharedSubnetsMap[namespacedName]; !exists {
		r.sharedSubnetsMap[namespacedName] = associatedResource
		log.Info("Added shared Subnet to polling queue", "Subnet", namespacedName, "AssociatedResource", associatedResource)
	}
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
