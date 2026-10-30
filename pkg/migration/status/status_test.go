/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package status

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestObservationPreservesActionAndMilestones(t *testing.T) {
	oldTime := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	s := &api.TransitionStatus{Conditions: []metav1.Condition{{Type: api.ConditionSubnetsDerived, Status: metav1.ConditionTrue, Reason: "Derived", LastTransitionTime: oldTime, ObservedGeneration: 2}}}
	require.NoError(t, RecordAction(s, api.ActionDerive, api.ActionStateSucceeded, 2))
	require.NoError(t, Observe(s, 3, api.PhaseBlocked, metav1.Condition{Type: api.ConditionReady, Status: metav1.ConditionFalse, Reason: "DependencyNotReady", Message: "Waiting for infrastructure", LastTransitionTime: oldTime}))
	require.Equal(t, int64(3), s.ObservedGeneration)
	require.Equal(t, int64(2), s.ActionGeneration)
	require.Equal(t, api.ActionDerive, s.LastAction)
	require.Equal(t, api.ActionStateSucceeded, s.ActionState)
	before := s.DeepCopy()
	require.NoError(t, Observe(s, 3, api.PhaseBlocked, metav1.Condition{Type: api.ConditionReady, Status: metav1.ConditionFalse, Reason: "DependencyNotReady", Message: "Waiting for infrastructure"}))
	require.Equal(t, before, s, "repeated observations must be idempotent")
	require.NoError(t, Observe(s, 3, api.PhaseNeedsInput, metav1.Condition{Type: api.ConditionReady, Status: metav1.ConditionFalse, Reason: "NeedsInput", Message: "Supply gateway"}))
	require.Equal(t, oldTime, meta.FindStatusCondition(s.Conditions, api.ConditionReady).LastTransitionTime)
	require.NoError(t, Observe(s, 3, api.PhaseCompleted, metav1.Condition{Type: api.ConditionReady, Status: metav1.ConditionTrue, Reason: "Verified"}))
	require.True(t, meta.FindStatusCondition(s.Conditions, api.ConditionReady).LastTransitionTime.After(oldTime.Time))
	require.Equal(t, before.Conditions[0], s.Conditions[0], "unrelated milestones must be retained")
	before = s.DeepCopy()
	require.Error(t, Observe(s, 2, api.PhaseEvaluated, metav1.Condition{Type: api.ConditionReady, Status: metav1.ConditionTrue, Reason: "Stale"}))
	require.Error(t, RecordAction(s, api.ActionExecute, api.ActionStateRunning, 2))
	require.Equal(t, before, s)
}

func TestCheckpointAndWorkflowRecovery(t *testing.T) {
	s := &api.TransitionStatus{}
	first := api.NSXWorkflowStatus{VDNName: "vdn-a", WorkflowID: "workflow-a"}
	second := api.NSXWorkflowStatus{VDNName: "vdn-b", WorkflowID: "workflow-b"}
	require.NoError(t, RecordWorkflow(s, first))
	require.NoError(t, RecordWorkflow(s, second))
	require.NoError(t, RecordWorkflow(s, first))
	require.Equal(t, []api.NSXWorkflowStatus{first, second}, s.NSXWorkflows)
	require.Error(t, RecordWorkflow(s, api.NSXWorkflowStatus{VDNName: "vdn-a", WorkflowID: "replacement"}))
	for i, action := range []api.Action{api.ActionEvaluate, api.ActionPlan, api.ActionDerive, api.ActionExecute, api.ActionRollback} {
		require.NoError(t, RecordAction(s, action, api.ActionStateRunning, int64(i+1)))
		require.NoError(t, RecordAction(s, action, api.ActionStateFailed, int64(i+1)))
		restored := s.DeepCopy()
		require.NoError(t, RecordAction(restored, action, api.ActionStateSucceeded, int64(i+1)))
		require.Equal(t, []api.NSXWorkflowStatus{first, second}, restored.NSXWorkflows)
		s = restored
	}
	s.IrreversibleAction = string(api.ActionCommit)
	before := s.DeepCopy()
	require.Error(t, RecordAction(s, api.ActionRollback, api.ActionStateRunning, 6))
	require.Equal(t, before, s)
	require.NoError(t, RecordAction(s, api.ActionCommit, api.ActionStateRunning, 6))
	require.Equal(t, string(api.ActionCommit), s.IrreversibleAction)
}

func TestNamespaceProgressRetainsOtherRecords(t *testing.T) {
	obj := &api.NamespaceNetworkTransition{Spec: api.NamespaceNetworkTransitionSpec{Namespaces: []string{"one", "two", "three"}}, Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: api.PhaseExecuting}}}
	entry := api.NamespaceStatus{Name: "one", Phase: api.PhaseCompleted, Reason: "CutoverVerified", Message: "Workloads and VIPs verified"}
	require.NoError(t, RecordNamespace(obj.Spec.Namespaces, obj.Status, entry))
	require.NoError(t, RecordNamespace(obj.Spec.Namespaces, obj.Status, api.NamespaceStatus{Name: "two", Phase: api.PhaseFailed, Reason: "RebindFailed", Message: "VM timeout"}))
	require.NoError(t, RecordNamespace(obj.Spec.Namespaces, obj.Status, entry))
	require.Len(t, obj.Status.NamespaceStatuses, 2)
	require.Equal(t, map[string]int32{api.PhaseCompleted: 1, api.PhaseFailed: 1, api.PhasePending: 1}, obj.NamespacePhaseCounts())
	require.Equal(t, api.PhaseExecuting, obj.Status.Phase, "per-namespace progress must not promote batch success")
	require.Error(t, RecordNamespace(obj.Spec.Namespaces, obj.Status, api.NamespaceStatus{Name: "other"}))
	require.Len(t, obj.Status.NamespaceStatuses, 2)
	require.Equal(t, entry, obj.Status.NamespaceStatuses[0])
}

func TestInvalidReportsDoNotMutateStatus(t *testing.T) {
	for _, update := range []func(*api.TransitionStatus) error{
		func(s *api.TransitionStatus) error { return RecordAction(s, "INVALID", api.ActionStateRunning, 1) },
		func(s *api.TransitionStatus) error { return RecordAction(s, api.ActionEvaluate, "INVALID", 1) },
		func(s *api.TransitionStatus) error {
			return RecordAction(s, api.ActionEvaluate, api.ActionStateRunning, 0)
		},
		func(s *api.TransitionStatus) error { return RecordWorkflow(s, api.NSXWorkflowStatus{VDNName: "vdn"}) },
		func(s *api.TransitionStatus) error { return Observe(s, 1, "", metav1.Condition{}) },
		func(s *api.TransitionStatus) error {
			return Observe(s, 1, api.PhaseFailed, metav1.Condition{Type: api.ConditionReady, Status: "INVALID", Reason: "Failed"})
		},
	} {
		s := &api.TransitionStatus{}
		require.Error(t, update(s))
		require.Equal(t, &api.TransitionStatus{}, s)
	}
}
