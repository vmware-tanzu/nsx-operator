/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package status

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestStatusPersistence(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		object client.Object
		update func(client.Client, client.Object, func(*api.TransitionStatus) error) (client.Object, error)
		status func(client.Object) *api.TransitionStatus
	}{
		{"controlplane", &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: "cp-uid", Generation: 1}, Spec: api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate, SourceMode: "VSPHERE_VDS", TargetMode: "NSX_VPC"}}},
			func(c client.Client, o client.Object, f func(*api.TransitionStatus) error) (client.Object, error) {
				return UpdateControlPlane(ctx, c, o.(*api.ControlPlaneNetworkTransition), func(s *api.ControlPlaneNetworkTransitionStatus) error { return f(&s.TransitionStatus) })
			},
			func(o client.Object) *api.TransitionStatus {
				return &o.(*api.ControlPlaneNetworkTransition).Status.TransitionStatus
			}},
		{"namespace", &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", UID: "batch-uid", Generation: 1}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate, SourceMode: "VSPHERE_VDS", TargetMode: "NSX_VPC"}, Namespaces: []string{"one", "two"}}},
			func(c client.Client, o client.Object, f func(*api.TransitionStatus) error) (client.Object, error) {
				return UpdateNamespace(ctx, c, o.(*api.NamespaceNetworkTransition), func(s *api.NamespaceNetworkTransitionStatus) error { return f(&s.TransitionStatus) })
			},
			func(o client.Object) *api.TransitionStatus {
				return &o.(*api.NamespaceNetworkTransition).Status.TransitionStatus
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, api.AddToScheme(scheme))
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(tc.object).WithObjects(tc.object).Build()
			key := client.ObjectKeyFromObject(tc.object)
			snapshot := tc.object.DeepCopyObject().(client.Object)
			require.NoError(t, c.Get(ctx, key, snapshot))
			original := snapshot.DeepCopyObject()
			updated, err := tc.update(c, snapshot, func(s *api.TransitionStatus) error {
				if err := RecordAction(s, api.ActionEvaluate, api.ActionStateRunning, 1); err != nil {
					return err
				}
				if err := RecordWorkflow(s, api.NSXWorkflowStatus{VDNName: "vdn-a", WorkflowID: "workflow-a"}); err != nil {
					return err
				}
				return Observe(s, 1, api.PhaseEvaluating, metav1.Condition{Type: api.ConditionReady, Status: metav1.ConditionFalse, Reason: "InProgress"})
			})
			require.NoError(t, err)
			require.Equal(t, original, snapshot, "writing status must not mutate the caller's snapshot")
			read := tc.object.DeepCopyObject().(client.Object)
			require.NoError(t, c.Get(ctx, key, read))
			require.Equal(t, updated, read)
			require.Equal(t, api.ActionStateRunning, tc.status(read).ActionState)
			require.Equal(t, int64(1), tc.status(read).ActionGeneration)
			same, err := tc.update(c, read, func(s *api.TransitionStatus) error {
				return Observe(s, 1, api.PhaseEvaluating, metav1.Condition{Type: api.ConditionReady, Status: metav1.ConditionFalse, Reason: "InProgress"})
			})
			require.NoError(t, err)
			require.Equal(t, read.GetResourceVersion(), same.GetResourceVersion(), "no-op status must not write")
			// A concurrent reporter adds another workflow. Replacing the status list
			// from the older snapshot must conflict, not lose that reporter's handle.
			concurrent, err := tc.update(c, read, func(s *api.TransitionStatus) error {
				return RecordWorkflow(s, api.NSXWorkflowStatus{VDNName: "vdn-b", WorkflowID: "workflow-b"})
			})
			require.NoError(t, err)
			_, err = tc.update(c, read, func(s *api.TransitionStatus) error {
				return RecordWorkflow(s, api.NSXWorkflowStatus{VDNName: "vdn-c", WorkflowID: "workflow-c"})
			})
			require.True(t, apierrors.IsConflict(err), "%v", err)
			require.NoError(t, c.Get(ctx, key, concurrent))
			require.Len(t, tc.status(concurrent).NSXWorkflows, 2)
			retried, err := tc.update(c, concurrent, func(s *api.TransitionStatus) error {
				return RecordWorkflow(s, api.NSXWorkflowStatus{VDNName: "vdn-c", WorkflowID: "workflow-c"})
			})
			require.NoError(t, err)
			require.Len(t, tc.status(retried).NSXWorkflows, 3)
			// A new desired generation invalidates a result computed for the old CR.
			desired := retried.DeepCopyObject().(client.Object)
			switch o := desired.(type) {
			case *api.ControlPlaneNetworkTransition:
				o.Spec.Action = api.ActionPlan
			case *api.NamespaceNetworkTransition:
				o.Spec.Action = api.ActionPlan
			}
			desired.SetGeneration(2)
			require.NoError(t, c.Update(ctx, desired))
			_, err = tc.update(c, retried, func(s *api.TransitionStatus) error {
				return RecordAction(s, api.ActionEvaluate, api.ActionStateSucceeded, 1)
			})
			require.True(t, apierrors.IsConflict(err), "%v", err)
			require.NoError(t, c.Get(ctx, key, desired))
			require.Equal(t, api.ActionStateRunning, tc.status(desired).ActionState)
			blocked, err := tc.update(c, desired, func(s *api.TransitionStatus) error {
				return Observe(s, 2, api.PhaseBlocked, metav1.Condition{Type: api.ConditionReady, Status: metav1.ConditionFalse, Reason: "PrerequisiteNotReady"})
			})
			require.NoError(t, err)
			require.Equal(t, int64(2), tc.status(blocked).ObservedGeneration)
			require.Equal(t, int64(1), tc.status(blocked).ActionGeneration)
			require.Len(t, tc.status(blocked).NSXWorkflows, 3)
			t.Run("generation regressions", func(t *testing.T) {
				seed := blocked.DeepCopyObject().(client.Object)
				seed.SetGeneration(4)
				tc.status(seed).ObservedGeneration = 3
				tc.status(seed).ActionGeneration = 2
				isolated := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(seed).WithObjects(seed).Build()
				require.NoError(t, isolated.Get(ctx, key, seed))
				before := seed.DeepCopyObject()
				for _, regression := range []struct {
					name   string
					mutate func(*api.TransitionStatus)
				}{
					{"observation", func(s *api.TransitionStatus) { s.ObservedGeneration = 2 }},
					{"action", func(s *api.TransitionStatus) { s.ActionGeneration = 1 }},
					{"both", func(s *api.TransitionStatus) { s.ObservedGeneration, s.ActionGeneration = 1, 1 }},
					{"clear checkpoint", func(s *api.TransitionStatus) { s.LastAction, s.ActionState, s.ActionGeneration = "", "", 0 }},
				} {
					t.Run(regression.name, func(t *testing.T) {
						_, err := tc.update(isolated, seed, func(s *api.TransitionStatus) error { regression.mutate(s); return nil })
						require.ErrorContains(t, err, "generations cannot regress")
						require.Equal(t, before, seed, "rejected mutations must preserve the caller's snapshot")
						actual := seed.DeepCopyObject().(client.Object)
						require.NoError(t, isolated.Get(ctx, key, actual))
						require.Equal(t, before, actual, "rejected mutations must not change persisted status or resourceVersion")
					})
				}
				// Observing a newer request must still retain the older action checkpoint.
				advanced, err := tc.update(isolated, seed, func(s *api.TransitionStatus) error {
					return Observe(s, 4, api.PhaseBlocked, metav1.Condition{Type: api.ConditionReady, Status: metav1.ConditionFalse, Reason: "PrerequisiteNotReady"})
				})
				require.NoError(t, err)
				require.Equal(t, int64(4), tc.status(advanced).ObservedGeneration)
				require.Equal(t, int64(2), tc.status(advanced).ActionGeneration)
			})
			failure := errors.New("invalid progress")
			_, err = tc.update(c, blocked, func(s *api.TransitionStatus) error { s.NSXWorkflows = nil; return failure })
			require.ErrorIs(t, err, failure)
			require.NoError(t, c.Get(ctx, key, desired))
			require.Equal(t, blocked, desired)
			_, err = tc.update(c, blocked, func(s *api.TransitionStatus) error { s.ObservedGeneration = 3; return nil })
			require.ErrorContains(t, err, "generations")
			require.NoError(t, c.Delete(ctx, desired))
			_, err = tc.update(c, blocked, func(s *api.TransitionStatus) error { s.Phase = api.PhaseFailed; return nil })
			require.True(t, apierrors.IsNotFound(err), "%v", err)
		})
	}
}

func TestNamespaceStatusUpdateAndCounts(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	obj := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", UID: "batch-uid", Generation: 4}, Spec: api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"one", "two", "three"}}, Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: api.PhaseExecuting}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).Build()
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	result, err := UpdateNamespace(ctx, c, obj, func(s *api.NamespaceNetworkTransitionStatus) error {
		if err := RecordNamespace(obj.Spec.Namespaces, s, api.NamespaceStatus{Name: "one", Phase: api.PhaseCompleted, Reason: "Verified", Message: "Cutover verified"}); err != nil {
			return err
		}
		return RecordNamespace(obj.Spec.Namespaces, s, api.NamespaceStatus{Name: "two", Phase: api.PhaseFailed, Reason: "RebindFailed", Message: "VM timeout"})
	})
	require.NoError(t, err)
	require.Equal(t, map[string]int32{api.PhaseCompleted: 1, api.PhaseFailed: 1, api.PhasePending: 1}, result.NamespacePhaseCounts())
	require.Equal(t, obj.Spec, result.Spec)
	require.Equal(t, api.PhaseExecuting, result.Status.Phase)
	require.Equal(t, "VM timeout", result.Status.NamespaceStatuses[1].Message)
	_, err = UpdateNamespace(ctx, c, result, func(s *api.NamespaceNetworkTransitionStatus) error {
		s.NamespaceStatuses = append(s.NamespaceStatuses, api.NamespaceStatus{Name: "outside"})
		return nil
	})
	require.ErrorContains(t, err, "outside the transition batch")
	before := result.DeepCopy()
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), result))
	require.Equal(t, before, result)
}

func TestInvalidStatusSnapshots(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	obj := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: "cp-uid", Generation: 2}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).Build()
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	for _, tc := range []struct {
		name   string
		mutate func(*api.ControlPlaneNetworkTransitionStatus) error
	}{
		{"partial checkpoint", func(s *api.ControlPlaneNetworkTransitionStatus) error { s.LastAction = api.ActionEvaluate; return nil }},
		{"action newer than observation", func(s *api.ControlPlaneNetworkTransitionStatus) error {
			s.ObservedGeneration = 1
			s.ActionGeneration = 2
			return nil
		}},
		{"future condition", func(s *api.ControlPlaneNetworkTransitionStatus) error {
			s.Conditions = []metav1.Condition{{Type: api.ConditionReady, ObservedGeneration: 3}}
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UpdateControlPlane(ctx, c, obj, tc.mutate)
			require.Error(t, err)
			actual := &api.ControlPlaneNetworkTransition{}
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), actual))
			require.Equal(t, obj, actual)
		})
	}
	for _, field := range []string{"UID", "resourceVersion"} {
		snapshot := obj.DeepCopy()
		if field == "UID" {
			snapshot.UID = ""
		} else {
			snapshot.ResourceVersion = ""
		}
		_, err := UpdateControlPlane(ctx, c, snapshot, func(s *api.ControlPlaneNetworkTransitionStatus) error { s.Phase = api.PhaseEvaluating; return nil })
		require.ErrorContains(t, err, "persisted snapshot")
	}
	unchanged, err := UpdateControlPlane(ctx, c, obj, func(*api.ControlPlaneNetworkTransitionStatus) error { return nil })
	require.NoError(t, err)
	require.Nil(t, unchanged.Status)
	require.Equal(t, obj.ResourceVersion, unchanged.ResourceVersion)
}

func TestStatusWriterActionScope(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	for _, infrastructure := range []bool{true, false} {
		for _, action := range []api.Action{api.ActionEvaluate, api.ActionPlan, api.ActionDerive, api.ActionExecute, api.ActionRollback, api.ActionCommit, api.ActionFinalize} {
			t.Run(fmt.Sprintf("infrastructure=%t/%s", infrastructure, action), func(t *testing.T) {
				var obj client.Object
				var update func(client.Client, client.Object, func(*api.TransitionStatus) error) (client.Object, error)
				if infrastructure {
					obj = &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: "cp", Generation: 1}}
					update = func(c client.Client, o client.Object, f func(*api.TransitionStatus) error) (client.Object, error) {
						return UpdateControlPlane(ctx, c, o.(*api.ControlPlaneNetworkTransition), func(s *api.ControlPlaneNetworkTransitionStatus) error { return f(&s.TransitionStatus) })
					}
				} else {
					obj = &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch", UID: "batch", Generation: 1}, Spec: api.NamespaceNetworkTransitionSpec{Namespaces: []string{"tenant"}}}
					update = func(c client.Client, o client.Object, f func(*api.TransitionStatus) error) (client.Object, error) {
						return UpdateNamespace(ctx, c, o.(*api.NamespaceNetworkTransition), func(s *api.NamespaceNetworkTransitionStatus) error { return f(&s.TransitionStatus) })
					}
				}
				c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).Build()
				require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
				before := obj.DeepCopyObject()
				_, err := update(c, obj, func(s *api.TransitionStatus) error { return RecordAction(s, action, api.ActionStateRunning, 1) })
				valid := (infrastructure && action != api.ActionRollback && action != api.ActionCommit) || (!infrastructure && action != api.ActionFinalize)
				if valid {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "cannot accept or advance")
					require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
					require.Equal(t, before, obj)
				}
			})
		}
	}
}

func TestHistoricalInfrastructureRollbackCheckpointPreserved(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	obj := &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: "cp", Generation: 2},
		Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{ObservedGeneration: 1, ActionGeneration: 1, LastAction: api.ActionRollback, ActionState: api.ActionStateFailed}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).Build()
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	updated, err := UpdateControlPlane(ctx, c, obj, func(s *api.ControlPlaneNetworkTransitionStatus) error {
		return Observe(&s.TransitionStatus, 2, api.PhaseBlocked, metav1.Condition{Type: api.ConditionReady, Status: metav1.ConditionFalse, Reason: "InfrastructureRollbackUnsupported"})
	})
	require.NoError(t, err)
	require.Equal(t, api.ActionRollback, updated.Status.LastAction)
	require.Equal(t, api.ActionStateFailed, updated.Status.ActionState)
	_, err = UpdateControlPlane(ctx, c, updated, func(s *api.ControlPlaneNetworkTransitionStatus) error {
		return RecordAction(&s.TransitionStatus, api.ActionRollback, api.ActionStateSucceeded, 2)
	})
	require.ErrorContains(t, err, "cannot accept or advance")
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	require.Equal(t, updated, obj)
}
