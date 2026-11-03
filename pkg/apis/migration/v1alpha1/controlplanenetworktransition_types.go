/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ControlPlaneNetworkTransitionName identifies the sole infrastructure transition in a Supervisor.
const ControlPlaneNetworkTransitionName = "supervisor-vds-to-vpc"

// ControlPlaneNetworkTransitionSpec requests infrastructure network migration.
// Infrastructure supports forward retry; reversion uses user-operated WCP backup and restore.
// +kubebuilder:validation:XValidation:rule="self.action != 'Rollback'",message="infrastructure Rollback is unsupported; use WCP backup and restore"
// +kubebuilder:validation:XValidation:rule="self.action != 'Commit'",message="Commit is a Namespace operation"
// +kubebuilder:validation:XValidation:rule="!has(self.networkConfiguration) || !has(self.networkConfiguration.lbSnatIPs) || size(self.networkConfiguration.lbSnatIPs) == 0 || !has(self.targetLbProvider) || self.targetLbProvider == 'NSX_LB'",message="LB SNAT IP inputs apply only to the NSX_LB target provider"
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
	// Accepted SNAT reservations freeze the IPv4 fallback address.
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

	// LBSNATIPs optionally supplies a backend IPv4 address for the shared NSX LB
	// service when the IPv4 VIP pool cannot provide a reservation during Evaluate.
	// Supervisor VDS source networks are IPv4-only in this release. Keep this as a
	// slice so a later release can add IPv6 without changing the field shape.
	// The migration service validates availability and reserves the supplied address
	// before use. An accepted allocation freezes this input; Plan freezes the entire configuration.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=1
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=45
	// +kubebuilder:validation:XValidation:rule="self.all(address, isIP(address) && ip(address).family() == 4)",message="lbSnatIPs must contain only plain IPv4 addresses for Supervisor VDS migration"
	LBSNATIPs []string `json:"lbSnatIPs,omitempty"`
	// ControlPlaneAndServiceDHCPNetworks describes existing DHCP-backed networks used by
	// control-plane VMs and Supervisor Services.
	// +optional
	// +listType=map
	// +listMapKey=vdnName
	// +kubebuilder:validation:MaxItems=1000
	ControlPlaneAndServiceDHCPNetworks []DHCPNetworkConfiguration `json:"controlPlaneAndServiceDHCPNetworks,omitempty"`
	// SourceLBFrontendDVPGs identifies the Supervisor frontend port groups on VDS
	// used by the source load balancer. HAProxy requires these inputs; other source
	// providers may use them when supported. FLB discovers its frontend networks.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=1000
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=256
	SourceLBFrontendDVPGs []string `json:"sourceLbFrontendDvpgs,omitempty"`
}

// IPAllocationStatus records an accepted LB SNAT IP allocation checkpoint.
// The controller verifies ownership and readiness before persisting it.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="accepted LB SNAT allocation identity is immutable"
// +kubebuilder:validation:XValidation:rule="isIP(self.ipAddress)",message="allocated address must be a plain IP address"
type IPAllocationStatus struct {
	// IPAddress is the address accepted for LB SNAT, from a source allocation or user input.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=45
	IPAddress string `json:"ipAddress"`
	// AllocationRef identifies the source allocation when one was created.
	// It uses the shared NamespacedName type and is omitted for an administrator-supplied
	// backend address without a source allocation.
	// +optional
	AllocationRef *NamespacedName `json:"allocationRef,omitempty"`
}

// ControlPlaneNetworkTransitionStatus records infrastructure discovery and realized topology.
// +kubebuilder:validation:XValidation:rule="!has(self.irreversibleAction) || self.irreversibleAction == 'Finalize'",message="infrastructure irreversibleAction must be Finalize"
type ControlPlaneNetworkTransitionStatus struct {
	TransitionStatus `json:",inline"`
	// LBSNATIPAllocations records the accepted IPv4 allocation and locks its input
	// against edits. Retain this checkpoint for recovery; it does not itself
	// reserve or release any address.
	// +optional
	// +listType=map
	// +listMapKey=ipAddress
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	// +kubebuilder:validation:XValidation:rule="oldSelf.all(r, self.exists(n, n.ipAddress == r.ipAddress))",message="accepted LB SNAT allocation checkpoints cannot be removed"
	// +kubebuilder:validation:XValidation:rule="self.all(a, isIP(a.ipAddress) && ip(a.ipAddress).family() == 4)",message="Supervisor VDS migration supports only IPv4 LB SNAT allocations"
	LBSNATIPAllocations []IPAllocationStatus `json:"lbSnatIPAllocations,omitempty"`
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
	// TargetNSXProjectPath is the full path of the resolved pre-existing NSX project.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	TargetNSXProjectPath string `json:"targetNsxProjectPath,omitempty"`
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
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSnatIPAllocations) || !oldSelf.status.lbSnatIPAllocations.exists(r, ip(r.ipAddress).family() == 4) || ((has(self.spec.networkConfiguration) && has(self.spec.networkConfiguration.lbSnatIPs)) ? self.spec.networkConfiguration.lbSnatIPs.filter(address, ip(address).family() == 4) : []) == ((has(oldSelf.spec.networkConfiguration) && has(oldSelf.spec.networkConfiguration.lbSnatIPs)) ? oldSelf.spec.networkConfiguration.lbSnatIPs.filter(address, ip(address).family() == 4) : [])",message="IPv4 LB SNAT input cannot change after its allocation is accepted"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSnatIPAllocations) || (has(self.status) && has(self.status.lbSnatIPAllocations))",message="accepted LB SNAT allocation checkpoints must be retained"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSnatIPAllocations) || (has(self.spec.targetLbProvider) == has(oldSelf.spec.targetLbProvider) && (!has(oldSelf.spec.targetLbProvider) || self.spec.targetLbProvider == oldSelf.spec.targetLbProvider))",message="targetLbProvider cannot change after an LB SNAT allocation is accepted"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSnatIPAllocations) || (has(self.spec.nsxProjectId) == has(oldSelf.spec.nsxProjectId) && (!has(oldSelf.spec.nsxProjectId) || self.spec.nsxProjectId == oldSelf.spec.nsxProjectId))",message="nsxProjectId cannot change after an LB SNAT allocation is accepted"
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
