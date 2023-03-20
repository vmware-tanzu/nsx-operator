package common

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

const (
	PageSize int64 = 1000
)

// Store is the interface for store, it should be implemented by subclass
type Store interface {
	// TransResourceToStore is the method to transform the resource of type data.StructValue
	// to specific nsx-t side resource and then add it to the store.
	TransResourceToStore(obj *data.StructValue) error
	// ListIndexFuncValues is the method to list all the values of the index
	ListIndexFuncValues(key string) sets.String
	// Operate is the method to create, update and delete the resource to the store based
	// on its tag MarkedForDelete.
	Operate(obj interface{}) error
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
	err2 := resourceStore.Add(obj)
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

func (resourceStore *ResourceStore) ListIndexFuncValues(key string) sets.String {
	values := sets.NewString()
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
		log.Error(err, "failed to get obj by key", "key", key)
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
		log.Error(err, "failed to get obj by index", "index", value)
	}
	return indexResults
}

func (resourceStore *ResourceStore) IsPolicyAPI() bool {
	return true
}

func TransError(err error) error {
	switch err.(type) {
	case vapierrors.ServiceUnavailable:
		vApiError, _ := err.(vapierrors.ServiceUnavailable)
		if vApiError.Data == nil {
			return err
		}
		dataError, errs := NewConverter().ConvertToGolang(vApiError.Data, model.ApiErrorBindingType())
		if len(errs) > 0 {
			return err
		}
		apiError := dataError.(model.ApiError)
		if *apiError.ErrorCode == int64(60576) {
			return nsxutil.PageMaxError{Desc: "page max overflow"}
		}
	default:
		return err
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

// InitializeCommonStore is the common method used by InitializeResourceStore and InitializeVPCResourceStore
func (service *Service) InitializeCommonStore(wg *sync.WaitGroup, fatalErrors chan error, org string, project string, resourceTypeValue string, tags []model.Tag, store Store) {
	defer wg.Done()

	tagScopeClusterKey := strings.Replace(TagScopeCluster, "/", "\\/", -1)
	tagScopeClusterValue := strings.Replace(service.NSXClient.NsxConfig.Cluster, ":", "\\:", -1)
	tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagScopeClusterKey, tagScopeClusterValue)

	for _, tag := range tags {
		tagKey := strings.Replace(*tag.Scope, "/", "\\/", -1)
		tagParam += fmt.Sprintf(" AND tags.scope:%s ", tagKey)
		if tag.Tag != nil {
			tagValue := strings.Replace(*tag.Tag, ":", "\\:", -1)
			tagParam += fmt.Sprintf(" AND tags.tag:%s ", tagValue)
		}
	}

	resourceParam := fmt.Sprintf("%s:%s", ResourceType, resourceTypeValue)
	queryParam := resourceParam + " AND " + tagParam

	if org != "" || project != "" {
		// QueryClient.List() will escape the path, "path:" then will be "path%25%3A" instead of "path:3A",
		//"path%25%3A" would fail to get response. Hack it here.
		path := "\\/orgs\\/" + org + "\\/projects\\/" + project + "\\/*"
		pathUnescape, _ := url.PathUnescape("path%3A")
		queryParam += " AND " + pathUnescape + path
	}

	var cursor *string = nil
	count := uint64(0)
	for {
		var err error

		var results []*data.StructValue
		var resultCount *int64
		if store.IsPolicyAPI() {
			response, searchEerr := service.NSXClient.QueryClient.List(queryParam, cursor, nil, Int64(PageSize), nil, nil)
			results = response.Results
			cursor = response.Cursor
			resultCount = response.ResultCount
			err = searchEerr
		} else {
			response, searchEerr := service.NSXClient.MPQueryClient.List(queryParam, cursor, nil, Int64(PageSize), nil, nil)
			results = response.Results
			cursor = response.Cursor
			resultCount = response.ResultCount
			err = searchEerr
		}
		err = TransError(err)
		if _, ok := err.(nsxutil.PageMaxError); ok == true {
			DecrementPageSize(Int64(PageSize))
			continue
		}
		if err != nil {
			fatalErrors <- err
		}
		for _, entity := range results {
			err = store.TransResourceToStore(entity)
			if err != nil {
				fatalErrors <- err
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
	log.Info("initialized store", "resourceType", resourceTypeValue, "count", count)
}
