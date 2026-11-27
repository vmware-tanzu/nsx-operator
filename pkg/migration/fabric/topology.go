/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (s *FabricService) reusableIDs(req FabricPreparationRequest, project, requestedTGW, requestedProfile, requestedVPC, snatBlock string) (string, string, string, error) {
	if req.Frozen != nil {
		f := req.Frozen
		for _, id := range []string{f.TGWID, f.ProfileID, f.VPCID} {
			if !validResourceID(id) {
				return "", "", "", fmt.Errorf("invalid frozen resource identity %q", id)
			}
		}
		if req.TGWID != "" && req.TGWID != f.TGWID || req.VPCID != "" && req.VPCID != f.VPCID {
			return "", "", "", fmt.Errorf("requested resource identity conflicts with frozen identities")
		}
		if req.SNAT != nil && (req.SNAT.ProfilePath != project+"/vpc-connectivity-profiles/"+f.ProfileID || req.SNAT.VPCPath != project+"/vpcs/"+f.VPCID) {
			return "", "", "", fmt.Errorf("SNAT paths conflict with frozen identities")
		}
		return f.TGWID, f.ProfileID, f.VPCID, nil
	}
	tgwID, err := s.TransitGateways.reusableID(req, s.cluster, requestedTGW)
	if err != nil {
		return "", "", "", err
	}
	tgwPath := project + "/transit-gateways/" + tgwID
	profileBlocks, err := profileBlockPaths(req, project)
	if err != nil {
		return "", "", "", err
	}
	profileID := ""
	if req.VPCID != "" {
		profileID, err = attachedProfileForVPC(s.Attachments, s.Profiles, project, req.VPCID, tgwPath, s.cluster)
		if err != nil {
			return "", "", "", err
		}
	}
	if profileID == "" {
		profileID, err = s.Profiles.reusableID(s.cluster, project, tgwID, tgwPath, profileBlocks, requestedProfile, snatBlock)
		if err != nil {
			return "", "", "", err
		}
	}
	if req.SNAT != nil {
		expected := project + "/vpc-connectivity-profiles/"
		frozenProfileID := strings.TrimPrefix(req.SNAT.ProfilePath, expected)
		if !strings.HasPrefix(req.SNAT.ProfilePath, expected) || !validResourceID(frozenProfileID) {
			return "", "", "", fmt.Errorf("invalid frozen SNAT profile path %q", req.SNAT.ProfilePath)
		}
		if profileID != requestedProfile && profileID != frozenProfileID {
			return "", "", "", fmt.Errorf("existing profile %q conflicts with frozen profile %q", profileID, frozenProfileID)
		}
		profileID = frozenProfileID
	}
	profilePath := project + "/vpc-connectivity-profiles/" + profileID
	vpcID, err := s.VPCs.reusableID(s.Attachments, req, s.cluster, project, profileID, profilePath, requestedVPC)
	if err != nil {
		return "", "", "", err
	}
	if req.SNAT != nil {
		frozenVPC, err := parseVPCPath(req.SNAT.VPCPath)
		if err != nil || req.SNAT.VPCPath != project+"/vpcs/"+frozenVPC.VPCID {
			return "", "", "", fmt.Errorf("invalid frozen SNAT VPC path %q", req.SNAT.VPCPath)
		}
		frozenID := frozenVPC.VPCID
		if vpcID != requestedVPC && vpcID != frozenID {
			return "", "", "", fmt.Errorf("selected VPC %q conflicts with frozen SNAT VPC %q", vpcID, frozenID)
		}
		if req.VPCID != "" && req.VPCID != frozenID {
			return "", "", "", fmt.Errorf("requested VPC %q conflicts with frozen SNAT VPC %q", req.VPCID, frozenID)
		}
		vpcID = frozenID
	}
	return tgwID, profileID, vpcID, nil
}

func attachedProfileForVPC(attachments AttachmentService, profiles ConnectivityProfileService, project, vpcID, tgwPath, cluster string) (string, error) {
	vpcPath := project + "/vpcs/" + vpcID
	var profilePath string
	for _, attachment := range attachments.Store.List() {
		if attachment == nil || value(attachment.ParentPath) != vpcPath ||
			(attachment.MarkedForDelete != nil && *attachment.MarkedForDelete) {
			continue
		}
		candidate := value(attachment.VpcConnectivityProfile)
		if candidate == "" {
			continue
		}
		if profilePath != "" && profilePath != candidate {
			return "", fmt.Errorf("VPC %q has conflicting live connectivity profile attachments", vpcID)
		}
		profilePath = candidate
	}
	if profilePath == "" {
		return "", nil
	}
	profile, found := profiles.Store.Get(profilePath)
	if !found || profile == nil {
		return "", fmt.Errorf("VPC %q attachment references an unresolved connectivity profile %q", vpcID, profilePath)
	}
	profileID := value(profile.Id)
	if profileID == "" || value(profile.Path) != profilePath || value(profile.ParentPath) != project ||
		value(profile.TransitGatewayPath) != tgwPath ||
		(profile.MarkedForDelete != nil && *profile.MarkedForDelete) || !ownedByCluster(profile.Tags, cluster) {
		return "", &ConflictError{Kind: Profile, Path: profilePath}
	}
	return profileID, nil
}

// validResourceID validates one path segment used as an NSX resource identity.
// Dot-only segments are excluded because they are non-canonical path identities.
func validResourceID(id string) bool {
	return validID.MatchString(id) && id != "." && id != ".."
}

func ownedByCluster(tags []model.Tag, cluster string) bool {
	return tagValue(tags, common.TagScopeCluster) == cluster
}
func ownedBy(cluster, namespaceUID string, tags []model.Tag) bool {
	if !ownedByCluster(tags, cluster) {
		return false
	}
	if namespaceUID == "" {
		return !hasTagScope(tags, common.TagScopeNamespaceUID)
	}
	return tagValue(tags, common.TagScopeNamespaceUID) == namespaceUID
}
func tagValue(tags []model.Tag, scope string) string {
	for _, tag := range tags {
		if value(tag.Scope) == scope {
			return value(tag.Tag)
		}
	}
	return ""
}
func hasTagScope(tags []model.Tag, scope string) bool {
	for _, tag := range tags {
		if value(tag.Scope) == scope {
			return true
		}
	}
	return false
}

func vpcPurpose(req FabricPreparationRequest) VPCPurpose {
	if req.VPCPurpose == "" {
		return VPCPurposePrimary
	}
	return req.VPCPurpose
}

func validate(r FabricPreparationRequest) error {
	parts := strings.Split(r.ProjectPath, "/")
	if len(parts) != 5 || parts[0] != "" || parts[1] != "orgs" || parts[3] != "projects" || !validResourceID(parts[2]) || !validResourceID(parts[4]) {
		return fmt.Errorf("invalid project path %q", r.ProjectPath)
	}
	if r.Identity == "" || len(r.Zones) == 0 {
		return fmt.Errorf("stable identity and zone span are required")
	}
	if r.VPCPurpose != "" && r.VPCPurpose != VPCPurposePrimary && r.VPCPurpose != VPCPurposeLoadBalancer && r.VPCPurpose != VPCPurposeWorkload {
		return fmt.Errorf("unsupported VPC purpose %q", r.VPCPurpose)
	}
	if r.SNAT != nil && vpcPurpose(r) != VPCPurposeLoadBalancer {
		return fmt.Errorf("SNAT preparation requires load-balancer VPC purpose")
	}
	for _, z := range r.Zones {
		if z == "" {
			return fmt.Errorf("zone identity must not be empty")
		}
	}
	for _, vds := range r.VDS {
		if strings.TrimSpace(vds) == "" {
			return fmt.Errorf("VDS identity must not be empty")
		}
	}
	for _, id := range []string{r.TGWID, r.VPCID, r.IPBlockID} {
		if id != "" && !validResourceID(id) {
			return fmt.Errorf("invalid resource ID %q", id)
		}
	}
	for _, id := range r.ProfileIPBlockIDs {
		if !validResourceID(id) {
			return fmt.Errorf("invalid connectivity profile IP block ID %q", id)
		}
	}
	if (r.IPBlockID == "") != (r.IPBlockCIDR == "") {
		return fmt.Errorf("IP block ID and CIDR must be supplied together")
	}
	if r.IPBlockID == "" && (len(r.IPBlockAllowedIPs) != 0 || len(r.IPBlockExcludedIPs) != 0 || r.IPBlockSubnetExclusive) {
		return fmt.Errorf("IP block exclusions and subnet-exclusive flag require an IP block")
	}
	if r.IPBlockCIDR != "" {
		p, err := netip.ParsePrefix(r.IPBlockCIDR)
		if err != nil || !p.Addr().Is4() || p != p.Masked() {
			return fmt.Errorf("invalid IP block CIDR %q", r.IPBlockCIDR)
		}
	}
	if _, err := requestIPBlockExclusions(r); err != nil {
		return err
	}
	return nil
}

func name(override, kind, identity string) string {
	if override != "" {
		return override
	}
	h := sha256.Sum256([]byte(identity))
	return "migration-" + kind + "-" + hex.EncodeToString(h[:12])
}
func sorted(values []string) []string {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
func equalSet(a, b []string) bool {
	return strings.Join(sorted(a), "\x00") == strings.Join(sorted(b), "\x00")
}

func subset(values, set []string) bool {
	available := make(map[string]struct{}, len(set))
	for _, value := range set {
		available[value] = struct{}{}
	}
	for _, value := range values {
		if _, found := available[value]; !found {
			return false
		}
	}
	return true
}

// containsAll reports whether the actual set contains every required value.
func containsAll(actual, required []string) bool {
	return subset(required, actual)
}

func profileBlockPaths(req FabricPreparationRequest, project string) ([]string, error) {
	ids := append([]string(nil), req.ProfileIPBlockIDs...)
	if req.IPBlockID != "" {
		ids = append(ids, req.IPBlockID)
	}
	ids = sorted(ids)
	paths := make([]string, 0, len(ids))
	previous := ""
	for _, id := range ids {
		if !validResourceID(id) {
			return nil, fmt.Errorf("invalid connectivity profile IP block ID %q", id)
		}
		if id == previous {
			continue
		}
		paths = append(paths, project+"/infra/ip-blocks/"+id)
		previous = id
	}
	return paths, nil
}
func compatible(a, b Resource) bool {
	if a.Kind != b.Kind || a.ID != b.ID || a.Path != b.Path || a.Parent != b.Parent || a.Gateway != b.Gateway || a.Profile != b.Profile || a.CIDR != b.CIDR || a.SubnetExclusive != b.SubnetExclusive || !slices.Equal(a.ExcludedIPs, b.ExcludedIPs) || !equalSet(a.Zones, b.Zones) || !equalSet(a.Blocks, b.Blocks) {
		return false
	}
	if a.Kind == TGW && !containsAll(b.VDS, a.VDS) || a.Kind != TGW && !equalSet(a.VDS, b.VDS) {
		return false
	}
	if a.Kind == VPC && !a.NamespaceShared {
		expectedNamespaceUID := tagValue(a.Tags, common.TagScopeNamespaceUID)
		if expectedNamespaceUID == "" {
			if hasTagScope(b.Tags, common.TagScopeNamespaceUID) {
				return false
			}
		} else if tagValue(b.Tags, common.TagScopeNamespaceUID) != expectedNamespaceUID {
			return false
		}
	}
	for _, required := range a.Tags {
		if tagValue(b.Tags, value(required.Scope)) != value(required.Tag) {
			return false
		}
	}
	return true
}
