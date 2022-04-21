package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	meta1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	util2 "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// When a rule contains named port, we should consider whether the rule should expand to
// multiple rules if the port name maps to conflicted port numbers.
func (service *SecurityPolicyService) expandRule(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	ruleIdx int) ([]*model.Group, []*model.Rule, error) {

	var nsxRules []*model.Rule
	var nsxGroups []*model.Group

	if len(rule.Ports) == 0 {
		rs, err := service.buildRuleBasicInfo(obj, rule, ruleIdx, 0, 0)
		if err != nil {
			return nil, nil, err
		}
		nsxRules = append(nsxRules, rs)
	}
	for portIdx, port := range rule.Ports {
		gs, rs, err := service.expandRuleByPort(obj, rule, ruleIdx, port, portIdx)
		if err != nil {
			return nil, nil, err
		}
		nsxGroups = append(nsxGroups, gs...)
		nsxRules = append(nsxRules, rs...)
	}
	return nsxGroups, nsxRules, nil
}

func (service *SecurityPolicyService) expandRuleByPort(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	ruleIdx int, port v1alpha1.SecurityPolicyPort, portIdx int) ([]*model.Group, []*model.Rule, error) {

	var err error
	var startPort []util.Address
	var nsxGroups []*model.Group
	var nsxRules []*model.Rule

	if port.Port.Type == intstr.Int {
		startPort = append(startPort, util.Address{Port: port.Port.IntValue()})
	} else {
		startPort, err = service.resolveNamedPort(obj, rule, port)
		if err != nil {
			return nil, nil, err
		}
	}

	for dupPortIdx, portIP := range startPort {
		gs, r, err := service.expandRuleByService(obj, rule, ruleIdx, port, portIdx, portIP, dupPortIdx)
		if err != nil {
			return nil, nil, err
		}
		nsxRules = append(nsxRules, r)
		nsxGroups = append(nsxGroups, gs...)
	}
	return nsxGroups, nsxRules, nil
}

func (service *SecurityPolicyService) expandRuleByService(obj *v1alpha1.SecurityPolicy,
	rule *v1alpha1.SecurityPolicyRule, ruleIdx int, port v1alpha1.SecurityPolicyPort,
	portIdx int, portIP util.Address, dupPortIdx int) ([]*model.Group, *model.Rule, error) {

	var nsxGroups []*model.Group

	nsxRule, err := service.buildRuleBasicInfo(obj, rule, ruleIdx, portIdx, dupPortIdx)
	if err != nil {
		return nil, nil, err
	}

	var ruleServiceEntries []*data.StructValue
	serviceEntry := service.buildEntry(port, portIP)
	ruleServiceEntries = append(ruleServiceEntries, serviceEntry)
	nsxRule.ServiceEntries = ruleServiceEntries

	if len(portIP.IPs) > 0 {
		ruleIPSetGroup := service.buildRuleIPGroup(nsxRule, portIP.IPs)
		groupPath := fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(service), *ruleIPSetGroup.Id)
		nsxRule.DestinationGroups = []string{groupPath}
		log.V(2).Info("built ruleIPSetGroup", "ruleIPSetGroup", ruleIPSetGroup)
		nsxGroups = append(nsxGroups, ruleIPSetGroup)
	}
	log.V(2).Info("built rule by service entry", "rule", nsxRule)
	return nsxGroups, nsxRule, nil
}

// Resolve a named port to port number by rule and policy selector.
// e.g. "http" -> [{"80":['1.1.1.1', '2.2.2.2']}, {"443":['3.3.3.3']}]
func (service *SecurityPolicyService) resolveNamedPort(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	spPort v1alpha1.SecurityPolicyPort) ([]util.Address, error) {

	var address []util.Address

	podSelector, namespaces, err := service.getPodSelector(obj, rule)
	if err != nil {
		return nil, err
	}
	if len(namespaces) == 0 {
		namespaces = append(namespaces, obj.Namespace)
	}

	for _, namespace := range namespaces {
		podsList := &v1.PodList{}
		podSelector.Namespace = namespace
		log.V(2).Info("port", "podSelector", podSelector)
		err := service.Client.List(context.Background(), podsList, podSelector)
		if err != nil {
			return nil, err
		}
		for _, pod := range podsList.Items {
			addr, err := service.resolvePodPort(&pod, &spPort)
			switch err.(type) {
			default:
			case util2.PodIPNotFound, util2.PodNotRunning:
				return nil, err
			}
			address = append(address, addr...)
		}
	}

	if len(address) == 0 {
		return nil, util2.NoFilteredPod{
			Desc: "no pod has the corresponding named port, perhaps pod has not been created yet",
		}
	}
	return util.MergeAddressByPort(address), nil
}

// Check port name and protocol, only when the pod is really running, and it does have effective ip.
func (service *SecurityPolicyService) resolvePodPort(pod *v1.Pod, spPort *v1alpha1.SecurityPolicyPort) (
	[]util.Address, error) {
	var addr []util.Address
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			log.V(2).Info("resolvePodPort", "namespace", pod.Namespace, "pod_name", pod.Name,
				"port_name", port.Name, "containerPort", port.ContainerPort,
				"protocol", port.Protocol, "podIP", pod.Status.PodIP)
			if port.Name == spPort.Port.String() && port.Protocol == spPort.Protocol {
				if pod.Status.Phase != "Running" {
					errMsg := fmt.Sprintf("pod %s/%s is not running", pod.Namespace, pod.Name)
					return nil, util2.PodNotRunning{Desc: errMsg}
				}
				if pod.Status.PodIP == "" {
					errMsg := fmt.Sprintf("pod %s/%s ip not initialized", pod.Namespace, pod.Name)
					return nil, util2.PodIPNotFound{Desc: errMsg}
				}
				addr = append(addr, util.Address{Port: int(port.ContainerPort), IPs: []string{pod.Status.PodIP}})
			}
		}
	}
	return addr, nil
}

// Build an ip set group for NSX.
func (service *SecurityPolicyService) buildRuleIPGroup(obj *model.Rule, ips []string) *model.Group {
	ipGroup := model.Group{}

	policyGroupID := fmt.Sprintf("%s_ipset", *obj.Id)
	ipGroup.Id = &policyGroupID
	policyGroupName := fmt.Sprintf("%s-ipset", *obj.DisplayName)
	ipGroup.DisplayName = &policyGroupName

	addresses := data.NewListValue()
	for _, ip := range ips {
		addresses.Add(data.NewStringValue(ip))
	}

	blockExpression := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"resource_type": data.NewStringValue("IPAddressExpression"),
			"ip_addresses":  addresses,
		},
	)
	ipGroup.Expression = append(ipGroup.Expression, blockExpression)
	return &ipGroup
}

// Different direction rule decides different target of the traffic, we should carefully get
// the destination pod selector and namespaces.
func (service *SecurityPolicyService) getPodSelector(obj *v1alpha1.SecurityPolicy,
	rule *v1alpha1.SecurityPolicyRule) (*client.ListOptions, []string, error) {

	// Port means the target of traffic, so we should select the pod by rule direction,
	// as for IN direction, we judge by rule's or policy's AppliedTo,
	// as for OUT direction, then by rule's destinations.
	// LabelSelect may return multiple namespaces
	var namespaces []string
	selector := client.ListOptions{}
	var labelSelector labels.Selector
	ruleDirection, err := getRuleDirection(rule)
	if err != nil {
		return nil, nil, err
	}

	if ruleDirection == "IN" {
		if len(rule.AppliedTo) > 0 {
			for _, target := range rule.AppliedTo {
				// We only consider named port for PodSelector, not VMSelector
				if target.PodSelector != nil {
					labelSelector, err = meta1.LabelSelectorAsSelector(target.PodSelector)
					if err != nil {
						return nil, nil, err
					}
				}
			}
		} else {
			if len(obj.Spec.AppliedTo) > 0 {
				for _, target := range obj.Spec.AppliedTo {
					if target.PodSelector != nil {
						labelSelector, err = meta1.LabelSelectorAsSelector(target.PodSelector)
						if err != nil {
							return nil, nil, err
						}
					}
				}
			}
		}
	} else if ruleDirection == "OUT" {
		if len(rule.Destinations) > 0 {
			for _, target := range rule.Destinations {
				if target.PodSelector != nil {
					labelSelector, err = meta1.LabelSelectorAsSelector(target.PodSelector)
					if err != nil {
						return nil, nil, err
					}
				}
				if target.NamespaceSelector != nil {
					ns, err := service.ResolveNamespace(target.NamespaceSelector)
					if err != nil {
						return nil, nil, err
					}
					for _, nsItem := range ns.Items {
						namespaces = append(namespaces, nsItem.Name)
					}
				}
			}
		}
	}
	if labelSelector == nil {
		return nil, nil, errors.New("no effective options filtered by the rule and security policy")
	}
	selector.LabelSelector = labelSelector
	return &selector, namespaces, nil
}

// ResolveNamespace Get namespace name when the rule has namespace selector.
func (service *SecurityPolicyService) ResolveNamespace(lbs *meta1.LabelSelector) (*v1.NamespaceList, error) {
	ctx := context.Background()
	nsList := &v1.NamespaceList{}
	nsOptions := &client.ListOptions{}
	labelMap, err := meta1.LabelSelectorAsMap(lbs)
	if err != nil {
		return nil, err
	}
	nsOptions.LabelSelector = labels.SelectorFromSet(labelMap)
	err = service.Client.List(ctx, nsList, nsOptions)
	if err != nil {
		return nil, err
	}
	return nsList, err
}
