/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package mockcache

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ cache.Cache = (*DelegatingCache)(nil)

// DelegatingCache implements cache.Cache by delegating reads to a client (same pattern as prior gateway test stubs).
type DelegatingCache struct{ Client client.Client }

// NewDelegating returns a Cache backed by c for Get/List; informer APIs return no-op stubs suitable for controller registration tests.
func NewDelegating(c client.Client) *DelegatingCache {
	return &DelegatingCache{Client: c}
}

type stubInformer struct{}

func (stubInformer) AddEventHandler(toolscache.ResourceEventHandler) (toolscache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}
func (stubInformer) AddEventHandlerWithResyncPeriod(toolscache.ResourceEventHandler, time.Duration) (toolscache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}
func (stubInformer) RemoveEventHandler(toolscache.ResourceEventHandlerRegistration) error { return nil }
func (stubInformer) AddIndexers(toolscache.Indexers) error                                { return nil }
func (stubInformer) HasSynced() bool                                                      { return true }
func (stubInformer) IsStopped() bool                                                      { return false }

func (s *DelegatingCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return s.Client.Get(ctx, key, obj, opts...)
}

func (s *DelegatingCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return s.Client.List(ctx, list, opts...)
}

func (s *DelegatingCache) GetInformer(context.Context, client.Object, ...cache.InformerGetOption) (cache.Informer, error) {
	return stubInformer{}, nil
}

func (s *DelegatingCache) GetInformerForKind(context.Context, schema.GroupVersionKind, ...cache.InformerGetOption) (cache.Informer, error) {
	return stubInformer{}, nil
}

func (s *DelegatingCache) RemoveInformer(context.Context, client.Object) error { return nil }

func (s *DelegatingCache) Start(context.Context) error { return nil }

func (s *DelegatingCache) WaitForCacheSync(context.Context) bool { return true }

func (s *DelegatingCache) IndexField(context.Context, client.Object, string, client.IndexerFunc) error {
	return nil
}
