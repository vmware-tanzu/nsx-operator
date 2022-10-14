package common

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

const (
	PageSize int64 = 1000
)

type ResourceAssertion func(i interface{}) interface{}

// Store is the interface for store, it should be implemented by subclass
type Store interface {
	// TransResourceToStore is the method to transform the resource of type data.StructValue
	// to specific nsx-t side resource and then add it to the store.
	TransResourceToStore(obj *data.StructValue) error
	// CRUDResource is the method to create, update and delete the resource to the store based
	// on its tag MarkedForDelete.
	CRUDResource(obj interface{}) error
}

// ResourceStore is the store for resource, embed it to subclass
type ResourceStore struct {
	cache.Indexer        // the ultimate place to store the resource
	bindings.BindingType // used by converter to convert the resource
	ResourceAssertion    // used to assert the resource to specific type
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
	err2 := resourceStore.Add(resourceStore.ResourceAssertion(obj))
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
func (service *Service) InitializeResourceStore(wg *sync.WaitGroup, fatalErrors chan error, resourceTypeValue string, store Store) {
	defer wg.Done()

	tagScopeClusterKey := strings.Replace(TagScopeCluster, "/", "\\/", -1)
	tagScopeClusterValue := strings.Replace(service.NSXClient.NsxConfig.Cluster, ":", "\\:", -1)
	tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagScopeClusterKey, tagScopeClusterValue)
	resourceParam := fmt.Sprintf("%s:%s", ResourceType, resourceTypeValue)
	queryParam := resourceParam + " AND " + tagParam

	var cursor *string = nil
	for {
		response, err := service.NSXClient.QueryClient.List(queryParam, cursor, nil, Int64(PageSize), nil, nil)
		err = TransError(err)
		if _, ok := err.(nsxutil.PageMaxError); ok == true {
			DecrementPageSize(Int64(PageSize))
			continue
		}
		if err != nil {
			fatalErrors <- err
		}
		for _, entity := range response.Results {
			err = store.TransResourceToStore(entity)
			if err != nil {
				fatalErrors <- err
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
