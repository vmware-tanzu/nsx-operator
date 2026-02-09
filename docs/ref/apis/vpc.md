# API Reference

## Packages
- [crd.nsx.vmware.com/v1alpha1](#crdnsxvmwarecomv1alpha1)


## crd.nsx.vmware.com/v1alpha1




### Resource Types
- [AddressBinding](#addressbinding)
- [IPAddressAllocation](#ipaddressallocation)
- [IPBlocksInfo](#ipblocksinfo)
- [NetworkInfo](#networkinfo)
- [SecurityPolicy](#securitypolicy)
- [StaticRoute](#staticroute)
- [Subnet](#subnet)
- [SubnetConnectionBindingMap](#subnetconnectionbindingmap)
- [SubnetIPReservation](#subnetipreservation)
- [SubnetPort](#subnetport)
- [SubnetSet](#subnetset)
- [VPCNetworkConfiguration](#vpcnetworkconfiguration)



#### AccessMode

_Underlying type:_ _string_





_Appears in:_
- [SubnetSetSpec](#subnetsetspec)
- [SubnetSpec](#subnetspec)



#### AddressBinding



AddressBinding is used to manage 1:1 NAT for a VM/NetworkInterface.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `AddressBinding` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AddressBindingSpec](#addressbindingspec)_ |  |  |  |
| `status` _[AddressBindingStatus](#addressbindingstatus)_ |  |  |  |


#### AddressBindingSpec







_Appears in:_
- [AddressBinding](#addressbinding)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vmName` _string_ | VMName contains the VM's name |  |  |
| `interfaceName` _string_ | InterfaceName contains the interface name of the VM, if not set, the first interface of the VM will be used |  |  |
| `ipAddressAllocationName` _string_ | IPAddressAllocationName contains name of the external IPAddressAllocation.<br />IP address will be allocated from an external IPBlock of the VPC when this field is not set. |  |  |


#### AddressBindingStatus







_Appears in:_
- [AddressBinding](#addressbinding)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](#condition) array_ | Conditions describes current state of AddressBinding. |  |  |
| `ipAddress` _string_ | IP Address for port binding. |  |  |


#### Condition



Condition defines condition of custom resource.



_Appears in:_
- [AddressBindingStatus](#addressbindingstatus)
- [IPAddressAllocationStatus](#ipaddressallocationstatus)
- [SecurityPolicyStatus](#securitypolicystatus)
- [StaticRouteCondition](#staticroutecondition)
- [SubnetConnectionBindingMapStatus](#subnetconnectionbindingmapstatus)
- [SubnetIPReservationStatus](#subnetipreservationstatus)
- [SubnetPortStatus](#subnetportstatus)
- [SubnetSetStatus](#subnetsetstatus)
- [SubnetStatus](#subnetstatus)
- [VPCNetworkConfigurationStatus](#vpcnetworkconfigurationstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[ConditionType](#conditiontype)_ | Type defines condition type. |  |  |
| `status` _[ConditionStatus](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#conditionstatus-v1-core)_ | Status of the condition, one of True, False, Unknown. |  |  |
| `lastTransitionTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#time-v1-meta)_ | Last time the condition transitioned from one status to another.<br />This should be when the underlying condition changed. If that is not known, then using the time when<br />the API field changed is acceptable. |  |  |
| `reason` _string_ | Reason shows a brief reason of condition. |  |  |
| `message` _string_ | Message shows a human-readable message about condition. |  |  |


#### ConditionType

_Underlying type:_ _string_





_Appears in:_
- [Condition](#condition)

| Field | Description |
| --- | --- |
| `Ready` |  |
| `GatewayConnectionReady` |  |
| `ServiceClusterReady` |  |
| `AutoSnatEnabled` |  |
| `ExternalIPBlocksConfigured` |  |
| `DeletionFailed` |  |


#### ConnectivityState

_Underlying type:_ _string_





_Appears in:_
- [SubnetAdvancedConfig](#subnetadvancedconfig)

| Field | Description |
| --- | --- |
| `Connected` |  |
| `Disconnected` |  |


#### DHCPConfigMode

_Underlying type:_ _string_





_Appears in:_
- [SubnetDHCPConfig](#subnetdhcpconfig)



#### DHCPServerAdditionalConfig



Additional DHCP server config for a VPC Subnet.
The additional configuration must not be set when the Subnet has DHCP relay enabled or DHCP is deactivated.



_Appears in:_
- [SubnetDHCPConfig](#subnetdhcpconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `reservedIPRanges` _string array_ | Reserved IP ranges.<br />Supported formats include: ["192.168.1.1", "192.168.1.3-192.168.1.100"] |  |  |


#### IPAddressAllocation



IPAddressAllocation is the Schema for the IP allocation API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `IPAddressAllocation` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[IPAddressAllocationSpec](#ipaddressallocationspec)_ |  |  |  |
| `status` _[IPAddressAllocationStatus](#ipaddressallocationstatus)_ |  |  |  |


#### IPAddressAllocationSpec



IPAddressAllocationSpec defines the desired state of IPAddressAllocation.



_Appears in:_
- [IPAddressAllocation](#ipaddressallocation)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ipAddressBlockVisibility` _[IPAddressVisibility](#ipaddressvisibility)_ | IPAddressBlockVisibility specifies the visibility of the IPBlocks to allocate IP addresses. Can be External, Private or PrivateTGW. |  | Enum: [External Private PrivateTGW] <br /> |
| `allocationSize` _integer_ | AllocationSize specifies the size of allocationIPs to be allocated.<br />It should be a power of 2. |  | Minimum: 1 <br /> |
| `allocationIPs` _string_ | AllocationIPs specifies the Allocated IP addresses in CIDR or single IP Address format. |  |  |


#### IPAddressAllocationStatus



IPAddressAllocationStatus defines the observed state of IPAddressAllocation.



_Appears in:_
- [IPAddressAllocation](#ipaddressallocation)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `allocationIPs` _string_ | AllocationIPs is the allocated IP addresses |  |  |
| `conditions` _[Condition](#condition) array_ |  |  |  |


#### IPAddressVisibility

_Underlying type:_ _string_





_Appears in:_
- [IPAddressAllocationSpec](#ipaddressallocationspec)



#### IPBlock



IPBlock describes a particular CIDR that is allowed or denied to/from the workloads matched by an AppliedTo.



_Appears in:_
- [SecurityPolicyPeer](#securitypolicypeer)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `cidr` _string_ | CIDR is a string representing the IP Block.<br />A valid example is "192.168.1.1/24". |  |  |


#### IPBlocksInfo



IPBlocksInfo is the Schema for the ipblocksinfo API





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `IPBlocksInfo` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `externalIPCIDRs` _string array_ | ExternalIPCIDRs is a list of CIDR strings. Each CIDR is a contiguous IP address<br />spaces represented by network address and prefix length. The visibility of the<br />IPBlocks is External. |  |  |
| `privateTGWIPCIDRs` _string array_ | PrivateTGWIPCIDRs is a list of CIDR strings. Each CIDR is a contiguous IP address<br />spaces represented by network address and prefix length. The visibility of the<br />IPBlocks is Private Transit Gateway. Only IPBlocks in default project will be included. |  |  |
| `externalIPRanges` _[IPPoolRange](#ippoolrange) array_ | ExternalIPRanges is an array of contiguous IP address space represented by start and end IPs.<br />The visibility of the IPBlocks is External. |  |  |
| `privateTGWIPRanges` _[IPPoolRange](#ippoolrange) array_ | PrivateTGWIPRanges is an array of contiguous IP address space represented by start and end IPs.<br />The visibility of the IPBlocks is Private Transit Gateway. |  |  |


#### IPPoolRange







_Appears in:_
- [IPBlocksInfo](#ipblocksinfo)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `start` _string_ | The start IP Address of the IP Range. |  |  |
| `end` _string_ | The end IP Address of the IP Range. |  |  |


#### NetworkInfo



NetworkInfo is used to report the network information for a namespace.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `NetworkInfo` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `vpcs` _[VPCState](#vpcstate) array_ |  |  |  |


#### NetworkInterfaceConfig







_Appears in:_
- [SubnetPortStatus](#subnetportstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `logicalSwitchUUID` _string_ | NSX Logical Switch UUID of the Subnet. |  |  |
| `ipAddresses` _[NetworkInterfaceIPAddress](#networkinterfaceipaddress) array_ |  |  |  |
| `macAddress` _string_ | The MAC address. |  |  |
| `dhcpDeactivatedOnSubnet` _boolean_ | DHCPDeactivatedOnSubnet indicates whether DHCP is deactivated on the Subnet. |  |  |


#### NetworkInterfaceIPAddress







_Appears in:_
- [NetworkInterfaceConfig](#networkinterfaceconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ipAddress` _string_ | IP address string with the prefix. |  |  |
| `gateway` _string_ | Gateway address of the Subnet. |  |  |


#### NetworkStackType

_Underlying type:_ _string_





_Appears in:_
- [VPCState](#vpcstate)

| Field | Description |
| --- | --- |
| `FullStackVPC` |  |
| `VLANBackedVPC` |  |


#### NextHop



NextHop defines next hop configuration for network.



_Appears in:_
- [StaticRouteSpec](#staticroutespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ipAddress` _string_ | Next hop gateway IP address. |  | Format: ip <br /> |


#### PortAddressBinding



PortAddressBinding defines static addresses for the Port.



_Appears in:_
- [SubnetPortSpec](#subnetportspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ipAddress` _string_ | The IP Address. |  |  |
| `macAddress` _string_ | The MAC address. |  |  |


#### PortAttachment



VIF attachment state of a SubnetPort.



_Appears in:_
- [SubnetPortStatus](#subnetportstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ | ID of the SubnetPort VIF attachment. |  |  |


#### RuleAction

_Underlying type:_ _string_

RuleAction describes the action to be applied on traffic matching a rule.



_Appears in:_
- [SecurityPolicyRule](#securitypolicyrule)

| Field | Description |
| --- | --- |
| `Allow` | RuleActionAllow describes that the traffic matching the rule must be allowed.<br /> |
| `Drop` | RuleActionDrop describes that the traffic matching the rule must be dropped.<br /> |
| `Reject` | RuleActionReject indicates that the traffic matching the rule must be rejected and the<br />client will receive a response.<br /> |


#### RuleDirection

_Underlying type:_ _string_

RuleDirection specifies the direction of traffic.



_Appears in:_
- [SecurityPolicyRule](#securitypolicyrule)

| Field | Description |
| --- | --- |
| `In` | RuleDirectionIn specifies that the direction of traffic must be ingress, equivalent to "Ingress".<br /> |
| `Ingress` | RuleDirectionIngress specifies that the direction of traffic must be ingress, equivalent to "In".<br /> |
| `Out` | RuleDirectionOut specifies that the direction of traffic must be egress, equivalent to "Egress".<br /> |
| `Egress` | RuleDirectionEgress specifies that the direction of traffic must be egress, equivalent to "Out".<br /> |


#### SecurityPolicy



SecurityPolicy is the Schema for the securitypolicies API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `SecurityPolicy` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SecurityPolicySpec](#securitypolicyspec)_ |  |  |  |
| `status` _[SecurityPolicyStatus](#securitypolicystatus)_ |  |  |  |


#### SecurityPolicyPeer



SecurityPolicyPeer defines the source or destination of traffic.



_Appears in:_
- [SecurityPolicyRule](#securitypolicyrule)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vmSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#labelselector-v1-meta)_ | VMSelector uses label selector to select VMs. |  |  |
| `podSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#labelselector-v1-meta)_ | PodSelector uses label selector to select Pods. |  |  |
| `namespaceSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#labelselector-v1-meta)_ | NamespaceSelector uses label selector to select Namespaces. |  |  |
| `ipBlocks` _[IPBlock](#ipblock) array_ | IPBlocks is a list of IP CIDRs. |  |  |


#### SecurityPolicyPort



SecurityPolicyPort describes protocol and ports for traffic.



_Appears in:_
- [SecurityPolicyRule](#securitypolicyrule)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `protocol` _[Protocol](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#protocol-v1-core)_ | Protocol(TCP, UDP) is the protocol to match traffic.<br />It is TCP by default. | TCP |  |
| `port` _[IntOrString](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#intorstring-intstr-util)_ | Port is the name or port number. |  |  |
| `endPort` _integer_ | EndPort defines the end of port range. |  |  |


#### SecurityPolicyRule



SecurityPolicyRule defines a rule of SecurityPolicy.



_Appears in:_
- [SecurityPolicySpec](#securitypolicyspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `action` _[RuleAction](#ruleaction)_ | Action specifies the action to be applied on the rule. |  |  |
| `appliedTo` _[SecurityPolicyTarget](#securitypolicytarget) array_ | AppliedTo is a list of rule targets.<br />Policy level 'Applied To' will take precedence over rule level. |  |  |
| `direction` _[RuleDirection](#ruledirection)_ | Direction is the direction of the rule, including 'In' or 'Ingress', 'Out' or 'Egress'. |  |  |
| `sources` _[SecurityPolicyPeer](#securitypolicypeer) array_ | Sources defines the endpoints where the traffic is from. For ingress rule only. |  |  |
| `destinations` _[SecurityPolicyPeer](#securitypolicypeer) array_ | Destinations defines the endpoints where the traffic is to. For egress rule only. |  |  |
| `ports` _[SecurityPolicyPort](#securitypolicyport) array_ | Ports is a list of ports to be matched. |  |  |
| `name` _string_ | Name is the display name of this rule. |  |  |


#### SecurityPolicySpec



SecurityPolicySpec defines the desired state of SecurityPolicy.



_Appears in:_
- [SecurityPolicy](#securitypolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `priority` _integer_ | Priority defines the order of policy enforcement. |  | Maximum: 1000 <br />Minimum: 0 <br /> |
| `appliedTo` _[SecurityPolicyTarget](#securitypolicytarget) array_ | AppliedTo is a list of policy targets to apply rules.<br />Policy level 'Applied To' will take precedence over rule level. |  |  |
| `rules` _[SecurityPolicyRule](#securitypolicyrule) array_ | Rules is a list of policy rules. |  |  |


#### SecurityPolicyStatus



SecurityPolicyStatus defines the observed state of SecurityPolicy.



_Appears in:_
- [SecurityPolicy](#securitypolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](#condition) array_ | Conditions describes current state of security policy. |  |  |


#### SecurityPolicyTarget



SecurityPolicyTarget defines the target endpoints to apply SecurityPolicy.



_Appears in:_
- [SecurityPolicyRule](#securitypolicyrule)
- [SecurityPolicySpec](#securitypolicyspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vmSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#labelselector-v1-meta)_ | VMSelector uses label selector to select VMs. |  |  |
| `podSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#labelselector-v1-meta)_ | PodSelector uses label selector to select Pods. |  |  |


#### SharedSubnet



SharedSubnet defines the information for a Subnet shared with vSphere Namespace.



_Appears in:_
- [VPCNetworkConfigurationSpec](#vpcnetworkconfigurationspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `path` _string_ | NSX path of Subnets created outside of the Supervisor to be associated with this vSphere Namespace |  |  |
| `podDefault` _boolean_ | Indicates if this Subnet is used for the Pod default network. |  |  |
| `vmDefault` _boolean_ | Indicates if this Subnet is used for the VM default network. |  |  |
| `name` _string_ | Name of the Subnet. If the name is empty, it will be derived from the shared Subnet path.<br />This field is immutable. |  |  |


#### StaticIPAllocation







_Appears in:_
- [SubnetAdvancedConfig](#subnetadvancedconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Activate or deactivate static IP allocation for VPC Subnet Ports.<br />If the DHCP mode is DHCPDeactivated or not set, its default value is true.<br />If the DHCP mode is DHCPServer or DHCPRelay, its default value is false.<br />The value cannot be set to true when the DHCP mode is DHCPServer or DHCPRelay. |  |  |


#### StaticRoute



StaticRoute is the Schema for the staticroutes API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `StaticRoute` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[StaticRouteSpec](#staticroutespec)_ |  |  |  |
| `status` _[StaticRouteStatus](#staticroutestatus)_ |  |  |  |


#### StaticRouteCondition

_Underlying type:_ _[Condition](#condition)_

StaticRouteCondition defines condition of StaticRoute.



_Appears in:_
- [StaticRouteStatus](#staticroutestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[ConditionType](#conditiontype)_ | Type defines condition type. |  |  |
| `status` _[ConditionStatus](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#conditionstatus-v1-core)_ | Status of the condition, one of True, False, Unknown. |  |  |
| `lastTransitionTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#time-v1-meta)_ | Last time the condition transitioned from one status to another.<br />This should be when the underlying condition changed. If that is not known, then using the time when<br />the API field changed is acceptable. |  |  |
| `reason` _string_ | Reason shows a brief reason of condition. |  |  |
| `message` _string_ | Message shows a human-readable message about condition. |  |  |


#### StaticRouteSpec



StaticRouteSpec defines static routes configuration on VPC.



_Appears in:_
- [StaticRoute](#staticroute)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `network` _string_ | Specify network address in CIDR format. |  | Format: cidr <br /> |
| `nextHops` _[NextHop](#nexthop) array_ | Next hop gateway |  | MinItems: 1 <br /> |


#### StaticRouteStatus



StaticRouteStatus defines the observed state of StaticRoute.



_Appears in:_
- [StaticRoute](#staticroute)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[StaticRouteCondition](#staticroutecondition) array_ |  |  |  |




#### Subnet



Subnet is the Schema for the subnets API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `Subnet` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SubnetSpec](#subnetspec)_ |  |  |  |
| `status` _[SubnetStatus](#subnetstatus)_ |  |  |  |


#### SubnetAdvancedConfig







_Appears in:_
- [SubnetSpec](#subnetspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `connectivityState` _[ConnectivityState](#connectivitystate)_ | Connectivity status of the Subnet from other Subnets of the VPC.<br />The default value is "Connected". | Connected | Enum: [Connected Disconnected] <br /> |
| `staticIPAllocation` _[StaticIPAllocation](#staticipallocation)_ | Static IP allocation for VPC Subnet Ports. |  |  |
| `gatewayAddresses` _string array_ | GatewayAddresses specifies custom gateway IP addresses for the Subnet. |  | MaxItems: 1 <br /> |
| `dhcpServerAddresses` _string array_ | DHCPServerAddresses specifies custom DHCP server IP addresses for the Subnet. |  | MaxItems: 1 <br /> |


#### SubnetConnectionBindingMap



SubnetConnectionBindingMap is the Schema for the SubnetConnectionBindingMap API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `SubnetConnectionBindingMap` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SubnetConnectionBindingMapSpec](#subnetconnectionbindingmapspec)_ |  |  |  |
| `status` _[SubnetConnectionBindingMapStatus](#subnetconnectionbindingmapstatus)_ |  |  |  |


#### SubnetConnectionBindingMapSpec







_Appears in:_
- [SubnetConnectionBindingMap](#subnetconnectionbindingmap)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `subnetName` _string_ | SubnetName is the Subnet name which this SubnetConnectionBindingMap is associated. |  |  |
| `targetSubnetSetName` _string_ | TargetSubnetSetName specifies the target SubnetSet which a Subnet is connected to. |  | Optional: \{\} <br /> |
| `targetSubnetName` _string_ | TargetSubnetName specifies the target Subnet which a Subnet is connected to. |  | Optional: \{\} <br /> |
| `vlanTrafficTag` _integer_ | VLANTrafficTag is the VLAN tag configured in the binding. Note, the value of VLANTrafficTag should be<br />unique on the target Subnet or SubnetSet. |  | Maximum: 4094 <br />Minimum: 0 <br />Required: \{\} <br /> |


#### SubnetConnectionBindingMapStatus



SubnetConnectionBindingMapStatus defines the observed state of SubnetConnectionBindingMap.



_Appears in:_
- [SubnetConnectionBindingMap](#subnetconnectionbindingmap)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](#condition) array_ | Conditions described if the SubnetConnectionBindingMaps is configured on NSX or not.<br />Condition type "" |  |  |


#### SubnetDHCPConfig



SubnetDHCPConfig is a DHCP configuration for Subnet.



_Appears in:_
- [SubnetSetSpec](#subnetsetspec)
- [SubnetSpec](#subnetspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _[DHCPConfigMode](#dhcpconfigmode)_ | DHCP Mode. DHCPDeactivated will be used if it is not defined.<br />It cannot switch from DHCPDeactivated to DHCPServer or DHCPRelay. |  | Enum: [DHCPServer DHCPRelay DHCPDeactivated] <br /> |
| `dhcpServerAdditionalConfig` _[DHCPServerAdditionalConfig](#dhcpserveradditionalconfig)_ | Additional DHCP server config for a VPC Subnet. |  |  |


#### SubnetIPReservation



SubnetIPReservation is the Schema for the subnetipreservations API





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `SubnetIPReservation` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SubnetIPReservationSpec](#subnetipreservationspec)_ |  |  | Required: \{\} <br /> |
| `status` _[SubnetIPReservationStatus](#subnetipreservationstatus)_ |  |  |  |


#### SubnetIPReservationSpec



SubnetIPReservationSpec defines the desired state of SubnetIPReservation



_Appears in:_
- [SubnetIPReservation](#subnetipreservation)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `subnet` _string_ | Subnet specifies the Subnet to reserve IPs from.<br />The Subnet needs to have static IP allocation activated. |  | Required: \{\} <br /> |
| `numberOfIPs` _integer_ | NumberOfIPs defines number of IPs requested to be reserved. |  | Maximum: 100 <br />Minimum: 1 <br />Required: \{\} <br /> |


#### SubnetIPReservationStatus



SubnetIPReservationStatus defines the observed state of SubnetIPReservation



_Appears in:_
- [SubnetIPReservation](#subnetipreservation)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](#condition) array_ | Conditions described if the SubnetIPReservation is configured on NSX or not.<br />Condition type "" |  |  |
| `ips` _string array_ | List of reserved IPs.<br />Supported formats include: ["192.168.1.1", "192.168.1.3-192.168.1.100"] |  |  |


#### SubnetInfo



SubnetInfo defines the observed state of a single Subnet of a SubnetSet.



_Appears in:_
- [SubnetSetStatus](#subnetsetstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `networkAddresses` _string array_ | Network address of the Subnet. |  |  |
| `gatewayAddresses` _string array_ | Gateway address of the Subnet. |  |  |
| `DHCPServerAddresses` _string array_ | Dhcp server IP address. |  |  |


#### SubnetPort



SubnetPort is the Schema for the subnetports API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `SubnetPort` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SubnetPortSpec](#subnetportspec)_ |  |  |  |
| `status` _[SubnetPortStatus](#subnetportstatus)_ |  |  |  |


#### SubnetPortSpec



SubnetPortSpec defines the desired state of SubnetPort.



_Appears in:_
- [SubnetPort](#subnetport)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `subnet` _string_ | Subnet defines the parent Subnet name of the SubnetPort. |  |  |
| `subnetSet` _string_ | SubnetSet defines the parent SubnetSet name of the SubnetPort. |  |  |
| `addressBindings` _[PortAddressBinding](#portaddressbinding) array_ | AddressBindings defines static address bindings used for the SubnetPort. |  |  |


#### SubnetPortStatus



SubnetPortStatus defines the observed state of SubnetPort.



_Appears in:_
- [SubnetPort](#subnetport)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](#condition) array_ | Conditions describes current state of SubnetPort. |  |  |
| `attachment` _[PortAttachment](#portattachment)_ | SubnetPort attachment state. |  |  |
| `networkInterfaceConfig` _[NetworkInterfaceConfig](#networkinterfaceconfig)_ |  |  |  |


#### SubnetSet



SubnetSet is the Schema for the subnetsets API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `SubnetSet` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SubnetSetSpec](#subnetsetspec)_ |  |  |  |
| `status` _[SubnetSetStatus](#subnetsetstatus)_ |  |  |  |


#### SubnetSetSpec



SubnetSetSpec defines the desired state of SubnetSet.



_Appears in:_
- [SubnetSet](#subnetset)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ipv4SubnetSize` _integer_ | Size of Subnet based upon estimated workload count. |  | Maximum: 65536 <br /> |
| `accessMode` _[AccessMode](#accessmode)_ | Access mode of Subnet, accessible only from within VPC or from outside VPC. |  | Enum: [Private Public PrivateTGW] <br /> |
| `subnetDHCPConfig` _[SubnetDHCPConfig](#subnetdhcpconfig)_ | Subnet DHCP configuration. |  |  |
| `subnetNames` _string_ | The names of the Subnets that have been created in advance.<br />It is mutually exclusive with the other fields like IPv4SubnetSize, AccessMode, and SubnetDHCPConfig.<br />Once this field is set, the other fields cannot be set. |  |  |


#### SubnetSetStatus



SubnetSetStatus defines the observed state of SubnetSet.



_Appears in:_
- [SubnetSet](#subnetset)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](#condition) array_ |  |  |  |
| `subnets` _[SubnetInfo](#subnetinfo) array_ |  |  |  |


#### SubnetSpec



SubnetSpec defines the desired state of Subnet.



_Appears in:_
- [Subnet](#subnet)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vpcName` _string_ | VPC name of the Subnet. |  |  |
| `ipv4SubnetSize` _integer_ | Size of Subnet based upon estimated workload count. |  | Maximum: 65536 <br /> |
| `accessMode` _[AccessMode](#accessmode)_ | Access mode of Subnet, accessible only from within VPC or from outside VPC. |  | Enum: [Private Public PrivateTGW L2Only] <br /> |
| `ipAddresses` _string array_ | Subnet CIDRS. |  | MaxItems: 2 <br />MinItems: 0 <br /> |
| `subnetDHCPConfig` _[SubnetDHCPConfig](#subnetdhcpconfig)_ | DHCP configuration for Subnet. |  |  |
| `advancedConfig` _[SubnetAdvancedConfig](#subnetadvancedconfig)_ | VPC Subnet advanced configuration. |  |  |
| `vlanConnectionName` _string_ | Distributed VLAN Connection name. |  |  |


#### SubnetStatus



SubnetStatus defines the observed state of Subnet.



_Appears in:_
- [Subnet](#subnet)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `networkAddresses` _string array_ | Network address of the Subnet. |  |  |
| `gatewayAddresses` _string array_ | Gateway address of the Subnet. |  |  |
| `DHCPServerAddresses` _string array_ | DHCP server IP address. |  |  |
| `vlanExtension` _[VLANExtension](#vlanextension)_ | VLAN extension configured for VPC Subnet. |  |  |
| `shared` _boolean_ | Whether this is a pre-created Subnet shared with the Namespace. | false |  |
| `conditions` _[Condition](#condition) array_ |  |  |  |


#### VLANExtension



VLANExtension describes VLAN extension configuration for the VPC Subnet.



_Appears in:_
- [SubnetStatus](#subnetstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vpcGatewayConnectionEnable` _boolean_ | Flag to control whether the VLAN extension Subnet connects to the VPC gateway. |  |  |
| `vlanId` _integer_ | VLAN ID of the VLAN extension Subnet. |  |  |


#### VPCInfo



VPCInfo defines VPC info needed by tenant admin.



_Appears in:_
- [VPCNetworkConfigurationStatus](#vpcnetworkconfigurationstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | VPC name. |  |  |
| `lbSubnetPath` _string_ | AVISESubnetPath is the NSX Policy Path for the AVI SE Subnet. |  |  |
| `nsxLoadBalancerPath` _string_ | NSXLoadBalancerPath is the NSX Policy path for the NSX Load Balancer. |  |  |
| `vpcPath` _string_ | NSX Policy path for VPC. |  |  |


#### VPCNetworkConfiguration



VPCNetworkConfiguration is the Schema for the vpcnetworkconfigurations API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `crd.nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `VPCNetworkConfiguration` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[VPCNetworkConfigurationSpec](#vpcnetworkconfigurationspec)_ |  |  |  |
| `status` _[VPCNetworkConfigurationStatus](#vpcnetworkconfigurationstatus)_ |  |  |  |


#### VPCNetworkConfigurationSpec



VPCNetworkConfigurationSpec defines the desired state of VPCNetworkConfiguration.
There is a default VPCNetworkConfiguration that applies to Namespaces
do not have a VPCNetworkConfiguration assigned. When a field is not set
in a Namespace's VPCNetworkConfiguration, the Namespace will use the value
in the default VPCNetworkConfiguration.



_Appears in:_
- [VPCNetworkConfiguration](#vpcnetworkconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vpc` _string_ | NSX path of the VPC the Namespace is associated with.<br />If vpc is set, only defaultSubnetSize takes effect, other fields are ignored. |  |  |
| `subnets` _[SharedSubnet](#sharedsubnet) array_ | Shared Subnets the Namespace is associated with. |  |  |
| `nsxProject` _string_ | NSX Project the Namespace is associated with. |  |  |
| `vpcConnectivityProfile` _string_ | VPCConnectivityProfile Path. This profile has configuration related to creating VPC transit gateway attachment. |  |  |
| `privateIPs` _string array_ | Private IPs. |  |  |
| `defaultSubnetSize` _integer_ | Default size of Subnets.<br />Defaults to 32. | 32 | Maximum: 65536 <br /> |


#### VPCNetworkConfigurationStatus



VPCNetworkConfigurationStatus defines the observed state of VPCNetworkConfiguration



_Appears in:_
- [VPCNetworkConfiguration](#vpcnetworkconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vpcs` _[VPCInfo](#vpcinfo) array_ | VPCs describes VPC info, now it includes Load Balancer Subnet info which are needed<br />for the Avi Kubernetes Operator (AKO). |  |  |
| `conditions` _[Condition](#condition) array_ | Conditions describe current state of VPCNetworkConfiguration. |  |  |


#### VPCState



VPCState defines information for VPC.



_Appears in:_
- [NetworkInfo](#networkinfo)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | VPC name. |  |  |
| `defaultSNATIP` _string_ | Default SNAT IP for Private Subnets. |  |  |
| `loadBalancerIPAddresses` _string_ | LoadBalancerIPAddresses (AVI SE Subnet CIDR or NSX LB SNAT IPs). |  |  |
| `privateIPs` _string array_ | Private CIDRs used for the VPC. |  |  |
| `networkStack` _[NetworkStackType](#networkstacktype)_ | NetworkStack indicates the networking stack for the VPC.<br />Valid values: FullStackVPC, VLANBackedVPC |  | Enum: [FullStackVPC VLANBackedVPC] <br /> |


