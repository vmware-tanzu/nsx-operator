package subnetipreservation

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type IPReservationStore struct {
	common.ResourceStore
}

func (s *IPReservationStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	ipr := i.(*model.DynamicIpAddressReservation)
	if ipr.MarkedForDelete != nil && *ipr.MarkedForDelete {
		err := s.Delete(ipr)
		if err != nil {
			log.Error(err, "Failed to delete SubnetIPReservation", "SubnetIPReservation", ipr)
			return err
		}
		log.Debug("Deleted SubnetIPReservation from store", "SubnetIPReservation", ipr)
	} else {
		err := s.Add(ipr)
		if err != nil {
			log.Error(err, "Failed to add SubnetIPReservation", "SubnetIPReservation", ipr)
			return err
		}
		log.Debug("Added SubnetIPReservation to store", "SubnetIPReservation", ipr)
	}
	return nil
}

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.DynamicIpAddressReservation:
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
	default:
		return nil, errors.New("ipReservationCRNameIndexFunc doesn't support unknown type")
	}
}

func (s *IPReservationStore) GetByKey(key string) *model.DynamicIpAddressReservation {
	var ipReservation *model.DynamicIpAddressReservation
	obj := s.ResourceStore.GetByKey(key)
	if obj != nil {
		ipReservation = obj.(*model.DynamicIpAddressReservation)
	}
	return ipReservation
}

func (s *IPReservationStore) GetByIndex(key string, value string) []*model.DynamicIpAddressReservation {
	ipReservations := make([]*model.DynamicIpAddressReservation, 0)
	objs := s.ResourceStore.GetByIndex(key, value)
	for _, ipReservation := range objs {
		ipReservations = append(ipReservations, ipReservation.(*model.DynamicIpAddressReservation))
	}
	return ipReservations
}

func (s *IPReservationStore) DeleteMultipleObjects(iprs []*model.DynamicIpAddressReservation) {
	for _, ipr := range iprs {
		s.Delete(ipr)
	}
}

func SetupStore() *IPReservationStore {
	return &IPReservationStore{
		ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc, cache.Indexers{
					ipReservationCRUIDIndexKey:  ipReservationCRUIDIndexFunc,
					ipReservationCRNameIndexKey: ipReservationCRNameIndexFunc,
				}),
			BindingType: model.DynamicIpAddressReservationBindingType(),
		},
	}
}
