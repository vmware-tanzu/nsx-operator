# API Reference

## Packages
- [iaas.vmware.com/v1alpha1](#iaasvmwarecomv1alpha1)


## iaas.vmware.com/v1alpha1

Package v1alpha1 defines Supervisor migration orchestration and network resource transfer APIs.

Package v1alpha1 defines the Supervisor network transition API.

### Resource Types
- [ControlPlaneNetworkTransition](#controlplanenetworktransition)
- [NamespaceNetworkTransition](#namespacenetworktransition)



#### Action

_Underlying type:_ _string_

Action selects an explicit migration operation.

_Validation:_
- Enum: [EVALUATE PLAN DERIVE EXECUTE ROLLBACK COMMIT FINALIZE]

_Appears in:_
- [ControlPlaneNetworkTransitionSpec](#controlplanenetworktransitionspec)
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionSpec](#namespacenetworktransitionspec)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)
- [TransitionSpec](#transitionspec)
- [TransitionStatus](#transitionstatus)

| Field | Description |
| --- | --- |
| `EVALUATE` | ActionEvaluate discovers prerequisites and missing inputs.<br /> |
| `PLAN` | ActionPlan generates a blueprint without changing the dataplane.<br /> |
| `DERIVE` | ActionDerive provisions the approved network topology.<br /> |
| `EXECUTE` | ActionExecute cuts workloads over to the target network.<br /> |
| `ROLLBACK` | ActionRollback restores the source network before finalization.<br /> |
| `COMMIT` | ActionCommit commits a verified namespace batch and closes its rollback window.<br /> |
| `FINALIZE` | ActionFinalize commits remaining infrastructure workflows and retires legacy resources.<br /> |


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


#### AviConfiguration



AviConfiguration supplies source and target Avi network addressing.



_Appears in:_
- [ControlPlaneUserInputs](#controlplaneuserinputs)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `legacyVipRanges` _string array_ | legacyVipRanges supplies existing VIP address ranges for an AVI source. |  | MaxItems: 1000 <br /> |
| `newVipRanges` _string array_ | newVipRanges supplies VIP CIDRs or address ranges when the target is AVI. |  | MaxItems: 1000 <br /> |
| `newFrontendDvpgs` _string array_ | newFrontendDvpgs identifies Avi target frontend networks, validated against the legacy frontend DVS. |  | MaxItems: 1000 <br /> |


#### ControlPlaneNetworkTransition



ControlPlaneNetworkTransition requests Stage 1 infrastructure transition and Stage 3 finalization.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `iaas.vmware.com/v1alpha1` | | |
| `kind` _string_ | `ControlPlaneNetworkTransition` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ControlPlaneNetworkTransitionSpec](#controlplanenetworktransitionspec)_ | spec describes the requested transition. |  |  |
| `status` _[ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)_ | status reports observed progress. |  |  |


#### ControlPlaneNetworkTransitionSpec



ControlPlaneNetworkTransitionSpec requests infrastructure network migration.



_Appears in:_
- [ControlPlaneNetworkTransition](#controlplanenetworktransition)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceMode` _string_ | sourceMode is the source network stack. |  | Enum: [VSPHERE_VDS] <br />MinLength: 1 <br /> |
| `targetMode` _string_ | targetMode is the target network stack. |  | Enum: [NSX_VPC] <br />MinLength: 1 <br /> |
| `action` _[Action](#action)_ | action selects the operation to reconcile. Operations never auto-advance. |  | Enum: [EVALUATE PLAN DERIVE EXECUTE ROLLBACK COMMIT FINALIZE] <br /> |
| `targetLbProvider` _[LoadBalancerProvider](#loadbalancerprovider)_ | targetLbProvider selects AVI or NSX_LB as the desired target load balancer provider.<br />If omitted, evaluation resolves an unambiguous supported target or requests input.<br />The resolved selection is bound to the reviewed plan and cannot change after planning starts. |  | Enum: [AVI NSX_LB] <br />MaxLength: 32 <br />MinLength: 1 <br /> |
| `nsxProjectId` _string_ | nsxProjectId selects a pre-existing NSX project; omission selects the default project.<br />Once supplied, the ID cannot be changed or removed. Omission is fixed when planning starts. |  | MaxLength: 256 <br />MinLength: 1 <br /> |
| `userInputs` _[ControlPlaneUserInputs](#controlplaneuserinputs)_ | userInputs supplies administrator-provided migration parameters.<br />Supplied values cannot be changed or removed; missing values may be added before planning starts. |  |  |


#### ControlPlaneNetworkTransitionStatus



ControlPlaneNetworkTransitionStatus records infrastructure discovery and realized topology.



_Appears in:_
- [ControlPlaneNetworkTransition](#controlplanenetworktransition)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#condition-v1-meta) array_ | conditions report the current operation's readiness and failures. |  | MaxItems: 32 <br /> |
| `observedGeneration` _integer_ | observedGeneration is the desired-state generation last inspected, even if its action was blocked.<br />It indicates status freshness and does not mean the action ran or succeeded. |  | Minimum: 1 <br /> |
| `phase` _string_ | phase describes current progress; Blocked means prerequisites or handlers are unavailable. |  | MaxLength: 64 <br />MinLength: 1 <br /> |
| `lastAction` _[Action](#action)_ | lastAction identifies the most recently checkpointed operation, regardless of outcome. |  | Enum: [EVALUATE PLAN DERIVE EXECUTE ROLLBACK COMMIT FINALIZE] <br /> |
| `actionState` _[ActionState](#actionstate)_ | actionState reports whether lastAction is running, succeeded, or failed. |  | Enum: [Running Succeeded Failed] <br /> |
| `actionGeneration` _integer_ | actionGeneration is the desired-state generation checkpointed for lastAction.<br />It stays unchanged when a newer request is blocked, so previous success cannot satisfy that request. |  | Minimum: 1 <br /> |
| `irreversibleAction` _string_ | irreversibleAction identifies the action whose point of no return has been persisted.<br />It is set only after prerequisite checks and before irreversible external changes.<br />Once set, only that action may be resumed and this checkpoint cannot be cleared. |  | Enum: [COMMIT FINALIZE] <br /> |
| `nsxWorkflows` _[NSXWorkflowStatus](#nsxworkflowstatus) array_ | nsxWorkflows records the exact external workflow handle for each source VDN. |  | MaxItems: 1000 <br /> |
| `requiredInputs` _[RequiredInput](#requiredinput) array_ | requiredInputs describe administrator inputs required before proceeding. |  | MaxItems: 1000 <br /> |
| `detectedSourceLbProvider` _[LoadBalancerProvider](#loadbalancerprovider)_ | detectedSourceLbProvider identifies the discovered source load balancer provider. |  | Enum: [AVI HAPROXY VSPHERE_FOUNDATION] <br />MaxLength: 32 <br />MinLength: 1 <br /> |
| `targetLbProvider` _[LoadBalancerProvider](#loadbalancerprovider)_ | targetLbProvider identifies the resolved and validated AVI or NSX_LB target provider. |  | Enum: [AVI NSX_LB] <br />MaxLength: 32 <br />MinLength: 1 <br /> |
| `targetNsxProject` _string_ | targetNsxProject is the full path of the resolved pre-existing NSX project. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `primaryVpcPath` _string_ | primaryVpcPath is the full path of the infrastructure VPC. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `loadBalancerVpcPath` _string_ | loadBalancerVpcPath is the full path of the load balancer VPC. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |


#### ControlPlaneUserInputs



ControlPlaneUserInputs supplies network and load balancer settings discovered during evaluation.



_Appears in:_
- [ControlPlaneNetworkTransitionSpec](#controlplanenetworktransitionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `cpVmServiceSubnets` _[SubnetInput](#subnetinput) array_ | cpVmServiceSubnets supplies addressing for DHCP-backed control plane and service networks. |  | MaxItems: 1000 <br /> |
| `legacyLbFrontendDvpgs` _string array_ | legacyLbFrontendDvpgs identifies frontend port groups for AVI or HAProxy sources; FLB discovers its backing. |  | MaxItems: 1000 <br /> |
| `aviConfig` _[AviConfiguration](#aviconfiguration)_ | aviConfig supplies Avi configuration when either the source or target provider is AVI. |  |  |


#### IPAddressRange



IPAddressRange describes inclusive lower and upper IP address bounds.



_Appears in:_
- [SubnetInput](#subnetinput)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `start` _string_ | start is the first IP address in the range. |  | MaxLength: 45 <br />MinLength: 1 <br /> |
| `end` _string_ | end is the last IP address in the range. |  | MaxLength: 45 <br />MinLength: 1 <br /> |


#### LoadBalancerProvider

_Underlying type:_ _string_

LoadBalancerProvider identifies a supported load balancer implementation.

_Validation:_
- MaxLength: 32
- MinLength: 1

_Appears in:_
- [ControlPlaneNetworkTransitionSpec](#controlplanenetworktransitionspec)
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)



#### NSXWorkflowStatus



NSXWorkflowStatus records a resumable NSX conversion workflow for one source VDN.



_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)
- [TransitionStatus](#transitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vdn` _string_ | vdn identifies the source virtual distributed network for this workflow. |  | MaxLength: 256 <br />MinLength: 1 <br /> |
| `workflowId` _string_ | workflowId is the persisted NSX handle used for retries, rollback, and cleanup. |  | MaxLength: 256 <br />MinLength: 1 <br /> |


#### NamespaceNetworkTransition



NamespaceNetworkTransition requests Stage 2 migration of an explicit namespace batch.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `iaas.vmware.com/v1alpha1` | | |
| `kind` _string_ | `NamespaceNetworkTransition` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[NamespaceNetworkTransitionSpec](#namespacenetworktransitionspec)_ | spec describes the requested transition. |  |  |
| `status` _[NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)_ | status reports observed aggregate and per-namespace progress. |  |  |


#### NamespaceNetworkTransitionSpec



NamespaceNetworkTransitionSpec requests a batch of tenant namespace transitions.



_Appears in:_
- [NamespaceNetworkTransition](#namespacenetworktransition)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceMode` _string_ | sourceMode is the source network stack. |  | Enum: [VSPHERE_VDS] <br />MinLength: 1 <br /> |
| `targetMode` _string_ | targetMode is the target network stack. |  | Enum: [NSX_VPC] <br />MinLength: 1 <br /> |
| `action` _[Action](#action)_ | action selects the operation to reconcile. Operations never auto-advance. |  | Enum: [EVALUATE PLAN DERIVE EXECUTE ROLLBACK COMMIT FINALIZE] <br /> |
| `namespaces` _string array_ | namespaces identifies the immutable set of target namespaces. |  | MaxItems: 1000 <br />MinItems: 1 <br /> |
| `userInputs` _[NamespaceUserInputs](#namespaceuserinputs)_ | userInputs supplies administrator-provided migration parameters.<br />Supplied values cannot be changed or removed; missing values may be added before planning starts. |  |  |


#### NamespaceNetworkTransitionStatus



NamespaceNetworkTransitionStatus contains aggregate and per-namespace progress.



_Appears in:_
- [NamespaceNetworkTransition](#namespacenetworktransition)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#condition-v1-meta) array_ | conditions report the current operation's readiness and failures. |  | MaxItems: 32 <br /> |
| `observedGeneration` _integer_ | observedGeneration is the desired-state generation last inspected, even if its action was blocked.<br />It indicates status freshness and does not mean the action ran or succeeded. |  | Minimum: 1 <br /> |
| `phase` _string_ | phase describes current progress; Blocked means prerequisites or handlers are unavailable. |  | MaxLength: 64 <br />MinLength: 1 <br /> |
| `lastAction` _[Action](#action)_ | lastAction identifies the most recently checkpointed operation, regardless of outcome. |  | Enum: [EVALUATE PLAN DERIVE EXECUTE ROLLBACK COMMIT FINALIZE] <br /> |
| `actionState` _[ActionState](#actionstate)_ | actionState reports whether lastAction is running, succeeded, or failed. |  | Enum: [Running Succeeded Failed] <br /> |
| `actionGeneration` _integer_ | actionGeneration is the desired-state generation checkpointed for lastAction.<br />It stays unchanged when a newer request is blocked, so previous success cannot satisfy that request. |  | Minimum: 1 <br /> |
| `irreversibleAction` _string_ | irreversibleAction identifies the action whose point of no return has been persisted.<br />It is set only after prerequisite checks and before irreversible external changes.<br />Once set, only that action may be resumed and this checkpoint cannot be cleared. |  | Enum: [COMMIT FINALIZE] <br /> |
| `nsxWorkflows` _[NSXWorkflowStatus](#nsxworkflowstatus) array_ | nsxWorkflows records the exact external workflow handle for each source VDN. |  | MaxItems: 1000 <br /> |
| `requiredInputs` _[RequiredInput](#requiredinput) array_ | requiredInputs describe administrator inputs required before proceeding. |  | MaxItems: 1000 <br /> |
| `namespaceStatuses` _[NamespaceStatus](#namespacestatus) array_ | namespaceStatuses records progress without creating separate status resources.<br />Batch membership comes from spec.namespaces; missing status entries are pending. |  | MaxItems: 1000 <br /> |


#### NamespaceStatus



NamespaceStatus reports progress for one target namespace.



_Appears in:_
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | name identifies the target namespace. |  | MaxLength: 63 <br />MinLength: 1 <br /> |
| `phase` _string_ | phase describes current progress for this namespace. |  | MaxLength: 64 <br />MinLength: 1 <br /> |
| `reason` _string_ | reason identifies the machine-readable cause of the namespace outcome. |  | MaxLength: 1024 <br />MinLength: 1 <br /> |
| `message` _string_ | message describes the namespace outcome or failure requiring attention. |  | MaxLength: 4096 <br />MinLength: 1 <br /> |


#### NamespaceUserInputs



NamespaceUserInputs supplies addressing discovered during evaluation.



_Appears in:_
- [NamespaceNetworkTransitionSpec](#namespacenetworktransitionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `tenantSubnets` _[SubnetInput](#subnetinput) array_ | tenantSubnets supplies addressing for DHCP-backed tenant networks.<br />Each vdn identifies a source network discovered from the target namespaces;<br />namespaces sharing that network use the same entry. |  | MaxItems: 1000 <br /> |


#### RequiredInput



RequiredInput describes administrator input needed to proceed.



_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)
- [TransitionStatus](#transitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `key` _string_ | key identifies the missing input. |  | MaxLength: 256 <br />MinLength: 1 <br /> |
| `description` _string_ | description explains how to supply the input. |  | MaxLength: 4096 <br />MinLength: 1 <br /> |


#### SubnetInput



SubnetInput supplies existing addressing for a DHCP-backed source VDN.



_Appears in:_
- [ControlPlaneUserInputs](#controlplaneuserinputs)
- [NamespaceUserInputs](#namespaceuserinputs)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vdn` _string_ | vdn identifies a discovered source virtual distributed network within the Supervisor. |  | MaxLength: 256 <br />MinLength: 1 <br /> |
| `dhcpRange` _[IPAddressRange](#ipaddressrange)_ | dhcpRange describes the existing inclusive DHCP pool, not free-IP inventory. |  |  |
| `subnetMask` _string_ | subnetMask is a contiguous dotted-decimal IPv4 mask used to derive the subnet CIDR. |  | MaxLength: 15 <br />MinLength: 1 <br />Pattern: `^((255[.])*(254\|252\|248\|240\|224\|192\|128\|0)([.]0)*\|255[.]255[.]255[.]255)$` <br /> |
| `gatewayAddress` _string_ | gatewayAddress supplies a plain gateway IP address without a prefix length. |  | MaxLength: 45 <br />MinLength: 1 <br /> |
| `subnetCidr` _string_ | subnetCidr optionally asserts the CIDR derived from the DHCP range and mask. |  | MaxLength: 49 <br />MinLength: 1 <br /> |


#### TransitionSpec



TransitionSpec identifies the source, target and requested operation.



_Appears in:_
- [ControlPlaneNetworkTransitionSpec](#controlplanenetworktransitionspec)
- [NamespaceNetworkTransitionSpec](#namespacenetworktransitionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceMode` _string_ | sourceMode is the source network stack. |  | Enum: [VSPHERE_VDS] <br />MinLength: 1 <br /> |
| `targetMode` _string_ | targetMode is the target network stack. |  | Enum: [NSX_VPC] <br />MinLength: 1 <br /> |
| `action` _[Action](#action)_ | action selects the operation to reconcile. Operations never auto-advance. |  | Enum: [EVALUATE PLAN DERIVE EXECUTE ROLLBACK COMMIT FINALIZE] <br /> |


#### TransitionStatus



TransitionStatus persists shared progress and resumable workflow state.



_Appears in:_
- [ControlPlaneNetworkTransitionStatus](#controlplanenetworktransitionstatus)
- [NamespaceNetworkTransitionStatus](#namespacenetworktransitionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#condition-v1-meta) array_ | conditions report the current operation's readiness and failures. |  | MaxItems: 32 <br /> |
| `observedGeneration` _integer_ | observedGeneration is the desired-state generation last inspected, even if its action was blocked.<br />It indicates status freshness and does not mean the action ran or succeeded. |  | Minimum: 1 <br /> |
| `phase` _string_ | phase describes current progress; Blocked means prerequisites or handlers are unavailable. |  | MaxLength: 64 <br />MinLength: 1 <br /> |
| `lastAction` _[Action](#action)_ | lastAction identifies the most recently checkpointed operation, regardless of outcome. |  | Enum: [EVALUATE PLAN DERIVE EXECUTE ROLLBACK COMMIT FINALIZE] <br /> |
| `actionState` _[ActionState](#actionstate)_ | actionState reports whether lastAction is running, succeeded, or failed. |  | Enum: [Running Succeeded Failed] <br /> |
| `actionGeneration` _integer_ | actionGeneration is the desired-state generation checkpointed for lastAction.<br />It stays unchanged when a newer request is blocked, so previous success cannot satisfy that request. |  | Minimum: 1 <br /> |
| `irreversibleAction` _string_ | irreversibleAction identifies the action whose point of no return has been persisted.<br />It is set only after prerequisite checks and before irreversible external changes.<br />Once set, only that action may be resumed and this checkpoint cannot be cleared. |  | Enum: [COMMIT FINALIZE] <br /> |
| `nsxWorkflows` _[NSXWorkflowStatus](#nsxworkflowstatus) array_ | nsxWorkflows records the exact external workflow handle for each source VDN. |  | MaxItems: 1000 <br /> |
| `requiredInputs` _[RequiredInput](#requiredinput) array_ | requiredInputs describe administrator inputs required before proceeding. |  | MaxItems: 1000 <br /> |
