package namespace

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// createSubnetCRInK8s creates the Subnet CR in Kubernetes
func (r *NamespaceReconciler) createSubnetCRInK8s(ctx context.Context, subnetCR *v1alpha1.Subnet, subnetName string) error {
	err := r.Client.Create(ctx, subnetCR)
	if err != nil {
		// If the Subnet CR already exists with the same name, try using generateName
		if apierrors.IsAlreadyExists(err) {
			log.Info("Subnet CR with the same name already exists, using generateName",
				"Namespace", subnetCR.Namespace, "Name", subnetName)

			// Create a new Subnet CR with generateName
			// subnetCR.ObjectMeta.Name will be subnetName + "-" + randomSuffix
			subnetCR.ObjectMeta.Name = ""
			subnetCR.ObjectMeta.GenerateName = subnetName + "-"

			err = r.Client.Create(ctx, subnetCR)
			if err != nil {
				return fmt.Errorf("failed to create Subnet CR with generateName: %w", err)
			}
		} else {
			log.Error(err, "Failed to create Subnet CR", "Namespace", subnetCR.Namespace, "Name", subnetName)
			return fmt.Errorf("failed to create Subnet CR: %w", err)
		}
	}

	return nil
}

// createSharedSubnetCR creates a Subnet CR for a shared subnet
func (r *NamespaceReconciler) createSharedSubnetCR(ctx context.Context, ns string, sharedSubnetPath string) error {
	// Extract the org id, project id, VPC id, and subnet id
	orgID, projectID, vpcID, subnetID, err := common.ExtractSubnetPath(sharedSubnetPath)
	if err != nil {
		return err
	}

	projectName, err := r.VPCService.GetProjectName(orgID, projectID)
	if err != nil {
		return fmt.Errorf("failed to get project name: %w", err)
	}

	vpcName, err := r.VPCService.GetVPCName(orgID, projectID, vpcID)
	if err != nil {
		return fmt.Errorf("failed to get VPC name: %w", err)
	}

	// Format VPC full name
	vpcFullName := fmt.Sprintf("%s:%s", projectName, vpcName)
	if isDefault, err := r.SubnetService.IsDefaultNSXProject(orgID, projectID); err != nil {
		return fmt.Errorf("failed to check if project is default: %w", err)
	} else if isDefault {
		vpcFullName = fmt.Sprintf(":%s", vpcName)
	}

	// Get associated resource name
	associatedName, err := common.ConvertSubnetPathToAssociatedResource(sharedSubnetPath)
	if err != nil {
		return err
	}

	// Get subnet from NSX
	nsxSubnet, err := r.SubnetService.GetNSXSubnetByAssociatedResource(associatedName)
	if err != nil {
		return err
	}

	// Create the Subnet CR object
	subnetCR := r.SubnetService.BuildSubnetCR(ns, subnetID, vpcFullName, associatedName, nsxSubnet)

	// Create the Subnet CR in Kubernetes
	err = r.createSubnetCRInK8s(ctx, subnetCR, subnetID)
	if err != nil {
		return err
	}

	log.Info("Created Subnet CR for shared Subnet", "Namespace", ns, "Name", subnetCR.Name, "SharedSubnet", sharedSubnetPath)
	return nil
}

// getExistingSharedSubnetCRs gets a map of existing shared Subnet CRs in the namespace
func (r *NamespaceReconciler) getExistingSharedSubnetCRs(ctx context.Context, ns string) (map[string]*v1alpha1.Subnet, error) {
	// Get the current list of Subnet CRs in the namespace with label selector
	subnetList := &v1alpha1.SubnetList{}
	labelSelector := client.HasLabels{servicecommon.LabelAssociatedResource}
	err := r.Client.List(ctx, subnetList, client.InNamespace(ns), labelSelector)
	if err != nil {
		return nil, fmt.Errorf("failed to list Subnet CRs: %w", err)
	}

	// Create a map of existing shared Subnet CRs
	existingSharedSubnets := make(map[string]*v1alpha1.Subnet)
	for i := range subnetList.Items {
		subnet := &subnetList.Items[i]
		// The label selector ensures the label exists, but we still check for non-empty value
		if labelValue := subnet.Labels[servicecommon.LabelAssociatedResource]; labelValue != "" {
			existingSharedSubnets[labelValue] = subnet
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
		associatedResource, err := common.ConvertSubnetPathToAssociatedResource(sharedSubnetPath)
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
func (r *NamespaceReconciler) checkSubnetReferences(ctx context.Context, ns string, subnet *v1alpha1.Subnet, associatedResource string) (bool, error) {
	// Check if there are any SubnetPort CRs referencing this Subnet CR
	subnetPortList := &v1alpha1.SubnetPortList{}
	err := r.Client.List(ctx, subnetPortList, client.InNamespace(ns))
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetPort CRs: %w", err)
	}

	for _, subnetPort := range subnetPortList.Items {
		if subnetPort.Spec.Subnet == subnet.Name {
			log.Info("Cannot delete Subnet CR for shared subnet because it is referenced by a SubnetPort CR",
				"Namespace", ns, "Name", subnet.Name, "AssociatedResource", associatedResource, "SubnetPort", subnetPort.Name)
			return true, nil
		}
	}

	// Check if there are any SubnetConnectionBindingMap CRs referencing this Subnet CR
	subnetBindingList := &v1alpha1.SubnetConnectionBindingMapList{}
	err = r.Client.List(ctx, subnetBindingList, client.InNamespace(ns))
	if err != nil {
		return false, fmt.Errorf("failed to list SubnetConnectionBindingMap CRs: %w", err)
	}

	for _, subnetBinding := range subnetBindingList.Items {
		if subnetBinding.Spec.TargetSubnetName == subnet.Name {
			log.Info("Cannot delete Subnet CR for shared subnet because it is referenced by a SubnetConnectionBindingMap CR",
				"Namespace", ns, "Name", subnet.Name, "AssociatedResource", associatedResource, "SubnetBinding", subnetBinding.Name)
			return true, nil
		}
	}

	return false, nil
}

// deleteUnusedSharedSubnets deletes Subnet CRs that are no longer needed
func (r *NamespaceReconciler) deleteUnusedSharedSubnets(ctx context.Context, ns string, remainingSubnets map[string]*v1alpha1.Subnet) error {
	var errs []error
	for associatedResource, subnet := range remainingSubnets {
		// Check if there are any references to this Subnet CR
		hasReferences, err := r.checkSubnetReferences(ctx, ns, subnet, associatedResource)
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
