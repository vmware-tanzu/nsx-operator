# NSX Operator

## Overview

An operator for leveraging NSX network resources to manage networking for Kubernetes cluster.

## Getting Started

Build nsx-operator with the following command:

```
make all
```

## Documentation

Right now nsx-operator supports SecurityPolicy CRD reconciling, check out
[Security Policy](docs/security-policy.md) document for details.

## Network migration support

NSX Operator migration support is opt-in. In the operator's `nsxop.ini`, set:

```ini
[coe]
enable_network_migration = true
```

The default is `false`, including when the key is omitted. Restart NSX Operator
for a configuration change to take effect; live toggling is not supported.

When enabled, explicit IPv4 requests select an existing SubnetSet member by CIDR,
with address conflict checks, reservations and retry recovery. Shared namespaces
can inherit their VPC owner's network configuration, initialize shared Subnets and
pre-created SubnetSets, and synchronize on owner/configuration changes. Existing
provider checks and T1 conflict restrictions still apply.

When disabled, the existing provider filters, configuration lookup and capacity
allocation paths apply. Existing resource deletion and cleanup remain available.
Disabling this option does not undo migration or remove shared resources; complete
migration or its rollback before disabling it for an active migration workload.

This option is independent of `vpc_wcp_enhance` and the migration operator's
`--enable-network-migration` argument. Enabling either process does not configure
the other, and this option does not itself start a migration.

## License

This repository is available under the [Apache 2.0 license](LICENSE).

NetworkResourceTransition API documentation and installation notes are available
in [Network resource transitions](docs/network-resource-transition.md).
