# NetworkResourceTransition API

NetworkResourceTransition is a cluster-scoped API in `migration.vmware.com/v1alpha1`.
The Go package is `pkg/apis/migration/v1alpha1`; generated clients use
`MigrationV1alpha1().NetworkResourceTransitions()`.
VPC resource references continue to identify objects in `crd.nsx.vmware.com/v1alpha1`.
See the [API reference](ref/apis/migration.md) and
[sample requests](../build/yaml/samples/nsx_v1alpha1_networkresourcetransition.yaml).

Install the CRD alongside the VPC CRDs:

```sh
kubectl apply -f build/yaml/crd/vpc/
kubectl apply -f build/yaml/crd/migration/
```

Deployment RBAC must explicitly grant access to `networkresourcetransitions` and
`networkresourcetransitions/status` in `migration.vmware.com`. Permissions for
`crd.nsx.vmware.com` do not cover this group. The optional
[requester role](../build/yaml/migration/rbac/requester-role.yaml) grants request submission
and observation; bind it to the migration orchestrator's configured service account.
Transition controllers and their execution permissions are separate implementation work.

## Rollback request

Each detached SubnetPort restores one reservation. The destination reference and exact
addresses are siblings, with no intermediate reservation wrapper:

```yaml
apiVersion: migration.vmware.com/v1alpha1
kind: NetworkResourceTransition
metadata:
  name: workload-revert
spec:
  subnetPortReverts:
  - subnetPort:
      namespace: workload
      name: port-a
    subnetIPReservation:
      namespace: destination
      name: reservation-a
    reservedIPs: ["192.0.2.25", "2001:db8::25"]
```

`reservedIPs` must contain one or two individual addresses, with at most one IPv4
and one IPv6 address. Execution must verify that they exactly match the source port.
Resolve the source port's actual Subnet CR name and require a same-named Subnet in
the destination Namespace with matching backing identity. No subnet input is needed.

Cutover and revert requests each reference one reservation per port. Each operation
list is limited to 100 entries. Source and destination references must be unique
within their operation list. Forward cutovers and reverts use separate requests;
rollback creates a new request because the original spec is immutable.

## Compatibility

This branch defines NetworkResourceTransition in `migration.vmware.com/v1alpha1`.
ControlPlaneNetworkTransition and NamespaceNetworkTransition are defined separately
on `topic/xl018891/supervisor-network-migration-operator`, using the same API group.

The new group and flattened rollback schema replace the APIs proposed earlier. Update manifests, Go imports, clients, and RBAC together. Existing objects under
`crd.nsx.vmware.com`, `migration.nsx.vmware.com`, or `iaas.vmware.com` are separate Kubernetes resources and are not migrated automatically;
inspect and recreate any test requests deliberately before retiring the old CRD.
