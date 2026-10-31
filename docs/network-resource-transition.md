# NetworkResourceTransition API

NetworkResourceTransition is a cluster-scoped API in `migration.network.vmware.com/v1alpha1`.
The Go package is `pkg/apis/migration/v1alpha1`; generated clients use
`MigrationV1alpha1().NetworkResourceTransitions()`.
VPC resource references continue to identify objects in `crd.nsx.vmware.com/v1alpha1`.
See the [API reference](ref/apis/migration.md) and
[sample requests](../build/yaml/samples/nsx_v1alpha1_networkresourcetransition.yaml).

Install the CRD alongside the VPC CRDs:

```sh
kubectl apply -f build/yaml/crd/vpc/
kubectl apply -k build/yaml/crd/migration/
```

Requester and controller RBAC manifests will be added with controller integration.
This API-only change does not grant resource access.

## Rollback request

Each detached SubnetPort restores one reservation. The destination reference and exact
addresses are siblings, with no intermediate reservation wrapper:

```yaml
apiVersion: migration.network.vmware.com/v1alpha1
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
list in spec and status is limited to 100 entries. Source and destination references
must be unique within their operation list. An allocation move must use different
source and target objects. Forward cutovers and reverts use separate requests;
rollback creates a new request because the original spec is immutable.

## Compatibility

This branch defines NetworkResourceTransition in `migration.network.vmware.com/v1alpha1`.
ControlPlaneNetworkTransition and NamespaceNetworkTransition are defined separately
on `topic/xl018891/supervisor-network-migration-operator`, using the same API group.

The new group and flattened rollback schema replace the APIs proposed earlier. Update manifests, Go imports, clients, and RBAC together. Existing objects under
`crd.nsx.vmware.com`, `migration.nsx.vmware.com`, `migration.vmware.com`, or `iaas.vmware.com` are separate Kubernetes resources and are not migrated automatically;
inspect and recreate any test requests deliberately before retiring the old CRD.

This PR defines the API and generated client support. The main NSX Operator manager
will register the migration scheme when its transition controller is implemented;
no transition controller or NSX transfer execution is wired by this API-only change.
