/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"context"
	"fmt"
	"math"
	"reflect"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// InitializeStore loads complete NSX model objects into the requested stores.
// Initialization remains locked for its entire duration; Prepare cannot see a partial inventory.
// With no kinds, all stores are initialized for callers that need a full inventory.
func (s *FabricService) InitializeStore(ctx context.Context, requested ...Kind) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initializeStoreLocked(ctx, requested...)
}

func (s *FabricService) initializeStoreLocked(ctx context.Context, requested ...Kind) error {
	if s.initialized {
		return nil
	}
	if s.initializedKinds == nil {
		s.initializedKinds = make(map[Kind]bool)
	}
	if len(requested) == 0 {
		requested = allFabricKinds[:]
	}
	for _, kind := range requested {
		if s.initializedKinds[kind] {
			continue
		}
		access, err := s.access(kind)
		if err != nil {
			return err
		}
		if err := access.initializeStore(ctx, s.query, s.cluster); err != nil {
			return fmt.Errorf("initialize %s store: %w", kind, err)
		}
		s.initializedKinds[kind] = true
	}
	s.initialized = len(s.initializedKinds) == len(allFabricKinds)
	return nil
}

var allFabricKinds = [...]Kind{TGW, IPBlock, Profile, VPC, Attachment}

func initializeResourceStore(ctx context.Context, query search.QueryClient, cluster string, kind Kind, resourceType string, store func(string, *data.StructValue) error) error {
	client := &fabricSearchClient{QueryClient: query, ctx: ctx, seen: make(map[string]bool)}
	service := common.Service{NSXClient: &nsx.Client{QueryClient: client}}
	queryParam := common.QueryTagCondition(resourceType, cluster) + " AND marked_for_delete:false"
	_, err := service.SearchResource(resourceType, queryParam, fabricQueryStore{ctx: ctx, kind: kind, store: store}, nil)
	if err != nil {
		return fmt.Errorf("query %s: %w", kind, err)
	}
	return ctx.Err()
}

// fabricSearchClient preserves a failed page's cursor and validates pagination
// before passing results to the shared SearchResource implementation.
type fabricSearchClient struct {
	search.QueryClient
	ctx  context.Context
	seen map[string]bool
}

func (c *fabricSearchClient) List(query string, cursor, fields *string, pageSize *int64, ascending *bool, sortBy *string) (model.SearchResponse, error) {
	if err := c.ctx.Err(); err != nil {
		return model.SearchResponse{Cursor: cursor}, err
	}
	result, err := c.QueryClient.List(query, cursor, fields, pageSize, ascending, sortBy)
	if err != nil {
		// SearchResource reduces its process-wide page size for PageMaxError.
		// Bound retries when NSX rejects even its minimum page size.
		if _, ok := common.TransError(err).(nsxutil.PageMaxError); ok && pageSize != nil && *pageSize <= 10 {
			return model.SearchResponse{Cursor: cursor}, fmt.Errorf("NSX rejected the minimum search page size: %v", err)
		}
		return model.SearchResponse{Cursor: cursor}, err
	}
	if result.Cursor != nil && *result.Cursor == "" {
		result.Cursor = nil
	}
	if result.Cursor != nil {
		if c.seen[*result.Cursor] {
			return model.SearchResponse{Cursor: cursor}, fmt.Errorf("search returned repeated cursor")
		}
		c.seen[*result.Cursor] = true
	}
	// Search cursors are opaque. Prevent SearchResource from interpreting
	// cursor text as a result offset or dereferencing a missing result count.
	maxCount := int64(math.MaxInt64)
	result.ResultCount = &maxCount
	return result, nil
}

type fabricQueryStore struct {
	ctx   context.Context
	kind  Kind
	store func(string, *data.StructValue) error
}

func (s fabricQueryStore) TransResourceToStore(entity *data.StructValue) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if entity == nil {
		return fmt.Errorf("query %s returned empty resource", s.kind)
	}
	path, err := entity.StringField("path")
	if err != nil {
		return fmt.Errorf("query %s returned resource without path: %w", s.kind, err)
	}
	if path == "" {
		return fmt.Errorf("query %s returned resource with empty path", s.kind)
	}
	if err := s.store(path, entity); err != nil {
		return fmt.Errorf("decode %s at %s: %w", s.kind, path, err)
	}
	return nil
}
func (fabricQueryStore) ListIndexFuncValues(string) sets.Set[string] { return nil }
func (fabricQueryStore) Apply(interface{}) error {
	return fmt.Errorf("fabric search store does not support Apply")
}
func (fabricQueryStore) IsPolicyAPI() bool { return true }

func decode[T any](entity *data.StructValue, binding bindings.BindingType) (*T, error) {
	obj, errs := common.NewConverter().ConvertToGolang(entity, binding)
	if len(errs) > 0 {
		return nil, fmt.Errorf("convert NSX resource: %v", errs)
	}
	switch value := obj.(type) {
	case T:
		return &value, nil
	case *T:
		return value, nil
	default:
		return nil, fmt.Errorf("unexpected NSX resource type %T", obj)
	}
}
func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	}
	return false
}

// refreshStoreLocked takes a complete new discovery snapshot before replacing
// any existing entries. A failed search never authorizes reuse or creation.
func (s *FabricService) refreshStoreLocked(ctx context.Context, kinds ...Kind) error {
	if len(kinds) == 0 {
		kinds = allFabricKinds[:]
	}
	if s.query == nil {
		return s.initializeStoreLocked(ctx, kinds...)
	}
	fresh := NewStores()
	for _, kind := range kinds {
		var a ResourceAccess
		switch kind {
		case TGW:
			a = TransitGatewayService{Store: fresh.TGWs}
		case IPBlock:
			a = IPBlockService{Store: fresh.Blocks}
		case Profile:
			a = ConnectivityProfileService{Store: fresh.Profiles}
		case VPC:
			a = VPCService{Store: fresh.VPCs}
		case Attachment:
			a = AttachmentService{Store: fresh.Attachments}
		default:
			return fmt.Errorf("unsupported fabric resource kind %q", kind)
		}
		if err := a.initializeStore(ctx, s.query, s.cluster); err != nil {
			return fmt.Errorf("refresh %s store: %w", kind, err)
		}
	}
	for _, kind := range kinds {
		var err error
		switch kind {
		case TGW:
			err = replaceStore(s.TransitGateways.Store, fresh.TGWs)
		case IPBlock:
			err = replaceStore(s.IPBlocks.Store, fresh.Blocks)
		case Profile:
			err = replaceStore(s.Profiles.Store, fresh.Profiles)
		case VPC:
			err = replaceStore(s.VPCs.Store, fresh.VPCs)
		case Attachment:
			err = replaceStore(s.Attachments.Store, fresh.Attachments)
		}
		if err != nil {
			return err
		}
	}
	if s.initializedKinds == nil {
		s.initializedKinds = make(map[Kind]bool)
	}
	for _, kind := range kinds {
		s.initializedKinds[kind] = true
	}
	s.initialized = len(s.initializedKinds) == len(allFabricKinds)
	return nil
}
