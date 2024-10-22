package securitypolicy

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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// When a rule contains named port, we should consider whether the rule should be expanded to
// multiple rules if the port name maps to conflicted port numbers.
func (service *SecurityPolicyService) expandRule(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	ruleIdx int, createdFor string,
) ([]*model.Group, []*model.Rule, error) {
	if len(rule.Ports) == 0 {
		nsxRule, err := service.buildRuleBasicInfo(obj, rule, ruleIdx, createdFor, nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, []*model.Rule{nsxRule}, nil
	}

	// Check if there is a namedport in the rule
	hasNamedPort := service.hasNamedPort(rule)
	if !hasNamedPort {
		nsxRule, err := service.buildRuleBasicInfo(obj, rule, ruleIdx, createdFor, nil)
		if err != nil {
			return nil, nil, err
		}
		var ruleServiceEntries []*data.StructValue
		for _, port := range rule.Ports {
			serviceEntry := buildRuleServiceEntries(port)
			ruleServiceEntries = append(ruleServiceEntries, serviceEntry)
		}
		nsxRule.ServiceEntries = ruleServiceEntries
		return nil, []*model.Rule{nsxRule}, nil
	}

	var nsxRules []*model.Rule
	// nsxGroups is a slice for the IPSet groups referred by a security Rule if named port is configured.
	var nsxGroups []*model.Group
	for portIdx, port := range rule.Ports {
		nsxGroups2, nsxRules2, err := service.expandRuleByPort(obj, rule, ruleIdx, port, portIdx, createdFor)
		if err != nil {
			return nil, nil, err
		}
		nsxGroups = append(nsxGroups, nsxGroups2...)
		nsxRules = append(nsxRules, nsxRules2...)
	}

	return nsxGroups, nsxRules, nil
}

func (service *SecurityPolicyService) expandRuleByPort(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	ruleIdx int, port v1alpha1.SecurityPolicyPort, portIdx int, createdFor string,
) ([]*model.Group, []*model.Rule, error) {
	var portInfos []*portInfo

	// Use PortAddress to handle normal port and named port, if it only contains int value Port,
	// then it is a normal port. If it contains a list of IPs, it is a named port.
	if port.Port.Type == intstr.Int {
		portInfo := newPortInfo(port)
		portInfo.idSuffix = fmt.Sprintf("%d%s0", portIdx, common.ConnectorUnderline)
		portInfos = append(portInfos, portInfo)
	} else {
		// endPort can only be defined if port is also defined. Both ports must be numeric.
		if port.EndPort != 0 {
			return nil, nil, nsxutil.RestrictionError{Desc: "endPort can only be defined if port is also numeric."}
		}
		startPort, err := service.resolveNamedPort(obj, rule, port)
		if err != nil {
			// In case there is no more valid ip set selected, so clear the stale ip set group in NSX if stale ips exist
			if errors.As(err, &nsxutil.NoEffectiveOption{}) {
				groups := service.groupStore.GetByIndex(common.TagScopeRuleID, service.buildRuleID(obj, ruleIdx, createdFor))
				var ipSetGroup *model.Group
				for _, group := range groups {
					ipSetGroup = group
					// Clear ip set group in NSX
					ipSetGroup.Expression = nil
					log.V(1).Info("clear ruleIPSetGroup", "ruleIPSetGroup", ipSetGroup)
					err3 := service.createOrUpdateGroups(obj, []*model.Group{ipSetGroup})
					if err3 != nil {
						return nil, nil, err3
					}
				}
			}
			return nil, nil, err
		}

		for addrIdx, portAddr := range startPort {
			portInfo := newPortInfoForNamedPort(portAddr, port.Protocol)
			portInfo.idSuffix = fmt.Sprintf("%d%s%d", portIdx, common.ConnectorUnderline, addrIdx)
			portInfos = append(portInfos, portInfo)
		}
	}

	var nsxGroups []*model.Group
	var nsxRules []*model.Rule
	for _, portInfo := range portInfos {
		gs, r, err := service.expandRuleByService(obj, rule, ruleIdx, createdFor, portInfo)
		if err != nil {
			return nil, nil, err
		}
		nsxRules = append(nsxRules, r)
		nsxGroups = append(nsxGroups, gs...)
	}
	return nsxGroups, nsxRules, nil
}

func (service *SecurityPolicyService) expandRuleByService(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleIdx int,
	createdFor string, namedPort *portInfo,
) ([]*model.Group, *model.Rule, error) {
	var nsxGroups []*model.Group

	nsxRule, err := service.buildRuleBasicInfo(obj, rule, ruleIdx, createdFor, namedPort)
	if err != nil {
		return nil, nil, err
	}

	var ruleServiceEntries []*data.StructValue
	serviceEntry := buildRuleServiceEntries(namedPort.port)
	ruleServiceEntries = append(ruleServiceEntries, serviceEntry)
	nsxRule.ServiceEntries = ruleServiceEntries

	// If portAddress contains a list of IPs, we should build an ip set group for the rule.
	if len(namedPort.ips) > 0 {
		ruleIPSetGroup := service.buildRuleIPSetGroup(obj, rule, nsxRule, namedPort.ips, ruleIdx, createdFor)

		// In VPC network, NSGroup with IPAddressExpression type can be supported in VPC level as well.
		IPSetGroupPath, err := service.buildRuleIPSetGroupPath(obj, nsxRule)
		if err != nil {
			return nil, nil, err
		}
		nsxRule.DestinationGroups = []string{IPSetGroupPath}
		log.V(1).Info("built ruleIPSetGroup", "ruleIPSetGroup", ruleIPSetGroup)
		nsxGroups = append(nsxGroups, ruleIPSetGroup)
	}
	log.V(1).Info("built rule by service entry", "nsxRule", nsxRule)
	return nsxGroups, nsxRule, nil
}

// Resolve a named port to port number by rule and policy selector.
// e.g. "http" -> [{"80":['1.1.1.1', '2.2.2.2']}, {"443":['3.3.3.3']}]
func (service *SecurityPolicyService) resolveNamedPort(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule,
	spPort v1alpha1.SecurityPolicyPort,
) ([]nsxutil.PortAddress, error) {
	var portAddress []nsxutil.PortAddress

	podSelectors, err := service.getPodSelectors(obj, rule)
	if err != nil {
		return nil, err
	}

	for _, selector := range podSelectors {
		podSelector := selector
		podsList := &v1.PodList{}
		log.V(2).Info("port", "podSelector", podSelector)
		err := service.Client.List(context.Background(), podsList, &podSelector)
		if err != nil {
			return nil, err
		}
		for _, pod := range podsList.Items {
			addr := service.resolvePodPort(pod, &spPort)
			portAddress = append(portAddress, addr...)
		}
	}

	if len(portAddress) == 0 {
		log.Info("no pod has the corresponding named port", "port", spPort)
	}
	return nsxutil.MergeAddressByPort(portAddress), nil
}

// Check port name and protocol, only when the pod is really running, and it does have effective ip.
func (service *SecurityPolicyService) resolvePodPort(pod v1.Pod, spPort *v1alpha1.SecurityPolicyPort) []nsxutil.PortAddress {
	var addr []nsxutil.PortAddress
	for _, c := range pod.Spec.Containers {
		container := c
		for _, port := range container.Ports {
			log.V(2).Info("resolvePodPort", "namespace", pod.Namespace, "podName", pod.Name,
				"portName", port.Name, "containerPort", port.ContainerPort,
				"protocol", port.Protocol, "podIP", pod.Status.PodIP)
			if port.Name == spPort.Port.String() && port.Protocol == spPort.Protocol {
				if pod.Status.Phase != "Running" {
					log.Info("pod with named port is not running", "pod.Namespace", pod.Namespace, "pod.Name", pod.Name)
					return addr
				}
				if pod.Status.PodIP == "" {
					log.Info("pod with named port doesn't have initialized IP", "pod.Namespace", pod.Namespace, "pod.Name", pod.Name)
					return addr
				}
				addr = append(
					addr,
					nsxutil.PortAddress{Port: int(port.ContainerPort), IPs: []string{pod.Status.PodIP}},
				)
			}
		}
	}
	return addr
}

func (service *SecurityPolicyService) buildRuleIPSetGroupID(ruleModel *model.Rule) string {
	return util.GenerateID(*ruleModel.Id, "", common.IpSetGroupSuffix, "")
}

func (service *SecurityPolicyService) buildRuleIPSetGroupName(ruleModel *model.Rule) string {
	return util.GenerateTruncName(common.MaxNameLength, *ruleModel.DisplayName, "", common.IpSetGroupSuffix, "", "")
}

func (service *SecurityPolicyService) buildRuleIPSetGroupPath(obj *v1alpha1.SecurityPolicy, ruleModel *model.Rule) (string, error) {
	ipSetGroupID := service.buildRuleIPSetGroupID(ruleModel)

	if IsVPCEnabled(service) {
		vpcInfo, err := service.getVPCInfo(obj.ObjectMeta.Namespace)
		if err != nil {
			return "", err
		}
		orgID := (*vpcInfo).OrgID
		projectID := (*vpcInfo).ProjectID
		vpcID := (*vpcInfo).VPCID
		return fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s/groups/%s", orgID, projectID, vpcID, ipSetGroupID), nil
	}

	return fmt.Sprintf("/infra/domains/%s/groups/%s", getDomain(service), ipSetGroupID), nil
}

// Build an ip set group for NSX.
func (service *SecurityPolicyService) buildRuleIPSetGroup(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule, ruleModel *model.Rule,
	ips []string, ruleIdx int, createdFor string,
) *model.Group {
	ipSetGroup := model.Group{}

	ipSetGroupID := service.buildRuleIPSetGroupID(ruleModel)
	ipSetGroup.Id = &ipSetGroupID
	ipSetGroupName := service.buildRuleIPSetGroupName(ruleModel)
	ipSetGroup.DisplayName = &ipSetGroupName

	// IPSetGroup is always destination group for named port
	peerTags := service.buildPeerTags(obj, rule, ruleIdx, false, false, false, createdFor)
	ipSetGroup.Tags = peerTags

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
	ipSetGroup.Expression = append(ipSetGroup.Expression, blockExpression)
	return &ipSetGroup
}

// Different direction rule decides different target of the traffic, we should carefully get
// the destination pod selector and namespaces. Named port only cares about the destination
// pod selector, so we use policy's AppliedTo or rule's AppliedTo when "IN" direction and
// rule's DestinationSelector when "OUT" direction.
func (service *SecurityPolicyService) getPodSelectors(obj *v1alpha1.SecurityPolicy, rule *v1alpha1.SecurityPolicyRule) ([]client.ListOptions, error) {
	// Port means the target of traffic, so we should select the pod by rule direction,
	// as for IN direction, we judge by rule's or policy's AppliedTo,
	// as for OUT direction, then by rule's destinations.
	// LabelSelect may return multiple namespaces
	var finalSelectors []client.ListOptions
	ruleDirection, err := getRuleDirection(rule)
	if err != nil {
		return nil, err
	}

	if ruleDirection == "IN" {
		if len(obj.Spec.AppliedTo) > 0 {
			for _, target := range obj.Spec.AppliedTo {
				selector := client.ListOptions{}
				if target.PodSelector != nil {
					label, err := meta1.LabelSelectorAsSelector(target.PodSelector)
					if err != nil {
						return nil, err
					}
					selector.LabelSelector = label
					selector.Namespace = obj.Namespace
					finalSelectors = append(finalSelectors, selector)
				}
			}
		} else if len(rule.AppliedTo) > 0 {
			for _, target := range rule.AppliedTo {
				// We only consider named port for PodSelector, not VMSelector
				selector := client.ListOptions{}
				if target.PodSelector != nil {
					label, err := meta1.LabelSelectorAsSelector(target.PodSelector)
					if err != nil {
						return nil, err
					}
					selector.LabelSelector = label
					selector.Namespace = obj.Namespace
					finalSelectors = append(finalSelectors, selector)
				}
			}
		}
	} else if ruleDirection == "OUT" {
		if len(rule.Destinations) > 0 {
			for _, target := range rule.Destinations {
				var namespaceSelectors []client.ListOptions // ResolveNamespace may return multiple namespaces
				var labelSelector client.ListOptions
				var namespaceSelector client.ListOptions
				if target.PodSelector != nil {
					label, err := meta1.LabelSelectorAsSelector(target.PodSelector)
					if err != nil {
						return nil, err
					}
					labelSelector.LabelSelector = label
				}
				if target.NamespaceSelector != nil {
					ns, err := service.ResolveNamespace(target.NamespaceSelector)
					if err != nil {
						return nil, err
					}
					for _, nsItem := range ns.Items {
						namespaceSelector.Namespace = nsItem.Name
						namespaceSelectors = append(namespaceSelectors, namespaceSelector)
					}
				} else {
					namespaceSelector.Namespace = obj.Namespace
					namespaceSelectors = append(namespaceSelectors, namespaceSelector)
				}
				// calculate the union of labelSelector and namespaceSelectors
				for _, nsSelector := range namespaceSelectors {
					if labelSelector.LabelSelector != nil {
						finalSelectors = append(finalSelectors, client.ListOptions{
							LabelSelector: labelSelector.LabelSelector,
							Namespace:     nsSelector.Namespace,
						})
					} else {
						finalSelectors = append(finalSelectors, client.ListOptions{
							Namespace: nsSelector.Namespace,
						})
					}
				}
			}
		}
	}
	if len(finalSelectors) == 0 {
		return nil, nsxutil.NoEffectiveOption{
			Desc: "no effective options filtered by the rule and security policy",
		}
	}
	return finalSelectors, nil
}

func (service *SecurityPolicyService) hasNamedPort(rule *v1alpha1.SecurityPolicyRule) bool {
	hasNamedPort := false
	for _, port := range rule.Ports {
		if port.Port.Type == intstr.String {
			hasNamedPort = true
			break
		}
	}
	return hasNamedPort
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
