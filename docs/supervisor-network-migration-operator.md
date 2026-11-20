# Supervisor network migration container

The Supervisor migration framework runs as a separate process/container in the
NCP Pod. It owns the cluster-scoped `migration.network.vmware.com/v1alpha1` resources
`ControlPlaneNetworkTransition` and `NamespaceNetworkTransition`.

## Framework and services

- `cmd_migration` runs an independent controller manager without starting the
  normal NSX controllers, calling `electMaster`, or changing `nsx-operator-role`.
  The admission server can be enabled independently of transition controllers.
- `pkg/apis/migration/v1alpha1` defines the cluster-scoped transition APIs.
- `pkg/migration/controllers` and `pkg/migration/transition` provide action
  dispatch, durable checkpoints, dependency gates, and restart recovery.
- `pkg/nsx/services/migration.Service` initializes NSX connectivity lazily for
  handlers; connections are process-local.

Migration dispatch defaults to disabled. Disabled startup does not start
transition controllers, watches, or leader election and does not probe API-server
health, though valid local REST configuration is still required. Enabling
`--enable-network-migration` starts dispatch and its independent lease; it does
not implement the vCenter FSS/HTTP authorization gate. Startup does not contact
NSX. `HasVPCNamespaces()` is not a valid startup prerequisite because a VDS
Supervisor may not yet have VPC namespaces.

Both CRDs accept `Evaluate` and `Execute`. Namespace batches also accept
`Rollback` and `Commit`; infrastructure accepts `Finalize` and rejects
`Rollback`. The dispatcher persists action/generation checkpoints before
invoking handlers. Namespace `Commit` validates completed per-Namespace status
and shared workflow references, then asks the controller to persist
`status.irreversibleAction: Commit` before deleting the recorded workflow IDs.
Retries reuse those IDs, including after partial deletion. Failed infrastructure
actions retain checkpoints for forward recovery. Infrastructure reversion uses
WCP backup and restore outside this migration API.

`lastAction`, `actionState`, and `actionGeneration` identify the accepted work.
`irreversibleAction` records a point of no return and cannot be cleared. A
successful namespace Rollback sets `rollbackComplete`; this checkpoint survives
subsequent Evaluate so infrastructure Finalize can recognize the restored batch.
A new Execute, Rollback, or Commit clears it. Namespace status records progress
per member of the immutable batch; aggregate counts are derived from
`spec.namespaces` and `namespaceStatuses`.

## Status contract (WBS 1.1.3)

The status foundation follows [WBS 1.1.3, version 77](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=3003298182)
and detailed design version 187, Sections 4.1/4.2. `pkg/apis/migration/v1alpha1`
exports phase and condition names. `pkg/migration/status` provides reporting and
status-subresource persistence helpers:

- `Observe` updates phase, a condition, and `observedGeneration`, retaining the
  checkpointed action and unrelated milestone conditions.
- `RecordAction` records `lastAction`, `actionState`, and `actionGeneration`
  together. A blocked request does not promote older work to success.
- `RecordWorkflow` records one exact workflow handle per VDN. Replacing an
  accepted handle requires a separately defined recovery/reset flow.
- `RecordNamespace` updates one member of the immutable batch while preserving
  other records. Admission rejects progress entries outside `spec.namespaces`.
- `UpdateControlPlane` and `UpdateNamespace` patch only status from a persisted
  snapshot. Resource-version preconditions protect concurrent progress and spec
  changes; callers must recompute after conflicts and avoid external side effects
  inside patch mutations.

`NamespacePhaseCounts()` derives counts from the batch membership and per-namespace
records. Missing records and empty phases count as `Pending`. Counts describe
recorded progress and do not prove that the current action/generation succeeded;
check the checkpoint and relevant conditions for that decision.

## NSX resource recovery contract

`status.nsxWorkflows` retains the workflow ID for each source VDN. Conversion
handlers use the SDK workflow GET (`/policy/api/v1/workflows/{workflow-id}`)
and read `workflow_state.migration_status` for per-resource paths, types and
progress as the conversion inventory.
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
optional `loadBalancerVPCName` for the dedicated LB VPC.

For each VDN, Evaluate must:

1. Resolve all backing DVPGs and their VDSes. Derive the zone-based span from
   Supervisor-enabled clusters attached to those VDSes, with canonical zone
   identities and order-independent comparison. Do not assume a VDN has only one
   DVPG or VDS.
2. Search for a compatible TGW with the same span in the resolved NSX scope.
   Validate the associated VPC and attachment before reusing them. A matching
   span alone does not authorize adoption or select one arbitrary VPC. Multiple
   candidates, inconsistent attachments or an incomplete pair need diagnostics.
3. If new TGW/VPC topology is needed, use supplied `tgwName`/`vpcName` overrides
   or generate omitted names using the naming pattern. Validate supplied names
   and collisions before preparation. VDNs sharing topology must resolve the same
   identities, including when one entry supplies names and another omits them.
4. For each backing DVPG, find a PortSetting tagged as created by migration for
   that DVPG. Resolve its subnet, DVC and VPC and reuse the conversion. Report
   ambiguous or incomplete matches instead of starting another conversion.
5. If no tagged PortSetting exists, the handler invokes the DVPG conversion
   workflow, which derives the VLAN. Apply the migration tag to the resulting
   PortSetting and use its resolved subnet and DVC when creating the
   VDNSubnetLink. Use supplied `dvcName`/`subnetName` overrides or generate names.
   Private VLAN networks remain unsupported. The workflow integration must
   validate any remaining VLAN eligibility and multi-DVPG consistency.

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
VDNs and Namespace VDNs. There is no per-VLAN naming list. Each VDN entry has at
most one DVC/subnet name pair, shared by all of its backing DVPGs. VLAN IDs remain
scoped to their resolved VPC/span, not globally unique. Evaluate must reject unknown
VDNs, mixed-VLAN backing and conflicting names for shared topology. The schema
allows omitted/partial names: discovery chooses reuse or creation, and the migrator
generates missing creation names. It cannot validate VLAN consistency from CR input alone.

### Optional naming discovery for the UI

Both CRDs expose `status.optionalInputs` with the same
`fieldName`/`description` shape as `status.requiredInputs`. A `fieldName`
identifies the spec field and source VDN, for example
`spec.networkConfiguration.resourceNaming.networks[vdnName=vdn-cp-dhcp].tgwName`.
Only required missing data blocks Evaluate. An omitted optional name uses the
migrator's generated default. The UI groups the advertised paths by VDN.

A two-VDN example is validated in
`pkg/apis/migration/v1alpha1/testdata/design-controlplane-two-vdns.yaml`:

```yaml
status:
  observedGeneration: 1
  phase: NeedsInput
  requiredInputs:
  - fieldName: spec.networkConfiguration.controlPlaneAndServiceDHCPNetworks[vdnName=vdn-cp-dhcp]
    description: Supply the DHCP range, mask and gateway; vdn-services-static needs no DHCP input.
  optionalInputs:
  - fieldName: spec.networkConfiguration.resourceNaming.networks[vdnName=vdn-cp-dhcp].tgwName
    description: Optional TGW name for vdn-cp-dhcp; otherwise generated.
  - fieldName: spec.networkConfiguration.resourceNaming.networks[vdnName=vdn-services-static].tgwName
    description: Optional TGW name for vdn-services-static; otherwise generated.
```

The matching user spec is validated in
`pkg/apis/migration/v1alpha1/testdata/design-controlplane-two-vdns-inputs.yaml`.
It contains one DHCP entry and two resourceNaming entries. It illustrates two
VDNs sharing a TGW/VPC span while using separate VLAN extension subnets/DVCs;
shared TGW/VPC overrides therefore agree. Distinct spans can use distinct names.

The full fixture advertises all four name fields for both VDNs. Each field has a
separate `fieldName`/`description` entry. `fieldName` is the full spec field path,
including the VDN selector, and must be unique within each input list. Omit
entries for reused or frozen resources and withdraw a name once creation has started or its outcome is
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

The inspection implementation is in `pkg/migration/inspection`. For the
currently supported single-DVPG VDN, it reads the observed VLAN type from
`VSphereDistributedNetwork.status.defaultPortConfig.vlan`. Missing status is
retried; trunk and private VLANs are rejected. This is the DVPG's default
port configuration and does not describe individual port overrides. VLAN ID
discovery and validation are delegated to the conversion workflow. Inspection
intersects each VDS cluster span with the current Supervisor topology and resolves
zones only for the Supervisor-enabled clusters. Other VDS clusters do not affect
the TGW/VPC span or cause missing-zone diagnostics. Inspection compares zone spans independent of order before considering a
TGW/VPC pair reusable. The target inventory must also resolve the TGW's effective
VDS attachments, including any custom VDS attachments, to vCenter VDS UUIDs; an
unknown or different attachment set blocks reuse. Reuse also requires a ready
VPC and a valid TGW attachment. Conversion reuse requires a migration-tagged
PortSetting with verified DVPG identity and resolved subnet/DVC. A DVC created by DVPG conversion does not provide
effective zone availability, so inspection validates the TGW/VPC span and VDS
attachments without requiring DVC zone data. A second VDN using the same DVPG
may reuse that pair. Ambiguous or incomplete candidates fail
Evaluate; they do not trigger a second subnet creation. Evaluate
publishes optional name paths alongside missing DHCP inputs, including while
the DHCP values are still missing.

`VCenterZoneResolver` reads the specific Supervisor's zone/cluster topology and maps the zone to its
NSX-discovered external ID using the NSX compute-manager ID. A vCenter zone not
yet discovered by NSX is retried, not reported as a missing vCenter zone.
`NSXTargetInventory` reads the project TGWs, VPCs, connectivity profiles,
attachments, VLAN subnets and migration-tagged PortSettings. `NewService` wires
these readers to authenticated clients; the controller must initialize and
own those clients. Setup also supplies the authoritative Supervisor ID. It supplies a direct Kubernetes reader and a status client
once at setup; the Namespace handler then calls
`inspector.EvaluateNamespaces(ctx, transition)`. The package acceptance tests
exercise unsupported trunk VLANs, missing zone data,
order-independent multi-zone reuse, tagged PortSetting reuse, and
optional name reporting. They use test inventories; a live NSX testbed result
is separate acceptance evidence and is not implied by these tests.

`NSXPortSettingReader` lists migration-tagged PortSettings in one NSX subnet,
including all pages. The shared `DVPGTag` helper uses scope
`migration.network.vmware.com/dvpg` and value `<vCenter UUID>:<DVPG MoID>`;
the conversion handler must apply this tag to the converted PortSetting.
The target inventory resolves explicitly configured custom VDS attachments via
NSX inventory. Effective default VDS attachments are not yet resolved; a TGW
without explicit attachments is therefore not considered verified for reuse.
The conversion handler must still write the tag and reconcile VDNSubnetLinks.

Evaluate reports optional naming diagnostics without creating target resources. Users
can supply overrides during Evaluate or in the initial spec; Execute uses generated
names where overrides are omitted. Runtime diagnostic publication,
default generation and eligibility checks remain handler work; the observational
framework defines the schema only.

Names follow the fabric ID syntax: 1–255 ASCII letters, digits, underscores,
hyphens or dots, excluding `.` and `..`. Explicit empty strings are invalid.
Validate endpoint-specific limits, unknown VDNs, shared-topology naming conflicts,
name collisions and ownership before mutation. Resolve and freeze the identity
before creation, including requests with unknown outcomes. Generated names must
remain stable across retries, process restarts and batches sharing topology; do not
regenerate a different identity after a lost reply. Names cannot rename
retained resources when returning to Evaluate. Existing Execute/input-edit guards
continue to apply; successful Namespace rollback can reopen creation naming only
for resources proven removed and no longer shared.

The framework defines the input schema and does not perform Evaluate discovery,
TGW span searches, subnet checks, naming resolution, required DHCP diagnostics
or NSX creation.
Concrete Evaluate, fabric and VDN mapping handlers must implement these rules.
Workflow-managed DVC/subnet naming also requires the owning Plan/Conversion API
to accept and preserve the supplied IDs and display names. The published detailed design (version 187), WBS (version 77), architecture
(version 145), one-pager (version 97), and naming reference (version 14) now describe
the optional-override, topology-reuse and `status.optionalInputs` UI contract,
including a validated two-VDN example with required DHCP data for only one VDN.
Runtime integration and acceptance tests remain handler work.
## Network inspection (WBS 1.4.2)

`pkg/migration/inspection` implements the read-only network inspection shared
service from WBS version 66 and detailed design version 158:

- `KubernetesInventory` resolves namespaced Net Operator `Network` provider
  references to cluster-scoped `VSphereDistributedNetwork` resources. Shared
  VDNs are deduplicated. The reader uses the official `net-operator-api/v1alpha1`
  Network, VDN, and IPPool Go types, registered in the manager scheme.
  DHCP is determined from `spec.ipAssignmentMode`; absent
  mode uses Net Operator's documented `staticpool` default, and unsupported
  modes fail inspection. Net Operator enforces the mode's IPPool contract;
  inspection reads the referenced static IPPools to derive migration addressing.
- `VCenterTopology` uses a govmomi property collector to resolve the vCenter
  UUID, DVPG managed-object ID, DVS identity/UUID, and attached clusters. The
  vCenter UUID plus DVPG ID is the source network identity used for migration
  tags and network comparisons. `Topology.SameBacking` compares this identity;
  VLAN ID or CIDR alone does not prove shared backing. Missing topology fails
  closed. Callers
  provide the authenticated vCenter session and a direct Kubernetes API reader.
- `VCenterFLBNetworkReader` reads the Supervisor edge list from
  `/api/vcenter/namespace-management/supervisors/{supervisor}/networks/edges`.
  It keeps only the vSphere Foundation edge's `FRONTEND` and `WORKLOAD`
  interface network IDs and VIP ranges; credential-bearing edge fields are not
  decoded. Since the edge API may identify a DVPG by name while Net Operator
  supplies its managed-object ID, `VCenterTopology` also records the DVPG name
  for the same-vCenter match.
- `Service.Inspect` validates administrator entries against discovered VDNs,
  rejects duplicate/unknown entries and conflicting shared-DVPG configurations,
  and returns normalized source addressing. Validation covers contiguous IPv4
  masks, address families, ordered usable ranges, gateway membership and pool
  overlap, deriving CIDR from range start and mask without a separate CIDR input. Source subnet inputs remain IPv4
  under the current dotted-decimal mask contract; this does not change IPv6 LB
  SNAT input support. Existing workload addresses may lie outside the DHCP pool
  while remaining inside the subnet. `Service.Inspect` does not allocate
  addresses; the Evaluate SNAT selector reports the source from which the
  handler should reserve the LB SNAT address when required.
  Static VDNs on the same DVPG may use different IPPool ranges when their CIDR
  and gateway match and the ranges do not overlap.
- `EvaluateControlPlane` and `EvaluateNamespaces` persist diagnostics through
  the optimistic status writers. Missing DHCP fields produce `NeedsInput`, a
  false `NetworkInputsValidated` condition, and one stable `requiredInputs`
  entry per VDN naming the missing fields and the applicable
  `spec.networkConfiguration` DHCP list. Each field name identifies the list
  and VDN, for example `spec.networkConfiguration.namespaceDHCPNetworks[vdnName=workload]`.
  Invalid supplied configuration takes
  precedence and produces `EvaluationFailed`; unavailable discovery produces
  `Blocked`/Unknown and returns the error for retry. Unrelated inputs and action
  checkpoints are preserved. Success records only this inspection milestone,
  leaving the phase `Evaluating` until the other Evaluate work completes.

The control-plane handler must pass the authoritative CPVM/Supervisor Service
VDN set obtained from WCP configuration; administrator inputs never determine
inspection scope. Namespace discovery uses the selected namespaces' source
Network resources. Re-run discovery after a status conflict and before later
mutation; returned topology is an observation, not a lock or reservation.
Cross-batch ownership, NSX fabric validation, and final reservation/consumer
rechecks belong to the respective orchestration and coordination services.
This package does not create a separate resolved-subnet status inventory.

The framework controllers still only observe CRs. Wiring these service methods
into Evaluate and supplying the WCP scope/vCenter session belongs to the stage
handlers. RBAC includes source inventory reads and transition status patches,
but no source-network writes. Tests cover the live govmomi simulator, Kubernetes
inventory reads, validation failures, both CR status writers, stale snapshots,
missing-input recovery, and preservation of unrelated status.

## Design contract

The reviewed API uses `migration.network.vmware.com/v1alpha1` and
`spec.networkConfiguration`. DHCP inputs are `namespaceDHCPNetworks` or
`controlPlaneAndServiceDHCPNetworks`, both lists of `DHCPNetworkConfiguration`
keyed by `vdnName`. Workflow records also use `vdnName`. Action values use PascalCase
(`Evaluate`, `Execute`, `Rollback`, `Commit`, `Finalize`).
These are breaking changes to the pre-release API. Clients and dependent branches
must adopt them together; no aliases or conversion webhook are provided.

The orchestration API follows [the detailed design, version 187](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=2586034708),
sections 4.1 and 4.2, and [WBS version 77](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=3003298182).
The [architecture, version 145](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=2535758740),
[one-pager, version 97](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=2726684074),
and [naming reference, version 14](https://vmw-confluence.broadcom.net/pages/viewpage.action?pageId=2539833087)
use the same per-VDN naming and topology-reuse contract. Detailed design Section
4.1.2 distinguishes the committed schema from the concrete handler work.
The `design-*.yaml` fixtures reproduce the updated design examples and are
validated against the generated schemas. Proposed recovery extensions and
concrete handler behavior are explicitly distinguished from this framework.

In 9.2, supported migrations use a Foundation Load Balancer (FLB,
`VSPHERE_FOUNDATION`) source and an NSX LB (`NSX_LB`) target. HAProxy and Avi
sources and targets are unsupported. Evaluate handlers must report unsupported
discovered sources through failure conditions before target preparation. The
observational framework does not yet perform provider evaluation. FLB discovers
frontend networks and VIP ranges; no frontend-DVPG input is exposed in the CRD.
Unresolved FLB configuration is a discovery failure, not a prompt to guess
network values.

If HAProxy or Avi source migration is supported in a future release, the API
will need administrator-provided frontend DVPGs used by the Supervisor on VDS.
Frontend-DVPG input and provider-specific validation must be introduced with
that support; removing the current field does not make those sources eligible.

Control plane inputs use `networkConfiguration.controlPlaneAndServiceDHCPNetworks`;
namespace inputs use `networkConfiguration.namespaceDHCPNetworks`. Both contain one entry per `vdnName`,
with `dhcpRange.start`, `dhcpRange.end`, `subnetMask`, and `gatewayAddress`.
The inspection handler derives the subnet CIDR from the range start and mask,
then validates the range end and gateway against that subnet; no separate CIDR
input is exposed. Both network lists use `DHCPNetworkConfiguration` to describe
existing source networks, not to request new DHCP configuration.

While the requested action is `Evaluate` and no execution checkpoint exists,
network configuration may be added, corrected, or removed, including individual
addressing values, network entries, or the whole configuration. Syntax validation
still applies. Edits change the CR generation and require fresh evaluation. Configuration cannot change in the same update
that requests Execute, or while Execute is pending and `status.lastAction` still
records Evaluate. The same request-time guard applies
to control-plane project/provider selection. The existing checkpoint-based
freeze remains, with one Namespace-only exception: `networkConfiguration` may
change with requested action `Evaluate` when the stored `status.lastAction` is
`Rollback`, `status.actionState` is `Succeeded`, `status.phase` is `RolledBack`,
both `status.actionGeneration` and `status.observedGeneration` match the current
metadata generation, and no `irreversibleAction` checkpoint exists. This records
verified whole-batch restoration and invalidates the previous Execute results.
The action and inputs may change together or in separate updates. Running, failed,
stale, or incomplete rollback checkpoints do not reopen edits.
Syntax validation and the requirement for fresh evaluation still apply; edits
cannot accompany a request for Execute. Infrastructure has no rollback exception.
Discovery-dependent checks, including subnet membership, ordered DHCP
bounds, unknown VDNs and conflicts between batches, belong to the concrete
handlers, which have not shipped.

`status.nsxWorkflows` is a map list keyed by `vdnName`, retaining the exact
`workflowID` for recovery. This branch reports `InfraTransitionComplete` and the
matching condition for infrastructure execution, and `Completed` for Namespace
execution. Infrastructure finalization reports `Finalized`; Namespace commit
reports `Committed`.
The checked-in `design-*.yaml` test fixtures retain all five current design output
examples. Apply the samples under `build/yaml/samples/migration` to begin with
`Evaluate`, then request each subsequent action after its prerequisites succeed.

Supervisor networks backed by VDS are IPv4-only. For an NSX_LB target,
`networkConfiguration.lbSNATIPs` is an array that currently accepts at most one
plain IPv4 address. Evaluate resolves the FLB backend Network and compares its
actual identity and backing with the VIP/frontend and workload networks:

1. If the backend is the VIP or a workload network, select that managed Network
   as the allocation source.
2. If the backend is separate and the user supplies `lbSNATIPs`, validate its
   IPv4 syntax. The allocation handler must also verify backend membership and
   check conflicts where authoritative information is available. The
   administrator is responsible for availability if exclusive ownership cannot
   be verified.
3. If the backend is separate and no address is supplied, select the VIP pool
   as the allocation source and report a non-blocking warning to verify backend
   reachability. The allocation handler must fail Evaluate if it cannot reserve
   an address.

The inspection result carries the selected source and any VIP ranges in
`Result.LBSNAT`. The Evaluate handler must perform the allocation and record the
accepted address in `status.lbSNATIPAllocations`; that handler is not part of
this inspection package. The warning for VIP-pool fallback is included in the
`NetworkInputsValidated` condition message. Namespace inspection exposes
`Result.SourceTopologies` even while DHCP inputs are missing; the control-plane
handler can pass those topologies to `EvaluateControlPlane` so backend/workload
matching does not wait for DHCP corrections.

Do not infer network identity from VLAN ID or CIDR alone. The array shape is
retained so a future release can add IPv6 support if Supervisor VDS supports it.
The input may be added, corrected, or removed during Evaluate until its allocation
is accepted. Once
`status.lbSNATIPAllocations` records an accepted allocation,
`spec.networkConfiguration.lbSNATIPs` cannot be added, changed, or removed
during Evaluate. Requesting Execute freezes all inputs even before its checkpoint
is recorded. DHCP range bounds and gateways for VDS-backed source networks are
also IPv4-only. See
`build/yaml/samples/migration/controlplane-nsx-lb.yaml`.
The CRD validates syntax and family. The Evaluate handler must perform source
allocation, validate allocation ownership/readiness, and retain the selected
address on retries; cancellation cleanup requires handler implementation.
Before recording `status.lbSNATIPAllocations`, a handler must confirm the address
and, when it came from a source IPAddressAllocation, that allocation's ownership,
readiness, and Namespace/name. `allocationRef` is omitted for an administrator-
supplied backend address that has no source allocation. The accepted checkpoint
uses the existing `NamespacedName` API type when a source allocation exists and
prevents changes to `spec.networkConfiguration.lbSNATIPs`,
`spec.targetLBProvider`, and `spec.nsxProjectID`. The status allocation cannot be
replaced or removed. These records do not allocate addresses; post-execution
revision/reset handling remains separate service work. Infrastructure has no
rollback-based reset path.

The design explicitly marks post-execution input revisions/reset, reviewed plan
snapshots, resolved inventories, fabric provenance, rollback domains, cutover
protection, operation checkpoints and detailed commit/approval recovery contracts
as proposed extensions requiring finalized schemas and concrete handlers. LB
SNAT selection is an Evaluate responsibility; `lbSNATIPs` is optional for a
separate backend and is not requested through `status.requiredInputs`. This API alignment
does not implement those execution paths or remove the irreversible checkpoint
guards. The Namespace CRD now permits corrected inputs during Evaluate after a
stored successful rollback. Before recording that success, the concrete rollback
handler must verify restoration of the whole batch and invalidate the previous
evaluation results and any prepared target state. That handler work remains separate; the schema
exception does not perform rollback or verify external state.

Evaluate-to-Execute freshness is enforced by the transition-validation webhook
for both transition kinds. The old object's successful Evaluate checkpoint must
have matching `actionGeneration` and `observedGeneration` values equal to
`oldObject.metadata.generation`; a newer spec generation requires a fresh
Evaluate. The webhook admits only the action edges appropriate to each CR kind,
requires successful current Execute before Commit/Finalize, and permits Evaluate
after Namespace Rollback only for a current, successful `RolledBack` checkpoint.
CRD validation freezes all inputs once Execute is requested; the design-internal
plan/derive work remains inside that action. It is not active until the admission
server and its ValidatingWebhookConfiguration are deployed. No `evaluatedSpecHash`
field is added. The namespace lifecycle validator is wired separately below.

The webhook checks both transition kinds through a direct API reader and rejects
an observed active conflicting Execute. This best-effort check reduces conflicting
requests before they are stored; two simultaneous requests can still both pass the
list-before-write check. The 1.4.9 dispatch path must re-read live transition state
before execution as the race backstop. This is not a durable global lock.

The webhook requires a new transition to start with Evaluate. Unchanged Execute,
Rollback, Commit and Finalize requests remain retryable. Namespace Rollback may
be requested only for a current checkpointed Execute before the irreversible
Commit boundary. Inputs reopen only after successful whole-batch Rollback. The
transition webhook rejects deletion while an action is unresolved and performs a
best-effort cross-CR Execute check. Namespace lifecycle protection is described
below. Workload admission has a separate fail-closed baseline, while its controller
exceptions and atomic concurrency guarantee remain separate work.

### Deploying the transition-validation webhook

The admission server is opt-in via `--enable-migration-admission`, independent
of `--enable-network-migration` and leader election. It serves on port 9982
and reads `tls.crt`/`tls.key` from `/etc/nsx-operator/migration-webhook-certs`
by default; `--migration-admission-port` and `--migration-admission-cert-dir`
override these settings. The server is available on every configured Pod,
including non-leaders, and the readiness check waits for it to start.

The configuration also installs a `SupervisorNamespace` lifecycle validator for
`iaas.vmware.com/v1alpha1` and `v1alpha2`. During Stage 1, implicit and
explicit VDS creation is allowed, while an explicit VPC provider or
`spec.vpcNetworkConfig` is rejected because VPC is not ready yet.
After the control-plane transition reaches `InfraTransitionComplete` or
`Finalized`, creation is rejected when the object explicitly selects VDS or has
non-empty VDS network/portgroup fields. If provider selection is omitted, the
Supervisor Namespace API defaults the new namespace to VPC.

For namespaces named in an unresolved `NamespaceNetworkTransition`, create,
delete and user updates are rejected. The shared NCP Pod identity
`system:serviceaccount:<operator-namespace>:ncp-svc-account` may change only
`spec.vpcNetworkConfig`, and only while the matching Execute or Rollback
checkpoint is Running or Failed. Protection is derived from the transition's
namespace list, requested action and durable status; API read failures and
multiple active transitions fail closed. Protection ends only after a current,
successful Completed/Committed or whole-batch RolledBack checkpoint.

`build/yaml/migration/admission/` supplies a Service and a cert-manager Issuer
and Certificate. Its kustomization intentionally excludes both validating
webhook configurations, so applying it alone cannot install a fail-closed
webhook before a serving endpoint is ready. The separate webhook YAML files are
opt-in resources. The container
patch enables the admission server, exposes port 9982, and mounts the TLS Secret
at `/etc/nsx-operator/migration-webhook-certs`. Downstream packaging must use
the actual Pod labels and provide equivalent wiring and certificate/CA
injection if it does not use these manifests. When changing the port or
namespace, update the Service, certificate DNS names, webhook configuration,
and mounted Secret in concert.

Install the certificate and Service:

```sh
kubectl apply -k build/yaml/migration/admission
```

Then merge the container patch into the NCP Deployment. Wait for the Secret to
be ready and every Service-selected Pod to serve the admission endpoint. Only
then explicitly install `build/yaml/migration/admission/webhook.yaml` and
`build/yaml/migration/admission/namespace-webhook.yaml`. Both configurations
are fail-closed for their respective resources; status subresources are not
selected. Install this
admission before enabling concrete migration action handlers. Remove or
replace the webhook configuration before disabling its serving endpoint.

`build/yaml/migration/admission/workload-webhook.yaml` is a separate, opt-in
guard for Pods, VirtualMachines and LoadBalancer Services. Its Service entry
filters out non-LB Services before calling the webhook; Pod and VM requests
still call it. During Stage 1, a workload is in scope when its namespace is a
system namespace identified by the existing WCP T1 or shared-VPC namespace
markers and the `ControlPlaneNetworkTransition` has an unresolved Execute.
This guard does not inspect or change `SupervisorNamespace.spec.networking`;
the `vsphereNetworkConfig` to `vpcNetworkConfig` handoff belongs to WBS 1.4.6.
During Stage 2, a workload is in scope when its namespace is listed in an
unresolved `NamespaceNetworkTransition`. In either scope, users cannot create,
update or delete protected workloads. Updates are allowed only for configured
controller identities and only after the matching Execute checkpoint is
Running or Failed; RBAC governs resource and verb access. The webhook does not
maintain a controller-to-field diff matrix. The namespace lifecycle guard
retains its specific NCP-only ownership rule for `spec.vpcNetworkConfig`.

The fixed workload controller identities observed on TB1 are NCP
(`vmware-system-nsx:ncp-svc-account`), Net Operator
(`vmware-system-netop:vmware-system-netop-controller-manager`), VM Operator
(`vmware-system-vmop:vmware-system-vmop-vmoperator-service-account`) and AKO
(`vmware-system-ako:vmware-system-ako-controller-manager`). WBS 1.6.2 does not
require these identities to be configurable or checked during startup.
**TODO:** Confirm whether Spherelet's `system:node:*` identity needs main-resource
updates during migration and whether the current controller list matches the
required write paths. Keep identities fixed while the package identity contract
remains stable. The workload webhook configuration remains opt-in and must be
installed before enabling migration handlers. The Execute inventory barrier and
cutover handlers must reconcile requests admitted before protection took effect.
Status subresources are outside this webhook; downstream packaging must verify
their controller RBAC. The transition and namespace lifecycle webhooks above do
not depend on this guard.

VCFA Namespace onboarding and project selection belong to the VCFA client
workstream: every Namespace must be imported before confirmation/Commit, using
an explicitly selected project or a find-or-create project named after the
Namespace. These mappings are not additional transition CR inputs. VC-only
migration uses the same Namespace transition API without VCFA onboarding.

## WBS 1.4.9 action dispatch and restart reconciliation

Restart progress is reconstructed from existing Kubernetes resources and NSX
workflow status on every reconciliation. A lost transition-status write reruns the
same idempotent handler against observed state. There are no ConfigMap-backed
locks, operation journals, fencing sessions, completion snapshots, per-workload
step lists, or new `status.recovery` fields. The migration runtime does not add or
remove recovery finalizers and has no ConfigMap or finalizer-update RBAC.

For workloads, verify realized SubnetPorts, the NetworkInterface's confirmed actual
target backing, and applicable NetworkResourceTransition/reservation state. Reuse
completed work, wait for pending work and create only missing requests with stable
identities. Desired references or existing but unrealized ports do not prove cutover.
Keep source identities/backing available through existing child/workflow lifecycle
contracts until no longer needed. If required historical identity or rollback state
cannot be reconstructed, stop with the specific missing-contract diagnostic.

The transition admission webhook checks both CR kinds before accepting Execute,
but a list-before-write check cannot guarantee exclusivity for simultaneous
requests. The 1.4.9 dispatch path must re-read live transition state as the race
backstop. Failed or incomplete Namespace Execute/Rollback keeps namespace
lifecycle protection active; changing phase or action alone does not release it.

Admission webhooks validate requests before Kubernetes accepts them: the
transition and namespace lifecycle guards are wired for WBS 1.1.4. The workload
cutover handler covers Stage 1 system namespaces and Stage 2 namespace batches,
with release tied to current successful transition checkpoints. WBS 1.1.4.3
still requires the workload webhook configuration to be installed in the target
package and end-to-end validation of the controller identity contract, status
RBAC, and restart and partial-failure paths.
The in-flight inventory barrier belongs to the Execute handlers and cutover
integration.
The controller processes accepted requests without confirmation callbacks or a
second admission layer. It re-reads the live CR before dispatch to reject changed
UID/action/generation or deletion. This check prevents dispatching stale intent;
it does not implement webhook validation or guarantee atomic admission. Deploy the
separate webhooks before enabling concrete migration handlers. There is no second
ownership or protection record in ConfigMaps or transition status.

The existing leader-election Lease selects the active controller process. The
engine mutex serializes reconciliations inside that process; it is not a durable
cross-transition lock. Execute, Commit and Finalize use stable resource
identities, idempotent APIs and live ownership/dependency checks; this PR does not
claim persisted exclusion of every external mutation. Concrete handlers must recheck
these conditions immediately before writes. The controller also re-reads transition
UID/action/generation and deletion state before dispatch to avoid executing stale
intent. Admission guards must prevent deletion of unresolved active transitions;
the controller skips objects already marked for deletion and performs no implicit
external cleanup.

Existing `lastAction`, `actionState`, `actionGeneration`, workflow/SNAT references
and engine-owned `irreversibleAction` retain their dedicated purposes. Commit and
Finalize still require the irreversible marker to be stored before destructive work.
Concrete handlers verify actual outcomes before returning Succeeded. Tests model
observed SubnetPort readiness and failed transition-status publication to demonstrate
restart without duplicate creation; full NetworkInterface/workload cutover remains
concrete handler work. Tests verify changed intent and deletion prevent dispatch;
existing dispatcher tests exercise accepted actions without admission callbacks.

The branch is based on main commit `35df6c93`, which includes framework PR #1613.
Resource naming, optional-input diagnostics, generated schemas and examples were
introduced by that framework change. This branch adds one WBS 1.4.9 orchestration
commit above main and does not change migration APIs or CRDs.

Concrete external handlers remain unregistered. End-to-end WBS 1.4.9 acceptance
requires the separately implemented admission webhook/guards and concrete services.

## Build and validate

```sh
make generate-migration manifests-migration
make test-migration
make build-migration
make docker-build-migration MIGRATION_IMG=supervisor-network-migration-operator:latest
```

Use the repository Go toolchain and Kubernetes/controller-runtime versions. CRD
admission is validated with Kubernetes 1.33, the minimum supported API-server
version, and its standard CEL compatibility settings. The API module has its
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
   new container. The TB1 NCP Deployment uses `ncp-svc-account`; the migration
   container shares that Pod identity. Match both bindings to the ServiceAccount
   configured on the downstream NCP Deployment.
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

The WBS 1.4.9 runtime supplies action dispatch, checkpoints and dependency gates,
and processes requests validated by separately implemented admission webhooks. It
stores no ConfigMap coordination or recovery records. Concrete handlers need live
NSX ownership and consumer checks, reviewed plan verification, approval/onboarding
checks and idempotent reconciliation. Admission webhooks remain separate WBS 1.1.4
work; concrete handlers repeat dependency and live-state checks before each external
mutation. Reconciliation recovers progress from existing Kubernetes resources and
NSX workflow state. UBMWM-12405 owns persisted execution checkpoints and
commit/finalize guards.
Never duplicate normal NSX controllers or delete active VPC/LB/SNAT resources.
`NetworkResourceTransition` support is a prerequisite for real cutover handlers;
it is not implemented by this scaffold. Container co-location does not replace
these recovery contracts or protect against Pod replacement.

The two orchestration CRDs use `migration.network.vmware.com/v1alpha1`; resource transfer
uses the separate `NetworkResourceTransition` API and remains an NSX Operator
responsibility. The leader-election
lease keeps its historical name across the API group change.

## Non-goals

The 9.2 migration scope supports Foundation Load Balancer (FLB,
`VSPHERE_FOUNDATION`) sources to NSX LB only. HAProxy and Avi sources and
targets are unsupported and must fail Evaluate before target resource
preparation. FLB frontend networks and VIP ranges are discovered automatically;
the current transition CRDs expose no frontend-DVPG input. Unsupported
discovered providers are reported through failure conditions. Frontend-DVPG
input would be needed if HAProxy or Avi migration is supported in a future
release.

The following source topologies are also outside the migration scope:

- Multiple VDS networks using the same VLAN but different gateways are
  unsupported because NSX supports one CIDR and gateway per VLAN extension
  Subnet. This is considered a rare deployment case.
- VDS networks with private VLAN type are not eligible for migration.
- A Supervisor using VDS networks with SR-IOV cannot be migrated because NSX
  VPC does not support SR-IOV.

## WBS 1.4.1: NSX workflow SDK client

The migration service lazily calls `nsx.NewMigrationClient`, which registers the
SDK `WorkflowsClient` and `Dvpg_validation_resultsClient` in `pkg/nsx/client.go`.
Ordinary `nsx.NewClient` callers leave workflow clients unset. Disabled migration
does not invoke migration handlers or initialize their NSX clients.

Migration requests and responses use SDK models,
including `DvpgToSubnetWorkflow` and `DvpgToSubnetConfig`, with SDK binding
conversion. The NSXT module is pinned to dev commit `125769460d42`.

NSX validates workflow IDs and request configuration. The migration wrapper adds
response identity checks, validation-result pagination, and polling that checks the requested desired state, nested workflow status and
resource completion in the expected VPC scope. Auto-commit stays disabled so the
operator retains its Commit/Finalize boundary. SDK errors remain available to
callers. Context cancellation stops polling; in-flight SDK requests use the
shared NSX transport timeout. No separate JSON transport or workflow models are
maintained by the operator.
