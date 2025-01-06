package ipaddressallocation

import (
	"context"
	"fmt"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log                             = &logger.Log
	MarkedForDelete                 = true
	ResourceTypeIPAddressAllocation = common.ResourceTypeIPAddressAllocation
)

type IPAddressAllocationService struct {
	common.Service
	ipAddressAllocationStore *IPAddressAllocationStore
	VPCService               common.VPCServiceProvider
	builder                  *common.PolicyTreeBuilder[*model.VpcIpAddressAllocation]
}

func InitializeIPAddressAllocation(service common.Service, vpcService common.VPCServiceProvider, includeNCP bool) (*IPAddressAllocationService,
	error) {
	builder, _ := common.PolicyPathVpcIPAddressAllocation.NewPolicyTreeBuilder()

	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	ipAddressAllocationService := &IPAddressAllocationService{Service: service, VPCService: vpcService, builder: builder}
	ipAddressAllocationService.ipAddressAllocationStore = &IPAddressAllocationStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeIPAddressAllocationCRUID: indexFunc,
			common.IndexByVPCPathFuncKey:            common.IndexByVPCFunc,
		}),
		BindingType: model.VpcIpAddressAllocationBindingType(),
	}}

	if includeNCP {
		wg.Add(2)
		go ipAddressAllocationService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeIPAddressAllocation,
			[]model.Tag{{Scope: String(common.TagScopeNCPCluster), Tag: String(service.NSXClient.NsxConfig.Cluster)}},
			ipAddressAllocationService.ipAddressAllocationStore)
	} else {
		wg.Add(1)
	}
	go ipAddressAllocationService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeIPAddressAllocation,
		[]model.Tag{{Scope: String(common.TagScopeCluster), Tag: String(service.NSXClient.NsxConfig.Cluster)}},
		ipAddressAllocationService.ipAddressAllocationStore)

	go func() {
		wg.Wait()
		close(wgDone)
	}()
	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return ipAddressAllocationService, err
	}
	return ipAddressAllocationService, nil
}

func (service *IPAddressAllocationService) CreateOrUpdateIPAddressAllocation(obj *v1alpha1.IPAddressAllocation) (bool, error) {
	nsxIPAddressAllocation, err := service.BuildIPAddressAllocation(obj)
	if err != nil {
		return false, err
	}
	existingIPAddressAllocation, err := service.indexedIPAddressAllocation(obj.UID)
	if err != nil {
		log.Error(err, "Failed to get ipaddressallocation", "UID", obj.UID)
		return false, err
	}
	log.V(1).Info("Existing ipaddressallocation", "ipaddressallocation", existingIPAddressAllocation)
	ipAddressAllocationUpdated := common.CompareResource(IpAddressAllocationToComparable(existingIPAddressAllocation),
		IpAddressAllocationToComparable(nsxIPAddressAllocation))

	if !ipAddressAllocationUpdated {
		log.Info("Ipaddressallocation is not changed", "UID", obj.UID)
		return false, nil
	}

	if err := service.Apply(nsxIPAddressAllocation); err != nil {
		return false, err
	}

	createdIPAddressAllocation, err := service.indexedIPAddressAllocation(obj.UID)
	if err != nil {
		log.Error(err, "Failed to get created ipaddressallocation", "UID", obj.UID)
		return false, err
	}
	allocation_ips := createdIPAddressAllocation.AllocationIps
	if allocation_ips == nil {
		return false, fmt.Errorf("ipaddressallocation %s didn't realize available allocation_ips", obj.UID)
	}
	obj.Status.AllocationIPs = *allocation_ips
	return true, nil
}

func (service *IPAddressAllocationService) Apply(nsxIPAddressAllocation *model.VpcIpAddressAllocation) error {
	ns := service.GetIPAddressAllocationNamespace(nsxIPAddressAllocation)
	VPCInfo := service.VPCService.ListVPCInfo(ns)
	if len(VPCInfo) == 0 {
		err := util.NoEffectiveOption{Desc: "no valid org and project for ipaddressallocation"}
		log.Error(err, "Failed to list VPCInfo for IPAddressAllocation")
		return err
	}
	errPatch := service.NSXClient.IPAddressAllocationClient.Patch(VPCInfo[0].OrgID, VPCInfo[0].ProjectID, VPCInfo[0].ID, *nsxIPAddressAllocation.Id, *nsxIPAddressAllocation)
	errPatch = util.TransNSXApiError(errPatch)
	if errPatch != nil {
		// not return err, try to get it from nsx, in case if cidr not realized at the first time
		// so it can be patched in the next time and reacquire cidr
		log.Error(errPatch, "Patch failed, try to get it from nsx", "nsxIPAddressAllocation", nsxIPAddressAllocation)
	}
	// get back from nsx, it contains path which is used to parse vpc info when deleting
	nsxIPAddressAllocationNew, errGet := service.NSXClient.IPAddressAllocationClient.Get(VPCInfo[0].OrgID, VPCInfo[0].ProjectID, VPCInfo[0].ID, *nsxIPAddressAllocation.Id)
	errGet = util.TransNSXApiError(errGet)
	if errGet != nil {
		if errPatch != nil {
			return fmt.Errorf("error get %s, error patch %s", errGet.Error(), errPatch.Error())
		}
		return errGet
	}
	if nsxIPAddressAllocationNew.AllocationIps == nil {
		err := fmt.Errorf("cidr not realized yet")
		return err
	}
	err := service.ipAddressAllocationStore.Apply(&nsxIPAddressAllocationNew)
	if err != nil {
		return err
	}
	log.V(1).Info("Successfully created or updated ipaddressallocation", "nsxIPAddressAllocation", nsxIPAddressAllocation)
	return nil
}

func (service *IPAddressAllocationService) DeleteIPAddressAllocation(obj interface{}) error {
	var err error
	var nsxIPAddressAllocation *model.VpcIpAddressAllocation
	switch o := obj.(type) {
	case *v1alpha1.IPAddressAllocation:
		nsxIPAddressAllocation, err = service.indexedIPAddressAllocation(o.UID)
		if err != nil {
			log.Error(err, "Failed to get ipaddressallocation", "IPAddressAllocation", o)
		}
	case types.UID:
		nsxIPAddressAllocation, err = service.indexedIPAddressAllocation(o)
		if err != nil {
			log.Error(err, "Failed to get ipaddressallocation by UID", "UID", o)
		}
	case string:
		ok := false
		obj = service.ipAddressAllocationStore.GetByKey(o)
		nsxIPAddressAllocation, ok = obj.(*model.VpcIpAddressAllocation)
		if !ok {
			log.Error(err, "Failed to get ipaddressallocation by key", "key", o)
		}
	case model.VpcIpAddressAllocation:
		nsxIPAddressAllocation = &o
	}
	if nsxIPAddressAllocation == nil {
		log.Error(nil, "Failed to get ipaddressallocation from store, skip")
		return nil
	}
	vpcResourceInfo, err := common.ParseVPCResourcePath(*nsxIPAddressAllocation.Path)
	if err != nil {
		return err
	}
	err = service.NSXClient.IPAddressAllocationClient.Delete(vpcResourceInfo.OrgID, vpcResourceInfo.ProjectID, vpcResourceInfo.VPCID, *nsxIPAddressAllocation.Id)
	if err != nil {
		return err
	}
	nsxIPAddressAllocation.MarkedForDelete = &MarkedForDelete
	err = service.ipAddressAllocationStore.Apply(nsxIPAddressAllocation)
	if err != nil {
		return err
	}
	log.Info("Successfully deleted nsxIPAddressAllocation", "nsxIPAddressAllocation", nsxIPAddressAllocation)
	return nil
}

func (service *IPAddressAllocationService) DeleteIPAddressAllocationByNamespacedName(namespace, name string) error {
	// NamespacedName is a unique identity in store as only one worker can deal with the NamespacedName at a time
	allIPAddressAllocations := service.ipAddressAllocationStore.List()
	var targetIPAddressAllocation *model.VpcIpAddressAllocation

	for _, obj := range allIPAddressAllocations {
		ipAddressAllocation, ok := obj.(*model.VpcIpAddressAllocation)
		if !ok {
			continue
		}

		namespaceMatch, nameMatch := false, false
		for _, tag := range ipAddressAllocation.Tags {
			if *tag.Scope == common.TagScopeNamespace && *tag.Tag == namespace {
				namespaceMatch = true
			}
			if *tag.Scope == common.TagScopeIPAddressAllocationCRName && *tag.Tag == name {
				nameMatch = true
			}
		}

		if namespaceMatch && nameMatch {
			targetIPAddressAllocation = ipAddressAllocation
			err := service.DeleteIPAddressAllocation(*targetIPAddressAllocation)
			if err != nil {
				log.Error(err, "Failed to delete IPAddressAllocation", "Namespace", namespace, "Name", name)
				return err
			}
		}
	}
	return nil
}

func (service *IPAddressAllocationService) ListIPAddressAllocationID() sets.Set[string] {
	ipAddressAllocationSet := service.ipAddressAllocationStore.ListIndexFuncValues(common.TagScopeIPAddressAllocationCRUID)
	return ipAddressAllocationSet
}

func (service *IPAddressAllocationService) ListIPAddressAllocationKeys() []string {
	ipAddressAllocationKeys := service.ipAddressAllocationStore.ListKeys()
	return ipAddressAllocationKeys
}

func (service *IPAddressAllocationService) GetIPAddressAllocationNamespace(nsxIPAddressAllocation *model.VpcIpAddressAllocation) string {
	for _, tag := range nsxIPAddressAllocation.Tags {
		if *tag.Scope == common.TagScopeNamespace {
			return *tag.Tag
		}
	}
	return ""
}

func (service *IPAddressAllocationService) GetIPAddressAllocationUID(nsxIPAddressAllocation *model.VpcIpAddressAllocation) string {
	for _, tag := range nsxIPAddressAllocation.Tags {
		if *tag.Scope == common.TagScopeIPAddressAllocationCRUID {
			return *tag.Tag
		}
	}
	return ""
}
