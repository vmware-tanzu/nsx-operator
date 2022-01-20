package services

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const (
	resourceType               = "resource_type"
	resourceTypeGroup          = "group"
	resourceTypeSecurityPolicy = "securitypolicy"
	resourceTypeRule           = "rule"
)

var (
	pageSize int64 = 10 // TODO consider a appropriate page size
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
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, nil, nil, nil)
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
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, nil, nil, nil)
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
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, nil, nil, nil)
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
