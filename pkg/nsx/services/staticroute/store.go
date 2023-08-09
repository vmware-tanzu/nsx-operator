package staticroute

import (
	"errors"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// StaticRouteStore is a store for static route
type StaticRouteStore struct {
	common.ResourceStore
}

// keyFunc is used to get the key of a resource, usually, which is the ID of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case model.StaticRoutes:
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
	case model.StaticRoutes:
		return filterTag(v.Tags), nil
	default:
		break
	}
	return res, nil
}

var filterTag = func(v []model.Tag) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == common.TagScopeStaticRouteCRUID {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

func (StaticRouteStore *StaticRouteStore) Operate(i interface{}) error {
	// not used by staticroute since staticroute doesn't use hierarchy API
	return nil
}

func (StaticRouteStore *StaticRouteStore) GetByKey(key string) *model.StaticRoutes {
	obj := StaticRouteStore.ResourceStore.GetByKey(key)
	if obj != nil {
		staticRoute := obj.(model.StaticRoutes)
		return &staticRoute
	}
	return nil
}
