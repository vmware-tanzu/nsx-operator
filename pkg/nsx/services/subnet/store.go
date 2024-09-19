package subnet

import (
	"errors"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
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

// subnetIndexFunc is used to filter out NSX Subnets which are tagged with CR UID.
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

// subnetIndexFunc is used to filter out NSX Subnets which are tagged with CR UID.
func subnetSetIndexFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnet:
		return filterTag(o.Tags, common.TagScopeSubnetSetCRUID), nil
	default:
		return nil, errors.New("subnetSetIndexFunc doesn't support unknown type")
	}
}

// SubnetStore is a store for subnet.
type SubnetStore struct {
	common.ResourceStore
	// save locks for subnet by path
	pathLocks sync.Map
}

func (subnetStore *SubnetStore) Add(i interface{}) error {
	subnet := i.(*model.VpcSubnet)
	if subnet.Path == nil {
		log.Info("Store a subnet without path", "subnet", subnet)
		return subnetStore.ResourceStore.Add(i)
	}
	lock := sync.Mutex{}
	subnetStore.pathLocks.LoadOrStore(*subnet.Path, &lock)
	return subnetStore.ResourceStore.Add(i)
}

func (subnetStore *SubnetStore) Delete(i interface{}) error {
	subnet := i.(*model.VpcSubnet)
	if subnet.Path == nil {
		log.Info("Delete a subnet without path", "subnet", subnet)
		return subnetStore.ResourceStore.Delete(i)
	}
	subnetStore.pathLocks.Delete(*subnet.Path)
	return subnetStore.ResourceStore.Delete(i)
}

func (subnetStore *SubnetStore) Lock(path string) {
	lock := sync.Mutex{}
	subnetLock, _ := subnetStore.pathLocks.LoadOrStore(path, &lock)
	subnetLock.(*sync.Mutex).Lock()
}

func (subnetStore *SubnetStore) Unlock(path string) {
	if subnetLock, existed := subnetStore.pathLocks.Load(path); existed {
		subnetLock.(*sync.Mutex).Unlock()
	}
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
	objs := subnetStore.ResourceStore.GetByIndex(key, value)
	for _, subnet := range objs {
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
