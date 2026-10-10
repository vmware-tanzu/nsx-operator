# Pod v2 e2e on a legacy Pod testbed

This branch defaults to **only `TestPodV2`**. Other e2e cases and their bulk
namespace setup are bypassed. The operator image must already contain commits 1
and 2. The runner does not build, replace or deploy the operator image.

The suite validates the operator's Kubernetes/NSX behavior on a legacy Pod
platform. Only TB01 checks workload connectivity. CR Ready does not establish
that full Pod v2 guest networking works. Restore is simulated by removing a
specific test-owned NSX port while the operator is stopped; no backup rollback
is performed.

## Before running

Use an idle operator instance with no unrelated Pod workloads in VPC namespaces
and no existing SubnetPort CRs. Feature switches and forced restore affect the
whole operator instance. Do not run another e2e suite, deploy an operator update,
or create other workloads concurrently.

Supply these environment-specific inputs as flags (no values are embedded in
the source):

- `-remote.kubeconfig`: kubeconfig for the test cluster.
- `-operator-cfg-path`: **local** operator configuration usable by the existing
  e2e NSX client, including locally accessible certificates/credentials.
- `-podv2-operator-namespace` and `-podv2-operator-deployment`: the Deployment
  running the operator. An HPA targeting it is unsupported.
- `-podv2-operator-container`: required if the Deployment has several containers.
- `-podv2-config-path`: the configuration file **inside** that container; defaults
  to `/etc/nsx-ujo/ncp.ini`. It must be the file actually used by `-nsxconfig`.
- `-podv2-namespace`: an existing empty, provisioned VPC namespace, with a default
  Pod SubnetSet carrying `nsx.vmware.com/default-network=pod` and a populated IP
  family. Its VPC must allow a private Subnet. The suite temporarily selects its
  own static SubnetSet, then restores the original default label.
- `-podv2-dhcp-namespace`: a **second** empty VPC namespace, whose default Pod
  SubnetSet already references pre-created DHCP Subnets through `spec.subnetNames`.
  Those Subnets must be allowed for the default Pod network by the namespace's
  VPCNetworkConfiguration, with static allocation disabled and DHCP enabled for
  the tested IP families. TB03 uses these existing parents without changing them.
  This flag is required for the full suite, but not when selecting other cases.
- `-podv2-image`: image providing `sh`, `httpd`, `wget`, `nslookup`, and `sleep`;
  default `busybox:1.36`. Use an image that this testbed can pull.

The kubeconfig needs cluster-wide read access for the isolation checks, access to
Pod exec/logs, permission to update the operator Deployment and temporary Secret,
and CRUD access for the test resources and NCPConfig. Default SubnetSet label
updates use **operator service-account impersonation** because the repository's
webhook explicitly restricts those updates. A dry-run checks this permission
before changing the testbed. NSX credentials need query/get/delete access to
ports; deletion is guarded by the test namespace UID and recorded Pod UID.

NSX feature support is checked for the enhancement and `restore_vif=true`
variants. Unsupported variants are reported as SKIP, not as successful coverage.
IP-family coverage follows the namespace's configured family; use separate
provisioned environments for additional families.

## Full run

From the repository root, with the Go version specified in `go.mod`:

```bash
./hack/test-podv2-e2e.sh \
  -remote.kubeconfig /path/to/kubeconfig \
  -operator-cfg-path /path/to/local/ncp.ini \
  -podv2-operator-namespace '<operator-namespace>' \
  -podv2-operator-deployment '<operator-deployment>' \
  -podv2-operator-container '<operator-container>' \
  -podv2-namespace '<empty-vpc-namespace>' \
  -podv2-dhcp-namespace '<empty-precreated-dhcp-namespace>' \
  -podv2-image '<available-test-image>'
```

The wrapper compiles first, then runs the test binary. It disables Go's hard
process timeout because that panic bypasses test cleanup. Work has a cancellable
90-minute budget (`-podv2-budget`); each wait defaults to 5 minutes
(`-podv2-step-timeout`). Cleanup has a separate 15-minute budget
(`-podv2-cleanup-timeout`), with the last third reserved for operator restoration.
These are configurable deadlines, not predicted execution times.

SIGINT/SIGTERM cancels test work and allows rollback to finish. The wrapper waits
for that rollback instead of immediately exiting. Do not force-kill it while
cleanup is running.

## Select a case

Add the test-binary flag to the same invocation:

```bash
-test.run '^TestPodV2$/TB08_RestoreExistingCR/restore_vif_false$'
```

For the ordinary `go test` entry point, the equivalent filter is `-run` rather
than `-test.run`; use `e2e=true`, `-timeout=0`, and the same connection flags.
The branch rejects filters outside `^TestPodV2$` while `-podv2-only=true`.
To explicitly restore the original repository suite, use `-podv2-only=false`.

| Case | What is checked |
| --- | --- |
| TB01_Legacy | Legacy IP/MAC annotations, no Pod CR, peer HTTP, DNS, Service, deletion |
| TB02_StaticLifecycle | One Pod-owned CR/port, default static SubnetSet, IP family, allocation, identity, host context, deletion |
| TB03_PrecreatedDHCP | Default pre-created Subnet selection, actual DHCP/static settings, restart identity, deletion |
| TB04_Labels | Add/change/remove labels, namespace tag uniqueness, stable CR/port |
| TB05_HostRetry | Node fallback, explicit host, bad host Ready=False without empty context, recovery |
| TB06_StatefulSet | Enhancement on/off, ordinal replacement, reuse/identity tags, scale-down and final cleanup; Parallel Pod management |
| TB07_RestartAndReplacement | Label churn, two restarts, one CR/port per UID, same-name replacement |
| TB08_RestoreExistingCR | Missing NSX port with original CR/status retained, both restore_vif settings, persisted status and normal reconciliation |
| TB09_RestoreLegacyMissingCR | Realized legacy Pod with missing NSX port and no CR, both restore_vif settings, creation and reuse of one CR |
| TB10_RestoreRetry | Bad host during restore, no end-timestamp advance or new port, repair/retry, both restore_vif settings |

Cases run serially. A failed case stops the remaining cases and starts cleanup.
Selecting any one case still includes its setup and full cleanup. No workload
from an earlier case is required.

## Failure diagnosis

Output includes `STAGE ...`, the expected condition, and the last observed state
or API error. Examples include CR conditions, matching CR/port counts, UID
changes, IP/MAC differences, and the before/after restore timestamp.

Failures collect files under
`podv2-artifacts/<run-id>/<test-name>/` (override with `-podv2-artifacts`):

- Pod and SubnetPort JSON, Kubernetes events;
- NSX ports in the active test namespace;
- current and previous operator container logs.

The original/temporary operator configuration is kept in the recovery Secret;
it is not included in those artifacts. Diagnostics are collected before rollback.
Local fake-client tests cover recovery guards and retry behavior; they do not
substitute for running these cases on the testbed.

## Cleanup and recovery

Before the first mutation, the suite creates
`<operator-deployment>-podv2-e2e-state` in the operator namespace. This Secret is
also a run lock. It stores the original configuration, Pod template, replica
count, default SubnetSet identity, restore annotations and test resource IDs.
Existing configuration files/ConfigMaps are not edited: a temporary Secret file
mount supplies the feature settings during the run.

Every case checks the product's own deletion behavior. If that check fails,
rollback can remove remaining **test-owned** ports, but the test stays failed.
Final cleanup verifies removal of test Pods, CRs, StatefulSets, Services,
temporary SubnetSets and their live NSX backing resources, then restores the
original Deployment template/replicas, default label, and restore annotations.
ReplicaSets created by the temporary operator template are also removed after
they scale to zero. The pre-existing namespaces and DHCP parents remain. Normal output ends with
`CLEANUP PASS`; rollback failure makes the command fail and keeps the journal.

After SIGKILL, a runner crash, or an API outage, rerun the same connection/operator
arguments with:

```bash
-podv2-cleanup
```

Workload namespace and DHCP flags are unnecessary for recovery: the journal has
them. Recovery is idempotent. Use it only after the previous process has stopped.
A new test run refuses to overwrite an existing journal. If a resource has been
replaced with a different UID, recovery refuses to delete/overwrite its
replacement and reports the remaining blocker.

No program can complete remote cleanup while the API is unavailable or after its
process is killed. In those cases the retained journal and explicit recovery
command are required before a subsequent run; a failed cleanup is never reported
as a clean testbed.

## Local checks without a testbed

```bash
PODV2_LOCAL_TESTS=true go test -race ./test/e2e -count=1
go test -race ./test/e2e/podv2 -count=1
go test -c -o /tmp/podv2-e2e.test ./test/e2e
```
