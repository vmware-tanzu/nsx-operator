/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package fabric coordinates the prerequisite resources for a migration.
package fabric

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type Kind string

const (
	TGW        Kind = "transit-gateway"
	IPBlock    Kind = "ip-block"
	Profile    Kind = "connectivity-profile"
	VPC        Kind = "vpc"
	Attachment Kind = "attachment"
)

// Resource contains the identity and dependency fields that must be verified
// against live NSX state. Zones and Blocks are sets, not ordered lists.
type Resource struct {
	Kind            Kind        `json:"kind"`
	ID              string      `json:"id"`
	Path            string      `json:"path"`
	Parent          string      `json:"parent,omitempty"`
	Gateway         string      `json:"gateway,omitempty"`
	Profile         string      `json:"profile,omitempty"`
	CIDR            string      `json:"cidr,omitempty"`
	ExcludedIPs     []IPRange   `json:"excludedIPs,omitempty"`
	SubnetExclusive bool        `json:"subnetExclusive,omitempty"`
	Zones           []string    `json:"zones,omitempty"`
	VDS             []string    `json:"vds,omitempty"`
	Blocks          []string    `json:"blocks,omitempty"`
	Tags            []model.Tag `json:"tags,omitempty"`
}

// FrozenIdentities pins the selected resource identities across retries.
type FrozenIdentities struct {
	TGWID     string `json:"tgwID"`
	ProfileID string `json:"profileID"`
	VPCID     string `json:"vpcID"`
}

// IPRange is an inclusive range of IP addresses.
type IPRange struct{ Start, End string }

// VPCPurpose distinguishes target VPC roles that may share a connectivity profile.
type VPCPurpose string

const (
	VPCPurposePrimary      VPCPurpose = "primary"
	VPCPurposeLoadBalancer VPCPurpose = "load-balancer"
)

// FabricPreparationRequest describes the target topology to prepare in NSX.
type FabricPreparationRequest struct {
	ProjectPath, Identity string
	NamespaceUID          string
	// VPCPurpose separates VPCs with different roles while allowing VDNs with
	// the same purpose and owner to reuse a VPC. Empty defaults to primary.
	VPCPurpose VPCPurpose
	// Frozen is optional because transition status does not currently persist
	// the selected TGW, profile and VPC IDs. Without it, Prepare reselects
	// identities from live inventory on each call, so reuse after an uncertain
	// write or restart is not guaranteed to retain a previously selected ID.
	// Callers that can checkpoint identities should persist ResolveIdentities'
	// result before the first write and pass it here for every retry.
	Frozen *FrozenIdentities
	// SNAT identifies the accepted dedicated block, if the profile includes one.
	SNAT                   *SNATPreparationRequest
	Zones, VDS             []string
	TGWID, VPCID           string
	IPBlockID, IPBlockCIDR string
	// ProfileIPBlockIDs is the complete target set of ordinary blocks linked
	// through the connectivity profile. It may grow across level-driven Prepare
	// calls as the coordinator creates each block. Empty falls back to IPBlockID.
	ProfileIPBlockIDs []string
	// Allowed ranges combine the applicable static, DHCP, VKS and VIP pools.
	// Callers must supply the complete inventory for this block.
	IPBlockAllowedIPs []IPRange
	// Exclusions must be derived from authoritative source network facts by the caller.
	IPBlockExcludedIPs     []IPRange
	IPBlockSubnetExclusive bool
}

type PreparationState string

const (
	PreparationPending PreparationState = "PendingRealization"
	PreparationReady   PreparationState = "Ready"
)

type ResourceRef struct {
	Kind Kind   `json:"kind"`
	Path string `json:"path"`
}

type Result struct {
	TGW, IPBlock, Profile, VPC, Attachment Resource
	Identity                               string           `json:"identity"`
	Identities                             FrozenIdentities `json:"identities"`
	State                                  PreparationState `json:"state"`
	Pending                                *ResourceRef     `json:"pending,omitempty"`
	// Ready is retained for callers that only need the completion predicate.
	Ready bool `json:"ready"`
}

// ConflictError identifies an incompatible object already occupying a stable
// resource identity. Callers can report it as terminal until inputs are fixed.
type ConflictError struct {
	Kind Kind
	Path string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflicting %s at %s", e.Kind, e.Path)
}

type FabricService struct {
	TransitGateways  TransitGatewayService
	IPBlocks         IPBlockService
	Profiles         ConnectivityProfileService
	VPCs             VPCService
	Attachments      AttachmentService
	cluster          string
	mu               sync.Mutex
	initialized      bool
	initializedKinds map[Kind]bool
	query            search.QueryClient
}

// NewFabricService wires the resource-specific NSX clients. It makes no NSX
// requests; each resource service initializes its own store on demand.
func NewFabricService(deps FabricServiceClients) (*FabricService, error) {
	if deps.Cluster == "" || isNil(deps.QueryClient) || isNil(deps.TransitGatewaysClient) || isNil(deps.IPBlocksClient) || isNil(deps.ProfilesClient) || isNil(deps.VPCsClient) || isNil(deps.AttachmentsClient) || isNil(deps.RealizedEntitiesClient) {
		return nil, fmt.Errorf("cluster identity and all NSX resource, query and realization clients are required")
	}
	return &FabricService{
		TransitGateways: TransitGatewayService{Client: deps.TransitGatewaysClient, Realizer: NSXRealizer{Client: deps.RealizedEntitiesClient}, Store: NewResourceStore[model.TransitGateway]()},
		IPBlocks:        IPBlockService{Client: deps.IPBlocksClient, Realizer: NSXRealizer{Client: deps.RealizedEntitiesClient}, Store: NewResourceStore[model.IpAddressBlock]()},
		Profiles:        ConnectivityProfileService{Client: deps.ProfilesClient, Realizer: NSXRealizer{Client: deps.RealizedEntitiesClient}, Store: NewResourceStore[model.VpcConnectivityProfile]()},
		VPCs:            VPCService{Client: deps.VPCsClient, Realizer: NSXRealizer{Client: deps.RealizedEntitiesClient}, Store: NewResourceStore[model.Vpc]()},
		Attachments:     AttachmentService{Client: deps.AttachmentsClient, Realizer: NSXRealizer{Client: deps.RealizedEntitiesClient}, Store: NewResourceStore[model.VpcAttachment]()},
		cluster:         deps.Cluster, query: deps.QueryClient,
	}, nil
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,255}$`)

// Prepare performs one level-driven Execute reconciliation. It stops after a
// write, ensuring that the next reconcile reads and verifies the result before
// proceeding. Ambiguous create errors are returned without changing identity.
func (s *FabricService) Prepare(ctx context.Context, req FabricPreparationRequest) (Result, error) {
	result := Result{Identity: req.Identity, State: PreparationPending}
	if s == nil {
		return result, fmt.Errorf("fabric service is required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := validate(req); err != nil {
		return result, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, snatBlock, err := s.resolveIdentitiesLocked(ctx, req)
	if err != nil {
		return result, err
	}
	result.Identities = ids
	base := strings.TrimSuffix(req.ProjectPath, "/")
	tgwID, profileID, vpcID := ids.TGWID, ids.ProfileID, ids.VPCID
	result.TGW = s.TransitGateways.expected(req, base, tgwID)
	result.IPBlock = s.IPBlocks.expected(req, base)
	profileBlocks, err := profileBlockPaths(req, base)
	if err != nil {
		return result, err
	}
	result.Profile = s.Profiles.expected(base, profileID, result.TGW, profileBlocks)

	result.VPC = s.VPCs.expected(base, vpcID)
	result.Attachment = s.Attachments.expected(result.VPC, result.Profile)
	resources := []Resource{result.TGW}
	if result.IPBlock.Path != "" {
		resources = append(resources, result.IPBlock)
	}
	resources = append(resources, result.Profile, result.VPC, result.Attachment)
	for _, expected := range resources {
		expected.Tags = s.tags(expected.Kind, req.NamespaceUID, vpcPurpose(req))
		ready, err := s.prepareResource(ctx, expected, snatBlock)
		if err != nil {
			return result, err
		}
		if !ready {
			result.Pending = &ResourceRef{Kind: expected.Kind, Path: expected.Path}
			return result, nil
		}
	}
	result.Ready = true
	result.State = PreparationReady
	return result, nil
}

// PrepareIPBlock reconciles and verifies a source network's block separately
// so a shared connectivity profile can be created only after all of its block
// dependencies exist.
func (s *FabricService) PrepareIPBlock(ctx context.Context, req FabricPreparationRequest) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("fabric service is required")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validate(req); err != nil {
		return false, err
	}
	if req.IPBlockID == "" {
		return false, fmt.Errorf("IP block identity is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, snatBlock, err := s.resolveIdentitiesLocked(ctx, req)
	if err != nil {
		return false, err
	}
	expected := s.IPBlocks.expected(req, req.ProjectPath)
	expected.Tags = s.tags(IPBlock, req.NamespaceUID, vpcPurpose(req))
	return s.prepareResource(ctx, expected, snatBlock)
}

// ResolveIdentities discovers identities without modifying NSX. Persist its
// result in the transition checkpoint before calling Prepare with Frozen set.
func (s *FabricService) ResolveIdentities(ctx context.Context, req FabricPreparationRequest) (FrozenIdentities, error) {
	if s == nil {
		return FrozenIdentities{}, fmt.Errorf("fabric service is required")
	}
	if err := ctx.Err(); err != nil {
		return FrozenIdentities{}, err
	}
	if err := validate(req); err != nil {
		return FrozenIdentities{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, _, err := s.resolveIdentitiesLocked(ctx, req)
	return ids, err
}

func (s *FabricService) resolveIdentitiesLocked(ctx context.Context, req FabricPreparationRequest) (FrozenIdentities, string, error) {
	needed := []Kind{TGW, Profile, VPC, Attachment}
	if req.IPBlockID != "" {
		needed = append(needed, IPBlock)
	}
	if err := s.refreshStoreLocked(ctx, needed...); err != nil {
		return FrozenIdentities{}, "", fmt.Errorf("initialize fabric stores: %w", err)
	}
	base := strings.TrimSuffix(req.ProjectPath, "/")
	tgwID := name(req.TGWID, "tgw", req.Identity)
	vpcID := name(req.VPCID, "vpc", req.Identity)
	profileID := name("", "profile", req.Identity+"\x00"+tgwID+"\x00"+vpcID)
	snatBlock, err := s.validatedSNATBlock(ctx, req)
	if err != nil {
		return FrozenIdentities{}, "", err
	}
	// Policy is authoritative even when search has not indexed the chosen ID yet.
	if req.Frozen == nil {
		requested := s.TransitGateways.expected(req, base, tgwID)
		actual, found, readErr := s.TransitGateways.Get(ctx, requested)
		if readErr != nil {
			return FrozenIdentities{}, "", fmt.Errorf("read requested transit gateway %s: %w", requested.Path, readErr)
		}
		if found {
			requested.Tags = s.tags(TGW, req.NamespaceUID, vpcPurpose(req))
			if !compatible(requested, actual) {
				return FrozenIdentities{}, "", &ConflictError{Kind: TGW, Path: requested.Path}
			}
		}
	}
	tgwID, profileID, vpcID, err = s.reusableIDs(req, base, tgwID, profileID, vpcID, snatBlock)
	if err != nil {
		return FrozenIdentities{}, "", err
	}
	return FrozenIdentities{TGWID: tgwID, ProfileID: profileID, VPCID: vpcID}, snatBlock, nil
}

// prepareResource performs one idempotent reconciliation step for a resource.
func (s *FabricService) prepareResource(ctx context.Context, expected Resource, snatBlock string) (bool, error) {
	access, err := s.access(expected.Kind)
	if err != nil {
		return false, err
	}
	actual, found, err := access.Get(ctx, expected)
	if err != nil {
		return false, fmt.Errorf("read %s %s: %w", expected.Kind, expected.Path, err)
	}
	if !found {
		if err := access.Create(ctx, expected, expected.Tags); err != nil {
			return false, fmt.Errorf("create %s %s: %w", expected.Kind, expected.Path, err)
		}
		return false, nil
	}
	if expected.Kind == Profile {
		ordinaryBlocks := make([]string, 0, len(actual.Blocks))
		hasSNATBlock := false
		for _, block := range actual.Blocks {
			if snatBlock != "" && block == snatBlock {
				hasSNATBlock = true
				continue
			}
			ordinaryBlocks = append(ordinaryBlocks, block)
		}
		actualForCompare := actual
		actualForCompare.Blocks = ordinaryBlocks
		expectedForCompare := expected
		expectedForCompare.Blocks = ordinaryBlocks
		if !compatible(expectedForCompare, actualForCompare) {
			return false, &ConflictError{Kind: expected.Kind, Path: expected.Path}
		}
		if !equalSet(ordinaryBlocks, expected.Blocks) {
			if !subset(ordinaryBlocks, expected.Blocks) {
				return false, &ConflictError{Kind: expected.Kind, Path: expected.Path}
			}
			desiredBlocks := append([]string(nil), expected.Blocks...)
			if hasSNATBlock {
				desiredBlocks = append(desiredBlocks, snatBlock)
			}
			updater, ok := access.(interface {
				UpdateBlocks(context.Context, Resource, []string) error
			})
			if !ok {
				return false, fmt.Errorf("connectivity profile service cannot update external IP blocks")
			}
			if err := updater.UpdateBlocks(ctx, actual, sorted(desiredBlocks)); err != nil {
				return false, fmt.Errorf("update connectivity profile %s blocks: %w", expected.Path, err)
			}
			return false, nil
		}
		actual.Blocks = ordinaryBlocks
	} else if !compatible(expected, actual) {
		return false, &ConflictError{Kind: expected.Kind, Path: expected.Path}
	}
	if expected.Kind == Profile && !compatible(expected, actual) {
		return false, &ConflictError{Kind: expected.Kind, Path: expected.Path}
	}
	return access.CheckRealization(ctx, actual)
}

// ResourceAccess is implemented by the resource-specific services below.
// Each implementation owns exactly one NSX Policy client.
type ResourceAccess interface {
	initializeStore(context.Context, search.QueryClient, string) error
	Get(context.Context, Resource) (Resource, bool, error)
	GetRealized(context.Context, Resource) (Resource, bool, bool, error)
	CheckRealization(context.Context, Resource) (bool, error)
	Create(context.Context, Resource, []model.Tag) error
	StoreQueryResult(string, *data.StructValue) error
}
type Realizer interface {
	Ready(context.Context, string) (bool, error)
}

func (s *FabricService) access(kind Kind) (ResourceAccess, error) {
	var access ResourceAccess
	switch kind {
	case TGW:
		access = s.TransitGateways
	case IPBlock:
		access = s.IPBlocks
	case Profile:
		access = s.Profiles
	case VPC:
		access = s.VPCs
	case Attachment:
		access = s.Attachments
	default:
		return nil, fmt.Errorf("unsupported fabric resource kind %q", kind)
	}
	return access, nil
}
func (s *FabricService) tags(kind Kind, namespaceUID string, purpose VPCPurpose) []model.Tag {
	// TODO: Confirm whether migration-created resources should use the same tags
	// as NSX Operator-created resources, including nsx/managed-by=nsx-op where
	// applicable. See the VPC and attachment builders before changing ownership tags.
	tags := []model.Tag{{Scope: common.String(common.TagScopeCluster), Tag: common.String(s.cluster)}}
	if namespaceUID != "" && (kind == IPBlock || kind == VPC || kind == Attachment) {
		tags = append(tags, model.Tag{Scope: common.String(common.TagScopeNamespaceUID), Tag: common.String(namespaceUID)})
	}
	if kind == VPC {
		tags = append(tags, model.Tag{Scope: common.String(migrationVPCPurposeTagScope), Tag: common.String(string(purpose))})
	}
	return tags
}
