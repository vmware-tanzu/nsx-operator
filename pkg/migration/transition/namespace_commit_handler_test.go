/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

type workflowDeleterFunc func(context.Context, string) error

func (f workflowDeleterFunc) DeleteWorkflow(ctx context.Context, id string) error {
	return f(ctx, id)
}

func TestValidateCommitWorkflowIDs(t *testing.T) {
	ids, err := validateCommitWorkflowIDs([]api.NSXWorkflowStatus{
		{VDNName: "vdn-b", WorkflowID: "workflow-b"},
		{VDNName: "vdn-a", WorkflowID: "workflow-a"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"workflow-a", "workflow-b"}, ids)

	tests := []struct {
		name      string
		workflows []api.NSXWorkflowStatus
		wantError string
	}{
		{
			name:      "no recorded workflows",
			workflows: nil,
			wantError: "no recorded NSX workflows",
		},
		{
			name:      "missing VDN name",
			workflows: []api.NSXWorkflowStatus{{WorkflowID: "workflow-a"}},
			wantError: "incomplete workflow identity",
		},
		{
			name:      "missing workflow ID",
			workflows: []api.NSXWorkflowStatus{{VDNName: "vdn-a"}},
			wantError: "incomplete workflow identity",
		},
		{
			name: "duplicate workflow ID",
			workflows: []api.NSXWorkflowStatus{
				{VDNName: "vdn-a", WorkflowID: "workflow-a"},
				{VDNName: "vdn-b", WorkflowID: "workflow-a"},
			},
			wantError: "duplicate workflow ID",
		},
		{
			name: "too many workflows",
			workflows: func() []api.NSXWorkflowStatus {
				workflows := make([]api.NSXWorkflowStatus, 1001)
				for i := range workflows {
					workflows[i] = api.NSXWorkflowStatus{
						VDNName:    "vdn-" + strconv.Itoa(i),
						WorkflowID: "workflow-" + strconv.Itoa(i),
					}
				}
				return workflows
			}(),
			wantError: "exceeds the workflow limit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateCommitWorkflowIDs(test.workflows)
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestNamespaceCommitRejectsEmptyWorkflowListDuringPreflightAndResume(t *testing.T) {
	batch := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch"},
		Spec: api.NamespaceNetworkTransitionSpec{
			TransitionSpec: api.TransitionSpec{Action: api.ActionCommit},
			Namespaces:     []string{"tenant"},
		},
	}
	deleted := false
	handler := NamespaceCommitHandler{Workflows: workflowDeleterFunc(func(context.Context, string) error {
		deleted = true
		return nil
	})}

	for _, irreversibleAction := range []string{"", string(api.ActionCommit)} {
		t.Run("checkpoint="+irreversibleAction, func(t *testing.T) {
			status := &api.TransitionStatus{IrreversibleAction: irreversibleAction}
			req := Request{Stage: Namespaces, Object: batch, Action: api.ActionCommit, Status: status}
			_, err := handler.Reconcile(context.Background(), req)
			require.ErrorContains(t, err, "no recorded NSX workflows")
			require.False(t, deleted, "must not report success or delete anything without recorded workflows")
		})
	}
}

func TestNamespaceCommitCheckpointsBeforeDeletingRecordedWorkflows(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	batch := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch"},
		Spec: api.NamespaceNetworkTransitionSpec{
			TransitionSpec: api.TransitionSpec{Action: api.ActionCommit},
			Namespaces:     []string{"tenant-a", "tenant-b"},
		},
		Status: &api.NamespaceNetworkTransitionStatus{NamespaceStatuses: []api.NamespaceStatus{{Name: "tenant-a", Phase: "Completed"}, {Name: "tenant-b", Phase: "Completed"}}},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(batch).Build()
	status := &api.TransitionStatus{
		NSXWorkflows: []api.NSXWorkflowStatus{{VDNName: "vdn-b", WorkflowID: "workflow-b"}, {VDNName: "vdn-a", WorkflowID: "workflow-a"}},
	}
	var deleted []string
	handler := NamespaceCommitHandler{Reader: reader, Workflows: workflowDeleterFunc(func(_ context.Context, id string) error {
		deleted = append(deleted, id)
		return nil
	})}
	req := Request{Stage: Namespaces, Object: batch, Action: api.ActionCommit, Generation: 2, Status: status}
	result, err := handler.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, OutcomeCheckpointIrreversible, result.Outcome)
	require.Empty(t, deleted, "preflight must return before the engine persists PONR")

	status.IrreversibleAction = string(api.ActionCommit)
	result, err = handler.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, OutcomeSucceeded, result.Outcome)
	require.Equal(t, []string{"workflow-a", "workflow-b"}, deleted)
}

func TestNamespaceCommitFailureResumesWithSameWorkflowIdentities(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	batch := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch"},
		Spec: api.NamespaceNetworkTransitionSpec{
			TransitionSpec: api.TransitionSpec{Action: api.ActionCommit},
			Namespaces:     []string{"tenant"},
		},
		Status: &api.NamespaceNetworkTransitionStatus{NamespaceStatuses: []api.NamespaceStatus{{Name: "tenant", Phase: "Completed"}}},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(batch).Build()
	status := &api.TransitionStatus{
		NSXWorkflows:       []api.NSXWorkflowStatus{{VDNName: "vdn-a", WorkflowID: "workflow-a"}, {VDNName: "vdn-b", WorkflowID: "workflow-b"}},
		IrreversibleAction: string(api.ActionCommit),
	}
	failure := errors.New("NSX temporarily unavailable")
	failSecond := true
	var calls []string
	handler := NamespaceCommitHandler{Reader: reader, Workflows: workflowDeleterFunc(func(_ context.Context, id string) error {
		calls = append(calls, id)
		if id == "workflow-b" && failSecond {
			return failure
		}
		return nil
	})}
	req := Request{Stage: Namespaces, Object: batch, Action: api.ActionCommit, Generation: 2, Status: status}
	_, err := handler.Reconcile(ctx, req)
	require.ErrorIs(t, err, failure)
	require.Equal(t, string(api.ActionCommit), status.IrreversibleAction, "handler cannot clear the persisted point of no return")

	failSecond = false
	result, err := handler.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, OutcomeSucceeded, result.Outcome)
	require.Equal(t, []string{"workflow-a", "workflow-b", "workflow-a", "workflow-b"}, calls, "retry uses the same persisted identities")
}

func TestNamespaceCommitRejectsSharedWorkflowAndIncompleteBatch(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	batch := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch"},
		Spec: api.NamespaceNetworkTransitionSpec{
			TransitionSpec: api.TransitionSpec{Action: api.ActionCommit},
			Namespaces:     []string{"tenant"},
		},
		Status: &api.NamespaceNetworkTransitionStatus{NamespaceStatuses: []api.NamespaceStatus{{Name: "tenant", Phase: "Completed"}}},
	}
	shared := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "other-batch"},
		Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"other"}},
		Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
			NSXWorkflows: []api.NSXWorkflowStatus{{VDNName: "shared-vdn", WorkflowID: "workflow-a"}},
		}},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(batch, shared).Build()
	deleted := 0
	handler := NamespaceCommitHandler{Reader: reader, Workflows: workflowDeleterFunc(func(context.Context, string) error { deleted++; return nil })}
	status := &api.TransitionStatus{NSXWorkflows: []api.NSXWorkflowStatus{{VDNName: "vdn-a", WorkflowID: "workflow-a"}}}
	req := Request{Stage: Namespaces, Object: batch, Action: api.ActionCommit, Generation: 2, Status: status}
	_, err := handler.Reconcile(ctx, req)
	require.ErrorContains(t, err, "still referenced by namespace transition")
	require.Empty(t, status.IrreversibleAction)
	require.Zero(t, deleted)

	require.NoError(t, reader.Delete(ctx, shared))
	batch.Status.NamespaceStatuses[0].Phase = "Failed"
	_, err = handler.Reconcile(ctx, req)
	require.ErrorContains(t, err, "has not completed verified network execution")
	require.Empty(t, status.IrreversibleAction)
	require.Zero(t, deleted)
}
