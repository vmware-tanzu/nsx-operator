package util

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestRestoreStatusWithoutAnnotations(t *testing.T) {
	patches := gomonkey.ApplyFunc((*fakeStatusClient).Get, func(_ *fakeStatusClient, _ *string) (model.ClusterRestoreStatus, error) {
		return model.ClusterRestoreStatus{Status: &model.GlobalRestoreStatus{Value: &RestoreStatusInitial}}, nil
	})
	defer patches.Reset()
	obj := NewNSXRestoreStatus()
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(obj).Build()
	nsxClient := &nsx.Client{StatusClient: &fakeStatusClient{}}
	restore, err := CompareNSXRestore(c, nsxClient)
	require.NoError(t, err)
	assert.False(t, restore)
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(obj), obj))
	assert.Equal(t, "-1", obj.GetAnnotations()[AnnotationRestoreEndTime])
	obj.SetAnnotations(nil)
	require.NoError(t, c.Update(context.Background(), obj))
	require.NoError(t, updateRestoreEndTime(c))
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(obj), obj))
	assert.NotEmpty(t, obj.GetAnnotations()[AnnotationRestoreEndTime])
}

func TestUpdateRestoreEndTimePreservesLBProvider(t *testing.T) {
	obj := NewNSXRestoreStatus()
	obj.SetAnnotations(map[string]string{
		common.AnnotationLBProvider: "avi", AnnotationRestoreEndTime: "-1", "restore_end_time": "123",
	})
	obj.Object["custom"] = "keep"
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(obj).Build()
	require.NoError(t, updateRestoreEndTime(c))
	stored := NewNSXRestoreStatus()
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(obj), stored))
	assert.Equal(t, "avi", stored.GetAnnotations()[common.AnnotationLBProvider])
	assert.Equal(t, "123", stored.GetAnnotations()["restore_end_time"])
	assert.NotEqual(t, "-1", stored.GetAnnotations()[AnnotationRestoreEndTime])
	assert.Equal(t, "keep", stored.Object["custom"])
}
