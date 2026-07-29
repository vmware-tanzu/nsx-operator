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

// VPCServiceEndpointStatisticsREST implements the REST api for VPCServiceEndpointStatistics.
type VPCServiceEndpointStatisticsREST struct {
	store *storage.VPCServiceEndpointStatisticsStorage
}

var _ rest.Getter = &VPCServiceEndpointStatisticsREST{}
var _ rest.Scoper = &VPCServiceEndpointStatisticsREST{}
var _ rest.SingularNameProvider = &VPCServiceEndpointStatisticsREST{}

// NewVPCServiceEndpointStatisticsREST creates a new REST instance.
func NewVPCServiceEndpointStatisticsREST(store *storage.VPCServiceEndpointStatisticsStorage) *VPCServiceEndpointStatisticsREST {
	return &VPCServiceEndpointStatisticsREST{
		store: store,
	}
}

// New returns an empty object that can be used with Create and Update after request data has been put into it.
func (r *VPCServiceEndpointStatisticsREST) New() runtime.Object {
	return &easv1alpha1.VPCServiceEndpointStatistics{}
}

// Destroy cleans up resources on shutdown.
func (r *VPCServiceEndpointStatisticsREST) Destroy() {
}

// NamespaceScoped returns true if the storage is namespaced
func (r *VPCServiceEndpointStatisticsREST) NamespaceScoped() bool {
	return true
}

// GetSingularName implements the rest.SingularNameProvider interface
func (r *VPCServiceEndpointStatisticsREST) GetSingularName() string {
	return "vpcserviceendpointstatistic"
}

// Get finds a resource in the storage by name and returns it.
func (r *VPCServiceEndpointStatisticsREST) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	namespace, ok := request.NamespaceFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("namespace not found in context")
	}
	return r.store.Get(ctx, namespace, name)
}
