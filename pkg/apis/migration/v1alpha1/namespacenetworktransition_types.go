/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// NamespaceNetworkTransitionSpec requests a batch of Supervisor Namespace transitions.
// +kubebuilder:validation:XValidation:rule="self.namespaces == oldSelf.namespaces",message="Namespaces is immutable"
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
	// Evaluate and no execution checkpoint has been recorded, or after a successful
	// Rollback with no irreversible checkpoint. Execute otherwise freezes the configuration.
	// Rollback success requires verified restoration; a new Evaluate is required before another Execute.
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
	// NamespaceStatuses records progress without creating separate status resources.
	// Entries must belong to spec.namespaces; missing entries and empty phases are Pending.
	// Clients derive aggregate counts with NamespacePhaseCounts; counters are not persisted.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=1000
	NamespaceStatuses []NamespaceStatus `json:"namespaceStatuses,omitempty"`
}

// NamespaceNetworkTransition requests Stage 2 migration of an explicit Namespace batch.
// +kubebuilder:validation:XValidation:rule="!has(self.status) || !has(self.status.namespaceStatuses) || [self.spec.namespaces.transformMapEntry(i, name, {name: true})].all(names, self.status.namespaceStatuses.all(ns, ns.name in names))",message="namespaceStatuses entries must belong to spec.namespaces"
// +kubebuilder:validation:XValidation:rule="self.spec.action == 'Evaluate' || (has(self.spec.networkConfiguration) == has(oldSelf.spec.networkConfiguration) && (!has(oldSelf.spec.networkConfiguration) || self.spec.networkConfiguration == oldSelf.spec.networkConfiguration))",message="networkConfiguration may only change with Evaluate"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['Execute', 'Rollback', 'Commit', 'Finalize']) || (self.spec.action == 'Evaluate' && oldSelf.status.lastAction == 'Rollback' && has(oldSelf.status.actionState) && oldSelf.status.actionState == 'Succeeded' && !has(oldSelf.status.irreversibleAction)) || (has(self.spec.networkConfiguration) == has(oldSelf.spec.networkConfiguration) && (!has(oldSelf.spec.networkConfiguration) || self.spec.networkConfiguration == oldSelf.spec.networkConfiguration))",message="networkConfiguration cannot change after execution starts unless Evaluate follows a successful Rollback without an irreversible checkpoint"
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
