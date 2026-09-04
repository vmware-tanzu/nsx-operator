# API Reference

## Packages
- [nsx.vmware.com/v1alpha1](#nsxvmwarecomv1alpha1)


## nsx.vmware.com/v1alpha1



### Resource Types
- [NSXNetworkConfiguration](#nsxnetworkconfiguration)
- [NSXServiceAccount](#nsxserviceaccount)
- [SecurityPolicy](#securitypolicy)



#### Condition



Condition defines condition of custom resource.



_Appears in:_
- [SecurityPolicyStatus](#securitypolicystatus)

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


#### IPBlock



IPBlock describes a particular CIDR that is allowed or denied to/from the workloads matched by an AppliedTo.
Both IPv4 and IPv6 CIDRs are supported.



_Appears in:_
- [SecurityPolicyPeer](#securitypolicypeer)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `cidr` _string_ | CIDR is a string representing the IP Block.<br />Valid examples are "192.168.1.0/24" and "2001:db8::/32". |  |  |


#### ModeType

_Underlying type:_ _string_

ModeType defines the routing mode supported.



_Appears in:_
- [NSXNetworkConfigurationSpec](#nsxnetworkconfigurationspec)

| Field | Description |
| --- | --- |
| `NAT` | TypeNat defines the Mode as NAT.<br /> |
| `ROUTED` | TypeRouted defines the Mode as ROUTED.<br /> |


#### NSXNetworkConfiguration



NSXNetworkConfiguration is the Schema for the nsxnetworkconfigurations API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `NSXNetworkConfiguration` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[NSXNetworkConfigurationSpec](#nsxnetworkconfigurationspec)_ |  |  |  |
| `status` _[NSXNetworkConfigurationStatus](#nsxnetworkconfigurationstatus)_ |  |  |  |


#### NSXNetworkConfigurationCondition



NSXNetworkConfigurationCondition defines the condition for the NSXNetworkConfiguration.



_Appears in:_
- [NSXNetworkConfigurationStatus](#nsxnetworkconfigurationstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[NSXNetworkConfigurationConditionType](#nsxnetworkconfigurationconditiontype)_ |  |  |  |
| `status` _[ConditionStatus](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#conditionstatus-v1-core)_ |  |  |  |
| `reason` _string_ |  |  |  |
| `message` _string_ |  |  |  |


#### NSXNetworkConfigurationConditionType

_Underlying type:_ _string_

NSXNetworkConfigurationConditionType describes the condition type.



_Appears in:_
- [NSXNetworkConfigurationCondition](#nsxnetworkconfigurationcondition)

| Field | Description |
| --- | --- |
| `ready` | NSXNetworkConfigurationConditionTypeReady means NSXNetworkConfiguration is healthy.<br /> |
| `failure` | NSXNetworkConfigurationConditionTypeFailure means NSXNetworkConfiguration is unhealthy.<br /> |


#### NSXNetworkConfigurationSpec



NSXNetworkConfigurationSpec defines the desired state of NSXNetworkConfiguration.



_Appears in:_
- [NSXNetworkConfiguration](#nsxnetworkconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ingressCIDRs` _string array_ | IngressCIDRs defines CIDRs used for ingress traffic. |  |  |
| `egressCIDRs` _string array_ | EgressCIDRs defines CIDRs used for egress SNAT. |  |  |
| `appliedToNamespaces` _string array_ | AppliedToNamespaces lists the namespaces where this network configuration applies. |  |  |
| `mode` _[ModeType](#modetype)_ | Mode specifies the network mode: NAT or ROUTED. |  |  |
| `subnetCIDRs` _string array_ | SubnetCIDRs defines the CIDRs allocated for subnets. |  |  |
| `tier0Gateway` _string_ | Tier0Gateway specifies the path or name of the Tier0 gateway. |  |  |
| `loadbalancerSize` _[SizeType](#sizetype)_ | LoadBalancerSize specifies the default load balancer size. |  |  |
| `subnetPrefixLength` _integer_ | SubnetPrefixLength specifies the prefix length for namespace subnets. |  |  |
| `subnetQuota` _integer_ | SubnetQuota specifies quota for subnets. |  |  |
| `snatIPQuota` _integer_ | SNATIPQuota specifies quota for SNAT IPs. |  |  |
| `lbVIPQuota` _integer_ | LBVIPQuota specifies quota for LoadBalancer VIPs. |  |  |
| `l7ruleQuota` _integer_ | L7RuleQuota specifies quota for L7 rules. |  |  |


#### NSXNetworkConfigurationStatus



NSXNetworkConfigurationStatus defines the observed state of NSXNetworkConfiguration.



_Appears in:_
- [NSXNetworkConfiguration](#nsxnetworkconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[NSXNetworkConfigurationCondition](#nsxnetworkconfigurationcondition) array_ |  |  |  |


#### NSXProxyEndpoint







_Appears in:_
- [NSXServiceAccountStatus](#nsxserviceaccountstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `addresses` _[NSXProxyEndpointAddress](#nsxproxyendpointaddress) array_ |  |  |  |
| `ports` _[NSXProxyEndpointPort](#nsxproxyendpointport) array_ |  |  |  |


#### NSXProxyEndpointAddress







_Appears in:_
- [NSXProxyEndpoint](#nsxproxyendpoint)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `hostname` _string_ |  |  |  |
| `ip` _string_ |  |  | Format: ip <br /> |


#### NSXProxyEndpointPort







_Appears in:_
- [NSXProxyEndpoint](#nsxproxyendpoint)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  |  |
| `port` _integer_ |  |  |  |
| `protocol` _[NSXProxyProtocol](#nsxproxyprotocol)_ |  |  |  |


#### NSXProxyProtocol

_Underlying type:_ _string_





_Appears in:_
- [NSXProxyEndpointPort](#nsxproxyendpointport)

| Field | Description |
| --- | --- |
| `TCP` |  |


#### NSXRestoreStatus







_Appears in:_
- [NSXServiceAccountStatus](#nsxserviceaccountstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ |  |  |  |
| `status` _string_ |  |  |  |
| `restoreEndTime` _integer_ |  |  |  |


#### NSXSecret







_Appears in:_
- [NSXServiceAccountStatus](#nsxserviceaccountstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  |  |
| `namespace` _string_ |  |  |  |


#### NSXServiceAccount



NSXServiceAccount is the Schema for the nsxserviceaccounts API





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `nsx.vmware.com/v1alpha1` | | |
| `kind` _string_ | `NSXServiceAccount` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[NSXServiceAccountSpec](#nsxserviceaccountspec)_ |  |  |  |
| `status` _[NSXServiceAccountStatus](#nsxserviceaccountstatus)_ |  |  |  |


#### NSXServiceAccountPhase

_Underlying type:_ _string_





_Appears in:_
- [NSXServiceAccountStatus](#nsxserviceaccountstatus)

| Field | Description |
| --- | --- |
| `realized` |  |
| `inProgress` |  |
| `failed` |  |


#### NSXServiceAccountSpec



NSXServiceAccountSpec defines the desired state of NSXServiceAccount



_Appears in:_
- [NSXServiceAccount](#nsxserviceaccount)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `vpcName` _string_ |  |  |  |
| `enableCertRotation` _boolean_ | EnableCertRotation enables cert rotation feature in this cluster when NSXT >=4.1.3 |  |  |


#### NSXServiceAccountStatus



NSXServiceAccountStatus defines the observed state of NSXServiceAccount



_Appears in:_
- [NSXServiceAccount](#nsxserviceaccount)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `phase` _[NSXServiceAccountPhase](#nsxserviceaccountphase)_ |  |  |  |
| `reason` _string_ |  |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#condition-v1-meta) array_ | Represents the realization status of a NSXServiceAccount's current state.<br />Known .status.conditions.type is: "Realized" |  |  |
| `vpcPath` _string_ |  |  |  |
| `nsxManagers` _string array_ |  |  |  |
| `proxyEndpoints` _[NSXProxyEndpoint](#nsxproxyendpoint)_ |  |  |  |
| `clusterID` _string_ |  |  |  |
| `clusterName` _string_ |  |  |  |
| `secrets` _[NSXSecret](#nsxsecret) array_ |  |  |  |
| `nsxRestoreStatus` _[NSXRestoreStatus](#nsxrestorestatus)_ |  |  |  |


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
| `apiVersion` _string_ | `nsx.vmware.com/v1alpha1` | | |
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
| `sources` _[SecurityPolicyPeer](#securitypolicypeer) array_ | Deprecated: use From instead.<br />Sources defines the endpoints where the traffic is from. For ingress rule only. |  |  |
| `destinations` _[SecurityPolicyPeer](#securitypolicypeer) array_ | Deprecated: use To instead.<br />Destinations defines the endpoints where the traffic is to. For egress rule only. |  |  |
| `from` _[SecurityPolicyPeer](#securitypolicypeer) array_ | From defines the endpoints where the traffic is from. For ingress rule only.<br />This is the preferred field over the deprecated Sources. |  |  |
| `to` _[SecurityPolicyPeer](#securitypolicypeer) array_ | To defines the endpoints where the traffic is to. For egress rule only.<br />This is the preferred field over the deprecated Destinations. |  |  |
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


#### SizeType

_Underlying type:_ _string_

SizeType defines load balancer size supported.



_Appears in:_
- [NSXNetworkConfigurationSpec](#nsxnetworkconfigurationspec)

| Field | Description |
| --- | --- |
| `SMALL` | LbSizeSmall defines the size as SMALL.<br /> |
| `MEDIUM` | LbSizeMedium defines the size as MEDIUM.<br /> |
| `LARGE` | LbSizeLarge defines the size as LARGE.<br /> |


