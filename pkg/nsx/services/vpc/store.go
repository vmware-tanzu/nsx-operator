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
	case *model.SharedResource:
		return *v.Path, nil
	case *model.LBAppProfile:
		return *v.Path, nil
	case *model.TlsCertificate:
		return *v.Path, nil
	case *model.LBPersistenceProfile:
		return *v.Path, nil
	case *model.Share:
		return *v.Path, nil
	case *model.LBMonitorProfile:
		return *v.Path, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func vpcIndexNamespaceNameFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.Vpc:
		return filterTagBy(o.Tags, common.TagScopeNamespace), nil
	default:
		return nil, errors.New("VPCIndexNamespaceFunc doesn't support unknown type")
	}
}

func vpcIndexNamespaceIDFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.Vpc:
		return filterTagBy(o.Tags, common.TagScopeNamespaceUID), nil
	default:
		return nil, errors.New("VPCIndexNamespaceFunc doesn't support unknown type")
	}
}

func vpcIndexVpcNameFunc(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.Vpc:
		return []string{*o.DisplayName}, nil
	default:
		return nil, errors.New("vpcIndexVpcNameFunc doesn't support unknown type")
	}
}

func filterTagBy(v []model.Tag, tagScope string) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == tagScope {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

// VPCStore is a store for VPCs
type VPCStore struct {
	common.ResourceStore
}

func (s *VPCStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	vpc := i.(*model.Vpc)
	if vpc.MarkedForDelete != nil && *vpc.MarkedForDelete {
		err := s.Delete(vpc)
		log.V(1).Info("delete VPC from store", "VPC", vpc)
		if err != nil {
			return err
		}
	} else {
		err := s.Add(vpc)
		log.V(1).Info("add VPC to store", "VPC", vpc)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *VPCStore) GetByIndex(key string, value string) []*model.Vpc {
	vpcs := make([]*model.Vpc, 0)
	objs := s.ResourceStore.GetByIndex(key, value)
	for _, vpc := range objs {
		vpcs = append(vpcs, vpc.(*model.Vpc))
	}
	return vpcs
}

func (s *VPCStore) GetVPCsByNamespaceFromStore(ns string) []*model.Vpc {
	return s.GetByIndex(common.TagScopeNamespace, ns)
}

func (s *VPCStore) GetVPCsByNamespaceIDFromStore(namespaceID string) []*model.Vpc {
	return s.GetByIndex(common.TagScopeNamespaceUID, namespaceID)
}

func (s *VPCStore) GetByKey(key string) *model.Vpc {
	obj := s.ResourceStore.GetByKey(key)
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

func (ls *LBSStore) GetByIndex(key string, value string) []*model.LBService {
	lbss := make([]*model.LBService, 0)
	objs := ls.ResourceStore.GetByIndex(key, value)
	for _, lbs := range objs {
		lbss = append(lbss, lbs.(*model.LBService))
	}
	return lbss
}
