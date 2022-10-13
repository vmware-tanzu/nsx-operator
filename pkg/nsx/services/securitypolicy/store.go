package securitypolicy

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func queryTagCondition(service *SecurityPolicyService) string {
	return fmt.Sprintf("tags.scope:%s AND tags.tag:%s",
		strings.Replace(util.TagScopeCluster, "/", "\\/", -1),
		strings.Replace(service.NSXClient.NsxConfig.Cluster, ":", "\\:", -1))
}

func queryGroup(service *SecurityPolicyService, wg *sync.WaitGroup, fatalErrors chan error) {
	defer wg.Done()
	queryParam := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeGroup) + " AND " + queryTagCondition(service)
	var cursor *string = nil
	pageSize := common.PageSize
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		err = common.TransError(err)
		if _, ok := err.(nsxutil.PageMaxError); ok == true {
			common.DecrementPageSize(&pageSize)
			continue
		}
		if err != nil {
			fatalErrors <- err
		}
		for _, g := range response.Results {
			a, err := Converter.ConvertToGolang(g, model.GroupBindingType())
			if err != nil {
				for _, e := range err {
					fatalErrors <- e
				}
			}
			c, _ := a.(model.Group)
			err2 := service.GroupStore.Add(c)
			if err2 != nil {
				fatalErrors <- err2
			}
		}
		cursor = response.Cursor
		if cursor == nil {
			break
		}
		c, _ := strconv.Atoi(*cursor)
		if int64(c) >= *response.ResultCount {
			break
		}
	}
}

func querySecurityPolicy(service *SecurityPolicyService, wg *sync.WaitGroup, fatalErrors chan error) {
	defer wg.Done()
	queryParam := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeSecurityPolicy) + " AND " + queryTagCondition(service)
	var cursor *string = nil
	pageSize := common.PageSize
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		err = common.TransError(err)
		if _, ok := err.(nsxutil.PageMaxError); ok == true {
			common.DecrementPageSize(&pageSize)
			continue
		}
		if err != nil {
			fatalErrors <- err
		}
		for _, g := range response.Results {
			a, err := Converter.ConvertToGolang(g, model.SecurityPolicyBindingType())
			if err != nil {
				for _, e := range err {
					fatalErrors <- e
				}
			}
			c, _ := a.(model.SecurityPolicy)
			err2 := service.SecurityPolicyStore.Add(c)
			if err2 != nil {
				fatalErrors <- err2
			}
		}
		cursor = response.Cursor
		if cursor == nil {
			break
		}
		c, _ := strconv.Atoi(*cursor)
		if int64(c) >= *response.ResultCount {
			break
		}
	}
}

func queryRule(service *SecurityPolicyService, wg *sync.WaitGroup, fatalErrors chan error) {
	defer wg.Done()
	queryParam := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeRule) + " AND " + queryTagCondition(service)
	var cursor *string = nil
	pageSize := common.PageSize
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		err = common.TransError(err)
		if _, ok := err.(nsxutil.PageMaxError); ok == true {
			common.DecrementPageSize(&pageSize)
			continue
		}
		if err != nil {
			fatalErrors <- err
		}
		for _, g := range response.Results {
			a, err := Converter.ConvertToGolang(g, model.RuleBindingType())
			if err != nil {
				for _, e := range err {
					fatalErrors <- e
				}
			}
			c, _ := a.(model.Rule)
			err2 := service.RuleStore.Add(c)
			if err2 != nil {
				fatalErrors <- err2
			}
		}
		cursor = response.Cursor
		if cursor == nil {
			break
		}
		c, _ := strconv.Atoi(*cursor)
		if int64(c) >= *response.ResultCount {
			break
		}
	}
}

func (service *SecurityPolicyService) OperateSecurityStore(sp *model.SecurityPolicy) error {
	if sp == nil {
		return nil
	}
	if sp.MarkedForDelete != nil && *sp.MarkedForDelete {
		err := service.SecurityPolicyStore.Delete(*sp) // Pass in the object to be deleted, not the pointer
		log.V(1).Info("delete security policy from store", "securitypolicy", sp)
		if err != nil {
			return err
		}
	} else {
		err := service.SecurityPolicyStore.Add(*sp)
		log.V(1).Info("add security policy to store", "securitypolicy", sp)
		if err != nil {
			return err
		}
	}
	return nil
}

func (service *SecurityPolicyService) OperateRuleStore(sp *model.SecurityPolicy) error {
	for _, rule := range sp.Rules {
		if rule.MarkedForDelete != nil && *rule.MarkedForDelete {
			err := service.RuleStore.Delete(rule)
			log.V(1).Info("delete rule from store", "rule", rule)
			if err != nil {
				return err
			}
		} else {
			err := service.RuleStore.Add(rule)
			log.V(1).Info("add rule to store", "rule", rule)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (service *SecurityPolicyService) OperateGroupStore(gs *[]model.Group) error {
	for _, group := range *gs {
		if group.MarkedForDelete != nil && *group.MarkedForDelete {
			err := service.GroupStore.Delete(group)
			log.V(1).Info("delete group from store", "group", group)
			if err != nil {
				return err
			}
		} else {
			err := service.GroupStore.Add(group)
			log.V(1).Info("add group to store", "group", group)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
