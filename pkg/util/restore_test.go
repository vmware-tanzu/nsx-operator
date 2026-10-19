package util

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestCompareNSXRestore(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	tests := []struct {
		name           string
		prepareFunc    func() *gomonkey.Patches
		expectedResult bool
		expectedErr    string
	}{
		{
			name: "NCPConfigGetFailure",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("failed to get ncpconfig"))
				return nil
			},
			expectedResult: false,
			expectedErr:    "failed to get ncpconfig",
		},
		{
			name: "NCPConfigCreateFailure",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(apierrors.NewNotFound(
					schema.GroupResource{
						Group:    "nsx.vmware.com",
						Resource: "NCPConfig",
					}, ""))
				k8sClient.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("failed to create ncpconfig"))
				return nil
			},
			expectedResult: false,
			expectedErr:    "failed to create ncpconfig",
		},
		{
			name: "ForceRestore",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					obj.SetAnnotations(map[string]string{AnnotationForceRestore: "true"})
					return nil
				})
				return nil
			},
			expectedResult: true,
		},
		{
			name: "NCPConfigUpdateFailure",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					obj.SetAnnotations(map[string]string{})
					return nil
				})
				k8sClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("failed to update ncpconfig"))
				return nil
			},
			expectedResult: false,
			expectedErr:    "failed to update ncpconfig",
		},
		{
			name: "NSXStatusError",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					obj.SetAnnotations(map[string]string{})
					return nil
				})
				k8sClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
				patches := gomonkey.ApplyFunc((*fakeStatusClient).Get, func(c *fakeStatusClient, restoreComponentParam *string) (model.ClusterRestoreStatus, error) {
					return model.ClusterRestoreStatus{}, errors.New("mock NSX status error")
				})
				return patches
			},
			expectedResult: false,
			expectedErr:    "failed to get NSX restore status",
		},
		{
			name: "NSXNotRestore",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					obj.SetAnnotations(map[string]string{})
					return nil
				})
				k8sClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
				patches := gomonkey.ApplyFunc((*fakeStatusClient).Get, func(c *fakeStatusClient, restoreComponentParam *string) (model.ClusterRestoreStatus, error) {
					return model.ClusterRestoreStatus{
						Status: &model.GlobalRestoreStatus{
							Value: &RestoreStatusInitial,
						},
					}, nil
				})
				return patches
			},
			expectedResult: false,
		},
		{
			name: "NSXRestoreNotSuccess",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					obj.SetAnnotations(map[string]string{AnnotationRestoreEndTime: "-1"})
					return nil
				})
				patches := gomonkey.ApplyFunc((*fakeStatusClient).Get, func(c *fakeStatusClient, restoreComponentParam *string) (model.ClusterRestoreStatus, error) {
					return model.ClusterRestoreStatus{
						Status: &model.GlobalRestoreStatus{
							Value: common.String("RUNNING"),
						},
					}, nil
				})
				return patches
			},
			expectedResult: false,
			expectedErr:    "NSX restore not succeeds with status RUNNING",
		},
		{
			name: "NSXRestored",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					obj.SetAnnotations(map[string]string{AnnotationRestoreEndTime: "-1"})
					return nil
				})
				patches := gomonkey.ApplyFunc((*fakeStatusClient).Get, func(c *fakeStatusClient, restoreComponentParam *string) (model.ClusterRestoreStatus, error) {
					return model.ClusterRestoreStatus{
						Status: &model.GlobalRestoreStatus{
							Value: common.String(RestoreStatusSuccess),
						},
						RestoreEndTime: common.Int64(time.Now().UnixMilli()),
					}, nil
				})
				return patches
			},
			expectedResult: true,
		},
		{
			name: "NSXRestoredOutdated",
			prepareFunc: func() *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					obj.SetAnnotations(map[string]string{AnnotationRestoreEndTime: strconv.Itoa(int(time.Now().UnixMilli()))})
					return nil
				})
				patches := gomonkey.ApplyFunc((*fakeStatusClient).Get, func(c *fakeStatusClient, restoreComponentParam *string) (model.ClusterRestoreStatus, error) {
					return model.ClusterRestoreStatus{
						Status: &model.GlobalRestoreStatus{
							Value: common.String(RestoreStatusSuccess),
						},
						RestoreEndTime: common.Int64(int64(time.Now().AddDate(0, 0, -1).UnixMilli())),
					}, nil
				})
				return patches
			},
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc()
			if patches != nil {
				defer patches.Reset()
			}
			result, err := CompareNSXRestore(k8sClient, &nsx.Client{
				StatusClient: &fakeStatusClient{},
			})
			assert.Equal(t, tt.expectedResult, result)
			if tt.expectedErr != "" {
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.Nil(t, err)
			}
		})
	}

}

func TestUpdateRestoreEndTime(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	now := time.Now().UnixMilli()
	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		obj.SetAnnotations(map[string]string{AnnotationRestoreEndTime: "-1"})
		return nil
	})
	k8sClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, obj client.Object, option ...client.UpdateOption) error {
		anno := obj.GetAnnotations()
		lastRestoreTime, err := strconv.ParseInt(anno[AnnotationRestoreEndTime], 10, 64)
		assert.Nil(t, err)
		assert.LessOrEqual(t, now, lastRestoreTime)
		return nil
	})

	err := updateRestoreEndTime(k8sClient)
	assert.Nil(t, err)
}

func TestProcessRestore(t *testing.T) {
	for _, tc := range []struct {
		name        string
		gcErr       error
		restoreErr  error
		expectedErr string
	}{
		{name: "Success"},
		{name: "GCError", gcErr: errors.New("mocked gc error for Subnet"), expectedErr: "failed to collect garbage: [mocked gc error for Subnet]"},
		{name: "RestoreError", restoreErr: errors.New("mocked restore error for SubnetPort"), expectedErr: "failed to restore resources: [mocked restore error for SubnetPort]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			reconcilerList := []ReconcilerProvider{
				&fakeReconcilerProvider{Name: "VPC", calls: &calls},
				&fakeReconcilerProvider{Name: "Subnet", gcErr: tc.gcErr, calls: &calls},
				&fakeReconcilerProvider{Name: "SubnetPort", restoreErr: tc.restoreErr, calls: &calls},
			}
			obj := NewNSXRestoreStatus()
			obj.SetAnnotations(map[string]string{common.AnnotationLBProvider: "avi", AnnotationRestoreEndTime: "-1", "restore_end_time": "123"})
			c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(obj).Build()
			err := ProcessRestore(reconcilerList, c)
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
			}
			expectedCalls := []string{"gc:SubnetPort", "gc:Subnet", "gc:VPC"}
			if tc.gcErr == nil {
				expectedCalls = append(expectedCalls, "restore:VPC", "restore:Subnet", "restore:SubnetPort")
			}
			assert.Equal(t, expectedCalls, calls)
			stored := NewNSXRestoreStatus()
			require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(obj), stored))
			assert.Equal(t, "avi", stored.GetAnnotations()[common.AnnotationLBProvider])
			assert.Equal(t, "123", stored.GetAnnotations()["restore_end_time"])
			if tc.expectedErr == "" {
				assert.NotEqual(t, "-1", stored.GetAnnotations()[AnnotationRestoreEndTime])
			} else {
				assert.Equal(t, "-1", stored.GetAnnotations()[AnnotationRestoreEndTime])
			}
		})
	}
}

type fakeStatusClient struct{}

func (c *fakeStatusClient) Get(restoreComponentParam *string) (model.ClusterRestoreStatus, error) {
	return model.ClusterRestoreStatus{}, nil
}

type fakeReconcilerProvider struct {
	Name       string
	gcErr      error
	restoreErr error
	calls      *[]string
}

func (r *fakeReconcilerProvider) RestoreReconcile() error {
	*r.calls = append(*r.calls, "restore:"+r.Name)
	return r.restoreErr
}

func (r *fakeReconcilerProvider) CollectGarbage(ctx context.Context) error {
	*r.calls = append(*r.calls, "gc:"+r.Name)
	return r.gcErr
}

func (r *fakeReconcilerProvider) StartController(mgr ctrl.Manager, hookServer webhook.Server) error {
	return nil
}
