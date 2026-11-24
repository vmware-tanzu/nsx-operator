/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package fabric

import (
	"context"
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func (s AttachmentService) initializeStore(ctx context.Context, query search.QueryClient, cluster string) error {
	return initializeResourceStore(ctx, query, cluster, Attachment, common.ResourceTypeVpcAttachment, s.StoreQueryResult)
}

func (s AttachmentService) StoreQueryResult(path string, e *data.StructValue) error {
	x, err := decode[model.VpcAttachment](e, model.VpcAttachmentBindingType())
	if err != nil {
		return err
	}
	return s.Store.Put(path, x)
}
func (s AttachmentService) Get(ctx context.Context, r Resource) (Resource, bool, error) {
	if err := ctx.Err(); err != nil {
		return Resource{}, false, err
	}
	o, p, vpcID, e := attachmentScope(r)
	if e != nil {
		return Resource{}, false, e
	}
	x, e := s.Client.Get(o, p, vpcID, r.ID)
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
	return attachmentResource(x)
}
func attachmentResource(x model.VpcAttachment) (Resource, bool, error) {
	if x.MarkedForDelete != nil && *x.MarkedForDelete {
		return Resource{}, false, &ConflictError{Kind: Attachment, Path: value(x.Path)}
	}
	out := resourceFrom(Attachment, value(x.Id), value(x.Path), value(x.ParentPath))
	out.Tags = x.Tags
	out.Profile = value(x.VpcConnectivityProfile)
	return out, true, nil
}
func (s AttachmentService) Create(ctx context.Context, r Resource, tags []model.Tag) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o, p, vpcID, e := attachmentScope(r)
	if e != nil {
		return e
	}
	return s.Client.Patch(o, p, vpcID, r.ID, model.VpcAttachment{Id: &r.ID, DisplayName: &r.ID, VpcConnectivityProfile: &r.Profile, Tags: tags})
}

func attachmentScope(r Resource) (string, string, string, error) {
	const expectedPath = "/orgs/<org>/projects/<project>/vpcs/<vpc>/attachments/<id>"
	parts := strings.Split(r.Path, "/")
	if len(parts) != 9 || parts[0] != "" || parts[1] != "orgs" || parts[3] != "projects" || parts[5] != "vpcs" || parts[7] != "attachments" {
		return "", "", "", fmt.Errorf("invalid attachment path %q: expected format %s", r.Path, expectedPath)
	}
	if parts[8] != r.ID {
		return "", "", "", fmt.Errorf("invalid attachment path %q: path ID %q does not match resource ID %q (expected format %s)", r.Path, parts[8], r.ID, expectedPath)
	}
	if r.ID == "" || !validResourceID(parts[8]) {
		return "", "", "", fmt.Errorf("invalid attachment ID %q in path %q: expected format %s", parts[8], r.Path, expectedPath)
	}
	vpcPath := strings.Join(parts[:7], "/")
	info, err := parseVPCPath(vpcPath)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid attachment VPC path %q in attachment path %q (expected format %s): %w", vpcPath, r.Path, expectedPath, err)
	}
	return info.OrgID, info.ProjectID, info.VPCID, nil
}

func (s AttachmentService) expected(vpc, profile Resource) Resource {
	return Resource{Kind: Attachment, ID: "default", Parent: vpc.Path, Path: vpc.Path + "/attachments/default", Profile: profile.Path}
}
