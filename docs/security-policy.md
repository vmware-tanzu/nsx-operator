# NSX Operator Security Policy CRD

## Summary

Standard K8s NetworkPolicies secures traffic between Pods, a cluster administrator
may want to have the ability to configure consistent network policy through a common
API for Namespaces which apply to both Pods and VMs in K8s cluster. This document
describes SecurityPolicy CRD to support the security control of workloads within
the cluster.
SecurityPolicy CRD is used to apply NSX-T based security policy to VMs and Pods in
K8s cluster. VMs should be defined as workload resources in K8s cluster, both VMs
and Pods are attached to NSX-T segments, each workload has segment port on NSX-T.
Labels on VMs and Pods are tagged on their segment ports.
In the first version, nsx-operator leverages Tags added by NSX Container Plugin(NCP)
on NSX-T segment ports, nsx-operator needs to work together with NCP on Supervisor
cluster of vSphere with Kubernetes.
nsx-operator will reconcile SecurityPolicy CRs, call NSX-T API to create
NSX-T Distributed Firewall rules, then update CR status with realized state.

## CRD Design

SecurityPolicy is Namespaced scope CRD, admins could apply CR within a namespace to configure security of workloads
which are attached to NSX-T networking. Compared with standard K8s NetworkPolicies, SecurityPolicy CRD adds `vmSelector`
to select VMs, it also introduces some rule syntax including Drop and Reject actions, priority, rule level
selectors(`appliedTo`).

An example of SecurityPolicy CR:

```yaml
apiVersion: nsx.vmware.com/v1alpha1
kind: SecurityPolicy
metadata:
  name: db-isolation
  namespace: prod-ns
spec:
  priority: 1
  appliedTo:
    - vmSelector:
        matchLabels:
          role: db
        matchExpressions:
          - {key: k1, operator: In, values: [a1,a2]}
          - {key: k2, operator: NotIn, values: [a3,a4]}
 
  rules:
    - direction: in
      action: allow
      sources:
        - namespaceSelector:
            matchLabels:
              role: control
            matchExpressions:
              - {key: k3, operator: Exists}

        - podSelector:
            matchLabels:
              role: frontend
            matchExpressions:
            - {key: k4, operator: DoesNotExist}

      ports:
        - protocol: TCP
          port: 8000
    - direction: out
      action: allow
      destinations:
        - podSelector:
            matchLabels:
              role: dns
      ports:
        - protocol: UDP
          port: 53

      appliedTo:
        - vmSelector:
            matchLabels:
              user: internal

    - direction: in
      action: drop
    - direction: out
      action: drop
status:
  conditions:
    - type: "Ready"
      status: True
      reason: "SuccessfulRealized"
```

The example CR defines security rules for VMs which match the label "role: db"
in Namespace prod-ns. The first rule allows traffic from Namespaces matching
label "role: control" or Pods matching label "role: frontend" in Namespace prod-ns
to access through TCP with port 8000. The second rule allows the selected VMs to
access Pods with label "role: dns" through UDP with port 53. The third and forth
rules are to drop any other ingress and egress traffic to/from the selected VMs.

Below are explanations for the fields:

**spec**: defines all the configurations for a SecurityPolicy CR.

**priority**: defines the order of policy enforcement within a cluster, the range
is from 0 to 1000. Lower value has higher priority between policies.

**appliedTo**: is a list of policy targets to apply rules. As the CRD is namespaced
scope, `vmSelector` or `podSelector` will be selected from the Namespace where the
CR is created. `vmSelector` and `podSelector` cannot be in one entry as it would
not select any workload. We can also have `appliedTo` in each rule entry, but if
there is policy level `appliedTo`, it will take precedence over rule level.

**rules**: is a list of policy rules. The relative priority is based on the rule
order in the list, rules in the front have higher priority than rules in the end.

**action**: specifies the action to be applied on the rule, including 'Allow',
'Drop' and 'Reject'.

**direction**: is the direction of the rule, including 'In' or 'Ingress', 'Out'
or 'Egress'.

**sources** and **destinations**: defines a list of peers where the traffic is from/to.
It could be `podSelector`, `vmSelector`, `namespaceSelector` and `ipBlocks`.
`podSelector` and `namespaceSelector` in the same entry select particular Pods within
particular Namespaces.
`vmSelector` and `namespaceSelector` in the same entry select particular VMs within
particular Namespaces.

**status**: shows CR realization state. If there is any error during realization,
nsx-operator will also update status with error message.

## Note:
There are certain limitations for generating SecurityPolicy CR NSGroup Criteria, including:
policy 'appliedTo' group, sources group, destinations group and rule level 'appliedTo' group.
Limitations of SecurityPolicy CR:
1. NSX-T version >= 3.2.0 (for mixed criterion)
2. Max criteria in one NSGroup: 5
3. Max conditions with the mixed member type in single criterion: 15
4. Total of 35 conditions in one NSGroup criteria.
5. Operator 'NotIn' in matchExpressions for namespaceSelector is not supported, since its member type is segment
6. In one NSGroup group, supports only one 'In' with at most of five values in MatchExpressions,
   given NSX-T does not support 'In' in NSGroup condition, so we use a workaround to support 'In' with limited counts.
7. Max IP elements in one security policy: 4000
8. Priority range of SecurityPolicy CR is [0, 1000].
9. Support named port. Check build/yaml/samples/named-port-sample/*.yaml for use case.