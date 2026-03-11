package subnetipreservation

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type DynamicIPReservationStore struct {
	common.ResourceStore
}

type StaticIPReservationStore struct {
	common.ResourceStore
}

func (s *DynamicIPReservationStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	ipr := i.(*model.DynamicIpAddressReservation)
	if ipr.MarkedForDelete != nil && *ipr.MarkedForDelete {
		err := s.Delete(ipr)
		if err != nil {
			log.Error(err, "Failed to delete DynamicIPReservation", "DynamicIPReservation", ipr)
			return err
		}
		log.Debug("Deleted DynamicIPReservation from store", "DynamicIPReservation", ipr)
	} else {
		err := s.Add(ipr)
		if err != nil {
			log.Error(err, "Failed to add DynamicIPReservation", "DynamicIPReservation", ipr)
			return err
		}
		log.Debug("Added DynamicIPReservation to store", "DynamicIPReservation", ipr)
	}
	return nil
}

func (s *StaticIPReservationStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	ipr := i.(*model.StaticIpAddressReservation)
	if ipr.MarkedForDelete != nil && *ipr.MarkedForDelete {
		err := s.Delete(ipr)
		if err != nil {
			log.Error(err, "Failed to delete StaticIPReservation", "StaticIPReservation", ipr)
			return err
		}
		log.Debug("Deleted StaticIPReservation from store", "StaticIPReservation", ipr)
	} else {
		err := s.Add(ipr)
		if err != nil {
			log.Error(err, "Failed to add StaticIPReservation", "StaticIPReservation", ipr)
			return err
		}
		log.Debug("Added StaticIPReservation to store", "StaticIPReservation", ipr)
	}
	return nil
}

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.DynamicIpAddressReservation:
		return *v.Id, nil
	case *model.StaticIpAddressReservation:
		return *v.Id, nil
	case string:
		return v, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func ipReservationCRUIDIndexFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.DynamicIpAddressReservation:
		for _, tag := range o.Tags {
			if *tag.Scope == common.TagScopeSubnetIPReservationCRUID {
				return []string{*tag.Tag}, nil
			}
		}
		return []string{}, nil
	case *model.StaticIpAddressReservation:
		for _, tag := range o.Tags {
			if *tag.Scope == common.TagScopeSubnetIPReservationCRUID {
				return []string{*tag.Tag}, nil
			}
		}
		return []string{}, nil
	default:
		return nil, errors.New("ipReservationCRUIDIndexFunc doesn't support unknown type")
	}
}

func ipReservationCRNameIndexFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.DynamicIpAddressReservation:
		var res []string
		var crName, crNamespace string
		for _, tag := range o.Tags {
			switch *tag.Scope {
			case common.TagScopeSubnetIPReservationCRName:
				crName = *tag.Tag
			case common.TagScopeNamespace:
				crNamespace = *tag.Tag
			}
		}
		if crName != "" && crNamespace != "" {
			res = append(res, types.NamespacedName{Name: crName, Namespace: crNamespace}.String())
		}
		return res, nil
	case *model.StaticIpAddressReservation:
		var res []string
		var crName, crNamespace string
		for _, tag := range o.Tags {
			switch *tag.Scope {
			case common.TagScopeSubnetIPReservationCRName:
				crName = *tag.Tag
			case common.TagScopeNamespace:
				crNamespace = *tag.Tag
			}
		}
		if crName != "" && crNamespace != "" {
			res = append(res, types.NamespacedName{Name: crName, Namespace: crNamespace}.String())
		}
		return res, nil
	default:
		return nil, errors.New("ipReservationCRNameIndexFunc doesn't support unknown type")
	}
}

func (s *DynamicIPReservationStore) GetByKey(key string) *model.DynamicIpAddressReservation {
	var ipReservation *model.DynamicIpAddressReservation
	obj := s.ResourceStore.GetByKey(key)
	if obj != nil {
		ipReservation = obj.(*model.DynamicIpAddressReservation)
	}
	return ipReservation
}

func (s *StaticIPReservationStore) GetByKey(key string) *model.StaticIpAddressReservation {
	var ipReservation *model.StaticIpAddressReservation
	obj := s.ResourceStore.GetByKey(key)
	if obj != nil {
		ipReservation = obj.(*model.StaticIpAddressReservation)
	}
	return ipReservation
}

func (s *DynamicIPReservationStore) GetByIndex(key string, value string) []*model.DynamicIpAddressReservation {
	ipReservations := make([]*model.DynamicIpAddressReservation, 0)
	objs := s.ResourceStore.GetByIndex(key, value)
	for _, ipReservation := range objs {
		ipReservations = append(ipReservations, ipReservation.(*model.DynamicIpAddressReservation))
	}
	return ipReservations
}

func (s *StaticIPReservationStore) GetByIndex(key string, value string) []*model.StaticIpAddressReservation {
	ipReservations := make([]*model.StaticIpAddressReservation, 0)
	objs := s.ResourceStore.GetByIndex(key, value)
	for _, ipReservation := range objs {
		ipReservations = append(ipReservations, ipReservation.(*model.StaticIpAddressReservation))
	}
	return ipReservations
}

func (s *DynamicIPReservationStore) DeleteMultipleObjects(iprs []*model.DynamicIpAddressReservation) {
	for _, ipr := range iprs {
		s.Delete(ipr)
	}
}

func (s *StaticIPReservationStore) DeleteMultipleObjects(iprs []*model.StaticIpAddressReservation) {
	for _, ipr := range iprs {
		s.Delete(ipr)
	}
}

func SetupDynamicIPReservationStore() *DynamicIPReservationStore {
	return &DynamicIPReservationStore{
		ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc, cache.Indexers{
					common.TagScopeSubnetIPReservationCRUID:  ipReservationCRUIDIndexFunc,
					common.TagScopeSubnetIPReservationCRName: ipReservationCRNameIndexFunc,
				}),
			BindingType: model.DynamicIpAddressReservationBindingType(),
		},
	}
}

func SetupStaticIPReservationStore() *StaticIPReservationStore {
	return &StaticIPReservationStore{
		ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc, cache.Indexers{
					common.TagScopeSubnetIPReservationCRUID:  ipReservationCRUIDIndexFunc,
					common.TagScopeSubnetIPReservationCRName: ipReservationCRNameIndexFunc,
				}),
			BindingType: model.StaticIpAddressReservationBindingType(),
		},
	}
}
