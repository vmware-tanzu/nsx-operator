/* Copyright © 2023 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	BuildVPCCRFunc = BuildVPCCR
)

func createNameSpaceReconciler() *NamespaceReconciler {
	service := &vpc.VPCService{
		Service: common.Service{
			Client:    nil,
			NSXClient: &nsx.Client{},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
		VPCNetworkConfigMap:   map[string]common.VPCNetworkConfigInfo{},
		VPCNSNetworkConfigMap: map[string]string{},
	}

	return &NamespaceReconciler{
		Client:     fake.NewClientBuilder().Build(),
		Scheme:     fake.NewClientBuilder().Build().Scheme(),
		VPCService: service,
	}
}

func TestGetDefaultNetworkConfigName(t *testing.T) {
	fakeNC := &common.VPCNetworkConfigInfo{
		IsDefault: true,
		Name:      "fake-name",
	}
	r := createNameSpaceReconciler()
	tests := []struct {
		name       string
		exist      bool
		nc         *common.VPCNetworkConfigInfo
		expectName string
	}{
		{"1", false, nil, ""},
		{"2", true, fakeNC, "fake-name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetDefaultNetworkConfig", func(_ *vpc.VPCService) (bool, *common.VPCNetworkConfigInfo) {
				return tt.exist, tt.nc
			})
			name, err := r.getDefaultNetworkConfigName()
			assert.Equal(t, tt.expectName, name)
			if name == "" {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
			patch.Reset()
		})
	}
}

func TestInsertNamespaceNetworkconfigBinding(t *testing.T) {
	r := createNameSpaceReconciler()
	fakeErr := errors.New("fake-error")
	tests := []struct {
		name       string
		ns         string
		exist      bool
		ncName     string
		annos      map[string]string
		expectName string
		err        error
	}{
		{"1", "test-ns1", false, "", nil, "", fakeErr},
		{"2", "test-ns2", true, "fake-nc1", map[string]string{}, "fake-nc1", nil},
		{"3", "test-ns3", true, "fake-nc2", map[string]string{common.AnnotationVPCNetworkConfig: "fake-name"}, "fake-name", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r.VPCService = &common.MockVPCServiceProvider{}
			patch := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "getDefaultNetworkConfigName", func(_ *NamespaceReconciler, ns string, anno map[string]string) (string, error) {
				return tt.ncName, tt.err
			})
			r.VPCService.(*common.MockVPCServiceProvider).On("RegisterNamespaceNetworkconfigBinding", mock.Anything, mock.Anything).Return(nil)
			err := r.insertNamespaceNetworkconfigBinding(tt.ns, tt.annos)
			if err != nil {
				r.VPCService.(*common.MockVPCServiceProvider).AssertNotCalled(t, "RegisterNamespaceNetworkconfigBinding")
			} else {
				r.VPCService.(*common.MockVPCServiceProvider).AssertCalled(t, "RegisterNamespaceNetworkconfigBinding", tt.ns, tt.expectName)
			}
			patch.Reset()
		})
	}
}

func TestCreateVPCCR(t *testing.T) {
	r := createNameSpaceReconciler()
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	ctx := context.Background()
	r.Client = k8sClient
	namespace := v1.Namespace{}
	vpcList1 := &v1alpha1.VPCList{Items: []v1alpha1.VPC{{ObjectMeta: metav1.ObjectMeta{Name: "fake-name1"}}, {}}}
	vpcs := &v1alpha1.VPCList{}
	k8sClient.EXPECT().List(gomock.Any(), vpcs, client.InNamespace("test-ns")).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		list.(*v1alpha1.VPCList).Items = vpcList1.Items
		return nil
	})
	target, err := r.createVPCCR(&ctx, &namespace, "test-ns", "test-nc", nil)
	assert.Equal(t, target.Name, "fake-name1")
	assert.Nil(t, err)

	vpcs = &v1alpha1.VPCList{}
	k8sClient.EXPECT().List(gomock.Any(), vpcs, client.InNamespace("test-ns")).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		list.(*v1alpha1.VPCList).Items = []v1alpha1.VPC{}
		return nil
	})
	patch1 := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfig", func(_ *vpc.VPCService, ncName string) (common.VPCNetworkConfigInfo, bool) {
		return common.VPCNetworkConfigInfo{}, false
	})
	patch2 := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "namespaceError", func(_ *NamespaceReconciler, _ *context.Context, _ client.Object, _ string, _ error) {
	})
	target, err = r.createVPCCR(&ctx, &namespace, "test-ns", "test-nc", nil)
	assert.Nil(t, target)
	assert.NotNil(t, err)
	patch1.Reset()
	patch2.Reset()

	vpcs = &v1alpha1.VPCList{}
	k8sClient.EXPECT().List(gomock.Any(), vpcs, client.InNamespace("test-ns")).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		list.(*v1alpha1.VPCList).Items = []v1alpha1.VPC{}
		return nil
	})
	patch1 = gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfig", func(_ *vpc.VPCService, ncName string) (common.VPCNetworkConfigInfo, bool) {
		return common.VPCNetworkConfigInfo{}, true
	})
	patch2 = gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "namespaceError", func(_ *NamespaceReconciler, _ *context.Context, _ client.Object, _ string, _ error) {
	})
	patch3 := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "ValidateNetworkConfig", func(_ *vpc.VPCService, nc common.VPCNetworkConfigInfo) bool {
		return false
	})
	target, err = r.createVPCCR(&ctx, &namespace, "test-ns", "test-nc", nil)
	assert.Nil(t, target)
	assert.NotNil(t, err)
	patch1.Reset()
	patch2.Reset()
	patch3.Reset()

	vpcs = &v1alpha1.VPCList{}
	mockVPC := &v1alpha1.VPC{}
	mockVPC.SetName("test-vpc")
	buildVPCPatch := gomonkey.ApplyFuncReturn(BuildVPCCR, mockVPC)

	k8sClient.EXPECT().List(gomock.Any(), vpcs, client.InNamespace("test-ns")).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		list.(*v1alpha1.VPCList).Items = []v1alpha1.VPC{}
		return nil
	})
	k8sClient.EXPECT().Create(gomock.Any(), mockVPC, gomock.Any()).Return(errors.New("create-err")).Do(func(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
		return errors.New("create vpc cr error")
	})
	patch1 = gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfig", func(_ *vpc.VPCService, ncName string) (common.VPCNetworkConfigInfo, bool) {
		return common.VPCNetworkConfigInfo{}, true
	})
	patch2 = gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "namespaceError", func(_ *NamespaceReconciler, _ *context.Context, _ client.Object, _ string, _ error) {
	})
	patch3 = gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "ValidateNetworkConfig", func(_ *vpc.VPCService, nc common.VPCNetworkConfigInfo) bool {
		return true
	})
	target, err = r.createVPCCR(&ctx, &namespace, "test-ns", "test-nc", nil)
	assert.Nil(t, target)
	assert.NotNil(t, err)
	patch1.Reset()
	patch2.Reset()
	patch3.Reset()

	vpcs = &v1alpha1.VPCList{}
	k8sClient.EXPECT().List(gomock.Any(), vpcs, client.InNamespace("test-ns")).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		list.(*v1alpha1.VPCList).Items = []v1alpha1.VPC{}
		return nil
	})
	k8sClient.EXPECT().Create(gomock.Any(), mockVPC, gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
		return nil
	})
	patch1 = gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetVPCNetworkConfig", func(_ *vpc.VPCService, ncName string) (common.VPCNetworkConfigInfo, bool) {
		return common.VPCNetworkConfigInfo{}, true
	})
	patch2 = gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "namespaceError", func(_ *NamespaceReconciler, _ *context.Context, _ client.Object, _ string, _ error) {
	})
	patch3 = gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "ValidateNetworkConfig", func(_ *vpc.VPCService, nc common.VPCNetworkConfigInfo) bool {
		return true
	})
	patchUtil := gomonkey.ApplyFuncReturn(util.UpdateK8sResourceAnnotation, nil)
	target, err = r.createVPCCR(&ctx, &namespace, "test-ns", "test-nc", nil)
	assert.Equal(t, target.GetName(), "test-vpc")
	assert.Nil(t, err)

	patch1.Reset()
	patch2.Reset()
	patch3.Reset()
	buildVPCPatch.Reset()
	patchUtil.Reset()
}
