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

## NSX resource recovery contract

`status.nsxWorkflows` retains the workflow ID for each source VDN. Conversion
handlers query `GET /policy/api/v1/workflows/{workflow-id}/status` and use the
workflow's per-resource paths, types and progress as the conversion inventory.
The CRDs do not duplicate that inventory in `status.fabricResources` and do not
expose per-resource consumer lists or creation/adoption provenance.

A planned path is not proof of realization, and a resource reference does not
establish ownership. Handlers must validate workflow/source/target identity,
resolve inconsistent aggregate and per-resource states, and verify live resource
readiness before reporting action success. The framework defines the workflow
reference only; it does not implement the workflow-status reader.

Operator-created prerequisites outside the conversion workflow use deterministic
IDs and live ownership checks. Existing primary/LB VPC paths and accepted SNAT
reservation checkpoints retain their dedicated purposes. Without an independently
retained expected unique ID, a resource at the same path cannot be distinguished
from an earlier incarnation. Shared-resource cleanup requires live dependency and
ownership verification; deleting a transition does not authorize NSX deletion.

## Resource naming inputs

Both transition CRDs accept optional naming overrides per source VDN. Supplied
values become both NSX resource IDs and display names; users do not supply internal
resource keys. The migrator generates deterministic names for omitted fields when
new resources are needed. Missing names never block Evaluate or appear in
`status.requiredInputs`. That list is reserved for required unresolved inputs,
such as missing DHCP configuration or an applicable SNAT fallback address.

```yaml
spec:
  action: Evaluate
  networkConfiguration:
    resourceNaming:
      networks:
      - vdnName: vdn-source-1
        tgwName: migration-tgw
        vpcName: migration-vpc
        dvcName: migration-dvc
        subnetName: migration-subnet
```

ControlPlaneNetworkTransition must inspect **all** VDNs used by CPVMs and
Supervisor Services, including static and DHCP-backed networks. NamespaceNetworkTransition
inspects all VDNs used by `spec.namespaces`. Neither discovery scope is limited to
entries in the DHCP input lists. Infrastructure naming also retains the separate
optional `loadBalancerVpcName` for the dedicated LB VPC.

For each VDN, Evaluate must:

1. Resolve all backing DVPGs and their VDSes. Derive the zone-based span from the
   full set of those VDSes, with canonical zone identities and order-independent
   comparison. Do not assume a VDN has only one DVPG or VDS.
2. Search for a compatible TGW with the same span in the resolved NSX scope.
   Validate the associated VPC and attachment before reusing them. A matching
   span alone does not authorize adoption or select one arbitrary VPC. Multiple
   candidates, inconsistent attachments or an incomplete pair need diagnostics.
3. If new TGW/VPC topology is needed, use supplied `tgwName`/`vpcName` overrides
   or generate omitted names using the naming pattern. Validate supplied names
   and collisions before preparation. VDNs sharing topology must resolve the same
   identities, including when one entry supplies names and another omits them.
4. Verify that **all DVPGs in a VDN use the same VLAN**. Derive the VLAN from
   source inventory; users do not supply a VLAN ID for naming. Mixed-VLAN backing
   is an invalid VDN for migration and must produce a diagnostic before creating
   resources, rather than being split into multiple VLAN subnets.
5. Under the resolved VPC/span, find the VDN's single compatible VLAN extension
   subnet and DVC. Reuse valid existing resources without requesting their names.
   If creation is required, use supplied `dvcName`/`subnetName` overrides and
   generate any omitted names. Preserve existing identities when only one resource
   is missing. Verify
   source bindings, VLAN, addressing, span and attachment; a CIDR or display name
   match alone is insufficient. Incompatible addressing/backing must be reported,
   not resolved by silently creating another subnet.

VDNSubnetLink is a source of existing mapping information, not the sole test for
reusable topology. Validate source identity, target subnet/VPC, readiness and live
NSX resources. Missing links must not cause duplicate creation when compatible
NSX topology exists. Pending/deleting/invalid links are retried or reported rather
than treated as permission to create replacements. Reconcile missing mappings
through the mapping service after validating existing topology.

Names for existing resources are unnecessary; supplied conflicting names must
be reported instead of renaming or recreating resources. Missing DHCP configuration
is different: Evaluate reports NeedsInput/requiredInputs, identifying the VDN and
its required DHCP entry, until that data is supplied. Optional names may be added
to the same spec update, but are not themselves required-input diagnostics.
The schema cannot inspect NSX/Net Operator or determine DHCP discovery results.

The same per-VDN `dvcName`/`subnetName` shape applies to CPVM/Supervisor Service
VDNs and namespace VDNs. There is no per-VLAN naming list. Each VDN entry has at
most one DVC/subnet name pair, shared by all of its backing DVPGs. VLAN IDs remain
scoped to their resolved VPC/span, not globally unique. Evaluate must reject unknown
VDNs, mixed-VLAN backing and conflicting names for shared topology. The schema
allows omitted/partial names: discovery chooses reuse or creation, and the migrator
generates missing creation names. It cannot validate VLAN consistency from CR input alone.

### Optional naming discovery for the UI

Both CRDs expose `status.optionalInputs` with the same `key`/`description` shape
as `status.requiredInputs`. A key identifies the spec field and source VDN, for
example `spec.networkConfiguration.resourceNaming.networks[vdnName=vdn-cp-dhcp].tgwName`.
Only required missing data blocks Evaluate. An omitted optional name uses the
migrator's generated default. The UI groups the advertised paths by VDN.

A two-VDN example is validated in
`pkg/apis/migration/v1alpha1/testdata/design-controlplane-two-vdns.yaml`:

```yaml
status:
  observedGeneration: 1
  phase: NeedsInput
  requiredInputs:
  - key: spec.networkConfiguration.controlPlaneAndServiceDHCPNetworks[vdnName=vdn-cp-dhcp]
    description: Supply the DHCP range, mask and gateway; vdn-services-static needs no DHCP input.
  optionalInputs:
  - key: spec.networkConfiguration.resourceNaming.networks[vdnName=vdn-cp-dhcp].tgwName
    description: Optional TGW name for vdn-cp-dhcp; otherwise generated.
  - key: spec.networkConfiguration.resourceNaming.networks[vdnName=vdn-services-static].tgwName
    description: Optional TGW name for vdn-services-static; otherwise generated.
```

The matching user spec is validated in
`pkg/apis/migration/v1alpha1/testdata/design-controlplane-two-vdns-inputs.yaml`.
It contains one DHCP entry and two resourceNaming entries. It illustrates two
VDNs sharing a TGW/VPC span while using separate VLAN extension subnets/DVCs;
shared TGW/VPC overrides therefore agree. Distinct spans can use distinct names.

The full fixture advertises all four name fields for both VDNs. Each field has a
separate key/description entry, and keys are unique. Omit entries for reused or
frozen resources and withdraw a name once creation has started or its outcome is
unresolved. The UI offers only advertised options, writes chosen values into the
matching `spec.networkConfiguration.resourceNaming.networks` entries, and submits
them together with required DHCP data while retaining `spec.action: Evaluate`.
It never writes status. Static VDNs require no DHCP entry.

Publish or clear `requiredInputs` and `optionalInputs` together with
`status.observedGeneration` for the inspected spec generation. The UI compares it
with `metadata.generation` and treats a mismatch as stale. Advancing the observed
generation must not make old optional diagnostics appear current: clear entries
that were not revalidated. After spec changes, reevaluate and refresh both lists.
Handlers revalidate topology, sharing, action/input-edit guards and creation state
before accepting values; a status hint is advisory, not an authorization to rename
resources. An absent list is not proof that discovery is complete.

Optional diagnostics do not pause automatic preparation. Missing DHCP data gives
the user an opportunity to submit optional names before creation. If required
inputs are already complete, Evaluate may continue using generated names; users
can also provide overrides in the initial spec. Runtime diagnostic publication,
default generation and eligibility checks remain handler work; the observational
framework defines the schema only.

Names follow the fabric ID syntax: 1–255 ASCII letters, digits, underscores,
hyphens or dots, excluding `.` and `..`. Explicit empty strings are invalid.
Validate endpoint-specific limits, unknown VDNs, shared-topology naming conflicts,
name collisions and ownership before mutation. Resolve and freeze the identity
before creation, including requests with unknown outcomes. Generated names must
remain stable across retries, process restarts and batches sharing topology; do not
regenerate a different identity after a lost reply. Names cannot rename
retained resources when returning to Evaluate. Existing Plan/input-edit guards
continue to apply; successful namespace rollback can reopen creation naming only
for resources proven removed and no longer shared.

The framework defines the input schema and does not perform Evaluate discovery,
TGW span searches, subnet checks, naming resolution, required DHCP diagnostics
or NSX creation.
Concrete Evaluate, fabric and VDN mapping handlers must implement these rules.
Workflow-managed DVC/subnet naming also requires the owning Plan/Conversion API
to accept and preserve the supplied IDs and display names. The published detailed design (version 163), WBS (version 71), architecture
(version 133), one-pager (version 85), and naming reference (version 14) now describe
the optional-override, topology-reuse and `status.optionalInputs` UI contract,
including a validated two-VDN example with required DHCP data for only one VDN.
Runtime integration and acceptance tests remain handler work.

## Design contract

The reviewed API uses `migration.network.vmware.com/v1alpha1` and
`spec.networkConfiguration`. DHCP inputs are `namespaceDHCPNetworks` or
`controlPlaneAndServiceDHCPNetworks`, both lists of `DHCPNetworkConfiguration`
keyed by `vdnName`. Workflow records also use `vdnName`. Action values use PascalCase
(`Evaluate`, `Plan`, `Derive`, `Execute`, `Rollback`, `Commit`, `Finalize`).
These are breaking changes to the pre-release API. Clients and dependent branches
must adopt them together; no aliases or conversion webhook are provided.

The orchestration API follows [the detailed design, version 163](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=2586034708),
sections 4.1 and 4.2, and [WBS version 71](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=3003298182).
The [architecture, version 133](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=2535758740),
[one-pager, version 85](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=2726684074),
and [naming reference, version 14](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=2539833087)
use the same per-VDN naming and topology-reuse contract. Detailed design Section
4.1.2 distinguishes the committed schema from the concrete handler work.
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
