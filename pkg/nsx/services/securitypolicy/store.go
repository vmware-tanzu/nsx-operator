package securitypolicy

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	util2 "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func queryTagCondition(service *Service) string {
	return fmt.Sprintf("tags.scope:%s AND tags.tag:%s",
		strings.Replace(util.TagScopeCluster, "/", "\\/", -1),
		strings.Replace(service.NSXClient.NsxConfig.Cluster, ":", "\\:", -1))
}

func queryGroup(service *Service, wg *sync.WaitGroup, fatalErrors chan error) {
	defer wg.Done()
	queryParam := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeGroup) + " AND " + queryTagCondition(service)
	var cursor *string = nil
	pageSize := common.PageSize
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		err = common.TransError(err)
		if _, ok := err.(util2.PageMaxError); ok == true {
			common.DecrementPageSize(&pageSize)
			continue
		}
		if err != nil {
			fatalErrors <- err
		}
		for _, g := range response.Results {
			a, err := common.Converter.ConvertToGolang(g, model.GroupBindingType())
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

func querySecurityPolicy(service *Service, wg *sync.WaitGroup, fatalErrors chan error) {
	defer wg.Done()
	queryParam := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeSecurityPolicy) + " AND " + queryTagCondition(service)
	var cursor *string = nil
	pageSize := common.PageSize
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		err = common.TransError(err)
		if _, ok := err.(util2.PageMaxError); ok == true {
			common.DecrementPageSize(&pageSize)
			continue
		}
		if err != nil {
			fatalErrors <- err
		}
		for _, g := range response.Results {
			a, err := common.Converter.ConvertToGolang(g, model.SecurityPolicyBindingType())
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

func queryRule(service *Service, wg *sync.WaitGroup, fatalErrors chan error) {
	defer wg.Done()
	queryParam := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeRule) + " AND " + queryTagCondition(service)
	var cursor *string = nil
	pageSize := common.PageSize
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		err = common.TransError(err)
		if _, ok := err.(util2.PageMaxError); ok == true {
			common.DecrementPageSize(&pageSize)
			continue
		}
		if err != nil {
			fatalErrors <- err
		}
		for _, g := range response.Results {
			a, err := common.Converter.ConvertToGolang(g, model.RuleBindingType())
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

func getAll(service *Service, obj *v1alpha1.SecurityPolicy,
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

func (service *Service) ListSecurityPolicyID() sets.String {
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

// InitializeSecurityPolicy sync NSX resources
func InitializeSecurityPolicy(service common.Service) (*Service, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(3)
	securityService := &Service{Service: service}
	securityService.GroupStore = cache.NewIndexer(common.KeyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: common.IndexFunc(util.TagScopeSecurityPolicyCRUID)})
	securityService.SecurityPolicyStore = cache.NewIndexer(common.KeyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: common.IndexFunc(util.TagScopeSecurityPolicyCRUID)})
	securityService.RuleStore = cache.NewIndexer(common.KeyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: common.IndexFunc(util.TagScopeSecurityPolicyCRUID)})

	go queryGroup(securityService, &wg, fatalErrors)
	go querySecurityPolicy(securityService, &wg, fatalErrors)
	go queryRule(securityService, &wg, fatalErrors)

	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return securityService, err
	}

	return securityService, nil
}
