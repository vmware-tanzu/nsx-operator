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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
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
	fakeNC := &common.VPCNetworkConfigInfo{
		IsDefault: true,
		Name:      "fake-name",
	}
	r := createNameSpaceReconciler(nil)
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
			patch := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetDefaultNetworkConfig", func(_ *vpc.VPCService) (*common.VPCNetworkConfigInfo, error) {
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
	vpcInfo := common.VPCNetworkConfigInfo{
		IsDefault:              true,
		Org:                    "",
		Name:                   "fake-VPCNetworkConfig",
		VPCConnectivityProfile: "",
		NSXProject:             "",
		PrivateIPs:             nil,
		DefaultSubnetSize:      0,
		VPCPath:                "fake-patch",
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
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetDefaultNetworkConfig", func(_ *vpc.VPCService) (*common.VPCNetworkConfigInfo, error) {
					return &common.VPCNetworkConfigInfo{
						IsDefault:              true,
						Org:                    "",
						Name:                   "fake-VPCNetworkConfig",
						VPCConnectivityProfile: "",
						NSXProject:             "",
						PrivateIPs:             nil,
						DefaultSubnetSize:      0,
						VPCPath:                "fake-patch",
					}, nil
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
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetDefaultNetworkConfig", func(_ *vpc.VPCService) (*common.VPCNetworkConfigInfo, error) {
					return &vpcInfo, nil
				})
				patches.ApplyMethod(reflect.TypeOf(&vpc.VPCService{}), "GetVPCNetworkConfig", func(_ *vpc.VPCService, ncCRName string) (*common.VPCNetworkConfigInfo, bool, error) {
					return &vpcInfo, true, nil
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
