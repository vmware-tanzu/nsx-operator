/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/fabric"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// NSXTargetInventory reads topology and tagged converted PortSettings in one project.
// Topology must read DVPGs from the vCenter identified by VCenterUUID.
type NSXTargetInventory struct {
	Client      *nsx.Client
	OrgID       string
	ProjectID   string
	VCenterUUID string
	Topology    TopologyReader
	DVCs        DVCReader
}

// DVCReader is implemented by the NSX SDK DistributedVlanConnectionsClient.
type DVCReader interface {
	Get(string) (model.DistributedVlanConnection, error)
}

// nsxPages reads all pages or returns an error; partial inventories are unsafe.
func nsxPages[T any](ctx context.Context, list func(*string) ([]T, *string, error)) ([]T, error) {
	var result []T
	var cursor *string
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, next, err := list(cursor)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if next == nil || *next == "" {
			return result, nil
		}
		if seen[*next] {
			return nil, fmt.Errorf("NSX inventory pagination repeated cursor")
		}
		seen[*next] = true
		cursor = next
	}
}

func searchPolicyResources[T any](ctx context.Context, client search.QueryClient, query string, bindingType bindings.BindingType, visit func(T)) error {
	var cursor *string
	seen := make(map[string]bool)
	converter := common.NewConverter()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := client.List(query, cursor, nil, nil, nil, nil)
		if err != nil {
			return err
		}
		for _, item := range page.Results {
			converted, errors := converter.ConvertToGolang(item, bindingType)
			if len(errors) > 0 {
				return fmt.Errorf("decode NSX search result: %v", errors)
			}
			resource, ok := converted.(T)
			if !ok {
				if pointer, pointerOK := converted.(*T); pointerOK && pointer != nil {
					resource, ok = *pointer, true
				}
			}
			if !ok {
				return fmt.Errorf("unexpected NSX search result type %T", converted)
			}
			visit(resource)
		}
		if page.Cursor == nil || *page.Cursor == "" {
			return nil
		}
		if seen[*page.Cursor] {
			return fmt.Errorf("NSX inventory pagination repeated cursor")
		}
		seen[*page.Cursor] = true
		cursor = page.Cursor
	}
}

func projectSearchQuery(resourceType, org, project string) string {
	path := `\/orgs\/` + strings.ReplaceAll(org, "/", `\/`) + `\/projects\/` + strings.ReplaceAll(project, "/", `\/`) + `\/*`
	pathPrefix, _ := url.PathUnescape("path%3A")
	return fmt.Sprintf("resource_type:%s AND %s%s AND marked_for_delete:false", resourceType, pathPrefix, path)
}

func (inventory NSXTargetInventory) ListTopology(ctx context.Context) ([]TargetTopology, error) {
	c := inventory.Client
	if c == nil || c.RestConnector == nil || c.TransitGatewayClient == nil || c.VPCClient == nil || (c.VpcAttachmentClient == nil && c.QueryClient == nil) || c.VPCConnectivityProfilesClient == nil || (c.SubnetsClient == nil && c.QueryClient == nil) || (c.PortSettingsClient == nil && c.QueryClient == nil) || c.RealizedEntitiesClient == nil || inventory.Topology == nil || inventory.OrgID == "" || inventory.ProjectID == "" || inventory.VCenterUUID == "" {
		return nil, fmt.Errorf("NSX clients, project, vCenter and topology reader are required")
	}
	org, project := inventory.OrgID, inventory.ProjectID
	tgws, err := nsxPages(ctx, func(cursor *string) ([]model.TransitGateway, *string, error) {
		p, e := c.TransitGatewayClient.List(org, project, cursor, nil, nil, nil, nil, nil, nil)
		return p.Results, p.Cursor, e
	})
	if err != nil {
		return nil, fmt.Errorf("list transit gateways: %w", err)
	}
	vpcs, err := nsxPages(ctx, func(cursor *string) ([]model.Vpc, *string, error) {
		p, e := c.VPCClient.List(org, project, cursor, nil, nil, nil, nil, nil, nil)
		return p.Results, p.Cursor, e
	})
	if err != nil {
		return nil, fmt.Errorf("list VPCs: %w", err)
	}
	profiles, err := nsxPages(ctx, func(cursor *string) ([]model.VpcConnectivityProfile, *string, error) {
		p, e := c.VPCConnectivityProfilesClient.List(org, project, cursor, nil, nil, nil, nil, nil, nil)
		return p.Results, p.Cursor, e
	})
	if err != nil {
		return nil, fmt.Errorf("list VPC connectivity profiles: %w", err)
	}
	var attachmentsByVPC map[string][]model.VpcAttachment
	var subnetsByVPC map[string][]model.VpcSubnet
	var settingsBySubnet map[string][]model.VpcSubnetPortSetting
	if c.QueryClient != nil {
		attachmentsByVPC = make(map[string][]model.VpcAttachment)
		query := projectSearchQuery("VpcAttachment", org, project)
		if searchErr := searchPolicyResources(ctx, c.QueryClient, query, model.VpcAttachmentBindingType(), func(attachment model.VpcAttachment) {
			if attachment.ParentPath != nil && *attachment.ParentPath != "" {
				attachmentsByVPC[*attachment.ParentPath] = append(attachmentsByVPC[*attachment.ParentPath], attachment)
			}
		}); searchErr != nil {
			return nil, fmt.Errorf("search VPC attachments: %w", searchErr)
		}
		subnetsByVPC = make(map[string][]model.VpcSubnet)
		query = projectSearchQuery("VpcSubnet", org, project)
		if searchErr := searchPolicyResources(ctx, c.QueryClient, query, model.VpcSubnetBindingType(), func(subnet model.VpcSubnet) {
			if subnet.ParentPath != nil && *subnet.ParentPath != "" {
				subnetsByVPC[*subnet.ParentPath] = append(subnetsByVPC[*subnet.ParentPath], subnet)
			}
		}); searchErr != nil {
			return nil, fmt.Errorf("search VPC subnets: %w", searchErr)
		}
		settingsBySubnet = make(map[string][]model.VpcSubnetPortSetting)
		query = projectSearchQuery("VpcSubnetPortSetting", org, project)
		if searchErr := searchPolicyResources(ctx, c.QueryClient, query, model.VpcSubnetPortSettingBindingType(), func(setting model.VpcSubnetPortSetting) {
			if setting.ParentPath != nil && *setting.ParentPath != "" {
				settingsBySubnet[*setting.ParentPath] = append(settingsBySubnet[*setting.ParentPath], setting)
			}
		}); searchErr != nil {
			return nil, fmt.Errorf("search VPC PortSettings: %w", searchErr)
		}
	}

	profilesByPath := make(map[string]model.VpcConnectivityProfile)
	for _, p := range profiles {
		if p.Path != nil && *p.Path != "" {
			profilesByPath[*p.Path] = p
		}
	}
	vpcsByTGW := make(map[string][]TargetTopology)
	dvcsByID := make(map[string]model.DistributedVlanConnection)
	topologiesByDVPG := make(map[string]Topology)
	for _, vpc := range vpcs {
		if vpc.Id == nil || *vpc.Id == "" || vpc.Path == nil || *vpc.Path == "" {
			return nil, fmt.Errorf("NSX VPC has incomplete identity")
		}
		vpcID := *vpc.Id
		var attachments []model.VpcAttachment
		if attachmentsByVPC != nil {
			attachments = attachmentsByVPC[*vpc.Path]
		} else {
			var listErr error
			attachments, listErr = nsxPages(ctx, func(cursor *string) ([]model.VpcAttachment, *string, error) {
				p, e := c.VpcAttachmentClient.List(org, project, vpcID, cursor, nil, nil, nil, nil, nil, nil)
				return p.Results, p.Cursor, e
			})
			if listErr != nil {
				return nil, fmt.Errorf("list VPC attachments: %w", listErr)
			}
		}
		settings, e := inventory.portSettings(ctx, vpcID, *vpc.Path, subnetsByVPC, settingsBySubnet, dvcsByID, topologiesByDVPG)
		if e != nil {
			return nil, e
		}
		if len(settings) > 0 && len(attachments) == 0 {
			return nil, fmt.Errorf("VPC %s has tagged PortSettings but no attachment", vpcID)
		}
		associated := false
		vpcReady, vpcReadyResolved := false, false
		for _, attachment := range attachments {
			if attachment.VpcConnectivityProfile == nil || *attachment.VpcConnectivityProfile == "" {
				continue
			}
			profile, ok := profilesByPath[*attachment.VpcConnectivityProfile]
			if !ok || profile.TransitGatewayPath == nil || *profile.TransitGatewayPath == "" {
				return nil, fmt.Errorf("VPC %s has an unresolved connectivity profile", vpcID)
			}
			if attachment.Path == nil || *attachment.Path == "" {
				return nil, fmt.Errorf("VPC %s has an attachment without a path", vpcID)
			}
			associated = true
			if !vpcReadyResolved {
				vpcReady, e = inventory.realized(*vpc.Path)
				if e != nil {
					return nil, e
				}
				vpcReadyResolved = true
			}
			attached, e := inventory.realized(*attachment.Path)
			if e != nil {
				return nil, e
			}
			vpcsByTGW[*profile.TransitGatewayPath] = append(vpcsByTGW[*profile.TransitGatewayPath], TargetTopology{VPCID: vpcID, VPCName: optionalString(vpc.DisplayName), VPCPath: *vpc.Path, VPCReady: vpcReady, Attached: attached, PortSettings: settings})
		}
		if len(settings) > 0 && !associated {
			return nil, fmt.Errorf("VPC %s has tagged PortSettings but no valid connectivity profile", vpcID)
		}
	}
	var result []TargetTopology
	resolvedTGWs := make(map[string]bool)
	for _, tgw := range tgws {
		if tgw.Id == nil || *tgw.Id == "" || tgw.Path == nil || *tgw.Path == "" {
			return nil, fmt.Errorf("NSX transit gateway has incomplete identity")
		}
		if tgw.Span == nil {
			return nil, fmt.Errorf("transit gateway %s has an unresolved span", *tgw.Id)
		}
		spanType, err := tgw.Span.StringField("type")
		if err != nil {
			return nil, fmt.Errorf("transit gateway %s has an invalid span: %w", *tgw.Id, err)
		}
		if spanType == model.ClusterBasedSpan__TYPE_IDENTIFIER {
			for _, candidate := range vpcsByTGW[*tgw.Path] {
				if len(candidate.PortSettings) > 0 {
					return nil, fmt.Errorf("tagged PortSettings belong to a cluster-based transit gateway")
				}
			}
			// Retain its identity for collision checks, but do not match it
			// against a zone-based migration span.
			candidates := vpcsByTGW[*tgw.Path]
			if len(candidates) == 0 {
				candidates = []TargetTopology{{}}
			}
			for _, candidate := range candidates {
				candidate.TGWID, candidate.TGWName = *tgw.Id, optionalString(tgw.DisplayName)
				result = append(result, candidate)
			}
			resolvedTGWs[*tgw.Path] = true
			continue
		}
		if spanType != model.ZoneBasedSpan__TYPE_IDENTIFIER {
			return nil, fmt.Errorf("transit gateway %s has unsupported span type %s", *tgw.Id, spanType)
		}
		converted, errors := common.NewConverter().ConvertToGolang(tgw.Span, model.ZoneBasedSpanBindingType())
		if len(errors) > 0 {
			return nil, fmt.Errorf("decode transit gateway %s zone span: %v", *tgw.Id, errors)
		}
		span, ok := converted.(model.ZoneBasedSpan)
		if !ok || span.Type_ != model.ZoneBasedSpan__TYPE_IDENTIFIER {
			return nil, fmt.Errorf("transit gateway %s has an invalid zone span", *tgw.Id)
		}
		if len(span.ZoneExternalIds) == 0 {
			return nil, fmt.Errorf("transit gateway %s has an unresolved zone span", *tgw.Id)
		}
		vdsUUIDs, err := inventory.customVDS(ctx, span.CustomVdsAttachments)
		if err != nil {
			return nil, err
		}
		candidates := vpcsByTGW[*tgw.Path]
		resolvedTGWs[*tgw.Path] = true
		if len(candidates) == 0 {
			candidates = []TargetTopology{{}}
		}
		for _, candidate := range candidates {
			candidate.TGWID = *tgw.Id
			candidate.TGWName = optionalString(tgw.DisplayName)
			candidate.ZoneIDs = span.ZoneExternalIds
			candidate.VDSUUIDs = vdsUUIDs
			result = append(result, candidate)
		}
	}
	for tgwPath, candidates := range vpcsByTGW {
		if resolvedTGWs[tgwPath] {
			continue
		}
		for _, candidate := range candidates {
			if len(candidate.PortSettings) > 0 {
				return nil, fmt.Errorf("tagged PortSettings belong to an unresolved transit gateway")
			}
		}
	}
	return result, nil
}

// customVDS resolves explicitly configured attachments. NSX does not expose
// the effective default VDS in this span; an empty result remains unverified.
func (inventory NSXTargetInventory) customVDS(ctx context.Context, attachments []string) ([]string, error) {
	client := fabric.NewVirtualSwitchesClient(inventory.Client.RestConnector)
	var uuids []string
	for _, attachment := range attachments {
		if attachment == "" {
			return nil, fmt.Errorf("empty custom VDS attachment")
		}
		switches, err := nsxPages(ctx, func(cursor *string) ([]mpmodel.DistributedVirtualSwitch, *string, error) {
			page, err := client.List(nil, cursor, nil, nil, &attachment, nil, nil, nil, nil, nil, nil)
			return page.Results, page.Cursor, err
		})
		if err != nil {
			return nil, fmt.Errorf("list NSX virtual switches: %w", err)
		}
		if len(switches) != 1 || switches[0].ExternalId == nil || *switches[0].ExternalId != attachment || switches[0].Uuid == nil || *switches[0].Uuid == "" {
			return nil, fmt.Errorf("custom VDS attachment %s cannot be resolved", attachment)
		}
		uuids = append(uuids, *switches[0].Uuid)
	}
	return uuids, nil
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (inventory NSXTargetInventory) realized(path string) (bool, error) {
	result, err := inventory.Client.RealizedEntitiesClient.List(path, nil)
	if err != nil {
		return false, fmt.Errorf("read NSX realization state: %w", err)
	}
	if len(result.Results) == 0 {
		return false, nil
	}
	for _, entity := range result.Results {
		if entity.State == nil || *entity.State != model.GenericPolicyRealizedResource_STATE_REALIZED {
			return false, nil
		}
	}
	return true, nil
}

// VerifyPortSetting re-reads the scoped NSX inventory and confirms exactly one
// live migration-tagged PortSetting joins the requested DVPG to the expected
// VPC, DVC and VLAN extension subnet.
func (inventory NSXTargetInventory) VerifyPortSetting(ctx context.Context, vcenterUUID, portGroupID, vpcPath, dvcPath, subnetPath string) (bool, error) {
	if vcenterUUID == "" || portGroupID == "" || vpcPath == "" || dvcPath == "" || subnetPath == "" {
		return false, fmt.Errorf("vCenter, DVPG, VPC, DVC and subnet identities are required")
	}
	if inventory.VCenterUUID != vcenterUUID {
		return false, fmt.Errorf("PortSetting inventory is scoped to a different vCenter")
	}
	dvcID, ok := resourcePathID(dvcPath, "/infra/distributed-vlan-connections/")
	if !ok {
		return false, fmt.Errorf("invalid expected DVC path %q", dvcPath)
	}
	// VPC subnet paths are project scoped and must be children of the VPC
	// selected for this conversion.
	subnetPrefix := strings.TrimSuffix(vpcPath, "/") + "/subnets/"
	subnetID, ok := pathChildID(subnetPath, subnetPrefix)
	if !ok {
		return false, fmt.Errorf("invalid expected subnet path %q", subnetPath)
	}
	topologies, err := inventory.ListTopology(ctx)
	if err != nil {
		return false, err
	}
	matches := 0
	for _, topology := range topologies {
		if topology.VPCPath != vpcPath {
			continue
		}
		for _, setting := range topology.PortSettings {
			if setting.PortGroupID == portGroupID && setting.DVCID == dvcID && setting.SubnetID == subnetID {
				matches++
			}
		}
	}
	if matches > 1 {
		return false, fmt.Errorf("multiple live PortSettings match DVPG %s in VPC %s", portGroupID, vpcPath)
	}
	return matches == 1, nil
}

func resourcePathID(path, prefix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	return pathChildID(path, prefix)
}

func pathChildID(path, prefix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	id := strings.TrimPrefix(path, prefix)
	if id == "" || strings.Contains(id, "/") || id == "." || id == ".." {
		return "", false
	}
	return id, true
}

func (inventory NSXTargetInventory) portSettings(ctx context.Context, vpcID, vpcPath string, subnetsByVPC map[string][]model.VpcSubnet, settingsBySubnet map[string][]model.VpcSubnetPortSetting, dvcsByID map[string]model.DistributedVlanConnection, topologiesByDVPG map[string]Topology) ([]ConvertedPortSetting, error) {
	c := inventory.Client
	org, project := inventory.OrgID, inventory.ProjectID
	var subnets []model.VpcSubnet
	if subnetsByVPC != nil {
		subnets = subnetsByVPC[vpcPath]
	} else {
		var err error
		subnets, err = nsxPages(ctx, func(cursor *string) ([]model.VpcSubnet, *string, error) {
			p, e := c.SubnetsClient.List(org, project, vpcID, cursor, nil, nil, nil, nil, nil, nil)
			return p.Results, p.Cursor, e
		})
		if err != nil {
			return nil, fmt.Errorf("list VPC subnets: %w", err)
		}
	}
	dvcClient := inventory.DVCs
	if dvcClient == nil {
		dvcClient = infra.NewDistributedVlanConnectionsClient(c.RestConnector)
	}
	var result []ConvertedPortSetting
	for _, subnet := range subnets {
		if subnet.Id == nil || *subnet.Id == "" {
			return nil, fmt.Errorf("NSX subnet has no identity")
		}
		subnetID := *subnet.Id
		var settings []model.VpcSubnetPortSetting
		if settingsBySubnet != nil {
			if subnet.Path == nil || *subnet.Path == "" {
				return nil, fmt.Errorf("NSX subnet has no path")
			}
			settings = settingsBySubnet[*subnet.Path]
		}
		if settingsBySubnet == nil {
			var listErr error
			settings, listErr = nsxPages(ctx, func(cursor *string) ([]model.VpcSubnetPortSetting, *string, error) {
				p, e := c.PortSettingsClient.List(org, project, vpcID, subnetID, cursor, nil, nil, nil, nil, nil, nil)
				return p.Results, p.Cursor, e
			})
			if listErr != nil {
				return nil, fmt.Errorf("list subnet PortSettings: %w", listErr)
			}
		}
		for _, setting := range settings {
			for _, tag := range setting.Tags {
				if tag.Scope == nil || tag.Tag == nil || *tag.Scope != DVPGTagScope {
					continue
				}
				vc, dvpg, ok := strings.Cut(*tag.Tag, ":")
				if !ok {
					return nil, fmt.Errorf("invalid migration DVPG tag")
				}
				if vc != inventory.VCenterUUID {
					continue
				}
				if _, e := DVPGTag(vc, dvpg); e != nil {
					return nil, fmt.Errorf("invalid migration DVPG tag: %w", e)
				}
				if setting.Path == nil || *setting.Path == "" {
					return nil, fmt.Errorf("tagged PortSetting has no path")
				}
				if subnet.VlanConnection == nil || *subnet.VlanConnection == "" {
					return nil, fmt.Errorf("tagged PortSetting subnet %s has no DVC", subnetID)
				}
				const dvcPrefix = "/infra/distributed-vlan-connections/"
				if !strings.HasPrefix(*subnet.VlanConnection, dvcPrefix) {
					return nil, fmt.Errorf("subnet %s has invalid DVC path", subnetID)
				}
				dvcID := strings.TrimPrefix(*subnet.VlanConnection, dvcPrefix)
				if dvcID == "" || strings.Contains(dvcID, "/") {
					return nil, fmt.Errorf("subnet %s has invalid DVC path", subnetID)
				}
				dvc, found := dvcsByID[dvcID]
				if !found {
					var getErr error
					dvc, getErr = dvcClient.Get(dvcID)
					if getErr != nil {
						return nil, fmt.Errorf("read subnet DVC: %w", getErr)
					}
					if dvc.Id == nil || *dvc.Id != dvcID {
						return nil, fmt.Errorf("subnet %s references an inconsistent DVC", subnetID)
					}
					dvcsByID[dvcID] = dvc
				}
				topology, found := topologiesByDVPG[dvpg]
				if !found {
					var resolveErr error
					topology, resolveErr = inventory.Topology.ResolvePortGroup(ctx, dvpg)
					if resolveErr != nil {
						return nil, fmt.Errorf("resolve tagged DVPG: %w", resolveErr)
					}
					topologiesByDVPG[dvpg] = topology
				}
				result = append(result, ConvertedPortSetting{PortGroupID: dvpg, SwitchUUID: topology.SwitchUUID, PortSettingPath: *setting.Path, SubnetID: subnetID, SubnetName: optionalString(subnet.DisplayName), DVCID: dvcID, DVCName: optionalString(dvc.DisplayName)})
			}
		}
	}
	return result, nil
}
