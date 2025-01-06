package common

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

const (
	IndexByVPCPathFuncKey = "indexedByVPCPath"
)

var pageSize = int64(1000)

// Store is the interface for store, it should be implemented by subclass
type Store interface {
	// TransResourceToStore is the method to transform the resource of type data.StructValue
	// to specific nsx-t side resource and then add it to the store.
	TransResourceToStore(obj *data.StructValue) error
	// ListIndexFuncValues is the method to list all the values of the index
	ListIndexFuncValues(key string) sets.Set[string]
	// Apply is the method to create, update and delete the resource to the store based
	// on its tag MarkedForDelete.
	Apply(obj interface{}) error
	// IsPolicyAPI returns if it is Policy resource
	IsPolicyAPI() bool
}

// ResourceStore is the store for resource, embed it to subclass
type ResourceStore struct {
	cache.Indexer        // the ultimate place to store the resource
	bindings.BindingType // used by converter to convert the resource
}

// TransResourceToStore is the method to transform the resource of type data.StructValue
// subclass could reuse it, distinguish the resource by bindingType and resourceAssertion
func (resourceStore *ResourceStore) TransResourceToStore(entity *data.StructValue) error {
	obj, err := NewConverter().ConvertToGolang(entity, resourceStore.BindingType)
	if err != nil {
		for _, e := range err {
			return e
		}
	}
	objAddr := nsxutil.CasttoPointer(obj)
	if objAddr == nil {
		return fmt.Errorf("Failed to cast to pointer")
	}
	err2 := resourceStore.Add(objAddr)
	if err2 != nil {
		return err2
	}
	return nil
}

func DecrementPageSize(pageSize *int64) {
	*pageSize -= 100
	if int(*pageSize) <= 0 {
		*pageSize = 10
	}
}

func (resourceStore *ResourceStore) ListIndexFuncValues(key string) sets.Set[string] {
	values := sets.New[string]()
	entities := resourceStore.Indexer.ListIndexFuncValues(key)
	for _, entity := range entities {
		values.Insert(entity)
	}
	return values
}

// GetByKey is the method to get the resource by key, it is used by the subclass
// to convert it to the specific type.
func (resourceStore *ResourceStore) GetByKey(key string) interface{} {
	res, exists, err := resourceStore.Indexer.GetByKey(key)
	if err != nil {
		log.Error(err, "Failed to get obj by key", "key", key)
	} else if exists {
		return res
	}
	return nil
}

// GetByIndex is the method to get the resource list by index, it is used by the subclass
// to convert it to the specific type.
func (resourceStore *ResourceStore) GetByIndex(index string, value string) []interface{} {
	indexResults, err := resourceStore.Indexer.ByIndex(index, value)
	if err != nil {
		log.Error(err, "Failed to get obj by index", "index", value)
	}
	return indexResults
}

func (resourceStore *ResourceStore) IsPolicyAPI() bool {
	return true
}

func TransError(err error) error {
	apierror, errortype := nsxutil.DumpAPIError(err)
	if apierror != nil {
		log.Info("Translate error", "apierror", apierror, "error type", errortype)
		if *errortype == apierrors.ErrorType_SERVICE_UNAVAILABLE && *apierror.ErrorCode == int64(60576) ||
			*errortype == apierrors.ErrorType_INVALID_REQUEST && *apierror.ErrorCode == int64(255) {
			return nsxutil.PageMaxError{Desc: "page max overflow"}
		}
	}
	return err
}

// InitializeResourceStore is the method to query all the various resources from nsx-t side and
// save them to the store, we could use it to cache all the resources when process starts.
func (service *Service) InitializeResourceStore(wg *sync.WaitGroup, fatalErrors chan error, resourceTypeValue string, tags []model.Tag, store Store) {
	service.InitializeCommonStore(wg, fatalErrors, "", "", resourceTypeValue, tags, store)
}

// InitializeVPCResourceStore is the method to query all the various VPC resources from nsx-t side and
// save them to the store, we could use it to cache all the resources when process starts.
func (service *Service) InitializeVPCResourceStore(wg *sync.WaitGroup, fatalErrors chan error, org string, project string, resourceTypeValue string, tags []model.Tag, store Store) {
	service.InitializeCommonStore(wg, fatalErrors, org, project, resourceTypeValue, tags, store)
}

type Filter func(interface{}) *data.StructValue

func (service *Service) SearchResource(resourceTypeValue string, queryParam string, store Store, filter Filter) (uint64, error) {
	// TODO: resourceTypeValue is not used in this function, but cannot be deleted, as the `fakeSearchResource` use the parameter
	var cursor *string
	count := uint64(0)
	for {
		var err error
		var results []*data.StructValue
		var resultCount *int64
		if store.IsPolicyAPI() {
			response, searchErr := service.NSXClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
			results = response.Results
			cursor = response.Cursor
			resultCount = response.ResultCount
			err = searchErr
		} else {
			response, searchErr := service.NSXClient.MPQueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
			results = response.Results
			cursor = response.Cursor
			resultCount = response.ResultCount
			err = searchErr
		}
		if err != nil {
			err = TransError(err)
			if _, ok := err.(nsxutil.PageMaxError); ok == true {
				DecrementPageSize(&pageSize)
				continue
			}
			return count, err
		}
		for _, entity := range results {
			if filter != nil {
				entity = filter(entity)
			}
			err = store.TransResourceToStore(entity)
			if err != nil {
				return count, err
			}
			count++
		}
		if cursor == nil {
			break
		}
		c, _ := strconv.Atoi(*cursor)
		if int64(c) >= *resultCount {
			break
		}
	}
	return count, nil
}

// PopulateResourcetoStore is the method used by populating resources created not by nsx-operator
func (service *Service) PopulateResourcetoStore(wg *sync.WaitGroup, fatalErrors chan error, resourceTypeValue string, queryParam string, store Store, filter Filter) {
	defer wg.Done()
	count, err := service.SearchResource("", queryParam, store, filter)
	if err != nil {
		fatalErrors <- err
	}
	log.Info("Initialized store", "resourceType", resourceTypeValue, "count", count)
}

// InitializeCommonStore is the common method used by InitializeResourceStore and InitializeVPCResourceStore
func (service *Service) InitializeCommonStore(wg *sync.WaitGroup, fatalErrors chan error, org string, project string, resourceTypeValue string, tags []model.Tag, store Store) {
	var tagParams []string
	// Check for specific tag scopes
	if !containsTagScope(tags, TagScopeCluster, TagScopeNCPCluster) {
		tagParams = append(tagParams, formatTagParamScope("tags.scope", TagScopeCluster))
		tagParams = append(tagParams, formatTagParamTag("tags.tag", service.NSXClient.NsxConfig.Cluster))
	}

	for _, tag := range tags {
		if tag.Scope != nil {
			tagParams = append(tagParams, formatTagParamScope("tags.scope", *tag.Scope))
			if tag.Tag != nil {
				tagParams = append(tagParams, formatTagParamTag("tags.tag", *tag.Tag))
			}
		}
	}

	// Join all tag parameters with "AND"
	tagParam := strings.Join(tagParams, " AND ")

	resourceParam := fmt.Sprintf("%s:%s", ResourceType, resourceTypeValue)
	queryParam := resourceParam + " AND " + tagParam

	if org != "" || project != "" {
		// QueryClient.List() will escape the path, "path:" then will be "path%25%3A" instead of "path:3A",
		// "path%25%3A" would fail to get response. Hack it here.
		path := "\\/orgs\\/" + org + "\\/projects\\/" + project + "\\/*"
		pathUnescape, _ := url.PathUnescape("path%3A")
		queryParam += " AND " + pathUnescape + path
	}
	if store.IsPolicyAPI() {
		queryParam += " AND marked_for_delete:false"
	}
	service.PopulateResourcetoStore(wg, fatalErrors, resourceTypeValue, queryParam, store, nil)
}

// Helper function to check if any tag has the specified scopes
func containsTagScope(tags []model.Tag, scopes ...string) bool {
	for _, tag := range tags {
		for _, scope := range scopes {
			if tag.Scope != nil && *tag.Scope == scope {
				return true
			}
		}
	}
	return false
}

// Helper function to format tag parameters
func formatTagParamScope(paramType, value string) string {
	valueEscaped := strings.Replace(value, "/", "\\/", -1)
	return fmt.Sprintf("%s:%s", paramType, valueEscaped)
}

func formatTagParamTag(paramType, value string) string {
	valueEscaped := strings.Replace(value, ":", "\\:", -1)
	return fmt.Sprintf("%s:%s", paramType, valueEscaped)
}

func IndexByVPCFunc(obj interface{}) ([]string, error) {
	switch v := obj.(type) {
	case *model.Vpc:
		return getVPCPathFromResourcePath(v.Path)
	case *model.VpcSubnet:
		return getVPCPathFromParentPath(v.ParentPath)
	case *model.VpcSubnetPort:
		return getVPCPathFromResourcePath(v.Path)
	case *model.SubnetConnectionBindingMap:
		return getVPCPathFromResourcePath(v.Path)
	case *model.VpcIpAddressAllocation:
		return getVPCPathFromParentPath(v.ParentPath)
	case *model.StaticRoutes:
		return getVPCPathFromParentPath(v.ParentPath)
	case *model.LBService:
		return getVPCPathFromParentPath(v.ParentPath)
	case *model.LBVirtualServer:
		return getVPCPathFromParentPath(v.ParentPath)
	case *model.LBPool:
		return getVPCPathFromParentPath(v.ParentPath)
	case *model.SecurityPolicy:
		return getVPCPathFromParentPath(v.ParentPath)
	case *model.Group:
		return getVPCPathFromParentPath(v.ParentPath)
	case *model.Rule:
		return getVPCPathFromResourcePath(v.Path)

	default:
		return []string{}, errors.New("indexFunc doesn't support unknown type")
	}
}
func (service *Service) QueryNCPCreatedResources(resourceTypes []string, store Store, additionalQueryFn func(query string) string) error {
	resQuery := make([]string, 0)
	for _, rt := range resourceTypes {
		resQuery = append(resQuery, fmt.Sprintf("%s:%s", ResourceType, rt))
	}

	var query string
	if len(resQuery) == 1 {
		query = resQuery[0]
	} else {
		query = fmt.Sprintf("(%s)", strings.Join(resQuery, " OR "))
	}

	query = service.AddNCPClusterTag(query)
	if additionalQueryFn != nil {
		query = additionalQueryFn(query)
	}
	count, searchErr := service.SearchResource("", query, store, nil)
	if searchErr != nil {
		log.Error(searchErr, "Failed to query resources", "query", query)
		return searchErr
	}
	log.V(1).Info("Queried resources", "count", count)
	return nil
}

func (service *Service) AddNCPClusterTag(query string) string {
	tagScopeClusterKey := strings.Replace(TagScopeNCPCluster, "/", "\\/", -1)
	tagScopeClusterValue := strings.Replace(service.NSXClient.NsxConfig.Cluster, ":", "\\:", -1)
	tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagScopeClusterKey, tagScopeClusterValue)
	return query + " AND " + tagParam
}

func AddNCPCreatedForTag(query string, createdFor string) string {
	tagScopeClusterKey := strings.Replace(TagScopeNCPCreateFor, "/", "\\/", -1)
	tagScopeClusterValue := strings.Replace(createdFor, ":", "\\:", -1)
	tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagScopeClusterKey, tagScopeClusterValue)
	return query + " AND " + tagParam
}

func getVPCPathFromParentPath(parentPath *string) ([]string, error) {
	if parentPath == nil {
		return []string{}, errors.New("NSX resource does not set ParentPath field")
	}
	return []string{*parentPath}, nil
}

func getVPCPathFromResourcePath(path *string) ([]string, error) {
	if path == nil {
		return []string{}, errors.New("NSX resource does not set Path field")
	}
	resInfo, err := ParseVPCResourcePath(*path)
	if err != nil {
		return []string{}, err
	}
	return []string{resInfo.GetVPCPath()}, nil
}
