/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ControlPlaneNetworkTransitionName identifies the sole infrastructure transition in a Supervisor.
const ControlPlaneNetworkTransitionName = "supervisor-vds-to-vpc"

// ControlPlaneNetworkTransitionSpec requests infrastructure network migration.
// +kubebuilder:validation:XValidation:rule="self.action != 'COMMIT'",message="COMMIT is a namespace operation"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.nsxProjectId) || (has(self.nsxProjectId) && self.nsxProjectId == oldSelf.nsxProjectId)",message="nsxProjectId cannot change once supplied"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.userInputs) || has(self.userInputs)",message="userInputs cannot be removed once supplied"
type ControlPlaneNetworkTransitionSpec struct {
	TransitionSpec `json:",inline"`
	// targetLbProvider selects AVI or NSX_LB as the desired target load balancer provider.
	// If omitted, evaluation resolves an unambiguous supported target or requests input.
	// The resolved selection is bound to the reviewed plan and cannot change after planning starts.
	// +optional
	// +kubebuilder:validation:Enum=AVI;NSX_LB
	TargetLBProvider LoadBalancerProvider `json:"targetLbProvider,omitempty"`
	// nsxProjectId selects a pre-existing NSX project; omission selects the default project.
	// Once supplied, the ID cannot be changed or removed. Omission is fixed when planning starts.
	// +optional
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:MinLength=1
	NSXProjectID string `json:"nsxProjectId,omitempty"`
	// userInputs supplies administrator-provided migration parameters.
	// Supplied values cannot be changed or removed; missing values may be added before planning starts.
	// +optional
	UserInputs *ControlPlaneUserInputs `json:"userInputs,omitempty"`
}

// ControlPlaneUserInputs supplies network and load balancer settings discovered during evaluation.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.cpVmServiceSubnets) || has(self.cpVmServiceSubnets)",message="cpVmServiceSubnets cannot be removed once supplied"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.legacyLbFrontendDvpgs) || has(self.legacyLbFrontendDvpgs)",message="legacyLbFrontendDvpgs cannot be removed once supplied"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.aviConfig) || has(self.aviConfig)",message="aviConfig cannot be removed once supplied"
type ControlPlaneUserInputs struct {
	// cpVmServiceSubnets supplies addressing for DHCP-backed control plane and service networks.
	// +optional
	// +listType=map
	// +listMapKey=vdn
	// +kubebuilder:validation:MaxItems=1000
	// +kubebuilder:validation:XValidation:rule="[self.transformMapEntry(i, s, {s.vdn: true})].all(ids, oldSelf.all(s, s.vdn in ids))",message="existing subnet inputs cannot be removed"
	CPVMServiceSubnets []SubnetInput `json:"cpVmServiceSubnets,omitempty"`
	// legacyLbFrontendDvpgs identifies frontend port groups for AVI or HAProxy sources; FLB discovers its backing.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=1000
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:XValidation:rule="[self.transformMapEntry(i, s, {s: true})].all(values, oldSelf.all(s, s in values))",message="existing values cannot be removed or changed"
	LegacyLBFrontendDVPGs []string `json:"legacyLbFrontendDvpgs,omitempty"`
	// aviConfig supplies Avi configuration when either the source or target provider is AVI.
	// +optional
	AviConfig *AviConfiguration `json:"aviConfig,omitempty"`
}

// AviConfiguration supplies source and target Avi network addressing.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.legacyVipRanges) || has(self.legacyVipRanges)",message="legacyVipRanges cannot be removed once supplied"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.newVipRanges) || has(self.newVipRanges)",message="newVipRanges cannot be removed once supplied"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.newFrontendDvpgs) || has(self.newFrontendDvpgs)",message="newFrontendDvpgs cannot be removed once supplied"
type AviConfiguration struct {
	// legacyVipRanges supplies existing VIP address ranges for an AVI source.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=1000
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:XValidation:rule="[self.transformMapEntry(i, s, {s: true})].all(values, oldSelf.all(s, s in values))",message="existing values cannot be removed or changed"
	LegacyVIPRanges []string `json:"legacyVipRanges,omitempty"`
	// newVipRanges supplies VIP CIDRs or address ranges when the target is AVI.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=1000
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +kubebuilder:validation:XValidation:rule="[self.transformMapEntry(i, s, {s: true})].all(values, oldSelf.all(s, s in values))",message="existing values cannot be removed or changed"
	NewVIPRanges []string `json:"newVipRanges,omitempty"`
	// newFrontendDvpgs identifies Avi target frontend networks, validated against the legacy frontend DVS.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=1000
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:XValidation:rule="[self.transformMapEntry(i, s, {s: true})].all(values, oldSelf.all(s, s in values))",message="existing values cannot be removed or changed"
	NewFrontendDVPGs []string `json:"newFrontendDvpgs,omitempty"`
}

// ControlPlaneNetworkTransitionStatus records infrastructure discovery and realized topology.
type ControlPlaneNetworkTransitionStatus struct {
	TransitionStatus `json:",inline"`
	// detectedSourceLbProvider identifies the discovered source load balancer provider.
	// +optional
	// +kubebuilder:validation:Enum=AVI;HAPROXY;VSPHERE_FOUNDATION
	DetectedSourceLBProvider LoadBalancerProvider `json:"detectedSourceLbProvider,omitempty"`
	// targetLbProvider identifies the resolved and validated AVI or NSX_LB target provider.
	// +optional
	// +kubebuilder:validation:Enum=AVI;NSX_LB
	TargetLBProvider LoadBalancerProvider `json:"targetLbProvider,omitempty"`
	// targetNsxProject is the full path of the resolved pre-existing NSX project.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	TargetNSXProject string `json:"targetNsxProject,omitempty"`
	// primaryVpcPath is the full path of the infrastructure VPC.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	PrimaryVPCPath string `json:"primaryVpcPath,omitempty"`
	// loadBalancerVpcPath is the full path of the load balancer VPC.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	LoadBalancerVPCPath string `json:"loadBalancerVpcPath,omitempty"`
}

// ControlPlaneNetworkTransition requests Stage 1 infrastructure transition and Stage 3 finalization.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['PLAN', 'DERIVE', 'EXECUTE', 'ROLLBACK', 'FINALIZE']) || (has(self.spec.targetLbProvider) == has(oldSelf.spec.targetLbProvider) && (!has(oldSelf.spec.targetLbProvider) || self.spec.targetLbProvider == oldSelf.spec.targetLbProvider))",message="targetLbProvider cannot change after planning starts"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['PLAN', 'DERIVE', 'EXECUTE', 'ROLLBACK', 'FINALIZE']) || (has(self.spec.nsxProjectId) == has(oldSelf.spec.nsxProjectId) && (!has(oldSelf.spec.nsxProjectId) || self.spec.nsxProjectId == oldSelf.spec.nsxProjectId))",message="nsxProjectId cannot change after planning starts"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['PLAN', 'DERIVE', 'EXECUTE', 'ROLLBACK', 'FINALIZE']) || (has(self.spec.userInputs) == has(oldSelf.spec.userInputs) && (!has(oldSelf.spec.userInputs) || self.spec.userInputs == oldSelf.spec.userInputs))",message="userInputs cannot change after planning starts"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.irreversibleAction) || (has(self.status) && has(self.status.irreversibleAction) && self.status.irreversibleAction == oldSelf.status.irreversibleAction && self.spec.action == oldSelf.status.irreversibleAction)",message="an irreversible checkpoint cannot change or be cleared; only its action may be resumed"
// +kubebuilder:validation:XValidation:rule="!has(self.status) || !has(self.status.irreversibleAction) || self.spec.action == self.status.irreversibleAction",message="an irreversible checkpoint requires the corresponding spec.action"
// +kubebuilder:object:root=true
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:resource:scope=Cluster,shortName=cpnt
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Action",type=string,JSONPath=`.spec.action`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'supervisor-vds-to-vpc'",message="the infrastructure transition must be named supervisor-vds-to-vpc"
type ControlPlaneNetworkTransition struct {
	metav1.TypeMeta `json:",inline"`
	// metadata identifies this transition.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// spec describes the requested transition.
	// +required
	Spec ControlPlaneNetworkTransitionSpec `json:"spec,omitzero"`
	// status reports observed progress.
	// +optional
	Status *ControlPlaneNetworkTransitionStatus `json:"status,omitempty"`
}

// ControlPlaneNetworkTransitionList contains infrastructure transitions.
// +kubebuilder:object:root=true
type ControlPlaneNetworkTransitionList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata describes this list.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// items contains the transitions.
	// +required
	Items []ControlPlaneNetworkTransition `json:"items"`
}
