package namespace

import (
	"context"
	"errors"
	"fmt"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// generateValidSubnetName creates a valid Kubernetes name from subnet ID
func generateValidSubnetName(subnetID string) string {
	if len(validation.IsDNS1123Subdomain(subnetID)) == 0 {
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
func (r *NamespaceReconciler) createSharedSubnetCR(ctx context.Context, ns string, sharedSubnetPath string, realName string, existingSharedSubnets map[string]*v1alpha1.Subnet) error {
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

	nsxSubnet, err := r.SubnetService.GetNSXSubnetFromCacheOrAPI(associatedName, false)
	if err != nil {
		return err
	}

	// Generate a valid Kubernetes name from the subnet ID
	// If subnet ID meets Kubernetes standards, use it; otherwise hash it
	if realName != "" {
		subnetID = realName
	}
	subnetName := generateValidSubnetName(subnetID)

	// Create the Subnet CR object with spec populated from NSX subnet
	subnetCR := r.SubnetService.BuildSubnetCR(ns, subnetName, vpcFullID, associatedName, nsxSubnet)

	// Create the Subnet CR in Kubernetes
	err = r.createSubnetCRInK8s(ctx, subnetCR)
	if err != nil {
		return err
	}
	// Update existingSharedSubnets as it will be used to check the mapping between
	// vm/pod default Subnet path and Subnet CR when update default SubnetSet with shared Subnets
	existingSharedSubnets[associatedName] = subnetCR
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

func (r *NamespaceReconciler) updateDefaultSubnetSetWithSubnets(name string, subnetSetType string, ns string, subnetNames []string) error {
	ctx := context.TODO()
	if err := retry.OnError(retry.DefaultRetry, func(err error) bool {
		return err != nil
	}, func() error {
		subnetSetCR, err := common.ListDefaultSubnetSet(ctx, r.Client, ns, subnetSetType)
		if err != nil {
			return err
		}
		if subnetSetCR != nil {
			if len(subnetNames) == 0 {
				// When there is no pre-created Subnets for this default network,
				// Delete the default SubnetSet if it is created with pre-created Subnets
				if subnetSetCR.Spec.SubnetNames != nil {
					log.Debug("Delete default SubnetSet", "Name", name, "Namespace", ns)
					err := r.Client.Delete(ctx, subnetSetCR)
					if err != nil {
						setSubnetSetCondition(ctx, r.Client, subnetSetCR, &v1alpha1.Condition{
							Type:    v1alpha1.UpdateFailure,
							Status:  v1.ConditionTrue,
							Reason:  "SubnetNamesUpdateFailure",
							Message: err.Error(),
						})
					}
					return err
				}
				// Do nothing if the default SubnetSet is created with auto-created Subnets
				log.Debug("Default SubnetSet with auto-created Subnets exists", "Name", name, "Namespace", ns)
				return nil
			}
			// If the existing SubnetSet is auto-created, it is not deleted by NetworkInfo controller yet, need to retry
			if subnetSetCR.Spec.SubnetNames == nil {
				return fmt.Errorf("SubnetSet %s/%s with shared Subnets cannot be created as the old default SubnetSet still exists, will retry later", ns, name)
			}
			// Update the default SubnetSet with the pre-created Subnets
			if !nsxutil.CompareArraysWithoutOrder(*subnetSetCR.Spec.SubnetNames, subnetNames) {
				subnetSetCR.Spec.SubnetNames = &subnetNames
				log.Debug("Update default SubnetSet with shared Subnets", "Name", name, "Namespace", ns, "subnetNames", subnetNames)
				err := r.Client.Update(ctx, subnetSetCR)
				if err != nil {
					setSubnetSetCondition(ctx, r.Client, subnetSetCR, &v1alpha1.Condition{
						Type:    v1alpha1.UpdateFailure,
						Status:  v1.ConditionTrue,
						Reason:  "SubnetNamesUpdateFailure",
						Message: err.Error(),
					})
					return err
				} else {
					// If update succeeds, check if it is needed to clear the previous update failure condition
					clearSubnetSetFailureCondition(ctx, r.Client, subnetSetCR)
				}
			} else {
				// If no update is needed any more, check if it is needed to clear the previous update failure condition
				clearSubnetSetFailureCondition(ctx, r.Client, subnetSetCR)
			}
			return nil
		}
		// Create the default SubnetSet with the pre-created Subnets
		if len(subnetNames) > 0 {
			obj := &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns,
					Name:      name,
					Labels: map[string]string{
						servicecommon.LabelDefaultNetwork: subnetSetType,
						// TODO: remove this old Label when all other dependencies consume the new label
						servicecommon.LabelDefaultSubnetSet: common.NetworkSubnetSetNameMap[subnetSetType],
					},
				},
				Spec: v1alpha1.SubnetSetSpec{
					SubnetNames: &subnetNames,
				},
			}
			return r.Client.Create(ctx, obj)
		}
		return nil
	}); err != nil {
		log.Error(err, "Failed to update SubnetSet with shared Subnets", "Namespace", ns, "Name", name)
		return err
	}
	return nil
}

func clearSubnetSetFailureCondition(ctx context.Context, kubeClient client.Client, subnetSet *v1alpha1.SubnetSet) {
	retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: subnetSet.Namespace, Name: subnetSet.Name}, subnetSet); err != nil {
			return err
		}
		updatedConditions := make([]v1alpha1.Condition, 0)
		updated := false
		for i := range subnetSet.Status.Conditions {
			cond := subnetSet.Status.Conditions[i]
			if cond.Type == v1alpha1.UpdateFailure {
				updated = true
			} else {
				updatedConditions = append(updatedConditions, cond)
			}
		}
		if updated {
			subnetSet.Status.Conditions = updatedConditions
			if err := kubeClient.Status().Update(ctx, subnetSet); err != nil {
				log.Error(err, "Failed to clear SubnetSet failure condition", "Namespace", subnetSet.Namespace, "SubnetSet", subnetSet.Name)
				return err
			}
			log.Info("Cleared SubnetSet failure condition", "Namespace", subnetSet.Namespace, "SubnetSet", subnetSet.Name)
		}
		return nil
	})
}

func setSubnetSetCondition(ctx context.Context, kubeClient client.Client, subnetSet *v1alpha1.SubnetSet, condition *v1alpha1.Condition) {
	updatedConditions := make([]v1alpha1.Condition, 0)
	existingConditions := subnetSet.Status.Conditions
	var extCondition *v1alpha1.Condition
	for i := range existingConditions {
		cond := subnetSet.Status.Conditions[i]
		if cond.Type == condition.Type {
			extCondition = &cond
		} else {
			updatedConditions = append(updatedConditions, cond)
		}
	}
	// Return if the failure (reason/message) is already added on SubnetSet condition.
	if extCondition != nil && subnetSetConditionEquals(*extCondition, *condition) {
		return
	}
	updatedConditions = append(updatedConditions, v1alpha1.Condition{
		Type:               condition.Type,
		Status:             condition.Status,
		Reason:             condition.Reason,
		Message:            condition.Message,
		LastTransitionTime: metav1.Now(),
	})
	subnetSet.Status.Conditions = updatedConditions
	if err := kubeClient.Status().Update(ctx, subnetSet, &client.SubResourceUpdateOptions{}); err != nil {
		log.Error(err, "Failed to update SubnetSet status", "Namespace", subnetSet.Namespace, "SubnetSet", subnetSet.Name)
		return
	}
	log.Info("Updated SubnetSet condition", "Namespace", subnetSet.Namespace, "SubnetSet", subnetSet.Name, "status", condition.Status, "reason", condition.Reason, "message", condition.Message)
}

func subnetSetConditionEquals(old, new v1alpha1.Condition) bool {
	return old.Type == new.Type && old.Status == new.Status &&
		old.Reason == new.Reason && old.Message == new.Message
}

func (r *NamespaceReconciler) updateDefaultSubnetSetWithSpecifiedSubnets(sharedSubnets []v1alpha1.SharedSubnet, existingSharedSubnets map[string]*v1alpha1.Subnet, ns string) error {
	var podDefaultSubnets, vmDefaultSubnets []string
	for _, sharedSubnet := range sharedSubnets {
		associatedResource, err := servicecommon.ConvertSubnetPathToAssociatedResource(sharedSubnet.Path)
		if err != nil {
			log.Error(err, "Failed to convert Subnet path to associated resource", "SharedSubnet", sharedSubnet.Path)
			return err
		}
		sharedSubnetCR, ok := existingSharedSubnets[associatedResource]
		if !ok {
			err := fmt.Errorf("failed to create default Network SubnetSet with Subnets: shared Subnet CR for %s is not created", sharedSubnet.Path)
			log.Error(err, "Shared Subnet CR should be created before updating Default SubnetSet")
			return err
		}
		if sharedSubnet.PodDefault {
			podDefaultSubnets = append(podDefaultSubnets, sharedSubnetCR.Name)
		}
		if sharedSubnet.VMDefault {
			vmDefaultSubnets = append(vmDefaultSubnets, sharedSubnetCR.Name)
		}
	}
	// create or update subnetset
	var errList []error
	if err := r.updateDefaultSubnetSetWithSubnets(servicecommon.DefaultPodSubnetSet, servicecommon.DefaultPodNetwork, ns, podDefaultSubnets); err != nil {
		errList = append(errList, err)
	}
	if err := r.updateDefaultSubnetSetWithSubnets(servicecommon.DefaultVMSubnetSet, servicecommon.DefaultVMNetwork, ns, vmDefaultSubnets); err != nil {
		errList = append(errList, err)
	}
	if len(errList) > 0 {
		return errors.Join(errList...)
	}
	return nil
}

// processNewSharedSubnets creates Subnet CRs for new shared subnets
func (r *NamespaceReconciler) processNewSharedSubnets(ctx context.Context, ns string,
	vpcNetConfig *v1alpha1.VPCNetworkConfiguration, existingSharedSubnets map[string]*v1alpha1.Subnet) (map[string]*v1alpha1.Subnet, error) {

	unusedSubnets := make(map[string]*v1alpha1.Subnet)
	processedSubnets := make(map[string]bool)

	for _, sharedSubnet := range vpcNetConfig.Spec.Subnets {
		associatedResource, err := servicecommon.ConvertSubnetPathToAssociatedResource(sharedSubnet.Path)
		if err != nil {
			log.Error(err, "Failed to convert Subnet path to associated resource", "Namespace", ns, "SharedSubnet", sharedSubnet.Path)
			return unusedSubnets, err
		}

		if _, exists := existingSharedSubnets[associatedResource]; !exists {
			err := r.createSharedSubnetCR(ctx, ns, sharedSubnet.Path, sharedSubnet.Name, existingSharedSubnets)
			if err != nil {
				log.Error(err, "Failed to create Subnet CR for shared Subnet", "Namespace", ns, "SharedSubnet", sharedSubnet.Path)
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
				subnetPath, err := servicecommon.GetSubnetPathFromAssociatedResource(associatedResource)
				if err != nil {
					log.Error(err, "Invalid associatedResource", "associatedResource", associatedResource)
					return err
				} else {
					r.SubnetPortService.DeletePortCount(subnetPath)
				}
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

	// Update default SubnetSet based on shared Subnets
	err = r.updateDefaultSubnetSetWithSpecifiedSubnets(vpcNetConfig.Spec.Subnets, existingSharedSubnets, ns)
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
