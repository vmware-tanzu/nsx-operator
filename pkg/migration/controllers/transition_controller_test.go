/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestScaffoldPreservesTransitionState(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, object := range []client.Object{
			&api.ControlPlaneNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 3},
				Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}},
				Status:     &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: "Evaluated", LastAction: api.ActionEvaluate}},
			},
			&api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "batch", Generation: 3},
				Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant"}},
				Status:     &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: "Failed", LastAction: api.ActionExecute}},
			},
		} {
			t.Run(fmt.Sprintf("%T/enabled=%t", object, enabled), func(t *testing.T) {
				scheme := runtime.NewScheme()
				require.NoError(t, api.AddToScheme(scheme))
				c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(object).WithObjects(object).Build()
				key := types.NamespacedName{Name: object.GetName()}
				before := object.DeepCopyObject().(client.Object)
				require.NoError(t, c.Get(context.Background(), key, before))
				r := &Reconciler{Client: c, Enabled: enabled, Object: object}
				result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
				require.NoError(t, err)
				require.Zero(t, result)
				after := object.DeepCopyObject().(client.Object)
				require.NoError(t, c.Get(context.Background(), key, after))
				require.Equal(t, before, after, "framework watches must preserve desired state and recovery records")
				_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "missing"}})
				require.NoError(t, err)
			})
		}
	}
}
