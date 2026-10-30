# API Reference

## Packages
- [migration.network.vmware.com/v1alpha1](#migrationnetworkvmwarecomv1alpha1)


## migration.network.vmware.com/v1alpha1

Package v1alpha1 defines Supervisor migration orchestration and network resource transfer APIs.

Package v1alpha1 defines the Supervisor network transition API.

### Resource Types
- [ControlPlaneNetworkTransition](#controlplanenetworktransition)
- [NamespaceNetworkTransition](#namespacenetworktransition)



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
| `Execute` | ActionExecute cuts workloads over to the target network.<br /> |
| `Rollback` | ActionRollback restores an eligible namespace batch before its Commit checkpoint.<br />Infrastructure transitions do not support Rollback.<br /> |
| `Commit` | ActionCommit commits a verified namespace batch and closes its rollback window.<br /> |
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


#### AllocationReference



AllocationReference identifies the exact source IPAddressAllocation reserved for SNAT.



_Appears in:_
- [LBSNATReservationStatus](#lbsnatreservationstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `namespace` _string_ | Namespace identifies the source allocation's Kubernetes namespace. |  | MaxLength: 63 <br />MinLength: 1 <br /> |
| `name` _string_ | Name identifies the source IPAddressAllocation for Kubernetes API lookups. |  | MaxLength: 253 <br />MinLength: 1 <br /> |


#### ControlPlaneNetworkConfiguration



ControlPlaneNetworkConfiguration supplies network and load balancer settings discovered during evaluation.



_Appears in:_
- [ControlPlaneNetworkTransitionSpec](#controlplanenetworktransitionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `resourceNaming` _[ControlPlaneResourceNaming](#controlplaneresourcenaming)_ | ResourceNaming supplies optional IDs for NSX resources created for migration.<br />Omitted names use deterministic generated names. Naming inputs never rename<br />or implicitly adopt existing resources and are not requiredInputs. |  |  |
| `lbSnatIPv4` _string_ | LBSNATIPv4 supplies an available backend IPv4 address for the shared NSX LB service<br />when no IPv4 address can be reserved from the VIP pools during evaluation.<br />The migration service must validate availability and reserve the address before use.<br />May be corrected before reservation acceptance; acceptance or Plan freezes the input. |  | MaxLength: 15 <br />MinLength: 1 <br /> |
| `lbSnatIPv6` _string_ | LBSNATIPv6 supplies an available backend IPv6 address for the shared NSX LB service<br />when no IPv6 address can be reserved from the VIP pools during evaluation.<br />The migration service must validate availability and reserve the address before use.<br />May be corrected before reservation acceptance; acceptance or Plan freezes the input. |  | MaxLength: 45 <br />MinLength: 1 <br /> |
| `controlPlaneAndServiceDHCPNetworks` _[DHCPNetworkConfiguration](#dhcpnetworkconfiguration) array_ | ControlPlaneAndServiceDHCPNetworks describes existing DHCP-backed networks used by<br />control-plane VMs and Supervisor Services. |  | MaxItems: 1000 <br /> |
| `legacyLbFrontendDvpgs` _string array_ | LegacyLBFrontendDVPGs identifies frontend port groups required for HAProxy sources.<br />FLB discovers its backing automatically. |  | MaxItems: 1000 <br /> |


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
| `targetLbProvider` _[LoadBalancerProvider](#loadbalancerprovider)_ | TargetLBProvider selects the desired target load balancer provider.<br />In 9.2, only NSX_LB is supported for HAProxy/FLB sources.<br />If omitted, evaluation resolves an unambiguous supported/configured target.<br />The resolved selection is bound to the reviewed plan and cannot change after planning starts. |  | Enum: [NSX_LB] <br />MaxLength: 32 <br />MinLength: 1 <br /> |
| `nsxProjectId` _string_ | NSXProjectID selects a pre-existing NSX project; omission selects the default project.<br />Once supplied, the ID cannot be changed or removed. Omission is fixed when planning starts. |  | MaxLength: 256 <br />MinLength: 1 <br /> |
| `networkConfiguration` _[ControlPlaneNetworkConfiguration](#controlplanenetworkconfiguration)_ | NetworkConfiguration supplies administrator-provided migration parameters.<br />Values may be added, corrected, or removed during Evaluate before planning.<br />Accepted SNAT reservations freeze the corresponding family's fallback address.<br />Changes require requested action Evaluate; requesting Plan freezes inputs,<br />and a recorded planning checkpoint prevents reopening them. |  |  |


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
| `lbSnatReservations` _[LBSNATReservationStatus](#lbsnatreservationstatus) array_ | LBSNATReservations records accepted reservations and freezes the corresponding SNAT inputs.<br />Retain checkpoints for recovery; they do not themselves reserve or release any address. |  | MaxItems: 2 <br />MinItems: 1 <br /> |
| `detectedSourceLbProvider` _[LoadBalancerProvider](#loadbalancerprovider)_ | DetectedSourceLBProvider identifies the discovered source load balancer provider.<br />In 9.2, HAPROXY and VSPHERE_FOUNDATION (FLB) are supported.<br />Unsupported discovered providers are reported through failure conditions. |  | Enum: [HAPROXY VSPHERE_FOUNDATION] <br />MaxLength: 32 <br />MinLength: 1 <br /> |
| `targetLbProvider` _[LoadBalancerProvider](#loadbalancerprovider)_ | TargetLBProvider identifies the resolved target provider; only NSX_LB is supported in 9.2. |  | Enum: [NSX_LB] <br />MaxLength: 32 <br />MinLength: 1 <br /> |
| `targetNsxProject` _string_ | TargetNSXProject is the full path of the resolved pre-existing NSX project. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `primaryVpcPath` _string_ | PrimaryVPCPath is the full path of the infrastructure VPC. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `loadBalancerVpcPath` _string_ | LoadBalancerVPCPath is the full path of the load balancer VPC. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |


#### ControlPlaneResourceNaming



ControlPlaneResourceNaming supplies optional IDs for infrastructure resources
created for migration. Evaluate discovers all CPVM and Supervisor Service VDNs;
resources without overrides use deterministic generated names.



_Appears in:_
- [ControlPlaneNetworkConfiguration](#controlplanenetworkconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `networks` _[VDNResourceNaming](#vdnresourcenaming) array_ | Networks supplies optional naming overrides per infrastructure VDN.<br />It includes static and DHCP-backed CPVM and Supervisor Service networks. |  | MaxItems: 1000 <br /> |
| `loadBalancerVpcName` _[NSXResourceName](#nsxresourcename)_ | LoadBalancerVPCName overrides the generated name of the separate LB VPC. |  | MaxLength: 255 <br />MinLength: 1 <br />Pattern: `^[A-Za-z0-9_.-]+$` <br /> |


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
| `gatewayAddress` _string_ | GatewayAddress supplies a plain gateway IP address without a prefix length. |  | MaxLength: 45 <br />MinLength: 1 <br /> |


#### IPAddressRange



IPAddressRange describes inclusive lower and upper IP address bounds.



_Appears in:_
- [DHCPNetworkConfiguration](#dhcpnetworkconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `start` _string_ | Start is the first IP address in the range. |  | MaxLength: 45 <br />MinLength: 1 <br /> |
| `end` _string_ | End is the last IP address in the range. |  | MaxLength: 45 <br />MinLength: 1 <br /> |


#### LBSNATReservationStatus



LBSNATReservationStatus records an accepted source reservation, not an allocation request.
The controller verifies ownership and readiness before persisting this checkpoint.



_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ipFamily` _string_ | IPFamily identifies the reserved address family. |  | Enum: [IPv4 IPv6] <br /> |
| `ipAddress` _string_ | IPAddress is the address realized by the source allocation. |  | MaxLength: 45 <br />MinLength: 1 <br /> |
| `allocationRef` _[AllocationReference](#allocationreference)_ | AllocationRef binds the reservation to the source allocation's namespace and name. |  |  |


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
| `workflowId` _string_ | WorkflowID is the persisted NSX handle used for retries, rollback, and cleanup. |  | MaxLength: 256 <br />MinLength: 1 <br /> |


#### NamespaceNetworkConfiguration



NamespaceNetworkConfiguration supplies addressing discovered during evaluation.



_Appears in:_
- [NamespaceNetworkTransitionSpec](#namespacenetworktransitionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `resourceNaming` _[NamespaceResourceNaming](#namespaceresourcenaming)_ | ResourceNaming supplies optional IDs for NSX resources created for migration.<br />Omitted names use deterministic generated names. Naming inputs never rename<br />or implicitly adopt existing resources and are not requiredInputs. |  |  |
| `namespaceDHCPNetworks` _[DHCPNetworkConfiguration](#dhcpnetworkconfiguration) array_ | NamespaceDHCPNetworks describes existing DHCP-backed networks used by the target namespaces.<br />Each vdnName identifies a source network discovered from the target namespaces;<br />namespaces sharing that network use the same entry. |  | MaxItems: 1000 <br /> |


#### NamespaceNetworkTransition



NamespaceNetworkTransition requests Stage 2 migration of an explicit namespace batch.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `migration.network.vmware.com/v1alpha1` | | |
| `kind` _string_ | `NamespaceNetworkTransition` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[NamespaceNetworkTransitionSpec](#namespacenetworktransitionspec)_ | Spec describes the requested transition. |  |  |
| `status` _[NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)_ | Status reports observed aggregate and per-namespace progress. |  |  |


#### NamespaceNetworkTransitionSpec



NamespaceNetworkTransitionSpec requests a batch of Supervisor namespace transitions.



_Appears in:_
- [NamespaceNetworkTransition](#namespacenetworktransition)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceMode` _string_ | SourceMode is the source network stack. |  | Enum: [VSPHERE_VDS] <br />MinLength: 1 <br /> |
| `targetMode` _string_ | TargetMode is the target network stack. |  | Enum: [NSX_VPC] <br />MinLength: 1 <br /> |
| `action` _[Action](#action)_ | Action selects the operation to reconcile. Operations never auto-advance. |  | Enum: [Evaluate Plan Derive Execute Rollback Commit Finalize] <br /> |
| `namespaces` _string array_ | Namespaces identifies the immutable set of target namespaces. |  | MaxItems: 1000 <br />MinItems: 1 <br /> |
| `networkConfiguration` _[NamespaceNetworkConfiguration](#namespacenetworkconfiguration)_ | NetworkConfiguration supplies administrator-provided migration parameters.<br />Values may be added, corrected, or removed while the requested action is<br />Evaluate and no planning checkpoint has been recorded, or after a successful<br />Rollback with no irreversible checkpoint. Plan otherwise freezes the configuration.<br />Rollback success must mean restoration is verified and the previous plan is invalidated. |  |  |


#### NamespaceNetworkTransitionStatus



NamespaceNetworkTransitionStatus contains aggregate and per-namespace progress.



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
| `namespaceStatuses` _[NamespaceStatus](#namespacestatus) array_ | NamespaceStatuses records progress without creating separate status resources.<br />Batch membership comes from spec.namespaces; missing status entries are pending. |  | MaxItems: 1000 <br /> |


#### NamespaceResourceNaming



NamespaceResourceNaming supplies optional IDs for new workload topology.
Evaluate discovers the VDNs used by spec.namespaces, resolves their VDS-derived
zone spans, and searches for compatible existing TGW/VPC topology.



_Appears in:_
- [NamespaceNetworkConfiguration](#namespacenetworkconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `networks` _[VDNResourceNaming](#vdnresourcenaming) array_ | Networks supplies optional naming overrides for source VDNs. Existing<br />compatible resources are reused; new resources without overrides use<br />deterministic generated names. Missing names are not requiredInputs. |  | MaxItems: 1000 <br /> |


#### NamespaceStatus



NamespaceStatus reports progress for one target namespace.



_Appears in:_
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name identifies the target namespace. |  | MaxLength: 63 <br />MinLength: 1 <br /> |
| `phase` _string_ | Phase describes current progress for this namespace. |  | MaxLength: 64 <br />MinLength: 1 <br /> |
| `reason` _string_ | Reason identifies the machine-readable cause of the namespace outcome. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `message` _string_ | Message describes the namespace outcome or failure requiring attention. |  | MaxLength: 4096 <br />MinLength: 1 <br /> |


#### OptionalInput



OptionalInput describes an optional administrator input; omission uses defaults.
It has the same key/description shape as RequiredInput without blocking progress.



_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)
- [TransitionStatus](#transitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `key` _string_ | Key identifies the spec field the UI can offer, including a VDN selector. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `description` _string_ | Description identifies the VDN/resource and explains the default on omission. |  | MaxLength: 4096 <br />MinLength: 1 <br /> |


#### RequiredInput



RequiredInput describes administrator input needed to proceed.



_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)
- [TransitionStatus](#transitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `key` _string_ | Key identifies the missing input. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `description` _string_ | Description explains how to supply the input. |  | MaxLength: 4096 <br />MinLength: 1 <br /> |


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
