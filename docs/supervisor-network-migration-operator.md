# Supervisor network migration container

The Supervisor migration framework runs as a separate process/container in the
NCP Pod. It owns the cluster-scoped `migration.network.vmware.com/v1alpha1` resources
`ControlPlaneNetworkTransition` and `NamespaceNetworkTransition`.

## Framework and services

- `cmd_migration`: independent controller manager; no normal NSX controllers,
  webhook servers, `electMaster` calls, or `nsx-operator-role` label changes.
- `pkg/apis/migration/v1alpha1`: Shared orchestration APIs in migration.network.vmware.com/v1alpha1.
- `pkg/migration/controllers`: controller registration and observation of both
  transition kinds. Reconciliation leaves spec, status, and external resources unchanged.
- `pkg/migration.SetupWithManager`: registers both `TransitionReconciler` instances
  when migration is enabled; no intermediate `Operator` owner is constructed.
- `pkg/nsx/services/migration.Service`: lazy NSX connectivity available for future
  handlers, reusing `pkg/config` and `nsx.GetClient`. The framework does not
  construct or inject an unused service. Sessions and connection pools are
  process-local, not shared between containers.

Migration observation defaults to disabled: no transition controllers, watches,
or leader election are started. Disabled startup loads local Kubernetes REST
configuration without probing API-server health, so an API `/healthz` failure
does not prevent the process health/readiness endpoints from starting. Valid
local REST configuration is still required. Enabled startup retains the existing
API health check and loopback fallback. `--enable-network-migration` enables
controller observation and the independent lease. This startup flag does not implement the vCenter FSS/HTTP
authorization gate; a disabled reconciler does not return HTTP 403.
The framework does not dispatch actions, update
status, checkpoint execution, or enforce cross-resource dependencies. Those
behaviors and their tests are isolated on `topic/xl018891/UBMWM-12405` for
[UBMWM-12405](https://vmw-jira.broadcom.net/browse/UBMWM-12405).
Startup does not load NSX configuration or contact NSX. Missing configuration or
failed client initialization can be retried by future handlers. Migration startup
must not depend solely on `HasVPCNamespaces()`, because a VDS Supervisor may not
have any VPC namespaces before migration preparation.

Both CRDs accept `Evaluate`, `Plan`, `Derive`, and `Execute`. Namespace batches
additionally support `Rollback` and `Commit`; infrastructure supports `Finalize`.
The infrastructure CRD rejects `Rollback` at every phase. Failed infrastructure
actions retain checkpoints for diagnosis and forward retry. Infrastructure
reversion uses the user-operated WCP backup-and-restore procedure outside the
migration API; arrange a usable backup and recovery procedure before migration.
Namespace rollback remains subject to dependency checks and the Commit boundary.
Status fields define the contract for future execution and recovery, including
`lastAction`, `actionState`, `actionGeneration`, and `irreversibleAction`. CRD
validation remains part of the API definitions; no execution checkpoints are
written by this branch. Namespace status defines `name`, `phase`, `reason`, and
`message`; milestones are batch conditions, and counters follow spec membership.

## Status contract (WBS 1.1.3)

The status foundation follows [WBS 1.1.3, version 66](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=3003298182)
and the current status contract in detailed design version 158, Sections 4.1/4.2.
`pkg/apis/migration/v1alpha1` exports phase and condition names for callers.
`pkg/migration/status` supplies reporting and status-subresource persistence helpers:

- `Observe` updates phase, a condition, and `observedGeneration`. It retains
  the checkpointed action and unrelated milestone conditions. Repeating a
  condition preserves its transition timestamp when its truth value is unchanged.
- `RecordAction` records `lastAction`, `actionState`, and `actionGeneration`
  together. Reporting a newer blocked request does not promote the older action
  to success. Neither this helper nor per-namespace progress advances an action
  or establishes the irreversible checkpoint. Callers must verify whole-batch
  restoration and invalidate the previous plan before recording Rollback/Succeeded;
  that checkpoint permits Evaluate-time input corrections in the namespace CRD.
- `RecordWorkflow` records one exact workflow handle per VDN. Repeated reports
  are idempotent; replacing an accepted handle requires a separately defined
  recovery/reset flow.
- `RecordNamespace` updates `name`, `phase`, `reason`, and `message` for one
  member of the immutable batch. Other records and the CR-level phase survive.
  Admission rejects namespace status records outside `spec.namespaces`.
- `UpdateControlPlane` and `UpdateNamespace` patch only status from a persisted
  snapshot. Resource-version preconditions protect concurrent progress and spec
  changes. Callers must read again and recompute on conflict; mutations must
  have no external side effects. No-op reports issue no patch. Writers reject
  regressed generation checkpoints and CR-inappropriate actions: infrastructure
  cannot accept or advance Rollback/Commit; namespaces cannot accept or advance
  Finalize. Unchanged historical checkpoints remain available for diagnosis.
  Shared rollback phase names describe namespace progress only.

Clients call `NamespacePhaseCounts()` to derive counts from batch membership and
per-namespace records. Missing records and empty phases count as `Pending`;
future phase names retain separate buckets. `Completed`, `Committed`,
`RolledBack`, and evaluation failures remain distinct. Counts describe recorded
progress, and do not prove that the current action/generation succeeded. Check
`lastAction`, `actionState`, `actionGeneration`, and the relevant conditions for
that decision. No redundant counter fields or extra status CRs are introduced.

The framework controllers remain observational. The orchestration workstream
can use these helpers when reporting handler outcomes; this branch does not
install concrete handlers or grant additional controller permissions. Status
persistence tests exercise both CR kinds, conflicts and retries, retained
workflow/condition history, and namespace counts. API tests validate the full
1,000-namespace batch and the checked-in design output examples.

## Design contract

The reviewed API uses `migration.network.vmware.com/v1alpha1` and
`spec.networkConfiguration`. DHCP inputs are `namespaceDHCPNetworks` or
`controlPlaneAndServiceDHCPNetworks`, both lists of `DHCPNetworkConfiguration`
keyed by `vdnName`. Workflow records also use `vdnName`. Action values use PascalCase
(`Evaluate`, `Plan`, `Derive`, `Execute`, `Rollback`, `Commit`, `Finalize`).
These are breaking changes to the pre-release API. Clients and dependent branches
must adopt them together; no aliases or conversion webhook are provided.

The orchestration API follows [the detailed design, version 158](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=2586034708),
sections 4.1 and 4.2, and [WBS version 66](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=3003298182).
The `design-*.yaml` fixtures reproduce the updated design examples and are
validated against the generated schemas. Proposed recovery extensions and
concrete handler behavior are explicitly distinguished from this framework.

In 9.2, supported migrations use HAProxy or FLB (`VSPHERE_FOUNDATION`) sources
and an NSX LB (`NSX_LB`) target. The API rejects unsupported provider values
and exposes no Avi configuration. Evaluate handlers must report unsupported
discovered sources through failure conditions before target preparation. The
observational framework does not yet perform provider evaluation. HAProxy requires `networkConfiguration.legacyLbFrontendDvpgs`; FLB
discovers frontend networks and VIP ranges. Unresolved FLB configuration is a
discovery failure, not a prompt to guess network values.

Control plane inputs use `networkConfiguration.controlPlaneAndServiceDHCPNetworks`;
namespace inputs use `networkConfiguration.namespaceDHCPNetworks`. Both contain one entry per `vdnName`,
with `dhcpRange.start`, `dhcpRange.end`, `subnetMask`, and `gatewayAddress`.
The inspection handler derives the subnet CIDR from the range start and mask,
then validates the range end and gateway against that subnet; no separate CIDR
input is exposed. Both network lists use `DHCPNetworkConfiguration` to describe
existing source networks, not to request new DHCP configuration.

While the requested action is `Evaluate` and no planning checkpoint exists,
network configuration may be added, corrected, or removed, including individual
addressing values, network entries, HAProxy frontend port groups, or the whole
configuration. Syntax validation still applies. Edits change the CR generation
and require fresh evaluation. Configuration cannot change in the same update
that requests Plan, or while Plan is pending and `status.lastAction` still
records Evaluate. The same request-time guard applies
to control-plane project/provider selection. The existing checkpoint-based
freeze remains, with one namespace-only exception: `networkConfiguration` may
change with requested action `Evaluate` when the stored `status.lastAction` is
`Rollback`, `status.actionState` is `Succeeded`, and no `irreversibleAction`
checkpoint exists. The action and inputs may change together or in separate
updates. Running, failed, or incomplete rollback checkpoints do not reopen edits.
Syntax validation and the requirement for fresh evaluation still apply; edits
cannot accompany a request for Plan. Infrastructure has no rollback exception.
Discovery-dependent checks, including subnet membership, ordered DHCP
bounds, unknown VDNs and conflicts between batches, belong to the concrete
handlers, which have not shipped.

`status.nsxWorkflows` is a map list keyed by `vdnName`, retaining the exact
`workflowId` for recovery. The execution contract specifies `InfraTransitionComplete`
and the matching condition for infrastructure, and `Completed` for namespace
execution. It specifies `Finalized` for infrastructure finalization and `Committed`
for namespace commit. Execution is implemented separately under UBMWM-12405.
The checked-in `design-*.yaml` test fixtures retain all five current design output
examples. Apply the samples under `build/yaml/samples/migration` to begin with
`Evaluate`, then request each subsequent action after its prerequisites succeed.

For an NSX_LB target, `networkConfiguration.lbSnatIPv4` and `networkConfiguration.lbSnatIPv6`
optionally supply backend fallback addresses when the corresponding VIP pool
cannot provide a reservation during Evaluate. Each field accepts one plain IP
of its declared family; neither is required for the other family. They may be
added, corrected, or removed during Evaluate until the respective family has an
accepted reservation. Requesting Plan freezes all inputs even before its
checkpoint is recorded. See
`build/yaml/samples/migration/controlplane-nsx-lb.yaml`.
The CRD validates syntax and family; availability checks, early reservation,
reservation ownership, and cancellation cleanup require execution handlers.
Before recording `status.lbSnatReservations`, a handler must confirm the source
allocation's ownership, readiness, actual IP, and namespace/name. The accepted
checkpoint freezes that family's fallback input, provider selection, and project.
Checkpoint identity cannot be replaced or cleared. The unreserved address family
can still be supplied during Evaluate. These records do not allocate addresses;
post-planning revision/reset handling remains separate service work. Infrastructure has
no rollback-based reset path.

The design explicitly marks post-planning input revisions/reset, SNAT fallback confirmation,
resolved inventories, fabric provenance, rollback domains, cutover protection,
operation checkpoints and detailed commit/approval recovery records as proposed
extensions requiring finalized schemas and concrete handlers. This API alignment
does not implement those execution paths or remove the irreversible checkpoint
guards. The namespace CRD now permits corrected inputs during Evaluate after a
stored successful rollback. Before recording that success, the concrete rollback
handler must verify restoration of the whole batch and invalidate the previous
plan and derived results. That handler work remains separate; the schema
exception does not perform rollback or verify external state.

Evaluate-to-Plan freshness belongs to admission work WBS 1.1.4.1. For that action
change, require the old object's successful Evaluate checkpoint to have
`status.actionGeneration == oldObject.metadata.generation`. `observedGeneration`
alone is not proof of success; the new generation changes with the Plan request.
This separately tracked admission integration remains pending. No
`evaluatedSpecHash` field is added.

VCFA namespace onboarding and project selection belong to the VCFA client
workstream: every namespace must be imported before confirmation/Commit, using
an explicitly selected project or a find-or-create project named after the
namespace. These mappings are not additional transition CR inputs. VC-only
migration uses the same namespace transition API without VCFA onboarding.

## Build and validate

```sh
make generate-migration manifests-migration
make test-migration
make build-migration
make docker-build-migration MIGRATION_IMG=supervisor-network-migration-operator:latest
```

Use the repository Go toolchain and Kubernetes/controller-runtime versions. CRD
admission is validated with the repository-pinned Kubernetes 1.35 API-server
libraries and their standard CEL compatibility settings. The API module has its
own admission/serialization tests, run by `test-migration-api` in CI
and included in `test-migration`.
The existing NSX operator and coverage images contain only their own binaries.
Build the separate image with `make docker-build-migration`; its Dockerfile is
`build/image/migration/Dockerfile`. Its entrypoint runs
`/usr/local/bin/supervisor-network-migration-operator`. The existing NSX operator
images retain their manager entrypoint.

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
5. When migration is enabled, preserve an independent lease named
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
gates, and commit/finalize guards. Its follow-on branch must adopt the new
namespace-only Rollback contract before integration. Concrete handlers must recheck dependencies
under the durable lock.
Never duplicate normal NSX controllers or delete active VPC/LB/SNAT resources.
`NetworkResourceTransition` support is a prerequisite for real cutover handlers;
it is not implemented by this scaffold. Container co-location does not replace
these recovery contracts or protect against Pod replacement.

The two orchestration CRDs use `migration.network.vmware.com/v1alpha1`; resource transfer
uses the separate `NetworkResourceTransition` API and remains an NSX Operator
responsibility. The leader-election
lease keeps its historical name across the API group change.
