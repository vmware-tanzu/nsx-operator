package common

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

const (
	DefaultHAPIChildrenCount = 500
)

type LeafDataWrapper[T any] func(leafData T) (*data.StructValue, error)
type GetPath[T any] func(obj T) *string
type GetId[T any] func(obj T) *string

func getNSXResourcePath[T any](obj T) *string {
	switch v := any(obj).(type) {
	case *model.VpcIpAddressAllocation:
		return v.Path
	case *model.VpcSubnet:
		return v.Path
	case *model.VpcSubnetPort:
		return v.Path
	case *model.SubnetConnectionBindingMap:
		return v.Path
	case *model.Vpc:
		return v.Path
	case *model.StaticRoutes:
		return v.Path
	case *model.SecurityPolicy:
		return v.Path
	case *model.Group:
		return v.Path
	case *model.Rule:
		return v.Path
	case *model.Share:
		return v.Path
	case *model.LBService:
		return v.Path
	case *model.LBVirtualServer:
		return v.Path
	case *model.LBPool:
		return v.Path
	case *model.TlsCertificate:
		return v.Path
	default:
		log.Error(nil, "Unknown NSX resource type %v", v)
		return nil
	}
}

func getNSXResourceId[T any](obj T) *string {
	switch v := any(obj).(type) {
	case *model.VpcIpAddressAllocation:
		return v.Id
	case *model.VpcSubnet:
		return v.Id
	case *model.VpcSubnetPort:
		return v.Id
	case *model.SubnetConnectionBindingMap:
		return v.Id
	case *model.Vpc:
		return v.Id
	case *model.StaticRoutes:
		return v.Id
	case *model.SecurityPolicy:
		return v.Id
	case *model.Group:
		return v.Id
	case *model.Rule:
		return v.Id
	case *model.Share:
		return v.Id
	case *model.LBService:
		return v.Id
	case *model.LBVirtualServer:
		return v.Id
	case *model.LBPool:
		return v.Id
	case *model.TlsCertificate:
		return v.Id
	default:
		log.Error(nil, "Unknown NSX resource type %v", v)
		return nil
	}
}

func leafWrapper[T any](obj T) (*data.StructValue, error) {
	switch v := any(obj).(type) {
	case *model.VpcIpAddressAllocation:
		return WrapVpcIpAddressAllocation(v)
	case *model.VpcSubnet:
		return WrapVpcSubnet(v)
	case *model.VpcSubnetPort:
		return WrapVpcSubnetPort(v)
	case *model.SubnetConnectionBindingMap:
		return WrapSubnetConnectionBindingMap(v)
	case *model.Vpc:
		return WrapVPC(v)
	case *model.StaticRoutes:
		return WrapStaticRoutes(v)
	case *model.SecurityPolicy:
		return WrapSecurityPolicy(v)
	case *model.Group:
		return WrapGroup(v)
	case *model.Rule:
		return WrapRule(v)
	case *model.Share:
		return WrapShare(v)
	case *model.LBService:
		return WrapLBService(v)
	case *model.LBVirtualServer:
		return WrapLBVirtualServer(v)
	case *model.LBPool:
		return WrapLBPool(v)
	case *model.TlsCertificate:
		return WrapCertificate(v)
	default:
		log.Error(nil, "Unknown NSX resource type", v)
		return nil, fmt.Errorf("unsupported NSX resource type %v", v)
	}
}

type PolicyResourceType struct {
	ModelKey string
	PathKey  string
}

type PolicyResourcePath[T any] []PolicyResourceType

func (p *PolicyResourcePath[T]) Length() int {
	resourceTypes := ([]PolicyResourceType)(*p)
	return len(resourceTypes)
}

func (p *PolicyResourcePath[T]) String() string {
	resourceTypes := ([]PolicyResourceType)(*p)
	resources := make([]string, len(resourceTypes))
	for i := 0; i < len(resourceTypes); i++ {
		resources[i] = resourceTypes[i].ModelKey
	}
	return strings.Join(resources, "-")
}

func (p *PolicyResourcePath[T]) getResources() []PolicyResourceType {
	return (*p)
}

func (p *PolicyResourcePath[T]) getChildrenResources() []PolicyResourceType {
	resources := p.getResources()
	if resources[0] == PolicyResourceInfra {
		return resources[1:]
	}
	return resources
}

func (p *PolicyResourcePath[T]) getKVPathFormat() []string {
	resourceTypes := p.getChildrenResources()
	format := make([]string, len(resourceTypes))
	for i := 0; i < len(resourceTypes); i++ {
		format[i] = resourceTypes[i].PathKey
	}
	return format
}

func (p *PolicyResourcePath[T]) getChildrenModelFormat() []string {
	resourceTypes := p.getChildrenResources()
	format := make([]string, len(resourceTypes))
	for i := 0; i < len(resourceTypes); i++ {
		format[i] = resourceTypes[i].ModelKey
	}
	return format
}

func (p *PolicyResourcePath[T]) getRootType() string {
	resourceTypes := p.getResources()
	if resourceTypes[0] == PolicyResourceOrg {
		return ResourceTypeOrgRoot
	}
	return ResourceTypeInfra
}

var (
	PolicyResourceInfra                         = PolicyResourceType{ModelKey: ResourceTypeInfra, PathKey: "infra"}
	PolicyResourceOrg                           = PolicyResourceType{ModelKey: ResourceTypeOrg, PathKey: "orgs"}
	PolicyResourceProject                       = PolicyResourceType{ModelKey: ResourceTypeProject, PathKey: "projects"}
	PolicyResourceVpc                           = PolicyResourceType{ModelKey: ResourceTypeVpc, PathKey: "vpcs"}
	PolicyResourceStaticRoutes                  = PolicyResourceType{ModelKey: ResourceTypeStaticRoute, PathKey: "static-routes"}
	PolicyResourceVpcSubnet                     = PolicyResourceType{ModelKey: ResourceTypeSubnet, PathKey: "subnets"}
	PolicyResourceVpcSubnetPort                 = PolicyResourceType{ModelKey: ResourceTypeSubnetPort, PathKey: "ports"}
	PolicyResourceVpcSubnetConnectionBindingMap = PolicyResourceType{ModelKey: ResourceTypeSubnetConnectionBindingMap, PathKey: "subnet-connection-binding-maps"}
	PolicyResourceVpcLBService                  = PolicyResourceType{ModelKey: ResourceTypeLBService, PathKey: "vpc-lbs"}
	PolicyResourceVpcLBPool                     = PolicyResourceType{ModelKey: ResourceTypeLBPool, PathKey: "vpc-lb-pools"}
	PolicyResourceVpcLBVirtualServer            = PolicyResourceType{ModelKey: ResourceTypeLBVirtualServer, PathKey: "vpc-lb-virtual-servers"}
	PolicyResourceInfraLBService                = PolicyResourceType{ModelKey: ResourceTypeLBService, PathKey: "lb-services"}
	PolicyResourceInfraLBPool                   = PolicyResourceType{ModelKey: ResourceTypeLBPool, PathKey: "lb-pools"}
	PolicyResourceInfraLBVirtualServer          = PolicyResourceType{ModelKey: ResourceTypeLBVirtualServer, PathKey: "lb-virtual-servers"}
	PolicyResourceVpcIPAddressAllocation        = PolicyResourceType{ModelKey: ResourceTypeIPAddressAllocation, PathKey: "ip-address-allocations"}
	PolicyResourceDomain                        = PolicyResourceType{ModelKey: ResourceTypeDomain, PathKey: "domains"}
	PolicyResourceShare                         = PolicyResourceType{ModelKey: ResourceTypeShare, PathKey: "shares"}
	PolicyResourceSharedResource                = PolicyResourceType{ModelKey: ResourceTypeSharedResource, PathKey: "resources"}
	PolicyResourceGroup                         = PolicyResourceType{ModelKey: ResourceTypeGroup, PathKey: "groups"}
	PolicyResourceRule                          = PolicyResourceType{ModelKey: ResourceTypeRule, PathKey: "rules"}
	PolicyResourceSecurityPolicy                = PolicyResourceType{ModelKey: ResourceTypeSecurityPolicy, PathKey: "security-policies"}
	PolicyResourceTlsCertificate                = PolicyResourceType{ModelKey: ResourceTypeTlsCertificate, PathKey: "certificates"}

	PolicyPathVpcSubnet                     PolicyResourcePath[*model.VpcSubnet]                  = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceVpc, PolicyResourceVpcSubnet}
	PolicyPathVpcSubnetConnectionBindingMap PolicyResourcePath[*model.SubnetConnectionBindingMap] = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceVpc, PolicyResourceVpcSubnet, PolicyResourceVpcSubnetConnectionBindingMap}
	PolicyPathVpcSubnetPort                 PolicyResourcePath[*model.VpcSubnetPort]              = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceVpc, PolicyResourceVpcSubnet, PolicyResourceVpcSubnetPort}
	PolicyPathVpcLBPool                     PolicyResourcePath[*model.LBPool]                     = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceVpc, PolicyResourceVpcLBPool}
	PolicyPathVpcLBService                  PolicyResourcePath[*model.LBService]                  = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceVpc, PolicyResourceVpcLBService}
	PolicyPathVpcLBVirtualServer            PolicyResourcePath[*model.LBVirtualServer]            = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceVpc, PolicyResourceVpcLBVirtualServer}
	PolicyPathVpcIPAddressAllocation        PolicyResourcePath[*model.VpcIpAddressAllocation]     = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceVpc, PolicyResourceVpcIPAddressAllocation}
	PolicyPathVpcSecurityPolicy             PolicyResourcePath[*model.SecurityPolicy]             = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceVpc, PolicyResourceSecurityPolicy}
	PolicyPathVpcStaticRoutes               PolicyResourcePath[*model.StaticRoutes]               = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceVpc, PolicyResourceStaticRoutes}
	PolicyPathVpcSecurityPolicyRule         PolicyResourcePath[*model.Rule]                       = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceVpc, PolicyResourceSecurityPolicy, PolicyResourceRule}
	PolicyPathVpcGroup                      PolicyResourcePath[*model.Group]                      = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceVpc, PolicyResourceGroup}
	PolicyPathProjectGroup                  PolicyResourcePath[*model.Group]                      = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceInfra, PolicyResourceDomain, PolicyResourceGroup}
	PolicyPathProjectShare                  PolicyResourcePath[*model.Share]                      = []PolicyResourceType{PolicyResourceOrg, PolicyResourceProject, PolicyResourceInfra, PolicyResourceShare}
	PolicyPathInfraGroup                    PolicyResourcePath[*model.Group]                      = []PolicyResourceType{PolicyResourceInfra, PolicyResourceDomain, PolicyResourceGroup}
	PolicyPathInfraShare                    PolicyResourcePath[*model.Share]                      = []PolicyResourceType{PolicyResourceInfra, PolicyResourceShare}
	PolicyPathInfraSharedResource           PolicyResourcePath[*model.SharedResource]             = []PolicyResourceType{PolicyResourceInfra, PolicyResourceShare, PolicyResourceSharedResource}
	PolicyPathInfraCert                     PolicyResourcePath[*model.TlsCertificate]             = []PolicyResourceType{PolicyResourceInfra, PolicyResourceTlsCertificate}
	PolicyPathInfraLBVirtualServer          PolicyResourcePath[*model.LBVirtualServer]            = []PolicyResourceType{PolicyResourceInfra, PolicyResourceInfraLBVirtualServer}
	PolicyPathInfraLBPool                   PolicyResourcePath[*model.LBPool]                     = []PolicyResourceType{PolicyResourceInfra, PolicyResourceInfraLBPool}
	PolicyPathInfraLBService                PolicyResourcePath[*model.LBService]                  = []PolicyResourceType{PolicyResourceInfra, PolicyResourceInfraLBService}
)

type hNodeKey struct {
	resType string
	resID   string
}

func (k *hNodeKey) Equals(other *hNodeKey) bool {
	return k.resType == other.resType && k.resID == other.resID
}

func (k *hNodeKey) String() string {
	return fmt.Sprintf("/%s", strings.Join([]string{k.resType, k.resID}, "/"))
}

type hNode[T any] struct {
	key        *hNodeKey
	leafData   *data.StructValue
	childNodes map[hNodeKey]*hNode[T]
}

func (n *hNode[T]) mergeChildNode(node *hNode[T], leafType string) {
	if n.childNodes == nil {
		n.childNodes = make(map[hNodeKey]*hNode[T])
	}

	if node.key.resType == leafType {
		n.childNodes[*node.key] = node
		return
	}

	cn, found := n.childNodes[*node.key]
	if found {
		for _, chN := range node.childNodes {
			cn.mergeChildNode(chN, leafType)
		}
		return
	}
	n.childNodes[*node.key] = node
}

func (n *hNode[T]) buildTree(rootType, leafType string) ([]*data.StructValue, error) {
	if n.key.resType == leafType {
		return []*data.StructValue{n.leafData}, nil
	}

	children := make([]*data.StructValue, 0)
	for _, cn := range n.childNodes {
		cnDataValues, err := cn.buildTree(rootType, leafType)
		if err != nil {
			return nil, err
		}
		children = append(children, cnDataValues...)
	}

	if n.key.resType == rootType {
		return children, nil
	}

	return WrapChildResourceReference(n.key.resType, n.key.resID, children)
}

type PolicyTreeBuilder[T any] struct {
	leafType string
	rootType string

	leafWrapper LeafDataWrapper[T]
	pathGetter  GetPath[T]
	idGetter    GetId[T]

	pathFormat    []string
	modelFormat   []string
	hasInnerInfra bool
}

func (b *PolicyTreeBuilder[T]) BuildRootNode(resources []T, parentPath string) *hNode[T] {
	rootNode := &hNode[T]{
		key: &hNodeKey{
			resType: b.rootType,
		},
	}
	leafPathKey := b.pathFormat[len(b.pathFormat)-1]
	for _, res := range resources {
		var path string
		if parentPath != "" {
			idValue := b.idGetter(res)
			path = fmt.Sprintf("%s/%s/%s", parentPath, leafPathKey, *idValue)
		} else {
			pathValue := b.pathGetter(res)
			if pathValue == nil {
				continue
			}
			path = *pathValue
		}
		orgNode, err := b.buildHNodeFromResource(path, res)
		if err != nil {
			log.Error(err, "Failed to build data value for resource, ignore", "Path", path)
			continue
		}
		rootNode.mergeChildNode(orgNode, b.leafType)
	}
	return rootNode
}

func (b *PolicyTreeBuilder[T]) buildTree(resources []T, parentPath string) ([]*data.StructValue, error) {
	rootNode := b.BuildRootNode(resources, parentPath)
	children, err := rootNode.buildTree(b.rootType, b.leafType)
	if err != nil {
		log.Error(err, "Failed to build data values for multiple resources")
		return nil, err
	}
	return children, nil
}

func (b *PolicyTreeBuilder[T]) BuildOrgRoot(resources []T, parentPath string) (*model.OrgRoot, error) {
	children, err := b.buildTree(resources, parentPath)
	if err != nil {
		return nil, err
	}

	return &model.OrgRoot{
		Children:     children,
		ResourceType: String(ResourceTypeOrgRoot),
	}, nil
}

func (b *PolicyTreeBuilder[T]) BuildInfra(resources []T, parentPath string) (*model.Infra, error) {
	children, err := b.buildTree(resources, parentPath)
	if err != nil {
		return nil, err
	}

	return wrapInfra(children), nil
}

func (b *PolicyTreeBuilder[T]) buildHNodeFromResource(path string, res T) (*hNode[T], error) {
	pathSegments, err := b.parsePathSegments(path)
	if err != nil {
		return nil, err
	}

	dataValue, err := b.leafWrapper(res)
	if err != nil {
		return nil, err
	}

	idx := len(pathSegments) - 1
	nodeCount := len(b.pathFormat)
	nodes := make([]*hNode[T], nodeCount)
	leafIdx := nodeCount - 1
	nodes[leafIdx] = &hNode[T]{
		key: &hNodeKey{
			resID:   pathSegments[idx],
			resType: b.modelFormat[leafIdx],
		},
		leafData: dataValue,
	}
	idx -= 2

	for i := leafIdx - 1; i >= 0; i-- {
		child := nodes[i+1]
		resType := b.modelFormat[i]
		var resID string
		if resType != ResourceTypeInfra {
			resID = pathSegments[idx]
			idx -= 2
		} else {
			resID = ""
			idx -= 1
		}
		node := &hNode[T]{
			key: &hNodeKey{
				resID:   resID,
				resType: resType,
			},
			childNodes: map[hNodeKey]*hNode[T]{
				*child.key: child,
			},
		}
		nodes[i] = node
	}
	n := nodes[0]
	return n, nil
}

func (b *PolicyTreeBuilder[T]) parsePathSegments(inputPath string) ([]string, error) {
	pathSegments := strings.Split(strings.Trim(inputPath, "/"), "/")
	// Remove "infra" in the path since the infra resource's path does not follow the format "/key/value", so we remove
	// the first "infra" in the pathSegments.
	if b.rootType == ResourceTypeInfra {
		pathSegments = pathSegments[1:]
	}

	segmentsCount := len(pathSegments)
	leafPathKey := b.pathFormat[len(b.pathFormat)-1]
	if segmentsCount < 2 || pathSegments[segmentsCount-2] != leafPathKey {
		return nil, fmt.Errorf("invalid input path %s for resource %s", inputPath, b.leafType)
	}
	if b.hasInnerInfra {
		segmentsCount += 1
	}
	if segmentsCount != len(b.pathFormat)*2 {
		return nil, fmt.Errorf("invalid input path: %s", inputPath)
	}

	return pathSegments, nil
}

func (b *PolicyTreeBuilder[T]) DeleteMultipleResourcesOnNSX(objects []T, nsxClient *nsx.Client) error {
	if len(objects) == 0 {
		return nil
	}
	enforceRevisionCheckParam := false
	if b.rootType == ResourceTypeOrgRoot {
		orgRoot, err := b.BuildOrgRoot(objects, "")
		if err != nil {
			log.Error(err, "Failed to generate OrgRoot with multiple resources", "resourceType", b.leafType)
			return err
		}
		if err = nsxClient.OrgRootClient.Patch(*orgRoot, &enforceRevisionCheckParam); err != nil {
			log.Error(err, "Failed to delete multiple resources on NSX with HAPI", "resourceType", b.leafType)
			err = util.TransNSXApiError(err)
			return err
		}
		return nil
	}

	infraRoot, err := b.BuildInfra(objects, "")
	if err != nil {
		log.Error(err, "Failed to generate Infra with multiple resources", "resourceType", b.leafType)
		return err
	}
	if err = nsxClient.InfraClient.Patch(*infraRoot, &enforceRevisionCheckParam); err != nil {
		log.Error(err, "Failed to delete multiple resources on NSX with HAPI", "resourceType", b.leafType)
		err = util.TransNSXApiError(err)
		return err
	}

	return nil
}

func PagingNSXResources[T any](resources []T, pageSize int) [][]T {
	totalCount := len(resources)
	pages := (totalCount + pageSize - 1) / pageSize
	pagedResources := make([][]T, 0)
	for i := 1; i <= pages; i++ {
		start := (i - 1) * pageSize
		end := start + pageSize
		if end > totalCount {
			end = totalCount
		}
		pagedResources = append(pagedResources, resources[start:end])
	}
	return pagedResources
}

func (p *PolicyResourcePath[T]) NewPolicyTreeBuilder() (*PolicyTreeBuilder[T], error) {
	if p.Length() == 0 {
		return nil, fmt.Errorf("invalid PolicyResourcePath: %s", p.String())
	}
	modelFormat := p.getChildrenModelFormat()
	rootType := p.getRootType()
	pathFormat := p.getKVPathFormat()

	return &PolicyTreeBuilder[T]{
		pathFormat:    pathFormat,
		modelFormat:   modelFormat,
		hasInnerInfra: sets.New[string](pathFormat...).Has(PolicyResourceInfra.PathKey),
		rootType:      rootType,
		leafType:      modelFormat[len(modelFormat)-1],

		leafWrapper: leafWrapper[T],
		pathGetter:  getNSXResourcePath[T],
		idGetter:    getNSXResourceId[T],
	}, nil
}

func (builder *PolicyTreeBuilder[T]) PagingDeleteResources(ctx context.Context, objs []T, pageSize int, nsxClient *nsx.Client, delFn func(deletedObjs []T)) error {
	if len(objs) == 0 {
		return nil
	}
	var nsxErr error
	pagedObjs := PagingNSXResources(objs, pageSize)
	for _, partialObjs := range pagedObjs {
		select {
		case <-ctx.Done():
			return errors.Join(util.TimeoutFailed, ctx.Err())
		default:
			delErr := builder.DeleteMultipleResourcesOnNSX(partialObjs, nsxClient)
			if delErr == nil {
				if delFn != nil {
					delFn(partialObjs)
				}
				continue
			}
			nsxErr = delErr
		}
	}
	return nsxErr
}
