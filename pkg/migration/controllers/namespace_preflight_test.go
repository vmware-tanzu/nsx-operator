/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/topology"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

func TestReconcileNamespacePreflight(t *testing.T) {
	tests := []struct {
		name                string
		stage1Ready         bool
		namespaceGone       bool
		terminating         bool
		conflictingCR       bool
		topologyUnavailable bool
		action              api.Action
		initialStatus       *api.NamespaceNetworkTransitionStatus
		partialExecute      bool
		wantStatus          metav1.ConditionStatus
		wantReason          string
	}{
		{name: "known prerequisites pass", stage1Ready: true, wantStatus: metav1.ConditionTrue, wantReason: "KnownChecksPassed"},
		{
			name: "execute retry preserves progress checkpoint", stage1Ready: true, action: api.ActionExecute,
			initialStatus: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
				Phase: api.PhaseExecuting, LastAction: api.ActionExecute, ActionState: api.ActionStateRunning,
				ActionGeneration: 2, ObservedGeneration: 2,
			}, NamespaceStatuses: []api.NamespaceStatus{{Name: "tenant", Phase: api.PhaseExecuting}}}, partialExecute: true,
			wantStatus: metav1.ConditionTrue, wantReason: "KnownChecksPassed",
		},
		{name: "stage 1 incomplete", wantStatus: metav1.ConditionFalse, wantReason: "Stage1Incomplete"},
		{name: "namespace missing", stage1Ready: true, namespaceGone: true, wantStatus: metav1.ConditionFalse, wantReason: "NamespaceMissing"},
		{name: "namespace terminating", stage1Ready: true, terminating: true, wantStatus: metav1.ConditionFalse, wantReason: "NamespaceTerminating"},
		{name: "overlapping execute is unresolved", stage1Ready: true, conflictingCR: true, wantStatus: metav1.ConditionFalse, wantReason: "ConflictingBatch"},
		{name: "topology discovery unavailable", stage1Ready: true, topologyUnavailable: true, wantStatus: metav1.ConditionFalse, wantReason: "TopologyDiscoveryUnavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			action := tt.action
			if action == "" {
				action = api.ActionEvaluate
			}
			batch := &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "current", UID: types.UID("current-uid"), Generation: 2},
				Spec: api.NamespaceNetworkTransitionSpec{
					TransitionSpec: api.TransitionSpec{Action: action},
					Namespaces:     []string{"tenant"},
				},
				Status: tt.initialStatus,
			}
			objects := []client.Object{batch}
			if tt.stage1Ready {
				objects = append(objects, readyStage1Transition())
				objects = append(objects, preflightDependencyObjects(tt.partialExecute)...)
			} else {
				objects = append(objects, &api.ControlPlaneNetworkTransition{
					ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: "stage1-uid", Generation: 4},
					Status:     &api.ControlPlaneNetworkTransitionStatus{},
				})
			}
			if !tt.namespaceGone {
				namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: "tenant-uid"}}
				if tt.terminating {
					namespace.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
					namespace.Finalizers = []string{"test-finalizer"}
				}
				objects = append(objects, namespace)
			}
			if tt.conflictingCR {
				objects = append(objects, &api.NamespaceNetworkTransition{
					ObjectMeta: metav1.ObjectMeta{Name: "other", UID: "other-uid", Generation: 1},
					Spec: api.NamespaceNetworkTransitionSpec{
						TransitionSpec: api.TransitionSpec{Action: api.ActionExecute},
						Namespaces:     []string{"tenant"},
					},
					Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
						LastAction: api.ActionExecute, ActionState: api.ActionStateRunning,
						ActionGeneration: 1, ObservedGeneration: 1, Phase: api.PhaseExecuting,
					}},
				})
			}

			scheme := preflightTestScheme(t)
			c := fake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(&api.NamespaceNetworkTransition{}).
				WithObjects(objects...).Build()
			handlerCalls := 0
			var handledRequest transition.Request
			engine := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
				{Stage: transition.Namespaces, Action: api.ActionEvaluate}: actionHandlerFunc(func(_ context.Context, req transition.Request) (transition.Result, error) {
					handlerCalls++
					handledRequest = req
					return transition.Result{Outcome: transition.OutcomeInProgress, RequeueAfter: time.Second}, nil
				}),
				{Stage: transition.Namespaces, Action: api.ActionExecute}: actionHandlerFunc(func(_ context.Context, req transition.Request) (transition.Result, error) {
					handlerCalls++
					handledRequest = req
					return transition.Result{Outcome: transition.OutcomeInProgress, RequeueAfter: time.Second}, nil
				}),
			}}
			discoverer := topology.Discoverer(emptyTopologyDiscoverer())
			if tt.topologyUnavailable {
				discoverer = nil
			}
			r := &Reconciler{Client: c, APIReader: c, Engine: engine, Stage: transition.Namespaces, TopologyDiscoverer: discoverer}
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: batch.Name}})
			if err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			// A fresh Evaluate first persists its action checkpoint. The next
			// reconcile dispatches the handler with the preflight snapshot.
			if tt.wantStatus == metav1.ConditionTrue && action == api.ActionEvaluate {
				result, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: batch.Name}})
				if err != nil {
					t.Fatalf("second Reconcile() error = %v", err)
				}
			}
			if tt.wantStatus == metav1.ConditionFalse && result.RequeueAfter != namespacePreflightRequeue {
				t.Fatalf("blocked Reconcile() requeue = %s, want %s", result.RequeueAfter, namespacePreflightRequeue)
			}
			if tt.wantStatus == metav1.ConditionTrue && result.RequeueAfter <= 0 {
				t.Fatalf("ready Reconcile() requeue = %s, want a handler requeue", result.RequeueAfter)
			}

			after := &api.NamespaceNetworkTransition{}
			if err := c.Get(ctx, client.ObjectKey{Name: batch.Name}, after); err != nil {
				t.Fatalf("read transition after reconcile: %v", err)
			}
			if after.Status == nil {
				t.Fatal("reconcile did not persist a preflight status")
			}
			if !tt.stage1Ready {
				outcome, err := checkNamespaceBatchPrerequisites(ctx, c, batch, emptyTopologyDiscoverer())
				if err != nil {
					t.Fatalf("checkNamespaceBatchPrerequisites() error = %v", err)
				}
				if outcome.reason != tt.wantReason {
					t.Fatalf("preflight reason = %q, want %q", outcome.reason, tt.wantReason)
				}
				if got := meta.FindStatusCondition(after.Status.Conditions, api.ConditionReady); got == nil || got.Reason != "InfrastructureNotReady" {
					t.Fatalf("controller gate = %#v, want InfrastructureNotReady", got)
				}
				return
			}
			condition := findCondition(after.Status.Conditions, api.ConditionBatchPreflight)
			if condition == nil || condition.Status != tt.wantStatus || condition.Reason != tt.wantReason {
				t.Fatalf("preflight condition = %#v, want status=%s reason=%s", condition, tt.wantStatus, tt.wantReason)
			}
			if tt.wantStatus == metav1.ConditionFalse && after.Status.Phase != api.PhaseBlocked {
				t.Fatalf("blocked preflight phase = %q, want %q", after.Status.Phase, api.PhaseBlocked)
			}
			if tt.wantStatus == metav1.ConditionTrue {
				if handledRequest.NamespaceTopology == nil || !handledRequest.NamespaceTopology.Complete {
					t.Fatalf("handler NamespaceTopology = %#v, want complete preflight snapshot", handledRequest.NamespaceTopology)
				}
				if handledRequest.NamespaceTopologyDiscoverer != discoverer {
					t.Fatalf("handler NamespaceTopologyDiscoverer = %T, want injected %T", handledRequest.NamespaceTopologyDiscoverer, discoverer)
				}
			}
			if tt.wantStatus == metav1.ConditionTrue && after.Status.LastAction != action {
				t.Fatalf("ready preflight did not pass control to the action reconciler: lastAction = %q", after.Status.LastAction)
			}
			if tt.wantStatus == metav1.ConditionFalse && handlerCalls != 0 {
				t.Fatalf("blocked preflight dispatched handler %d times", handlerCalls)
			}
			if tt.wantStatus == metav1.ConditionTrue && action == api.ActionExecute && tt.initialStatus != nil && handlerCalls != 1 {
				t.Fatalf("ready Execute retry handler calls = %d, want 1", handlerCalls)
			}
			if tt.wantStatus == metav1.ConditionFalse && tt.initialStatus == nil && (after.Status.LastAction != "" || after.Status.ActionState != "" || after.Status.ActionGeneration != 0) {
				t.Fatalf("preflight falsely advanced the action checkpoint: %#v", after.Status.TransitionStatus)
			}
			if tt.initialStatus != nil && (after.Status.LastAction != tt.initialStatus.LastAction || after.Status.ActionState != tt.initialStatus.ActionState || after.Status.ActionGeneration != tt.initialStatus.ActionGeneration) {
				t.Fatalf("preflight changed the action checkpoint: %#v", after.Status.TransitionStatus)
			}
		})
	}
}

func TestHasUnresolvedMutation(t *testing.T) {
	tests := []struct {
		name string
		obj  *api.NamespaceNetworkTransition
		want bool
	}{
		{
			name: "new execute request",
			obj: &api.NamespaceNetworkTransition{Spec: api.NamespaceNetworkTransitionSpec{
				TransitionSpec: api.TransitionSpec{Action: api.ActionExecute},
			}},
			want: true,
		},
		{
			name: "verified rollback released",
			obj: &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}},
				Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
					LastAction: api.ActionRollback, ActionState: api.ActionStateSucceeded,
					ActionGeneration: 1, ObservedGeneration: 2, Phase: api.PhaseRolledBack,
				}},
			},
			want: false,
		},
		{
			name: "new execute after rollback is active",
			obj: &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}},
				Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
					LastAction: api.ActionRollback, ActionState: api.ActionStateSucceeded,
					ActionGeneration: 1, ObservedGeneration: 2, Phase: api.PhaseRolledBack,
				}},
			},
			want: true,
		},
		{
			name: "execute remains unresolved after success",
			obj: &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}},
				Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
					LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded,
					ActionGeneration: 1, ObservedGeneration: 1, Phase: api.PhaseCompleted,
				}},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasUnresolvedMutation(tt.obj); got != tt.want {
				t.Fatalf("hasUnresolvedMutation() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestCoexistenceModeRequiresWCPNetworkProvider(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		configMap bool
	}{
		{name: "registered", provider: "nsx-vpc", configMap: true},
		{name: "legacy provider", provider: "nsx", configMap: true},
		{name: "registration missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batch := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "batch"}, Spec: api.NamespaceNetworkTransitionSpec{
				TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}, Namespaces: []string{"tenant"},
			}}
			objects := []client.Object{readyStage1Transition(), batch}
			if tt.configMap {
				objects = append(objects, &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "supervisor-properties", Namespace: "vmware-system-vks-public"},
					Data:       map[string]string{"networkProvider": tt.provider},
				})
			}
			c := fake.NewClientBuilder().WithScheme(preflightTestScheme(t)).WithObjects(objects...).Build()
			outcome, err := checkNamespaceBatchPrerequisites(context.Background(), c, batch, emptyTopologyDiscoverer())
			if err != nil {
				t.Fatalf("checkNamespaceBatchPrerequisites() error = %v", err)
			}
			if tt.provider == "nsx-vpc" {
				if outcome.reason == "CoexistenceModeNotEnabled" {
					t.Fatalf("registered WCP networkProvider was rejected: %s", outcome.message)
				}
				return
			}
			if outcome.reason != "CoexistenceModeNotEnabled" {
				t.Fatalf("preflight reason = %q, want CoexistenceModeNotEnabled", outcome.reason)
			}
		})
	}
}

func TestRollbackPreflightAllowsProviderRestorePropagation(t *testing.T) {
	tests := []struct {
		name           string
		withCheckpoint bool
		wantReady      bool
		wantReason     string
	}{
		{name: "desired VDS while effective provider is still migrated VPC", withCheckpoint: true, wantReady: true, wantReason: "RollbackDependenciesPassed"},
		{name: "reject intermediate state without namespace checkpoint", wantReason: "NamespaceProgressCheckpointMissing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batch := &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "rollback-batch", UID: "rollback-uid", Generation: 3},
				Spec: api.NamespaceNetworkTransitionSpec{
					TransitionSpec: api.TransitionSpec{Action: api.ActionRollback}, Namespaces: []string{"tenant"},
				},
				Status: &api.NamespaceNetworkTransitionStatus{NamespaceStatuses: nil},
			}
			if tt.withCheckpoint {
				batch.Status.NamespaceStatuses = []api.NamespaceStatus{{Name: "tenant", Phase: api.PhaseRollingBack}}
			}
			networkSettings := &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "netoperator.vmware.com/v1alpha1", "kind": "NetworkSettings",
				"metadata": map[string]interface{}{"name": "default", "namespace": "tenant"},
				"provider": "vpc", "legacyProvider": "vsphere-distributed",
			}}
			objects := []client.Object{
				batch,
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: "tenant-uid"}},
				newSupervisorNamespace("tenant", false),
				networkSettings,
			}
			c := fake.NewClientBuilder().WithScheme(preflightTestScheme(t)).WithObjects(objects...).Build()
			outcome, err := checkNamespaceRollbackPrerequisites(context.Background(), c, batch, emptyTopologyDiscoverer())
			if err != nil {
				t.Fatalf("checkNamespaceRollbackPrerequisites() error = %v", err)
			}
			if outcome.ready != tt.wantReady || outcome.reason != tt.wantReason {
				t.Fatalf("rollback preflight = (ready=%t, reason=%q), want (ready=%t, reason=%q)", outcome.ready, outcome.reason, tt.wantReady, tt.wantReason)
			}
		})
	}
}

func TestFindActiveNamespaceBatchConflictsOnSharedDVPG(t *testing.T) {
	current := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch-a", UID: "uid-a", Generation: 1},
		Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}, Namespaces: []string{"tenant-a"}},
	}
	other := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch-b", UID: "uid-b", Generation: 1},
		Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant-b"}},
		Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
			LastAction: api.ActionExecute, ActionState: api.ActionStateRunning, ActionGeneration: 1,
		}},
	}
	objects := []client.Object{
		readyStage1Transition(), current, other,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant-a", UID: "tenant-a-uid"}},
		newSupervisorNamespace("tenant-a", false),
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "netoperator.vmware.com/v1alpha1", "kind": "NetworkSettings",
			"metadata": map[string]interface{}{"name": "default", "namespace": "tenant-a"},
			"provider": "vsphere-distributed",
		}},
	}
	objects = append(objects, preflightDependencyObjects(false)...)
	c := fake.NewClientBuilder().WithScheme(preflightTestScheme(t)).WithObjects(objects...).Build()
	discoverer := &topologyDiscovererFunc{fn: func(_ context.Context, batch *api.NamespaceNetworkTransition) (topology.BatchSnapshot, error) {
		name := "network-a"
		if batch.Name == other.Name {
			name = "network-b"
		}
		return topology.BatchSnapshot{Complete: true, Resources: []topology.ResourceIdentity{{
			Kind: topology.ResourceDVPG, Key: "vcenter-uuid:dvpg-moid", VDNName: name,
		}}}, nil
	}}
	outcome, err := checkNamespaceBatchPrerequisites(context.Background(), c, current, discoverer)
	if err != nil {
		t.Fatalf("checkNamespaceBatchPrerequisites() error = %v", err)
	}
	if outcome.ready || outcome.reason != "ConflictingBatch" {
		t.Fatalf("preflight = (ready=%t, reason=%q), want blocked with ConflictingBatch", outcome.ready, outcome.reason)
	}
}

type topologyDiscovererFunc struct {
	fn func(context.Context, *api.NamespaceNetworkTransition) (topology.BatchSnapshot, error)
}

func (f *topologyDiscovererFunc) DiscoverBatch(ctx context.Context, batch *api.NamespaceNetworkTransition) (topology.BatchSnapshot, error) {
	return f.fn(ctx, batch)
}

func emptyTopologyDiscoverer() topology.Discoverer {
	return &topologyDiscovererFunc{fn: func(context.Context, *api.NamespaceNetworkTransition) (topology.BatchSnapshot, error) {
		return topology.BatchSnapshot{Complete: true}, nil
	}}
}

func TestStage1ReadinessAllowsPendingFinalize(t *testing.T) {
	snapshot := &Stage1Snapshot{
		Generation:           5,
		RequestedAction:      api.ActionFinalize,
		TargetNSXProjectPath: "orgs/default/projects/project-1",
		TargetLBProvider:     api.LoadBalancerProvider("NSX_LB"),
		PrimaryVPCPath:       "orgs/default/projects/project-1/vpcs/primary",
		LoadBalancerVPCPath:  "orgs/default/projects/project-1/vpcs/lb",
		Status: api.TransitionStatus{
			LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded,
			ActionGeneration: 4, ObservedGeneration: 5,
			IrreversibleAction: "",
			Conditions: []metav1.Condition{{
				Type: api.ConditionInfraTransitionComplete, Status: metav1.ConditionTrue,
				ObservedGeneration: 4,
			}},
		},
	}
	if reason, _ := stage1Readiness(snapshot); reason != "" {
		t.Fatalf("pending Finalize was rejected with reason %q", reason)
	}

	snapshot.Status.IrreversibleAction = string(api.ActionFinalize)
	if reason, _ := stage1Readiness(snapshot); reason != "Stage1CheckpointStale" {
		t.Fatalf("Finalize with an irreversible checkpoint was accepted, reason=%q", reason)
	}
}

func readyStage1Transition() *api.ControlPlaneNetworkTransition {
	return &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: "stage1-uid", Generation: 4},
		Status: &api.ControlPlaneNetworkTransitionStatus{
			TransitionStatus: api.TransitionStatus{
				Conditions: []metav1.Condition{{
					Type: api.ConditionInfraTransitionComplete, Status: metav1.ConditionTrue,
					Reason: "Complete", Message: "done", ObservedGeneration: 4,
				}},
				Phase: api.PhaseInfraTransitionComplete, LastAction: api.ActionExecute,
				ActionState: api.ActionStateSucceeded, ActionGeneration: 4, ObservedGeneration: 4,
			},
			TargetNSXProjectPath: "orgs/default/projects/project-1",
			TargetLBProvider:     api.LoadBalancerProvider("NSX_LB"),
			PrimaryVPCPath:       "orgs/default/projects/project-1/vpcs/primary",
			LoadBalancerVPCPath:  "orgs/default/projects/project-1/vpcs/lb",
		},
	}
}

func preflightTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("register core API types: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("register migration API types: %v", err)
	}
	for _, entry := range []struct{ group, version, kind string }{
		{group: "iaas.vmware.com", version: "v1alpha2", kind: "SupervisorNamespace"},
		{group: "iaas.vmware.com", version: "v1alpha1", kind: "SupervisorNamespace"},
		{group: "netoperator.vmware.com", version: "v1alpha1", kind: "NetworkSettings"},
	} {
		gvk := schema.GroupVersionKind{Group: entry.group, Version: entry.version, Kind: entry.kind}
		scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		metav1.AddToGroupVersion(scheme, schema.GroupVersion{Group: entry.group, Version: entry.version})
	}
	return scheme
}

func preflightDependencyObjects(partialExecute bool) []client.Object {
	tenantVPC := partialExecute
	return []client.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "vmware-system-net-staging"}},
		newSupervisorNamespace("tenant", tenantVPC),
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "supervisor-properties", Namespace: "vmware-system-vks-public"}, Data: map[string]string{"networkProvider": "nsx-vpc"}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "netoperator.vmware.com/v1alpha1", "kind": "NetworkSettings",
			"metadata": map[string]interface{}{"name": "default", "namespace": "tenant"},
			"provider": "vsphere-distributed",
		}},
	}
}

func newSupervisorNamespace(name string, vpc bool) *unstructured.Unstructured {
	providerConfig := "vsphereNetworkConfig"
	if vpc {
		providerConfig = "vpcNetworkConfig"
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "iaas.vmware.com/v1alpha2", "kind": "SupervisorNamespace",
		"metadata": map[string]interface{}{"name": name},
		"spec":     map[string]interface{}{"networking": map[string]interface{}{providerConfig: map[string]interface{}{}}},
	}}
}
