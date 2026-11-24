/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"context"
	"errors"
	"fmt"
	"strings"

	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects"
	projectinfra "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// FabricServiceClients contains the cluster identity and NSX API clients used
// by the fabric services. Each resource service receives only its own client.
type FabricServiceClients struct {
	Cluster                string
	QueryClient            search.QueryClient
	TransitGatewaysClient  projects.TransitGatewaysClient
	IPBlocksClient         projectinfra.IpBlocksClient
	ProfilesClient         projects.VpcConnectivityProfilesClient
	VPCsClient             projects.VpcsClient
	AttachmentsClient      vpcs.AttachmentsClient
	RealizedEntitiesClient RealizationClient
}
type RealizationClient interface {
	List(string, *string) (model.GenericPolicyRealizedResourceListResult, error)
}
type NSXRealizer struct{ Client RealizationClient }

func (r NSXRealizer) Ready(ctx context.Context, path string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	result, err := r.Client.List(path, nil)
	if err != nil {
		return false, err
	}
	// TODO: Verify on a target NSX environment whether realized-entities List
	// can return a non-empty cursor and, if so, which API supports continuation.
	// The SDK List method has no cursor argument; do not accept a partial scan.
	if result.Cursor != nil && *result.Cursor != "" {
		return false, fmt.Errorf("incomplete realization results for %s: continuation is not supported by the NSX SDK", path)
	}
	if len(result.Results) == 0 {
		return false, nil
	}
	for _, item := range result.Results {
		if item.State == nil {
			return false, nil
		}
		switch *item.State {
		case model.GenericPolicyRealizedResource_STATE_REALIZED:
		case model.GenericPolicyRealizedResource_STATE_ERROR:
			return false, fmt.Errorf("NSX reports realization error for %s", path)
		default:
			return false, nil
		}
	}
	return true, nil
}

type TransitGatewayService struct {
	Realizer Realizer
	Client   projects.TransitGatewaysClient
	Store    *ResourceStore[model.TransitGateway]
}
type IPBlockService struct {
	Realizer Realizer
	Client   projectinfra.IpBlocksClient
	Store    *ResourceStore[model.IpAddressBlock]
}
type ConnectivityProfileService struct {
	Realizer Realizer
	Client   projects.VpcConnectivityProfilesClient
	Store    *ResourceStore[model.VpcConnectivityProfile]
}
type VPCService struct {
	Realizer Realizer
	Client   projects.VpcsClient
	Store    *ResourceStore[model.Vpc]
}
type AttachmentService struct {
	Realizer Realizer
	Client   vpcs.AttachmentsClient
	Store    *ResourceStore[model.VpcAttachment]
}

func absent(err error) bool {
	var value vapierrors.NotFound
	var pointer *vapierrors.NotFound
	return errors.As(err, &value) || errors.As(err, &pointer)
}
func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func scope(r Resource, kind Kind) (string, string, error) {
	parts := strings.Split(r.Path, "/")
	var collection string
	switch kind {
	case TGW:
		collection = "transit-gateways"
	case IPBlock:
		collection = "infra/ip-blocks"
	case Profile:
		collection = "vpc-connectivity-profiles"
	case VPC:
		collection = "vpcs"
	default:
		return "", "", fmt.Errorf("unsupported resource kind %q", kind)
	}
	if len(parts) < 5 || parts[0] != "" || parts[1] != "orgs" || parts[3] != "projects" ||
		!validResourceID(parts[2]) || !validResourceID(parts[4]) || !validResourceID(r.ID) ||
		(r.Kind != "" && r.Kind != kind) || r.Path != strings.Join(parts[:5], "/")+"/"+collection+"/"+r.ID {
		return "", "", fmt.Errorf("invalid resource path %q", r.Path)
	}
	return parts[2], parts[4], nil
}
func resourceFrom(kind Kind, id, path, parent string) Resource {
	return Resource{Kind: kind, ID: id, Path: path, Parent: parent}
}

// parseVPCPath accepts only a canonical VPC resource path. The shared parser
// also accepts descendant resources, so compare its reconstructed VPC path.
func parseVPCPath(path string) (common.VPCResourceInfo, error) {
	info, err := common.ParseVPCResourcePath(path)
	if err != nil || info.OrgID == "" || info.ProjectID == "" || info.VPCID == "" || info.GetVPCPath() != path {
		return common.VPCResourceInfo{}, fmt.Errorf("invalid VPC path %q", path)
	}
	for _, id := range []string{info.OrgID, info.ProjectID, info.VPCID} {
		if !validResourceID(id) {
			return common.VPCResourceInfo{}, fmt.Errorf("invalid VPC path %q", path)
		}
	}
	return info, nil
}

// getRealized distinguishes Policy absence from pending realization. A Policy
// read alone does not establish readiness for a dependent resource.
func getRealized(ctx context.Context, resource Resource, get func(context.Context, Resource) (Resource, bool, error), realizer Realizer) (Resource, bool, bool, error) {
	actual, found, err := get(ctx, resource)
	if err != nil || !found {
		return actual, found, false, err
	}
	ready, err := checkRealization(ctx, actual, realizer)
	return actual, true, ready, err
}

// checkRealization verifies the realization state of an already-read resource.
func checkRealization(ctx context.Context, actual Resource, realizer Realizer) (bool, error) {
	if isNil(realizer) {
		return false, fmt.Errorf("fabric realization service is required")
	}
	ready, err := realizer.Ready(ctx, actual.Path)
	if err != nil {
		return false, fmt.Errorf("realize %s %s: %w", actual.Kind, actual.Path, err)
	}
	return ready, nil
}

// GetRealized reads live Policy and independently checks realization.
func (s TransitGatewayService) GetRealized(ctx context.Context, resource Resource) (Resource, bool, bool, error) {
	return getRealized(ctx, resource, s.Get, s.Realizer)
}

// GetRealized reads live Policy and independently checks realization.
func (s IPBlockService) GetRealized(ctx context.Context, resource Resource) (Resource, bool, bool, error) {
	return getRealized(ctx, resource, s.Get, s.Realizer)
}

// GetRealized reads live Policy and independently checks realization.
func (s ConnectivityProfileService) GetRealized(ctx context.Context, resource Resource) (Resource, bool, bool, error) {
	return getRealized(ctx, resource, s.Get, s.Realizer)
}

// GetRealized reads live Policy and independently checks realization.
func (s VPCService) GetRealized(ctx context.Context, resource Resource) (Resource, bool, bool, error) {
	return getRealized(ctx, resource, s.Get, s.Realizer)
}

// GetRealized reads live Policy and independently checks realization.
func (s AttachmentService) GetRealized(ctx context.Context, resource Resource) (Resource, bool, bool, error) {
	return getRealized(ctx, resource, s.Get, s.Realizer)
}

// CheckRealization checks the state of an already-read Policy resource.
func (s TransitGatewayService) CheckRealization(ctx context.Context, actual Resource) (bool, error) {
	return checkRealization(ctx, actual, s.Realizer)
}

// CheckRealization checks the state of an already-read Policy resource.
func (s IPBlockService) CheckRealization(ctx context.Context, actual Resource) (bool, error) {
	return checkRealization(ctx, actual, s.Realizer)
}

// CheckRealization checks the state of an already-read Policy resource.
func (s ConnectivityProfileService) CheckRealization(ctx context.Context, actual Resource) (bool, error) {
	return checkRealization(ctx, actual, s.Realizer)
}

// CheckRealization checks the state of an already-read Policy resource.
func (s VPCService) CheckRealization(ctx context.Context, actual Resource) (bool, error) {
	return checkRealization(ctx, actual, s.Realizer)
}

// CheckRealization checks the state of an already-read Policy resource.
func (s AttachmentService) CheckRealization(ctx context.Context, actual Resource) (bool, error) {
	return checkRealization(ctx, actual, s.Realizer)
}
