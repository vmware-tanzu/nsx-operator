/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Action selects an explicit migration operation.
// +kubebuilder:validation:Enum=Evaluate;Plan;Derive;Execute;Rollback;Commit;Finalize
type Action string

const (
	// ActionEvaluate discovers prerequisites and missing inputs.
	ActionEvaluate Action = "Evaluate"
	// ActionPlan generates a blueprint without changing the dataplane.
	ActionPlan Action = "Plan"
	// ActionDerive provisions the approved network topology.
	ActionDerive Action = "Derive"
	// ActionExecute cuts workloads over to the target network.
	ActionExecute Action = "Execute"
	// ActionRollback restores an eligible namespace batch before its Commit checkpoint.
	// Infrastructure transitions do not support Rollback.
	ActionRollback Action = "Rollback"
	// ActionCommit commits a verified namespace batch and closes its rollback window.
	ActionCommit Action = "Commit"
	// ActionFinalize commits remaining infrastructure workflows and retires legacy resources.
	ActionFinalize Action = "Finalize"
)

// ActionState describes the execution outcome of the last checkpointed action.
// +kubebuilder:validation:Enum=Running;Succeeded;Failed
type ActionState string

const (
	// ActionStateRunning means the action is checkpointed or still in progress.
	ActionStateRunning ActionState = "Running"
	// ActionStateSucceeded means the action finished successfully.
	ActionStateSucceeded ActionState = "Succeeded"
	// ActionStateFailed means the action failed and may have left partial changes.
	ActionStateFailed ActionState = "Failed"
)

// TransitionSpec identifies the source, target and requested operation.
type TransitionSpec struct {
	// SourceMode is the source network stack.
	// +required
	// +kubebuilder:validation:Enum=VSPHERE_VDS
	// +kubebuilder:validation:MinLength=1
	SourceMode string `json:"sourceMode,omitempty"`
	// TargetMode is the target network stack.
	// +required
	// +kubebuilder:validation:Enum=NSX_VPC
	// +kubebuilder:validation:MinLength=1
	TargetMode string `json:"targetMode,omitempty"`
	// Action selects the operation to reconcile. Operations never auto-advance.
	// +required
	Action Action `json:"action,omitempty"`
}

// DHCPNetworkConfiguration describes existing addressing for a DHCP-backed source VDN.
// It does not request a new DHCP network. Values may be corrected during Evaluate;
// the parent transition freezes configuration when planning starts.
type DHCPNetworkConfiguration struct {
	// VDNName identifies a discovered source VSphereDistributedNetwork within the Supervisor.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	VDNName string `json:"vdnName,omitempty"`
	// DHCPRange describes the existing inclusive DHCP pool, not free-IP inventory.
	// +optional
	DHCPRange *IPAddressRange `json:"dhcpRange,omitempty"`
	// SubnetMask is a contiguous dotted-decimal IPv4 mask used to derive the subnet CIDR.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=15
	// +kubebuilder:validation:Pattern="^((255[.])*(254|252|248|240|224|192|128|0)([.]0)*|255[.]255[.]255[.]255)$"
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="subnetMask must be a contiguous IPv4 mask in dotted-decimal form"
	SubnetMask string `json:"subnetMask,omitempty"`
	// GatewayAddress supplies a plain gateway IP address without a prefix length.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=45
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="gatewayAddress must be a plain IP address"
	GatewayAddress string `json:"gatewayAddress,omitempty"`
}

// IPAddressRange describes inclusive lower and upper IP address bounds.
type IPAddressRange struct {
	// Start is the first IP address in the range.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=45
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="start must be a plain IP address"
	Start string `json:"start,omitempty"`
	// End is the last IP address in the range.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=45
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="end must be a plain IP address"
	End string `json:"end,omitempty"`
}

// LoadBalancerProvider identifies a requested or discovered load balancer implementation.
// Field-specific enums restrict the providers supported by the migration release.
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=32
type LoadBalancerProvider string

// RequiredInput describes administrator input needed to proceed.
type RequiredInput struct {
	// Key identifies the missing input.
	// +required
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key,omitempty"`
	// Description explains how to supply the input.
	// +required
	// +kubebuilder:validation:MaxLength=4096
	// +kubebuilder:validation:MinLength=1
	Description string `json:"description,omitempty"`
}

// TransitionStatus persists shared progress and resumable workflow state.
type TransitionStatus struct {
	// Conditions report the current operation's readiness and failures.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=32
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the desired-state generation last inspected, even if its action was blocked.
	// It indicates status freshness and does not mean the action ran or succeeded.
	// +optional
	// +kubebuilder:validation:Minimum=1
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Phase describes current progress; Blocked means prerequisites or handlers are unavailable.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:MinLength=1
	Phase string `json:"phase,omitempty"`
	// LastAction identifies the most recently checkpointed operation, regardless of outcome.
	// +optional
	LastAction Action `json:"lastAction,omitempty"`
	// ActionState reports whether lastAction is running, succeeded, or failed.
	// +optional
	ActionState ActionState `json:"actionState,omitempty"`
	// ActionGeneration is the desired-state generation checkpointed for lastAction.
	// It stays unchanged when a newer request is blocked, so previous success cannot satisfy that request.
	// +optional
	// +kubebuilder:validation:Minimum=1
	ActionGeneration int64 `json:"actionGeneration,omitempty"`
	// IrreversibleAction identifies the action whose point of no return has been persisted.
	// It is set only after prerequisite checks and before irreversible external changes.
	// Once set, only that action may be resumed and this checkpoint cannot be cleared.
	// +optional
	// +kubebuilder:validation:Enum=Commit;Finalize
	IrreversibleAction string `json:"irreversibleAction,omitempty"`
	// NSXWorkflows records the exact external workflow handle for each source VDN.
	// +optional
	// +listType=map
	// +listMapKey=vdnName
	// +kubebuilder:validation:MaxItems=1000
	NSXWorkflows []NSXWorkflowStatus `json:"nsxWorkflows,omitempty"`
	// RequiredInputs describe administrator inputs required before proceeding.
	// +optional
	// +listType=map
	// +listMapKey=key
	// +kubebuilder:validation:MaxItems=1000
	RequiredInputs []RequiredInput `json:"requiredInputs,omitempty"`
	// OptionalInputs describe optional overrides the UI may offer during Evaluate.
	// Omitting these inputs uses defaults and does not block evaluation. Handlers
	// publish/clear them with RequiredInputs for ObservedGeneration, remove frozen
	// resource names, and revalidate live eligibility before accepting overrides.
	// This is advisory UI information, not a resource inventory or a review gate.
	// +optional
	// +listType=map
	// +listMapKey=key
	// +kubebuilder:validation:MaxItems=5000
	OptionalInputs []OptionalInput `json:"optionalInputs,omitempty"`
}

// NSXWorkflowStatus records a resumable NSX conversion workflow for one source VDN.
type NSXWorkflowStatus struct {
	// VDNName identifies the source VSphereDistributedNetwork for this workflow.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	VDNName string `json:"vdnName,omitempty"`
	// WorkflowID is the persisted NSX handle used for retries, rollback, and cleanup.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	WorkflowID string `json:"workflowId,omitempty"`
}

// NSXResourceName is used as both NSX resource ID and display name.
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=255
// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_.-]+$`
// +kubebuilder:validation:XValidation:rule="self != '.' && self != '..'",message="resource name cannot be a dot path segment"
type NSXResourceName string

// ControlPlaneResourceNaming supplies optional IDs for infrastructure resources
// created for migration. Evaluate discovers all CPVM and Supervisor Service VDNs;
// resources without overrides use deterministic generated names.
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
	LoadBalancerVPCName NSXResourceName `json:"loadBalancerVpcName,omitempty"`
}

// NamespaceResourceNaming supplies optional IDs for new workload topology.
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

// VDNResourceNaming identifies a source VDN by its Kubernetes name, not an
// internal resource key. Shared topology must resolve to consistent names.
// Omitted names use deterministic generated names and do not block Evaluate.
type VDNResourceNaming struct {
	// VDNName identifies a source VSphereDistributedNetwork used by the batch.
	// Unknown VDNs and conflicting names for shared topology are rejected by Evaluate.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	VDNName string `json:"vdnName"`
	// TGWName overrides the generated name when a new same-span TGW is needed.
	// +optional
	TGWName NSXResourceName `json:"tgwName,omitempty"`
	// VPCName overrides the generated name of a new VPC for this VDN.
	// +optional
	VPCName NSXResourceName `json:"vpcName,omitempty"`
	// DVCName overrides the generated name when a new DVC is needed.
	// +optional
	DVCName NSXResourceName `json:"dvcName,omitempty"`
	// SubnetName overrides the generated name of a new VLAN extension subnet.
	// All backing DVPGs must use the same discovered VLAN and share one subnet
	// under the resolved VPC/span. Evaluate must reject inconsistent VLANs.
	// Existing compatible resources are reused without creation names.
	// +optional
	SubnetName NSXResourceName `json:"subnetName,omitempty"`
}

// OptionalInput describes an optional administrator input; omission uses defaults.
// It has the same key/description shape as RequiredInput without blocking progress.
type OptionalInput struct {
	// Key identifies the spec field the UI can offer, including a VDN selector.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Key string `json:"key"`
	// Description identifies the VDN/resource and explains the default on omission.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	Description string `json:"description"`
}
