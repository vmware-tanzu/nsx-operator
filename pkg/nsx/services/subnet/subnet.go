package subnet

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
)

var (
	log                       = logger.Log
	MarkedForDelete           = true
	EnforceRevisionCheckParam = false
	ResourceTypeSubnet        = common.ResourceTypeSubnet
	NewConverter              = common.NewConverter
	// Default static ip-pool under Subnet.
	ipPoolID        = "static-ipv4-default"
	SubnetTypeError = errors.New("unsupported type")
)

type SubnetService struct {
	common.Service
	SubnetStore *SubnetStore
}

// SubnetParameters stores parameters to CRUD Subnet object
type SubnetParameters struct {
	OrgID     string
	ProjectID string
	VPCID     string
}

var subnetService *SubnetService
var lock = &sync.Mutex{}

// GetSubnetService get singleton SubnetService instance, subnet/subnetset controller share the same instance.
func GetSubnetService(service common.Service) *SubnetService {
	if subnetService == nil {
		lock.Lock()
		defer lock.Unlock()
		if subnetService == nil {
			var err error
			if subnetService, err = InitializeSubnetService(service); err != nil {
				log.Error(err, "failed to initialize subnet commonService")
				os.Exit(1)
			}
		}
	}
	return subnetService
}

// InitializeSubnetService initialize Subnet service.
func InitializeSubnetService(service common.Service) (*SubnetService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)
	subnetService := &SubnetService{
		Service: service,
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetCRType:   subnetTypeIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
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

func (service *SubnetService) CreateOrUpdateSubnet(obj client.Object, tags []model.Tag) (string, error) {
	vpcList := &v1alpha1.VPCList{}
	if err := service.Client.List(context.Background(), vpcList, client.InNamespace(obj.GetNamespace())); err != nil {
		log.Error(err, "fail to list VPC", "ns", obj.GetNamespace())
		return "", err
	}
	if len(vpcList.Items) == 0 {
		err := errors.New("no VPC found")
		log.Error(err, "", "ns", obj.GetNamespace())
		return "", err
	}
	vpc := vpcList.Items[0]
	vpcInfo, err := common.ParseVPCResourcePath(vpc.Status.NSXResourcePath)
	if err != nil {
		err := fmt.Errorf("failed to parse NSX VPC path for VPC %s: %s", vpc.UID, err)
		return "", err
	}
	uid := string(obj.GetUID())
	nsxSubnet, err := service.buildSubnet(obj, tags)
	if err != nil {
		log.Error(err, "failed to build Subnet")
		return "", err
	}
	// Only check whether needs update when obj is v1alpha1.Subnet
	if subnet, ok := obj.(*v1alpha1.Subnet); ok {
		existingSubnet := service.SubnetStore.GetByKey(service.BuildSubnetID(subnet))
		changed := false
		if existingSubnet == nil {
			changed = true
		} else {
			changed = common.CompareResource(SubnetToComparable(existingSubnet), SubnetToComparable(nsxSubnet))
		}
		if !changed {
			log.Info("subnet not changed, skip updating", "subnet.Id", uid)
			return uid, nil
		}
	}
	return service.createOrUpdateSubnet(obj, nsxSubnet, &vpcInfo)
}

func (service *SubnetService) createOrUpdateSubnet(obj client.Object, nsxSubnet *model.VpcSubnet, vpcInfo *common.VPCResourceInfo) (string, error) {
	orgRoot, err := service.WrapHierarchySubnet(nsxSubnet, vpcInfo)
	if err != nil {
		log.Error(err, "WrapHierarchySubnet failed")
		return "", err
	}
	if err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam); err != nil {
		return "", err
	}
	// Get Subnet from NSX after patch operation as NSX renders several fields like `path`/`parent_path`.
	if *nsxSubnet, err = service.NSXClient.SubnetsClient.Get(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, *nsxSubnet.Id); err != nil {
		return "", err
	}
	realizeService := realizestate.InitializeRealizeState(service.Service)
	if err = realizeService.CheckRealizeState(retry.DefaultRetry, *nsxSubnet.Path, "RealizedLogicalSwitch"); err != nil {
		log.Error(err, "failed to check subnet realization state", "ID", *nsxSubnet.Id)
		return "", err
	}
	if err = service.SubnetStore.Apply(nsxSubnet); err != nil {
		log.Error(err, "failed to add subnet to store", "ID", *nsxSubnet.Id)
		return "", err
	}
	if subnetSet, ok := obj.(*v1alpha1.SubnetSet); ok {
		if err = service.UpdateSubnetSetStatus(subnetSet); err != nil {
			return "", err
		}
	}
	log.Info("successfully updated nsxSubnet", "nsxSubnet", nsxSubnet)
	return *nsxSubnet.Path, nil
}

func (service *SubnetService) DeleteSubnet(nsxSubnet model.VpcSubnet) error {
	vpcInfo, _ := common.ParseVPCResourcePath(*nsxSubnet.Path)
	nsxSubnet.MarkedForDelete = &MarkedForDelete
	// WrapHighLevelSubnet will modify the input subnet, make a copy for the following store update.
	subnetCopy := nsxSubnet
	orgRoot, err := service.WrapHierarchySubnet(&nsxSubnet, &vpcInfo)
	if err != nil {
		return err
	}
	if err = service.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam); err != nil {
		// Subnets that are not deleted successfully will finally be deleted by GC.
		log.Error(err, "failed to delete Subnet", "ID", *nsxSubnet.Id)
		return err
	}
	if err = service.SubnetStore.Apply(&subnetCopy); err != nil {
		log.Error(err, "failed to delete Subnet from store", "ID", *nsxSubnet.Id)
		return err
	}
	log.Info("successfully deleted nsxSubnet", "nsxSubnet", nsxSubnet)
	return nil
}

func (service *SubnetService) ListSubnetCreatedByCR() []model.VpcSubnet {
	return service.listSubnetByCRType("subnet")
}

func (service *SubnetService) listSubnetByCRType(crType string) []model.VpcSubnet {
	subnets := service.SubnetStore.GetByIndex(common.TagScopeSubnetCRType, crType)
	subnetList := []model.VpcSubnet{}
	for _, subnet := range subnets {
		subnetList = append(subnetList, subnet)
	}
	return subnetList
}

func (service *SubnetService) ListSubnetCreatedBySubnetSet() []model.VpcSubnet {
	return service.listSubnetByCRType("subnetset")
}

func (service *SubnetService) ListSubnetSetID(ctx context.Context) sets.String {
	crdSubnetSetList := &v1alpha1.SubnetSetList{}
	subnetsetIDs := sets.NewString()
	err := service.Client.List(ctx, crdSubnetSetList)
	if err != nil {
		log.Error(err, "failed to list subnetset CR")
		return subnetsetIDs
	}
	for _, subnetset := range crdSubnetSetList.Items {
		subnetsetIDs.Insert(string(subnetset.UID))
	}
	return subnetsetIDs
}

// check if subnet belongs to a subnetset, if yes, check if that subnetset still exists
func (service *SubnetService) IsOrphanSubnet(subnet model.VpcSubnet, subnetsetIDs sets.String) bool {
	for _, tag := range subnet.Tags {
		if *tag.Scope == common.TagScopeSubnetSetCRUID && subnetsetIDs.Has(*tag.Tag) {
			return false
		}
	}
	return true
}

func (service *SubnetService) DeleteIPAllocation(orgID, projectID, vpcID, subnetID string) error {
	ipAllocations, err := service.NSXClient.IPAllocationClient.List(orgID, projectID, vpcID, subnetID, ipPoolID,
		nil, nil, nil, nil, nil, nil)
	if err != nil {
		log.Error(err, "failed to get ip-allocations", "Subnet", subnetID)
		return err
	}
	for _, alloc := range ipAllocations.Results {
		if err = service.NSXClient.IPAllocationClient.Delete(orgID, projectID, vpcID, subnetID, ipPoolID, *alloc.Id); err != nil {
			log.Error(err, "failed to delete ip-allocation", "Subnet", subnetID, "ip-alloc", *alloc.Id)
			return err
		}
	}
	log.Info("all IP allocations have been deleted", "Subnet", subnetID)
	return nil
}

func (service *SubnetService) GetSubnetStatus(subnet *model.VpcSubnet) ([]model.VpcSubnetStatus, error) {
	param, err := common.ParseVPCResourcePath(*subnet.Path)
	if err != nil {
		return nil, err
	}
	statusList, err := service.NSXClient.SubnetStatusClient.List(param.OrgID, param.ProjectID, param.VPCID, *subnet.Id)
	if err != nil {
		log.Error(err, "failed to get subnet status")
		return nil, err
	}
	if len(statusList.Results) == 0 {
		err := errors.New("empty status result")
		log.Error(err, "no subnet status found")
		return nil, err
	}
	return statusList.Results, nil
}

func (service *SubnetService) getIPPoolUsage(nsxSubnet *model.VpcSubnet) (*model.PolicyPoolUsage, error) {
	param, err := common.ParseVPCResourcePath(*nsxSubnet.Path)
	if err != nil {
		return nil, err
	}
	ipPool, err := service.NSXClient.IPPoolClient.Get(param.OrgID, param.ProjectID, param.VPCID, *nsxSubnet.Id, ipPoolID)
	if err != nil {
		log.Error(err, "failed to get ip-pool", "Subnet", *nsxSubnet.Id)
		return nil, err
	}
	return ipPool.PoolUsage, nil
}

func (service *SubnetService) GetIPPoolUsage(subnet *v1alpha1.Subnet) (*model.PolicyPoolUsage, error) {
	nsxSubnets := service.SubnetStore.GetByIndex(common.TagScopeSubnetCRUID, string(subnet.GetUID()))
	if len(nsxSubnets) == 0 {
		return nil, errors.New("NSX Subnet doesn't exist in store")
	}
	return service.getIPPoolUsage(&nsxSubnets[0])
}

func (service *SubnetService) UpdateSubnetSetStatus(obj *v1alpha1.SubnetSet) error {
	var subnetInfoList []v1alpha1.SubnetInfo
	nsxSubnets := service.SubnetStore.GetByIndex(common.TagScopeSubnetSetCRUID, string(obj.GetUID()))
	for _, subnet := range nsxSubnets {
		statusList, err := service.GetSubnetStatus(&subnet)
		if err != nil {
			return err
		}
		subnetInfo := v1alpha1.SubnetInfo{
			NSXResourcePath: *subnet.Path,
		}
		for _, status := range statusList {
			subnetInfo.IPAddresses = append(subnetInfo.IPAddresses, *status.NetworkAddress)
		}
		subnetInfoList = append(subnetInfoList, subnetInfo)
	}
	obj.Status.Subnets = subnetInfoList
	if err := service.Client.Status().Update(context.Background(), obj); err != nil {
		log.Error(err, "failed to update SubnetSet status")
		return err
	}
	return nil
}

func (service *SubnetService) ListSubnetID() sets.String {
	subnets := service.SubnetStore.ListIndexFuncValues(common.TagScopeSubnetCRUID)
	subnetSets := service.SubnetStore.ListIndexFuncValues(common.TagScopeSubnetSetCRUID)
	return subnets.Union(subnetSets)
}

func (service *SubnetService) Cleanup() error {
	uids := service.ListSubnetID()
	log.Info("cleaning up subnet", "count", len(uids))
	for uid := range uids {
		nsxSubnets := service.SubnetStore.GetByIndex(common.TagScopeSubnetCRUID, string(uid))
		for _, nsxSubnet := range nsxSubnets {
			err := service.DeleteSubnet(nsxSubnet)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (service *SubnetService) GenerateSubnetNSTags(obj client.Object, nsUID string) []model.Tag {
	var tags []model.Tag
	switch o := obj.(type) {
	case *v1alpha1.Subnet:
		tags = append(tags,
			model.Tag{Scope: String(common.TagScopeVMNamespaceUID), Tag: String(nsUID)},
			model.Tag{Scope: String(common.TagScopeVMNamespace), Tag: String(obj.GetNamespace())})
	case *v1alpha1.SubnetSet:
		findLabelDefaultPodSubnetSet := false
		for k, v := range o.Labels {
			if k == common.LabelDefaultSubnetSet && v == common.LabelDefaultPodSubnetSet {
				findLabelDefaultPodSubnetSet = true
				break
			}
		}
		if findLabelDefaultPodSubnetSet {
			tags = append(tags,
				model.Tag{Scope: common.String(common.TagScopeNamespaceUID), Tag: common.String(nsUID)},
				model.Tag{Scope: common.String(common.TagScopeNamespace), Tag: common.String(obj.GetNamespace())})
		} else {
			tags = append(tags,
				model.Tag{Scope: common.String(common.TagScopeVMNamespaceUID), Tag: common.String(nsUID)},
				model.Tag{Scope: common.String(common.TagScopeVMNamespace), Tag: common.String(obj.GetNamespace())})
		}
	}
	return tags
}

func (service *SubnetService) UpdateSubnetSetTags(ns string, vpcSubnets []model.VpcSubnet, tags []model.Tag) error {
	for _, existingSubnet := range vpcSubnets {
		subnetSet := &v1alpha1.SubnetSet{}
		var name string

		matchNamespace := false
		for _, tag := range existingSubnet.Tags {
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

		if matchNamespace {
			if err := service.Client.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, subnetSet); err != nil {
				return err
			}
			newTags := append(service.buildBasicTags(subnetSet), tags...)
			changed := common.CompareResource(SubnetToComparable(&existingSubnet), SubnetToComparable(&model.VpcSubnet{Tags: newTags}))
			if !changed {
				log.Info("NSX subnet tags unchanged, skip updating")
				continue
			}
			existingSubnet.Tags = newTags
			vpcInfo, err := common.ParseVPCResourcePath(*existingSubnet.Path)
			if err != nil {
				err := fmt.Errorf("failed to parse NSX VPC path for Subnet %s: %s", *existingSubnet.Path, err)
				return err
			}
			if _, err := service.createOrUpdateSubnet(subnetSet, &existingSubnet, &vpcInfo); err != nil {
				return err
			}
			log.Info("successfully updated subnet set tags", "subnetSet", subnetSet)
		}
	}
	return nil
}
