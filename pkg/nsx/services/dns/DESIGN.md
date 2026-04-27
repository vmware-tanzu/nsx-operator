# DNS desired-state service (`pkg/nsx/services/dns`)

Short design reference for the DNS layer between Kubernetes controllers (especially Gateway / Route) and NSX DNS policy. This package owns **validation**, **stable record identity**, **in-memory store indexing**, and **batch apply** semantics—not the NSX RPC client lifecycle (except future hydration).

## 1. Responsibilities

| Component | Role |
|-----------|------|
| **`DNSRecordService`** | Entry point: `CreateOrUpdateDNSRecords`, deletes by owner/Gateway, namespace zone resolution (`ZonePathForHostname`). |
| **`DNSRecordStore`** | Thread-safe store (`common.ResourceStore`) with multiple indexes for GC, Gateway-scoped deletes, IP lookup, and conflict detection. |
| **`AggregatedDNSEndponts`** (and related types) | Unified reconcile input: namespace-aggregated Route rows **or** **owner-scoped** **Gateway** / **Route** batches. **ListenerSet** is **not** configured for direct DNS via annotations; ListenerSet **listeners** exist in the Gateway controller only to drive **Route hostname admission** (not as a DNS row owner from `nsx.vmware.com/hostname` on the ListenerSet). |
| **`zones.go`** | Resolve hostname → NSX DNS zone policy path (permitted VPC DNS zones + allowed domains). Zone choice uses **`third_party/externaldns/provider.ZoneIDName.FindZone`**, mirroring **`sigs.k8s.io/external-dns/provider`** longest-suffix match and per-label IDNA normalization; **`dnsNameForZoneMatch`** only trims / strips a leading `*.` before **`FindZone`** when matching a hostname to a zone (not for deciding whether to publish a row). **`ValidateEndpointsByDNSZone`** **skips** endpoints whose DNS name is a wildcard apex (`*.…` after trim); those rows are never created in NSX. Apex hostnames equal to the delegated zone remain rejected. |

Controllers call into this package; NSX reconciliation of `DNSRecord` objects is expected to consume the store elsewhere (see **Open work**).

## 2. Reconcile pipeline

1. **`CreateOrUpdateDNSRecords`**: No-op if `batch == nil` or store nil.
2. **`validateDNSUpsertBatch`**: Owner-scoped rows must align with `ScopeOwner`; rows must carry primary UID, endpoint, record type, **DNS zone path on each `EndpointRow`**; detects conflicting owners for the same **(zone path, FQDN, type)** via **`ensureZoneFQDNOwnership`**.
3. **`applyDNSUpsertRows`**: Builds/updates `DNSRecord` structs from `DNSEndpointOwnerRow`; computes **desired record IDs**; marks **missing** previously-owned rows `MarkedForDelete` (owner-scoped prune by **`GetByOwnerResourceNamespacedName`**; primary key from **`dns_for` + `dns_owner_namespace` + `dns_owner_name`** tags).
4. **`DNSRecordStore.Apply`** applies the mutation list.

## 3. Stable IDs & labels

- ExternalDNS **`Endpoint`** label **`EndpointLabelParentGateway`** anchors indexing and GC; zone path lives on **`EndpointRow`** after validation.
- **`stableDNSRecordIDFromDNSEndpointOwnerRow`** prefers **zone path** when present (`recordservice_endpoints.go`), else legacy parent-gateway label material.

## 4. Store indexes (selected)

Used for queries without full scans:

- **`ownerNamespacedName`**: Owner-scoped GC and deletes (`<dns_for>/<ns>/<name>` key).
- **`gatewayNamespacedName`**: Gateway-scoped index for rows carrying parent-Gateway labels (Route + Gateway direct); orphan pruning uses **`DeleteOrphanedDNSRecordsInGateway`**.
- **`namespaceRouteAggregateDNS`**: Namespace-scoped Route aggregation prune.
- **`dnsZonePathFqdn`**: Same-FQDN conflict detection across owners.
- **`dnsTargetIP`**: Reverse lookup by IP (e.g. Gateway IP diagnostics).

Full index registration: **`BuildDNSRecordStore`** in `store.go`.

## 5. Delete semantics (important)

- **`DeleteDNSRecordByOwnerNN`**: Removes or re-tags store rows for **`kind/namespace/name`** ( **`deleteOrUpdateDNSRecordsByOwnerNN`**). Gateway delete does not remove Route-owned rows; GC and Route not-found both use the same call.

### ListenerSet: tags vs Kubernetes annotations (this package)

- **NSX / store tags**: **`tagsForOwner`** only writes **`dns_for`** values from **`resourceKindToCreatedFor`** (Gateway, Route kinds, Service). **`CreateOrUpdateDNSRecords`** rejects **`Owner.kind == ListenerSet`** with rows. Nothing here sets annotations on **ListenerSet** objects.
- **Kubernetes annotations**: This package does **not** read **Gateway API ListenerSet** `metadata.annotations` for DNS. Namespace allow-domains use **`ParseAllowedDNSZonesJSON`** / **`AnnotationNamespaceAllowedDNSZones`** only (`zones.go`).

## 6. Interface for controllers

**`RouteDNSWrite`** (`types.go`): `CreateOrUpdateDNSRecords`, `DeleteDNSRecordByOwnerNN`, `ValidateEndpointsByDNSZone` — allows tests to inject mocks.

## 7. Open work

- **`InitializeDNSRecordService`**: Store is **not** yet loaded from NSX after restart (`TODO [VCFN-2809]`); current correctness relies on controllers repopulating desired state from Kubernetes.
- **`store.go`**: Comment to align `dnsRecordKeyFunc` with final NSX DNS record model when API stabilizes.

## 8. Testing

Table-driven tests in `*_test.go` cover validation, prune behavior, zone resolution, and stable ID helpers. Extend tests when changing **delete** or **index** contracts.

---

*Copyright © 2026 Broadcom, Inc. All Rights Reserved.*  
*SPDX-License-Identifier: Apache-2.0*
