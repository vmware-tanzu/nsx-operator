# API Reference

## Packages
- [migration.network.vmware.com/v1alpha1](#migrationnetworkvmwarecomv1alpha1)


## migration.network.vmware.com/v1alpha1

Package v1alpha1 defines Supervisor migration orchestration and network resource transfer APIs.

Package v1alpha1 defines the Supervisor network transition API.

### Resource Types
- [NetworkResourceTransition](#networkresourcetransition)



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


#### NamespacedName



NamespacedName identifies a Kubernetes resource by Namespace and name.
This API type preserves lowercase JSON fields and admission validation.



_Appears in:_
- [IPAddressAllocationMoveSpec](#ipaddressallocationmovespec)
- [IPAddressAllocationMoveStatus](#ipaddressallocationmovestatus)
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
