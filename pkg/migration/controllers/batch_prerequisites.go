/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

// NamespaceBatchSnapshot contains live Kubernetes identity and the persisted
// progress associated with one immutable NamespaceNetworkTransition batch.
// NSXWorkflows are only the workflow handles persisted in this CR; callers must
// resolve those handles to live workflow state before recovery decisions. This
// snapshot is read-only and must be refreshed before external writes.
type NamespaceBatchSnapshot struct {
	TransitionUID      types.UID
	Generation         int64
	RequestedAction    api.Action
	Phase              string
	LastAction         api.Action
	ActionState        api.ActionState
	ActionGeneration   int64
	ObservedGeneration int64
	IrreversibleAction string
	Namespaces         []NamespaceSnapshot
	NSXWorkflows       []api.NSXWorkflowStatus
}

// NamespaceSnapshot records the live Kubernetes Namespace identity. A
// terminating Namespace is returned so action-specific policy can distinguish
// fresh Evaluate from recovery; it is not silently treated as absent.
type NamespaceSnapshot struct {
	Name              string
	UID               types.UID
	Terminating       bool
	DesiredProvider   string
	EffectiveProvider string
	LegacyProvider    string
	Progress          api.NamespaceStatus
	HasProgress       bool
}

// Stage1Snapshot contains the currently persisted Stage 1 handoff evidence
// available in ControlPlaneNetworkTransition. It deliberately does not decide
// readiness: the Stage 1 completion/generation contract is owned by the Stage 1
// workflow and must be applied by the action-specific preflight.
type Stage1Snapshot struct {
	Transition           *api.ControlPlaneNetworkTransition
	TransitionUID        types.UID
	Generation           int64
	RequestedAction      api.Action
	Status               api.TransitionStatus
	TargetNSXProjectPath string
	TargetNSXProjectID   string
	RequestedLBProvider  api.LoadBalancerProvider
	TargetLBProvider     api.LoadBalancerProvider
	PrimaryVPCPath       string
	LoadBalancerVPCPath  string
	LBSNATAllocations    []api.IPAllocationStatus
}

// ReadNamespaceBatchSnapshot reads every immutable batch member and copies
// progress/workflow checkpoints from the transition. Members are sorted to
// make later conflict diagnostics and logs deterministic.
func ReadNamespaceBatchSnapshot(ctx context.Context, c client.Reader, batch *api.NamespaceNetworkTransition) (*NamespaceBatchSnapshot, error) {
	if batch == nil {
		return nil, fmt.Errorf("namespace transition is nil")
	}
	if len(batch.Spec.Namespaces) == 0 {
		return nil, fmt.Errorf("namespace transition %q has an empty batch", batch.Name)
	}

	names := append([]string(nil), batch.Spec.Namespaces...)
	sort.Strings(names)
	if names[0] == "" {
		return nil, fmt.Errorf("namespace transition %q has an empty member", batch.Name)
	}
	for i := 1; i < len(names); i++ {
		if names[i] == names[i-1] {
			return nil, fmt.Errorf("namespace transition %q has duplicate member %q", batch.Name, names[i])
		}
	}

	snapshot := &NamespaceBatchSnapshot{
		TransitionUID:   batch.UID,
		Generation:      batch.Generation,
		RequestedAction: batch.Spec.Action,
		Namespaces:      make([]NamespaceSnapshot, 0, len(names)),
	}
	members := namespaceSet(names)
	progress := make(map[string]api.NamespaceStatus, len(names))
	if batch.Status != nil {
		snapshot.Phase = batch.Status.Phase
		snapshot.LastAction = batch.Status.LastAction
		snapshot.ActionState = batch.Status.ActionState
		snapshot.ActionGeneration = batch.Status.ActionGeneration
		snapshot.ObservedGeneration = batch.Status.ObservedGeneration
		snapshot.IrreversibleAction = batch.Status.IrreversibleAction
		snapshot.NSXWorkflows = append([]api.NSXWorkflowStatus(nil), batch.Status.NSXWorkflows...)
		for _, run := range batch.Status.NamespaceStatuses {
			if _, inBatch := members[run.Name]; !inBatch {
				return nil, fmt.Errorf("namespace transition %q has progress for out-of-batch Namespace %q", batch.Name, run.Name)
			}
			if _, duplicate := progress[run.Name]; duplicate {
				return nil, fmt.Errorf("namespace transition %q has duplicate progress for Namespace %q", batch.Name, run.Name)
			}
			progress[run.Name] = run
		}
		workflowIDs := make(map[string]struct{}, len(batch.Status.NSXWorkflows))
		workflowVDNs := make(map[string]struct{}, len(batch.Status.NSXWorkflows))
		for _, workflow := range batch.Status.NSXWorkflows {
			if workflow.VDNName == "" || workflow.WorkflowID == "" {
				return nil, fmt.Errorf("namespace transition %q has an incomplete NSX workflow checkpoint", batch.Name)
			}
			if _, duplicate := workflowIDs[workflow.WorkflowID]; duplicate {
				return nil, fmt.Errorf("namespace transition %q has duplicate workflow ID %q", batch.Name, workflow.WorkflowID)
			}
			if _, duplicate := workflowVDNs[workflow.VDNName]; duplicate {
				return nil, fmt.Errorf("namespace transition %q has duplicate workflow checkpoint for VDN %q", batch.Name, workflow.VDNName)
			}
			workflowIDs[workflow.WorkflowID] = struct{}{}
			workflowVDNs[workflow.VDNName] = struct{}{}
		}
	}

	for _, name := range names {
		namespace := &corev1.Namespace{}
		if err := c.Get(ctx, client.ObjectKey{Name: name}, namespace); err != nil {
			return nil, fmt.Errorf("read Namespace %q in transition %q: %w", name, batch.Name, err)
		}
		terminating := !namespace.DeletionTimestamp.IsZero()
		entry := NamespaceSnapshot{
			Name: name, UID: namespace.UID, Terminating: terminating,
			Progress: api.NamespaceStatus{Name: name, Phase: api.PhasePending},
		}
		if run, ok := progress[name]; ok {
			entry.Progress = run
			entry.HasProgress = true
		}
		snapshot.Namespaces = append(snapshot.Namespaces, entry)
	}
	return snapshot, nil
}

var errStage1StatusMissing = errors.New("Stage 1 transition has no status")

// ReadStage1Snapshot loads the singleton infrastructure transition and copies
// its current status and shared-resource references for Stage 2 consumers.
// Missing/incomplete success evidence is preserved for the caller to classify.
func ReadStage1Snapshot(ctx context.Context, c client.Reader) (*Stage1Snapshot, error) {
	transition := &api.ControlPlaneNetworkTransition{}
	if err := c.Get(ctx, client.ObjectKey{Name: api.ControlPlaneNetworkTransitionName}, transition); err != nil {
		return nil, fmt.Errorf("read Stage 1 transition %q: %w", api.ControlPlaneNetworkTransitionName, err)
	}
	if transition.Status == nil {
		return nil, fmt.Errorf("%w: %q", errStage1StatusMissing, transition.Name)
	}

	status := transition.Status.TransitionStatus
	status.Conditions = append([]metav1.Condition(nil), status.Conditions...)
	status.NSXWorkflows = append([]api.NSXWorkflowStatus(nil), status.NSXWorkflows...)
	status.RequiredInputs = append([]api.RequiredInput(nil), status.RequiredInputs...)
	status.OptionalInputs = append([]api.OptionalInput(nil), status.OptionalInputs...)
	return &Stage1Snapshot{
		Transition:           transition.DeepCopy(),
		TransitionUID:        transition.UID,
		Generation:           transition.Generation,
		RequestedAction:      transition.Spec.Action,
		Status:               status,
		TargetNSXProjectPath: transition.Status.TargetNSXProjectPath,
		TargetNSXProjectID:   transition.Spec.NSXProjectID,
		RequestedLBProvider:  transition.Spec.TargetLBProvider,
		TargetLBProvider:     transition.Status.TargetLBProvider,
		PrimaryVPCPath:       transition.Status.PrimaryVPCPath,
		LoadBalancerVPCPath:  transition.Status.LoadBalancerVPCPath,
		LBSNATAllocations:    append([]api.IPAllocationStatus(nil), transition.Status.LBSNATIPAllocations...),
	}, nil
}

type networkSettingsProvider struct {
	Provider       string
	LegacyProvider string
}

type invalidSupervisorNamespaceError struct {
	message string
}

func (e *invalidSupervisorNamespaceError) Error() string { return e.message }

type invalidNetworkSettingsError struct {
	message string
}

func (e *invalidNetworkSettingsError) Error() string { return e.message }

// ReadSupervisorNamespaceProvider reads the desired provider from the
// SupervisorNamespace networking union. Exactly one provider config must be
// present; annotations and tags do not identify the desired network stack.
func ReadSupervisorNamespaceProvider(ctx context.Context, c client.Reader, name string) (string, error) {
	var lastErr error
	for _, version := range []string{"v1alpha2", "v1alpha1"} {
		object := &unstructured.Unstructured{}
		object.SetAPIVersion("iaas.vmware.com/" + version)
		object.SetKind("SupervisorNamespace")
		if err := c.Get(ctx, client.ObjectKey{Name: name}, object); err != nil {
			lastErr = err
			if meta.IsNoMatchError(err) {
				continue
			}
			return "", fmt.Errorf("read SupervisorNamespace %q (%s): %w", name, version, err)
		}
		return supervisorNamespaceProvider(object, name)
	}
	return "", fmt.Errorf("read SupervisorNamespace %q: %w", name, lastErr)
}

// ReadCoexistenceNetworkProvider reads the WCP provider registration used to
// enable mixed VDS/VPC mode. System namespaces do not have SupervisorNamespace
// CRs, so tenant CRs cannot reliably establish cluster-level coexistence.
func ReadCoexistenceNetworkProvider(ctx context.Context, c client.Reader) (string, error) {
	config := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: "vmware-system-vks-public", Name: "supervisor-properties"}
	if err := c.Get(ctx, key, config); err != nil {
		return "", fmt.Errorf("read WCP coexistence ConfigMap %s/%s: %w", key.Namespace, key.Name, err)
	}
	return config.Data["networkProvider"], nil
}

func supervisorNamespaceProvider(object *unstructured.Unstructured, name string) (string, error) {
	_, found, err := unstructured.NestedMap(object.Object, "spec", "networking")
	if err != nil {
		return "", &invalidSupervisorNamespaceError{message: fmt.Sprintf("SupervisorNamespace %q has invalid spec.networking: %v", name, err)}
	}
	if !found {
		return "", &invalidSupervisorNamespaceError{message: fmt.Sprintf("SupervisorNamespace %q has no valid spec.networking", name)}
	}
	_, vds, err := unstructured.NestedMap(object.Object, "spec", "networking", "vsphereNetworkConfig")
	if err != nil {
		return "", &invalidSupervisorNamespaceError{message: fmt.Sprintf("SupervisorNamespace %q has invalid vsphereNetworkConfig: %v", name, err)}
	}
	_, vpc, err := unstructured.NestedMap(object.Object, "spec", "networking", "vpcNetworkConfig")
	if err != nil {
		return "", &invalidSupervisorNamespaceError{message: fmt.Sprintf("SupervisorNamespace %q has invalid vpcNetworkConfig: %v", name, err)}
	}
	if vds == vpc {
		return "", &invalidSupervisorNamespaceError{message: fmt.Sprintf("SupervisorNamespace %q must have exactly one of spec.networking.vsphereNetworkConfig or spec.networking.vpcNetworkConfig", name)}
	}
	if vpc {
		return "NSX_VPC", nil
	}
	return "VSPHERE_VDS", nil
}

// ReadNetworkSettingsProvider reads the Net Operator's effective provider
// state for a Namespace. SupervisorNamespace is desired configuration; this
// resource is the provider state used by workload controllers.
func ReadNetworkSettingsProvider(ctx context.Context, c client.Reader, namespace string) (networkSettingsProvider, error) {
	object := &unstructured.Unstructured{}
	object.SetAPIVersion("netoperator.vmware.com/v1alpha1")
	object.SetKind("NetworkSettings")
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "default"}, object); err != nil {
		return networkSettingsProvider{}, fmt.Errorf("read NetworkSettings/default in Namespace %q: %w", namespace, err)
	}
	provider, found, err := unstructured.NestedString(object.Object, "provider")
	if err != nil {
		return networkSettingsProvider{}, &invalidNetworkSettingsError{message: fmt.Sprintf("NetworkSettings/default in Namespace %q has invalid provider: %v", namespace, err)}
	}
	if !found || provider == "" {
		return networkSettingsProvider{}, &invalidNetworkSettingsError{message: fmt.Sprintf("NetworkSettings/default in Namespace %q has no valid provider", namespace)}
	}
	legacyProvider, found, err := unstructured.NestedString(object.Object, "legacyProvider")
	if err != nil {
		return networkSettingsProvider{}, &invalidNetworkSettingsError{message: fmt.Sprintf("read NetworkSettings/default legacyProvider in Namespace %q: %v", namespace, err)}
	}
	if !found {
		legacyProvider = ""
	}
	return networkSettingsProvider{Provider: provider, LegacyProvider: legacyProvider}, nil
}

func namespaceSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}
