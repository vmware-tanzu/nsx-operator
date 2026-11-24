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

const migrationVPCPurposeTagScope = "migration.network.vmware.com/vpc-purpose"

func (s VPCService) initializeStore(ctx context.Context, query search.QueryClient, cluster string) error {
	return initializeResourceStore(ctx, query, cluster, VPC, common.ResourceTypeVpc, s.StoreQueryResult)
}

func (s VPCService) StoreQueryResult(path string, e *data.StructValue) error {
	x, err := decode[model.Vpc](e, model.VpcBindingType())
	if err != nil {
		return err
	}
	return s.Store.Put(path, x)
}
func (s VPCService) Get(ctx context.Context, r Resource) (Resource, bool, error) {
	if err := ctx.Err(); err != nil {
		return Resource{}, false, err
	}
	o, p, e := scope(r, VPC)
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
	return vpcResource(x)
}
func vpcResource(x model.Vpc) (Resource, bool, error) {
	if x.MarkedForDelete != nil && *x.MarkedForDelete {
		return Resource{}, false, &ConflictError{Kind: VPC, Path: value(x.Path)}
	}
	out := resourceFrom(VPC, value(x.Id), value(x.Path), value(x.ParentPath))
	out.Tags = x.Tags
	return out, true, nil
}
func (s VPCService) Create(ctx context.Context, r Resource, tags []model.Tag) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o, p, e := scope(r, VPC)
	if e != nil {
		return e
	}
	return s.Client.Patch(o, p, r.ID, model.Vpc{Id: &r.ID, DisplayName: &r.ID, Tags: tags})
}

func (s VPCService) reusableID(attachments AttachmentService, req FabricPreparationRequest, cluster, project, profileID, profilePath, requestedVPC string) (string, error) {
	vpcMatches := make(map[string]*model.Vpc)
	for _, attachment := range attachments.Store.List() {
		if (attachment.MarkedForDelete != nil && *attachment.MarkedForDelete) || value(attachment.VpcConnectivityProfile) != profilePath {
			continue
		}
		vpcPath := value(attachment.ParentPath)
		vpcInfo, err := parseVPCPath(vpcPath)
		if err != nil || vpcPath != project+"/vpcs/"+vpcInfo.VPCID {
			continue
		}
		vpc, ok := s.Store.Get(vpcPath)
		if !ok || (vpc.MarkedForDelete != nil && *vpc.MarkedForDelete) || !ownedBy(cluster, req.NamespaceUID, vpc.Tags) {
			continue
		}
		purpose := tagValue(vpc.Tags, migrationVPCPurposeTagScope)
		if purpose != string(vpcPurpose(req)) {
			continue
		}
		if vpc.Id != nil && *vpc.Id == vpcInfo.VPCID {
			vpcMatches[vpcInfo.VPCID] = vpc
		}
	}
	if len(vpcMatches) > 1 {
		return "", fmt.Errorf("multiple compatible VPCs attach to profile %q for the intended owner", profileID)
	}
	vpcID := requestedVPC
	if len(vpcMatches) == 1 {
		for id := range vpcMatches {
			if req.VPCID != "" && req.VPCID != id {
				return "", fmt.Errorf("requested VPC %q conflicts with compatible existing VPC %q", req.VPCID, id)
			}
			vpcID = id
		}
	}
	return vpcID, nil
}

func (s VPCService) expected(project, id string) Resource {
	return Resource{Kind: VPC, ID: id, Parent: project, Path: project + "/vpcs/" + id}
}
