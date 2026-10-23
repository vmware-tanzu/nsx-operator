/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// NamespaceNetworkTransitionSpec requests a batch of tenant namespace transitions.
// +kubebuilder:validation:XValidation:rule="self.namespaces == oldSelf.namespaces",message="namespaces is immutable"
// +kubebuilder:validation:XValidation:rule="self.action != 'FINALIZE'",message="FINALIZE is an infrastructure operation"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.userInputs) || has(self.userInputs)",message="userInputs cannot be removed once supplied"
type NamespaceNetworkTransitionSpec struct {
	TransitionSpec `json:",inline"`
	// namespaces identifies the immutable set of target namespaces.
	// +required
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1000
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
	Namespaces []string `json:"namespaces,omitempty"`
	// userInputs supplies administrator-provided migration parameters.
	// Supplied values cannot be changed or removed; missing values may be added before planning starts.
	// +optional
	UserInputs *NamespaceUserInputs `json:"userInputs,omitempty"`
}

// NamespaceUserInputs supplies addressing discovered during evaluation.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.tenantSubnets) || has(self.tenantSubnets)",message="tenantSubnets cannot be removed once supplied"
type NamespaceUserInputs struct {
	// tenantSubnets supplies addressing for DHCP-backed tenant networks.
	// Each vdn identifies a source network discovered from the target namespaces;
	// namespaces sharing that network use the same entry.
	// +optional
	// +listType=map
	// +listMapKey=vdn
	// +kubebuilder:validation:MaxItems=1000
	// +kubebuilder:validation:XValidation:rule="[self.transformMapEntry(i, s, {s.vdn: true})].all(ids, oldSelf.all(s, s.vdn in ids))",message="existing subnet inputs cannot be removed"
	TenantSubnets []SubnetInput `json:"tenantSubnets,omitempty"`
}

// NamespaceStatus reports progress for one target namespace.
type NamespaceStatus struct {
	// name identifies the target namespace.
	// +required
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`
	// phase describes current progress for this namespace.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:MinLength=1
	Phase string `json:"phase,omitempty"`
	// reason identifies the machine-readable cause of the namespace outcome.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Reason string `json:"reason,omitempty"`
	// message describes the namespace outcome or failure requiring attention.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	Message string `json:"message,omitempty"`
}

// NamespaceNetworkTransitionStatus contains aggregate and per-namespace progress.
type NamespaceNetworkTransitionStatus struct {
	TransitionStatus `json:",inline"`
	// namespaceStatuses records progress without creating separate status resources.
	// Batch membership comes from spec.namespaces; missing status entries are pending.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=1000
	NamespaceStatuses []NamespaceStatus `json:"namespaceStatuses,omitempty"`
}

// NamespaceNetworkTransition requests Stage 2 migration of an explicit namespace batch.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['PLAN', 'DERIVE', 'EXECUTE', 'ROLLBACK', 'COMMIT', 'FINALIZE']) || (has(self.spec.userInputs) == has(oldSelf.spec.userInputs) && (!has(oldSelf.spec.userInputs) || self.spec.userInputs == oldSelf.spec.userInputs))",message="userInputs cannot change after planning starts"
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
	// metadata identifies this transition.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// spec describes the requested transition.
	// +required
	Spec NamespaceNetworkTransitionSpec `json:"spec,omitzero"`
	// status reports observed aggregate and per-namespace progress.
	// +optional
	Status *NamespaceNetworkTransitionStatus `json:"status,omitempty"`
}

// NamespaceNetworkTransitionList contains namespace transition batches.
// +kubebuilder:object:root=true
type NamespaceNetworkTransitionList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata describes this list.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// items contains the transition batches.
	// +required
	Items []NamespaceNetworkTransition `json:"items"`
}
