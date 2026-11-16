/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSharedOwnerStatusGuards(t *testing.T) {
	previous := config.IsPerNamespaceProvidersSupported()
	config.SetPerNamespaceProvidersSupportedForTest(true)
	t.Cleanup(func() { config.SetPerNamespaceProvidersSupportedForTest(previous) })
	for _, tc := range []struct {
		name      string
		ownerLive bool
		explicit  bool
		readError bool
	}{
		{name: "owner confirmed missing"},
		{name: "stale cached NotFound after owner recovery", ownerLive: true},
		{name: "child selected explicit configuration while queued", explicit: true},
		{name: "API reader transient failure", readError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: "kube-system"}}}
			if tc.explicit {
				child.Annotations[config.VPCNetworkConfigAnnotation] = "local"
			}
			ready := []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: corev1.ConditionTrue}}
			member := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: child.Name, Annotations: map[string]string{servicecommon.AnnotationAssociatedResource: "project:vpc:subnet"}}, Status: v1alpha1.SubnetStatus{Conditions: ready}}
			members := []string{member.Name}
			set := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: child.Name}, Spec: v1alpha1.SubnetSetSpec{SubnetNames: &members}, Status: v1alpha1.SubnetSetStatus{Conditions: ready}}
			managed := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "managed", Namespace: child.Name}, Status: v1alpha1.SubnetSetStatus{Conditions: ready}}
			ordinary := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "ordinary", Namespace: child.Name}, Status: v1alpha1.SubnetStatus{Conditions: ready}}
			objects := []client.Object{child, member, set, managed, ordinary}
			if tc.ownerLive {
				objects = append(objects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", Annotations: map[string]string{config.VPCNetworkConfigAnnotation: "owner-config"}}})
			}
			r := createNameSpaceReconciler(objects)
			api := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(objects...).WithStatusSubresource(&v1alpha1.Subnet{}, &v1alpha1.SubnetSet{}).Build()
			r.Client, r.APIReader = api, api
			if tc.readError {
				r.APIReader = &namespaceFailureClient{Client: api, ownerFailures: 1}
			}
			err := r.updateSharedOwnerResourceStatus(ctx, child.Name, true)
			if tc.readError {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.NoError(t, err)
			}
			for _, obj := range []client.Object{member, set, managed, ordinary} {
				require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(obj), obj))
			}
			wantReady := tc.ownerLive || tc.explicit || tc.readError
			require.Equal(t, wantReady, controllercommon.IsObjectReady(member.Status.Conditions))
			require.Equal(t, wantReady, controllercommon.IsObjectReady(set.Status.Conditions))
			require.True(t, controllercommon.IsObjectReady(managed.Status.Conditions))
			require.True(t, controllercommon.IsObjectReady(ordinary.Status.Conditions))
			require.Equal(t, members, *set.Spec.SubnetNames)
			if !wantReady {
				version := set.ResourceVersion
				require.NoError(t, r.updateSharedOwnerResourceStatus(ctx, child.Name, true))
				require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(set), set))
				require.Equal(t, version, set.ResourceVersion, "retries must not repeatedly write the same status")
				// Revoking the provider must not clear the unavailable marker or
				// report readiness; explicit child configuration can revalidate it.
				owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", Annotations: map[string]string{config.T1DefaultConfigAnnotation: "true"}}}
				require.NoError(t, api.Create(ctx, owner))
				require.NoError(t, r.updateSharedOwnerResourceStatus(ctx, child.Name, false))
				require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(set), set))
				require.Equal(t, sharedOwnerUnavailableReason, set.Status.Conditions[0].Reason)
				require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(child), child))
				child.Annotations[config.VPCNetworkConfigAnnotation] = "local"
				require.NoError(t, api.Update(ctx, child))
				require.NoError(t, r.updateSharedOwnerResourceStatus(ctx, child.Name, false))
				require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(set), set))
				require.Equal(t, corev1.ConditionUnknown, set.Status.Conditions[0].Status)
			}
		})
	}
}

func TestSharedOwnerStatusWriteFailureRetries(t *testing.T) {
	previous := config.IsPerNamespaceProvidersSupported()
	config.SetPerNamespaceProvidersSupportedForTest(true)
	t.Cleanup(func() { config.SetPerNamespaceProvidersSupportedForTest(previous) })
	ctx := context.Background()
	child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: "missing"}}}
	member := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: child.Name, Annotations: map[string]string{servicecommon.AnnotationAssociatedResource: "project:vpc:subnet"}}, Status: v1alpha1.SubnetStatus{Conditions: []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: corev1.ConditionTrue}}}}
	r := createNameSpaceReconciler([]client.Object{child, member})
	fail := true
	api := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(child, member).WithStatusSubresource(member).WithInterceptorFuncs(interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if fail {
				fail = false
				return context.DeadlineExceeded
			}
			return c.SubResource(subresource).Update(ctx, obj, opts...)
		},
	}).Build()
	r.Client, r.APIReader = api, api
	require.ErrorIs(t, r.updateSharedOwnerResourceStatus(ctx, child.Name, true), context.DeadlineExceeded)
	require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(member), member))
	require.True(t, controllercommon.IsObjectReady(member.Status.Conditions))
	require.NoError(t, r.updateSharedOwnerResourceStatus(ctx, child.Name, true))
	require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(member), member))
	require.False(t, controllercommon.IsObjectReady(member.Status.Conditions))
}
