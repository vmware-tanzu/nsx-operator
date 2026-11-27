/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"strings"
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

	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
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

func TestNamespacePreflightMissingStage1Status(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(&api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName},
	}).Build()
	outcome, err := checkNamespaceBatchPrerequisites(context.Background(), c, &api.NamespaceNetworkTransition{}, nil)
	if err != nil || outcome.ready || outcome.reason != "Stage1Incomplete" {
		t.Fatalf("preflight = %#v, error = %v; want Stage1Incomplete without error", outcome, err)
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

func TestNamespacePreflightRediscoversRetainedBackingAfterCutover(t *testing.T) {
	ctx := context.Background()
	scheme := preflightTestScheme(t)
	member := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: "tenant-uid"}}
	settings := &netop.NetworkSettings{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: member.Name}, Provider: netop.NetworkProviderVSphereDistributed}
	source := &netop.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: member.Name},
		Spec: netop.NetworkSpec{Type: netop.NetworkTypeVDS, ProviderRef: netop.NetworkProviderReference{
			APIGroup: netop.GroupName, Kind: "VSphereDistributedNetwork", Name: "source-vdn",
		}},
	}
	vdn := &netop.VSphereDistributedNetwork{
		ObjectMeta: metav1.ObjectMeta{Name: "source-vdn"},
		Spec:       netop.VSphereDistributedNetworkSpec{PortGroupID: "dvpg-1", IPAssignmentMode: netop.IPAssignmentModeDHCP},
		Status: netop.VSphereDistributedNetworkStatus{DefaultPortConfig: &netop.VSphereDistributedPortConfig{
			Vlan: &netop.VlanSpec{Type: netop.VLANTypeStandard},
		}},
	}
	batch := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch", UID: "batch-uid", Generation: 3},
		Spec: api.NamespaceNetworkTransitionSpec{
			TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{member.Name},
			NetworkConfiguration: &api.NamespaceNetworkConfiguration{NamespaceDHCPNetworks: []api.DHCPNetworkConfiguration{{
				VDNName: vdn.Name, SubnetMask: "255.255.255.0", GatewayAddress: "192.0.2.1",
				DHCPRange: &api.IPAddressRange{Start: "192.0.2.10", End: "192.0.2.20"},
			}}},
		},
		Status: &api.NamespaceNetworkTransitionStatus{
			TransitionStatus:  api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateRunning, ActionGeneration: 3, ObservedGeneration: 3},
			NamespaceStatuses: []api.NamespaceStatus{{Name: member.Name, Phase: api.PhaseExecuting}},
		},
	}
	desired := newSupervisorNamespace(member.Name, true)
	deps := preflightDependencyObjects(false)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		batch, member, settings, source, vdn, desired, readyStage1Transition(), deps[0], deps[2],
	).Build()
	discoverer := topology.InspectionDiscoverer{
		Service: inspection.Service{
			Inventory:       inspection.KubernetesInventory{Reader: c, Topology: preflightPortGroupReader{}},
			TargetInventory: preflightTargetInventory{},
		},
		Graph: preflightGraphFunc(func(_ context.Context, _ *api.NamespaceNetworkTransition, networks []inspection.Network) ([]topology.ResourceIdentity, []topology.ConsumerIdentity, error) {
			if len(networks) != 1 || networks[0].Source.VDNName != vdn.Name || len(networks[0].Source.Topologies) != 1 || networks[0].Source.Topologies[0].PortGroupID != vdn.Spec.PortGroupID {
				t.Fatalf("wrong source topology: %+v", networks)
			}
			return nil, nil, nil
		}),
	}
	checkExecute := func() {
		t.Helper()
		outcome, err := checkNamespaceBatchPrerequisites(ctx, c, batch, discoverer)
		if err != nil || !outcome.ready || len(outcome.topologySnapshot.Resources) != 1 || outcome.topologySnapshot.Resources[0].Key != "vc:dvpg-1" {
			t.Fatalf("Execute preflight = %+v, err = %v", outcome, err)
		}
	}
	checkExecute()
	target := &netop.Network{ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: member.Name}, Spec: netop.NetworkSpec{Type: netop.NetworkTypeNSXTVPC}}
	if err := c.Create(ctx, target); err != nil {
		t.Fatal(err)
	}
	checkExecute()
	settings.Provider, settings.LegacyProvider = netop.NetworkProviderVPC, netop.NetworkProviderVSphereDistributed
	if err := c.Update(ctx, settings); err != nil {
		t.Fatal(err)
	}
	checkExecute()
	batch.Spec.Action = api.ActionRollback
	batch.Generation++
	discoverer.Service.TargetInventory = nil
	outcome, err := checkNamespaceRollbackPrerequisites(ctx, c, batch, discoverer)
	if err != nil || !outcome.ready {
		t.Fatalf("Rollback preflight = %+v, err = %v", outcome, err)
	}
	if err := c.Delete(ctx, vdn); err != nil {
		t.Fatal(err)
	}
	_, err = checkNamespaceRollbackPrerequisites(ctx, c, batch, discoverer)
	if err == nil || !strings.Contains(err.Error(), "resolve source VDN source-vdn") {
		t.Fatalf("missing VDN backing error = %v", err)
	}
	// Batch-wide workflow names cannot prove the original member's backing.
	batch.Status.NSXWorkflows = []api.NSXWorkflowStatus{{VDNName: vdn.Name, WorkflowID: "workflow"}}
	if err := c.Delete(ctx, source); err != nil {
		t.Fatal(err)
	}
	_, err = checkNamespaceRollbackPrerequisites(ctx, c, batch, discoverer)
	if err == nil || !strings.Contains(err.Error(), "namespace tenant has no source Networks") {
		t.Fatalf("missing retained source error = %v", err)
	}
}

type preflightPortGroupReader struct{}

func (preflightPortGroupReader) ResolvePortGroup(_ context.Context, id string) (inspection.Topology, error) {
	return inspection.Topology{VCenterUUID: "vc", PortGroupID: id, SwitchID: "switch", SwitchUUID: "switch-uuid", ClusterIDs: []string{"cluster"}, ZoneIDs: []string{"zone"}}, nil
}

type preflightTargetInventory struct{}

func (preflightTargetInventory) ListTopology(context.Context) ([]inspection.TargetTopology, error) {
	return nil, nil
}

type preflightGraphFunc func(context.Context, *api.NamespaceNetworkTransition, []inspection.Network) ([]topology.ResourceIdentity, []topology.ConsumerIdentity, error)

func (f preflightGraphFunc) DiscoverShared(ctx context.Context, batch *api.NamespaceNetworkTransition, networks []inspection.Network) ([]topology.ResourceIdentity, []topology.ConsumerIdentity, error) {
	return f(ctx, batch, networks)
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
	if !outcome.ready {
		t.Fatalf("read-only Evaluate blocked on shared DVPG: reason=%q", outcome.reason)
	}
}

func TestSharedVDNOnlyConflictsWhenResourcesAreMutated(t *testing.T) {
	current := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "current"}, Spec: api.NamespaceNetworkTransitionSpec{
		TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"a"},
		NetworkConfiguration: &api.NamespaceNetworkConfiguration{NamespaceDHCPNetworks: []api.DHCPNetworkConfiguration{{VDNName: "shared"}}},
	}}
	other := &api.NamespaceNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: "other"}, Spec: api.NamespaceNetworkTransitionSpec{
		TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"b"},
	}, Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionExecute, ActionState: api.ActionStateRunning, NSXWorkflows: []api.NSXWorkflowStatus{{VDNName: "shared"}}}}}
	c := fake.NewClientBuilder().WithScheme(preflightTestScheme(t)).WithObjects(current, other).Build()
	dvpg := topology.ResourceIdentity{Kind: topology.ResourceDVPG, Key: "vc:dvpg"}
	for _, tc := range []struct {
		name         string
		resources    []topology.ResourceIdentity
		wantConflict bool
	}{
		{"reuse", nil, false}, {"modify", []topology.ResourceIdentity{dvpg}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			discoverer := &topologyDiscovererFunc{fn: func(context.Context, *api.NamespaceNetworkTransition) (topology.BatchSnapshot, error) {
				return topology.BatchSnapshot{Complete: true, Resources: []topology.ResourceIdentity{dvpg}}, nil
			}}
			conflicts, err := findActiveNamespaceBatchConflicts(context.Background(), c, current, topology.BatchSnapshot{Complete: true, Resources: tc.resources}, discoverer)
			if err != nil {
				t.Fatal(err)
			}
			if (len(conflicts) != 0) != tc.wantConflict {
				t.Fatalf("conflicts = %v, want conflict=%t", conflicts, tc.wantConflict)
			}
		})
	}
}

func TestPendingExecuteConflictPolicy(t *testing.T) {
	tests := []struct {
		name          string
		currentAction api.Action
		currentName   string
		mutate        func(*api.NamespaceNetworkTransition)
		wantConflict  bool
	}{
		{name: "Rollback can pass a pending Execute", currentAction: api.ActionRollback},
		{name: "earlier pending Execute wins arbitration", currentAction: api.ActionExecute, wantConflict: false},
		{name: "later pending Execute waits for the winner", currentAction: api.ActionExecute, currentName: "z-current", wantConflict: true},
		{name: "Evaluate still waits for a pending Execute", currentAction: api.ActionEvaluate, wantConflict: true},
		{name: "missing status is not proof of pending Execute", currentAction: api.ActionRollback, wantConflict: true, mutate: func(b *api.NamespaceNetworkTransition) { b.Status = nil }},
		{name: "failed Evaluate is not proof of pending Execute", currentAction: api.ActionRollback, wantConflict: true, mutate: func(b *api.NamespaceNetworkTransition) { b.Status.ActionState = api.ActionStateFailed }},
		{name: "Execute checkpoint remains protected", currentAction: api.ActionRollback, wantConflict: true, mutate: func(b *api.NamespaceNetworkTransition) {
			b.Status.LastAction = api.ActionExecute
			b.Status.ActionState = api.ActionStateRunning
			b.Status.ActionGeneration = b.Generation
			b.Status.NSXWorkflows = []api.NSXWorkflowStatus{{VDNName: "network", WorkflowID: "workflow"}}
		}},
		{name: "completed Execute remains protected", currentAction: api.ActionRollback, wantConflict: true, mutate: func(b *api.NamespaceNetworkTransition) { b.Status.LastAction = api.ActionExecute }},
		{name: "Rollback checkpoint remains protected", currentAction: api.ActionRollback, wantConflict: true, mutate: func(b *api.NamespaceNetworkTransition) {
			b.Spec.Action = api.ActionRollback
			b.Status.LastAction = api.ActionRollback
			b.Status.ActionState = api.ActionStateRunning
		}},
		{name: "Commit checkpoint remains protected", currentAction: api.ActionRollback, wantConflict: true, mutate: func(b *api.NamespaceNetworkTransition) {
			b.Spec.Action = api.ActionCommit
			b.Status.LastAction = api.ActionCommit
			b.Status.ActionState = api.ActionStateRunning
		}},
		{name: "validation workflow does not imply Execute started", currentAction: api.ActionRollback, mutate: func(b *api.NamespaceNetworkTransition) {
			b.Status.NSXWorkflows = []api.NSXWorkflowStatus{{VDNName: "network", WorkflowID: "workflow"}}
		}},
		{name: "namespace mutation evidence remains protected", currentAction: api.ActionRollback, wantConflict: true, mutate: func(b *api.NamespaceNetworkTransition) {
			b.Status.NamespaceStatuses = []api.NamespaceStatus{{Name: "tenant", Phase: api.PhaseExecuting}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentName := tt.currentName
			if currentName == "" {
				currentName = "current"
			}
			current := &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: currentName, UID: "current-uid", Generation: 3},
				Spec: api.NamespaceNetworkTransitionSpec{
					TransitionSpec: api.TransitionSpec{Action: tt.currentAction}, Namespaces: []string{"tenant"},
				},
			}
			if tt.currentAction == api.ActionExecute {
				current.Status = &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
					LastAction: api.ActionEvaluate, ActionState: api.ActionStateSucceeded,
					ActionGeneration: 2, ObservedGeneration: 3, Phase: api.PhaseBlocked,
				}}
			}
			other := &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "other", UID: "other-uid", Generation: 3},
				Spec: api.NamespaceNetworkTransitionSpec{
					TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant"},
				},
				Status: &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
					LastAction: api.ActionEvaluate, ActionState: api.ActionStateSucceeded,
					ActionGeneration: 2, ObservedGeneration: 3, Phase: api.PhaseBlocked,
				}},
			}
			if tt.mutate != nil {
				tt.mutate(other)
			}
			c := fake.NewClientBuilder().WithScheme(preflightTestScheme(t)).WithObjects(current, other).Build()
			conflicts, err := findActiveNamespaceBatchConflicts(context.Background(), c, current, topology.BatchSnapshot{Complete: true}, emptyTopologyDiscoverer())
			if err != nil {
				t.Fatal(err)
			}
			if (len(conflicts) != 0) != tt.wantConflict {
				t.Fatalf("conflicts = %v, want conflict=%t", conflicts, tt.wantConflict)
			}
		})
	}
}

func TestRollbackMakesProgressWhileSharedDVPGExecuteWaits(t *testing.T) {
	ctx := context.Background()
	a := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch-a", UID: "uid-a", Generation: 3},
		Spec: api.NamespaceNetworkTransitionSpec{
			TransitionSpec: api.TransitionSpec{Action: api.ActionRollback}, Namespaces: []string{"tenant-a"},
		},
		Status: &api.NamespaceNetworkTransitionStatus{
			TransitionStatus: api.TransitionStatus{
				LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded,
				ActionGeneration: 2, ObservedGeneration: 2, Phase: api.PhaseCompleted,
			},
			NamespaceStatuses: []api.NamespaceStatus{{Name: "tenant-a", Phase: api.PhaseCompleted}},
		},
	}
	b := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "batch-b", UID: "uid-b", Generation: 3},
		Spec: api.NamespaceNetworkTransitionSpec{
			TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant-b"},
		},
		Status: &api.NamespaceNetworkTransitionStatus{
			TransitionStatus: api.TransitionStatus{
				LastAction: api.ActionEvaluate, ActionState: api.ActionStateSucceeded,
				ActionGeneration: 2, ObservedGeneration: 2, Phase: api.PhaseEvaluated,
				NSXWorkflows: []api.NSXWorkflowStatus{{VDNName: "batch-b-vdn", WorkflowID: "validation-workflow-b"}},
			},
			NamespaceStatuses: []api.NamespaceStatus{{Name: "tenant-b", Phase: api.PhaseEvaluated}},
		},
	}
	objects := []client.Object{a, b, readyStage1Transition()}
	objects = append(objects, preflightDependencyObjects(false)...)
	for _, name := range []string{"tenant-a", "tenant-b"} {
		objects = append(objects,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid")}},
			newSupervisorNamespace(name, false),
			&unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "netoperator.vmware.com/v1alpha1", "kind": "NetworkSettings",
				"metadata": map[string]interface{}{"name": "default", "namespace": name},
				"provider": "vsphere-distributed",
			}},
		)
	}
	c := fake.NewClientBuilder().WithScheme(preflightTestScheme(t)).
		WithStatusSubresource(&api.NamespaceNetworkTransition{}).WithObjects(objects...).Build()
	discoverer := &topologyDiscovererFunc{fn: func(_ context.Context, batch *api.NamespaceNetworkTransition) (topology.BatchSnapshot, error) {
		return topology.BatchSnapshot{Complete: true, Resources: []topology.ResourceIdentity{{
			Kind: topology.ResourceDVPG, Key: "vcenter-uuid:shared-dvpg", VDNName: batch.Name + "-vdn",
		}}}, nil
	}}
	rollbackCalls, executeCalls := 0, 0
	engine := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{
		{Stage: transition.Namespaces, Action: api.ActionRollback}: actionHandlerFunc(func(context.Context, transition.Request) (transition.Result, error) {
			rollbackCalls++
			return transition.Result{Outcome: transition.OutcomeSucceeded}, nil
		}),
		{Stage: transition.Namespaces, Action: api.ActionExecute}: actionHandlerFunc(func(context.Context, transition.Request) (transition.Result, error) {
			executeCalls++
			return transition.Result{Outcome: transition.OutcomeInProgress, RequeueAfter: time.Second}, nil
		}),
	}}
	r := &Reconciler{Client: c, APIReader: c, Engine: engine, Stage: transition.Namespaces, TopologyDiscoverer: discoverer}
	reconcile := func(name string) *api.NamespaceNetworkTransition {
		t.Helper()
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}); err != nil {
			t.Fatalf("reconcile %s: %v", name, err)
		}
		after := &api.NamespaceNetworkTransition{}
		if err := c.Get(ctx, client.ObjectKey{Name: name}, after); err != nil {
			t.Fatal(err)
		}
		return after
	}
	assertWaiting := func() {
		t.Helper()
		after := reconcile(b.Name)
		condition := findCondition(after.Status.Conditions, api.ConditionBatchPreflight)
		if condition == nil || condition.Reason != "ConflictingBatch" || after.Status.LastAction != api.ActionEvaluate || executeCalls != 0 {
			t.Fatalf("pending Execute must keep waiting without an Execute checkpoint: status=%+v, handler calls=%d", after.Status, executeCalls)
		}
	}
	assertWaiting()
	afterA := reconcile(a.Name)
	if afterA.Status.LastAction != api.ActionRollback || afterA.Status.ActionState != api.ActionStateRunning {
		t.Fatalf("Rollback must checkpoint despite the waiting Execute: %+v", afterA.Status)
	}
	assertWaiting()
	afterA = reconcile(a.Name)
	if rollbackCalls != 1 || !afterA.Status.RollbackComplete || afterA.Status.ActionState != api.ActionStateSucceeded {
		t.Fatalf("Rollback did not finish: status=%+v, handler calls=%d", afterA.Status, rollbackCalls)
	}
	afterB := reconcile(b.Name)
	if afterB.Status.LastAction != api.ActionExecute || afterB.Status.ActionState != api.ActionStateRunning || executeCalls != 0 {
		t.Fatalf("Execute must checkpoint after Rollback releases the DVPG: %+v", afterB.Status)
	}
	if len(afterB.Status.NSXWorkflows) != 1 || afterB.Status.NSXWorkflows[0] != b.Status.NSXWorkflows[0] {
		t.Fatalf("pending Execute lost its validation workflow reference: %+v", afterB.Status.NSXWorkflows)
	}
	reconcile(b.Name)
	if executeCalls != 1 {
		t.Fatalf("Execute handler calls = %d, want 1 after Rollback", executeCalls)
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
	if err := netop.AddToScheme(scheme); err != nil {
		t.Fatalf("register Net Operator API types: %v", err)
	}
	for _, entry := range []struct{ group, version, kind string }{
		{group: "iaas.vmware.com", version: "v1alpha2", kind: "SupervisorNamespace"},
		{group: "iaas.vmware.com", version: "v1alpha1", kind: "SupervisorNamespace"},
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
