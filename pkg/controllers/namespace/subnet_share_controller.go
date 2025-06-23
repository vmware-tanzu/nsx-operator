package namespace

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

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
	orgID, projectID, vpcID, _, err := servicecommon.ExtractSubnetPath(sharedSubnetPath)
	if err != nil {
		return err
	}

	vpcFullName, err := servicecommon.GetVPCFullName(orgID, projectID, vpcID, r.VPCService)
	if err != nil {
		return err
	}

	// Get associated resource name
	associatedName, err := servicecommon.ConvertSubnetPathToAssociatedResource(sharedSubnetPath)
	if err != nil {
		return err
	}

	nsxSubnet, err := r.SubnetService.GetNSXSubnetFromCacheOrAPI(associatedName)
	if err != nil {
		return err
	}

	// Get the subnet name from the NSX subnet
	if nsxSubnet.DisplayName == nil {
		log.Error(err, "Failed to get subnet name from NSX subnet", "Namespace", ns, "Name", associatedName)
		return fmt.Errorf("failed to get subnet name from NSX subnet: %w", err)
	}
	subnetName := *nsxSubnet.DisplayName

	// Create the Subnet CR object
	subnetCR := r.SubnetService.BuildSubnetCR(ns, subnetName, vpcFullName, associatedName)

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
	// Get the list of Subnet CRs in the namespace that have the associated-resource annotation
	subnetList := &v1alpha1.SubnetList{}
	err := r.Client.List(ctx, subnetList,
		client.InNamespace(ns),
		client.MatchingFields{
			servicecommon.AssociatedResourceIndexKey: "true",
		})
	if err != nil {
		return nil, fmt.Errorf("failed to list Subnet CRs: %w", err)
	}

	// Create a map of existing shared Subnet CRs
	existingSharedSubnets := make(map[string]*v1alpha1.Subnet)
	for i := range subnetList.Items {
		subnet := &subnetList.Items[i]
		if subnet.Annotations[servicecommon.AnnotationAssociatedResource] != "" {
			existingSharedSubnets[subnet.Annotations[servicecommon.AnnotationAssociatedResource]] = subnet
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

// checkSubnetReferences checks if a Subnet CR is referenced by any SubnetPort CRs or SubnetConnectionBindingMap CRs
func (r *NamespaceReconciler) checkSubnetReferences(ctx context.Context, ns string, subnet *v1alpha1.Subnet) (bool, error) {
	// Check if there are any SubnetPort CRs referencing this Subnet CR
	subnetPortList := &v1alpha1.SubnetPortList{}
	option := metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.subnet", subnet.Name).String(),
	}
	err := r.Client.List(ctx, subnetPortList, client.InNamespace(ns), &client.ListOptions{Raw: &option})
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
	option = metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.subnetName", subnet.Name).String(),
	}
	err = r.Client.List(ctx, subnetBindingList, client.InNamespace(ns), &client.ListOptions{Raw: &option})
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetConnectionBindingMap CRs: %w", err)
	}

	if len(subnetBindingList.Items) > 0 {
		log.Info("Cannot delete Subnet CR for shared subnet because it is referenced by a SubnetConnectionBindingMap CR",
			"Namespace", ns, "Name", subnet.Name, "SubnetBinding", subnetBindingList.Items[0].Name)
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

	// Process new shared subnets and get remaining subnets that might need to be deleted
	unusedSubnets, err := r.processNewSharedSubnets(ctx, ns, vpcNetConfig, existingSharedSubnets)
	if err != nil {
		return err
	}

	// Delete unused Subnet CRs
	err = r.deleteUnusedSharedSubnets(ctx, ns, unusedSubnets)
	if err != nil {
		return err
	}

	return nil
}
