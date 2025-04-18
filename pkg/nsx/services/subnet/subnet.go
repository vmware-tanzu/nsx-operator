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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                       = &logger.Log
	MarkedForDelete           = true
	EnforceRevisionCheckParam = false
	ResourceTypeSubnet        = common.ResourceTypeSubnet
	NewConverter              = common.NewConverter
	// Default static ip-pool under Subnet.
	SubnetTypeError            = errors.New("unsupported type")
	ErrorCodeUnrecognizedField = int64(287)
)

type SubnetService struct {
	common.Service
	SubnetStore *SubnetStore
	builder     *common.PolicyTreeBuilder[*model.VpcSubnet]
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
		Service: service,
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
					common.IndexByVPCPathFuncKey:  common.IndexByVPCFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
		builder: builder,
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

func (service *SubnetService) CreateOrUpdateSubnet(obj client.Object, vpcInfo common.VPCResourceInfo, tags []model.Tag) (subnet *model.VpcSubnet, err error) {
	uid := string(obj.GetUID())
	nsxSubnet, err := service.buildSubnet(obj, tags)
	if err != nil {
		log.Error(err, "Failed to build Subnet")
		return nil, err
	}
	// Only check whether it needs update when obj is v1alpha1.Subnet
	if subnet, ok := obj.(*v1alpha1.Subnet); ok {
		existingSubnet := service.SubnetStore.GetByKey(service.BuildSubnetID(subnet))
		changed := false
		if existingSubnet == nil {
			changed = true
		} else {
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
		// Delete the subnet if realization check fails, avoiding creating duplicate subnets continuously.
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
		// Subnets that are not deleted successfully will finally be deleted by GC.
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

func (service *SubnetService) GetSubnetByPath(path string) (*model.VpcSubnet, error) {
	pathSlice := strings.Split(path, "/")
	if len(pathSlice) == 0 {
		return nil, fmt.Errorf("invalid path '%s' while getting subnet", path)
	}
	key := pathSlice[len(pathSlice)-1]
	nsxSubnet, err := service.GetSubnetByKey(key)
	return nsxSubnet, err
}

func (service *SubnetService) ListSubnetSetIDsFromNSXSubnets() sets.Set[string] {
	subnetSetIDs := service.SubnetStore.ListIndexFuncValues(common.TagScopeSubnetSetCRUID)
	return subnetSetIDs
}

func (service *SubnetService) ListSubnetIDsFromNSXSubnets() sets.Set[string] {
	subnetIDs := service.SubnetStore.ListIndexFuncValues(common.TagScopeSubnetCRUID)
	return subnetIDs
}

// ListIndexFuncValues returns all the indexed values of the given index
// Index maps the indexed value to a set of keys in the store that match on that value: type Index map[string]sets.String
// see the getIndexValues function in k8s.io/client-go/tools/cache/thread_safe_store.go
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
	var tags []model.Tag
	switch o := obj.(type) {
	case *v1alpha1.Subnet:
		tags = append(tags,
			model.Tag{Scope: String(common.TagScopeVMNamespaceUID), Tag: String(nsUID)},
			model.Tag{Scope: String(common.TagScopeVMNamespace), Tag: String(obj.GetNamespace())})
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

		// Skip this subnet if the Namespace doesn't match
		if !matchNamespace {
			log.Info("Namespace mismatch, skipping subnet", "Subnet", *vpcSubnet.Id, "Namespace", ns)
			continue
		}

		if err := service.Client.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, subnetSet); err != nil {
			return fmt.Errorf("failed to get SubnetSet %s in Namespace %s: %w", name, ns, err)
		}
		newTags := append(service.buildBasicTags(subnetSet), tags...)

		// Avoid updating vpcSubnets[i] to ensure Subnet store
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
