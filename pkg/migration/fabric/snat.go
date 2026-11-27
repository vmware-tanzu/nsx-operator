/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	lbServiceID          = "default"
	snatIdentityTagScope = "migration.network.vmware.com/snat-identity"
)

// SNATPreparationDependencies contains only the clients used by the dedicated
// target LB SNAT flow. It can be constructed independently of the generic
// fabric resource services.
type SNATPreparationDependencies struct {
	Cluster     string
	IPBlocks    infra.IpBlocksClient
	Profiles    projects.VpcConnectivityProfilesClient
	Allocations vpcs.IpAddressAllocationsClient
	LBServices  vpcs.VpcLbsClient
	Realizer    Realizer
}

type SNATPreparationService struct {
	cluster     string
	ipBlocks    infra.IpBlocksClient
	profiles    projects.VpcConnectivityProfilesClient
	allocations vpcs.IpAddressAllocationsClient
	lbServices  vpcs.VpcLbsClient
	realizer    Realizer
	stores      *Stores
}

type SNATPreparationRequest struct {
	Identity    string
	VPCPath     string
	ProfilePath string
	IPAddress   string
	LBSize      string
}

type SNATPreparationResult struct {
	Identity       string
	IPAddress      string
	State          PreparationState
	Pending        *ResourceRef
	IPBlockPath    string
	ProfilePath    string
	AllocationPath string
	LBServicePath  string
	Ready          bool
}

func NewSNATPreparationService(deps SNATPreparationDependencies) (*SNATPreparationService, error) {
	if deps.Cluster == "" || isNil(deps.IPBlocks) || isNil(deps.Profiles) || isNil(deps.Allocations) || isNil(deps.LBServices) || isNil(deps.Realizer) {
		return nil, fmt.Errorf("cluster identity and SNAT NSX clients are required")
	}
	return &SNATPreparationService{cluster: deps.Cluster, ipBlocks: deps.IPBlocks, profiles: deps.Profiles,
		allocations: deps.Allocations, lbServices: deps.LBServices, realizer: deps.Realizer}, nil
}

// Prepare configures the accepted Evaluate address in the target LB VPC. Each
// call reads by deterministic identity, patches only a missing resource, then
// waits for realization before progressing to its dependent resource.
func (s *SNATPreparationService) Prepare(ctx context.Context, req SNATPreparationRequest) (SNATPreparationResult, error) {
	result := SNATPreparationResult{Identity: req.Identity, IPAddress: req.IPAddress, State: PreparationPending}
	if s == nil {
		return result, fmt.Errorf("SNAT preparation service is required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	plan, result, err := buildSNATPlan(req, s.cluster, result)
	if err != nil {
		return result, err
	}
	steps := []struct {
		kind    Kind
		path    string
		prepare func(context.Context, snatPlan) (bool, error)
	}{
		{Kind("snat-ip-block"), plan.blockPath, s.prepareIPBlock},
		{Kind("snat-profile"), req.ProfilePath, s.prepareProfile},
		{Kind("snat-ip-allocation"), plan.allocationPath, s.prepareAllocation},
		{Kind("snat-lb-service"), plan.lbPath, s.prepareLBService},
	}
	for _, step := range steps {
		ready, err := step.prepare(ctx, plan)
		if err != nil {
			return result, err
		}
		if !ready {
			return snatPending(result, step.kind, step.path), nil
		}
	}
	result.State = PreparationReady
	result.Ready = true
	return result, nil
}

type snatPlan struct {
	req                                                                 SNATPreparationRequest
	vpc                                                                 common.VPCResourceInfo
	ip                                                                  netip.Addr
	profileID, blockID, blockPath, allocationID, allocationPath, lbPath string
	tags                                                                []model.Tag
}

func buildSNATPlan(req SNATPreparationRequest, cluster string, result SNATPreparationResult) (snatPlan, SNATPreparationResult, error) {
	if req.Identity == "" || req.VPCPath == "" || req.ProfilePath == "" || req.LBSize == "" {
		return snatPlan{}, result, fmt.Errorf("SNAT identity, target VPC path, connectivity profile path and LB size are required")
	}
	switch req.LBSize {
	case model.LBService_SIZE_SMALL, model.LBService_SIZE_MEDIUM, model.LBService_SIZE_LARGE, model.LBService_SIZE_XLARGE, model.LBService_SIZE_DLB:
	default:
		return snatPlan{}, result, fmt.Errorf("unsupported LB size %q", req.LBSize)
	}
	ip, err := netip.ParseAddr(req.IPAddress)
	if err != nil || !ip.Is4() || ip.String() != req.IPAddress {
		return snatPlan{}, result, fmt.Errorf("SNAT address must be a canonical IPv4 address")
	}
	vpc, err := parseVPCPath(req.VPCPath)
	if err != nil {
		return snatPlan{}, result, fmt.Errorf("invalid target VPC path %q", req.VPCPath)
	}
	profileParts := strings.Split(req.ProfilePath, "/")
	if len(profileParts) != 7 || profileParts[0] != "" || profileParts[1] != "orgs" || profileParts[3] != "projects" || profileParts[5] != "vpc-connectivity-profiles" || profileParts[2] != vpc.OrgID || profileParts[4] != vpc.ProjectID || !validResourceID(profileParts[6]) {
		return snatPlan{}, result, fmt.Errorf("invalid target connectivity profile path %q", req.ProfilePath)
	}
	identityHash := sha256.Sum256([]byte(req.Identity + "\x00" + req.VPCPath))
	identitySuffix := hex.EncodeToString(identityHash[:8])
	blockID := "migration-lb-snat-" + identitySuffix
	blockPath := fmt.Sprintf("/orgs/%s/projects/%s/infra/ip-blocks/%s", vpc.OrgID, vpc.ProjectID, blockID)
	allocationID := common.LBServiceIPAllocationID
	allocationPath := fmt.Sprintf("%s/ip-address-allocations/%s", req.VPCPath, allocationID)
	lbPath := fmt.Sprintf("%s/vpc-lbs/%s", req.VPCPath, lbServiceID)
	result.IPBlockPath = blockPath
	result.ProfilePath = req.ProfilePath
	result.AllocationPath = allocationPath
	result.LBServicePath = lbPath
	// TODO: Compare these migration ownership tags with NSX Operator conventions,
	// including whether nsx/managed-by=nsx-op should be added to SNAT resources.
	tags := []model.Tag{
		{Scope: common.String(common.TagScopeCluster), Tag: common.String(cluster)},
		{Scope: common.String(snatIdentityTagScope), Tag: common.String(req.Identity)},
	}

	return snatPlan{req: req, vpc: vpc, ip: ip, profileID: profileParts[6], blockID: blockID, blockPath: blockPath, allocationID: allocationID, allocationPath: allocationPath, lbPath: lbPath, tags: tags}, result, nil
}

func (s *SNATPreparationService) prepareIPBlock(ctx context.Context, plan snatPlan) (bool, error) {
	block, err := s.ipBlocks.Get(plan.vpc.OrgID, plan.vpc.ProjectID, plan.blockID, nil)
	if absent(err) {
		cidr := plan.ip.String() + "/32"
		displayName := plan.blockID
		visibility := model.IpAddressBlock_VISIBILITY_EXTERNAL
		desired := model.IpAddressBlock{Id: &plan.blockID, DisplayName: &displayName, Cidrs: []string{cidr}, Visibility: &visibility, Tags: plan.tags}
		if err := s.ipBlocks.Patch(plan.vpc.OrgID, plan.vpc.ProjectID, plan.blockID, desired); err != nil {
			return false, fmt.Errorf("create SNAT IPBlock %s: %w", plan.blockPath, err)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get SNAT IPBlock %s: %w", plan.blockPath, err)
	}
	if !sameSNATBlock(block, plan.blockID, plan.blockPath, plan.ip.String()+"/32", plan.req.Identity, s.cluster) {
		return false, &ConflictError{Kind: IPBlock, Path: plan.blockPath}
	}
	return s.realizer.Ready(ctx, plan.blockPath)
}

func (s *SNATPreparationService) prepareProfile(ctx context.Context, plan snatPlan) (bool, error) {
	profile, err := s.profiles.Get(plan.vpc.OrgID, plan.vpc.ProjectID, plan.profileID)
	if s.stores != nil {
		s.stores.Profiles.Delete(plan.req.ProfilePath)
	}
	if err != nil {
		return false, fmt.Errorf("get target connectivity profile %s: %w", plan.req.ProfilePath, err)
	}
	if (profile.MarkedForDelete != nil && *profile.MarkedForDelete) || value(profile.Id) != plan.profileID || value(profile.Path) != plan.req.ProfilePath || !hasTag(profile.Tags, common.TagScopeCluster, s.cluster) {
		return false, &ConflictError{Kind: Profile, Path: plan.req.ProfilePath}
	}
	linked := false
	for _, path := range profile.ExternalIpBlocks {
		if path == plan.blockPath {
			linked = true
			break
		}
	}
	if !linked {
		profile.ExternalIpBlocks = append(profile.ExternalIpBlocks, plan.blockPath)
		if err := s.profiles.Patch(plan.vpc.OrgID, plan.vpc.ProjectID, plan.profileID, profile); err != nil {
			return false, fmt.Errorf("link SNAT IPBlock %s to connectivity profile %s: %w", plan.blockPath, plan.req.ProfilePath, err)
		}
		return false, nil
	}
	return s.realizer.Ready(ctx, plan.req.ProfilePath)
}

func (s *SNATPreparationService) prepareAllocation(ctx context.Context, plan snatPlan) (bool, error) {
	allocation, err := s.allocations.Get(plan.vpc.OrgID, plan.vpc.ProjectID, plan.vpc.VPCID, plan.allocationID)
	if absent(err) {
		displayName := plan.allocationID
		visibility := model.VpcIpAddressAllocation_IP_ADDRESS_BLOCK_VISIBILITY_EXTERNAL
		ipType := model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV4
		desired := model.VpcIpAddressAllocation{Id: &plan.allocationID, DisplayName: &displayName, ParentPath: &plan.req.VPCPath,
			AllocationIp: common.String(plan.ip.String()), IpAddressBlockVisibility: &visibility, IpAddressType: &ipType,
			Tags: []model.Tag{{Scope: common.String(common.TagScopeCluster), Tag: common.String(s.cluster)},
				{Scope: common.String(common.TagScopeVPCService), Tag: common.String(common.TagValueUserSpecifiedIP)},
				{Scope: common.String(snatIdentityTagScope), Tag: common.String(plan.req.Identity)}}}
		if err := s.allocations.Patch(plan.vpc.OrgID, plan.vpc.ProjectID, plan.vpc.VPCID, plan.allocationID, desired); err != nil {
			return false, fmt.Errorf("create SNAT VPC IP allocation %s: %w", plan.allocationPath, err)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get SNAT VPC IP allocation %s: %w", plan.allocationPath, err)
	}
	if !sameSNATAllocation(allocation, plan.allocationID, plan.allocationPath, plan.req.VPCPath, plan.ip.String(), plan.req.Identity, s.cluster) {
		return false, &ConflictError{Kind: Kind("snat-ip-allocation"), Path: plan.allocationPath}
	}
	return s.realizer.Ready(ctx, plan.allocationPath)
}

func (s *SNATPreparationService) prepareLBService(ctx context.Context, plan snatPlan) (bool, error) {
	lb, err := s.lbServices.Get(plan.vpc.OrgID, plan.vpc.ProjectID, plan.vpc.VPCID, lbServiceID)
	if absent(err) {
		displayName := lbServiceID
		desired := model.LBService{Id: common.String(lbServiceID), DisplayName: &displayName,
			ConnectivityPath: &plan.req.VPCPath, Size: &plan.req.LBSize,
			Tags: append(append([]model.Tag(nil), plan.tags...), model.Tag{Scope: common.String(common.TagScopeCreatedFor), Tag: common.String(common.TagValueSLB)})}
		if err := s.lbServices.Patch(plan.vpc.OrgID, plan.vpc.ProjectID, plan.vpc.VPCID, lbServiceID, desired, nil); err != nil {
			return false, fmt.Errorf("create SNAT LBService %s: %w", plan.lbPath, err)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get SNAT LBService %s: %w", plan.lbPath, err)
	}
	if !sameSNATLB(lb, lbServiceID, plan.lbPath, plan.req.VPCPath, plan.req.LBSize, plan.req.Identity, s.cluster) {
		return false, &ConflictError{Kind: Kind("snat-lb-service"), Path: plan.lbPath}
	}
	return s.realizer.Ready(ctx, plan.lbPath)
}

func snatPending(result SNATPreparationResult, kind Kind, path string) SNATPreparationResult {
	result.State = PreparationPending
	result.Pending = &ResourceRef{Kind: kind, Path: path}
	return result
}

func hasSNATIdentity(tags []model.Tag, identity string) bool {
	for _, tag := range tags {
		if value(tag.Scope) == snatIdentityTagScope && value(tag.Tag) == identity {
			return true
		}
	}
	return false
}
func sameSNATBlock(r model.IpAddressBlock, id, path, cidr, identity, cluster string) bool {
	actualCIDR, err := ipBlockCIDR(r)
	return err == nil && (r.MarkedForDelete == nil || !*r.MarkedForDelete) && value(r.Id) == id && value(r.Path) == path && actualCIDR == cidr &&
		value(r.Visibility) == model.IpAddressBlock_VISIBILITY_EXTERNAL && hasSNATIdentity(r.Tags, identity) && hasTag(r.Tags, common.TagScopeCluster, cluster)
}
func sameSNATAllocation(r model.VpcIpAddressAllocation, id, path, parent, ip, identity, cluster string) bool {
	return (r.MarkedForDelete == nil || !*r.MarkedForDelete) && value(r.Id) == id && value(r.Path) == path && value(r.ParentPath) == parent && value(r.AllocationIp) == ip &&
		value(r.IpAddressType) == model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV4 &&
		value(r.IpAddressBlockVisibility) == model.VpcIpAddressAllocation_IP_ADDRESS_BLOCK_VISIBILITY_EXTERNAL && hasSNATIdentity(r.Tags, identity) &&
		hasTag(r.Tags, common.TagScopeCluster, cluster) && hasTag(r.Tags, common.TagScopeVPCService, common.TagValueUserSpecifiedIP)
}
func sameSNATLB(r model.LBService, id, path, vpcPath, size, identity, cluster string) bool {
	return (r.MarkedForDelete == nil || !*r.MarkedForDelete) && value(r.Id) == id && value(r.Path) == path && value(r.ConnectivityPath) == vpcPath && value(r.Size) == size &&
		hasSNATIdentity(r.Tags, identity) && hasTag(r.Tags, common.TagScopeCluster, cluster) && hasTag(r.Tags, common.TagScopeCreatedFor, common.TagValueSLB)
}
func hasTag(tags []model.Tag, scope, tag string) bool {
	for _, value := range tags {
		if value.Scope != nil && value.Tag != nil && *value.Scope == scope && *value.Tag == tag {
			return true
		}
	}
	return false
}

// validatedSNATBlock accepts only the dedicated block for the frozen SNAT request.
func (s *FabricService) validatedSNATBlock(ctx context.Context, req FabricPreparationRequest) (string, error) {
	if req.SNAT == nil {
		return "", nil
	}
	plan, _, err := buildSNATPlan(*req.SNAT, s.cluster, SNATPreparationResult{})
	if err != nil {
		return "", err
	}
	if req.ProjectPath != "/orgs/"+plan.vpc.OrgID+"/projects/"+plan.vpc.ProjectID {
		return "", fmt.Errorf("SNAT VPC is outside target project")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	block, err := s.IPBlocks.Client.Get(plan.vpc.OrgID, plan.vpc.ProjectID, plan.blockID, nil)
	if absent(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read accepted SNAT IPBlock %s: %w", plan.blockPath, err)
	}
	if !sameSNATBlock(block, plan.blockID, plan.blockPath, plan.ip.String()+"/32", plan.req.Identity, s.cluster) {
		return "", &ConflictError{Kind: IPBlock, Path: plan.blockPath}
	}
	return plan.blockPath, nil
}
