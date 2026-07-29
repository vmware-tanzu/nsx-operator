package rest

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
)

// ServiceEndpointStatisticsREST implements the REST api for ServiceEndpointStatistics.
type ServiceEndpointStatisticsREST struct {
	store *storage.ServiceEndpointStatisticsStorage
}

var _ rest.Getter = &ServiceEndpointStatisticsREST{}
var _ rest.Scoper = &ServiceEndpointStatisticsREST{}
var _ rest.SingularNameProvider = &ServiceEndpointStatisticsREST{}

// NewServiceEndpointStatisticsREST creates a new REST instance.
func NewServiceEndpointStatisticsREST(store *storage.ServiceEndpointStatisticsStorage) *ServiceEndpointStatisticsREST {
	return &ServiceEndpointStatisticsREST{
		store: store,
	}
}

// New returns an empty object that can be used with Create and Update after request data has been put into it.
func (r *ServiceEndpointStatisticsREST) New() runtime.Object {
	return &easv1alpha1.ServiceEndpointStatistics{}
}

// Destroy cleans up resources on shutdown.
func (r *ServiceEndpointStatisticsREST) Destroy() {
}

// NamespaceScoped returns true if the storage is namespaced
func (r *ServiceEndpointStatisticsREST) NamespaceScoped() bool {
	return true
}

// GetSingularName implements the rest.SingularNameProvider interface
func (r *ServiceEndpointStatisticsREST) GetSingularName() string {
	return "serviceendpointstatistic"
}

// Get finds a resource in the storage by name and returns it.
func (r *ServiceEndpointStatisticsREST) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	namespace, ok := request.NamespaceFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("namespace not found in context")
	}
	return r.store.Get(ctx, namespace, name)
}
