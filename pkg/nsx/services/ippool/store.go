package ippool

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case model.IpAddressPool:
		return *v.Id, nil
	case model.IpAddressPoolBlockSubnet:
		return *v.Id, nil
	case model.GenericPolicyRealizedResource:
		return *v.Id, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func indexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch v := obj.(type) {
	case model.IpAddressPoolBlockSubnet:
		return filterTag(v.Tags), nil
	case model.IpAddressPool:
		return filterTag(v.Tags), nil
	default:
		return res, errors.New("indexFunc doesn't support unknown type")
	}
}

var filterTag = func(v []model.Tag) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == common.TagScopeIPPoolCRUID {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

type IPPoolStore struct {
	common.ResourceStore
}

type IPPoolBlockSubnetStore struct {
	common.ResourceStore
}

func ipPoolAssertion(i interface{}) interface{} {
	return i.(model.IpAddressPool)
}

func ipPoolBlockSubnetAssertion(i interface{}) interface{} {
	return i.(model.IpAddressPoolBlockSubnet)
}

func (ipPoolStore *IPPoolStore) Operate(i interface{}) error {
	ipPool := i.(*model.IpAddressPool)
	if ipPool.MarkedForDelete != nil && *ipPool.MarkedForDelete {
		err := ipPoolStore.Delete(*ipPool)
		log.V(1).Info("delete ipPool from store", "ipPool", ipPool)
		if err != nil {
			return err
		}
	} else {
		err := ipPoolStore.Add(*ipPool)
		log.V(1).Info("add ipPool to store", "ipPool", ipPool)
		if err != nil {
			return err
		}
	}
	return nil
}

func (ipPoolBlockSubnetStore *IPPoolBlockSubnetStore) Operate(i interface{}) error {
	ipPoolBlockSubnets := i.([]*model.IpAddressPoolBlockSubnet)
	for _, ipPoolBlockSubnet := range ipPoolBlockSubnets {
		if ipPoolBlockSubnet.MarkedForDelete != nil && *ipPoolBlockSubnet.MarkedForDelete {
			err := ipPoolBlockSubnetStore.Delete(*ipPoolBlockSubnet)
			log.V(1).Info("delete ipPoolBlockSubnet from store", "ipPoolBlockSubnet", ipPoolBlockSubnet)
			if err != nil {
				return err
			}
		} else {
			err := ipPoolBlockSubnetStore.Add(*ipPoolBlockSubnet)
			log.V(1).Info("add ipPoolBlockSubnet to store", "ipPoolBlockSubnet", ipPoolBlockSubnet)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (service *IPPoolService) indexedIPPoolAndIPPoolSubnets(uid types.UID) (*model.IpAddressPool, []*model.IpAddressPoolBlockSubnet, error) {
	nsxIPPool, err := service.ipPoolStore.GetByIndex(uid)
	if err != nil {
		return nil, nil, err
	}
	nsxIPPoolSubnets, err := service.ipPoolBlockSubnetStore.GetByIndex(uid)
	if err != nil {
		return nil, nil, err
	}
	return nsxIPPool, nsxIPPoolSubnets, nil
}

func (ipPoolBlockSubnetStore *IPPoolBlockSubnetStore) GetByIndex(uid types.UID) ([]*model.IpAddressPoolBlockSubnet, error) {
	nsxIPSubnets := make([]*model.IpAddressPoolBlockSubnet, 0)
	indexResults, err := ipPoolBlockSubnetStore.ResourceStore.ByIndex(common.TagScopeIPPoolCRUID, string(uid))
	if err != nil {
		log.Error(err, "failed to get ip subnets", "UID", string(uid))
		return nil, err
	}
	if len(indexResults) == 0 {
		log.Info("did not get ip subnets with index", "UID", string(uid))
	}
	for _, ipSubnet := range indexResults {
		t := ipSubnet.(model.IpAddressPoolBlockSubnet)
		nsxIPSubnets = append(nsxIPSubnets, &t)
	}
	return nsxIPSubnets, nil
}

func (ipPoolStore *IPPoolStore) GetByIndex(uid types.UID) (*model.IpAddressPool, error) {
	nsxIPPool := &model.IpAddressPool{}
	indexResults, err := ipPoolStore.ResourceStore.ByIndex(common.TagScopeIPPoolCRUID, string(uid))
	if err != nil {
		log.Error(err, "failed to get ip pool", "UID", string(uid))
		return nil, err
	}
	if len(indexResults) > 0 {
		t := indexResults[0].(model.IpAddressPool)
		nsxIPPool = &t
	} else {
		log.Info("did not get ip pool with index", "UID", string(uid))
	}
	return nsxIPPool, nil
}
