package ipaddressallocation

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.VpcIpAddressAllocation:
		return *v.Id, nil
	case *model.GenericPolicyRealizedResource:
		return *v.Id, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func indexByIPAddressAllocation(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch v := obj.(type) {
	case *model.VpcIpAddressAllocation:
		return filterTag(v.Tags, common.TagScopeIPAddressAllocationCRUID), nil
	case *model.GenericPolicyRealizedResource:
		return filterTag(v.Tags, common.TagScopeIPAddressAllocationCRUID), nil
	default:
		return res, errors.New("indexByIPAddressAllocation doesn't support unknown type")
	}
}

func indexByAddressBinding(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch v := obj.(type) {
	case *model.VpcIpAddressAllocation:
		return filterTag(v.Tags, common.TagScopeAddressBindingCRUID), nil
	case *model.GenericPolicyRealizedResource:
		return filterTag(v.Tags, common.TagScopeAddressBindingCRUID), nil
	default:
		return res, errors.New("indexByAddressBinding doesn't support unknown type")
	}
}

func indexBySubnetPort(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch v := obj.(type) {
	case *model.VpcIpAddressAllocation:
		return filterTag(v.Tags, common.TagScopeSubnetPortCRUID), nil
	case *model.GenericPolicyRealizedResource:
		return filterTag(v.Tags, common.TagScopeSubnetPortCRUID), nil
	default:
		return res, errors.New("indexByindexBySubnetPort doesn't support unknown type")
	}
}

var filterTag = func(v []model.Tag, scope string) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == scope {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

type IPAddressAllocationStore struct {
	common.ResourceStore
}

func (ipAddressAllocationStore *IPAddressAllocationStore) Apply(i interface{}) error {
	ipAddressAllocation := i.(*model.VpcIpAddressAllocation)
	if ipAddressAllocation.MarkedForDelete != nil && *ipAddressAllocation.MarkedForDelete {
		err := ipAddressAllocationStore.Delete(ipAddressAllocation)
		if err != nil {
			return err
		}
		log.V(1).Info("delete ipAddressAllocation from store", "ipAddressAllocation", ipAddressAllocation)
	} else {
		err := ipAddressAllocationStore.Add(ipAddressAllocation)
		if err != nil {
			return err
		}
		log.V(1).Info("add ipAddressAllocation to store", "ipAddressAllocation", ipAddressAllocation)
	}
	return nil
}

func (service *IPAddressAllocationService) indexedIPAddressAllocation(uid types.UID) (*model.VpcIpAddressAllocation, error) {
	nsxIPAddressAllocation, err := service.ipAddressAllocationStore.GetByUID(uid)
	if err != nil {
		return nil, err
	}
	return nsxIPAddressAllocation, nil
}

func (ipAddressAllocationStore *IPAddressAllocationStore) GetByUID(uid types.UID) (*model.VpcIpAddressAllocation, error) {
	nsxIPAddressAllocation := &model.VpcIpAddressAllocation{}
	indicies := []string{common.TagScopeIPAddressAllocationCRUID, common.TagScopeAddressBindingCRUID, common.TagScopeSubnetPortCRUID}
	var indexResults []interface{}
	for _, index := range indicies {
		indexResult, err := ipAddressAllocationStore.ResourceStore.ByIndex(index, string(uid))
		if err != nil {
			log.Error(err, "Failed to get ipaddressallocation", index, string(uid))
			return nil, err
		}
		indexResults = append(indexResults, indexResult...)
	}

	if len(indexResults) > 0 {
		t := indexResults[0].(*model.VpcIpAddressAllocation)
		nsxIPAddressAllocation = t
	} else {
		log.Info("did not get ipaddressallocation with index", "UID", string(uid))
		return nil, nil
	}
	return nsxIPAddressAllocation, nil
}

func (ipAddressAllocationStore *IPAddressAllocationStore) GetByVPCPath(vpcPath string) ([]*model.VpcIpAddressAllocation, error) {
	objs, err := ipAddressAllocationStore.ResourceStore.ByIndex(common.IndexByVPCPathFuncKey, vpcPath)
	if err != nil {
		return nil, err
	}
	allocations := make([]*model.VpcIpAddressAllocation, len(objs))
	for i, obj := range objs {
		allocation := obj.(*model.VpcIpAddressAllocation)
		allocations[i] = allocation
	}
	return allocations, nil
}

func (ipAddressAllocationStore *IPAddressAllocationStore) DeleteMultipleObjects(allocations []*model.VpcIpAddressAllocation) {
	for _, allocation := range allocations {
		ipAddressAllocationStore.Delete(allocation)
	}
}

func buildIPAddressAllocationStore() *IPAddressAllocationStore {
	return &IPAddressAllocationStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeIPAddressAllocationCRUID: indexByIPAddressAllocation,
			common.TagScopeAddressBindingCRUID:      indexByAddressBinding,
			common.TagScopeSubnetPortCRUID:          indexBySubnetPort,
			common.IndexByVPCPathFuncKey:            common.IndexByVPCFunc,
		}),
		BindingType: model.VpcIpAddressAllocationBindingType(),
	}}
}
