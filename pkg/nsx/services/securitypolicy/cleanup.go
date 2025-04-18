package securitypolicy

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// CleanupVPCChildResources is called when cleaning up the VPC related resources. For the resources in an auto-created
// VPC, this function is called after the VPC is deleted on NSX, so the provider only needs to clean up with the local
// cache. For the resources in a pre-created VPC, this function is called to delete resources on NSX and in the local cache.
func (service *SecurityPolicyService) CleanupVPCChildResources(ctx context.Context, vpcPath string) error {
	if err := service.cleanupRulesByVPC(ctx, vpcPath); err != nil {
		log.Error(err, "Failed to clean up Rule by VPC", "VPC", vpcPath)
		return err
	}

	if err := service.cleanupSecurityPoliciesByVPC(ctx, vpcPath); err != nil {
		log.Error(err, "Failed to clean up SecurityPolicy by VPC", "VPC", vpcPath)
		return err
	}
	if err := service.cleanupGroupsByVPC(ctx, vpcPath); err != nil {
		log.Error(err, "Failed to clean up Group by VPC", "VPC", vpcPath)
		return err
	}
	return nil
}

// cleanupSecurityPoliciesByVPC is deleting all the NSX security policies in the given vpcPath on NSX and/or in local cache.
// If vpcPath is not empty, the function is called with auto-created VPC case, so it only deletes in the local cache for
// the NSX resources are already removed when VPC is deleted recursively. Otherwise, it should delete all cached seurity policies
// on NSX and in local cache.
func (service *SecurityPolicyService) cleanupSecurityPoliciesByVPC(ctx context.Context, vpcPath string) error {
	if vpcPath != "" {
		securityPolicies := service.securityPolicyStore.GetByIndex(common.IndexByVPCPathFuncKey, vpcPath)
		if len(securityPolicies) == 0 {
			return nil
		}
		// Delete resources from the store and return.
		service.securityPolicyStore.DeleteMultipleObjects(securityPolicies)
		return nil
	}

	securityPolicies := make([]*model.SecurityPolicy, 0)
	// Mark the resources for delete.
	for _, obj := range service.securityPolicyStore.List() {
		sp := obj.(*model.SecurityPolicy)
		sp.MarkedForDelete = &MarkedForDelete
		securityPolicies = append(securityPolicies, sp)
	}

	return service.securityPolicyBuilder.PagingUpdateResources(ctx, securityPolicies, common.DefaultHAPIChildrenCount, service.NSXClient, func(deletedObjs []*model.SecurityPolicy) {
		service.securityPolicyStore.DeleteMultipleObjects(deletedObjs)
	})
}

// cleanupRulesByVPC is deleting all the NSX rules in the given vpcPath on NSX and/or in local cache.
// If vpcPath is not empty, the function is called with auto-created VPC case, so it only deletes in the local cache for
// the NSX resources are already removed when VPC is deleted recursively. Otherwise, it should delete all cached rules
// on NSX and in local cache.
func (service *SecurityPolicyService) cleanupRulesByVPC(ctx context.Context, vpcPath string) error {
	if vpcPath != "" {
		rules := service.ruleStore.GetByIndex(common.IndexByVPCPathFuncKey, vpcPath)
		if len(rules) == 0 {
			return nil
		}
		// Delete resources from the store and return.
		service.ruleStore.DeleteMultipleObjects(rules)
		return nil
	}

	rules := make([]*model.Rule, 0)
	// Mark the resources for delete.
	for _, obj := range service.ruleStore.List() {
		rule := obj.(*model.Rule)
		rule.MarkedForDelete = &MarkedForDelete
		rules = append(rules, rule)
	}

	return service.ruleBuilder.PagingUpdateResources(ctx, rules, common.DefaultHAPIChildrenCount, service.NSXClient, func(deletedObjs []*model.Rule) {
		service.ruleStore.DeleteMultipleObjects(deletedObjs)
	})
}

// cleanupGroupsByVPC is deleting all the NSX groups in the given vpcPath on NSX and/or in local cache.
// If vpcPath is not empty, the function is called with auto-created VPC case, so it only deletes in the local cache for
// the NSX resources are already removed when VPC is deleted recursively. Otherwise, it should delete all cached groups
// on NSX and in local cache.
func (service *SecurityPolicyService) cleanupGroupsByVPC(ctx context.Context, vpcPath string) error {
	if vpcPath != "" {
		groups := service.groupStore.GetByIndex(common.IndexByVPCPathFuncKey, vpcPath)
		if len(groups) == 0 {
			return nil
		}
		// Delete resources from the store and return.
		service.groupStore.DeleteMultipleObjects(groups)
		return nil
	}

	return cleanGroups(ctx, service.groupStore, service.groupBuilder, service.NSXClient)
}

// CleanupInfraResources is to clean up the resources created by SecurityPolicyService under path /infra.
func (service *SecurityPolicyService) CleanupInfraResources(ctx context.Context) error {
	for _, config := range []struct {
		store   *ShareStore
		builder *common.PolicyTreeBuilder[*model.Share]
	}{
		{
			store:   service.projectShareStore,
			builder: service.projectShareBuilder,
		}, {
			store:   service.infraShareStore,
			builder: service.infraShareBuilder,
		},
	} {
		if err := cleanShares(ctx, config.store, config.builder, service.NSXClient); err != nil {
			return err
		}
	}
	for _, config := range []struct {
		store   *GroupStore
		builder *common.PolicyTreeBuilder[*model.Group]
	}{
		{
			store:   service.projectGroupStore,
			builder: service.projectGroupBuilder,
		}, {
			store:   service.infraGroupStore,
			builder: service.infraGroupBuilder,
		},
	} {
		if err := cleanGroups(ctx, config.store, config.builder, service.NSXClient); err != nil {
			return err
		}
	}
	return nil
}

func cleanShares(ctx context.Context, store *ShareStore, builder *common.PolicyTreeBuilder[*model.Share], nsxClient *nsx.Client) error {
	cachedObjs := store.List()
	if len(cachedObjs) == 0 {
		return nil
	}
	log.Info("Cleaning up Shares", "Count", len(cachedObjs))
	cachedShares := make([]*model.Share, 0)
	for _, obj := range cachedObjs {
		share := obj.(*model.Share)
		share.MarkedForDelete = &MarkedForDelete
		cachedShares = append(cachedShares, share)
	}

	return builder.PagingUpdateResources(ctx, cachedShares, common.DefaultHAPIChildrenCount, nsxClient, func(deletedObjs []*model.Share) {
		store.DeleteMultipleObjects(deletedObjs)
	})
}

func cleanGroups(ctx context.Context, store *GroupStore, builder *common.PolicyTreeBuilder[*model.Group], nsxClient *nsx.Client) error {
	cachedObjs := store.List()
	if len(cachedObjs) == 0 {
		return nil
	}
	log.Info("Cleaning up Groups", "Count", len(cachedObjs))

	cachedGroups := make([]*model.Group, 0)
	for _, obj := range cachedObjs {
		group := obj.(*model.Group)
		group.MarkedForDelete = &MarkedForDelete
		cachedGroups = append(cachedGroups, group)
	}
	return builder.PagingUpdateResources(ctx, cachedGroups, common.DefaultHAPIChildrenCount, nsxClient, func(deletedObjs []*model.Group) {
		store.DeleteMultipleObjects(deletedObjs)
	})
}
