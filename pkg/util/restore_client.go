package util

import (
	"context"
	"reflect"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// ShouldInterceptResource checks if a Kubernetes resource should have its write operations
// bypassed during restore mode to prevent calling offline validating webhooks.
// Only resources that have validating webhooks configured are intercepted.
func ShouldInterceptResource(obj client.Object) bool {
	if obj == nil {
		return false
	}

	// Match target NSX CRD types that have validating webhooks configured:
	// Subnet, SubnetSet, StaticRoute, IPAddressAllocation, and AddressBinding.
	switch obj.(type) {
	case *v1alpha1.Subnet,
		*v1alpha1.SubnetSet,
		*v1alpha1.StaticRoute,
		*v1alpha1.IPAddressAllocation,
		*v1alpha1.AddressBinding:
		return true
	}

	return false
}

// RestoreClient wraps a client.Client and intercepts write operations on targeted NSX CRDs
// during restore mode to prevent triggering webhooks that are not yet running.
type RestoreClient struct {
	client.Client
}

// NewRestoreClient creates a new RestoreClient.
func NewRestoreClient(c client.Client) client.Client {
	return &RestoreClient{
		Client: c,
	}
}

// Update intercepts Update calls on target CRDs in restore mode.
func (c *RestoreClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if ShouldInterceptResource(obj) {
		log.Info("Restore mode active: bypassing CR update",
			"type", reflect.TypeOf(obj).String(),
			"namespace", obj.GetNamespace(),
			"name", obj.GetName())
		return nil
	}
	return c.Client.Update(ctx, obj, opts...)
}

// Create intercepts Create calls on target CRDs in restore mode.
func (c *RestoreClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if ShouldInterceptResource(obj) {
		log.Info("Restore mode active: bypassing CR create",
			"type", reflect.TypeOf(obj).String(),
			"namespace", obj.GetNamespace(),
			"name", obj.GetName())
		return nil
	}
	return c.Client.Create(ctx, obj, opts...)
}

// Delete intercepts Delete calls on target CRDs in restore mode.
func (c *RestoreClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if ShouldInterceptResource(obj) {
		log.Info("Restore mode active: bypassing CR delete",
			"type", reflect.TypeOf(obj).String(),
			"namespace", obj.GetNamespace(),
			"name", obj.GetName())
		return nil
	}
	return c.Client.Delete(ctx, obj, opts...)
}

// RestoreManager wraps a ctrl.Manager so that GetClient() returns the RestoreClient in restore mode.
type RestoreManager struct {
	ctrl.Manager
	client client.Client
}

// NewRestoreManager creates a new RestoreManager wrapping the provided manager with a RestoreClient.
func NewRestoreManager(mgr ctrl.Manager) ctrl.Manager {
	return &RestoreManager{
		Manager: mgr,
		client:  NewRestoreClient(mgr.GetClient()),
	}
}

// GetClient returns the wrapped RestoreClient.
func (m *RestoreManager) GetClient() client.Client {
	return m.client
}
