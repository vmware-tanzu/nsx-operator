package subnet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                = &logger.Log
	MarkedForDelete    = true
	ResourceTypeSubnet = common.ResourceTypeSubnet
	SubnetTypeError    = errors.New("unsupported type")
)

// SharedSubnetData contains data related to shared subnets
type SharedSubnetData struct {
	// NSXSubnetCache is a cache of associatedResource -> nsxSubnet and statusList mapping, only for pre-created shared subnets currently
	NSXSubnetCache map[string]struct {
		Subnet     *model.VpcSubnet
		StatusList []model.VpcSubnetStatus
	}
	// mutex to protect the NSXSubnetCache map
	nsxSubnetCacheMutex sync.RWMutex
	// SharedSubnetResourceMap is a map of associatedResource -> set of namespaced names of Subnet CRs
	SharedSubnetResourceMap map[string]sets.Set[types.NamespacedName]
	// mutex to protect the SharedSubnetResourceMap
	sharedSubnetResourceMapMutex sync.RWMutex
}

type SubnetService struct {
	common.Service
	SubnetStore *SubnetStore
	builder     *common.PolicyTreeBuilder[*model.VpcSubnet]
	// SharedSubnetData contains data related to shared subnets
	SharedSubnetData
}

// SubnetParameters stores parameters to CRUD Subnet object
type SubnetParameters struct {
	OrgID     string
	ProjectID string
	VPCID     string
}

// InitializeSubnetService initialize Subnet service.
func InitializeSubnetService(service common.Service) (*SubnetService, error) {
	builder, _ := common.PolicyPathVpcSubnet.NewPolicyTreeBuilder()

	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)
	subnetService := &SubnetService{
		Service:     service,
		SubnetStore: buildSubnetStore(),
		builder:     builder,
		SharedSubnetData: SharedSubnetData{
			NSXSubnetCache: make(map[string]struct {
				Subnet     *model.VpcSubnet
				StatusList []model.VpcSubnetStatus
			}),
			SharedSubnetResourceMap: make(map[string]sets.Set[types.NamespacedName]),
		},
	}

	wg.Add(1)
	go subnetService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeSubnet, nil, subnetService.SubnetStore)
	go func() {
		wg.Wait()
		close(wgDone)
	}()
	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return subnetService, err
	}
	return subnetService, nil
}

func (service *SubnetService) RestoreSubnetSet(obj *v1alpha1.SubnetSet, vpcInfo common.VPCResourceInfo, tags []model.Tag) error {
	nsxSubnets := service.SubnetStore.GetByIndex(common.TagScopeSubnetSetCRUID, string(obj.UID))
	var errList []error
	for _, subnetInfo := range obj.Status.Subnets {
		nsxSubnet, err := service.buildSubnet(obj, tags, subnetInfo.NetworkAddresses)
		if err != nil {
			log.Error(err, "Failed to build Subnet", "subnetInfo", subnetInfo)
			return err
		}
		// If the Subnet with the same CIDR existed in the cache, check if it is updated
		// If the existing Subnet is not updated, skip the API call
		changed := true
		for _, existingSubnet := range nsxSubnets {
			if nsxutil.CompareArraysWithoutOrder(existingSubnet.IpAddresses, subnetInfo.NetworkAddresses) {
				if common.CompareResource(SubnetToComparable(existingSubnet), SubnetToComparable(nsxSubnet)) {
					updatedSubnet := *existingSubnet
					updatedSubnet.Tags = nsxSubnet.Tags
					updatedSubnet.SubnetDhcpConfig = nsxSubnet.SubnetDhcpConfig
					nsxSubnet = &updatedSubnet
				} else {
					changed = false
				}
				break
			}
		}
		if changed {
			_, err = service.createOrUpdateSubnet(obj, nsxSubnet, &vpcInfo)
			if err != nil {
				errList = append(errList, err)
			}
		}
	}
	if len(errList) > 0 {
		return errors.Join(errList...)
	}
	return nil
}

func (service *SubnetService) CreateOrUpdateSubnet(obj client.Object, vpcInfo common.VPCResourceInfo, tags []model.Tag) (subnet *model.VpcSubnet, err error) {
	uid := string(obj.GetUID())
	nsxSubnet, err := service.buildSubnet(obj, tags, []string{})
	if err != nil {
		log.Error(err, "Failed to build Subnet")
		return nil, err
	}
	// Only check whether it needs update when obj is v1alpha1.Subnet
	if subnet, ok := obj.(*v1alpha1.Subnet); ok {
		var existingSubnet *model.VpcSubnet
		existingSubnets := service.SubnetStore.GetByIndex(common.TagScopeSubnetCRUID, string(subnet.GetUID()))
		changed := false
		if len(existingSubnets) == 0 {
			changed = true
		} else {
			existingSubnet = existingSubnets[0]
			// Reset with the existing NSX VpcSubnet's id and display_name to keep consistent.
			nsxSubnet.Id = common.String(*existingSubnet.Id)
			nsxSubnet.DisplayName = common.String(*existingSubnet.DisplayName)

			changed = common.CompareResource(SubnetToComparable(existingSubnet), SubnetToComparable(nsxSubnet))
			if changed {
				// Only tags and dhcp are expected to be updated
				// inherit other fields from the existing Subnet
				// Avoid modification on existingSubnet to ensure
				// Subnet store is only updated after the updating succeeds.
				updatedSubnet := *existingSubnet
				updatedSubnet.Tags = nsxSubnet.Tags
				updatedSubnet.SubnetDhcpConfig = nsxSubnet.SubnetDhcpConfig
				nsxSubnet = &updatedSubnet
			}
		}
		if !changed {
			log.Info("Subnet not changed, skip updating", "SubnetId", uid)
			return existingSubnet, nil
		}
	}
	return service.createOrUpdateSubnet(obj, nsxSubnet, &vpcInfo)
}

func (service *SubnetService) createOrUpdateSubnet(obj client.Object, nsxSubnet *model.VpcSubnet, vpcInfo *common.VPCResourceInfo) (*model.VpcSubnet, error) {
	err := service.NSXClient.SubnetsClient.Patch(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, *nsxSubnet.Id, *nsxSubnet)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to create or update nsxSubnet", "ID", *nsxSubnet.Id)
		return nil, err
	}

	// Get Subnet from NSX after patch operation as NSX renders several fields like `path`/`parent_path`.
	if *nsxSubnet, err = service.NSXClient.SubnetsClient.Get(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, *nsxSubnet.Id); err != nil {
		err = nsxutil.TransNSXApiError(err)
		return nil, err
	}
	realizeService := realizestate.InitializeRealizeState(service.Service)
	// Failure of CheckRealizeState may result in the creation of an existing Subnet.
	// For Subnets, it's important to reuse the already created NSXSubnet.
	// For SubnetSets, since the ID includes a random value, the created NSX Subnet needs to be deleted and recreated.
	if err = realizeService.CheckRealizeState(util.NSXTRealizeRetry, *nsxSubnet.Path, []string{}); err != nil {
		log.Error(err, "Failed to check Subnet realization state", "ID", *nsxSubnet.Id)
		// Delete the subnet if the realization check fails, avoiding creating duplicate subnets continuously.
		deleteErr := service.DeleteSubnet(*nsxSubnet)
		if deleteErr != nil {
			log.Error(deleteErr, "Failed to delete Subnet after realization check failure", "ID", *nsxSubnet.Id)
			return nil, fmt.Errorf("realization check failed: %v; deletion failed: %v", err, deleteErr)
		}
		return nil, err
	}
	if err = service.SubnetStore.Apply(nsxSubnet); err != nil {
		log.Error(err, "Failed to add nsxSubnet to store", "ID", *nsxSubnet.Id)
		return nil, err
	}
	if subnetSet, ok := obj.(*v1alpha1.SubnetSet); ok {
		if err = service.UpdateSubnetSetStatus(subnetSet); err != nil {
			return nil, err
		}
	}
	log.Info("Successfully created or updated nsxSubnet", "nsxSubnet", nsxSubnet)
	return nsxSubnet, nil
}

func (service *SubnetService) DeleteSubnet(nsxSubnet model.VpcSubnet) error {
	subnetInfo, _ := common.ParseVPCResourcePath(*nsxSubnet.Path)
	nsxSubnet.MarkedForDelete = &MarkedForDelete
	err := service.NSXClient.SubnetsClient.Delete(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		// GC will finally delete subnets that are not deleted successfully.
		log.Error(err, "Failed to delete nsxSubnet", "ID", *nsxSubnet.Id)
		return err
	}

	if err = service.SubnetStore.Apply(&nsxSubnet); err != nil {
		log.Error(err, "Failed to delete nsxSubnet from store", "ID", *nsxSubnet.Id)
		return err
	}
	log.Info("Successfully deleted nsxSubnet", "nsxSubnet", nsxSubnet)
	return nil
}

func (service *SubnetService) ListSubnetCreatedBySubnet(id string) []*model.VpcSubnet {
	return service.SubnetStore.GetByIndex(common.TagScopeSubnetCRUID, id)
}

func (service *SubnetService) ListSubnetCreatedBySubnetSet(id string) []*model.VpcSubnet {
	return service.SubnetStore.GetByIndex(common.TagScopeSubnetSetCRUID, id)
}

func (service *SubnetService) ListSubnetByName(ns, name string) []*model.VpcSubnet {
	nsxSubnets := service.SubnetStore.GetByIndex(common.TagScopeVMNamespace, ns)
	res := make([]*model.VpcSubnet, 0, len(nsxSubnets))
	for _, nsxSubnet := range nsxSubnets {
		tagName := nsxutil.FindTag(nsxSubnet.Tags, common.TagScopeSubnetCRName)
		if tagName == name {
			res = append(res, nsxSubnet)
		}
	}
	return res
}

func (service *SubnetService) ListSubnetBySubnetSetName(ns, subnetSetName string) []*model.VpcSubnet {
	nsxSubnets := service.SubnetStore.GetByIndex(common.TagScopeVMNamespace, ns)
	nsxSubnetsOfDefaultPodSubnetSet := service.SubnetStore.GetByIndex(common.TagScopeNamespace, ns)
	nsxSubnets = append(nsxSubnets, nsxSubnetsOfDefaultPodSubnetSet...)
	res := make([]*model.VpcSubnet, 0, len(nsxSubnets))
	for _, nsxSubnet := range nsxSubnets {
		tagName := nsxutil.FindTag(nsxSubnet.Tags, common.TagScopeSubnetSetCRName)
		if tagName == subnetSetName {
			res = append(res, nsxSubnet)
		}
	}
	return res
}

func (service *SubnetService) GetSubnetStatus(subnet *model.VpcSubnet) ([]model.VpcSubnetStatus, error) {
	param, err := common.ParseVPCResourcePath(*subnet.Path)
	if err != nil {
		return nil, err
	}
	statusList, err := service.NSXClient.SubnetStatusClient.List(param.OrgID, param.ProjectID, param.VPCID, *subnet.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to get Subnet status")
		return nil, err
	}
	if len(statusList.Results) == 0 {
		err := errors.New("empty status result")
		log.Error(err, "No subnet status found")
		return nil, err
	}
	if statusList.Results[0].NetworkAddress == nil || statusList.Results[0].GatewayAddress == nil {
		err := fmt.Errorf("invalid status result: %+v", statusList.Results[0])
		log.Error(err, "Subnet status does not have network address or gateway address", "subnet.Id", subnet.Id)
		return nil, err
	}
	return statusList.Results, nil
}

func (service *SubnetService) UpdateSubnetSetStatus(obj *v1alpha1.SubnetSet) error {
	var subnetInfoList []v1alpha1.SubnetInfo
	nsxSubnets := service.SubnetStore.GetByIndex(common.TagScopeSubnetSetCRUID, string(obj.GetUID()))
	for _, subnet := range nsxSubnets {
		subnet := subnet
		statusList, err := service.GetSubnetStatus(subnet)
		if err != nil {
			return err
		}
		subnetInfo := v1alpha1.SubnetInfo{}
		for _, status := range statusList {
			subnetInfo.NetworkAddresses = append(subnetInfo.NetworkAddresses, *status.NetworkAddress)
			subnetInfo.GatewayAddresses = append(subnetInfo.GatewayAddresses, *status.GatewayAddress)
			// DHCPServerAddress is only for the subnet with DHCP enabled
			if status.DhcpServerAddress != nil {
				subnetInfo.DHCPServerAddresses = append(subnetInfo.DHCPServerAddresses, *status.DhcpServerAddress)
			}
		}
		subnetInfoList = append(subnetInfoList, subnetInfo)
	}
	obj.Status.Subnets = subnetInfoList
	if err := service.Client.Status().Update(context.Background(), obj); err != nil {
		log.Error(err, "Failed to update SubnetSet status")
		return err
	}
	return nil
}

func (service *SubnetService) GetSubnetByKey(key string) (*model.VpcSubnet, error) {
	nsxSubnet := service.SubnetStore.GetByKey(key)
	if nsxSubnet == nil {
		return nil, errors.New("NSX subnet not found in store")
	}
	return nsxSubnet, nil
}

func (service *SubnetService) GetSubnetByPath(path string, sharedSubnet bool) (*model.VpcSubnet, error) {
	if sharedSubnet {
		associatedResource, err := common.ConvertSubnetPathToAssociatedResource(path)
		if err != nil {
			return nil, err
		}
		return service.GetNSXSubnetFromCacheOrAPI(associatedResource)
	}
	info, err := common.ParseVPCResourcePath(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path '%s' while getting Subnet", path)
	}
	return service.GetSubnetByKey(info.ID)
}

// GetSubnetByCR gets NSX Subnet based on the Subnet CR
// For shared Subnet, it gets from shared subnet cache or API
// Otherwise it gets from Subnet store
func (service *SubnetService) GetSubnetByCR(subnet *v1alpha1.Subnet) (*model.VpcSubnet, error) {
	if common.IsSharedSubnet(subnet) {
		return service.GetNSXSubnetFromCacheOrAPI(subnet.Annotations[common.AnnotationAssociatedResource])
	}
	subnetList := service.GetSubnetsByIndex(common.TagScopeSubnetCRUID, string(subnet.GetUID()))
	if len(subnetList) == 0 {
		err := fmt.Errorf("empty NSX resource path for Subnet CR %s(%s)", subnet.Name, subnet.GetUID())
		return nil, err
	} else if len(subnetList) > 1 {
		err := fmt.Errorf("multiple NSX Subnets found for Subnet CR %s(%s)", subnet.Name, subnet.GetUID())
		log.Error(err, "Failed to get NSX Subnet by Subnet CR UID", "subnetList", subnetList)
		return nil, err
	}
	return subnetList[0], nil
}

func (service *SubnetService) ListSubnetSetIDsFromNSXSubnets() sets.Set[string] {
	subnetSetIDs := service.SubnetStore.ListIndexFuncValues(common.TagScopeSubnetSetCRUID)
	return subnetSetIDs
}

func (service *SubnetService) ListSubnetIDsFromNSXSubnets() sets.Set[string] {
	subnetIDs := service.SubnetStore.ListIndexFuncValues(common.TagScopeSubnetCRUID)
	return subnetIDs
}

// ListAllSubnet ListIndexFuncValues returns all the indexed values of the given index
// maps the indexed value to a set of keys in the store that match on that value: type Index map[string]sets.String
// sees the getIndexValues function in k8s.io/client-go/tools/cache/thread_safe_store.go
func (service *SubnetService) ListAllSubnet() []*model.VpcSubnet {
	var allNSXSubnets []*model.VpcSubnet
	// ListSubnetCreatedBySubnet
	subnets := service.SubnetStore.ListIndexFuncValues(common.TagScopeSubnetCRUID)
	for subnetID := range subnets {
		nsxSubnets := service.ListSubnetCreatedBySubnet(subnetID)
		allNSXSubnets = append(allNSXSubnets, nsxSubnets...)
	}
	// ListSubnetCreatedBySubnetSet
	subnetSets := service.SubnetStore.ListIndexFuncValues(common.TagScopeSubnetSetCRUID)
	for subnetSetID := range subnetSets {
		nsxSubnets := service.ListSubnetCreatedBySubnetSet(subnetSetID)
		allNSXSubnets = append(allNSXSubnets, nsxSubnets...)
	}
	return allNSXSubnets
}

func (service *SubnetService) GetSubnetsByIndex(key, value string) []*model.VpcSubnet {
	return service.SubnetStore.GetByIndex(key, value)
}

func (service *SubnetService) GenerateSubnetNSTags(obj client.Object) []model.Tag {
	ns := obj.GetNamespace()
	namespace := &v1.Namespace{}
	namespacedName := types.NamespacedName{
		Name: ns,
	}
	// Get the namespace object from the Kubernetes API
	if err := service.Client.Get(context.Background(), namespacedName, namespace); err != nil {
		log.Error(err, "Failed to get Namespace", "Namespace", ns)
		return nil
	}
	nsUID := string(namespace.UID)
	var tags = []model.Tag{
		{Scope: common.String(common.TagScopeManagedBy), Tag: common.String(common.AutoCreatedTagValue)},
	}
	switch o := obj.(type) {
	case *v1alpha1.Subnet:
		tags = append(tags,
			model.Tag{Scope: common.String(common.TagScopeVMNamespaceUID), Tag: common.String(nsUID)},
			model.Tag{Scope: common.String(common.TagScopeVMNamespace), Tag: common.String(obj.GetNamespace())})
	case *v1alpha1.SubnetSet:
		isDefaultPodSubnetSet := o.Labels[common.LabelDefaultSubnetSet] == common.LabelDefaultPodSubnetSet
		if isDefaultPodSubnetSet {
			tags = append(tags,
				model.Tag{Scope: common.String(common.TagScopeNamespaceUID), Tag: common.String(nsUID)},
				model.Tag{Scope: common.String(common.TagScopeNamespace), Tag: common.String(obj.GetNamespace())})
		} else {
			tags = append(tags,
				model.Tag{Scope: common.String(common.TagScopeVMNamespaceUID), Tag: common.String(nsUID)},
				model.Tag{Scope: common.String(common.TagScopeVMNamespace), Tag: common.String(obj.GetNamespace())})
		}
	}
	// Append Namespace labels in order as tags
	labelKeys := make([]string, 0, len(namespace.Labels))
	for k := range namespace.Labels {
		labelKeys = append(labelKeys, k)
	}
	sort.Strings(labelKeys)
	for _, k := range labelKeys {
		tags = append(tags, model.Tag{Scope: common.String(k), Tag: common.String(namespace.Labels[k])})
	}
	return tags
}

func (service *SubnetService) UpdateSubnetSet(ns string, vpcSubnets []*model.VpcSubnet, tags []model.Tag, dhcpMode string) error {
	if dhcpMode == "" {
		dhcpMode = v1alpha1.DHCPConfigModeDeactivated
	}
	for i, vpcSubnet := range vpcSubnets {
		subnetSet := &v1alpha1.SubnetSet{}
		var name string
		// Generate new Subnet tags
		matchNamespace := false
		for _, t := range vpcSubnets[i].Tags {
			tag := t
			if *tag.Scope == common.TagScopeSubnetSetCRName {
				name = *tag.Tag
			}
			if *tag.Scope == common.TagScopeNamespace || *tag.Scope == common.TagScopeVMNamespace {
				if *tag.Tag != ns {
					break
				}
				matchNamespace = true
			}
		}

		// Skip the subnet if the Namespace doesn't match
		if !matchNamespace {
			log.Info("Namespace mismatch, skipping subnet", "Subnet", *vpcSubnet.Id, "Namespace", ns)
			continue
		}

		if err := service.Client.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, subnetSet); err != nil {
			return fmt.Errorf("failed to get SubnetSet %s in Namespace %s: %w", name, ns, err)
		}
		newTags := append(service.buildBasicTags(subnetSet), tags...)

		// Avoid updating vpcSubnets[i] to ensure the Subnet store
		// is only updated after the updating succeeds.
		updatedSubnet := *vpcSubnets[i]
		updatedSubnet.Tags = newTags
		// Update the SubnetSet DHCP Config
		if updatedSubnet.SubnetDhcpConfig != nil {
			// Generate a new SubnetDhcpConfig for updatedSubnet to
			// avoid changing vpcSubnets[i].SubnetDhcpConfig
			updatedSubnet.SubnetDhcpConfig = service.buildSubnetDHCPConfig(dhcpMode, updatedSubnet.SubnetDhcpConfig.DhcpServerAdditionalConfig)
		}
		changed := common.CompareResource(SubnetToComparable(vpcSubnets[i]), SubnetToComparable(&updatedSubnet))
		if !changed {
			log.Info("NSX Subnet unchanged, skipping update", "Subnet", *vpcSubnet.Id)
			continue
		}

		vpcInfo, err := common.ParseVPCResourcePath(*vpcSubnets[i].Path)
		if err != nil {
			err := fmt.Errorf("failed to parse NSX VPC path for Subnet %s: %s", *vpcSubnets[i].Path, err)
			return err
		}
		if _, err := service.createOrUpdateSubnet(subnetSet, &updatedSubnet, &vpcInfo); err != nil {
			return fmt.Errorf("failed to update Subnet %s in SubnetSet %s: %w", *vpcSubnet.Id, subnetSet.Name, err)
		}
		log.Info("Successfully updated SubnetSet", "subnetSet", subnetSet, "Subnet", *vpcSubnet.Id)
	}
	return nil
}

// GetNSXSubnetByAssociatedResource gets the NSX subnet based on the associated resource annotation
func (service *SubnetService) GetNSXSubnetByAssociatedResource(associatedResource string) (*model.VpcSubnet, error) {
	// Parse the associated resource string (format: projectID:vpcID:subnetID)
	parts := strings.Split(associatedResource, ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid associated resource format: %s, expected format: projectID:vpcID:subnetID", associatedResource)
	}

	orgID := "default" // hardcoded for now
	projectID := parts[0]
	vpcID := parts[1]
	subnetID := parts[2]

	nsxSubnet, err := service.NSXClient.SubnetsClient.Get(orgID, projectID, vpcID, subnetID)
	if err != nil {
		log.Error(err, "Failed to get NSX Subnet", "SubnetName", subnetID)
		return nil, err
	}

	return &nsxSubnet, nil
}

// MapNSXSubnetToSubnetCR maps NSX subnet properties to Subnet CR properties
func (service *SubnetService) MapNSXSubnetToSubnetCR(subnetCR *v1alpha1.Subnet, nsxSubnet *model.VpcSubnet) {
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

	// Map AdvancedConfig
	if nsxSubnet.AdvancedConfig != nil {
		// Map EnableVLANExtension from NSX Subnet
		if nsxSubnet.AdvancedConfig.EnableVlanExtension != nil {
			subnetCR.Spec.AdvancedConfig.EnableVLANExtension = *nsxSubnet.AdvancedConfig.EnableVlanExtension
		}

		if nsxSubnet.AdvancedConfig.ConnectivityState != nil {
			connectivityState := *nsxSubnet.AdvancedConfig.ConnectivityState
			switch connectivityState {
			case "CONNECTED":
				subnetCR.Spec.AdvancedConfig.ConnectivityState = v1alpha1.ConnectivityStateConnected
			case "DISCONNECTED":
				subnetCR.Spec.AdvancedConfig.ConnectivityState = v1alpha1.ConnectivityStateDisconnected
			}
		}
	}
}

// MapNSXSubnetStatusToSubnetCRStatus maps NSX subnet status to Subnet CR status
func (service *SubnetService) MapNSXSubnetStatusToSubnetCRStatus(subnetCR *v1alpha1.Subnet, statusList []model.VpcSubnetStatus) {
	// Clear existing status fields
	subnetCR.Status.NetworkAddresses = subnetCR.Status.NetworkAddresses[:0]
	subnetCR.Status.GatewayAddresses = subnetCR.Status.GatewayAddresses[:0]
	subnetCR.Status.DHCPServerAddresses = subnetCR.Status.DHCPServerAddresses[:0]

	// Set the shared flag to true for shared subnets
	if _, ok := subnetCR.Annotations[common.AnnotationAssociatedResource]; ok {
		subnetCR.Status.Shared = true
	}

	// Map status fields from NSX subnet status
	for _, status := range statusList {
		if status.NetworkAddress != nil {
			subnetCR.Status.NetworkAddresses = append(subnetCR.Status.NetworkAddresses, *status.NetworkAddress)
		}
		if status.GatewayAddress != nil {
			subnetCR.Status.GatewayAddresses = append(subnetCR.Status.GatewayAddresses, *status.GatewayAddress)
		}
		// DHCPServerAddress is only for the subnet with DHCP enabled
		if status.DhcpServerAddress != nil {
			subnetCR.Status.DHCPServerAddresses = append(subnetCR.Status.DHCPServerAddresses, *status.DhcpServerAddress)
		}
		// Handle VLAN extension
		if status.VlanExtension != nil {
			vlanExtension := v1alpha1.VLANExtension{}
			if status.VlanExtension.VlanId != nil {
				vlanExtension.VLANID = int(*status.VlanExtension.VlanId)
			}
			if status.VlanExtension.VpcGatewayConnectionEnable != nil {
				vlanExtension.VPCGatewayConnectionEnable = *status.VlanExtension.VpcGatewayConnectionEnable
			}
			subnetCR.Status.VLANExtension = vlanExtension
		}
	}
}

// BuildSubnetCR creates a Subnet CR object with the given parameters
func (service *SubnetService) BuildSubnetCR(ns, subnetName, vpcFullID, associatedName string) *v1alpha1.Subnet {
	// Create the Subnet CR
	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      subnetName,
			Namespace: ns,
			Annotations: map[string]string{
				common.AnnotationAssociatedResource: associatedName,
			},
		},
		Spec: v1alpha1.SubnetSpec{
			VPCName: vpcFullID,
		},
	}
	log.Info("Build Subnet CR", "Subnet", subnetCR)

	// Initialize subnetCR from nsxSubnet if available
	return subnetCR
}

// GetNSXSubnetFromCacheOrAPI retrieves the NSX subnet from cache if available, otherwise from the NSX API
// It returns the NSX subnet and any error encountered
func (service *SubnetService) GetNSXSubnetFromCacheOrAPI(associatedResource string) (*model.VpcSubnet, error) {
	// First check if the NSX subnet is in the cache
	service.nsxSubnetCacheMutex.RLock()
	cachedData, exists := service.NSXSubnetCache[associatedResource]
	service.nsxSubnetCacheMutex.RUnlock()

	if exists && cachedData.Subnet != nil {
		log.V(1).Info("Found NSX subnet in cache", "AssociatedResource", associatedResource)
		return cachedData.Subnet, nil
	}

	// Get the NSX subnet from the NSX API
	log.V(1).Info("NSX subnet not in cache, fetching from NSX API", "AssociatedResource", associatedResource)
	nsxSubnet, err := service.GetNSXSubnetByAssociatedResource(associatedResource)
	if err != nil {
		log.Error(err, "Failed to get NSX Subnet", "AssociatedResource", associatedResource)
		return nil, err
	}

	service.UpdateNSXSubnetCache(associatedResource, nsxSubnet, []model.VpcSubnetStatus{})

	return nsxSubnet, nil
}

// GetSubnetStatusFromCacheOrAPI retrieves the subnet status from cache if available, otherwise from the NSX API
// It returns the status list and any error encountered
func (service *SubnetService) GetSubnetStatusFromCacheOrAPI(nsxSubnet *model.VpcSubnet, associatedResource string) ([]model.VpcSubnetStatus, error) {
	// Check if statusList is in cache
	service.nsxSubnetCacheMutex.RLock()
	cachedData, exists := service.NSXSubnetCache[associatedResource]
	service.nsxSubnetCacheMutex.RUnlock()

	if exists && len(cachedData.StatusList) > 0 {
		log.V(1).Info("Found status list in cache", "AssociatedResource", associatedResource)
		return cachedData.StatusList, nil
	}

	// Get subnet status from NSX
	log.V(1).Info("Status list not in cache, fetching from NSX API", "AssociatedResource", associatedResource)
	statusList, err := service.GetSubnetStatus(nsxSubnet)
	if err != nil {
		log.Error(err, "Failed to get Subnet status", "AssociatedResource", associatedResource)
		return nil, err
	}

	service.UpdateNSXSubnetCache(associatedResource, nsxSubnet, statusList)

	return statusList, nil
}

// UpdateNSXSubnetCache updates the cache with the NSX subnet and status list
func (service *SubnetService) UpdateNSXSubnetCache(associatedResource string, nsxSubnet *model.VpcSubnet, statusList []model.VpcSubnetStatus) {
	service.nsxSubnetCacheMutex.Lock()
	defer service.nsxSubnetCacheMutex.Unlock()

	service.NSXSubnetCache[associatedResource] = struct {
		Subnet     *model.VpcSubnet
		StatusList []model.VpcSubnetStatus
	}{
		Subnet:     nsxSubnet,
		StatusList: statusList,
	}
	log.Info("Updated NSX subnet cache", "AssociatedResource", associatedResource)
}

// RemoveSubnetFromCache removes a subnet from the NSXSubnetCache
func (service *SubnetService) RemoveSubnetFromCache(associatedResource string, reason string) {
	service.nsxSubnetCacheMutex.Lock()
	defer service.nsxSubnetCacheMutex.Unlock()
	delete(service.NSXSubnetCache, associatedResource)
	log.Info("Removed Subnet from cache", "reason", reason, "AssociatedResource", associatedResource)
}

// AddSharedSubnetToResourceMap adds a shared subnet CR to the resource map
func (service *SubnetService) AddSharedSubnetToResourceMap(associatedResource string, namespacedName types.NamespacedName) {
	service.sharedSubnetResourceMapMutex.Lock()
	defer service.sharedSubnetResourceMapMutex.Unlock()

	// If the set doesn't exist for this associatedResource, create it
	if _, exists := service.SharedSubnetResourceMap[associatedResource]; !exists {
		service.SharedSubnetResourceMap[associatedResource] = sets.New[types.NamespacedName]()
	}

	// Add the namespacedName to the set (no need to check if it exists, sets handle that automatically)
	service.SharedSubnetResourceMap[associatedResource].Insert(namespacedName)
	log.Info("Added shared subnet to resource map", "AssociatedResource", associatedResource, "NamespacedName", namespacedName)
}

// RemoveSharedSubnetFromResourceMap removes a shared subnet CR from the resource map
func (service *SubnetService) RemoveSharedSubnetFromResourceMap(associatedResource string, namespacedName types.NamespacedName) {
	service.sharedSubnetResourceMapMutex.Lock()
	defer service.sharedSubnetResourceMapMutex.Unlock()

	// If the set exists for this associatedResource, remove the namespacedName
	if namespacedNames, exists := service.SharedSubnetResourceMap[associatedResource]; exists {
		namespacedNames.Delete(namespacedName)
		log.Info("Removed shared subnet from resource map", "AssociatedResource", associatedResource, "NamespacedName", namespacedName)

		// If the set is now empty, remove the associatedResource key
		if namespacedNames.Len() == 0 {
			delete(service.SharedSubnetResourceMap, associatedResource)
		}
	}
}
