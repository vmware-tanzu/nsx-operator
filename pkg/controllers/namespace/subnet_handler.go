package namespace

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// extractSubnetPath extracts the subnet name, VPC name, and project name from a subnet path
func (r *NamespaceReconciler) extractSubnetPath(sharedSubnetPath string) (subnetName, projectName, vpcName string, err error) {
	// Format: /orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1
	re := regexp.MustCompile(`/projects/([^/]+)/vpcs/([^/]+)/subnets/([^/]+)`)
	matches := re.FindStringSubmatch(sharedSubnetPath)
	if len(matches) < 4 {
		return "", "", "", fmt.Errorf("invalid subnet path format: %s", sharedSubnetPath)
	}

	projectName = matches[1]
	vpcName = matches[2]
	subnetName = matches[3]
	return subnetName, projectName, vpcName, nil
}

// convertSubnetPathToAssociatedResource converts a subnet path to the associated resource format
// e.g., /orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1 -> proj-1:vpc-1:subnet-1
func (r *NamespaceReconciler) convertSubnetPathToAssociatedResource(sharedSubnetPath string) (string, error) {
	// Extract the subnet name, VPC name and project name using extractSubnetPath
	subnetName, projectName, vpcName, err := r.extractSubnetPath(sharedSubnetPath)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s:%s:%s", projectName, vpcName, subnetName), nil
}

// getNSXSubnet retrieves the NSX subnet from the NSX API
func (r *NamespaceReconciler) getNSXSubnet(ns string, sharedSubnetPath string) (*model.VpcSubnet, error) {
	vpcInfoList := r.VPCService.ListVPCInfo(ns)
	if len(vpcInfoList) == 0 {
		return nil, fmt.Errorf("no VPC info found for namespace: %s", ns)
	}

	vpcInfo := vpcInfoList[0]
	nsxSubnets, err := r.CommonService.NSXClient.SubnetsClient.List(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, nil, nil, nil, nil, nil, nil)
	if err != nil {
		log.Error(err, "Failed to list nsx subnets")
		return nil, err
	}

	for _, subnet := range nsxSubnets.Results {
		if *subnet.Path == sharedSubnetPath {
			return &subnet, nil
		}
	}

	return nil, nil
}

// buildSubnetCR creates a Subnet CR object with the given parameters
func (r *NamespaceReconciler) buildSubnetCR(ns, subnetName, vpcFullName, associatedName string, nsxSubnet *model.VpcSubnet) *v1alpha1.Subnet {
	// Create the Subnet CR
	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      subnetName,
			Namespace: ns,
			Annotations: map[string]string{
				servicecommon.AnnotationAssociatedResource: associatedName,
			},
		},
		Spec: v1alpha1.SubnetSpec{
			VPCName:             vpcFullName,
			EnableVLANExtension: true,
		},
	}

	// Initialize subnetCR from nsxSubnet if available
	if nsxSubnet != nil {
		r.mapNSXSubnetToSubnetCR(subnetCR, nsxSubnet)
	} else {
		// Use default values if nsxSubnet is not available
		subnetCR.Spec.AccessMode = v1alpha1.AccessMode(v1alpha1.AccessModePublic)
		subnetCR.Spec.IPv4SubnetSize = 64
		subnetCR.Spec.SubnetDHCPConfig.Mode = v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)
	}

	return subnetCR
}

// mapNSXSubnetToSubnetCR maps NSX subnet properties to Subnet CR properties
func (r *NamespaceReconciler) mapNSXSubnetToSubnetCR(subnetCR *v1alpha1.Subnet, nsxSubnet *model.VpcSubnet) {
	// Map AccessMode
	if nsxSubnet.AccessMode != nil {
		accessMode := *nsxSubnet.AccessMode
		// Convert from NSX format to v1alpha1 format
		if accessMode == "Private_TGW" {
			subnetCR.Spec.AccessMode = v1alpha1.AccessMode(v1alpha1.AccessModeProject)
		} else {
			subnetCR.Spec.AccessMode = v1alpha1.AccessMode(accessMode)
		}
	} else {
		subnetCR.Spec.AccessMode = v1alpha1.AccessMode(v1alpha1.AccessModePublic)
	}

	// Map IPv4SubnetSize
	if nsxSubnet.Ipv4SubnetSize != nil {
		subnetCR.Spec.IPv4SubnetSize = int(*nsxSubnet.Ipv4SubnetSize)
	} else {
		subnetCR.Spec.IPv4SubnetSize = 64
	}

	// Map IPAddresses
	subnetCR.Spec.IPAddresses = nsxSubnet.IpAddresses

	// Map SubnetDHCPConfig
	if nsxSubnet.SubnetDhcpConfig != nil && nsxSubnet.SubnetDhcpConfig.Mode != nil {
		dhcpMode := *nsxSubnet.SubnetDhcpConfig.Mode
		// Convert from NSX format to v1alpha1 format
		switch dhcpMode {
		case "DHCP_SERVER":
			subnetCR.Spec.SubnetDHCPConfig.Mode = v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer)
		case "DHCP_RELAY":
			subnetCR.Spec.SubnetDHCPConfig.Mode = v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeRelay)
		default:
			subnetCR.Spec.SubnetDHCPConfig.Mode = v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)
		}
	} else {
		subnetCR.Spec.SubnetDHCPConfig.Mode = v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)
	}
}

// createSubnetCRInK8s creates the Subnet CR in Kubernetes
func (r *NamespaceReconciler) createSubnetCRInK8s(ctx context.Context, subnetCR *v1alpha1.Subnet, subnetName string) error {
	err := r.Client.Create(ctx, subnetCR)
	if err != nil {
		// If the Subnet CR already exists with the same name, try using generateName
		if strings.Contains(err.Error(), "already exists") {
			log.Info("Subnet CR with the same name already exists, using generateName",
				"Namespace", subnetCR.Namespace, "Name", subnetName)

			// Create a new Subnet CR with generateName
			// subnetCR.ObjectMeta.Name will be subnetName + "-" + randomSuffix
			subnetCR.ObjectMeta.GenerateName = subnetName + "-"

			err = r.Client.Create(ctx, subnetCR)
			if err != nil {
				return fmt.Errorf("failed to create Subnet CR with generateName: %w", err)
			}
		} else {
			return fmt.Errorf("failed to create Subnet CR: %w", err)
		}
	}

	return nil
}

// createSharedSubnetCR creates a Subnet CR for a shared subnet
func (r *NamespaceReconciler) createSharedSubnetCR(ctx context.Context, ns string, sharedSubnetPath string) error {
	// Extract the subnet name, VPC name and project name
	subnetName, projectName, vpcName, err := r.extractSubnetPath(sharedSubnetPath)
	if err != nil {
		return err
	}

	// Format VPC full name
	vpcFullName := fmt.Sprintf("%s:%s", projectName, vpcName)
	if projectName == "default" {
		vpcFullName = fmt.Sprintf(":%s", vpcName)
	}

	// Get associated resource name
	associatedName, err := r.convertSubnetPathToAssociatedResource(sharedSubnetPath)
	if err != nil {
		return err
	}

	// Get subnet from NSX
	nsxSubnet, err := r.getNSXSubnet(ns, sharedSubnetPath)
	if err != nil {
		return err
	}

	// Create the Subnet CR object
	subnetCR := r.buildSubnetCR(ns, subnetName, vpcFullName, associatedName, nsxSubnet)

	// Create the Subnet CR in Kubernetes
	err = r.createSubnetCRInK8s(ctx, subnetCR, subnetName)
	if err != nil {
		return err
	}

	log.Info("Created Subnet CR for shared subnet", "Namespace", ns, "Name", subnetCR.Name, "SharedSubnet", sharedSubnetPath)
	return nil
}

// getExistingSharedSubnets gets a map of existing shared Subnet CRs in the namespace
func (r *NamespaceReconciler) getExistingSharedSubnets(ctx context.Context, ns string) (map[string]*v1alpha1.Subnet, error) {
	// Get the current list of Subnet CRs in the namespace
	subnetList := &v1alpha1.SubnetList{}
	err := r.Client.List(ctx, subnetList, client.InNamespace(ns))
	if err != nil {
		return nil, fmt.Errorf("failed to list Subnet CRs: %w", err)
	}

	// Create a map of existing shared Subnet CRs
	existingSharedSubnets := make(map[string]*v1alpha1.Subnet)
	for i := range subnetList.Items {
		subnet := &subnetList.Items[i]
		if subnet.Annotations != nil && subnet.Annotations[servicecommon.AnnotationAssociatedResource] != "" {
			existingSharedSubnets[subnet.Annotations[servicecommon.AnnotationAssociatedResource]] = subnet
		}
	}

	return existingSharedSubnets, nil
}

// processNewSharedSubnets creates Subnet CRs for new shared subnets
func (r *NamespaceReconciler) processNewSharedSubnets(ctx context.Context, ns string,
	vpcNetConfig *v1alpha1.VPCNetworkConfiguration, existingSharedSubnets map[string]*v1alpha1.Subnet) map[string]*v1alpha1.Subnet {

	unusedSubnets := make(map[string]*v1alpha1.Subnet)
	for k, v := range existingSharedSubnets {
		unusedSubnets[k] = v
	}

	for _, sharedSubnetPath := range vpcNetConfig.Spec.Subnets {
		associatedResource, err := r.convertSubnetPathToAssociatedResource(sharedSubnetPath)
		if err != nil {
			log.Error(err, "Failed to convert subnet path to associated resource", "Namespace", ns, "SharedSubnet", sharedSubnetPath)
			continue
		}

		if _, exists := existingSharedSubnets[associatedResource]; !exists {
			err := r.createSharedSubnetCR(ctx, ns, sharedSubnetPath)
			if err != nil {
				log.Error(err, "Failed to create Subnet CR for shared subnet", "Namespace", ns, "SharedSubnet", sharedSubnetPath)
				// Continue with the next shared subnet
			}
		}
		// Remove from the map to track which ones need to be deleted
		delete(unusedSubnets, associatedResource)
	}

	return unusedSubnets
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
	for associatedResource, subnet := range remainingSubnets {
		// Check if there are any references to this Subnet CR
		hasReferences, err := r.checkSubnetReferences(ctx, ns, subnet, associatedResource)
		if err != nil {
			log.Error(err, "Failed to check references for Subnet CR", "Namespace", ns, "Name", subnet.Name)
			continue
		}

		// If the Subnet CR is not referenced, delete it
		if !hasReferences {
			err = r.Client.Delete(ctx, subnet)
			if err != nil {
				log.Error(err, "Failed to delete Subnet CR for shared subnet",
					"Namespace", ns, "Name", subnet.Name, "AssociatedResource", associatedResource)
			} else {
				log.Info("Deleted Subnet CR for shared subnet",
					"Namespace", ns, "Name", subnet.Name, "AssociatedResource", associatedResource)
			}
		}
	}

	return nil
}

// syncSharedSubnets synchronizes the shared subnets in a VPCNetworkConfiguration CR with Subnets field
func (r *NamespaceReconciler) syncSharedSubnets(ctx context.Context, ns string, vpcNetConfig *v1alpha1.VPCNetworkConfiguration) error {
	// Get existing shared Subnet CRs
	existingSharedSubnets, err := r.getExistingSharedSubnets(ctx, ns)
	if err != nil {
		return err
	}

	// Process new shared subnets and get remaining subnets that might need to be deleted
	unusedSubnets := r.processNewSharedSubnets(ctx, ns, vpcNetConfig, existingSharedSubnets)

	// Delete unused Subnet CRs
	err = r.deleteUnusedSharedSubnets(ctx, ns, unusedSubnets)
	if err != nil {
		return err
	}

	return nil
}
