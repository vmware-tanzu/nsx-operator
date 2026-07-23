package subnetbinding

import (
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// CollectUsedVlansOnParentSubnetsFromCache returns VLAN tags already used on the given parent Subnet paths by
// querying local cache.
func (s *BindingService) CollectUsedVlansOnParentSubnetsFromCache(parentSubnetPaths []string, excludeCRUID string) (sets.Set[int], error) {
	used := sets.New[int]()
	for _, parentPath := range parentSubnetPaths {
		bindings := s.GetSubnetConnectionBindingMapsByParentSubnet(parentPath)
		for _, bm := range bindings {
			if excludeCRUID != "" && bindingMapCRUID(bm) == excludeCRUID {
				continue
			}
			if bm.VlanTrafficTag != nil {
				used.Insert(int(*bm.VlanTrafficTag))
			}
		}
	}
	return used, nil
}

// CollectUsedVlansOnParentSubnetsFromNSX returns VLAN tags already used on the given parent Subnet paths by
// querying NSX (includes bindings not created by this Supervisor).
func (s *BindingService) CollectUsedVlansOnParentSubnetsFromNSX(parentSubnetPaths []string, excludeCRUID string) (sets.Set[int], error) {
	used := sets.New[int]()
	for _, parentPath := range parentSubnetPaths {
		bindings, err := s.listBindingMapsByParentSubnetPath(parentPath)
		if err != nil {
			return nil, err
		}
		for _, bm := range bindings {
			if excludeCRUID != "" && bindingMapCRUID(bm) == excludeCRUID {
				continue
			}
			if bm.VlanTrafficTag != nil {
				used.Insert(int(*bm.VlanTrafficTag))
			}
		}
	}
	return used, nil
}

func bindingMapCRUID(bm *model.SubnetConnectionBindingMap) string {
	for _, tag := range bm.Tags {
		if tag.Scope != nil && *tag.Scope == servicecommon.TagScopeSubnetBindingCRUID && tag.Tag != nil {
			return *tag.Tag
		}
	}
	return ""
}

type localStore struct {
	servicecommon.ResourceStore
}

func (ls *localStore) Apply(_ interface{}) error {
	return nil
}

func (s *BindingService) listBindingMapsByParentSubnetPath(parentSubnetPath string) ([]*model.SubnetConnectionBindingMap, error) {
	if s == nil || s.NSXClient == nil || s.NSXClient.QueryClient == nil {
		return nil, fmt.Errorf("NSX query client is not initialized")
	}

	pathEscaped := strings.ReplaceAll(parentSubnetPath, "/", "\\/")
	// TODO：update here to support bridge mode.
	queryParam := fmt.Sprintf("%s:%s AND marked_for_delete:false AND subnet_path:%s",
		servicecommon.ResourceType, ResourceTypeSubnetConnectionBindingMap, pathEscaped)

	store := &localStore{ResourceStore: servicecommon.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.SubnetConnectionBindingMapBindingType(),
	}}
	_, err := s.SearchResource(ResourceTypeSubnetConnectionBindingMap, queryParam, store, nil)
	if err != nil {
		return nil, err
	}

	bindings := make([]*model.SubnetConnectionBindingMap, 0)
	for _, obj := range store.List() {
		bindings = append(bindings, obj.(*model.SubnetConnectionBindingMap))
	}
	return bindings, nil
}
