/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

type readinessFunc func(context.Context, bool, string) error

func (f readinessFunc) Check(ctx context.Context, allow bool, path string) error {
	return f(ctx, allow, path)
}

type projectFunc func(context.Context, string) (Project, error)

func (f projectFunc) GetProject(ctx context.Context, id string) (Project, error) {
	return f(ctx, id)
}

func setup() (*Service, *api.ControlPlaneNetworkTransition) {
	obj := &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: "infra", Generation: 1},
		Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{SourceMode: "VSPHERE_VDS", TargetMode: "NSX_VPC", Action: api.ActionEvaluate}},
	}
	service := &Service{
		Readiness: readinessFunc(func(context.Context, bool, string) error { return nil }),
		Projects: projectFunc(func(_ context.Context, id string) (Project, error) {
			return Project{ID: id, Path: "/orgs/default/projects/" + id}, nil
		}),
	}
	return service, obj
}

func TestProjectSelectionAndStatus(t *testing.T) {
	for _, id := range []string{"", "tenant-project"} {
		t.Run(id, func(t *testing.T) {
			service, obj := setup()
			obj.Spec.NSXProjectID = id
			require.NoError(t, service.Check(context.Background(), obj))
			want := id
			if want == "" {
				want = "default"
			}
			require.Equal(t, "/orgs/default/projects/"+want, obj.Status.TargetNSXProjectPath)
			require.True(t, meta.IsStatusConditionTrue(obj.Status.Conditions, ConditionReady))
			require.Empty(t, obj.Status.LastAction, "prerequisite checks do not complete Evaluate")
			require.Empty(t, obj.Status.PrimaryVPCPath)
			require.Empty(t, obj.Status.NSXWorkflows)
			require.Empty(t, obj.Status.LBSNATIPAllocations)
		})
	}
	for _, id := range []string{"/orgs/default/projects/x", "..", ".", " x", "x/child", "x%2fy", "x?query", "x\n"} {
		_, err := ProjectID(id)
		require.Error(t, err, id)
	}
}

func TestPrerequisiteFailurePreservesExistingStatus(t *testing.T) {
	service, obj := setup()
	obj.Status = &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{NSXWorkflows: []api.NSXWorkflowStatus{{VDNName: "network", WorkflowID: "recorded-workflow"}}, LastAction: api.ActionDerive, ActionState: api.ActionStateFailed, ActionGeneration: 1}, PrimaryVPCPath: "/existing-vpc"}
	service.Readiness = readinessFunc(func(context.Context, bool, string) error { return issue("NCPNotReady", "Wait for NCP") })
	service.Projects = projectFunc(func(context.Context, string) (Project, error) {
		t.Fatal("project read before readiness")
		return Project{}, nil
	})
	require.Error(t, service.Check(context.Background(), obj))
	require.Equal(t, "NCPNotReady", meta.FindStatusCondition(obj.Status.Conditions, ConditionReady).Reason)
	require.Equal(t, "recorded-workflow", obj.Status.NSXWorkflows[0].WorkflowID)
	require.Equal(t, "/existing-vpc", obj.Status.PrimaryVPCPath)
	require.Equal(t, api.ActionStateFailed, obj.Status.ActionState)
	service.Readiness = readinessFunc(func(context.Context, bool, string) error {
		return readFailure("Cannot read provider", errors.New("forbidden"))
	})
	require.Error(t, service.Check(context.Background(), obj))
	require.Equal(t, metav1.ConditionUnknown, meta.FindStatusCondition(obj.Status.Conditions, ConditionReady).Status)
}

func TestProjectSelectionPersistsInTransitionStatus(t *testing.T) {
	service, obj := setup()
	obj.Spec.NSXProjectID = "tenant-project"
	require.NoError(t, service.Check(context.Background(), obj))
	require.Equal(t, "/orgs/default/projects/tenant-project", obj.Status.TargetNSXProjectPath)

	// A restarted service resolves the same project from the persisted CR status.
	restarted, _ := setup()
	restarted.Projects = service.Projects
	require.NoError(t, restarted.Check(context.Background(), obj))
	require.Equal(t, "/orgs/default/projects/tenant-project", obj.Status.TargetNSXProjectPath)
}

func TestProjectCorrectionBeforeActionAndLockAfterStart(t *testing.T) {
	service, obj := setup()
	require.NoError(t, service.Check(context.Background(), obj))
	obj.Generation++
	obj.Spec.NSXProjectID = "tenant-project"
	require.NoError(t, service.Check(context.Background(), obj), "project may be corrected while still evaluating and before resources exist")
	require.Equal(t, "/orgs/default/projects/tenant-project", obj.Status.TargetNSXProjectPath)

	obj.Spec.Action = api.ActionExecute
	obj.Generation++
	obj.Status.LastAction = api.ActionExecute
	obj.Status.ActionState = api.ActionStateRunning
	obj.Status.ActionGeneration = obj.Generation
	obj.Spec.NSXProjectID = "another-project"
	require.Error(t, service.Check(context.Background(), obj), "the selected project is fixed after the action checkpoint")
	require.Equal(t, "ProjectIdentityConflict", meta.FindStatusCondition(obj.Status.Conditions, ConditionReady).Reason)
	require.Equal(t, "/orgs/default/projects/tenant-project", obj.Status.TargetNSXProjectPath)
}

func TestNSXLookupFailureDoesNotFallbackOrEraseIdentity(t *testing.T) {
	service, obj := setup()
	obj.Spec.NSXProjectID = "tenant-project"
	obj.Status = &api.ControlPlaneNetworkTransitionStatus{TargetNSXProjectPath: "/orgs/default/projects/tenant-project", LoadBalancerVPCPath: "/existing-lb-vpc"}
	calls := 0
	service.Projects = projectFunc(func(_ context.Context, id string) (Project, error) {
		calls++
		require.Equal(t, "tenant-project", id)
		return Project{}, &Issue{Reason: "NSXProjectReadFailed", Message: "Cannot read NSX project", Unknown: true}
	})
	require.Error(t, service.Check(context.Background(), obj))
	require.Equal(t, 1, calls)
	require.Equal(t, "/existing-lb-vpc", obj.Status.LoadBalancerVPCPath)
	require.Equal(t, "/orgs/default/projects/tenant-project", obj.Status.TargetNSXProjectPath)
}

func TestResumeAllowsVPCOnlyAfterExecuteCheckpoint(t *testing.T) {
	service, obj := setup()
	allow := false
	service.Readiness = readinessFunc(func(_ context.Context, vpc bool, _ string) error { allow = vpc; return nil })
	require.NoError(t, service.Check(context.Background(), obj))
	require.False(t, allow)
	obj.Status.LastAction = api.ActionExecute
	obj.Status.ActionState = api.ActionStateFailed
	require.NoError(t, service.Check(context.Background(), obj))
	require.True(t, allow)
}

func TestLegacyResourcesRequireRecordedProject(t *testing.T) {
	service, obj := setup()
	obj.Status = &api.ControlPlaneNetworkTransitionStatus{PrimaryVPCPath: "/existing-primary"}
	require.Error(t, service.Check(context.Background(), obj))
	require.Equal(t, "ProjectIdentityMissing", meta.FindStatusCondition(obj.Status.Conditions, ConditionReady).Reason)
	require.Equal(t, "/existing-primary", obj.Status.PrimaryVPCPath)
}

func TestReadinessChangeRefreshesCondition(t *testing.T) {
	service, obj := setup()
	require.NoError(t, service.Check(context.Background(), obj))
	service.Readiness = readinessFunc(func(context.Context, bool, string) error { return issue("NCPNotReady", "NCP restarted") })
	require.Error(t, service.Check(context.Background(), obj))
	require.Equal(t, "NCPNotReady", meta.FindStatusCondition(obj.Status.Conditions, ConditionReady).Reason)
	require.False(t, meta.IsStatusConditionTrue(obj.Status.Conditions, ConditionReady))
}
