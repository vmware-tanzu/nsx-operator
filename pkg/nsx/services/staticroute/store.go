package staticroute

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// StaticRouteStore is a store for static route
type StaticRouteStore struct {
	common.ResourceStore
}

// keyFunc is used to get the key of a resource, usually, which is the ID of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.StaticRoutes:
		return *v.Id, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

// indexFunc is used to get index of a resource, usually, which is the UID of the CR controller reconciles,
// index is used to filter out resources which are related to the CR
func indexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch v := obj.(type) {
	case *model.StaticRoutes:
		return filterTag(v.Tags, common.TagScopeStaticRouteCRUID), nil
	default:
		break
	}
	return res, nil
}

func indexStaticRouteNamespace(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.StaticRoutes:
		return filterTag(o.Tags, common.TagScopeNamespace), nil
	default:
		return nil, errors.New("indexByStaticRouteNamespace doesn't support unknown type")
	}
}

var filterTag = func(v []model.Tag, tagScope string) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == tagScope {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

func (StaticRouteStore *StaticRouteStore) Apply(i interface{}) error {
	// not used by staticroute since staticroute doesn't use hierarchy API
	return nil
}

func (StaticRouteStore *StaticRouteStore) GetByKey(key string) *model.StaticRoutes {
	obj := StaticRouteStore.ResourceStore.GetByKey(key)
	if obj != nil {
		staticRoute := obj.(*model.StaticRoutes)
		return staticRoute
	}
	return nil
}

func (StaticRouteStore *StaticRouteStore) GetByVPCPath(vpcPath string) ([]*model.StaticRoutes, error) {
	objs, err := StaticRouteStore.ResourceStore.ByIndex(common.IndexByVPCPathFuncKey, vpcPath)
	if err != nil {
		return nil, err
	}
	routes := make([]*model.StaticRoutes, len(objs))
	for i, obj := range objs {
		route := obj.(*model.StaticRoutes)
		routes[i] = route
	}
	return routes, nil
}

func (StaticRouteStore *StaticRouteStore) DeleteMultipleObjects(routes []*model.StaticRoutes) {
	for _, route := range routes {
		StaticRouteStore.Delete(route)
	}
}

func buildStaticRouteStore() *StaticRouteStore {
	return &StaticRouteStore{
		ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
				common.TagScopeStaticRouteCRUID: indexFunc,
				common.TagScopeNamespace:        indexStaticRouteNamespace,
				common.IndexByVPCPathFuncKey:    common.IndexByVPCFunc,
			}),
			BindingType: model.StaticRoutesBindingType(),
		},
	}
}
