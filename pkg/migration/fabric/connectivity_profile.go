/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package fabric

import (
	"context"
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (s ConnectivityProfileService) initializeStore(ctx context.Context, query search.QueryClient, cluster string) error {
	return initializeResourceStore(ctx, query, cluster, Profile, common.ResourceTypeVpcConnectivityProfile, s.StoreQueryResult)
}

func (s ConnectivityProfileService) StoreQueryResult(path string, e *data.StructValue) error {
	x, err := decode[model.VpcConnectivityProfile](e, model.VpcConnectivityProfileBindingType())
	if err != nil {
		return err
	}
	return s.Store.Put(path, x)
}
func (s ConnectivityProfileService) Get(ctx context.Context, r Resource) (Resource, bool, error) {
	if err := ctx.Err(); err != nil {
		return Resource{}, false, err
	}
	o, p, e := scope(r, Profile)
	if e != nil {
		return Resource{}, false, e
	}
	x, e := s.Client.Get(o, p, r.ID)
	if e != nil {
		if absent(e) {
			s.Store.Delete(r.Path)
			return Resource{}, false, nil
		}
		return Resource{}, false, e
	}
	if err := s.Store.Put(value(x.Path), &x); err != nil {
		return Resource{}, false, err
	}
	return profileResource(x)
}
func profileResource(x model.VpcConnectivityProfile) (Resource, bool, error) {
	if x.MarkedForDelete != nil && *x.MarkedForDelete {
		return Resource{}, false, &ConflictError{Kind: Profile, Path: value(x.Path)}
	}
	out := resourceFrom(Profile, value(x.Id), value(x.Path), value(x.ParentPath))
	out.Tags = x.Tags
	out.Gateway = value(x.TransitGatewayPath)
	out.Blocks = x.ExternalIpBlocks
	return out, true, nil
}
func (s ConnectivityProfileService) Create(ctx context.Context, r Resource, tags []model.Tag) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o, p, e := scope(r, Profile)
	if e != nil {
		return e
	}
	return s.Client.Patch(o, p, r.ID, model.VpcConnectivityProfile{Id: &r.ID, DisplayName: &r.ID, TransitGatewayPath: &r.Gateway, ExternalIpBlocks: r.Blocks, Tags: tags})
}

func (s ConnectivityProfileService) reusableID(cluster, project, tgwID, tgwPath string, blocks []string, requestedProfile, snatBlock string) (string, error) {
	profileMatches := make([]*model.VpcConnectivityProfile, 0)
	for _, candidate := range s.Store.List() {
		if candidate == nil || value(candidate.Id) == "" {
			continue
		}
		if value(candidate.ParentPath) != project || value(candidate.Path) != project+"/vpc-connectivity-profiles/"+value(candidate.Id) {
			continue
		}
		if candidate.MarkedForDelete != nil && *candidate.MarkedForDelete {
			continue
		}
		if !ownedByCluster(candidate.Tags, cluster) || value(candidate.TransitGatewayPath) != tgwPath {
			continue
		}
		if !profileBlocksCompatible(candidate.ExternalIpBlocks, blocks, project, snatBlock) {
			continue
		}
		profileMatches = append(profileMatches, candidate)
	}
	if len(profileMatches) > 1 {
		return "", fmt.Errorf("multiple compatible connectivity profiles match TGW %q", tgwID)
	}
	profileID := requestedProfile
	if len(profileMatches) == 1 {
		profileID = value(profileMatches[0].Id)
	}
	return profileID, nil
}

func (s ConnectivityProfileService) expected(project, id string, tgw Resource, blocks []string) Resource {
	return Resource{Kind: Profile, ID: id, Parent: project, Path: project + "/vpc-connectivity-profiles/" + id, Gateway: tgw.Path, Blocks: append([]string(nil), blocks...)}
}

// UpdateBlocks adds the next level of ordinary IP blocks to a profile already
// selected by the transition checkpoint. The caller supplies a complete set,
// including any accepted SNAT block that must remain attached.
func (s ConnectivityProfileService) UpdateBlocks(ctx context.Context, profile Resource, blocks []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o, p, err := scope(profile, Profile)
	if err != nil {
		return err
	}
	current, err := s.Client.Get(o, p, profile.ID)
	if err != nil {
		return fmt.Errorf("read connectivity profile before updating blocks: %w", err)
	}
	live, found, err := profileResource(current)
	if err != nil {
		return err
	}
	if !found || !compatible(profile, live) {
		return &ConflictError{Kind: Profile, Path: profile.Path}
	}
	current.ExternalIpBlocks = append([]string(nil), blocks...)
	return s.Client.Patch(o, p, profile.ID, current)
}

func profileBlocksCompatible(actual, requested []string, project, allowedSNATBlock string) bool {
	filtered := make([]string, 0, len(actual))
	for _, block := range actual {
		if allowedSNATBlock != "" && block == allowedSNATBlock {
			continue
		}
		filtered = append(filtered, block)
	}
	if len(requested) != 0 && len(filtered) == 0 {
		return false
	}
	return profileBlocksInScope(filtered, project)
}
