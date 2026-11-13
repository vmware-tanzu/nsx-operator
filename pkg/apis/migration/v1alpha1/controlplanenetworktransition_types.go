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
// +kubebuilder:validation:XValidation:rule="!has(self.networkConfiguration) || !has(self.networkConfiguration.lbSNATIPs) || size(self.networkConfiguration.lbSNATIPs) == 0 || !has(self.targetLBProvider) || self.targetLBProvider == 'NSX_LB'",message="LB SNAT IP inputs apply only to the NSX_LB target provider"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.nsxProjectID) || (has(self.nsxProjectID) && self.nsxProjectID == oldSelf.nsxProjectID)",message="nsxProjectID cannot change once supplied"
type ControlPlaneNetworkTransitionSpec struct {
	TransitionSpec `json:",inline"`
	// TargetLBProvider selects the desired target load balancer provider.
	// In 9.2, only NSX_LB is supported as the target for FLB sources.
	// If omitted, evaluation resolves an unambiguous supported/configured target.
	// Evaluation resolves the target provider; the selection cannot change after execution starts.
	// +optional
	// +kubebuilder:validation:Enum=NSX_LB
	TargetLBProvider LoadBalancerProvider `json:"targetLBProvider,omitempty"`
	// NSXProjectID selects a pre-existing NSX project; omission selects the default project.
	// Once supplied, the ID cannot be changed or removed. Omission is fixed when execution starts.
	// +optional
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:MinLength=1
	NSXProjectID string `json:"nsxProjectID,omitempty"`
	// NetworkConfiguration supplies administrator-provided migration parameters.
	// Values may be added, corrected, or removed during Evaluate before execution.
	// After status.lbSNATIPAllocations records an accepted allocation, LBSNATIPs
	// cannot be added, changed, or removed, including during Evaluate.
	// Changes require requested action Evaluate; requesting Execute freezes inputs,
	// and a recorded execution checkpoint prevents reopening them.
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
	// service when the backend network is separate from VIP and workload networks.
	// Supervisor VDS source networks are IPv4-only in this release. Keep this as a
	// slice so a later release can add IPv6 without changing the field shape.
	// The migration service validates backend membership and checks for conflicts
	// where possible. The administrator is responsible for address availability
	// when exclusive ownership cannot be verified. After status.lbSNATIPAllocations
	// records an accepted address,
	// this list cannot be added, changed, or removed, including during Evaluate.
	// Requesting Execute prevents changes to the entire NetworkConfiguration.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=1
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=45
	// +kubebuilder:validation:XValidation:rule="self.all(address, isIP(address) && ip(address).family() == 4)",message="lbSNATIPs must contain only plain IPv4 addresses for Supervisor VDS migration"
	LBSNATIPs []string `json:"lbSNATIPs,omitempty"`
	// ControlPlaneAndServiceDHCPNetworks describes existing DHCP-backed networks used by
	// control-plane VMs and Supervisor Services.
	// +optional
	// +listType=map
	// +listMapKey=vdnName
	// +kubebuilder:validation:MaxItems=1000
	ControlPlaneAndServiceDHCPNetworks []DHCPNetworkConfiguration `json:"controlPlaneAndServiceDHCPNetworks,omitempty"`
}

// ControlPlaneResourceNaming supplies optional names for infrastructure resources
// created for migration. Each supplied name is also used as the NSX resource ID.
// Evaluate discovers all CPVM and Supervisor Service VDNs; resources without
// overrides use deterministic generated names.
type ControlPlaneResourceNaming struct {
	// Networks supplies optional naming overrides per infrastructure VDN.
	// It includes static and DHCP-backed CPVM and Supervisor Service networks.
	// +optional
	// +listType=map
	// +listMapKey=vdnName
	// +kubebuilder:validation:MaxItems=1000
	Networks []VDNResourceNaming `json:"networks,omitempty"`
	// LoadBalancerVPCName overrides the generated name of the separate LB VPC.
	// +optional
	LoadBalancerVPCName NSXResourceName `json:"loadBalancerVPCName,omitempty"`
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
	// LBSNATIPAllocations records the accepted IPv4 LB SNAT address. Once recorded,
	// spec.networkConfiguration.lbSNATIPs cannot be added, changed, or removed
	// during Evaluate. spec.targetLBProvider and spec.nsxProjectID must also
	// remain unchanged. The accepted allocation cannot be changed or removed;
	// recording it does not itself allocate or release an address.
	// +optional
	// +listType=map
	// +listMapKey=ipAddress
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	// +kubebuilder:validation:XValidation:rule="oldSelf.all(r, self.exists(n, n.ipAddress == r.ipAddress))",message="accepted LB SNAT allocation checkpoints cannot be removed"
	// +kubebuilder:validation:XValidation:rule="self.all(a, isIP(a.ipAddress) && ip(a.ipAddress).family() == 4)",message="Supervisor VDS migration supports only IPv4 LB SNAT allocations"
	LBSNATIPAllocations []IPAllocationStatus `json:"lbSNATIPAllocations,omitempty"`
	// SourceLBProvider identifies the discovered source load balancer provider.
	// In 9.2, only VSPHERE_FOUNDATION (FLB) is supported.
	// Unsupported discovered providers are reported through failure conditions.
	// +optional
	// +kubebuilder:validation:Enum=VSPHERE_FOUNDATION
	SourceLBProvider LoadBalancerProvider `json:"sourceLBProvider,omitempty"`
	// TargetLBProvider identifies the resolved target provider; only NSX_LB is supported in 9.2.
	// +optional
	// +kubebuilder:validation:Enum=NSX_LB
	TargetLBProvider LoadBalancerProvider `json:"targetLBProvider,omitempty"`
	// TargetNSXProjectPath is the full path of the resolved pre-existing NSX project.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	TargetNSXProjectPath string `json:"targetNSXProjectPath,omitempty"`
	// PrimaryVPCPath is the full path of the infrastructure VPC.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	PrimaryVPCPath string `json:"primaryVPCPath,omitempty"`
	// LoadBalancerVPCPath is the full path of the load balancer VPC.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	LoadBalancerVPCPath string `json:"loadBalancerVPCPath,omitempty"`
}

// ControlPlaneNetworkTransition requests Stage 1 infrastructure transition and Stage 3 finalization.
// Rollback is unsupported; failed actions retain checkpoints for forward retry or WCP backup and restore.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSNATIPAllocations) || !oldSelf.status.lbSNATIPAllocations.exists(r, ip(r.ipAddress).family() == 4) || ((has(self.spec.networkConfiguration) && has(self.spec.networkConfiguration.lbSNATIPs)) ? self.spec.networkConfiguration.lbSNATIPs.filter(address, ip(address).family() == 4) : []) == ((has(oldSelf.spec.networkConfiguration) && has(oldSelf.spec.networkConfiguration.lbSNATIPs)) ? oldSelf.spec.networkConfiguration.lbSNATIPs.filter(address, ip(address).family() == 4) : [])",message="IPv4 LB SNAT input cannot change after its allocation is accepted"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSNATIPAllocations) || (has(self.status) && has(self.status.lbSNATIPAllocations))",message="accepted LB SNAT allocation checkpoints must be retained"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSNATIPAllocations) || (has(self.spec.targetLBProvider) == has(oldSelf.spec.targetLBProvider) && (!has(oldSelf.spec.targetLBProvider) || self.spec.targetLBProvider == oldSelf.spec.targetLBProvider))",message="targetLBProvider cannot change after an LB SNAT allocation is accepted"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lbSNATIPAllocations) || (has(self.spec.nsxProjectID) == has(oldSelf.spec.nsxProjectID) && (!has(oldSelf.spec.nsxProjectID) || self.spec.nsxProjectID == oldSelf.spec.nsxProjectID))",message="nsxProjectID cannot change after an LB SNAT allocation is accepted"
// +kubebuilder:validation:XValidation:rule="self.spec.action == 'Evaluate' || (has(self.spec.networkConfiguration) == has(oldSelf.spec.networkConfiguration) && (!has(oldSelf.spec.networkConfiguration) || self.spec.networkConfiguration == oldSelf.spec.networkConfiguration))",message="networkConfiguration may only change with Evaluate"
// +kubebuilder:validation:XValidation:rule="self.spec.action == 'Evaluate' || (has(self.spec.targetLBProvider) == has(oldSelf.spec.targetLBProvider) && (!has(oldSelf.spec.targetLBProvider) || self.spec.targetLBProvider == oldSelf.spec.targetLBProvider))",message="targetLBProvider may only change with Evaluate"
// +kubebuilder:validation:XValidation:rule="self.spec.action == 'Evaluate' || (has(self.spec.nsxProjectID) == has(oldSelf.spec.nsxProjectID) && (!has(oldSelf.spec.nsxProjectID) || self.spec.nsxProjectID == oldSelf.spec.nsxProjectID))",message="nsxProjectID may only change with Evaluate"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['Execute', 'Rollback', 'Finalize']) || (has(self.spec.targetLBProvider) == has(oldSelf.spec.targetLBProvider) && (!has(oldSelf.spec.targetLBProvider) || self.spec.targetLBProvider == oldSelf.spec.targetLBProvider))",message="targetLBProvider cannot change after execution starts"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['Execute', 'Rollback', 'Finalize']) || (has(self.spec.nsxProjectID) == has(oldSelf.spec.nsxProjectID) && (!has(oldSelf.spec.nsxProjectID) || self.spec.nsxProjectID == oldSelf.spec.nsxProjectID))",message="nsxProjectID cannot change after execution starts"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.status) || !has(oldSelf.status.lastAction) || !(oldSelf.status.lastAction in ['Execute', 'Rollback', 'Finalize']) || (has(self.spec.networkConfiguration) == has(oldSelf.spec.networkConfiguration) && (!has(oldSelf.spec.networkConfiguration) || self.spec.networkConfiguration == oldSelf.spec.networkConfiguration))",message="networkConfiguration cannot change after execution starts"
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
