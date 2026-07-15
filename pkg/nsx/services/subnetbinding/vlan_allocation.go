package subnetbinding

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

const searchPageSize = int64(1000)

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

func (s *BindingService) listBindingMapsByParentSubnetPath(parentSubnetPath string) ([]*model.SubnetConnectionBindingMap, error) {
	if s == nil || s.NSXClient == nil || s.NSXClient.QueryClient == nil {
		return nil, fmt.Errorf("NSX query client is not initialized")
	}

	pathEscaped := strings.ReplaceAll(parentSubnetPath, "/", "\\/")
	queryParam := fmt.Sprintf("%s:%s AND marked_for_delete:false AND subnet_path:%s",
		servicecommon.ResourceType, ResourceTypeSubnetConnectionBindingMap, pathEscaped)

	converter := servicecommon.NewConverter()
	bindings := make([]*model.SubnetConnectionBindingMap, 0)
	var cursor *string
	pageSize := searchPageSize

	for {
		response, err := s.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		if err != nil {
			err = servicecommon.TransError(err)
			if _, ok := err.(nsxutil.PageMaxError); ok {
				servicecommon.DecrementPageSize(&pageSize)
				continue
			}
			return nil, err
		}

		for _, entity := range response.Results {
			obj, convErrs := converter.ConvertToGolang(entity, model.SubnetConnectionBindingMapBindingType())
			if len(convErrs) > 0 {
				log.Error(convErrs[0], "Failed to convert NSX SubnetConnectionBindingMap from search result")
				continue
			}
			bmV, ok := obj.(model.SubnetConnectionBindingMap)
			if !ok {
				log.Info("Skipping unexpected search result type for SubnetConnectionBindingMap")
				continue
			}
			bindings = append(bindings, &bmV)
		}

		if response.Cursor == nil {
			break
		}
		c, err := strconv.Atoi(*response.Cursor)
		if err != nil {
			break
		}
		if response.ResultCount != nil && int64(c) >= *response.ResultCount {
			break
		}
		cursor = response.Cursor
	}

	return bindings, nil
}
