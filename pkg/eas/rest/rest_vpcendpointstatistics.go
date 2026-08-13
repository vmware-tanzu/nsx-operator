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

// VPCEndpointStatisticsREST implements the REST api for VPCEndpointStatistics.
type VPCEndpointStatisticsREST struct {
	store *storage.VPCEndpointStatisticsStorage
}

var _ rest.Getter = &VPCEndpointStatisticsREST{}
var _ rest.Scoper = &VPCEndpointStatisticsREST{}
var _ rest.SingularNameProvider = &VPCEndpointStatisticsREST{}

// NewVPCEndpointStatisticsREST creates a new REST instance.
func NewVPCEndpointStatisticsREST(store *storage.VPCEndpointStatisticsStorage) *VPCEndpointStatisticsREST {
	return &VPCEndpointStatisticsREST{
		store: store,
	}
}

// New returns an empty object that can be used with Create and Update after request data has been put into it.
func (r *VPCEndpointStatisticsREST) New() runtime.Object {
	return &easv1alpha1.VPCEndpointStatistics{}
}

// Destroy cleans up resources on shutdown.
func (r *VPCEndpointStatisticsREST) Destroy() {
}

// NamespaceScoped returns true if the storage is namespaced
func (r *VPCEndpointStatisticsREST) NamespaceScoped() bool {
	return true
}

// GetSingularName implements the rest.SingularNameProvider interface
func (r *VPCEndpointStatisticsREST) GetSingularName() string {
	return "vpcendpointstatistic"
}

// Get finds a resource in the storage by name and returns it.
func (r *VPCEndpointStatisticsREST) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	namespace, ok := request.NamespaceFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("namespace not found in context")
	}
	return r.store.Get(ctx, namespace, name)
}
