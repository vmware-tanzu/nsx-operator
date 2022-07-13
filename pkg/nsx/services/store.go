package services

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
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
		strings.Replace(util.TagScopeCluster, "/", "\\/", -1),
		strings.Replace(service.NSXClient.NsxConfig.Cluster, ":", "\\:", -1))
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
		typeConverter := service.NSXClient.RestConnector.TypeConverter()
		for _, g := range response.Results {
			a, err := typeConverter.ConvertToGolang(g, model.GroupBindingType())
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
		typeConverter := service.NSXClient.RestConnector.TypeConverter()
		for _, g := range response.Results {
			a, err := typeConverter.ConvertToGolang(g, model.SecurityPolicyBindingType())
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
		typeConverter := service.NSXClient.RestConnector.TypeConverter()
		for _, g := range response.Results {
			a, err := typeConverter.ConvertToGolang(g, model.RuleBindingType())
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

func decrementPageSize(pageSize *int64) {
	*pageSize -= 100
	if int(*pageSize) <= 0 {
		*pageSize = 10
	}
}

func transError(err error) error {
	var typeConverter = bindings.NewTypeConverter()
	typeConverter.SetMode(bindings.REST)
	switch err.(type) {
	case errors.ServiceUnavailable:
		vApiError, _ := err.(errors.ServiceUnavailable)
		if vApiError.Data == nil {
			return err
		}
		data, errs := typeConverter.ConvertToGolang(vApiError.Data, model.ApiErrorBindingType())
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

func getAll(service *SecurityPolicyService, obj *v1alpha1.SecurityPolicy,
	nsxSecurityPolicy *model.SecurityPolicy) ([]model.Group, *model.SecurityPolicy, []model.Rule, error) {
	indexResults, err := service.GroupStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(obj.UID))
	if err != nil {
		return nil, nil, nil, err
	}
	var existingGroups []model.Group
	for _, group := range indexResults {
		existingGroups = append(existingGroups, group.(model.Group))
	}
	existingSecurityPolicy := model.SecurityPolicy{}
	res, ok, err := service.SecurityPolicyStore.GetByKey(*nsxSecurityPolicy.Id)
	if err != nil {
		return nil, nil, nil, err
	}
	if ok {
		existingSecurityPolicy = res.(model.SecurityPolicy)
	}
	indexResults, err = service.RuleStore.ByIndex(util.TagScopeSecurityPolicyCRUID, string(obj.UID))
	if err != nil {
		return nil, nil, nil, err
	}
	var existingRules []model.Rule
	for _, rule := range indexResults {
		existingRules = append(existingRules, rule.(model.Rule))
	}
	return existingGroups, &existingSecurityPolicy, existingRules, nil
}

func (service *SecurityPolicyService) ListSecurityPolicyID() sets.String {
	groups := service.GroupStore.ListIndexFuncValues(util.TagScopeSecurityPolicyCRUID)
	groupSet := sets.NewString()
	for _, group := range groups {
		groupSet.Insert(group)
	}
	securityPolicies := service.SecurityPolicyStore.ListIndexFuncValues(util.TagScopeSecurityPolicyCRUID)
	policySet := sets.NewString()
	for _, policy := range securityPolicies {
		policySet.Insert(policy)
	}
	return groupSet.Union(policySet)
}
