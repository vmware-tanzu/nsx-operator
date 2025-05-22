package subnet

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	nsxSubnetNameIndexKey = "nsxSubnetNameIndex"
)

// keyFunc is used to get the key of a resource, usually, which is the ID of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.VpcSubnet:
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

// subnetIndexFunc is used to filter out NSX Subnets, which are tagged with CR UID.
func subnetIndexFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnet:
		return filterTag(o.Tags, common.TagScopeSubnetCRUID), nil
	default:
		return nil, errors.New("subnetIndexFunc doesn't support unknown type")
	}
}

func subnetIndexVMNamespaceFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnet:
		return filterTag(o.Tags, common.TagScopeVMNamespace), nil
	default:
		return nil, errors.New("subnetIndexVMNamespaceFunc doesn't support unknown type")
	}
}

func subnetIndexNamespaceFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnet:
		return filterTag(o.Tags, common.TagScopeNamespace), nil
	default:
		return nil, errors.New("subnetIndexNamespaceFunc doesn't support unknown type")
	}
}

// subnetIndexFunc is used to filter out NSX Subnets, which are tagged with CR UID.
func subnetSetIndexFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnet:
		return filterTag(o.Tags, common.TagScopeSubnetSetCRUID), nil
	default:
		return nil, errors.New("subnetSetIndexFunc doesn't support unknown type")
	}
}

func subnetIndexNameFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnet:
		return []string{*o.DisplayName}, nil
	default:
		return nil, errors.New("subnetIndexNameFunc doesn't support unknown type")
	}
}

// SubnetStore is a store for subnet.
type SubnetStore struct {
	common.ResourceStore
}

func (subnetStore *SubnetStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	subnet := i.(*model.VpcSubnet)
	if subnet.MarkedForDelete != nil && *subnet.MarkedForDelete {
		if err := subnetStore.Delete(subnet); err != nil {
			return err
		}
		log.Info("Subnet deleted from store", "Subnet", subnet)
	} else {
		if err := subnetStore.Add(subnet); err != nil {
			return err
		}
		log.Info("Subnet added to store", "Subnet", subnet)
	}
	return nil
}

func (subnetStore *SubnetStore) GetByIndex(key string, value string) []*model.VpcSubnet {
	subnets := make([]*model.VpcSubnet, 0)
	objects := subnetStore.ResourceStore.GetByIndex(key, value)
	for _, subnet := range objects {
		subnets = append(subnets, subnet.(*model.VpcSubnet))
	}
	return subnets
}

func (subnetStore *SubnetStore) GetByKey(key string) *model.VpcSubnet {
	obj := subnetStore.ResourceStore.GetByKey(key)
	if obj == nil {
		return nil
	}
	subnet := obj.(*model.VpcSubnet)
	return subnet
}

func (subnetStore *SubnetStore) DeleteMultipleObjects(subnets []*model.VpcSubnet) {
	for _, subnet := range subnets {
		_ = subnetStore.Delete(subnet)
	}
}

func buildSubnetStore() *SubnetStore {
	return &SubnetStore{
		ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
				common.TagScopeSubnetCRUID:    subnetIndexFunc,
				common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
				common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
				common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				common.IndexByVPCPathFuncKey:  common.IndexByVPCFunc,
				nsxSubnetNameIndexKey:         subnetIndexNameFunc,
			}),
			BindingType: model.VpcSubnetBindingType(),
		},
	}
}
