package ipaddressallocation

import (
	"context"
	"fmt"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log                             = logger.Log
	MarkedForDelete                 = true
	ResourceTypeIPAddressAllocation = common.ResourceTypeIPAddressAllocation
)

type IPAddressAllocationService struct {
	common.Service
	ipAddressAllocationStore *IPAddressAllocationStore
	ExhaustedIPBlock         []string
	VPCService               common.VPCServiceProvider
}

func InitializeIPAddressAllocation(service common.Service, vpcService common.VPCServiceProvider) (*IPAddressAllocationService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(1)

	ipAddressAllocationService := &IPAddressAllocationService{Service: service, VPCService: vpcService}
	ipAddressAllocationService.ipAddressAllocationStore = &IPAddressAllocationStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPAddressAllocationCRUID: indexFunc}),
		BindingType: model.IpAddressPoolBindingType(),
	}}

	tags := []model.Tag{
		{Scope: String(common.TagScopeIPAddressAllocationCRUID)},
	}
	go ipAddressAllocationService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeIPAddressAllocation, tags, ipAddressAllocationService.ipAddressAllocationStore)

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

func (service *IPAddressAllocationService) CreateOrUpdateIPAddressAllocation(obj *v1alpha1.IPAddressAllocation) error {
	nsxIPAddressAllocation, err := service.BuildIPAddressAllocation(obj)
	if err != nil {
		return err
	}
	existingIPAddressAllocation, err := service.indexedIPAddressAllocation(obj.UID)
	if err != nil {
		log.Error(err, "failed to get ipaddressallocation", "UID", obj.UID)
		return err
	}
	log.V(1).Info("existing ipaddressallocation", "ipaddressallocation", existingIPAddressAllocation)
	ipAddressAllocationUpdated := common.CompareResource(IpAddressAllocationToComparable(existingIPAddressAllocation),
		IpAddressAllocationToComparable(nsxIPAddressAllocation))

	if err := service.Apply(nsxIPAddressAllocation, ipAddressAllocationUpdated); err != nil {
		return err
	}

	cidr, e := service.acquireCidr(obj, nsxIPAddressAllocation)
	if e != nil {
		return e
	}
	if cidr == nil {
		return fmt.Errorf("ipaddressallocation %s didn't realize available cidr", obj.UID)
	}
	return nil
}

func (service *IPAddressAllocationService) Apply(nsxIPAddressAllocation *model.VpcIpAddressAllocation, ipAddressAllocationUpdated bool) error {
	if !ipAddressAllocationUpdated {
		return nil
	}
	ns := service.GetIPAddressAllocationNamespace(nsxIPAddressAllocation)
	var err error
	VPCInfo := service.VPCService.ListVPCInfo(ns)
	if len(VPCInfo) == 0 {
		err = util.NoEffectiveOption{Desc: "no valid org and project for ipaddressallocation"}
	} else {
		err := service.NSXClient.IPAddressAllocationClient.Patch(VPCInfo[0].OrgID, VPCInfo[0].ProjectID, VPCInfo[0].ID, *nsxIPAddressAllocation.Id, *nsxIPAddressAllocation)
		if err != nil {
			return err
		}
	}
	err = service.ipAddressAllocationStore.Apply(nsxIPAddressAllocation)
	if err != nil {
		return err
	}
	log.V(1).Info("successfully created or updated ipaddressallocation", "nsxIPAddressAllocation", nsxIPAddressAllocation)
	return nil
}

func (service *IPAddressAllocationService) DeleteIPAddressAllocation(obj interface{}) error {
	var err error
	var nsxIPAddressAllocation *model.VpcIpAddressAllocation
	switch o := obj.(type) {
	case *v1alpha1.IPAddressAllocation:
		nsxIPAddressAllocation, err = service.BuildIPAddressAllocation(o)
		if err != nil {
			log.Error(err, "failed to build ipaddressallocation", "IPAddressAllocation", o)
			return err
		}
	case types.UID:
		nsxIPAddressAllocation, err = service.indexedIPAddressAllocation(o)
		if err != nil {
			log.Error(err, "failed to get ipaddressallocation by UID", "UID", o)
			return err
		}
	}
	ns := service.GetIPAddressAllocationNamespace(nsxIPAddressAllocation)
	VPCInfo := service.VPCService.ListVPCInfo(ns)
	if len(VPCInfo) == 0 {
		err = util.NoEffectiveOption{Desc: "no valid org and project for ipaddressallocation"}
	} else {
		err := service.NSXClient.IPAddressAllocationClient.Delete(VPCInfo[0].OrgID, VPCInfo[0].ProjectID, VPCInfo[0].ID, *nsxIPAddressAllocation.Id)
		if err != nil {
			return err
		}
		nsxIPAddressAllocation.MarkedForDelete = &MarkedForDelete
		err = service.ipAddressAllocationStore.Apply(nsxIPAddressAllocation)
		if err != nil {
			return err
		}
	}
	log.V(1).Info("successfully deleted nsxIPAddressAllocation", "nsxIPAddressAllocation", nsxIPAddressAllocation)
	return nil
}

func (service *IPAddressAllocationService) acquireCidr(obj *v1alpha1.IPAddressAllocation, nsxIPAddressAllocation *model.VpcIpAddressAllocation) (*string, error) {
	VPCInfo := service.VPCService.ListVPCInfo(obj.Namespace)
	var err error
	if len(VPCInfo) == 0 {
		err = util.NoEffectiveOption{Desc: "no effective org and project for ippool"}
		return nil, err
	}
	m, err := service.NSXClient.IPAddressAllocationClient.Get(VPCInfo[0].OrgID, VPCInfo[0].ProjectID, VPCInfo[0].ID, *nsxIPAddressAllocation.Id)
	if err != nil {
		return nil, err
	}
	return m.AllocationIp, nil
}

func (service *IPAddressAllocationService) ListIPAddressAllocationID() sets.Set[string] { //nolint:staticcheck // Ignore the deprecation warning for sets.String
	ipAddressAllocationSet := service.ipAddressAllocationStore.ListIndexFuncValues(common.TagScopeIPAddressAllocationCRUID)
	return ipAddressAllocationSet
}

func (service *IPAddressAllocationService) GetIPAddressAllocationNamespace(nsxIPAddressAllocation *model.VpcIpAddressAllocation) string {
	for _, tag := range nsxIPAddressAllocation.Tags {
		if *tag.Scope == common.TagScopeNamespace {
			return *tag.Tag
		}
	}
	return ""
}

func (service *IPAddressAllocationService) Cleanup(ctx context.Context) error {
	uids := service.ListIPAddressAllocationID()
	log.Info("cleaning up ipaddressallocation", "count", len(uids))
	for uid := range uids {
		select {
		case <-ctx.Done():
			return util.TimeoutFailed
		default:
			err := service.DeleteIPAddressAllocation(types.UID(uid))
			if err != nil {
				return err
			}
		}
	}
	return nil
}
