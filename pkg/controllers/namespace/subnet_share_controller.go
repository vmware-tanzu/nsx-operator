package namespace

import (
	"context"
	"fmt"
	"regexp"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// isValidKubernetesName checks if a name meets Kubernetes RFC 1123 subdomain naming standards
func isValidKubernetesName(name string) bool {
	// RFC 1123 subdomain: lowercase alphanumeric characters, '-' or '.', and must-start and end with alphanumeric
	validNameRegex := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	return validNameRegex.MatchString(name)
}

// generateValidSubnetName creates a valid Kubernetes name from subnet ID
func generateValidSubnetName(subnetID string) string {
	if isValidKubernetesName(subnetID) {
		return subnetID
	}
	// Hash the whole subnet ID if it doesn't meet standards
	return "shared-subnet-" + util.TruncateUIDHash(subnetID)
}

// createSubnetCRInK8s creates the Subnet CR in Kubernetes
func (r *NamespaceReconciler) createSubnetCRInK8s(ctx context.Context, subnetCR *v1alpha1.Subnet) error {
	err := r.Client.Create(ctx, subnetCR)
	if err != nil {
		// If the Subnet CR already exists with the same name, try using generateName
		if apierrors.IsAlreadyExists(err) {
			log.Info("Subnet CR with the same name already exists, using generateName",
				"Namespace", subnetCR.Namespace, "Name", subnetCR.Name)

			// Create a new Subnet CR with generateName
			// subnetCR.ObjectMeta.Name will be subnetName + "-" + randomSuffix
			subnetCR.GenerateName = subnetCR.Name + "-"
			subnetCR.Name = ""

			err = r.Client.Create(ctx, subnetCR)
			if err != nil {
				return fmt.Errorf("failed to create Subnet CR with generateName: %w", err)
			}
		} else {
			log.Error(err, "Failed to create Subnet CR", "Namespace", subnetCR.Namespace, "Name", subnetCR.Name)
			return fmt.Errorf("failed to create Subnet CR: %w", err)
		}
	}

	return nil
}

// createSharedSubnetCR creates a Subnet CR for a shared subnet
func (r *NamespaceReconciler) createSharedSubnetCR(ctx context.Context, ns string, sharedSubnetPath string) error {
	// Extract the org id, project id, VPC id, and subnet id
	orgID, projectID, vpcID, subnetID, err := servicecommon.ExtractSubnetPath(sharedSubnetPath)
	if err != nil {
		return err
	}

	vpcFullID, err := servicecommon.GetVPCFullID(orgID, projectID, vpcID, r.VPCService)
	if err != nil {
		return err
	}

	// Get associated resource name
	associatedName, err := servicecommon.ConvertSubnetPathToAssociatedResource(sharedSubnetPath)
	if err != nil {
		return err
	}

	_, err = r.SubnetService.GetNSXSubnetFromCacheOrAPI(associatedName, false)
	if err != nil {
		return err
	}

	// Generate a valid Kubernetes name from the subnet ID
	// If subnet ID meets Kubernetes standards, use it; otherwise hash it
	subnetName := generateValidSubnetName(subnetID)

	// Create the Subnet CR object
	subnetCR := r.SubnetService.BuildSubnetCR(ns, subnetName, vpcFullID, associatedName)

	// Create the Subnet CR in Kubernetes
	err = r.createSubnetCRInK8s(ctx, subnetCR)
	if err != nil {
		return err
	}

	namespacedName := types.NamespacedName{
		Namespace: subnetCR.Namespace,
		Name:      subnetCR.Name,
	}
	r.SubnetService.AddSharedSubnetToResourceMap(associatedName, namespacedName)

	log.Info("Created Subnet CR for shared Subnet", "Namespace", ns, "Name", subnetCR.Name, "SharedSubnet", sharedSubnetPath)
	return nil
}

// getExistingSharedSubnetCRs gets a map of existing shared Subnet CRs in the namespace
func (r *NamespaceReconciler) getExistingSharedSubnetCRs(ctx context.Context, ns string) (map[string]*v1alpha1.Subnet, error) {
	// We met a bug in Client.List where it cannot list the latest CRs, thus leading to create duplicate Shared Subnet CRs.
	// To avoid this, we list all Subnet CRs directly from the APIReader instead of the cache.
	subnetList := &v1alpha1.SubnetList{}
	err := r.APIReader.List(ctx, subnetList, client.InNamespace(ns))
	if err != nil {
		return nil, fmt.Errorf("failed to list Subnet CRs: %w", err)
	}

	// Create a map of existing shared Subnet CRs
	existingSharedSubnets := make(map[string]*v1alpha1.Subnet)
	for i := range subnetList.Items {
		subnet := &subnetList.Items[i]
		if servicecommon.IsSharedSubnet(subnet) {
			value := subnet.Annotations[servicecommon.AnnotationAssociatedResource]
			if value != "" {
				existingSharedSubnets[value] = subnet
			}
		}
	}

	return existingSharedSubnets, nil
}

// processNewSharedSubnets creates Subnet CRs for new shared subnets
func (r *NamespaceReconciler) processNewSharedSubnets(ctx context.Context, ns string,
	vpcNetConfig *v1alpha1.VPCNetworkConfiguration, existingSharedSubnets map[string]*v1alpha1.Subnet) (map[string]*v1alpha1.Subnet, error) {

	unusedSubnets := make(map[string]*v1alpha1.Subnet)
	processedSubnets := make(map[string]bool)

	for _, sharedSubnetPath := range vpcNetConfig.Spec.Subnets {
		associatedResource, err := servicecommon.ConvertSubnetPathToAssociatedResource(sharedSubnetPath)
		if err != nil {
			log.Error(err, "Failed to convert Subnet path to associated resource", "Namespace", ns, "SharedSubnet", sharedSubnetPath)
			return unusedSubnets, err
		}

		if _, exists := existingSharedSubnets[associatedResource]; !exists {
			err := r.createSharedSubnetCR(ctx, ns, sharedSubnetPath)
			if err != nil {
				log.Error(err, "Failed to create Subnet CR for shared Subnet", "Namespace", ns, "SharedSubnet", sharedSubnetPath)
				return unusedSubnets, err
			}
		} else {
			// For existing shared subnets, we still need to add them to the SharedSubnetResourceMap
			existingSubnet := existingSharedSubnets[associatedResource]
			namespacedName := types.NamespacedName{
				Namespace: existingSubnet.Namespace,
				Name:      existingSubnet.Name,
			}
			r.SubnetService.AddSharedSubnetToResourceMap(associatedResource, namespacedName)
		}
		processedSubnets[associatedResource] = true
	}

	for k, v := range existingSharedSubnets {
		if !processedSubnets[k] {
			unusedSubnets[k] = v
		}
	}

	return unusedSubnets, nil
}

// checkSubnetReferences checks if a Subnet CR is referenced by any SubnetPort CRs, SubnetConnectionBindingMap CRs or SubnetIPReservation CRs
func (r *NamespaceReconciler) checkSubnetReferences(ctx context.Context, ns string, subnet *v1alpha1.Subnet) (bool, error) {
	// Check if there are any SubnetPort CRs referencing this Subnet CR
	subnetPortList := &v1alpha1.SubnetPortList{}
	err := r.Client.List(ctx, subnetPortList, client.InNamespace(ns), client.MatchingFields{"spec.subnet": subnet.Name})
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetPort CRs: %w", err)
	}

	if len(subnetPortList.Items) > 0 {
		log.Info("Cannot delete Subnet CR for shared subnet because it is referenced by a SubnetPort CR",
			"Namespace", ns, "Name", subnet.Name, "SubnetPort", subnetPortList.Items[0].Name)
		return true, nil
	}

	// Check if there are any SubnetConnectionBindingMap CRs referencing this Subnet CR
	subnetBindingList := &v1alpha1.SubnetConnectionBindingMapList{}
	err = r.Client.List(ctx, subnetBindingList, client.InNamespace(ns), client.MatchingFields{"spec.subnetName": subnet.Name})
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetConnectionBindingMap CRs: %w", err)
	}

	if len(subnetBindingList.Items) > 0 {
		log.Info("Cannot delete Subnet CR for shared subnet because it is referenced by a SubnetConnectionBindingMap CR",
			"Namespace", ns, "Name", subnet.Name, "SubnetBinding", subnetBindingList.Items[0].Name)
		return true, nil
	}

	// Check if there are any SubnetIPReservation CRs referencing this Subnet CR
	ipReservationList := &v1alpha1.SubnetIPReservationList{}
	err = r.Client.List(ctx, ipReservationList, client.InNamespace(ns), client.MatchingFields{"spec.subnet": subnet.Name})
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetIPReservation CRs: %w", err)
	}

	if len(ipReservationList.Items) > 0 {
		log.Info("Cannot delete Subnet CR for shared subnet because it is referenced by a SubnetIPReservation CR",
			"Namespace", ns, "Name", subnet.Name, "SubnetIPReservation", ipReservationList.Items[0].Name)
		return true, nil
	}

	return false, nil
}

// deleteUnusedSharedSubnets deletes Subnet CRs that are no longer needed
func (r *NamespaceReconciler) deleteUnusedSharedSubnets(ctx context.Context, ns string, unusedSubnets map[string]*v1alpha1.Subnet) error {
	var errs []error
	for associatedResource, subnet := range unusedSubnets {
		// Check if there are any references to this Subnet CR
		hasReferences, err := r.checkSubnetReferences(ctx, ns, subnet)
		if err != nil {
			log.Error(err, "Failed to check references for Subnet CR", "Namespace", ns, "Name", subnet.Name)
			return err
		}

		// If the Subnet CR is not referenced, delete it
		if !hasReferences {
			err = r.Client.Delete(ctx, subnet)
			if err != nil {
				log.Error(err, "Failed to delete Subnet CR for shared Subnet",
					"Namespace", ns, "Name", subnet.Name, "AssociatedResource", associatedResource)
				r.SubnetStatusUpdater.DeleteFail(client.ObjectKey{Namespace: ns, Name: subnet.Name}, subnet, err)
				return err
			} else {
				// Remove the subnet CR from the resource map
				namespacedName := types.NamespacedName{
					Namespace: subnet.Namespace,
					Name:      subnet.Name,
				}
				r.SubnetService.RemoveSharedSubnetFromResourceMap(associatedResource, namespacedName)

				// Clear the cache to prevent stale data when the subnet is recreated on NSX
				r.SubnetService.RemoveSubnetFromCache(associatedResource, "shared subnet CR deleted")

				log.Info("Deleted Subnet CR for shared Subnet",
					"Namespace", ns, "Name", subnet.Name, "AssociatedResource", associatedResource)
				r.SubnetStatusUpdater.DeleteSuccess(client.ObjectKey{Namespace: ns, Name: subnet.Name}, subnet)
			}
		} else {
			err := fmt.Errorf("subnet CR %s/%s is still referenced and cannot be deleted", subnet.Namespace, subnet.Name)
			log.Error(err, "Cannot delete Subnet CR for shared subnet because it is still referenced")
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("some subnets still have references and cannot be deleted, will retry later: %v", errs)
	}

	return nil
}

// syncSharedSubnets synchronizes the shared subnets in a VPCNetworkConfiguration CR with Subnets field
func (r *NamespaceReconciler) syncSharedSubnets(ctx context.Context, ns string, vpcNetConfig *v1alpha1.VPCNetworkConfiguration) error {
	// Get existing shared Subnet CRs
	existingSharedSubnets, err := r.getExistingSharedSubnetCRs(ctx, ns)
	if err != nil {
		return err
	}
	log.Trace("Existing shared Subnet CRs", "Namespace", ns, "Subnets", existingSharedSubnets)

	// Process new shared subnets and get remaining subnets that might need to be deleted
	unusedSubnets, err := r.processNewSharedSubnets(ctx, ns, vpcNetConfig, existingSharedSubnets)
	if err != nil {
		return err
	}
	log.Trace("Unused shared Subnet CRs", "Namespace", ns, "Subnets", unusedSubnets)

	// Delete unused Subnet CRs
	err = r.deleteUnusedSharedSubnets(ctx, ns, unusedSubnets)
	if err != nil {
		return err
	}

	return nil
}

// deleteAllSharedSubnets deletes all shared Subnet CRs in a namespace
func (r *NamespaceReconciler) deleteAllSharedSubnets(ctx context.Context, ns string) error {
	// Get existing shared Subnet CRs
	existingSharedSubnets, err := r.getExistingSharedSubnetCRs(ctx, ns)
	if err != nil {
		log.Error(err, "Failed to get existing shared Subnet CRs", "Namespace", ns)
		return err
	}

	// Delete all shared Subnet CRs
	err = r.deleteUnusedSharedSubnets(ctx, ns, existingSharedSubnets)
	if err != nil {
		log.Error(err, "Failed to delete shared Subnet CRs", "Namespace", ns)
		return err
	}

	log.Info("Deleted all shared Subnet CRs", "Namespace", ns)
	return nil
}
