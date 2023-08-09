package subnet

import (
	"errors"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

// keyFunc is used to get the key of a resource, usually, which is the ID of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case model.VpcSubnet:
		return *v.Id, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func filterTag(tags []model.Tag, tagScope string) []string {
	var res []string
	for _, tag := range tags {
		if *tag.Scope == tagScope {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

// subnetIndexFunc is used to filter out NSX Subnets which are tagged with CR UID.
func subnetIndexFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case model.VpcSubnet:
		return filterTag(o.Tags, common.TagScopeSubnetCRUID), nil
	default:
		return nil, errors.New("subnetIndexFunc doesn't support unknown type")
	}
}

// SubnetStore is a store for subnet.
type SubnetStore struct {
	common.ResourceStore
}

func (subnetStore *SubnetStore) Operate(i interface{}) error {
	if i == nil {
		return nil
	}
	subnet := i.(*model.VpcSubnet)
	if subnet.MarkedForDelete != nil && *subnet.MarkedForDelete {
		if err := subnetStore.Delete(*subnet); err != nil {
			return err
		}
		log.Info("Subnet deleted from store", "Subnet", subnet)
	} else {
		if err := subnetStore.Add(*subnet); err != nil {
			return err
		}
		log.Info("Subnet added to store", "Subnet", subnet)
	}
	return nil
}

func (subnetStore *SubnetStore) GetByIndex(key string, value string) []model.VpcSubnet {
	subnets := make([]model.VpcSubnet, 0)
	objs := subnetStore.ResourceStore.GetByIndex(key, value)
	for _, subnet := range objs {
		subnets = append(subnets, subnet.(model.VpcSubnet))
	}
	return subnets
}

func (subnetStore *SubnetStore) GetByKey(key string) *model.VpcSubnet {
	obj := subnetStore.ResourceStore.GetByKey(key)
	if obj == nil {
		return nil
	}
	subnet := obj.(model.VpcSubnet)
	return &subnet
}
