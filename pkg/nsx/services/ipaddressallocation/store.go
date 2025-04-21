package ipaddressallocation

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"

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

func indexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch v := obj.(type) {
	case *model.VpcIpAddressAllocation:
		return filterTag(v.Tags), nil
	case *model.GenericPolicyRealizedResource:
		return filterTag(v.Tags), nil
	default:
		return res, errors.New("indexFunc doesn't support unknown type")
	}
}

var filterTag = func(v []model.Tag) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == common.TagScopeIPAddressAllocationCRUID {
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
	nsxIPAddressAllocation, err := service.ipAddressAllocationStore.GetByIndex(uid)
	if err != nil {
		return nil, err
	}
	return nsxIPAddressAllocation, nil
}

func (ipAddressAllocationStore *IPAddressAllocationStore) GetByIndex(uid types.UID) (*model.VpcIpAddressAllocation, error) {
	nsxIPAddressAllocation := &model.VpcIpAddressAllocation{}
	indexResults, err := ipAddressAllocationStore.ResourceStore.ByIndex(common.TagScopeIPAddressAllocationCRUID, string(uid))
	if err != nil {
		log.Error(err, "failed to get ipaddressallocation", "UID", string(uid))
		return nil, err
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
