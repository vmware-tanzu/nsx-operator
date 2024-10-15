package vpc

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// keyFunc is used to get the key of a resource, usually, which is the ID of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.Vpc:
		return *v.Id, nil
	case *model.LBService:
		return generateLBSKey(*v)
	case *model.LBVirtualServer:
		return generateVirtualServerKey(*v)
	case *model.LBPool:
		return generatePoolKey(*v)
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

// indexFunc is used to get index of a resource, usually, which is the UID of the CR controller reconciles,
// index is used to filter out resources which are related to the CR
func indexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch o := obj.(type) {
	case *model.Vpc:
		return filterTag(o.Tags), nil
	case *model.LBService:
		return filterTag(o.Tags), nil
	default:
		return res, errors.New("indexFunc doesn't support unknown type")
	}
}

var filterTag = func(v []model.Tag) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == common.TagScopeNamespaceUID {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

// VPCStore is a store for VPCs
type VPCStore struct {
	common.ResourceStore
}

func (vs *VPCStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	vpc := i.(*model.Vpc)
	if vpc.MarkedForDelete != nil && *vpc.MarkedForDelete {
		err := vs.Delete(vpc)
		log.V(1).Info("delete VPC from store", "VPC", vpc)
		if err != nil {
			return err
		}
	} else {
		err := vs.Add(vpc)
		log.V(1).Info("add VPC to store", "VPC", vpc)
		if err != nil {
			return err
		}
	}
	return nil
}

func (vs *VPCStore) GetVPCsByNamespace(ns string) []*model.Vpc {
	var ret []*model.Vpc
	vpcs := vs.List()
	if len(vpcs) == 0 {
		log.V(1).Info("No vpc found in vpc store")
		return ret
	}

	for _, vpc := range vpcs {
		mvpc := vpc.(*model.Vpc)
		tags := mvpc.Tags
		for _, tag := range tags {
			if *tag.Scope == common.TagScopeNamespace && *tag.Tag == ns {
				ret = append(ret, mvpc)
			}
		}
	}
	return ret
}

func (vs *VPCStore) GetByKey(key string) *model.Vpc {
	obj := vs.ResourceStore.GetByKey(key)
	if obj != nil {
		vpc := obj.(*model.Vpc)
		return vpc
	}
	return nil
}

// ResourceStore is a store to query nsx resource
type ResourceStore struct {
	common.ResourceStore
}

func (r *ResourceStore) Apply(i interface{}) error {
	return nil
}

// LBSStore is a store for LBS
type LBSStore struct {
	common.ResourceStore
}

func (ls *LBSStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	lbs := i.(*model.LBService)
	if lbs.MarkedForDelete != nil && *lbs.MarkedForDelete {
		err := ls.Delete(lbs)
		log.V(1).Info("delete LBS from store", "LBS", lbs)
		if err != nil {
			return err
		}
	} else {
		err := ls.Add(lbs)
		log.V(1).Info("add LBS to store", "LBS", lbs)
		if err != nil {
			return err
		}
	}
	return nil
}

func (ls *LBSStore) GetByKey(vpcID string) *model.LBService {
	key := combineVPCIDAndLBSID(vpcID, defaultLBSName)
	obj := ls.ResourceStore.GetByKey(key)
	if obj != nil {
		lbs := obj.(*model.LBService)
		return lbs
	}
	return nil
}
