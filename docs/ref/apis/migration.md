# API Reference

## Packages
- [migration.network.vmware.com/v1alpha1](#migrationnetworkvmwarecomv1alpha1)


## migration.network.vmware.com/v1alpha1

Package v1alpha1 defines Supervisor migration orchestration and network resource transfer APIs.

Package v1alpha1 defines the Supervisor network transition API.

### Resource Types
- [ControlPlaneNetworkTransition](#controlplanenetworktransition)
- [NamespaceNetworkTransition](#namespacenetworktransition)
- [NetworkResourceTransition](#networkresourcetransition)



#### Action

_Underlying type:_ _string_

Action selects an explicit migration operation.

_Validation:_
- Enum: [Evaluate Plan Derive Execute Rollback Commit Finalize]

_Appears in:_
- [ControlPlaneNetworkTransitionSpec](#controlplanenetworktransitionspec)
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionSpec](#namespacenetworktransitionspec)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)
- [TransitionSpec](#transitionspec)
- [TransitionStatus](#transitionstatus)

| Field | Description |
| --- | --- |
| `Evaluate` | ActionEvaluate discovers prerequisites and missing inputs.<br /> |
| `Plan` | ActionPlan generates a blueprint without changing the dataplane.<br /> |
| `Derive` | ActionDerive provisions the approved network topology.<br /> |
| `Execute` | ActionExecute migrates workloads to the target network.<br /> |
| `Rollback` | ActionRollback restores an eligible Namespace batch before its Commit checkpoint.<br />Infrastructure transitions do not support Rollback.<br /> |
| `Commit` | ActionCommit commits a verified Namespace batch and closes its rollback window.<br /> |
| `Finalize` | ActionFinalize commits remaining infrastructure workflows and retires legacy resources.<br /> |


#### ActionState

_Underlying type:_ _string_

ActionState describes the execution outcome of the last checkpointed action.

_Validation:_
- Enum: [Running Succeeded Failed]

_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)
- [TransitionStatus](#transitionstatus)

| Field | Description |
| --- | --- |
| `Running` | ActionStateRunning means the action is checkpointed or still in progress.<br /> |
| `Succeeded` | ActionStateSucceeded means the action finished successfully.<br /> |
| `Failed` | ActionStateFailed means the action failed and may have left partial changes.<br /> |


#### ControlPlaneNetworkConfiguration



ControlPlaneNetworkConfiguration supplies network and load balancer settings discovered during evaluation.



_Appears in:_
- [ControlPlaneNetworkTransitionSpec](#controlplanenetworktransitionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `resourceNaming` _[ControlPlaneResourceNaming](#controlplaneresourcenaming)_ | ResourceNaming supplies optional IDs for NSX resources created for migration.<br />Omitted names use deterministic generated names. Naming inputs never rename<br />or implicitly adopt existing resources and are not requiredInputs. |  |  |
| `lbSNATIPs` _string array_ | LBSNATIPs optionally supplies a backend IPv4 address for the shared NSX LB<br />service when the IPv4 VIP pool cannot provide a reservation during Evaluate.<br />Supervisor VDS source networks are IPv4-only in this release. Keep this as a<br />slice so a later release can add IPv6 without changing the field shape.<br />The migration service validates availability and reserves the supplied address<br />before use. After status.lbSNATIPAllocations records an accepted allocation,<br />this list cannot be added, changed, or removed, including during Evaluate.<br />Requesting Plan prevents changes to the entire NetworkConfiguration. |  | MaxItems: 1 <br /> |
| `controlPlaneAndServiceDHCPNetworks` _[DHCPNetworkConfiguration](#dhcpnetworkconfiguration) array_ | ControlPlaneAndServiceDHCPNetworks describes existing DHCP-backed networks used by<br />control-plane VMs and Supervisor Services. |  | MaxItems: 1000 <br /> |


#### ControlPlaneNetworkTransition



ControlPlaneNetworkTransition requests Stage 1 infrastructure transition and Stage 3 finalization.
Rollback is unsupported; failed actions retain checkpoints for forward retry or WCP backup and restore.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `migration.network.vmware.com/v1alpha1` | | |
| `kind` _string_ | `ControlPlaneNetworkTransition` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ControlPlaneNetworkTransitionSpec](#controlplanenetworktransitionspec)_ | Spec describes the requested transition. |  |  |
| `status` _[ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)_ | Status reports observed progress. |  |  |


#### ControlPlaneNetworkTransitionSpec



ControlPlaneNetworkTransitionSpec requests infrastructure network migration.
Infrastructure supports forward retry; reversion uses user-operated WCP backup and restore.



_Appears in:_
- [ControlPlaneNetworkTransition](#controlplanenetworktransition)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceMode` _string_ | SourceMode is the source network stack. |  | Enum: [VSPHERE_VDS] <br />MinLength: 1 <br /> |
| `targetMode` _string_ | TargetMode is the target network stack. |  | Enum: [NSX_VPC] <br />MinLength: 1 <br /> |
| `action` _[Action](#action)_ | Action selects the operation to reconcile. Operations never auto-advance. |  | Enum: [Evaluate Plan Derive Execute Rollback Commit Finalize] <br /> |
| `targetLBProvider` _[LoadBalancerProvider](#loadbalancerprovider)_ | TargetLBProvider selects the desired target load balancer provider.<br />In 9.2, only NSX_LB is supported as the target for FLB sources.<br />If omitted, evaluation resolves an unambiguous supported/configured target.<br />The resolved selection is bound to the reviewed plan and cannot change after planning starts. |  | Enum: [NSX_LB] <br />MaxLength: 32 <br />MinLength: 1 <br /> |
| `nsxProjectID` _string_ | NSXProjectID selects a pre-existing NSX project; omission selects the default project.<br />Once supplied, the ID cannot be changed or removed. Omission is fixed when planning starts. |  | MaxLength: 256 <br />MinLength: 1 <br /> |
| `networkConfiguration` _[ControlPlaneNetworkConfiguration](#controlplanenetworkconfiguration)_ | NetworkConfiguration supplies administrator-provided migration parameters.<br />Values may be added, corrected, or removed during Evaluate before planning.<br />After status.lbSNATIPAllocations records an accepted allocation, LBSNATIPs<br />cannot be added, changed, or removed, including during Evaluate.<br />Changes require requested action Evaluate; requesting Plan freezes inputs,<br />and a recorded planning checkpoint prevents reopening them. |  |  |


#### ControlPlaneNetworkTransitionStatus



ControlPlaneNetworkTransitionStatus records infrastructure discovery and realized topology.



_Appears in:_
- [ControlPlaneNetworkTransition](#controlplanenetworktransition)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#condition-v1-meta) array_ | Conditions report the current operation's readiness and failures. |  | MaxItems: 32 <br /> |
| `observedGeneration` _integer_ | ObservedGeneration is the desired-state generation last inspected, even if its action was blocked.<br />It indicates status freshness and does not mean the action ran or succeeded. |  | Minimum: 1 <br /> |
| `phase` _string_ | Phase describes current progress; Blocked means prerequisites or handlers are unavailable. |  | MaxLength: 64 <br />MinLength: 1 <br /> |
| `lastAction` _[Action](#action)_ | LastAction identifies the most recently checkpointed operation, regardless of outcome. |  | Enum: [Evaluate Plan Derive Execute Rollback Commit Finalize] <br /> |
| `actionState` _[ActionState](#actionstate)_ | ActionState reports whether lastAction is running, succeeded, or failed. |  | Enum: [Running Succeeded Failed] <br /> |
| `actionGeneration` _integer_ | ActionGeneration is the desired-state generation checkpointed for lastAction.<br />It stays unchanged when a newer request is blocked, so previous success cannot satisfy that request. |  | Minimum: 1 <br /> |
| `irreversibleAction` _string_ | IrreversibleAction identifies the action whose point of no return has been persisted.<br />It is set only after prerequisite checks and before irreversible external changes.<br />Once set, only that action may be resumed and this checkpoint cannot be cleared. |  | Enum: [Commit Finalize] <br /> |
| `nsxWorkflows` _[NSXWorkflowStatus](#nsxworkflowstatus) array_ | NSXWorkflows records the exact external workflow handle for each source VDN. |  | MaxItems: 1000 <br /> |
| `requiredInputs` _[RequiredInput](#requiredinput) array_ | RequiredInputs describe administrator inputs required before proceeding. |  | MaxItems: 1000 <br /> |
| `optionalInputs` _[OptionalInput](#optionalinput) array_ | OptionalInputs describe optional overrides the UI may offer during Evaluate.<br />Omitting these inputs uses defaults and does not block evaluation. Handlers<br />publish/clear them with RequiredInputs for ObservedGeneration, remove frozen<br />resource names, and revalidate live eligibility before accepting overrides.<br />This is advisory UI information, not a resource inventory or a review gate. |  | MaxItems: 5000 <br /> |
| `lbSNATIPAllocations` _[IPAllocationStatus](#ipallocationstatus) array_ | LBSNATIPAllocations records the accepted IPv4 LB SNAT address. Once recorded,<br />spec.networkConfiguration.lbSNATIPs cannot be added, changed, or removed<br />during Evaluate. spec.targetLBProvider and spec.nsxProjectID must also<br />remain unchanged. The accepted allocation cannot be changed or removed;<br />recording it does not itself allocate or release an address. |  | MaxItems: 1 <br />MinItems: 1 <br /> |
| `sourceLBProvider` _[LoadBalancerProvider](#loadbalancerprovider)_ | SourceLBProvider identifies the discovered source load balancer provider.<br />In 9.2, only VSPHERE_FOUNDATION (FLB) is supported.<br />Unsupported discovered providers are reported through failure conditions. |  | Enum: [VSPHERE_FOUNDATION] <br />MaxLength: 32 <br />MinLength: 1 <br /> |
| `targetLBProvider` _[LoadBalancerProvider](#loadbalancerprovider)_ | TargetLBProvider identifies the resolved target provider; only NSX_LB is supported in 9.2. |  | Enum: [NSX_LB] <br />MaxLength: 32 <br />MinLength: 1 <br /> |
| `targetNSXProjectPath` _string_ | TargetNSXProjectPath is the full path of the resolved pre-existing NSX project. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `primaryVPCPath` _string_ | PrimaryVPCPath is the full path of the infrastructure VPC. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `loadBalancerVPCPath` _string_ | LoadBalancerVPCPath is the full path of the load balancer VPC. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |


#### ControlPlaneResourceNaming



ControlPlaneResourceNaming supplies optional names for infrastructure resources
created for migration. Each supplied name is also used as the NSX resource ID.
Evaluate discovers all CPVM and Supervisor Service VDNs; resources without
overrides use deterministic generated names.



_Appears in:_
- [ControlPlaneNetworkConfiguration](#controlplanenetworkconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `networks` _[VDNResourceNaming](#vdnresourcenaming) array_ | Networks supplies optional naming overrides per infrastructure VDN.<br />It includes static and DHCP-backed CPVM and Supervisor Service networks. |  | MaxItems: 1000 <br /> |
| `loadBalancerVPCName` _[NSXResourceName](#nsxresourcename)_ | LoadBalancerVPCName overrides the generated name of the separate LB VPC. |  | MaxLength: 255 <br />MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.-]+$` <br /> |


#### DHCPNetworkConfiguration



DHCPNetworkConfiguration describes existing addressing for a DHCP-backed source VDN.
It does not request a new DHCP network. Values may be corrected during Evaluate;
the parent transition freezes configuration when planning starts.



_Appears in:_
- [ControlPlaneNetworkConfiguration](#controlplanenetworkconfiguration)
- [NamespaceNetworkConfiguration](#namespacenetworkconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vdnName` _string_ | VDNName identifies a discovered source VSphereDistributedNetwork within the Supervisor. |  | MaxLength: 256 <br />MinLength: 1 <br /> |
| `dhcpRange` _[IPAddressRange](#ipaddressrange)_ | DHCPRange describes the existing inclusive DHCP pool, not free-IP inventory. |  |  |
| `subnetMask` _string_ | SubnetMask is a contiguous dotted-decimal IPv4 mask used to derive the subnet CIDR. |  | MaxLength: 15 <br />MinLength: 1 <br />Pattern: `^((255[.])*(254\|252\|248\|240\|224\|192\|128\|0)([.]0)*\|255[.]255[.]255[.]255)$` <br /> |
| `gatewayAddress` _string_ | GatewayAddress supplies the existing IPv4 gateway without a prefix length. |  | MaxLength: 45 <br />MinLength: 1 <br /> |


#### IPAddressAllocationMoveSpec



IPAddressAllocationMoveSpec specifies the source and target for transferring an IPAddressAllocation across Namespaces.
Ownership of the allocated IP addresses is transferred without releasing the addresses.
Rollback uses the same operation in a new transition, reversing source and target references.



_Appears in:_
- [NetworkResourceTransitionSpec](#networkresourcetransitionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `source` _[NamespacedName](#namespacedname)_ | Source specifies the existing IPAddressAllocation to transfer from. |  | Required: \{\} <br /> |
| `target` _[NamespacedName](#namespacedname)_ | Target specifies the target IPAddressAllocation in the destination Namespace. |  | Required: \{\} <br /> |


#### IPAddressAllocationMoveStatus



IPAddressAllocationMoveStatus records the observed transition status for an IPAddressAllocation move.



_Appears in:_
- [NetworkResourceTransitionStatus](#networkresourcetransitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `source` _[NamespacedName](#namespacedname)_ | Source mirrors the source IPAddressAllocation reference. |  |  |
| `target` _[NamespacedName](#namespacedname)_ | Target mirrors the target IPAddressAllocation reference. |  |  |
| `phase` _[TransitionPhase](#transitionphase)_ | Phase of this specific move operation. |  | Enum: [InProgress Succeeded Failed] <br /> |
| `message` _string_ | Message describes details or error information. |  |  |


#### IPAddressRange



IPAddressRange describes inclusive lower and upper IP address bounds.



_Appears in:_
- [DHCPNetworkConfiguration](#dhcpnetworkconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `start` _string_ | Start is the first IPv4 address in the existing DHCP range. |  | MaxLength: 45 <br />MinLength: 1 <br /> |
| `end` _string_ | End is the last IPv4 address in the existing DHCP range. |  | MaxLength: 45 <br />MinLength: 1 <br /> |


#### IPAllocationStatus



IPAllocationStatus records an accepted LB SNAT IP allocation checkpoint.
The controller verifies ownership and readiness before persisting it.



_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ipAddress` _string_ | IPAddress is the address accepted for LB SNAT, from a source allocation or user input. |  | MaxLength: 45 <br />MinLength: 1 <br /> |
| `allocationRef` _[NamespacedName](#namespacedname)_ | AllocationRef identifies the source allocation when one was created.<br />It uses the shared NamespacedName type and is omitted for an administrator-supplied<br />backend address without a source allocation. |  |  |


#### LoadBalancerProvider

_Underlying type:_ _string_

LoadBalancerProvider identifies a requested or discovered load balancer implementation.
Field-specific enums restrict the providers supported by the migration release.

_Validation:_
- MaxLength: 32
- MinLength: 1

_Appears in:_
- [ControlPlaneNetworkTransitionSpec](#controlplanenetworktransitionspec)
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)



#### NSXResourceName

_Underlying type:_ _string_

NSXResourceName is used as both NSX resource ID and display name.

_Validation:_
- MaxLength: 255
- MinLength: 1
- Pattern: `^[A-Za-z0-9_.-]+$`

_Appears in:_
- [ControlPlaneResourceNaming](#controlplaneresourcenaming)
- [VDNResourceNaming](#vdnresourcenaming)



#### NSXWorkflowStatus



NSXWorkflowStatus records a resumable NSX conversion workflow for one source VDN.



_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)
- [TransitionStatus](#transitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vdnName` _string_ | VDNName identifies the source VSphereDistributedNetwork for this workflow. |  | MaxLength: 256 <br />MinLength: 1 <br /> |
| `workflowID` _string_ | WorkflowID is the persisted NSX handle used for retries, rollback, and cleanup. |  | MaxLength: 256 <br />MinLength: 1 <br /> |


#### NamespaceNetworkConfiguration



NamespaceNetworkConfiguration supplies addressing discovered during evaluation.



_Appears in:_
- [NamespaceNetworkTransitionSpec](#namespacenetworktransitionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `resourceNaming` _[NamespaceResourceNaming](#namespaceresourcenaming)_ | ResourceNaming supplies optional IDs for NSX resources created for migration.<br />Omitted names use deterministic generated names. Naming inputs never rename<br />or implicitly adopt existing resources and are not requiredInputs. |  |  |
| `namespaceDHCPNetworks` _[DHCPNetworkConfiguration](#dhcpnetworkconfiguration) array_ | NamespaceDHCPNetworks describes existing DHCP-backed networks used by the target Namespaces.<br />Each vdnName identifies a source network discovered from the target Namespaces;<br />Namespaces sharing that network use the same entry. |  | MaxItems: 1000 <br /> |


#### NamespaceNetworkTransition



NamespaceNetworkTransition requests Stage 2 migration of an explicit Namespace batch.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `migration.network.vmware.com/v1alpha1` | | |
| `kind` _string_ | `NamespaceNetworkTransition` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[NamespaceNetworkTransitionSpec](#namespacenetworktransitionspec)_ | Spec describes the requested transition. |  |  |
| `status` _[NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)_ | Status reports observed aggregate and per-Namespace progress. |  |  |


#### NamespaceNetworkTransitionSpec



NamespaceNetworkTransitionSpec requests a batch of Supervisor Namespace transitions.



_Appears in:_
- [NamespaceNetworkTransition](#namespacenetworktransition)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceMode` _string_ | SourceMode is the source network stack. |  | Enum: [VSPHERE_VDS] <br />MinLength: 1 <br /> |
| `targetMode` _string_ | TargetMode is the target network stack. |  | Enum: [NSX_VPC] <br />MinLength: 1 <br /> |
| `action` _[Action](#action)_ | Action selects the operation to reconcile. Operations never auto-advance. |  | Enum: [Evaluate Plan Derive Execute Rollback Commit Finalize] <br /> |
| `namespaces` _string array_ | Namespaces identifies the immutable set of target Namespaces. |  | MaxItems: 1000 <br />MinItems: 1 <br /> |
| `networkConfiguration` _[NamespaceNetworkConfiguration](#namespacenetworkconfiguration)_ | NetworkConfiguration supplies administrator-provided migration parameters.<br />Values may be added, corrected, or removed while the requested action is<br />Evaluate and no planning checkpoint has been recorded, or after a successful<br />Rollback with no irreversible checkpoint. Plan otherwise freezes the configuration.<br />Rollback success must mean restoration is verified and the previous plan is invalidated. |  |  |


#### NamespaceNetworkTransitionStatus



NamespaceNetworkTransitionStatus contains aggregate and per-Namespace progress.



_Appears in:_
- [NamespaceNetworkTransition](#namespacenetworktransition)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#condition-v1-meta) array_ | Conditions report the current operation's readiness and failures. |  | MaxItems: 32 <br /> |
| `observedGeneration` _integer_ | ObservedGeneration is the desired-state generation last inspected, even if its action was blocked.<br />It indicates status freshness and does not mean the action ran or succeeded. |  | Minimum: 1 <br /> |
| `phase` _string_ | Phase describes current progress; Blocked means prerequisites or handlers are unavailable. |  | MaxLength: 64 <br />MinLength: 1 <br /> |
| `lastAction` _[Action](#action)_ | LastAction identifies the most recently checkpointed operation, regardless of outcome. |  | Enum: [Evaluate Plan Derive Execute Rollback Commit Finalize] <br /> |
| `actionState` _[ActionState](#actionstate)_ | ActionState reports whether lastAction is running, succeeded, or failed. |  | Enum: [Running Succeeded Failed] <br /> |
| `actionGeneration` _integer_ | ActionGeneration is the desired-state generation checkpointed for lastAction.<br />It stays unchanged when a newer request is blocked, so previous success cannot satisfy that request. |  | Minimum: 1 <br /> |
| `irreversibleAction` _string_ | IrreversibleAction identifies the action whose point of no return has been persisted.<br />It is set only after prerequisite checks and before irreversible external changes.<br />Once set, only that action may be resumed and this checkpoint cannot be cleared. |  | Enum: [Commit Finalize] <br /> |
| `nsxWorkflows` _[NSXWorkflowStatus](#nsxworkflowstatus) array_ | NSXWorkflows records the exact external workflow handle for each source VDN. |  | MaxItems: 1000 <br /> |
| `requiredInputs` _[RequiredInput](#requiredinput) array_ | RequiredInputs describe administrator inputs required before proceeding. |  | MaxItems: 1000 <br /> |
| `optionalInputs` _[OptionalInput](#optionalinput) array_ | OptionalInputs describe optional overrides the UI may offer during Evaluate.<br />Omitting these inputs uses defaults and does not block evaluation. Handlers<br />publish/clear them with RequiredInputs for ObservedGeneration, remove frozen<br />resource names, and revalidate live eligibility before accepting overrides.<br />This is advisory UI information, not a resource inventory or a review gate. |  | MaxItems: 5000 <br /> |
| `namespaceStatuses` _[NamespaceStatus](#namespacestatus) array_ | NamespaceStatuses records progress without creating separate status resources.<br />Entries must belong to spec.namespaces; missing entries and empty phases are Pending.<br />Clients derive aggregate counts with NamespacePhaseCounts; counters are not persisted. |  | MaxItems: 1000 <br /> |


#### NamespaceResourceNaming



NamespaceResourceNaming supplies optional names for new workload topology.
Each supplied name is also used as the NSX resource ID.
Evaluate discovers the VDNs used by spec.namespaces, resolves their VDS-derived
zone spans, and searches for compatible existing TGW/VPC topology.



_Appears in:_
- [NamespaceNetworkConfiguration](#namespacenetworkconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `networks` _[VDNResourceNaming](#vdnresourcenaming) array_ | Networks supplies optional naming overrides for source VDNs. Existing<br />compatible resources are reused; new resources without overrides use<br />deterministic generated names. Missing names are not requiredInputs. |  | MaxItems: 1000 <br /> |


#### NamespaceStatus



NamespaceStatus reports progress for one target Namespace.



_Appears in:_
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name identifies the target Namespace. |  | MaxLength: 63 <br />MinLength: 1 <br /> |
| `phase` _string_ | Phase describes current progress for this Namespace. |  | MaxLength: 64 <br />MinLength: 1 <br /> |
| `reason` _string_ | Reason identifies the machine-readable cause of the Namespace outcome. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `message` _string_ | Message describes the Namespace outcome or failure requiring attention. |  | MaxLength: 4096 <br />MinLength: 1 <br /> |


#### NamespacedName



NamespacedName identifies a Kubernetes resource by Namespace and name.
This API type preserves lowercase JSON fields and admission validation.



_Appears in:_
- [IPAddressAllocationMoveSpec](#ipaddressallocationmovespec)
- [IPAddressAllocationMoveStatus](#ipaddressallocationmovestatus)
- [IPAllocationStatus](#ipallocationstatus)
- [SubnetPortCutoverSpec](#subnetportcutoverspec)
- [SubnetPortCutoverStatus](#subnetportcutoverstatus)
- [SubnetPortRevertSpec](#subnetportrevertspec)
- [SubnetPortRevertStatus](#subnetportrevertstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `namespace` _string_ | Namespace of the referent. |  | MaxLength: 63 <br />Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` <br />Required: \{\} <br /> |
| `name` _string_ | Name of the referent. |  | MaxLength: 253 <br />Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$` <br />Required: \{\} <br /> |


#### NetworkResourceTransition



NetworkResourceTransition is the Schema for the networkresourcetransitions API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `migration.network.vmware.com/v1alpha1` | | |
| `kind` _string_ | `NetworkResourceTransition` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[NetworkResourceTransitionSpec](#networkresourcetransitionspec)_ |  |  | Required: \{\} <br /> |
| `status` _[NetworkResourceTransitionStatus](#networkresourcetransitionstatus)_ |  |  |  |


#### NetworkResourceTransitionSpec



NetworkResourceTransitionSpec defines the desired transition of network resources across Namespaces.



_Appears in:_
- [NetworkResourceTransition](#networkresourcetransition)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `subnetPortCutovers` _[SubnetPortCutoverSpec](#subnetportcutoverspec) array_ | SubnetPortCutovers specifies workload cutovers from one reservation to one SubnetPort each. |  | MaxItems: 100 <br /> |
| `subnetPortReverts` _[SubnetPortRevertSpec](#subnetportrevertspec) array_ | SubnetPortReverts specifies rollback from each detached SubnetPort to one exact-IP reservation.<br />Use a new transition request; the original forward transition remains immutable. |  | MaxItems: 100 <br /> |
| `ipAddressAllocationMoves` _[IPAddressAllocationMoveSpec](#ipaddressallocationmovespec) array_ | IPAddressAllocationMoves specifies IPAddressAllocation resources to transfer across Namespaces. |  | MaxItems: 100 <br /> |


#### NetworkResourceTransitionStatus



NetworkResourceTransitionStatus defines the observed state of NetworkResourceTransition.



_Appears in:_
- [NetworkResourceTransition](#networkresourcetransition)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration represents the .metadata.generation that the condition/status was set based upon. |  |  |
| `phase` _[TransitionPhase](#transitionphase)_ | Phase defines the overall transition phase. |  | Enum: [InProgress Succeeded Failed] <br /> |
| `totalResources` _integer_ | TotalResources counts operations across cutovers, reverts, and allocation moves.<br />Each cutover or revert transfers one reservation for one SubnetPort. |  |  |
| `succeededResources` _integer_ | SucceededResources is the count of successfully transitioned resources. |  |  |
| `failedResources` _integer_ | FailedResources is the count of failed resources. |  |  |
| `subnetPortCutovers` _[SubnetPortCutoverStatus](#subnetportcutoverstatus) array_ | SubnetPortCutovers mirrors the status for each SubnetPort cutover. |  | MaxItems: 100 <br /> |
| `subnetPortReverts` _[SubnetPortRevertStatus](#subnetportrevertstatus) array_ | SubnetPortReverts mirrors the rollback status for each source SubnetPort. |  | MaxItems: 100 <br /> |
| `ipAddressAllocationMoves` _[IPAddressAllocationMoveStatus](#ipaddressallocationmovestatus) array_ | IPAddressAllocationMoves mirrors the status for each IPAddressAllocation move. |  | MaxItems: 100 <br /> |
| `conditions` _Condition array_ | Conditions track overall transition conditions. |  |  |


#### OptionalInput



OptionalInput describes an optional administrator input; omission uses defaults.
It has the same fieldName/description shape as RequiredInput without blocking progress.



_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)
- [TransitionStatus](#transitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `fieldName` _string_ | FieldName identifies the optional spec field by its full path, including any VDN selector.<br />For example: spec.networkConfiguration.resourceNaming.networks[vdnName=vdn-cp].tgwName. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `description` _string_ | Description identifies the VDN/resource and explains the default on omission. |  | MaxLength: 4096 <br />MinLength: 1 <br /> |


#### RequiredInput



RequiredInput describes administrator input needed to proceed.



_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)
- [TransitionStatus](#transitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `fieldName` _string_ | FieldName identifies the missing spec field by its full path, including any VDN selector.<br />For example: spec.networkConfiguration.controlPlaneAndServiceDHCPNetworks[vdnName=vdn-cp]. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `description` _string_ | Description explains how to supply the input. |  | MaxLength: 4096 <br />MinLength: 1 <br /> |


#### SubnetPortCutoverSpec



SubnetPortCutoverSpec transfers one source SubnetIPReservation to a target SubnetPort.
The reservation holds the workload interface's IPv4 address and, when configured,
its IPv6 address. The target claims these addresses without releasing them, and
the source SubnetIPReservation is deleted after successful cutover.



_Appears in:_
- [NetworkResourceTransitionSpec](#networkresourcetransitionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `subnetIPReservation` _[NamespacedName](#namespacedname)_ | SubnetIPReservation identifies the source reservation containing the interface addresses. |  | Required: \{\} <br /> |
| `subnetPort` _[NamespacedName](#namespacedname)_ | SubnetPort identifies the target SubnetPort to create with the claimed addresses. |  | Required: \{\} <br /> |


#### SubnetPortCutoverStatus



SubnetPortCutoverStatus records the observed transition status for a SubnetPort cutover.



_Appears in:_
- [NetworkResourceTransitionStatus](#networkresourcetransitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `subnetPort` _[NamespacedName](#namespacedname)_ | SubnetPort mirrors the target SubnetPort reference. |  |  |
| `subnetIPReservation` _[NamespacedName](#namespacedname)_ | SubnetIPReservation mirrors the source reservation's Namespace and name. |  |  |
| `phase` _[TransitionPhase](#transitionphase)_ | Phase of this specific cutover operation. |  | Enum: [InProgress Succeeded Failed] <br /> |
| `message` _string_ | Message describes details or error information. |  |  |
| `attachmentID` _string_ | AttachmentID of the realized SubnetPort. |  |  |
| `ipAddresses` _string array_ | IPAddresses realized on the target SubnetPort. |  |  |
| `macAddress` _string_ | MACAddress preserved and programmed on the target SubnetPort. |  |  |


#### SubnetPortRevertSpec



SubnetPortRevertSpec transfers a detached SubnetPort's addresses back to one reservation.
The source SubnetPort must be detached from all network interfaces before transfer.
The destination reservation must restore exactly the source port's current addresses
on the same backing subnet. Transfer must be atomic and must not release addresses.



_Appears in:_
- [NetworkResourceTransitionSpec](#networkresourcetransitionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `subnetPort` _[NamespacedName](#namespacedname)_ | SubnetPort identifies the detached source SubnetPort. |  | Required: \{\} <br /> |
| `subnetIPReservation` _[NamespacedName](#namespacedname)_ | SubnetIPReservation identifies the destination reservation by Namespace and name.<br />Derive the resolved Subnet CR name from the source SubnetPort and resolve the<br />same-named Subnet in this Namespace, requiring matching backing identity. |  | Required: \{\} <br /> |
| `reservedIPs` _string array_ | ReservedIPs contains the exact addresses to restore, with at most one address<br />per family. Ranges, CIDRs, and requests for new addresses are not supported. |  | MaxItems: 2 <br />MinItems: 1 <br /> |


#### SubnetPortRevertStatus



SubnetPortRevertStatus reports allocation-preserving restoration of one source port.



_Appears in:_
- [NetworkResourceTransitionStatus](#networkresourcetransitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `subnetPort` _[NamespacedName](#namespacedname)_ | SubnetPort records the source port, retained after verified consumption. |  |  |
| `subnetIPReservation` _[NamespacedName](#namespacedname)_ | SubnetIPReservation mirrors the requested destination reservation.<br />Success means it is realized and port cleanup cannot release its addresses. |  |  |
| `reservedIPs` _string array_ | ReservedIPs records the exact addresses restored to the destination reservation. |  | MaxItems: 2 <br /> |
| `phase` _[TransitionPhase](#transitionphase)_ | Phase and Message report this operation's outcome, not whole-Namespace rollback. |  | Enum: [InProgress Succeeded Failed] <br /> |
| `message` _string_ |  |  |  |


#### TransitionPhase

_Underlying type:_ _string_

TransitionPhase defines the phase of a network resource transition.

_Validation:_
- Enum: [InProgress Succeeded Failed]

_Appears in:_
- [IPAddressAllocationMoveStatus](#ipaddressallocationmovestatus)
- [NetworkResourceTransitionStatus](#networkresourcetransitionstatus)
- [SubnetPortCutoverStatus](#subnetportcutoverstatus)
- [SubnetPortRevertStatus](#subnetportrevertstatus)

| Field | Description |
| --- | --- |
| `InProgress` |  |
| `Succeeded` |  |
| `Failed` |  |


#### TransitionSpec



TransitionSpec identifies the source, target and requested operation.



_Appears in:_
- [ControlPlaneNetworkTransitionSpec](#controlplanenetworktransitionspec)
- [NamespaceNetworkTransitionSpec](#namespacenetworktransitionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceMode` _string_ | SourceMode is the source network stack. |  | Enum: [VSPHERE_VDS] <br />MinLength: 1 <br /> |
| `targetMode` _string_ | TargetMode is the target network stack. |  | Enum: [NSX_VPC] <br />MinLength: 1 <br /> |
| `action` _[Action](#action)_ | Action selects the operation to reconcile. Operations never auto-advance. |  | Enum: [Evaluate Plan Derive Execute Rollback Commit Finalize] <br /> |


#### TransitionStatus



TransitionStatus persists shared progress and resumable workflow state.



_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#condition-v1-meta) array_ | Conditions report the current operation's readiness and failures. |  | MaxItems: 32 <br /> |
| `observedGeneration` _integer_ | ObservedGeneration is the desired-state generation last inspected, even if its action was blocked.<br />It indicates status freshness and does not mean the action ran or succeeded. |  | Minimum: 1 <br /> |
| `phase` _string_ | Phase describes current progress; Blocked means prerequisites or handlers are unavailable. |  | MaxLength: 64 <br />MinLength: 1 <br /> |
| `lastAction` _[Action](#action)_ | LastAction identifies the most recently checkpointed operation, regardless of outcome. |  | Enum: [Evaluate Plan Derive Execute Rollback Commit Finalize] <br /> |
| `actionState` _[ActionState](#actionstate)_ | ActionState reports whether lastAction is running, succeeded, or failed. |  | Enum: [Running Succeeded Failed] <br /> |
| `actionGeneration` _integer_ | ActionGeneration is the desired-state generation checkpointed for lastAction.<br />It stays unchanged when a newer request is blocked, so previous success cannot satisfy that request. |  | Minimum: 1 <br /> |
| `irreversibleAction` _string_ | IrreversibleAction identifies the action whose point of no return has been persisted.<br />It is set only after prerequisite checks and before irreversible external changes.<br />Once set, only that action may be resumed and this checkpoint cannot be cleared. |  | Enum: [Commit Finalize] <br /> |
| `nsxWorkflows` _[NSXWorkflowStatus](#nsxworkflowstatus) array_ | NSXWorkflows records the exact external workflow handle for each source VDN. |  | MaxItems: 1000 <br /> |
| `requiredInputs` _[RequiredInput](#requiredinput) array_ | RequiredInputs describe administrator inputs required before proceeding. |  | MaxItems: 1000 <br /> |
| `optionalInputs` _[OptionalInput](#optionalinput) array_ | OptionalInputs describe optional overrides the UI may offer during Evaluate.<br />Omitting these inputs uses defaults and does not block evaluation. Handlers<br />publish/clear them with RequiredInputs for ObservedGeneration, remove frozen<br />resource names, and revalidate live eligibility before accepting overrides.<br />This is advisory UI information, not a resource inventory or a review gate. |  | MaxItems: 5000 <br /> |


#### VDNResourceNaming



VDNResourceNaming identifies a source VDN by its Kubernetes name, not an
internal resource key. Shared topology must resolve to consistent names.
Omitted names use deterministic generated names and do not block Evaluate.



_Appears in:_
- [ControlPlaneResourceNaming](#controlplaneresourcenaming)
- [NamespaceResourceNaming](#namespaceresourcenaming)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vdnName` _string_ | VDNName identifies a source VSphereDistributedNetwork used by the batch.<br />Unknown VDNs and conflicting names for shared topology are rejected by Evaluate. |  | MaxLength: 253 <br />MinLength: 1 <br />Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$` <br /> |
| `tgwName` _[NSXResourceName](#nsxresourcename)_ | TGWName overrides the generated name when a new same-span TGW is needed. |  | MaxLength: 255 <br />MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.-]+$` <br /> |
| `vpcName` _[NSXResourceName](#nsxresourcename)_ | VPCName overrides the generated name of a new VPC for this VDN. |  | MaxLength: 255 <br />MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.-]+$` <br /> |
| `dvcName` _[NSXResourceName](#nsxresourcename)_ | DVCName overrides the generated name when a new DVC is needed. |  | MaxLength: 255 <br />MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.-]+$` <br /> |
| `subnetName` _[NSXResourceName](#nsxresourcename)_ | SubnetName overrides the generated name of a new VLAN extension subnet.<br />All backing DVPGs must use the same discovered VLAN and share one subnet<br />under the resolved VPC/span. Evaluate must reject inconsistent VLANs.<br />Existing compatible resources are reused without creation names. |  | MaxLength: 255 <br />MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.-]+$` <br /> |
