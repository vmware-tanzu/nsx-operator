/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ControlPlaneNetworkTransitionName identifies the sole infrastructure transition in a Supervisor.
const ControlPlaneNetworkTransitionName = "supervisor-vds-to-vpc"

// ControlPlaneNetworkTransitionSpec requests infrastructure network migration.
// Infrastructure supports forward retry; reversion uses user-operated WCP backup and restore.
// +kubebuilder:validation:XValidation:rule="self.action != 'Rollback'",message="infrastructure Rollback is unsupported; use WCP backup and restore"
// +kubebuilder:validation:XValidation:rule="self.action != 'Commit'",message="Commit is a namespace operation"
// +kubebuilder:validation:XValidation:rule="!has(self.networkConfiguration) || (!has(self.networkConfiguration.lbSnatIPv4) && !has(self.networkConfiguration.lbSnatIPv6)) || !has(self.targetLbProvider) || self.targetLbProvider == 'NSX_LB'",message="LB SNAT IP inputs apply only to the NSX_LB target provider"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.nsxProjectId) || (has(self.nsxProjectId) && self.nsxProjectId == oldSelf.nsxProjectId)",message="nsxProjectId cannot change once supplied"
type ControlPlaneNetworkTransitionSpec struct {
	TransitionSpec `json:",inline"`
	// TargetLBProvider selects the desired target load balancer provider.
	// In 9.2, only NSX_LB is supported for HAProxy/FLB sources.
	// If omitted, evaluation resolves an unambiguous supported/configured target.
	// The resolved selection is bound to the reviewed plan and cannot change after planning starts.
	// +optional
	// +kubebuilder:validation:Enum=NSX_LB
	TargetLBProvider LoadBalancerProvider `json:"targetLbProvider,omitempty"`
	// NSXProjectID selects a pre-existing NSX project; omission selects the default project.
	// Once supplied, the ID cannot be changed or removed. Omission is fixed when planning starts.
	// +optional
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:MinLength=1
	NSXProjectID string `json:"nsxProjectId,omitempty"`
	// NetworkConfiguration supplies administrator-provided migration parameters.
	// Values may be added, corrected, or removed during Evaluate before planning.
	// Accepted SNAT reservations freeze the corresponding family's fallback address.
	// Changes require requested action Evaluate; requesting Plan freezes inputs,
	// and a recorded planning checkpoint prevents reopening them.
	// +optional
	NetworkConfiguration *ControlPlaneNetworkConfiguration `json:"networkConfiguration,omitempty"`
}

// ControlPlaneNetworkConfiguration supplies network and load balancer settings discovered during evaluation.
type ControlPlaneNetworkConfiguration struct {
	// ResourceNaming supplies optional IDs for NSX resources created for migration.
	// Omitted names use deterministic generated names. Naming inputs never rename
	// or implicitly adopt existing resources and are not requiredInputs.
	// +optional
	ResourceNaming *ControlPlaneResourceNaming `json:"resourceNaming,omitempty"`

	// LBSNATIPv4 supplies an available backend IPv4 address for the shared NSX LB service
	// when no IPv4 address can be reserved from the VIP pools during evaluation.
	// The migration service must validate availability and reserve the address before use.
	// May be corrected before reservation acceptance; acceptance or Plan freezes the input.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=15
	// +kubebuilder:validation:XValidation:rule="isIP(self) && ip(self).family() == 4",message="lbSnatIPv4 must be a plain IPv4 address"
	LBSNATIPv4 string `json:"lbSnatIPv4,omitempty"`
	// LBSNATIPv6 supplies an available backend IPv6 address for the shared NSX LB service
	// when no IPv6 address can be reserved from the VIP pools during evaluation.
	// The migration service must validate availability and reserve the address before use.
	// May be corrected before reservation acceptance; acceptance or Plan freezes the input.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=45
	// +kubebuilder:validation:XValidation:rule="isIP(self) && ip(self).family() == 6",message="lbSnatIPv6 must be a plain IPv6 address"
	LBSNATIPv6 string `json:"lbSnatIPv6,omitempty"`
	// ControlPlaneAndServiceDHCPNetworks describes existing DHCP-backed networks used by
	// control-plane VMs and Supervisor Services.
	// +optional
	// +listType=map
	// +listMapKey=vdnName
	// +kubebuilder:validation:MaxItems=1000
	ControlPlaneAndServiceDHCPNetworks []DHCPNetworkConfiguration `json:"controlPlaneAndServiceDHCPNetworks,omitempty"`
	// LegacyLBFrontendDVPGs identifies frontend port groups required for HAProxy sources.
	// FLB discovers its backing automatically.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=1000
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	LegacyLBFrontendDVPGs []string `json:"legacyLbFrontendDvpgs,omitempty"`
}

// AllocationReference identifies the exact source IPAddressAllocation reserved for SNAT.
type AllocationReference struct {
	// Namespace identifies the source allocation's Kubernetes namespace.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`
	// Name identifies the source IPAddressAllocation for Kubernetes API lookups.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// LBSNATReservationStatus records an accepted source reservation, not an allocation request.
// The controller verifies ownership and readiness before persisting this checkpoint.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="accepted LB SNAT reservation identity is immutable"
// +kubebuilder:validation:XValidation:rule="isIP(self.ipAddress) && ip(self.ipAddress).family() == (self.ipFamily == 'IPv4' ? 4 : 6)",message="reserved address must match its IP family"
type LBSNATReservationStatus struct {
	// IPFamily identifies the reserved address family.
	// +required
	// +kubebuilder:validation:Enum=IPv4;IPv6
	IPFamily string `json:"ipFamily"`
	// IPAddress is the address realized by the source allocation.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=45
	IPAddress string `json:"ipAddress"`
	// AllocationRef binds the reservation to the source allocation's namespace and name.
	// +required
	AllocationRef AllocationReference `json:"allocationRef"`
}

// ControlPlaneNetworkTransitionStatus records infrastructure discovery and realized topology.
// +kubebuilder:validation:XValidation:rule="!has(self.irreversibleAction) || self.irreversibleAction == 'Finalize'",message="infrastructure irreversibleAction must be Finalize"
type ControlPlaneNetworkTransitionStatus struct {
	TransitionStatus `json:",inline"`
	// LBSNATReservations records accepted reservations and freezes the corresponding SNAT inputs.
	// Retain checkpoints for recovery; they do not themselves reserve or release any address.
	// +optional
	// +listType=map
	// +listMapKey=ipFamily
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:XValidation:rule="oldSelf.all(r, self.exists(n, n.ipFamily == r.ipFamily))",message="accepted LB SNAT reservation checkpoints cannot be removed"
	LBSNATReservations []LBSNATReservationStatus `json:"lbSnatReservations,omitempty"`
	// DetectedSourceLBProvider identifies the discovered source load balancer provider.
	// In 9.2, HAPROXY and VSPHERE_FOUNDATION (FLB) are supported.
	// Unsupported discovered providers are reported through failure conditions.
	// +optional
	// +kubebuilder:validation:Enum=HAPROXY;VSPHERE_FOUNDATION
	DetectedSourceLBProvider LoadBalancerProvider `json:"detectedSourceLbProvider,omitempty"`
	// TargetLBProvider identifies the resolved target provider; only NSX_LB is supported in 9.2.
	// +optional
	// +kubebuilder:validation:Enum=NSX_LB
	TargetLBProvider LoadBalancerProvider `json:"targetLbProvider,omitempty"`
	// TargetNSXProject is the full path of the resolved pre-existing NSX project.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	TargetNSXProject string `json:"targetNsxProject,omitempty"`
	// PrimaryVPCPath is the full path of the infrastructure VPC.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	PrimaryVPCPath string `json:"primaryVpcPath,omitempty"`
	// LoadBalancerVPCPath is the full path of the load balancer VPC.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	LoadBalancerVPCPath string `json:"loadBalancerVpcPath,omitempty"`
}

// ControlPlaneNetworkTransition requests Stage 1 infrastructure transition and Stage 3 finalization.
// Rollback is unsupported; failed actions retain checkpoints for forward retry or WCP backup and restore.
// +kubebuilder:validation:XValidation:rule="self.spec.action == 'Evaluate' || (((has(self.spec.networkConfiguration) && has(self.spec.networkConfiguration.lbSnatIPv4)) == (has(oldSelf.spec.networkConfiguration) && has(oldSelf.spec.networkConfiguration.lbSnatIPv4)) && (!(has(oldSelf.spec.networkConfiguration) && has(oldSelf.spec.networkConfiguration.lbSnatIPv4)) || self.spec.networkConfiguration.lbSnatIPv4 == oldSelf.spec.networkConfiguration.lbSnatIPv4)) && ((has(self.spec.networkConfiguration) && has(self.spec.networkConfiguration.lbSnatIPv6)) == (has(oldSelf.spec.networkConfiguration) && has(oldSelf.spec.networkConfiguration.lbSnatIPv6)) && (!(has(oldSelf.spec.networkConfiguration) && has(oldSelf.spec.networkConfiguration.lbSnatIPv6)) || self.spec.networkConfiguration.lbSnatIPv6 == oldSelf.spec.networkConfiguration.lbSnatIPv6)))",message="LB SNAT inputs may only be added, corrected or removed with Evaluate"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSnatReservations) || !oldSelf.status.lbSnatReservations.exists(r, r.ipFamily == 'IPv4') || ((has(self.spec.networkConfiguration) && has(self.spec.networkConfiguration.lbSnatIPv4)) == (has(oldSelf.spec.networkConfiguration) && has(oldSelf.spec.networkConfiguration.lbSnatIPv4)) && (!(has(oldSelf.spec.networkConfiguration) && has(oldSelf.spec.networkConfiguration.lbSnatIPv4)) || self.spec.networkConfiguration.lbSnatIPv4 == oldSelf.spec.networkConfiguration.lbSnatIPv4))",message="lbSnatIPv4 cannot change after its reservation is accepted"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSnatReservations) || !oldSelf.status.lbSnatReservations.exists(r, r.ipFamily == 'IPv6') || ((has(self.spec.networkConfiguration) && has(self.spec.networkConfiguration.lbSnatIPv6)) == (has(oldSelf.spec.networkConfiguration) && has(oldSelf.spec.networkConfiguration.lbSnatIPv6)) && (!(has(oldSelf.spec.networkConfiguration) && has(oldSelf.spec.networkConfiguration.lbSnatIPv6)) || self.spec.networkConfiguration.lbSnatIPv6 == oldSelf.spec.networkConfiguration.lbSnatIPv6))",message="lbSnatIPv6 cannot change after its reservation is accepted"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSnatReservations) || (has(self.status) && has(self.status.lbSnatReservations))",message="accepted LB SNAT reservation checkpoints must be retained"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSnatReservations) || (has(self.spec.targetLbProvider) == has(oldSelf.spec.targetLbProvider) && (!has(oldSelf.spec.targetLbProvider) || self.spec.targetLbProvider == oldSelf.spec.targetLbProvider))",message="targetLbProvider cannot change after an LB SNAT reservation is accepted"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSnatReservations) || (has(self.spec.nsxProjectId) == has(oldSelf.spec.nsxProjectId) && (!has(oldSelf.spec.nsxProjectId) || self.spec.nsxProjectId == oldSelf.spec.nsxProjectId))",message="nsxProjectId cannot change after an LB SNAT reservation is accepted"
// +kubebuilder:validation:XValidation:rule="self.spec.action == 'Evaluate' || (has(self.spec.networkConfiguration) == has(oldSelf.spec.networkConfiguration) && (!has(oldSelf.spec.networkConfiguration) || self.spec.networkConfiguration == oldSelf.spec.networkConfiguration))",message="networkConfiguration may only change with Evaluate"
// +kubebuilder:validation:XValidation:rule="self.spec.action == 'Evaluate' || (has(self.spec.targetLbProvider) == has(oldSelf.spec.targetLbProvider) && (!has(oldSelf.spec.targetLbProvider) || self.spec.targetLbProvider == oldSelf.spec.targetLbProvider))",message="targetLbProvider may only change with Evaluate"
// +kubebuilder:validation:XValidation:rule="self.spec.action == 'Evaluate' || (has(self.spec.nsxProjectId) == has(oldSelf.spec.nsxProjectId) && (!has(oldSelf.spec.nsxProjectId) || self.spec.nsxProjectId == oldSelf.spec.nsxProjectId))",message="nsxProjectId may only change with Evaluate"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['Plan', 'Derive', 'Execute', 'Rollback', 'Finalize']) || (has(self.spec.targetLbProvider) == has(oldSelf.spec.targetLbProvider) && (!has(oldSelf.spec.targetLbProvider) || self.spec.targetLbProvider == oldSelf.spec.targetLbProvider))",message="targetLbProvider cannot change after planning starts"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['Plan', 'Derive', 'Execute', 'Rollback', 'Finalize']) || (has(self.spec.nsxProjectId) == has(oldSelf.spec.nsxProjectId) && (!has(oldSelf.spec.nsxProjectId) || self.spec.nsxProjectId == oldSelf.spec.nsxProjectId))",message="nsxProjectId cannot change after planning starts"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['Plan', 'Derive', 'Execute', 'Rollback', 'Finalize']) || (has(self.spec.networkConfiguration) == has(oldSelf.spec.networkConfiguration) && (!has(oldSelf.spec.networkConfiguration) || self.spec.networkConfiguration == oldSelf.spec.networkConfiguration))",message="networkConfiguration cannot change after planning starts"
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
	// ObjectMeta identifies this transition.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec describes the requested transition.
	// +required
	Spec ControlPlaneNetworkTransitionSpec `json:"spec,omitzero"`
	// Status reports observed progress.
	// +optional
	Status *ControlPlaneNetworkTransitionStatus `json:"status,omitempty"`
}

// ControlPlaneNetworkTransitionList contains infrastructure transitions.
// +kubebuilder:object:root=true
type ControlPlaneNetworkTransitionList struct {
	metav1.TypeMeta `json:",inline"`
	// ListMeta describes this list.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items contains the transitions.
	// +required
	Items []ControlPlaneNetworkTransition `json:"items"`
}
