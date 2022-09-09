package services

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	util2 "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const (
	resourceType               = "resource_type"
	resourceTypeGroup          = "group"
	resourceTypeSecurityPolicy = "securitypolicy"
	resourceTypeRule           = "rule"
)

var (
	PageSize int64 = 1000
)

func securityPolicyCRUIDScopeIndexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch v := obj.(type) {
	case model.SecurityPolicy:
		res = appendTag(v.Tags, res)
	case model.Group:
		res = appendTag(v.Tags, res)
	case model.Rule:
		res = appendTag(v.Tags, res)
	default:
		break
	}
	return res, nil
}

func appendTag(v []model.Tag, res []string) []string {
	for _, tag := range v {
		if *tag.Scope == util.TagScopeSecurityPolicyCRUID {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

func namespaceIndexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	v, _ := obj.(model.Group)
	for _, tag := range v.Tags {
		if *tag.Scope == util.TagScopeNamespace {
			res = append(res, *tag.Tag)
		}
	}
	return res, nil
}

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case model.Group:
		return *v.Id, nil
	case model.SecurityPolicy:
		return *v.Id, nil
	case model.Rule:
		return *v.Id, nil
	}
	return "", nil
}

func queryTagCondition(service *SecurityPolicyService) string {
	return fmt.Sprintf("tags.scope:%s AND tags.tag:%s",
		strings.Replace(util.TagScopeCluster, "/", "\\/", -1), strings.Replace(service.NSXClient.NsxConfig.Cluster, ":", "\\:", -1))
}

func queryGroup(service *SecurityPolicyService, wg *sync.WaitGroup, fatalErrors chan error) {
	defer wg.Done()
	queryParam := fmt.Sprintf("%s:%s", resourceType, resourceTypeGroup) + " AND " + queryTagCondition(service)
	var cursor *string = nil
	pageSize := PageSize
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		err = transError(err)
		if _, ok := err.(util2.PageMaxError); ok == true {
			decrementPageSize(&pageSize)
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
			service.GroupStore.Add(c)
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
	queryParam := fmt.Sprintf("%s:%s", resourceType, resourceTypeSecurityPolicy) + " AND " + queryTagCondition(service)
	var cursor *string = nil
	pageSize := PageSize
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		err = transError(err)
		if _, ok := err.(util2.PageMaxError); ok == true {
			decrementPageSize(&pageSize)
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
			service.SecurityPolicyStore.Add(c)
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
	queryParam := fmt.Sprintf("%s:%s", resourceType, resourceTypeRule) + " AND " + queryTagCondition(service)
	var cursor *string = nil
	pageSize := PageSize
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		err = transError(err)
		if _, ok := err.(util2.PageMaxError); ok == true {
			decrementPageSize(&pageSize)
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
			service.RuleStore.Add(c)
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

func decrementPageSize(pageSize *int64) {
	*pageSize -= 100
	if int(*pageSize) <= 0 {
		*pageSize = 10
	}
}

func transError(err error) error {
	switch err.(type) {
	case errors.ServiceUnavailable:
		vapiError, _ := err.(errors.ServiceUnavailable)
		if vapiError.Data == nil {
			return err
		}
		data, errs := Converter.ConvertToGolang(vapiError.Data, model.ApiErrorBindingType())
		if len(errs) > 0 {
			return err
		}
		apiError := data.(model.ApiError)
		if *apiError.ErrorCode == int64(60576) {
			return util2.PageMaxError{Desc: "page max overflow"}
		}
	default:
		return err
	}
	return err
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

// InitializeSecurityPolicy sync NSX resources
func InitializeSecurityPolicy(NSXClient *nsx.Client, cf *config.NSXOperatorConfig) (*SecurityPolicyService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(3)
	service := &SecurityPolicyService{NSXClient: NSXClient}
	service.GroupStore = cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeNamespace: namespaceIndexFunc, util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc})
	service.SecurityPolicyStore = cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc})
	service.RuleStore = cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc})
	service.NSXConfig = cf

	go queryGroup(service, &wg, fatalErrors)
	go querySecurityPolicy(service, &wg, fatalErrors)
	go queryRule(service, &wg, fatalErrors)
	go func() {

		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return service, err
	}

	return service, nil
}
