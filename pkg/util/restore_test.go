package util

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

	err := UpdateRestoreEndTime(k8sClient)
	assert.Nil(t, err)
}

type fakeStatusClient struct{}

func (c *fakeStatusClient) Get(restoreComponentParam *string) (model.ClusterRestoreStatus, error) {
	return model.ClusterRestoreStatus{}, nil
}
