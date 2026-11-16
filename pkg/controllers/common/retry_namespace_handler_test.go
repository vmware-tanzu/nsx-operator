/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestRetryNamespaceHandlerRetriesFailedLists(t *testing.T) {
	var attempts atomic.Int32
	target := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer target.ShutDown()
	h := &retryNamespaceHandler{
		queue: workqueue.NewTypedRateLimitingQueue(workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, time.Millisecond)),
		list: func(_ context.Context, namespace string) ([]reconcile.Request, error) {
			if attempts.Add(1) < 3 {
				return nil, errors.New("temporary list error")
			}
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: "owner"}}}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Start(ctx) }()
	ni := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "info", Namespace: "ns"}}
	h.Update(ctx, event.UpdateEvent{ObjectNew: ni}, target)
	require.Eventually(t, func() bool { return target.Len() == 1 }, 5*time.Second, 10*time.Millisecond)
	req, shutdown := target.Get()
	require.False(t, shutdown)
	require.Equal(t, types.NamespacedName{Namespace: "ns", Name: "owner"}, req.NamespacedName)
	target.Done(req)
	cancel()
	require.NoError(t, <-done)
	require.EqualValues(t, 3, attempts.Load())
}
