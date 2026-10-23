# Supervisor network migration container

The Supervisor migration framework runs as a separate process/container in the
NCP Pod. It owns the cluster-scoped `migration.vmware.com/v1alpha1` resources
`ControlPlaneNetworkTransition` and `NamespaceNetworkTransition`.

## Framework and services

- `cmd_migration`: independent controller manager; no normal NSX controllers,
  webhook servers, `electMaster` calls, or `nsx-operator-role` label changes.
- `pkg/apis/migration/v1alpha1`: Shared orchestration APIs in migration.vmware.com/v1alpha1.
- `pkg/migration/controllers`: controller registration and observation of both
  transition kinds. Reconciliation leaves spec, status, and external resources unchanged.
- `pkg/migration.Operator.NSX`: lazy `pkg/nsx/services/migration.Service`, reusing
  `pkg/config` and `nsx.GetClient`. Register concrete action handlers with this
  provider before manager startup. Client instances, token refresh, connection
  pools, and rate-limit budgets are process-local, not shared between containers.

Migration observation defaults to disabled. `--enable-network-migration` enables
controller observation only. The framework does not dispatch actions, update
status, checkpoint execution, or enforce cross-resource dependencies. Those
behaviors and their tests are isolated on `topic/xl018891/UBMWM-12405` for
[UBMWM-12405](https://vmw-jira.broadcom.net/browse/UBMWM-12405).
Startup does not load NSX configuration or contact NSX. Missing configuration or
failed client initialization can be retried by future handlers. Migration startup
must not depend solely on `HasVPCNamespaces()`, because a VDS Supervisor may not
have any VPC namespaces before migration preparation.

The CRDs define `EVALUATE`, `PLAN`, `DERIVE`, `EXECUTE`, and `ROLLBACK`; namespace
batches additionally support `COMMIT`, and infrastructure supports `FINALIZE`.
Status fields define the contract for future execution and recovery, including
`lastAction`, `actionState`, `actionGeneration`, and `irreversibleAction`. CRD
validation remains part of the API definitions; no execution checkpoints are
written by this branch. Namespace status defines `name`, `phase`, `reason`, and
`message`; milestones are batch conditions, and counters follow spec membership.

## Design contract

The orchestration API follows [the detailed design, version 146](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=2586034708),
sections 4.1 and 4.2. Control plane inputs use `userInputs.cpVmServiceSubnets`;
namespace inputs use `userInputs.tenantSubnets`. Both contain one entry per `vdn`,
with `dhcpRange.start`, `dhcpRange.end`, `subnetMask`, `gatewayAddress`, and an
optional `subnetCidr` consistency assertion. Missing conditional addressing can
be completed during evaluation. Supplied values remain immutable in the current
schema. Discovery-dependent checks, including subnet membership, ordered DHCP
bounds, unknown VDNs and conflicts between batches, belong to the concrete
handlers, which have not shipped.

`status.nsxWorkflows` is a map list keyed by `vdn`, retaining the exact
`workflowId` for recovery. The execution contract specifies `InfraTransitionComplete`
and the matching condition for infrastructure, and `Completed` for namespace
execution. It specifies `Finalized` for infrastructure finalization and `Committed`
for namespace commit. Execution is implemented separately under UBMWM-12405.
The checked-in `design-*.yaml` test fixtures retain all five current design output
examples. Apply the samples under `build/yaml/samples/migration` to begin with
`EVALUATE`, then request each subsequent action after its prerequisites succeed.

The design explicitly marks input revisions/reset, SNAT fallback confirmation,
resolved inventories, fabric provenance, rollback domains, cutover protection,
operation checkpoints and detailed commit/approval recovery records as proposed
extensions requiring finalized schemas and concrete handlers. This API alignment
does not enable those paths or remove the existing input/PONR guards. In
particular, corrected inputs after rollback still require the revision/reset
implementation before they can be accepted on an existing CR.

## Build and validate

```sh
make generate-migration manifests-migration
make test-migration
make build-migration
```

Use the repository Go toolchain and Kubernetes/controller-runtime versions. CRD
admission requires Kubernetes 1.32+ for CEL two-variable comprehensions. The API
module has its own admission/serialization tests, run explicitly by `test-migration`.
The Photon images include `/usr/local/bin/supervisor-network-migration-operator`;
their existing entrypoint continues to run the normal NSX operator.

## NCP packaging integration

The production NCP Deployment is supplied outside this repository.
`build/yaml/migration/ncp-container.patch.yaml` is a strategic merge integration
patch, not a standalone Deployment. The packaging owner must:

1. Install the migration CRDs and `build/yaml/migration/rbac` before starting the
   new container. Match both bindings to the actual NCP Pod ServiceAccount
   (`nsx-ncp` is the checked-in default); the Pod shares one ServiceAccount.
2. Merge the container patch into the actual NCP Pod template and replace its
   image placeholder with the pinned image containing the migration binary.
3. Mount the existing NSX operator config at `/etc/nsx-operator/nsxop.ini`, along
   with its required CA, authentication/token mounts, and Envoy settings. Reuse
   those supported mounts rather than copying credentials into a new ConfigMap.
4. Keep port 9924 distinct from other host-network listeners. Metrics default to
   disabled. Migration liveness/readiness describe the process and do not depend
   on NSX health or aggregate NCP Pod readiness. Check the NSX operator container
   and its controller state explicitly when implementing migration prerequisites.
5. Preserve an independent lease named
   `supervisor-network-migration-operator.iaas.vmware.com` in `vmware-system-nsx`.
   The migration and NSX leaders may be in different Pods; communicate via APIs
   and CRs, not assumptions about colocated leadership or shared memory.

## Handoff and remaining work

Stop the standalone tera migration controller before enabling this replacement.
Preserve existing transition CRs and explicitly transfer CRD/package ownership;
removing the old Carvel package must not delete the CRDs or their instances.
The old tera bootstrapper/build wiring is removed as part of that coordinated
packaging change, not by this repository alone.

Concrete handlers still need a durable fenced mutation lock, finalizers and
admission protection, current NSX ownership/consumer checks, reviewed plan and
resource provenance records, and approval/onboarding checks. UBMWM-12405 owns action dispatch, persisted execution checkpoints, dependency
gates, and commit/finalize guards. Concrete handlers must recheck dependencies
under the durable lock.
Never duplicate normal NSX controllers or delete active VPC/LB/SNAT resources.
`NetworkResourceTransition` support is a prerequisite for real cutover handlers;
it is not implemented by this scaffold. Container co-location does not replace
these recovery contracts or protect against Pod replacement.

The two orchestration CRDs use `migration.vmware.com/v1alpha1`; resource transfer
uses the separate `NetworkResourceTransition` API and remains an NSX Operator
responsibility. The leader-election
lease keeps its historical name across the API group change.
