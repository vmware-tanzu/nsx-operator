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

func (s TransitGatewayService) initializeStore(ctx context.Context, query search.QueryClient, cluster string) error {
	return initializeResourceStore(ctx, query, cluster, TGW, "TransitGateway", s.StoreQueryResult)
}

func (s TransitGatewayService) StoreQueryResult(path string, entity *data.StructValue) error {
	x, err := decode[model.TransitGateway](entity, model.TransitGatewayBindingType())
	if err != nil {
		return err
	}
	return s.Store.Put(path, x)
}
func (s TransitGatewayService) Get(ctx context.Context, r Resource) (Resource, bool, error) {
	if err := ctx.Err(); err != nil {
		return Resource{}, false, err
	}
	o, p, e := scope(r, TGW)
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
	return transitGatewayResource(x)
}
func transitGatewayResource(x model.TransitGateway) (Resource, bool, error) {
	if x.MarkedForDelete != nil && *x.MarkedForDelete {
		return Resource{}, false, &ConflictError{Kind: TGW, Path: value(x.Path)}
	}
	out := resourceFrom(TGW, value(x.Id), value(x.Path), value(x.ParentPath))
	out.Tags = x.Tags
	if x.Span == nil {
		return out, true, &ConflictError{Kind: TGW, Path: out.Path}
	}
	decoded, errs := common.NewConverter().ConvertToGolang(x.Span, model.ZoneBasedSpanBindingType())
	if len(errs) > 0 {
		return out, true, &ConflictError{Kind: TGW, Path: out.Path}
	}
	span, ok := decoded.(model.ZoneBasedSpan)
	if !ok || span.Type_ != model.ZoneBasedSpan__TYPE_IDENTIFIER {
		return out, true, &ConflictError{Kind: TGW, Path: out.Path}
	}
	out.Zones, out.VDS = span.ZoneExternalIds, span.CustomVdsAttachments
	return out, true, nil
}
func (s TransitGatewayService) Create(ctx context.Context, r Resource, tags []model.Tag) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o, p, e := scope(r, TGW)
	if e != nil {
		return e
	}
	span := model.ZoneBasedSpan{Type_: model.ZoneBasedSpan__TYPE_IDENTIFIER, ZoneExternalIds: r.Zones, CustomVdsAttachments: r.VDS}
	dv, errs := common.NewConverter().ConvertToVapi(span, model.ZoneBasedSpanBindingType())
	if len(errs) > 0 {
		return fmt.Errorf("encode TGW span: %v", errs)
	}
	sv, ok := dv.(*data.StructValue)
	if !ok {
		return fmt.Errorf("invalid encoded TGW span")
	}
	return s.Client.Patch(o, p, r.ID, model.TransitGateway{Id: &r.ID, DisplayName: &r.ID, Span: sv, Tags: tags})
}

func (s TransitGatewayService) reusableID(req FabricPreparationRequest, cluster, requestedTGW string) (string, error) {
	tgwMatches := make([]*model.TransitGateway, 0)
	for _, candidate := range s.Store.List() {
		if value(candidate.ParentPath) != req.ProjectPath || value(candidate.Path) != req.ProjectPath+"/transit-gateways/"+value(candidate.Id) || value(candidate.Id) == "" || (candidate.MarkedForDelete != nil && *candidate.MarkedForDelete) || !ownedByCluster(candidate.Tags, cluster) || candidate.Span == nil {
			continue
		}
		decoded, errs := common.NewConverter().ConvertToGolang(candidate.Span, model.ZoneBasedSpanBindingType())
		if len(errs) > 0 {
			continue
		}
		span, ok := decoded.(model.ZoneBasedSpan)
		if ok && span.Type_ == model.ZoneBasedSpan__TYPE_IDENTIFIER && equalSet(span.ZoneExternalIds, req.Zones) && containsAll(span.CustomVdsAttachments, req.VDS) {
			tgwMatches = append(tgwMatches, candidate)
		}
	}
	for _, match := range tgwMatches {
		if value(match.Id) == requestedTGW {
			return requestedTGW, nil
		}
	}
	if len(tgwMatches) > 1 {
		return "", fmt.Errorf("multiple compatible transit gateways match zone span %v", sorted(req.Zones))
	}
	tgwID := requestedTGW
	if len(tgwMatches) == 1 {
		found := value(tgwMatches[0].Id)
		if req.TGWID != "" && req.TGWID != found {
			return "", fmt.Errorf("requested TGW %q conflicts with compatible existing TGW %q", req.TGWID, found)
		}
		tgwID = found
	}
	return tgwID, nil
}

func (s TransitGatewayService) expected(req FabricPreparationRequest, project, id string) Resource {
	return Resource{Kind: TGW, ID: id, Parent: project, Path: project + "/transit-gateways/" + id, Zones: sorted(req.Zones), VDS: sorted(req.VDS)}
}
