package util

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

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
