/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// NamespaceNetworkTransitionSpec requests a batch of Supervisor Namespace transitions.
// +kubebuilder:validation:XValidation:rule="self.namespaces == oldSelf.namespaces",message="Namespaces is immutable"
// +kubebuilder:validation:XValidation:rule="self.action in ['Evaluate', 'Execute', 'Commit', 'Rollback']",message="namespace action must be Evaluate, Execute, Commit, or Rollback"
// +kubebuilder:validation:XValidation:rule="self.action != 'Finalize'",message="Finalize is an infrastructure operation"
type NamespaceNetworkTransitionSpec struct {
	TransitionSpec `json:",inline"`
	// Namespaces identifies the immutable set of target Namespaces.
	// +required
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1000
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
	Namespaces []string `json:"namespaces,omitempty"`
	// NetworkConfiguration supplies administrator-provided migration parameters.
	// Values may be added, corrected, or removed while the requested action is
	// Evaluate and no execution checkpoint has been recorded. Execute freezes the
	// configuration. Namespace inputs may be edited again only after a verified,
	// successful whole-batch Rollback and a new Evaluate request.
	// +optional
	NetworkConfiguration *NamespaceNetworkConfiguration `json:"networkConfiguration,omitempty"`
}

// NamespaceNetworkConfiguration supplies addressing discovered during evaluation.
type NamespaceNetworkConfiguration struct {
	// ResourceNaming supplies optional IDs for NSX resources created for migration.
	// Omitted names use deterministic generated names. Naming inputs never rename
	// or implicitly adopt existing resources and are not requiredInputs.
	// +optional
	ResourceNaming *NamespaceResourceNaming `json:"resourceNaming,omitempty"`

	// NamespaceDHCPNetworks describes existing DHCP-backed networks used by the target Namespaces.
	// Each vdnName identifies a source network discovered from the target Namespaces;
	// Namespaces sharing that network use the same entry.
	// +optional
	// +listType=map
	// +listMapKey=vdnName
	// +kubebuilder:validation:MaxItems=1000
	NamespaceDHCPNetworks []DHCPNetworkConfiguration `json:"namespaceDHCPNetworks,omitempty"`
}

// NamespaceStatus reports progress for one target Namespace.
type NamespaceStatus struct {
	// Name identifies the target Namespace.
	// +required
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`
	// Phase describes current progress for this Namespace.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:MinLength=1
	Phase string `json:"phase,omitempty"`
	// Reason identifies the machine-readable cause of the Namespace outcome.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Reason string `json:"reason,omitempty"`
	// Message describes the Namespace outcome or failure requiring attention.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	Message string `json:"message,omitempty"`
}

// NamespaceResourceNaming supplies optional names for new workload topology.
// Each supplied name is also used as the NSX resource ID.
// Evaluate discovers the VDNs used by spec.namespaces, resolves their VDS-derived
// zone spans, and searches for compatible existing TGW/VPC topology.
type NamespaceResourceNaming struct {
	// Networks supplies optional naming overrides for source VDNs. Existing
	// compatible resources are reused; new resources without overrides use
	// deterministic generated names. Missing names are not requiredInputs.
	// +optional
	// +listType=map
	// +listMapKey=vdnName
	// +kubebuilder:validation:MaxItems=1000
	Networks []VDNResourceNaming `json:"networks,omitempty"`
}

// NamespaceNetworkTransitionStatus contains aggregate and per-Namespace progress.
// +kubebuilder:validation:XValidation:rule="!has(self.irreversibleAction) || self.irreversibleAction == 'Commit'",message="Namespace irreversibleAction must be Commit"
type NamespaceNetworkTransitionStatus struct {
	TransitionStatus `json:",inline"`
	// EvaluationPreview is the source-based, caller-facing result of the latest
	// successful Evaluate. Generation must match metadata.generation and the
	// successful Evaluate actionGeneration before a caller presents it for review.
	// Target-dependent checks remain pending until Execute prepares target resources.
	// +optional
	EvaluationPreview *NamespaceEvaluationPreview `json:"evaluationPreview,omitempty"`
	// RollbackComplete records that a successful Rollback fully restored the batch.
	// It remains true during Evaluate so infrastructure Finalize can recognize the
	// batch as closed while inputs are re-evaluated. A new Execute, Rollback, or
	// Commit action clears it.
	// +optional
	RollbackComplete bool `json:"rollbackComplete,omitempty"`
	// NamespaceStatuses records progress without creating separate status resources.
	// Entries must belong to spec.namespaces; missing entries and empty phases are Pending.
	// Clients derive aggregate counts with NamespacePhaseCounts; counters are not persisted.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=1000
	NamespaceStatuses []NamespaceStatus `json:"namespaceStatuses,omitempty"`
}

// NamespaceEvaluationPreview is the source-based migration preview exposed to
// VC/VCFA callers before Execute. It contains only facts collected by Evaluate;
// it is not a durable fabric inventory or an NSX conversion plan.
type NamespaceEvaluationPreview struct {
	// Generation identifies the spec generation evaluated to produce this preview.
	// +required
	// +kubebuilder:validation:Minimum=1
	Generation int64 `json:"generation"`
	// ReviewRevision increases whenever a new preview is published, including a
	// material target difference discovered during Execute.
	// +optional
	// +kubebuilder:validation:Minimum=1
	ReviewRevision int64 `json:"reviewRevision"`
	// ReviewRequired is true when Execute discovered a material difference and
	// paused before cutover. The caller acknowledges this revision by updating
	// metadata.annotations[reviewed-preview-revision] to its decimal value.
	// +optional
	ReviewRequired bool `json:"reviewRequired,omitempty"`
	// Networks contains one entry per discovered source VDN in the batch.
	// +optional
	// +listType=map
	// +listMapKey=vdnName
	// +kubebuilder:validation:MaxItems=1000
	Networks []NamespaceNetworkPreview `json:"networks,omitempty"`
	// PendingChecks identifies target-dependent checks that Execute must complete.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=100
	PendingChecks []string `json:"pendingChecks,omitempty"`
	// MaterialChanges records differences between the reviewed proposal and the
	// target-specific Execute result.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=5000
	MaterialChanges []NamespacePreviewChange `json:"materialChanges,omitempty"`
}

// NamespaceNetworkPreview groups source topology and source-only validation
// facts by the VDN key shared with networkConfiguration and nsxWorkflows.
type NamespaceNetworkPreview struct {
	// VDNName identifies the source VDN.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	VDNName string `json:"vdnName"`
	// Namespaces identifies the batch members discovered on this VDN.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=1000
	Namespaces []string `json:"namespaces,omitempty"`
	// SourceVDSRef is the source VDS identity used for NSX source-only validation.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	SourceVDSRef string `json:"sourceVdsRef,omitempty"`
	// DVPGs contains the validated source DVPGs and compatibility results.
	// +optional
	// +listType=map
	// +listMapKey=reference
	// +kubebuilder:validation:MaxItems=1000
	DVPGs []NamespaceDVPGPreview `json:"dvpgs,omitempty"`
	// TargetResources contains target resource reuse or creation decisions made
	// during Evaluate. Empty means Evaluate could not yet resolve target choices.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=5000
	TargetResources []NamespaceTargetResourcePreview `json:"targetResources,omitempty"`
}

// NamespaceDVPGPreview describes one source DVPG returned by NSX validation.
type NamespaceDVPGPreview struct {
	// Reference is the source DVPG identity.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Reference string `json:"reference"`
	// Name is the discovered display name, falling back to Reference when absent.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name"`
	// VLANID is the validated VLAN identifier.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	VLANID string `json:"vlanId"`
	// Compatibility is SUPPORTED or PARTIALLY_SUPPORTED.
	// +required
	// +kubebuilder:validation:Enum=SUPPORTED;PARTIALLY_SUPPORTED
	Compatibility string `json:"compatibility"`
	// Warnings contains non-blocking source validation findings.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=100
	Warnings []string `json:"warnings,omitempty"`
}

// NamespaceTargetResourcePreview describes a proposed target resource decision
// available from Evaluate. It is a review summary, not an NSX conversion payload.
type NamespaceTargetResourcePreview struct {
	// Kind identifies the target resource type.
	// +required
	// +kubebuilder:validation:Enum=TGW;VPC;DVC;Subnet
	Kind string `json:"kind"`
	// Decision reports whether the resource will be reused, created, or still
	// requires target-side resolution during Execute.
	// +required
	// +kubebuilder:validation:Enum=Reuse;Create;Pending
	Decision string `json:"decision"`
	// Name is the proposed or reused resource name when known.
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`
	// Path is the exact existing target path when a compatible resource is reused.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Path string `json:"path,omitempty"`
	// Reason explains the decision or why it remains pending.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Reason string `json:"reason,omitempty"`
}

// NamespacePreviewChange reports a material target-resource difference that
// requires another user review before cutover.
type NamespacePreviewChange struct {
	// VDNName identifies the source network whose target decision changed.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	VDNName string `json:"vdnName"`
	// Kind identifies the target resource type that changed.
	// +required
	// +kubebuilder:validation:Enum=TGW;VPC;DVC;Subnet
	Kind string `json:"kind"`
	// Previous is the reviewed decision; it is nil when the target resource was
	// not proposed by Evaluate.
	// +optional
	Previous *NamespaceTargetResourcePreview `json:"previous,omitempty"`
	// Current is the decision found by target-specific validation; it is nil
	// when the reviewed target resource is no longer present in the result.
	// +optional
	Current *NamespaceTargetResourcePreview `json:"current,omitempty"`
}

// NamespaceNetworkTransition requests Stage 2 migration of an explicit Namespace batch.
// +kubebuilder:validation:XValidation:rule="!has(self.status) || !has(self.status.lastAction) || self.status.lastAction in ['Evaluate', 'Execute', 'Rollback', 'Commit'] || (has(oldSelf.status) && has(oldSelf.status.lastAction) && self.status.lastAction == oldSelf.status.lastAction && has(self.status.actionState) == has(oldSelf.status.actionState) && (!has(self.status.actionState) || self.status.actionState == oldSelf.status.actionState) && has(self.status.actionGeneration) == has(oldSelf.status.actionGeneration) && (!has(self.status.actionGeneration) || self.status.actionGeneration == oldSelf.status.actionGeneration))",message="namespace lastAction must be Evaluate, Execute, Rollback, or Commit unless an existing checkpoint is unchanged"
// +kubebuilder:validation:XValidation:rule="!has(self.status) || !has(self.status.namespaceStatuses) || [self.spec.namespaces.transformMapEntry(i, name, {name: true})].all(names, self.status.namespaceStatuses.all(ns, ns.name in names))",message="namespaceStatuses entries must belong to spec.namespaces"
// +kubebuilder:validation:XValidation:rule="self.spec.action == 'Evaluate' || (has(self.spec.networkConfiguration) == has(oldSelf.spec.networkConfiguration) && (!has(oldSelf.spec.networkConfiguration) || self.spec.networkConfiguration == oldSelf.spec.networkConfiguration))",message="networkConfiguration may only change with Evaluate"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['Execute', 'Rollback', 'Commit', 'Finalize']) || (self.spec.action == 'Evaluate' && oldSelf.status.lastAction == 'Rollback' && has(oldSelf.status.actionState) && oldSelf.status.actionState == 'Succeeded' && has(oldSelf.status.actionGeneration) && has(oldSelf.status.observedGeneration) && oldSelf.status.actionGeneration == oldSelf.status.observedGeneration && has(oldSelf.status.phase) && oldSelf.status.phase == 'RolledBack' && has(oldSelf.status.rollbackComplete) && oldSelf.status.rollbackComplete && !has(oldSelf.status.irreversibleAction)) || (has(self.spec.networkConfiguration) == has(oldSelf.spec.networkConfiguration) && (!has(oldSelf.spec.networkConfiguration) || self.spec.networkConfiguration == oldSelf.spec.networkConfiguration))",message="networkConfiguration cannot change after execution starts unless Evaluate follows a current, verified successful Rollback without an irreversible checkpoint"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.irreversibleAction) || (has(self.status) && has(self.status.irreversibleAction) && self.status.irreversibleAction == oldSelf.status.irreversibleAction && self.spec.action == oldSelf.status.irreversibleAction)",message="an irreversible checkpoint cannot change or be cleared; only its action may be resumed"
// +kubebuilder:validation:XValidation:rule="!has(self.status) || !has(self.status.irreversibleAction) || self.spec.action == self.status.irreversibleAction",message="an irreversible checkpoint requires the corresponding spec.action"
// +kubebuilder:object:root=true
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:resource:scope=Cluster,shortName=nsnt
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Action",type=string,JSONPath=`.spec.action`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type NamespaceNetworkTransition struct {
	metav1.TypeMeta `json:",inline"`
	// ObjectMeta identifies this transition.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec describes the requested transition.
	// +required
	Spec NamespaceNetworkTransitionSpec `json:"spec,omitzero"`
	// Status reports observed aggregate and per-Namespace progress.
	// +optional
	Status *NamespaceNetworkTransitionStatus `json:"status,omitempty"`
}

// NamespaceNetworkTransitionList contains Namespace transition batches.
// +kubebuilder:object:root=true
type NamespaceNetworkTransitionList struct {
	metav1.TypeMeta `json:",inline"`
	// ListMeta describes this list.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items contains the transition batches.
	// +required
	Items []NamespaceNetworkTransition `json:"items"`
}
