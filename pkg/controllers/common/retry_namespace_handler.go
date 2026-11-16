/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"context"
	"sync"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// retryNamespaceHandler retries namespace fan-out when an API list fails.
type retryNamespaceHandler struct {
	queue  workqueue.TypedRateLimitingInterface[string]
	mu     sync.RWMutex
	target workqueue.TypedRateLimitingInterface[reconcile.Request]
	list   func(context.Context, string) ([]reconcile.Request, error)
}

// NewRetryingNamespaceHandler creates a managed handler that retries failed namespace fan-out.
func NewRetryingNamespaceHandler(mgr manager.Manager, list func(context.Context, string) ([]reconcile.Request, error)) (handler.EventHandler, error) {
	h := &retryNamespaceHandler{queue: workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()), list: list}
	if err := mgr.Add(h); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *retryNamespaceHandler) enqueue(obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if obj == nil || obj.GetNamespace() == "" {
		return
	}
	h.mu.Lock()
	h.target = q
	h.mu.Unlock()
	h.queue.Add(obj.GetNamespace())
}
func (h *retryNamespaceHandler) Create(_ context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueue(e.Object, q)
}
func (h *retryNamespaceHandler) Update(_ context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueue(e.ObjectNew, q)
}
func (h *retryNamespaceHandler) Delete(_ context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueue(e.Object, q)
}
func (h *retryNamespaceHandler) Generic(_ context.Context, e event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueue(e.Object, q)
}
func (h *retryNamespaceHandler) Start(ctx context.Context) error {
	go func() { <-ctx.Done(); h.queue.ShutDown() }()
	for {
		ns, shutdown := h.queue.Get()
		if shutdown {
			return nil
		}
		reqs, err := h.list(ctx, ns)
		if err != nil && ctx.Err() == nil {
			log.Error(err, "Failed to list resources for namespace fan-out", "Namespace", ns)
			h.queue.AddRateLimited(ns)
		} else {
			if err == nil {
				h.mu.RLock()
				target := h.target
				h.mu.RUnlock()
				if target != nil {
					for _, req := range reqs {
						target.Add(req)
					}
				}
			}
			h.queue.Forget(ns)
		}
		h.queue.Done(ns)
	}
}
