/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctlcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func createNameSpaceReconciler(objs []client.Object) *NamespaceReconciler {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...).Build()

	service := &vpc.VPCService{
		Service: common.Service{
			Client:    fakeClient,
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint:   "vmc-enforcementpoint",
					UseAVILoadBalancer: false,
				},
			},
		},
	}

	return &NamespaceReconciler{
		Client:     fakeClient,
		Scheme:     fake.NewClientBuilder().Build().Scheme(),
		VPCService: service,
		NSXConfig: &config.NSXOperatorConfig{
			NsxConfig: &config.NsxConfig{
				EnforcementPoint:   "vmc-enforcementpoint",
				UseAVILoadBalancer: false,
			},
		},
	}
}

func TestGetDefaultNetworkConfigName(t *testing.T) {
	fakeNC := &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "fake-name",
			Annotations: map[string]string{common.AnnotationDefaultNetworkConfig: "true"},
		},
	}
	r := createNameSpaceReconciler(nil)
	tests := []struct {
		name       string
		exist      bool
		nc         *v1alpha1.VPCNetworkConfiguration
		expectName string
	}{
		{"1", false, nil, ""},
		{"2", true, fakeNC, "fake-name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetDefaultNetworkConfig", func(_ *vpc.VPCService) (*v1alpha1.VPCNetworkConfiguration, error) {
				if !tt.exist {
					return tt.nc, fmt.Errorf("not found")
				}
				return tt.nc, nil
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

func TestNamespaceReconciler_Reconcile(t *testing.T) {
	nc := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "fake-VPCNetworkConfig",
			Annotations: map[string]string{common.AnnotationDefaultNetworkConfig: "true"},
		},
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			VPCConnectivityProfile: "",
			NSXProject:             "/orgs/org/projects/project",
			PrivateIPs:             nil,
			DefaultSubnetSize:      0,
			VPC:                    "fake-patch",
		},
	}
	testCases := []struct {
		name                string
		req                 ctrl.Request
		expectRes           ctrl.Result
		expectErrStr        string
		patches             func(r *NamespaceReconciler) *gomonkey.Patches
		existingNamespaceCR *v1.Namespace
		expectNamespaceCR   *v1.Namespace
	}{
		{
			name: "Namespace CR not found",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}},
			patches: func(r *NamespaceReconciler) *gomonkey.Patches {
				return nil
			},
			expectRes:           ctrl.Result{},
			existingNamespaceCR: nil,
		},
		{
			name: "Namespace with Finalizer",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}},
			patches: func(r *NamespaceReconciler) *gomonkey.Patches {
				return nil
			},
			expectRes: ctrl.Result{},
			existingNamespaceCR: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test-ns", DeletionTimestamp: &metav1.Time{Time: time.Now()}, Finalizers: []string{"test-Finalizers"}},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
		},
		{
			name: "Namespace update using default NetworkConfig",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}},
			patches: func(r *NamespaceReconciler) *gomonkey.Patches {
				// GetDefaultNetworkConfig
				return nil
			},
			expectRes: ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second},
			existingNamespaceCR: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test-ns"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
		},
		{
			name: "Namespace update with error missing NetworkConfig",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}},
			patches: func(r *NamespaceReconciler) *gomonkey.Patches {
				// GetDefaultNetworkConfig
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetDefaultNetworkConfig", func(_ *vpc.VPCService) (*v1alpha1.VPCNetworkConfiguration, error) {
					return &nc, nil
				})
				return patches
			},
			expectErrStr: "missing NetworkConfig",
			expectRes:    ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second},
			existingNamespaceCR: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test-ns"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
		},
		{
			name: "Namespace update",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}},
			patches: func(r *NamespaceReconciler) *gomonkey.Patches {
				// GetDefaultNetworkConfig
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetDefaultNetworkConfig", func(_ *vpc.VPCService) (*v1alpha1.VPCNetworkConfiguration, error) {
					return &nc, nil
				})
				patches.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetVPCNetworkConfig", func(_ *vpc.VPCService, ncCRName string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
					return &nc, true, nil
				})
				return patches
			},
			expectRes: ctrl.Result{},
			existingNamespaceCR: &v1.Namespace{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "test-ns"},
				Spec:       v1.NamespaceSpec{},
				Status:     v1.NamespaceStatus{},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var objs []client.Object
			if tc.existingNamespaceCR != nil {
				objs = append(objs, tc.existingNamespaceCR)
			}
			r := createNameSpaceReconciler(objs)

			if tc.patches(r) != nil {
				patches := tc.patches(r)
				defer patches.Reset()
			}

			res, err := r.Reconcile(context.Background(), tc.req)

			assert.Equal(t, tc.expectRes, res)

			if tc.expectErrStr != "" {
				assert.ErrorContains(t, err, tc.expectErrStr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNamespaceReconciler_StartController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	vpcService := &vpc.VPCService{
		Service: common.Service{
			Client: fakeClient,
		},
	}
	mockMgr := &MockManager{scheme: runtime.NewScheme()}
	patches := gomonkey.ApplyFunc((*NamespaceReconciler).setupWithManager, func(r *NamespaceReconciler, mgr manager.Manager) error {
		return nil
	})
	patches.ApplyFunc(ctlcommon.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
		return
	})
	defer patches.Reset()
	r := NewNamespaceReconciler(mockMgr, nil, vpcService)
	err := r.StartController(mockMgr, nil)
	assert.Nil(t, err)
}

type MockManager struct {
	ctrl.Manager
	client client.Client
	scheme *runtime.Scheme
}

func (m *MockManager) GetClient() client.Client {
	return m.client
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *MockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return nil
}

func (m *MockManager) Add(runnable manager.Runnable) error {
	return nil
}

func (m *MockManager) Start(context.Context) error {
	return nil
}
