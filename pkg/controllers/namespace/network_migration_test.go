/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestNetworkMigrationDisabledNamespace(t *testing.T) {
	previous := config.IsPerNamespaceProvidersSupported()
	config.SetPerNamespaceProvidersSupportedForTest(true)
	t.Cleanup(func() { config.SetPerNamespaceProvidersSupportedForTest(previous) })
	config.SetMixedModeStateForTest(true, true)
	t.Cleanup(func() { config.SetMixedModeStateForTest(false, false) })
	child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "child", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: "kube-system"}}}
	r := createNameSpaceReconciler([]client.Object{child})
	fault := &namespaceFailureClient{Client: r.Client, ownerFailures: 1, listFailures: 1}
	r.Client, r.APIReader = fault, fault
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(child)})
	require.NoError(t, err, "an inherited-only namespace retains its original provider filter")
	for _, dependency := range []string{sharedOwnerDependency, networkConfigDependency} {
		_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Namespace: dependency, Name: "owner"}})
		require.NoError(t, err)
	}
	// No owner reads, dependency fanout, status writes, or new shared resources.
	require.NoError(t, r.updateSharedOwnerResourceStatus(context.Background(), child.Name, true))
	require.Equal(t, 1, fault.ownerFailures)
	require.Equal(t, 1, fault.listFailures)
	members := &v1alpha1.SubnetList{}
	sets := &v1alpha1.SubnetSetList{}
	require.NoError(t, fault.Client.List(context.Background(), members))
	require.NoError(t, fault.Client.List(context.Background(), sets))
	require.Empty(t, members.Items)
	require.Empty(t, sets.Items)
}

func TestNetworkMigrationConfigUpdateRouting(t *testing.T) {
	owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "owner", Annotations: map[string]string{servicecommon.AnnotationVPCNetworkConfig: "network"}}}
	child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "child", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: owner.Name}}}
	local := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "local", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: owner.Name, servicecommon.AnnotationVPCNetworkConfig: "other"}}}
	for _, enabled := range []bool{false, true} {
		r := createNameSpaceReconciler([]client.Object{owner, child, local})
		r.NSXConfig.CoeConfig = &config.CoeConfig{EnableNetworkMigration: enabled}
		fault := &namespaceFailureClient{Client: r.Client, listFailures: 1}
		r.VPCService.(*vpc.VPCService).Client = fault
		queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
		nc := &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "network"}}
		requeueNamespacesByVPCNetworkConfiguration(context.Background(), r, nc, queue)
		if enabled {
			require.Equal(t, 1, fault.listFailures, "migration handler defers reads to the retryable dependency queue")
			require.Equal(t, 1, queue.Len())
			req, _ := queue.Get()
			require.Equal(t, networkConfigDependency, req.Namespace)
			queue.Done(req)
		} else {
			require.Zero(t, fault.listFailures, "legacy handler keeps direct namespace lookup")
			require.Zero(t, queue.Len())
			// When the lookup succeeds, only the explicit configuration user is queued.
			requeueNamespacesByVPCNetworkConfiguration(context.Background(), r, nc, queue)
			require.Equal(t, 1, queue.Len())
			req, _ := queue.Get()
			require.Equal(t, client.ObjectKey{Name: owner.Name}, req.NamespacedName)
			queue.Done(req)
		}
		queue.ShutDown()
	}
}
