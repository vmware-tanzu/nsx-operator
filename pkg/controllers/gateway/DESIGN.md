# Gateway DNS controller — design & function specification

This document describes the responsibilities, contracts, and upstream relationship of `pkg/controllers/gateway`. It is intended for design review and onboarding; it does not replace Godoc on individual symbols.

## 1. Purpose

The gateway package implements Kubernetes controllers that:

- Keep a **Gateway IP cache** (`gatewayIPCache`) keyed by Gateway namespaced name (IPs + admitted hostname filters).
- Reconcile **annotation-based direct DNS** for **`Gateway` only** into the shared **`dns.DNSRecordStore`**. **`ListenerSet`** is **not** a source of direct DNS rows: its **`spec.listeners`** (hostname fields) participate **only** in **Route hostname admission** together with the parent Gateway’s listeners (see admission helpers and `refreshGatewayIPCache`).
- Reconcile **Route DNS** (`HTTPRoute`, `GRPCRoute`, `TLSRoute`) into the same store using **owner-scoped** or **namespace-aggregated** batches (see `pkg/nsx/services/dns`).
- Run **garbage collection** against the store when Kubernetes owners disappear or Gateway API resources are not installed.
- Maintain optional **Route status conditions** (`DNSConfig`) on **Gateway** and **ListenerSet** parent refs when those parents appear in **`ipCache`** (Gateway shard entry or ListenerSet key in **`lsToRootGw`**).

NSX API calls for publishing DNS records are outside this package; the store holds desired state consumed by other layers.

## 2. Core types

### `GatewayReconciler`

Central dependency bag: Kubernetes client, `*dns.DNSRecordService`, status metrics, **`gatewayAPIResources`** (discovery snapshot), **`gatewayIPCache`**, resync channels for Route DNS, and **`ipCacheWarmedOnStartup`** (atomic).

### `gatewayAPIResources`

Boolean flags indicating which `gateway.networking.k8s.io/v1` kinds exist on the cluster. Used to avoid LIST calls for missing CRDs and to gate controller registration and GC behavior.

### `routeReconcilerAdapter`

Generic adapter implementing `reconcile.Reconciler` for each Route kind. Delegates to **`reconcileRouteDNS`** with a fresh wrapper instance per reconcile.

## 3. Lifecycle

### `StartController`

1. Optional webhook hook (caller-supplied).
2. **`checkGatewayCRDs`** → fills `apiResources`.
3. If Gateway CRD is absent → return success without registering controllers.
4. **`registerGatewayDNSFieldIndexes`** on the manager’s cache.
5. **`gatewaySetupWithManager`** (default **`setupWithManager`**) → registers Gateway controller + Route DNS controllers.
6. Registers **`warmGatewayIPCacheOnStartup`** as a manager runnable.

### `setupWithManager`

Registers the primary Gateway `Reconcile`, optional `ListenerSet` watches, then **`registerRouteDNSControllers`** (HTTP/GRPC/TLS DNS controllers gated by `apiResources`). Requires a full controller-runtime **`Manager`** with a working cache; unit tests typically do not execute this path without envtest-style setup.

### `warmGatewayIPCacheOnStartup`

Lists Gateways, refreshes ipCache for each processable Gateway (including ListenerSets when enabled), sets **`ipCacheWarmedOnStartup`**, then enqueues Route DNS resyncs. Route reconcilers **must not** treat an empty cache as “unmanaged” until this completes (they requeue with a short delay).

## 4. Gateway reconcile (`Reconcile`)

**Contract (summary):**

- Increments sync metrics.
- On Gateway delete / not found: delete DNS rows tied to that Gateway index, clear ipCache, optionally resync Routes.
- On live Gateway: skip if not managed (class / ignore annotation / no usable IP); list ListenerSets (for **admission rows** in **ipCache**, not for ListenerSet annotation DNS); **refreshGatewayIPCache**; build **Gateway-only** direct DNS batches; **`CreateOrUpdateDNSRecords`**; update DNS-ready status; enqueue Route resync when cache content changes materially.

## 5. Route reconcile (`reconcileRouteDNS`)

**Contract (summary):**

| Event | Behavior |
|-------|----------|
| Route **NotFound** | Delete store rows owned by that Route namespaced name for this kind. |
| **ipCache not warmed** | Requeue after `ipCacheStartupWarmRequeueAfter`. |
| Success path | Resolve namespace type → build merged endpoints → **`CreateOrUpdateDNSRecords`** (owner-scoped batch) → patch Route status (`DNSConfig`) on matching parent refs. |

**`mergeRouteParentDNSCondition`**: For each entry in `status.parents`, if the parent ref is a handled Gateway API **Gateway** or **ListenerSet** (`extdnssrc` parse rules), set `DNSConfig` when **`routeParentManagedInIPCache`** reports **managed** (Gateway: **`ipCache.get`**; ListenerSet: **`listenerSetInLSToRootIndex`** / **`lsToRootGw`**); otherwise remove `DNSConfig`. Unsupported parent kinds are left unchanged.

## 6. Parent resolution (`route_parent_resolve.go`)

Parent references use semantics aligned with **`pkg/third_party/externaldns/source`** (Gateway vs ListenerSet resolution, default namespace rules).

| Function | Responsibility |
|----------|------------------|
| **`resolveParentRefToRootGatewayNN`** | Single-ref → root Gateway NN: direct Gateway parent returns that NN; ListenerSet parent uses **`ipCache.rootGatewayForCachedListenerSet`** ( **`lsToRootGw`** index only; no apiserver GET). Returns `(nn, ok)`. |
| **`routeParentManagedInIPCache`** | For Route status parent refs: **`(managed, supportedKind)`** — supported when ref parses as Gateway or ListenerSet; managed when that parent is represented in **`ipCache`** (Gateway **`get`**, ListenerSet **`listenerSetInLSToRootIndex`**). |

**Tests:** `TestResolveRouteRootGatewayNNs_table` exercises **`resolveParentRefToRootGatewayNN`** across multiple parent refs (dedupe loop inlined in the test).

**Removed dead code:** `routeHasAcceptedAttachmentToRootGateway` was deleted; nothing called it. Accepted-parent filtering for DNS building uses **`distinctAcceptedRootGatewayNNs`** (in `route_subreconciler.go`) and **`extdnssrc.RouteAcceptedForParentRef`**.

**ipCache admission:** `gatewayDNSCacheEntry` carries **`AdmissionRows`** (per-listener hostname filters + Gateway vs ListenerSet identity + section name) from **`refreshGatewayIPCache`**, plus an index **ListenerSet NN → root Gateway NN** (**`lsToRootGw`**), maintained under **`lsRootMu`**. **`rootGatewayForCachedListenerSet`** / **`listenerSetInLSToRootIndex`** read that index. Route DNS inference uses **`AdmissionHostnameFiltersForRouteParentFromRows`** on **`AdmissionRows`** so the hot path avoids loading parent objects for admission only.

## 6b. Route DNS aggregation helpers (`route_subreconciler.go`)

Owner-scoped and aggregated Route DNS both rely on **`buildRouteDNSEndpointsForAggregation`** (same hostname/admission rules as namespace aggregation). The flow is split for clarity:

| Function | Role |
|----------|------|
| **`inferRouteDNSHostnamesFromAcceptedParents`** | When the Route has **no** spec or external-dns hostname annotation entries, infers DNS names from **Accepted** parent refs whose root Gateway has **ipCache** IPs, using **`AdmissionHostnameFiltersForRouteParentFromRows`** and **`RouteHostnamesMatchingAdmission`** with a placeholder empty route host (ExternalDNS-style). |
| **`appendAggregatedRouteDNSEndpointForHostname`** | For one deduplicated hostname: walk Accepted parents, **`resolveParentRefToRootGatewayNN`**, **`ipCache`**, **`AdmissionHostnameFiltersForRouteParentFromRows`**, **`BestMatchingAdmissionFilter`**, then either merge targets across root Gateways (when **`distinctAcceptedRootGatewayNNs`** ≥ 2 and filters align) or pick the most specific parent; calls **`buildEndpoints`** with parent Gateway labels (zone path is attached later by **`ValidateEndpointsByDNSZone`**). |
| **`buildRouteDNSEndpointsForAggregation`** | Orchestrates **`RouteHostnames`** → inference → per-host **`appendAggregatedRouteDNSEndpointForHostname`**, then **`ValidateEndpointsByDNSZone`** to produce **`EndpointRow`** values with resolved zone paths. Takes **`common.NameSpaceType`** for API symmetry with **`buildRouteDNSMergedEndpoints`** (reserved for future namespace-type–specific DNS rules). |

## 7. Garbage collection (`gc.go`)

- **`CollectGarbage`**: Requires Gateway API installed; lists live Gateways; runs **`gcOwnerMissingDNSRecords`**; removes stale Gateway-indexed store entries when the Gateway CR no longer exists.
- **`gcListExistingOwners`**: For each enabled Route/ListenerSet kind, lists API objects; for **disabled** kinds, returns an **empty set** so GC deletes all store rows for that kind (upgrade path when CRDs are removed).

## 8. Handlers & indexing

- **ListenerSet → Gateway** map function resolves parent Gateway, loads Gateway, enqueues reconcile only if **`shouldProcessGateway`**.
- Field indexes: **`routeParentGatewayIndex`**, **`routeParentListenerSetIndex`**, **`listenerSetParentGatewayIndex`** — used for LIST-by-field and resync enqueue paths.

## 9. Comparison with upstream external-dns (Gateway source)

The operator **does not** embed the full `external-dns/source/gateway.go` resolver. It vendors a **subset** under `pkg/third_party/externaldns` and implements Gateway/Route orchestration in this package.

| Topic | upstream external-dns | nsx-operator |
|-------|------------------------|----------------|
| Deployment | Standalone controller → DNS providers | Operator → **`DNSRecordStore`** → NSX (elsewhere) |
| ParentRef matching | **`gwRouteHasParentRef`**: group/kind/name + namespace defaults; **does not** compare SectionName/Port | **`ParentReferencesSemanticallyEqual`** / **`RouteAcceptedForParentRef`**: compares **SectionName** and **Port** where applicable |
| Acceptance | `gwRouteIsAccepted` on route parent status | **`RouteAcceptedForParentRef`** (same semantic family) |
| Multi-root / merge | Resolver-internal | **`distinctAcceptedRootGatewayNNs`** drives merge behavior |
| Cold start | Informer cache | Explicit **`warmGatewayIPCacheOnStartup`** gate |

When porting upstream fixes, compare **`gwRouteHasParentRef`** vs **`ParentReferencesSemanticallyEqual`** deliberately: stricter Section/Port behavior may be intentional for Gateway API attachment.

## 10. Invariants

1. Route DNS must not delete or strip records based on an **empty ipCache** before warm completes.
2. Owner-scoped Route batches prune by **owner kind + namespace + name** in the store (see dns package).
3. Gateway delete removes **Gateway-owned** direct DNS rows via index (and any legacy rows still indexed the same way); **Route-owned** rows remain unless Route reconcile deletes them. **ListenerSet** does not create new direct DNS from annotations; listener data is admission-only for Routes.

## 11. Testing & coverage

Unit tests live in **`gateway_test.go`**, **`gateway_dns_scenarios_test.go`** (suite/table DNS scenarios), and **`gateway_helpers_test.go`** (shared factories, gomock **`createFakeManagerAndClient`** / **`newGatewayGomockManager`**, **`StatusUpdater`** mocks, DNS store assertion helpers). Statement coverage for this package is approximately **79–80%**; **`setupWithManager`** / full **`registerRouteDNSControllers`** typically require a real **Manager + Cache** (e.g. envtest) for high coverage.

**Gateway test manager** (tests): **`pkg/mock/controller-runtime/manager.MockManager`** (mockgen) is wired by **`createFakeManagerAndClient`** (in **`gateway_helpers_test.go`**) with permissive gomock expectations; **`GetCache()`** returns **`pkg/mock/controller-runtime/cache.DelegatingCache`** (client-backed reads + no-op informers) so `builder.Complete` can register Route DNS controllers; **`GetControllerOptions().SkipNameValidation`** is **true** so controller-runtime’s process-wide controller name set does not fail sequential table tests. **`TestRegisterRouteDNSControllers_table`** exercises **`registerRouteDNSControllers`** (resync channel allocation per Route CRD flag) against this setup.

---

*Copyright © 2026 Broadcom, Inc. All Rights Reserved.*
*SPDX-License-Identifier: Apache-2.0*
