/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	migration "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	vpcapi "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

type transitionServiceReader struct {
	client.Client
	reject bool
}

func (c *transitionServiceReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, service := obj.(*corev1.Service); service && key.Namespace == "tenant" && c.reject {
		return apierrors.NewServiceUnavailable("injected transient Service GET failure")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func transitionOwnerCheck(t *testing.T, reject bool) {
	t.Helper()
	f := transitionSetup(t, "absent", false, false)
	ctx := context.Background()
	for _, svc := range []*corev1.Service{
		{ObjectMeta: metav1.ObjectMeta{Name: "lb-service", Namespace: "staging", UID: "source-service-uid"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Ports: []corev1.ServicePort{{Port: 80}}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "lb-service", Namespace: "tenant", UID: "target-service-uid"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Ports: []corev1.ServicePort{{Port: 80}}}},
	} {
		require.NoError(t, f.kube.Create(ctx, svc))
	}
	source := &vpcapi.IPAddressAllocation{}
	require.NoError(t, f.kube.Get(ctx, types.NamespacedName{Namespace: "staging", Name: "source"}, source))
	source.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Service", Name: "lb-service", UID: "source-service-uid"}}
	require.NoError(t, f.kube.Update(ctx, source))
	f.r.Client = &transitionServiceReader{Client: f.kube, reject: reject}
	result := f.reconcile(t)
	target := &vpcapi.IPAddressAllocation{}
	if reject {
		require.True(t, result.RequeueAfter > 0)
		require.True(t, apierrors.IsNotFound(f.kube.Get(ctx, types.NamespacedName{Namespace: "tenant", Name: "target"}, target)))
		require.Zero(t, f.patches)
		require.NoError(t, f.kube.Get(ctx, types.NamespacedName{Namespace: "staging", Name: "source"}, source))
		reader := f.r.Client.(*transitionServiceReader)
		reader.reject = false
		f.reconcile(t)
	}
	require.NoError(t, f.kube.Get(ctx, types.NamespacedName{Namespace: "tenant", Name: "target"}, target))
	request := &migration.NetworkResourceTransition{}
	require.NoError(t, f.kube.Get(ctx, types.NamespacedName{Name: "move"}, request))
	t.Logf("OBSERVED: transientServiceGETFailure=%t phase=%s targetOwnerReferences=%v", reject, request.Status.Phase, target.OwnerReferences)
	assert.Len(t, target.OwnerReferences, 1, "a transient owner lookup error must not silently discard the Service ownership link")
	if len(target.OwnerReferences) == 1 {
		assert.Equal(t, types.UID("target-service-uid"), target.OwnerReferences[0].UID)
	}
}

func TestTransitionOwnerReadFailure(t *testing.T) {
	transitionOwnerCheck(t, true)
}

func TestTransitionOwnerReferenceControl(t *testing.T) {
	transitionOwnerCheck(t, false)
}
