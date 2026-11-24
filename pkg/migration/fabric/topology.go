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
	blockPath := ""
	if req.IPBlockID != "" {
		blockPath = project + "/infra/ip-blocks/" + req.IPBlockID
	}
	profileID, err := s.Profiles.reusableID(s.cluster, project, tgwID, tgwPath, blockPath, requestedProfile, snatBlock)
	if err != nil {
		return "", "", "", err
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

// validResourceID validates one path segment used as an NSX resource identity.
// Dot-only segments are excluded because they are non-canonical path identities.
func validResourceID(id string) bool {
	return validID.MatchString(id) && id != "." && id != ".."
}

func optionalPath(path string) []string {
	if path == "" {
		return nil
	}
	return []string{path}
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
	if r.VPCPurpose != "" && r.VPCPurpose != VPCPurposePrimary && r.VPCPurpose != VPCPurposeLoadBalancer {
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
func compatible(a, b Resource) bool {
	if a.Kind != b.Kind || a.ID != b.ID || a.Path != b.Path || a.Parent != b.Parent || a.Gateway != b.Gateway || a.Profile != b.Profile || a.CIDR != b.CIDR || a.SubnetExclusive != b.SubnetExclusive || !slices.Equal(a.ExcludedIPs, b.ExcludedIPs) || !equalSet(a.Zones, b.Zones) || !equalSet(a.VDS, b.VDS) || !equalSet(a.Blocks, b.Blocks) {
		return false
	}
	if a.Kind == VPC {
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
