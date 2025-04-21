package subnetbinding

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type BindingStore struct {
	common.ResourceStore
}

const (
	parentSubnetIndexKey     = "parentSubnet"
	childSubnetIndexKey      = "childSubnet"
	bindingMapCRUIDIndexKey  = "bindingMapCRUID"
	bindingMapCRNameIndexKey = "bindingMapCRName"
)

func (s *BindingStore) GetByIndex(key string, value string) []*model.SubnetConnectionBindingMap {
	bindings := make([]*model.SubnetConnectionBindingMap, 0)
	objs := s.ResourceStore.GetByIndex(key, value)
	for _, binding := range objs {
		bindings = append(bindings, binding.(*model.SubnetConnectionBindingMap))
	}
	return bindings
}

func (s *BindingStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	binding := i.(*model.SubnetConnectionBindingMap)
	if binding.MarkedForDelete != nil && *binding.MarkedForDelete {
		err := s.Delete(binding)
		if err != nil {
			log.Error(err, "Failed to delete SubnetConnectionBindingMap", "subnetConnectionBindingMap", binding)
			return err
		}
		log.Info("Deleted SubnetConnectionBindingMap from store", "subnetConnectionBindingMap", binding)
	} else {
		err := s.Add(binding)
		if err != nil {
			log.Error(err, "Failed to add SubnetConnectionBindingMap", "subnetConnectionBindingMap", binding)
			return err
		}
		log.Info("Added SubnetConnectionBindingMap to store", "subnetConnectionBindingMap", binding)
	}
	return nil
}

func (s *BindingStore) GetByKey(key string) *model.SubnetConnectionBindingMap {
	obj := s.ResourceStore.GetByKey(key)
	if obj == nil {
		return nil
	}
	binding := obj.(*model.SubnetConnectionBindingMap)
	return binding
}

func (s *BindingStore) getBindingsByParentSubnet(subnetPath string) []*model.SubnetConnectionBindingMap {
	return s.GetByIndex(parentSubnetIndexKey, subnetPath)
}

func (s *BindingStore) getBindingsByChildSubnet(subnetPath string) []*model.SubnetConnectionBindingMap {
	return s.GetByIndex(childSubnetIndexKey, subnetPath)
}

func (s *BindingStore) getBindingsByBindingMapCRUID(bindingMapUID string) []*model.SubnetConnectionBindingMap {
	return s.GetByIndex(bindingMapCRUIDIndexKey, bindingMapUID)
}

func (s *BindingStore) getBindingsByBindingMapCRName(bindingName string, bindingNamespace string) []*model.SubnetConnectionBindingMap {
	nn := types.NamespacedName{Name: bindingName, Namespace: bindingNamespace}
	return s.GetByIndex(bindingMapCRNameIndexKey, nn.String())
}

func (s *BindingStore) DeleteMultipleObjects(bindingMaps []*model.SubnetConnectionBindingMap) {
	for _, bindingMap := range bindingMaps {
		s.Delete(bindingMap)
	}
}

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.SubnetConnectionBindingMap:
		return *v.Id, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func bindingMapCRUIDIndexFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.SubnetConnectionBindingMap:
		for _, tag := range o.Tags {
			if *tag.Scope == common.TagScopeSubnetBindingCRUID {
				return []string{*tag.Tag}, nil
			}
		}
		return []string{}, nil
	default:
		return nil, errors.New("bindingMapCRUIDIndexFunc doesn't support unknown type")
	}
}

func bindingMapCRNameIndexFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.SubnetConnectionBindingMap:
		var res []string
		var crName, crNamespace string
		for _, tag := range o.Tags {
			if *tag.Scope == common.TagScopeSubnetBindingCRName {
				crName = *tag.Tag
			} else if *tag.Scope == common.TagScopeNamespace {
				crNamespace = *tag.Tag
			}
		}
		if crName != "" && crNamespace != "" {
			res = append(res, types.NamespacedName{Name: crName, Namespace: crNamespace}.String())
		}
		return res, nil
	default:
		return nil, errors.New("bindingMapCRNameIndexFunc doesn't support unknown type")
	}
}

func childSubnetIndexFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.SubnetConnectionBindingMap:
		if o.ParentPath != nil {
			return []string{*o.ParentPath}, nil
		}
		return []string{}, nil
	default:
		return nil, errors.New("childSubnetIndexFunc doesn't support unknown type")
	}
}

func parentSubnetIndexFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.SubnetConnectionBindingMap:
		if o.SubnetPath != nil {
			return []string{*o.SubnetPath}, nil
		}
		return []string{}, nil
	default:
		return nil, errors.New("parentSubnetIndexFunc doesn't support unknown type")
	}
}

func SetupStore() *BindingStore {
	return &BindingStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(
			keyFunc, cache.Indexers{
				bindingMapCRUIDIndexKey:      bindingMapCRUIDIndexFunc,
				bindingMapCRNameIndexKey:     bindingMapCRNameIndexFunc,
				childSubnetIndexKey:          childSubnetIndexFunc,
				parentSubnetIndexKey:         parentSubnetIndexFunc,
				common.IndexByVPCPathFuncKey: common.IndexByVPCFunc,
			}),
		BindingType: model.SubnetConnectionBindingMapBindingType(),
	}}
}
