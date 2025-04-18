package vpc

import (
	"context"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (s *VPCService) ListAutoCreatedVPCPaths() sets.Set[string] {
	vpcPaths := sets.New[string]()
	for _, obj := range s.VpcStore.List() {
		vpc := obj.(*model.Vpc)
		vpcPaths.Insert(*vpc.Path)
	}
	return vpcPaths
}

func (s *VPCService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	if err := s.cleanupAviSubnetPorts(ctx); err != nil {
		log.Error(err, "Failed to clean up Avi subnet ports")
		return err
	}
	// LB Virtual Servers are removed before deleting VPC, otherwise it may block the parallel deletion with other
	// VPC children e.g., VpcIPAddressAllocations.
	if err := s.cleanupSLBVirtualServers(ctx); err != nil {
		log.Error(err, "Failed to clean up SLB VirtualServers")
		return err
	}
	return nil
}

// CleanupVPCChildResources is deleting all the VPC LB pools in the given vpcPath on NSX and/or in local cache.
// If vpcPath is not empty, the function is called with auto-created VPC case, so it only deletes in the local cache for
// the NSX resources are already removed when VPC is deleted recursively. Otherwise, it should delete all cached groups
// on NSX and in local cache.
func (s *VPCService) CleanupVPCChildResources(ctx context.Context, vpcPath string) error {
	if err := s.cleanupLBPools(ctx, vpcPath); err != nil {
		log.Error(err, "Failed to clean up LB Pool")
		return err
	}
	return nil
}

func (s *VPCService) cleanupSLBVirtualServers(ctx context.Context) error {
	lbVSs, err := s.getStaleSLBVirtualServers()
	if err != nil {
		return err
	}
	log.Info("Cleaning up SLB virtual servers", "Count", len(lbVSs))
	lbVSBuilder, _ := common.PolicyPathVpcLBVirtualServer.NewPolicyTreeBuilder()
	return lbVSBuilder.PagingUpdateResources(ctx, lbVSs, common.DefaultHAPIChildrenCount, s.NSXClient, nil)
}

func (s *VPCService) getStaleSLBPools() ([]*model.LBPool, error) {
	objs, err := s.getStaleSLBResources(common.ResourceTypeLBPool, model.LBPoolBindingType())
	if err != nil {
		return nil, err
	}
	var lbPools []*model.LBPool
	for _, obj := range objs {
		lbPools = append(lbPools, obj.(*model.LBPool))
	}
	return lbPools, nil
}

func (s *VPCService) getStaleSLBVirtualServers() ([]*model.LBVirtualServer, error) {
	objs, err := s.getStaleSLBResources(common.ResourceTypeLBVirtualServer, model.LBVirtualServerBindingType())
	if err != nil {
		return nil, err
	}
	var lbVSs []*model.LBVirtualServer
	for _, obj := range objs {
		lbVSs = append(lbVSs, obj.(*model.LBVirtualServer))
	}
	return lbVSs, nil
}

func (s *VPCService) getStaleSLBResources(resourceType string, resourceBindingType bindings.BindingType) ([]interface{}, error) {
	store, err := s.querySLBResources(resourceType, resourceBindingType)
	if err != nil {
		return nil, err
	}

	autoCreatedVPCPaths := s.ListAutoCreatedVPCPaths()

	isSLBResourceValidToDelete := func(obj interface{}) bool {
		var parentPath *string
		switch obj.(type) {
		case *model.LBVirtualServer:
			lbVS := obj.(*model.LBVirtualServer)
			parentPath = lbVS.ParentPath
			lbVS.MarkedForDelete = &MarkedForDelete
		case *model.LBPool:
			lbPool := obj.(*model.LBPool)
			parentPath = lbPool.ParentPath
			lbPool.MarkedForDelete = &MarkedForDelete
		default:
			return false
		}
		if parentPath == nil {
			return false
		}
		return strings.HasPrefix(*parentPath, "/orgs") && !autoCreatedVPCPaths.Has(*parentPath)
	}

	var slbObjects []interface{}
	for _, obj := range store.List() {
		if !isSLBResourceValidToDelete(obj) {
			continue
		}
		slbObjects = append(slbObjects, obj)
	}
	return slbObjects, nil
}

func (s *VPCService) cleanupLBPools(ctx context.Context, vpcPath string) error {
	if vpcPath != "" {
		// Return directly with the case for auto-created VPC by which vpcPath is not empty, since the LB Pool is
		// automatically removed when deleting the VPC recursively.
		return nil
	}

	lbPools, err := s.getStaleSLBPools()
	if err != nil {
		return err
	}
	log.Info("Cleaning up SLB pools", "Count", len(lbPools))
	lbPoolBuilder, _ := common.PolicyPathVpcLBPool.NewPolicyTreeBuilder()
	return lbPoolBuilder.PagingUpdateResources(ctx, lbPools, common.DefaultHAPIChildrenCount, s.NSXClient, nil)
}

func (s *VPCService) querySLBResources(resourceType string, resourceBindingType bindings.BindingType) (*ResourceStore, error) {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: resourceBindingType,
	}}
	if err := s.Service.QueryNCPCreatedResources([]string{resourceType}, store, func(query string) string {
		return common.AddNCPCreatedForTag(query, common.TagValueSLB)
	}); err != nil {
		log.Error(err, "Failed to query SLB resources", "resource type", resourceType)
		return store, err
	}
	return store, nil
}
