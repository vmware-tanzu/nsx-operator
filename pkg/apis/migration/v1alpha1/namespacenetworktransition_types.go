/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// NamespaceNetworkTransitionSpec requests a batch of Supervisor namespace transitions.
// +kubebuilder:validation:XValidation:rule="self.namespaces == oldSelf.namespaces",message="namespaces is immutable"
// +kubebuilder:validation:XValidation:rule="self.action != 'Finalize'",message="Finalize is an infrastructure operation"
type NamespaceNetworkTransitionSpec struct {
	TransitionSpec `json:",inline"`
	// Namespaces identifies the immutable set of target namespaces.
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
	// Evaluate and no planning checkpoint has been recorded, or after a successful
	// Rollback with no irreversible checkpoint. Plan otherwise freezes the configuration.
	// Rollback success must mean restoration is verified and the previous plan is invalidated.
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

	// NamespaceDHCPNetworks describes existing DHCP-backed networks used by the target namespaces.
	// Each vdnName identifies a source network discovered from the target namespaces;
	// namespaces sharing that network use the same entry.
	// +optional
	// +listType=map
	// +listMapKey=vdnName
	// +kubebuilder:validation:MaxItems=1000
	NamespaceDHCPNetworks []DHCPNetworkConfiguration `json:"namespaceDHCPNetworks,omitempty"`
}

// NamespaceStatus reports progress for one target namespace.
type NamespaceStatus struct {
	// Name identifies the target namespace.
	// +required
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`
	// Phase describes current progress for this namespace.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:MinLength=1
	Phase string `json:"phase,omitempty"`
	// Reason identifies the machine-readable cause of the namespace outcome.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Reason string `json:"reason,omitempty"`
	// Message describes the namespace outcome or failure requiring attention.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	Message string `json:"message,omitempty"`
}

// NamespaceNetworkTransitionStatus contains aggregate and per-namespace progress.
// +kubebuilder:validation:XValidation:rule="!has(self.irreversibleAction) || self.irreversibleAction == 'Commit'",message="namespace irreversibleAction must be Commit"
type NamespaceNetworkTransitionStatus struct {
	TransitionStatus `json:",inline"`
	// NamespaceStatuses records progress without creating separate status resources.
	// Batch membership comes from spec.namespaces; missing status entries are pending.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=1000
	NamespaceStatuses []NamespaceStatus `json:"namespaceStatuses,omitempty"`
}

// NamespaceNetworkTransition requests Stage 2 migration of an explicit namespace batch.
// +kubebuilder:validation:XValidation:rule="self.spec.action == 'Evaluate' || (has(self.spec.networkConfiguration) == has(oldSelf.spec.networkConfiguration) && (!has(oldSelf.spec.networkConfiguration) || self.spec.networkConfiguration == oldSelf.spec.networkConfiguration))",message="networkConfiguration may only change with Evaluate"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['Plan', 'Derive', 'Execute', 'Rollback', 'Commit', 'Finalize']) || (self.spec.action == 'Evaluate' && oldSelf.status.lastAction == 'Rollback' && has(oldSelf.status.actionState) && oldSelf.status.actionState == 'Succeeded' && !has(oldSelf.status.irreversibleAction)) || (has(self.spec.networkConfiguration) == has(oldSelf.spec.networkConfiguration) && (!has(oldSelf.spec.networkConfiguration) || self.spec.networkConfiguration == oldSelf.spec.networkConfiguration))",message="networkConfiguration cannot change after planning starts unless Evaluate follows a successful Rollback without an irreversible checkpoint"
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
	// Status reports observed aggregate and per-namespace progress.
	// +optional
	Status *NamespaceNetworkTransitionStatus `json:"status,omitempty"`
}

// NamespaceNetworkTransitionList contains namespace transition batches.
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
