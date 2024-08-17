package subnetport

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// keyFunc is used to get the key of a resource, usually, which is the ID of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.VpcSubnetPort:
		return *v.Id, nil
	case types.UID:
		return string(v), nil
	case string:
		return v, nil
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

// subnetPortIndexByCRUID is used to get index of a resource, usually, which is the UID of the CR controller reconciles,
// index is used to filter out resources which are related to the CR
func subnetPortIndexByCRUID(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnetPort:
		return filterTag(o.Tags, common.TagScopeSubnetPortCRUID), nil
	default:
		return nil, errors.New("subnetPortIndexByCRUID doesn't support unknown type")
	}
}

func subnetPortIndexByPodUID(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnetPort:
		return filterTag(o.Tags, common.TagScopePodUID), nil
	default:
		return nil, errors.New("subnetPortIndexByCRUID doesn't support unknown type")
	}
}

func subnetPortIndexBySubnetID(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnetPort:
		vpcInfo, err := common.ParseVPCResourcePath(*o.Path)
		if err != nil {
			return nil, err
		}
		return []string{vpcInfo.ParentID}, nil

	default:
		return nil, errors.New("subnetPortIndexBySubnetID doesn't support unknown type")
	}
}

// SubnetPortStore is a store for SubnetPorts
type SubnetPortStore struct {
	common.ResourceStore
}

func (vs *SubnetPortStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	subnetPort := i.(*model.VpcSubnetPort)
	if subnetPort.MarkedForDelete != nil && *subnetPort.MarkedForDelete {
		err := vs.Delete(subnetPort)
		log.V(1).Info("delete SubnetPort from store", "subnetport", subnetPort)
		if err != nil {
			return err
		}
	} else {
		err := vs.Add(subnetPort)
		log.V(1).Info("add SubnetPort to store", "subnetport", subnetPort)
		if err != nil {
			return err
		}
	}
	return nil
}

func (subnetPortStore *SubnetPortStore) GetByKey(key string) *model.VpcSubnetPort {
	var subnetPort *model.VpcSubnetPort
	obj := subnetPortStore.ResourceStore.GetByKey(key)
	if obj != nil {
		subnetPort = obj.(*model.VpcSubnetPort)
	}
	return subnetPort
}

func (subnetPortStore *SubnetPortStore) GetByIndex(key string, value string) []*model.VpcSubnetPort {
	subnetPorts := make([]*model.VpcSubnetPort, 0)
	objs := subnetPortStore.ResourceStore.GetByIndex(key, value)
	for _, subnetPort := range objs {
		subnetPorts = append(subnetPorts, subnetPort.(*model.VpcSubnetPort))
	}
	return subnetPorts
}

func (vs *SubnetPortStore) GetSubnetPortsByNamespace(ns string) []*model.VpcSubnetPort {
	var ret []*model.VpcSubnetPort
	subnetPorts := vs.List()
	if len(subnetPorts) == 0 {
		log.V(1).Info("No subnet port found in SubnetPort store")
		return ret
	}

	for _, subnetPort := range subnetPorts {
		msubnetport := subnetPort.(*model.VpcSubnetPort)
		tags := msubnetport.Tags
		for _, tag := range tags {
			// TODO: consider to create index for common.TagScopeNamespace like common.TagScopeSubnetPortCRUID, and leverage functions like getByIndex to perform searches.
			if *tag.Scope == common.TagScopeNamespace && *tag.Tag == ns {
				ret = append(ret, msubnetport)
			}
		}
	}
	return ret
}
