package subnetbinding

import (
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var leafType = "SubnetConnectionBindingMap"

type hNode struct {
	resourceType string
	resourceID   string
	bindingMap   *model.SubnetConnectionBindingMap
	childNodes   []*hNode
}

// TODO: Refine the struct of hNode to avoid "linear search" when merging nodes in case it has performance
// issue if the number of resources is huge.
func (n *hNode) mergeChildNode(node *hNode) {
	if node.resourceType == leafType {
		n.childNodes = append(n.childNodes, node)
		return
	}

	for _, cn := range n.childNodes {
		if cn.resourceType == node.resourceType && cn.resourceID == node.resourceID {
			for _, chN := range node.childNodes {
				cn.mergeChildNode(chN)
			}
			return
		}
	}
	n.childNodes = append(n.childNodes, node)
}

func (n *hNode) buildTree() ([]*data.StructValue, error) {
	if n.resourceType == leafType {
		dataValue, err := wrapSubnetBindingMap(n.bindingMap)
		if err != nil {
			return nil, err
		}
		return []*data.StructValue{dataValue}, nil
	}

	children := make([]*data.StructValue, 0)
	for _, cn := range n.childNodes {
		cnDataValues, err := cn.buildTree()
		if err != nil {
			return nil, err
		}
		children = append(children, cnDataValues...)
	}
	if n.resourceType == "OrgRoot" {
		return children, nil
	}

	return wrapChildResourceReference(n.resourceType, n.resourceID, children)
}

func buildHNodeFromSubnetConnectionBindingMap(subnetPath string, bindingMap *model.SubnetConnectionBindingMap) (*hNode, error) {
	vpcInfo, err := common.ParseVPCResourcePath(subnetPath)
	if err != nil {
		return nil, err
	}
	return &hNode{
		resourceType: "Org",
		resourceID:   vpcInfo.OrgID,
		childNodes: []*hNode{
			{
				resourceType: "Project",
				resourceID:   vpcInfo.ProjectID,
				childNodes: []*hNode{
					{
						resourceID:   vpcInfo.VPCID,
						resourceType: "Vpc",
						childNodes: []*hNode{
							{
								resourceID:   vpcInfo.ID,
								resourceType: "VpcSubnet",
								childNodes: []*hNode{
									{
										resourceID:   *bindingMap.Id,
										resourceType: leafType,
										bindingMap:   bindingMap,
									},
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

func buildRootNode(bindingMaps []*model.SubnetConnectionBindingMap, subnetPath string) *hNode {
	rootNode := &hNode{
		resourceType: "OrgRoot",
	}

	for _, bm := range bindingMaps {
		parentPath := subnetPath
		if parentPath == "" {
			parentPath = *bm.ParentPath
		}
		orgNode, err := buildHNodeFromSubnetConnectionBindingMap(parentPath, bm)
		if err != nil {
			log.Error(err, "Failed to build data value for SubnetConnectionBindingMap, ignore", "bindingMap", *bm.Path)
			continue
		}
		rootNode.mergeChildNode(orgNode)
	}
	return rootNode
}

func buildOrgRootBySubnetConnectionBindingMaps(bindingMaps []*model.SubnetConnectionBindingMap, subnetPath string) (*model.OrgRoot, error) {
	rootNode := buildRootNode(bindingMaps, subnetPath)

	children, err := rootNode.buildTree()
	if err != nil {
		log.Error(err, "Failed to build data values for multiple SubnetConnectionBindingMaps")
		return nil, err
	}

	return &model.OrgRoot{
		Children:     children,
		ResourceType: String("OrgRoot"),
	}, nil
}

func wrapChildResourceReference(targetType, resID string, children []*data.StructValue) ([]*data.StructValue, error) {
	childRes := model.ChildResourceReference{
		Id:           &resID,
		ResourceType: "ChildResourceReference",
		TargetType:   &targetType,
		Children:     children,
	}
	dataValue, errors := common.NewConverter().ConvertToVapi(childRes, model.ChildResourceReferenceBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return []*data.StructValue{dataValue.(*data.StructValue)}, nil
}

func wrapSubnetBindingMap(bindingMap *model.SubnetConnectionBindingMap) (*data.StructValue, error) {
	bindingMap.ResourceType = &common.ResourceTypeSubnetConnectionBindingMap
	childBindingMap := model.ChildSubnetConnectionBindingMap{
		Id:                         bindingMap.Id,
		MarkedForDelete:            bindingMap.MarkedForDelete,
		ResourceType:               "ChildSubnetConnectionBindingMap",
		SubnetConnectionBindingMap: bindingMap,
	}
	dataValue, errors := common.NewConverter().ConvertToVapi(childBindingMap, model.ChildSubnetConnectionBindingMapBindingType())
	if len(errors) > 0 {
		return nil, errors[0]
	}
	return dataValue.(*data.StructValue), nil
}

func (s *BindingService) hUpdateSubnetConnectionBindingMaps(subnetPath string, bindingMaps []*model.SubnetConnectionBindingMap) error {
	vpcInfo, err := common.ParseVPCResourcePath(subnetPath)
	if err != nil {
		return err
	}
	subnetID := vpcInfo.ID
	orgRoot, err := buildOrgRootBySubnetConnectionBindingMaps(bindingMaps, subnetPath)
	if err != nil {
		return err
	}

	if err = s.NSXClient.OrgRootClient.Patch(*orgRoot, &enforceRevisionCheckParam); err != nil {
		log.Error(err, "Failed to patch SubnetConnectionBindingMaps on NSX", "orgID", vpcInfo.OrgID, "projectID", vpcInfo.ProjectID, "vpcID", vpcInfo.VPCID, "subnetID", subnetID, "subnetConnectionBindingMaps", bindingMaps)
		err = nsxutil.TransNSXApiError(err)
		return err
	}

	// Get SubnetConnectionBindingMaps from NSX after patch operation as NSX renders several fields like `path`/`parent_path`.
	subnetBindingListResult, err := s.NSXClient.SubnetConnectionBindingMapsClient.List(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, subnetID, nil, nil, nil, nil, nil, nil)
	if err != nil {
		log.Error(err, "Failed to list SubnetConnectionBindingMaps from NSX under subnet", "orgID", vpcInfo.OrgID, "projectID", vpcInfo.ProjectID, "vpcID", vpcInfo.VPCID, "subnetID", subnetID, "subnetConnectionBindingMaps", bindingMaps)
		err = nsxutil.TransNSXApiError(err)
		return err
	}

	nsxBindingMaps := make(map[string]model.SubnetConnectionBindingMap)
	for _, bm := range subnetBindingListResult.Results {
		nsxBindingMaps[*bm.Id] = bm
	}

	for i := range bindingMaps {
		bm := bindingMaps[i]
		if bm.MarkedForDelete != nil && *bm.MarkedForDelete {
			s.BindingStore.Apply(bm)
		} else {
			nsxBindingMap := nsxBindingMaps[*bm.Id]
			s.BindingStore.Apply(&nsxBindingMap)
		}
	}

	return nil
}

func (s *BindingService) hDeleteSubnetConnectionBindingMap(bindingMaps []*model.SubnetConnectionBindingMap) error {
	markForDelete := true
	for _, bm := range bindingMaps {
		bm.MarkedForDelete = &markForDelete
	}

	orgRoot, err := buildOrgRootBySubnetConnectionBindingMaps(bindingMaps, "")
	if err != nil {
		return err
	}

	if err = s.NSXClient.OrgRootClient.Patch(*orgRoot, &enforceRevisionCheckParam); err != nil {
		log.Error(err, "Failed to delete multiple SubnetConnectionBindingMaps on NSX with HAPI")
		err = nsxutil.TransNSXApiError(err)
		return err
	}

	// Remove SubnetConnectionBindingMap from local store.
	for _, bm := range bindingMaps {
		s.BindingStore.Apply(bm)
	}
	return nil
}
