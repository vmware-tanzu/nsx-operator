package util

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	legacyv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mock_manager "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/manager"
)

func TestShouldInterceptResource(t *testing.T) {
	// Nil
	assert.False(t, ShouldInterceptResource(nil))

	// NCPConfig (no webhook)
	ncpConfig := &unstructured.Unstructured{}
	ncpConfig.SetName(NSXRestoreStatus)
	assert.False(t, ShouldInterceptResource(ncpConfig))

	// NSX CRDs that have validating webhooks configured
	assert.True(t, ShouldInterceptResource(&v1alpha1.Subnet{}))
	assert.True(t, ShouldInterceptResource(&v1alpha1.SubnetSet{}))
	assert.True(t, ShouldInterceptResource(&v1alpha1.StaticRoute{}))
	assert.True(t, ShouldInterceptResource(&v1alpha1.IPAddressAllocation{}))
	assert.True(t, ShouldInterceptResource(&v1alpha1.AddressBinding{}))

	// NSX CRDs without validating webhooks should NOT be intercepted
	assert.False(t, ShouldInterceptResource(&v1alpha1.SubnetPort{}))
	assert.False(t, ShouldInterceptResource(&v1alpha1.NetworkInfo{}))
	assert.False(t, ShouldInterceptResource(&v1alpha1.SecurityPolicy{}))
	assert.False(t, ShouldInterceptResource(&legacyv1alpha1.SecurityPolicy{}))
	assert.False(t, ShouldInterceptResource(&v1alpha1.SubnetIPReservation{}))
	assert.False(t, ShouldInterceptResource(&v1alpha1.SubnetConnectionBindingMap{}))

	// Non-NSX Core K8s Resources
	assert.False(t, ShouldInterceptResource(&corev1.Namespace{}))
	assert.False(t, ShouldInterceptResource(&corev1.Pod{}))
	assert.False(t, ShouldInterceptResource(&corev1.Node{}))
	assert.False(t, ShouldInterceptResource(&corev1.ConfigMap{}))
}

func TestRestoreClient_InterceptTargetCR(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	mockK8sClient := mock_client.NewMockClient(mockCtl)

	// When target CR is passed, underlying client must NEVER be called
	restoreClient := NewRestoreClient(mockK8sClient)
	ctx := context.Background()

	subnet := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnet",
			Namespace: "default",
		},
	}

	// Update intercepted
	err := restoreClient.Update(ctx, subnet)
	assert.NoError(t, err)

	// Create intercepted
	err = restoreClient.Create(ctx, subnet)
	assert.NoError(t, err)

	// Delete intercepted
	err = restoreClient.Delete(ctx, subnet)
	assert.NoError(t, err)
}

func TestRestoreClient_PassthroughWhitelistedAndNonTarget(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	mockK8sClient := mock_client.NewMockClient(mockCtl)

	restoreClient := NewRestoreClient(mockK8sClient)
	ctx := context.Background()

	// 1. Whitelisted NCPConfig should pass through
	ncpConfig := &unstructured.Unstructured{}
	ncpConfig.SetName(NSXRestoreStatus)

	mockK8sClient.EXPECT().Update(ctx, ncpConfig).Return(nil).Times(1)
	err := restoreClient.Update(ctx, ncpConfig)
	assert.NoError(t, err)

	// 2. Non-target core resource should pass through
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns",
		},
	}

	mockK8sClient.EXPECT().Update(ctx, ns).Return(nil).Times(1)
	err = restoreClient.Update(ctx, ns)
	assert.NoError(t, err)

	mockK8sClient.EXPECT().Create(ctx, ns).Return(nil).Times(1)
	err = restoreClient.Create(ctx, ns)
	assert.NoError(t, err)

	mockK8sClient.EXPECT().Delete(ctx, ns).Return(nil).Times(1)
	err = restoreClient.Delete(ctx, ns)
	assert.NoError(t, err)
}

func TestRestoreManager(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	mockMgr := mock_manager.NewMockManager(mockCtl)
	mockK8sClient := mock_client.NewMockClient(mockCtl)

	mockMgr.EXPECT().GetClient().Return(mockK8sClient).Times(1)
	restoreMgr := NewRestoreManager(mockMgr)
	require.NotNil(t, restoreMgr)
	c := restoreMgr.GetClient()
	require.NotNil(t, c)
	_, ok := c.(*RestoreClient)
	assert.True(t, ok)
}
