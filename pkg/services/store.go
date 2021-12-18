package services

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers"
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
	v, _ := obj.(model.SecurityPolicy)
	for _, tag := range v.Tags {
		if *tag.Scope == util.TagScopeSecurityPolicyCRUID {
			res = append(res, *tag.Tag)
		}
	}
	return res, nil
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
		return *v.UniqueId, nil
	case model.SecurityPolicy:
		return *v.UniqueId, nil
	case model.Rule:
		return *v.UniqueId, nil
	}
	return "", nil
}

func queryTagCondition(r *controllers.SecurityPolicyReconciler) string {
	return fmt.Sprintf("tags.scope:%s AND tags.tag:%s",
		strings.Replace(util.TagScopeCluster, "/", "\\/", -1), r.NSXClient.NsxConfig.Cluster)
}

func queryGroup(r *controllers.SecurityPolicyReconciler, wg *sync.WaitGroup, fatalErrors chan error) {
	defer wg.Done()
	queryParam := fmt.Sprintf("%s:%s", resourceType, resourceTypeGroup) + " AND " + queryTagCondition(r)
	var cursor *string = nil
	for {
		response, err := r.NSXClient.QueryClient.List(queryParam, cursor, nil, nil, nil, nil)
		if err != nil {
			fatalErrors <- err
		}
		typeConverter := r.NSXClient.RestConnector.TypeConverter()
		for _, g := range response.Results {
			a, err := typeConverter.ConvertToGolang(g, model.GroupBindingType())
			if err != nil {
				for _, e := range err {
					fatalErrors <- e
				}
			}
			c, _ := a.(model.Group)
			r.GroupStore.Add(c)
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

func querySecurityPolicy(r *controllers.SecurityPolicyReconciler, wg *sync.WaitGroup, fatalErrors chan error) {
	defer wg.Done()
	queryParam := fmt.Sprintf("%s:%s", resourceType, resourceTypeSecurityPolicy) + " AND " + queryTagCondition(r)
	var cursor *string = nil
	for {
		response, err := r.NSXClient.QueryClient.List(queryParam, cursor, nil, nil, nil, nil)
		if err != nil {
			fatalErrors <- err
		}
		typeConverter := r.NSXClient.RestConnector.TypeConverter()
		for _, g := range response.Results {
			a, err := typeConverter.ConvertToGolang(g, model.SecurityPolicyBindingType())
			if err != nil {
				for _, e := range err {
					fatalErrors <- e
				}
			}
			c, _ := a.(model.SecurityPolicy)
			r.SecurityPolicyStore.Add(c)
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

func queryRule(r *controllers.SecurityPolicyReconciler, wg *sync.WaitGroup, fatalErrors chan error) {
	defer wg.Done()
	queryParam := fmt.Sprintf("%s:%s", resourceType, resourceTypeRule) + " AND " + queryTagCondition(r)
	var cursor *string = nil
	for {
		response, err := r.NSXClient.QueryClient.List(queryParam, cursor, nil, nil, nil, nil)
		if err != nil {
			fatalErrors <- err
		}
		typeConverter := r.NSXClient.RestConnector.TypeConverter()
		for _, g := range response.Results {
			a, err := typeConverter.ConvertToGolang(g, model.RuleBindingType())
			if err != nil {
				for _, e := range err {
					fatalErrors <- e
				}
			}
			c, _ := a.(model.Rule)
			r.RuleStore.Add(c)
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
