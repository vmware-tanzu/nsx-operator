/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	vpc "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

func TestObservedResourceRecoversFailedStatusWrite(t *testing.T) {
	ctx := context.Background()
	r, key, obj := runningController(t, api.ActionExecute)
	calls, creates := 0, 0
	r.Engine.Handlers[transition.Key{Stage: r.Stage, Action: api.ActionExecute}] = actionHandlerFunc(func(ctx context.Context, req transition.Request) (transition.Result, error) {
		calls++
		// A concrete handler would also verify the NetworkInterface's actual backing
		// and address-transfer state. This fixture models observed SubnetPort realization.
		port := &vpc.SubnetPort{}
		portKey := client.ObjectKey{Namespace: "tenant", Name: "vm-a"}
		if err := r.Get(ctx, portKey, port); apierrors.IsNotFound(err) {
			port.Namespace, port.Name = portKey.Namespace, portKey.Name
			if err = r.Create(ctx, port); err != nil {
				return transition.Result{}, err
			}
			creates++
			port.Status.Attachment.ID = "realized-vif"
			port.Status.Conditions = []vpc.Condition{{Type: vpc.Ready, Status: corev1.ConditionTrue}}
			if err = r.Status().Update(ctx, port); err != nil {
				return transition.Result{}, err
			}
		} else if err != nil {
			return transition.Result{}, err
		}
		if port.Status.Attachment.ID != "realized-vif" || len(port.Status.Conditions) == 0 || port.Status.Conditions[0].Status != corev1.ConditionTrue {
			return transition.Result{Outcome: transition.OutcomeInProgress}, nil
		}
		req.Object.(*api.ControlPlaneNetworkTransition).Status.DetectedSourceLBProvider = "HAPROXY"
		return transition.Result{Outcome: transition.OutcomeSucceeded}, nil
	})
	base := r.Client
	fail := true
	r.Client = interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, o client.Object, opts ...client.SubResourceUpdateOption) error {
		_, transitionStatus := o.(*api.ControlPlaneNetworkTransition)
		if sub == "status" && transitionStatus && fail {
			fail = false
			return errors.New("status unavailable")
		}
		return c.SubResource(sub).Update(ctx, o, opts...)
	}})
	_, err := r.Reconcile(ctx, key)
	require.ErrorContains(t, err, "status unavailable")
	require.Equal(t, 1, calls)
	// Restart the dispatcher; existing CRs are the progress record.
	restarted := *r
	restarted.Client = base
	restarted.Engine = &transition.Engine{Enabled: true, Handlers: r.Engine.Handlers}
	_, err = restarted.Reconcile(ctx, key)
	require.NoError(t, err)
	require.Equal(t, 2, calls, "rerun the handler to reconstruct completion from existing resources")
	require.NoError(t, r.Get(ctx, key.NamespacedName, obj))
	require.Equal(t, api.ActionStateSucceeded, obj.Status.ActionState)
	require.Equal(t, 1, creates, "realized SubnetPort is observed rather than recreated after the lost transition status write")
	require.Equal(t, api.LoadBalancerProvider("HAPROXY"), obj.Status.DetectedSourceLBProvider)
}

func TestPendingSubnetPortKeepsActionPending(t *testing.T) {
	ctx := context.Background()
	r, key, obj := runningController(t, api.ActionExecute)
	port := &vpc.SubnetPort{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "vm-a"}}
	port.Status.Attachment.ID = "assigned-before-realization"
	port.Status.Conditions = []vpc.Condition{{Type: vpc.Ready, Status: corev1.ConditionFalse}}
	require.NoError(t, r.Create(ctx, port))
	r.Engine.Handlers[transition.Key{Stage: r.Stage, Action: api.ActionExecute}] = actionHandlerFunc(func(ctx context.Context, _ transition.Request) (transition.Result, error) {
		var observed vpc.SubnetPort
		if err := r.Get(ctx, client.ObjectKeyFromObject(port), &observed); err != nil {
			return transition.Result{}, err
		}
		if observed.Status.Conditions[0].Status != corev1.ConditionTrue {
			return transition.Result{Outcome: transition.OutcomeInProgress}, nil
		}
		return transition.Result{Outcome: transition.OutcomeSucceeded}, nil
	})
	for range 2 {
		restarted := *r
		restarted.Engine = &transition.Engine{Enabled: true, Handlers: r.Engine.Handlers}
		_, err := restarted.Reconcile(ctx, key)
		require.NoError(t, err)
		require.NoError(t, r.Get(ctx, key.NamespacedName, obj))
		require.Equal(t, api.ActionStateRunning, obj.Status.ActionState)
	}
	port.Status.Conditions[0].Status = corev1.ConditionTrue
	require.NoError(t, r.Status().Update(ctx, port))
	_, err := r.Reconcile(ctx, key)
	require.NoError(t, err)
	require.NoError(t, r.Get(ctx, key.NamespacedName, obj))
	require.Equal(t, api.ActionStateSucceeded, obj.Status.ActionState)
}
