# Gateway controller — DNS behavior (Gateway API)

This document describes **how the nsx-operator Gateway controller derives DNS records** from Kubernetes Gateway API objects. It is the contract operators and users should rely on, not a list of unit tests.

The implementation is intentionally aligned with **[ExternalDNS](https://github.com/kubernetes-sigs/external-dns)** Gateway semantics: hostname resolution, listener admission, and per-target record typing reuse logic vendored under `pkg/third_party/externaldns` (with attribution in package `doc.go` files there). Where this doc says “same as ExternalDNS,” it refers to that mirrored behavior.

---

## Two DNS ownership models

| Model | Kubernetes owners | Typical FQDN source |
|--------|-------------------|---------------------|
| **Route DNS** | `HTTPRoute`, `GRPCRoute`, `TLSRoute` | Route **spec hostnames** plus **annotations** (see below); filtered by parent **Gateway / ListenerSet admission** (ListenerSet **listeners** supply hostname allow-lists only—they do **not** create direct DNS rows). |
| **Direct DNS** | **`Gateway` only** | **`nsx.vmware.com/hostname`** on the Gateway (no Route involved). **`ListenerSet`** is **not** supported for annotation-based direct DNS. |

Both models publish **A** / **AAAA** records toward **Gateway `status.addresses`** (IP-type addresses). Targets are grouped by IP family using **`endpoint.SuitableType`** (IPv4 → A, IPv6 → AAAA), matching ExternalDNS endpoint helpers.

---

## Annotations (FQDN and policy)

Defined in `pkg/nsx/services/common` (keys are stable API for users):

| Annotation | Meaning |
|------------|---------|
| **`nsx.vmware.com/hostname`** | Explicit hostname list for **Routes** and **Gateways** (Route DNS vs **Gateway** direct DNS). Comma-separated FQDNs after parsing. **Not** used on **`ListenerSet`** for direct DNS—ListenerSet listeners participate only in **admission** (see below). |
| **`nsx.vmware.com/gateway-hostname-source`** | How Route **spec hostnames** combine with the hostname annotation: **`annotation-only`** (ignore spec for DNS), **`defined-hosts-only`** (spec only), or default merge (annotation + spec; see `RouteHostnames` in third_party `source`). |
| **`nsx.vmware.com/gateway-ignore`** | When **`true`**, the Gateway’s **direct DNS** path is skipped (no Gateway annotation DNS rows under that Gateway). **`false`** explicitly allows processing. |

Resolution of Route hostnames uses **`extdnssrc.RouteHostnames`** (same layering as ExternalDNS `gateway.go` hosts / gateway-route resolver).

---

## Admission: which route hostnames become DNS names

Admission is **not** the Kubernetes API “Accepted” condition alone for hostname picking; it is an **allow-list** derived from listeners:

1. **`CollectAdmissionHostnameFilters(Gateway, []ListenerSet)`** gathers hostname filters from **`Gateway.spec.listeners[].hostname`** and each **`ListenerSet.spec.listeners[].hostname`**, in order (Gateway listeners first, then ListenerSets). A **nil** listener hostname means “match any” (represented as `""` in the filter list), consistent with ExternalDNS listener semantics.
2. **`RouteHostnamesMatchingAdmission(admittedHosts, routeMeta, desiredHostnames, gateway-hostname-source key, hostname key)`** maps each candidate hostname from the route through **`GwMatchingHost`** against those filters (wildcard / exact matching as in ExternalDNS). Wildcard DNS names in the result may be **dropped** unless **`nsx.vmware.com/gateway-hostname-source`** is **`annotation-only`** and **`nsx.vmware.com/hostname`** lists at least one non-empty hostname (same rule as **`RouteHostnameWildcardAllowed`**).

So: users configure **which hosts are in scope** via **listener hostnames** (Gateway and ListenerSet) and optionally **ListenerSet-only** admission when the Gateway listener is open.

The Route must also reference a parent Gateway that passes controller **managed GatewayClass** checks and **parent readiness** (`IsParentReady`) where applicable; otherwise no Route DNS is emitted for that attachment.

### Parent `Accepted` and DNS targets / deletes

For each parent Gateway, **`IsParentReady`** requires **`RouteConditionAccepted=True`** on the matching `status.parents` entry (see `HTTPRouteParentReadyForGateway`, `GRPCRouteParentReadyForGateway`, `TLSRouteParentReadyForGateway` in `pkg/third_party/externaldns/source/httproute_status.go`, `grpcroute_status.go`, `tlsroute_status.go`).

- **Several Gateways as parents**: Batches are built **per parent** and **merged** by FQDN and record type. If one parent’s **Accepted** becomes **False**, that parent contributes **no** batch, so the **merged targets no longer include that Gateway’s IP** for shared hostnames. Remaining parents’ IPs stay.
- **Single parent** and **Accepted** becomes **False**: No batch is produced → **`reconcileRouteDNS`** deletes all DNS rows **owned by that Route** (`DeleteDNSRecordByOwnerNN`).

Reconcile runs when Route **status** changes (informer); until the next successful reconcile, stored DNS may still reflect the previous generation.

### Wildcard DNS names (`*.example.com`)

| Path | Behavior |
|------|----------|
| **All paths** (Gateway annotation DNS, Route DNS, LoadBalancer Service DNS) | **`pkg/nsx/services/dns.ValidateEndpointsByDNSZone`** **does not create** NSX DNS rows for endpoint FQDNs whose DNS name starts with **`*.`** (after trim). Those endpoints are **skipped** (logged); concrete hostnames in the same batch are still published. Listener / admission filters may still use **`*.example.com`** patterns; only the **literal wildcard apex** as a record owner name is rejected. |
| **Route DNS** (HTTPRoute / GRPCRoute / TLSRoute) | **`RouteHostnamesMatchingAdmission`** may still produce **`*.`** names when ExternalDNS-style rules allow; they are **not** written as DNS records. **`appendAggregatedRouteDNSEndpointForHostname`** may also omit wildcards when **`allowWild`** is false. |

---

## Targets (IPs)

- IPs come from **`Gateway.status.addresses`** entries whose **type is IP** (or unset, treated as IP). Non-IP address types are ignored for DNS.
- **Multi-parent routes** (several Gateways): DNS targets for the same FQDN are **merged** (union of IPs per record type). **`mergedRouteDNSEndpointsInOrder`** sorts endpoints deterministically by FQDN and record type before writes.

---

## Conflicts and precedence

### Direct DNS (Gateway annotation batches only)

Only **Gateway** objects contribute **direct DNS** batches from **`nsx.vmware.com/hostname`**. **`dedupeGatewayDirectDNSBatches`** orders batches and drops later endpoints whose names overlap earlier ones (**`ClaimGwMatchingDNSName`** / **`GwMatchingHost`**), so duplicate or wildcard-overlapping names inside the Gateway’s planned rows are stable.

Implementation: **`dedupeGatewayDirectDNSBatches`** in the gateway package; per-endpoint overlap uses **`ClaimGwMatchingDNSName`** in **`pkg/third_party/externaldns/source/gateway_host_matching.go`**. Those helpers build on **`GwMatchingHost`**; they are **not** copied from a single upstream symbol—ExternalDNS does not export an equivalent “ordered batch” dedupe, but matching rules match upstream `GwMatchingHost` semantics.

### Route vs Route

Different routes own different DNS owner keys; collision handling is at the **DNS store / NSX** layer if two owners target the same FQDN (not special-cased in the Gateway controller beyond merge within one route’s multi-parent batch).

---

## Managed Gateways and reconciliation

- Only **supported GatewayClasses** (see `filteredGatewayClasses` / `shouldProcessGateway`) participate in DNS and **ipCache** handoff to Route reconcilers.
- If a Gateway becomes **unmanaged** or **loses addresses**, direct DNS under that Gateway is removed and **ipCache** is cleared so Route DNS does not keep stale targets.

---

## Relationship to ExternalDNS

| Concern | In nsx-operator |
|---------|-------------------|
| Hostname list from spec + annotations | `pkg/third_party/externaldns/source` — `RouteHostnames`, `RouteHostnamesMatchingAdmission`, `CollectAdmissionHostnameFilters` |
| Wildcard / overlap matching | `GwMatchingHost` (same naming as ExternalDNS gateway source) |
| Ordered Gateway direct-DNS hostname dedupe | `ClaimGwMatchingDNSName` (`gateway_host_matching.go`; nsx-operator extension on top of `GwMatchingHost`) |
| Endpoint construction per FQDN and IP family | `pkg/third_party/externaldns/endpoint` — `EndpointsForHostname`, `SuitableType` |
| NSX / operator-specific | `dns.OwnerEndpoints`, stable record IDs, Gateway index GC, status conditions (`DNSConfig`) |

Behavioral differences are limited to **ownership** (NSX DNS rows keyed by Route/Gateway UIDs), **Supervisor namespace** flags (`ForSVService`), and **requeue / cache** orchestration—not to core hostname or admission math.
