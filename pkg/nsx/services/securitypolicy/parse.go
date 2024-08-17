package securitypolicy

import (
	"errors"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var validRuleActions = []string{
	util.ToUpper(v1alpha1.RuleActionAllow),
	util.ToUpper(v1alpha1.RuleActionDrop),
	util.ToUpper(v1alpha1.RuleActionReject),
}

var (
	ruleDirectionIngress = util.ToUpper(v1alpha1.RuleDirectionIngress)
	ruleDirectionIn      = util.ToUpper(v1alpha1.RuleDirectionIn)
	ruleDirectionEgress  = util.ToUpper(v1alpha1.RuleDirectionEgress)
	ruleDirectionOut     = util.ToUpper(v1alpha1.RuleDirectionOut)
)

func getRuleAction(rule *v1alpha1.SecurityPolicyRule) (string, error) {
	ruleAction := util.ToUpper(*rule.Action)
	for _, validRuleAction := range validRuleActions {
		if ruleAction == validRuleAction {
			return ruleAction, nil
		}
	}
	return "", errors.New("invalid rule action")
}

func getRuleDirection(rule *v1alpha1.SecurityPolicyRule) (string, error) {
	ruleDirection := util.ToUpper(*rule.Direction)
	if ruleDirection == ruleDirectionIngress || ruleDirection == ruleDirectionIn {
		return "IN", nil
	} else if ruleDirection == ruleDirectionEgress || ruleDirection == ruleDirectionOut {
		return "OUT", nil
	}
	return "", errors.New("invalid rule direction")
}

func getCluster(service *SecurityPolicyService) string {
	return service.NSXConfig.Cluster
}

func getDomain(service *SecurityPolicyService) string {
	return getCluster(service)
}

func getVpcProjectDomain() string {
	return "default"
}

func isVpcEnabled(service *SecurityPolicyService) bool {
	return service.NSXConfig.EnableVPCNetwork
}

func getScopeCluserTag(service *SecurityPolicyService) string {
	if isVpcEnabled(service) {
		return common.TagScopeCluster
	} else {
		return common.TagScopeNCPCluster
	}
}

func getScopePodTag(service *SecurityPolicyService) string {
	if isVpcEnabled(service) {
		return common.TagScopePodUID
	} else {
		return common.TagScopeNCPPod
	}
}

func getScopeVMInterfaceTag(service *SecurityPolicyService) string {
	if isVpcEnabled(service) {
		return common.TagScopeSubnetPortCRUID
	} else {
		return common.TagScopeNCPVNETInterface
	}
}

func getScopeNamespaceUIDTag(service *SecurityPolicyService, isVMNameSpace bool) string {
	if isVpcEnabled(service) {
		if isVMNameSpace {
			return common.TagScopeVMNamespaceUID
		} else {
			return common.TagScopeNamespaceUID
		}
	} else {
		if isVMNameSpace {
			return common.TagScopeNCPVIFProjectUID
		} else {
			return common.TagScopeNCPProjectUID
		}
	}
}
