/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/infrastructure"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

type infraReadiness func(context.Context, bool, string) error

func (f infraReadiness) Check(ctx context.Context, allowVPC bool, projectPath string) error {
	return f(ctx, allowVPC, projectPath)
}

type infraProjects func(context.Context, string) (infrastructure.Project, error)

func (f infraProjects) GetProject(ctx context.Context, id string) (infrastructure.Project, error) {
	return f(ctx, id)
}

type infraHandler func(context.Context, transition.Request) (transition.Result, error)

func (f infraHandler) Reconcile(ctx context.Context, req transition.Request) (transition.Result, error) {
	return f(ctx, req)
}

func TestInfrastructureStartGateAndResume(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, api.AddToScheme(scheme))
	obj := &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: types.UID("infra"), Generation: 1},
		Spec: api.ControlPlaneNetworkTransitionSpec{
			TransitionSpec: api.TransitionSpec{SourceMode: "VSPHERE_VDS", TargetMode: "NSX_VPC", Action: api.ActionEvaluate},
			NSXProjectID:   "tenant-project",
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).Build()
	ready := false
	checks, projectReads, handlerCalls := 0, 0, 0
	service := &infrastructure.Service{
		Readiness: infraReadiness(func(context.Context, bool, string) error {
			checks++
			if !ready {
				return &infrastructure.Issue{Reason: "NCPNotReady", Message: "NCP not ready"}
			}
			return nil
		}),
		Projects: infraProjects(func(_ context.Context, id string) (infrastructure.Project, error) {
			projectReads++
			return infrastructure.Project{ID: id, Path: "/orgs/default/projects/" + id}, nil
		}),
	}
	engine := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
		{Stage: transition.Infrastructure, Action: api.ActionEvaluate}: infraHandler(func(ctx context.Context, req transition.Request) (transition.Result, error) {
			handlerCalls++
			var saved api.ControlPlaneNetworkTransition
			require.NoError(t, c.Get(ctx, types.NamespacedName{Name: obj.Name}, &saved))
			require.Equal(t, "/orgs/default/projects/tenant-project", saved.Status.TargetNSXProjectPath)
			require.Equal(t, api.ActionStateRunning, saved.Status.ActionState, "action checkpoint must persist before handler effects")
			return transition.Result{Outcome: transition.OutcomeSucceeded}, nil
		}),
	}}
	r := &Reconciler{Client: c, APIReader: c, Engine: engine, Stage: transition.Infrastructure, Infrastructure: service}
	key := ctrl.Request{NamespacedName: types.NamespacedName{Name: obj.Name}}

	result, err := r.Reconcile(ctx, key)
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter)
	require.Zero(t, handlerCalls, "unready prerequisites must block action dispatch")
	require.Zero(t, projectReads, "do not resolve a project before readiness passes")
	var saved api.ControlPlaneNetworkTransition
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: obj.Name}, &saved))
	require.Equal(t, "EvaluationFailed", saved.Status.Phase)
	require.Equal(t, "NCPNotReady", meta.FindStatusCondition(saved.Status.Conditions, infrastructure.ConditionReady).Reason)

	ready = true
	checks, projectReads = 0, 0
	_, err = r.Reconcile(ctx, key)
	require.NoError(t, err)
	require.Zero(t, handlerCalls, "the engine persists its action checkpoint before dispatch")
	require.Equal(t, 1, checks)
	require.Equal(t, 1, projectReads)
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: obj.Name}, &saved))
	require.Equal(t, "/orgs/default/projects/tenant-project", saved.Status.TargetNSXProjectPath)
	require.Equal(t, api.ActionStateRunning, saved.Status.ActionState)

	_, err = r.Reconcile(ctx, key)
	require.NoError(t, err)
	require.Equal(t, 1, handlerCalls)
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: obj.Name}, &saved))
	require.Equal(t, api.ActionStateSucceeded, saved.Status.ActionState)
}
