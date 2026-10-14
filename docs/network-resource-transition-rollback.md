# NetworkResourceTransition rollback API

`subnetPortReverts` extends NetworkResourceTransition with the inverse of `subnetPortCutovers`: one detached SubnetPort transfers all of its exact IP addresses to one restored SubnetIPReservation containing the interface addresses. Submit a **new immutable transition request** for rollback; do not edit or delete the forward CR to undo its effects. The registered API is `migration.network.vmware.com/v1alpha1`. The Go types, generated CRD, and API reference define the schema; the reverse controller, coordinator integration, and NSX inverse transaction remain implementation work.

| Operation | Source → destination | Rollback use |
|---|---|---|
| `subnetPortCutovers` | One reservation → SubnetPort | Existing forward operation. |
| `subnetPortReverts` | Detached SubnetPort → one exact-IP reservation | New workload IPAM rollback operation. |
| `ipAddressAllocationMoves` | IPAddressAllocation → IPAddressAllocation | Existing direction-independent operation; reverse source/target references in a new request. No separate VIP rollback field is needed. |

Each revert has a `subnetPort` source and one `subnetIPReservation` destination specification. The destination directly contains `namespace` and `name`; sibling `reservedIPs` contains one IPv4 address, one IPv6 address, or one of each. A dual-stack interface uses one reservation containing both addresses. The destination is a creation/adoption template, not a request for fresh addresses. Never use numberOfIPs, ranges, or CIDRs for restoration. The controller must resolve the destination Subnet to the same backing subnet as the source port and verify that its canonicalized addresses exactly match the port's current addresses and the recorded forward allocation. A matching name alone does not authorize adopting an unrelated object.

The destination Subnet name is derived from the source SubnetPort, not supplied by the caller. Migration guarantees that the resolved Subnet CR has the same name in the source and destination Namespaces. Resolve that name in `SubnetIPReservation.Namespace` and verify that both objects map to the same backing subnet before transferring addresses. If the port uses a SubnetSet or default SubnetSet, resolve its actual selected Subnet rather than copying an empty `spec.subnet`. Persist the resolved Subnet name/backing identity in the controller's recovery checkpoint before consuming the source port. Missing or ambiguous mappings, missing destination Subnets, and backing-subnet mismatches block transfer; never allocate on another Subnet as a fallback.

Admission bounds a request to 100 reverts or 100 forward cutovers, with one reservation per operation. Each restore accepts at most two addresses, with at most one address per family. It rejects empty operation sets, duplicate source ports or reservations, duplicate destination reservations, and a request containing both nonempty forward and reverse SubnetPort lists. Nested reservation lists and their aggregate 512-reservation limit are no longer part of the API. `ipAddressAllocationMoves` can accompany either direction, with up to 100 moves. Runtime identity, canonical IP-set equality, Subnet mapping, detachment, and consumer checks remain mandatory before effects.

The public API uses the validated `NamespacedName` type with lowercase `namespace` and `name` JSON fields. Detachment is the API prerequisite; restoring and verifying VDS connectivity remains a migration-coordinator responsibility.

**Checkpointed execution and recovery:**

1. The migration operator validates the parent transition's PONR/dependency gates, retains admission protection, and classifies each forward effect. Restore and verify the selected workload's recorded VDS interface desired/actual backing through its owning controller. Keep the SubnetPort's IP ownership while restoring connectivity, and verify that no vNIC remains attached to the source port. CPVM rollback uses the separate rolling/quorum contract. An unverified or ambiguous detach blocks the inverse operation.
2. Net Operator submits the new reverse request using parent-owned address/backing checkpoints and validates the live source UID. The reverse controller registers transition ownership/intent before creating or adopting destination CRs. Normal reservation reconciliation must defer allocation under that intent, and forward SubnetPort reconciliation must not recreate the consumed port. Controller identity or a caller-supplied flag alone does not prove detachment or permission to release an address.
3. NSX Operator performs a supported atomic inverse transfer: consume the source VpcSubnetPort's allocation and restore exact-IP DynamicIpAddressReservations on the same NSX subnet, preserving every IPv4/IPv6 address. Verify this capability with NSX integration tests; if unavailable, fail with an actionable unsupported-inverse-transfer condition. **Delete-then-allocate is forbidden.** Do not create competing reservations through normal allocation while the source still owns the IPs.
4. Verify all restored reservations and their Kubernetes bindings, persist their new UIDs/external identities in the durable recovery checkpoint, then reap the source SubnetPort CR without releasing transferred addresses. Newly recreated reservations receive new UIDs; old source reservation UIDs identify historical objects only. Use the UID-binding and staging-finalizer contracts in Sections 3.6.8 and 4.3.1.1. On retry after API success/status-write failure, inspect recorded intent and actual NSX state; resume incomplete bookkeeping rather than allocating again or requiring a consumed source port to reappear.
5. `status.subnetPortReverts[]` mirrors each request with its port reference, exact reservation/IP mapping, phase, and message. Use InProgress/Succeeded/Failed, with counts across all operation lists; each revert counts as one operation restoring one reservation. Succeeded requires all destination reservations realized and source cleanup confirmed. It does not mean that the entire namespace batch has rolled back. Failure retains protection/checkpoints and allocated ownership for retry or inspection.

For **VIP rollback**, submit `ipAddressAllocationMoves` with the currently active tenant allocation as source and the staging allocation as target. The target can be recreated with a new UID; validate the same NSX allocation/VPC/IP, establish only valid same-namespace ownership, and remove the consumed CR without releasing the VIP. Reuse the move schema; verify reverse-direction controller behavior in integration tests. Source/target namespace roles must not be hard-coded to the forward path. Provider-specific Virtual Server/advertisement handoff is still coordinated separately; allocation tag transfer alone does not restore LB traffic.

After all parent-batch workload/VIP/provider/connectivity restoration succeeds, perform safe topology cleanup and release protection. Retain restored reservations while VDS consumers still require their NSX IPAM protection. A completed child CR can be pruned only after the parent has durably copied source/target identities and recovery outcomes; its deletion must not garbage-collect active restored reservations or allocations. A failed/partial child remains available for recovery. This contract does not introduce automatic rollback, bypass shared-resource checks, or alter PONR decisions.

## Example request

```yaml
apiVersion: migration.network.vmware.com/v1alpha1
kind: NetworkResourceTransition
metadata:
  name: rollback-ns-production
spec:
  subnetPortReverts:
  - subnetPort:
      namespace: ns-production
      name: vm-webapp-eth0
    subnetIPReservation:
      namespace: destination-namespace
      name: restored-vm-webapp-eth0
    reservedIPs: ["192.168.10.25", "fd00::10:25"]
  ipAddressAllocationMoves:
  - source:
      namespace: ns-production
      name: vip-ingress-https
    target:
      namespace: destination-namespace
      name: restored-vip-ingress-https
```
