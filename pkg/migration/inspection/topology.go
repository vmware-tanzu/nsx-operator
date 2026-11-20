/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"fmt"
	"sort"
	"strings"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

// TargetInventory is a read-only snapshot of migration resources already in
// NSX. Implementations must return only resources in the configured migration
// org/project scope and include their live attachment and source bindings.
// PortSettings must be filtered by the migration tag and resolved to their
// current subnet and DVC; an incomplete search must return an error.
type TargetInventory interface {
	ListTopology(context.Context) ([]TargetTopology, error)
}

// TargetTopology describes one TGW/VPC pair and its tagged converted PortSettings.
// The inventory must verify the migration tag and source DVPG identity, and
// resolve the parent subnet and DVC from current NSX resources.
type TargetTopology struct {
	TGWID, TGWName     string
	ZoneIDs, VDSUUIDs  []string
	VPCID, VPCName     string
	VPCReady, Attached bool
	PortSettings       []ConvertedPortSetting
}

// ConvertedPortSetting is a migration-tagged PortSetting and its resolved resources.
type ConvertedPortSetting struct {
	PortGroupID, SwitchUUID              string
	PortSettingPath                      string
	SubnetID, SubnetName, DVCID, DVCName string
}

// TargetDecision tells the handler which TGW/VPC and DVPG conversions to use.
type TargetDecision struct {
	TGWID, TGWName, VPCID, VPCName string
	CreateTGW, CreateVPC           bool
	// SharedWithVDN identifies the network whose target plan creates the pair.
	// Empty for the plan owner or when an existing pair is reused.
	SharedWithVDN string
	// VDSUUIDs lists the attachments required by all VDNs sharing this span.
	VDSUUIDs    []string
	Conversions []DVPGConversion
}

// DVPGConversion identifies an existing conversion or one that must be created.
type DVPGConversion struct {
	PortGroupID, SwitchUUID              string
	PortSettingPath                      string
	SubnetID, SubnetName, DVCID, DVCName string
	Create                               bool
}

func validateSourceTopology(topologies []Topology) ([]string, error) {
	zoneIDs := make(map[string]struct{})
	for _, topology := range topologies {
		if len(topology.ZoneIDs) == 0 {
			return nil, fmt.Errorf("DVPG %s has no resolved vCenter zone", topology.PortGroupID)
		}
		for _, zoneID := range topology.ZoneIDs {
			if zoneID == "" {
				return nil, fmt.Errorf("DVPG %s has an empty zone external ID", topology.PortGroupID)
			}
			zoneIDs[zoneID] = struct{}{}
		}
	}
	return sortedKeys(zoneIDs), nil
}

func mergeSpanNames(vdns []string, sourcesByVDN map[string]inspectedSource, namesByVDN map[string]api.VDNResourceNaming) (map[string]api.VDNResourceNaming, map[string]bool, []string) {
	type spanNames struct {
		vdns     []string
		tgwName  api.NSXResourceName
		vpcName  api.NSXResourceName
		conflict bool
	}
	bySpan := make(map[string]*spanNames)
	for _, vdnName := range vdns {
		inspected, found := sourcesByVDN[vdnName]
		if !found {
			continue
		}
		spanKey := strings.Join(inspected.zoneIDs, "\x00")
		sharedNames := bySpan[spanKey]
		if sharedNames == nil {
			sharedNames = &spanNames{}
			bySpan[spanKey] = sharedNames
		}
		sharedNames.vdns = append(sharedNames.vdns, vdnName)
		vdnNames := namesByVDN[vdnName]
		if vdnNames.TGWName != "" && sharedNames.tgwName != "" && vdnNames.TGWName != sharedNames.tgwName {
			sharedNames.conflict = true
		}
		if vdnNames.VPCName != "" && sharedNames.vpcName != "" && vdnNames.VPCName != sharedNames.vpcName {
			sharedNames.conflict = true
		}
		if sharedNames.tgwName == "" {
			sharedNames.tgwName = vdnNames.TGWName
		}
		if sharedNames.vpcName == "" {
			sharedNames.vpcName = vdnNames.VPCName
		}
	}
	effectiveNames := make(map[string]api.VDNResourceNaming, len(sourcesByVDN))
	invalidSpans := make(map[string]bool)
	var problems []string
	for _, sharedNames := range bySpan {
		if sharedNames.conflict {
			problems = append(problems, fmt.Sprintf("VDNs %s share a zone span but specify conflicting TGW/VPC names", strings.Join(sharedNames.vdns, ", ")))
		}
		for _, vdnName := range sharedNames.vdns {
			if sharedNames.conflict {
				invalidSpans[vdnName] = true
			}
			vdnNames := namesByVDN[vdnName]
			vdnNames.TGWName, vdnNames.VPCName = sharedNames.tgwName, sharedNames.vpcName
			effectiveNames[vdnName] = vdnNames
		}
	}
	return effectiveNames, invalidSpans, problems
}

func resolveTargetTopology(vdnName string, zoneIDs, requiredVDSUUIDs []string, sourceTopologies []Topology, names api.VDNResourceNaming, candidates []TargetTopology) (TargetDecision, []api.OptionalInput, error) {
	matchingTGWs := make([]TargetTopology, 0)
	for _, candidate := range candidates {
		if sameStrings(candidate.ZoneIDs, zoneIDs) {
			matchingTGWs = append(matchingTGWs, candidate)
		}
	}
	if len(matchingTGWs) > 1 {
		return TargetDecision{}, nil, fmt.Errorf("multiple TGW/VPC pairs match the VDS zone span")
	}
	decision := TargetDecision{TGWName: string(names.TGWName), VPCName: string(names.VPCName), VDSUUIDs: requiredVDSUUIDs}
	var hints []api.OptionalInput
	if len(matchingTGWs) == 0 {
		decision.CreateTGW, decision.CreateVPC = true, true
		if err := checkNameCollision("TGW", string(names.TGWName), candidates, ""); err != nil {
			return TargetDecision{}, nil, err
		}
		if err := checkNameCollision("VPC", string(names.VPCName), candidates, ""); err != nil {
			return TargetDecision{}, nil, err
		}
		if names.TGWName == "" {
			hints = appendNameHint(hints, vdnName, "tgwName", "TGW")
		}
		if names.VPCName == "" {
			hints = appendNameHint(hints, vdnName, "vpcName", "VPC")
		}
	} else {
		candidate := matchingTGWs[0]
		if candidate.TGWID == "" || candidate.VPCID == "" || !candidate.VPCReady || !candidate.Attached {
			return TargetDecision{}, nil, fmt.Errorf("matching TGW/VPC pair is incomplete or has no valid attachment")
		}
		if !containsAll(candidate.VDSUUIDs, requiredVDSUUIDs) {
			return TargetDecision{}, nil, fmt.Errorf("matching TGW/VPC pair has unverified or incompatible VDS attachments")
		}
		decision.TGWID, decision.VPCID = candidate.TGWID, candidate.VPCID
		decision.TGWName, decision.VPCName = candidate.TGWName, candidate.VPCName
		if names.TGWName != "" && string(names.TGWName) != candidate.TGWID {
			return TargetDecision{}, nil, fmt.Errorf("tgwName conflicts with reusable TGW ID %q", candidate.TGWID)
		}
		if names.VPCName != "" && string(names.VPCName) != candidate.VPCID {
			return TargetDecision{}, nil, fmt.Errorf("vpcName conflicts with reusable VPC ID %q", candidate.VPCID)
		}
	}

	needsConversion := false
	for _, source := range sourceTopologies {
		conversion, err := resolveDVPGConversion(source, names, decision.VPCID, candidates)
		if err != nil {
			return TargetDecision{}, nil, err
		}
		decision.Conversions = append(decision.Conversions, conversion)
		needsConversion = needsConversion || conversion.Create
	}
	if needsConversion {
		if err := checkNameCollision("DVC", string(names.DVCName), candidates, decision.VPCID); err != nil {
			return TargetDecision{}, nil, err
		}
		if decision.VPCID != "" {
			if err := checkNameCollision("subnet", string(names.SubnetName), candidates, decision.VPCID); err != nil {
				return TargetDecision{}, nil, err
			}
		}
		if names.DVCName == "" {
			hints = appendNameHint(hints, vdnName, "dvcName", "DVC")
		}
		if names.SubnetName == "" {
			hints = appendNameHint(hints, vdnName, "subnetName", "VLAN extension subnet")
		}
	}

	return decision, hints, nil
}

// resolveDVPGConversion finds the one tagged conversion for a DVPG, if any.
func resolveDVPGConversion(source Topology, names api.VDNResourceNaming, targetVPCID string, candidates []TargetTopology) (DVPGConversion, error) {
	conversion := DVPGConversion{
		PortGroupID: source.PortGroupID, SwitchUUID: source.SwitchUUID,
		SubnetName: string(names.SubnetName), DVCName: string(names.DVCName), Create: true,
	}
	for _, candidate := range candidates {
		for _, setting := range candidate.PortSettings {
			if setting.PortGroupID != source.PortGroupID || setting.SwitchUUID != source.SwitchUUID {
				continue
			}
			if targetVPCID == "" || candidate.VPCID != targetVPCID {
				return DVPGConversion{}, fmt.Errorf("migration PortSetting for DVPG %s belongs to a different or incompatible VPC", source.PortGroupID)
			}
			if !conversion.Create {
				return DVPGConversion{}, fmt.Errorf("multiple migration PortSettings match DVPG %s", source.PortGroupID)
			}
			if setting.PortSettingPath == "" || setting.SubnetID == "" || setting.DVCID == "" {
				return DVPGConversion{}, fmt.Errorf("migration PortSetting for DVPG %s has incomplete subnet or DVC information", source.PortGroupID)
			}
			if names.SubnetName != "" && string(names.SubnetName) != setting.SubnetID {
				return DVPGConversion{}, fmt.Errorf("subnetName conflicts with reusable subnet ID %q", setting.SubnetID)
			}
			if names.DVCName != "" && string(names.DVCName) != setting.DVCID {
				return DVPGConversion{}, fmt.Errorf("dvcName conflicts with reusable DVC ID %q", setting.DVCID)
			}
			conversion = DVPGConversion{
				PortGroupID: source.PortGroupID, SwitchUUID: source.SwitchUUID,
				PortSettingPath: setting.PortSettingPath, SubnetID: setting.SubnetID,
				SubnetName: setting.SubnetName, DVCID: setting.DVCID, DVCName: setting.DVCName,
			}
		}
	}
	return conversion, nil
}

func switchUUIDs(topologies []Topology) []string {
	switches := make(map[string]struct{}, len(topologies))
	for _, topology := range topologies {
		switches[topology.SwitchUUID] = struct{}{}
	}
	return sortedKeys(switches)
}

func containsAll(have, required []string) bool {
	available := make(map[string]struct{}, len(have))
	for _, value := range have {
		available[value] = struct{}{}
	}
	for _, value := range required {
		if _, found := available[value]; !found {
			return false
		}
	}
	return true
}

func checkNameCollision(resourceType, requestedName string, candidates []TargetTopology, reusableVPCID string) error {
	if requestedName == "" {
		return nil
	}
	for _, candidate := range candidates {
		if resourceType == "TGW" && candidate.TGWID == requestedName {
			return fmt.Errorf("requested tgwName %q is already used by another TGW", requestedName)
		}
		if resourceType == "VPC" && candidate.VPCID == requestedName {
			return fmt.Errorf("requested vpcName %q is already used by another VPC", requestedName)
		}
		if resourceType == "DVC" || resourceType == "subnet" {
			for _, subnet := range candidate.PortSettings {
				if resourceType == "subnet" && reusableVPCID != "" && candidate.VPCID != reusableVPCID {
					continue
				}
				if resourceType == "DVC" && subnet.DVCID == requestedName {
					return fmt.Errorf("requested dvcName %q is already used by another DVC", requestedName)
				}
				if resourceType == "subnet" && subnet.SubnetID == requestedName {
					return fmt.Errorf("requested subnetName %q is already used by another subnet", requestedName)
				}
			}
		}
	}
	return nil
}

func appendNameHint(hints []api.OptionalInput, vdnName, field, resource string) []api.OptionalInput {
	hints = append(hints, api.OptionalInput{
		FieldName:   fmt.Sprintf("spec.networkConfiguration.resourceNaming.networks[vdnName=%s].%s", vdnName, field),
		Description: fmt.Sprintf("Optionally set the %s name for VDN %s; if omitted, the migrator generates a deterministic name.", resource, vdnName),
	})
	return hints
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sameStrings(left, right []string) bool {
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	if len(leftCopy) != len(rightCopy) {
		return false
	}
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}
