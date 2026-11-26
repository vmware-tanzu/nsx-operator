/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestReadNamespaceBatchSnapshot(t *testing.T) {
	ctx := context.Background()
	deletionTimestamp := metav1.NewTime(time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC))
	batch := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch", UID: "batch-uid", Generation: 7},
		Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"zeta", "alpha"}},
		Status: &api.NamespaceNetworkTransitionStatus{
			NamespaceStatuses: []api.NamespaceStatus{{Name: "alpha", Phase: api.PhaseExecuting, Reason: "InProgress", Message: "workload cutover started"}},
			TransitionStatus: api.TransitionStatus{
				Phase: api.PhaseExecuting, LastAction: api.ActionExecute, ActionState: api.ActionStateRunning,
				ActionGeneration: 7, ObservedGeneration: 7,
				NSXWorkflows: []api.NSXWorkflowStatus{{VDNName: "source-vdn", WorkflowID: "workflow-1"}},
			},
		},
	}
	alpha := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "alpha", UID: "alpha-uid"}}
	zeta := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "zeta", UID: "zeta-uid", DeletionTimestamp: &deletionTimestamp, Finalizers: []string{"test-finalizer"}}}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(batch, alpha, zeta).Build()

	snapshot, err := ReadNamespaceBatchSnapshot(ctx, c, batch)
	if err != nil {
		t.Fatalf("ReadNamespaceBatchSnapshot() error = %v", err)
	}
	if snapshot.TransitionUID != "batch-uid" || snapshot.Generation != 7 {
		t.Fatalf("unexpected transition identity: uid=%q generation=%d", snapshot.TransitionUID, snapshot.Generation)
	}
	if snapshot.RequestedAction != api.ActionExecute || snapshot.LastAction != api.ActionExecute ||
		snapshot.ActionState != api.ActionStateRunning || snapshot.ActionGeneration != 7 ||
		snapshot.ObservedGeneration != 7 || snapshot.Phase != api.PhaseExecuting {
		t.Fatalf("batch action checkpoint was not copied: %#v", snapshot)
	}
	if len(snapshot.Namespaces) != 2 {
		t.Fatalf("got %d namespace snapshots, want 2", len(snapshot.Namespaces))
	}
	if snapshot.Namespaces[0].Name != "alpha" || snapshot.Namespaces[1].Name != "zeta" {
		t.Fatalf("namespace snapshots are not sorted: %#v", snapshot.Namespaces)
	}
	if snapshot.Namespaces[0].UID != "alpha-uid" || !snapshot.Namespaces[0].HasProgress ||
		snapshot.Namespaces[0].Progress.Phase != api.PhaseExecuting || snapshot.Namespaces[0].Progress.Reason != "InProgress" ||
		snapshot.Namespaces[0].Progress.Message != "workload cutover started" {
		t.Fatalf("alpha snapshot lost identity or progress: %#v", snapshot.Namespaces[0])
	}
	if snapshot.Namespaces[1].UID != "zeta-uid" || !snapshot.Namespaces[1].Terminating ||
		snapshot.Namespaces[1].HasProgress || snapshot.Namespaces[1].Progress.Phase != api.PhasePending {
		t.Fatalf("zeta snapshot lost identity or termination state: %#v", snapshot.Namespaces[1])
	}
	if len(snapshot.NSXWorkflows) != 1 || snapshot.NSXWorkflows[0].WorkflowID != "workflow-1" {
		t.Fatalf("workflow checkpoint was not copied: %#v", snapshot.NSXWorkflows)
	}
}

func TestMalformedProviderErrorsIncludeFieldType(t *testing.T) {
	supervisor := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec": map[string]interface{}{"networking": "not-a-map"},
	}}
	_, err := supervisorNamespaceProvider(supervisor, "tenant")
	if err == nil || !strings.Contains(err.Error(), "invalid spec.networking") || !strings.Contains(err.Error(), "expected map[string]interface{}") {
		t.Fatalf("supervisorNamespaceProvider() error = %v, want invalid spec.networking with underlying type error", err)
	}

	networkSettings := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "netoperator.vmware.com/v1alpha1", "kind": "NetworkSettings",
		"metadata": map[string]interface{}{"name": "default", "namespace": "tenant"},
		"provider": map[string]interface{}{"name": "vsphere-distributed"},
	}}
	c := fake.NewClientBuilder().WithScheme(preflightTestScheme(t)).WithObjects(networkSettings).Build()
	_, err = ReadNetworkSettingsProvider(context.Background(), c, "tenant")
	if err == nil || !strings.Contains(err.Error(), "read NetworkSettings/default provider") || !strings.Contains(err.Error(), "expected string") {
		t.Fatalf("ReadNetworkSettingsProvider() error = %v, want provider path with underlying type error", err)
	}
}

func TestReadNamespaceBatchSnapshotRejectsInvalidBatchState(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		batch *api.NamespaceNetworkTransition
		objs  []client.Object
		want  string
	}{
		{
			name: "nil batch",
			want: "namespace transition is nil",
		},
		{
			name:  "empty batch",
			batch: &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "empty"}},
			want:  `namespace transition "empty" has an empty batch`,
		},
		{
			name:  "duplicate member",
			batch: &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "duplicate"}, Spec: api.NamespaceNetworkTransitionSpec{Namespaces: []string{"same", "same"}}},
			want:  `namespace transition "duplicate" has duplicate member "same"`,
		},
		{
			name: "out of batch status",
			batch: &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-status"},
				Spec:       api.NamespaceNetworkTransitionSpec{Namespaces: []string{"member"}},
				Status:     &api.NamespaceNetworkTransitionStatus{NamespaceStatuses: []api.NamespaceStatus{{Name: "other"}}},
			},
			want: `namespace transition "bad-status" has progress for out-of-batch Namespace "other"`,
		},
		{
			name:  "namespace not found",
			batch: &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "missing"}, Spec: api.NamespaceNetworkTransitionSpec{Namespaces: []string{"missing-ns"}}},
			want:  `read Namespace "missing-ns" in transition "missing"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(tt.objs...).Build()
			_, err := ReadNamespaceBatchSnapshot(ctx, c, tt.batch)
			if err == nil || !contains(err.Error(), tt.want) {
				t.Fatalf("ReadNamespaceBatchSnapshot() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestReadStage1Snapshot(t *testing.T) {
	ctx := context.Background()
	condition := metav1.Condition{Type: api.ConditionInfraTransitionComplete, Status: metav1.ConditionTrue, Reason: "Complete", Message: "Stage 1 complete", ObservedGeneration: 4, LastTransitionTime: metav1.Now()}
	transition := &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: types.UID("stage1-uid"), Generation: 4},
		Status: &api.ControlPlaneNetworkTransitionStatus{
			TransitionStatus: api.TransitionStatus{
				Conditions:       []metav1.Condition{condition},
				Phase:            api.PhaseInfraTransitionComplete,
				LastAction:       api.ActionExecute,
				ActionState:      api.ActionStateSucceeded,
				ActionGeneration: 4,
				NSXWorkflows:     []api.NSXWorkflowStatus{{VDNName: "infra-vdn", WorkflowID: "workflow-2"}},
			},
			TargetNSXProjectPath: "orgs/default/projects/project-1",
			TargetLBProvider:     api.LoadBalancerProvider("NSX_LB"),
			PrimaryVPCPath:       "orgs/default/projects/project-1/vpcs/primary",
			LoadBalancerVPCPath:  "orgs/default/projects/project-1/vpcs/lb",
			LBSNATIPAllocations:  []api.IPAllocationStatus{{IPAddress: "192.0.2.10"}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(transition).Build()

	snapshot, err := ReadStage1Snapshot(ctx, c)
	if err != nil {
		t.Fatalf("ReadStage1Snapshot() error = %v", err)
	}
	if snapshot.TransitionUID != "stage1-uid" || snapshot.Generation != 4 {
		t.Fatalf("unexpected Stage 1 identity: uid=%q generation=%d", snapshot.TransitionUID, snapshot.Generation)
	}
	if snapshot.Status.Phase != api.PhaseInfraTransitionComplete || len(snapshot.Status.Conditions) != 1 || snapshot.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Fatalf("Stage 1 status evidence was not copied: %#v", snapshot.Status)
	}
	if snapshot.TargetNSXProjectPath != "orgs/default/projects/project-1" || snapshot.TargetLBProvider != api.LoadBalancerProvider("NSX_LB") ||
		snapshot.PrimaryVPCPath == "" || snapshot.LoadBalancerVPCPath == "" || len(snapshot.LBSNATAllocations) != 1 {
		t.Fatalf("Stage 1 shared resource references were not copied: %#v", snapshot)
	}

	snapshot.Status.Conditions[0].Message = "mutated snapshot"
	snapshot.Status.NSXWorkflows[0].WorkflowID = "mutated snapshot"
	snapshot.LBSNATAllocations[0].IPAddress = "198.51.100.10"
	if transition.Status.Conditions[0].Message == "mutated snapshot" ||
		transition.Status.NSXWorkflows[0].WorkflowID == "mutated snapshot" ||
		transition.Status.LBSNATIPAllocations[0].IPAddress == "198.51.100.10" {
		t.Fatal("snapshot slices alias the source transition")
	}
}

func TestReadStage1SnapshotRequiresStatus(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(&api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName},
	}).Build()

	_, err := ReadStage1Snapshot(ctx, c)
	if err == nil || !contains(err.Error(), "has no status") {
		t.Fatalf("ReadStage1Snapshot() error = %v, want missing status error", err)
	}
}

func contains(value, substring string) bool {
	return strings.Contains(value, substring)
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("register core API types: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("register migration API types: %v", err)
	}
	return scheme
}
