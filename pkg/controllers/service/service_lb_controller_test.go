/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package service

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"

	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func NewFakeServiceLbReconciler() *ServiceLbReconciler {
	return &ServiceLbReconciler{
		Client:   fake.NewClientBuilder().Build(),
		Scheme:   fake.NewClientBuilder().Build().Scheme(),
		Service:  nil,
		Recorder: fakeRecorder{},
	}
}

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

type MockManager struct {
	ctrl.Manager
	client client.Client
	scheme *runtime.Scheme
	config *rest.Config
}

func (m *MockManager) GetClient() client.Client {
	return m.client
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *MockManager) GetConfig() *rest.Config {
	return m.config
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

func TestServiceLbReconciler_setServiceLbStatus(t *testing.T) {
	r := NewFakeServiceLbReconciler()
	ctx := context.TODO()
	lbService := &v1.Service{}
	lbService.Spec.Type = v1.ServiceTypeLoadBalancer
	lbService.Labels = map[string]string{
		common.LabelLbIngressIpMode: common.LabelLbIngressIpModeVipValue,
	}
	vipIpMode := v1.LoadBalancerIPModeVIP
	lbService.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			IP:     "192.168.28.1",
			IPMode: &vipIpMode,
		},
	}

	// Case: IPMode is set and ingress-ip-mode label is set as vip.
	r.setServiceLbStatus(ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeVIP, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case: IPMode is set and ingress-ip-mode label is set as proxy.
	lbService.Labels = map[string]string{
		common.LabelLbIngressIpMode: common.LabelLbIngressIpModeProxyValue,
	}
	r.setServiceLbStatus(ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeProxy, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case: IPMode is set and ingress-ip-mode label is not set.
	lbService.Labels = nil
	lbService.Status.LoadBalancer.Ingress[0].IPMode = &vipIpMode
	r.setServiceLbStatus(ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeProxy, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case IPMode is not set and label is set as VIP.
	lbService.Labels = map[string]string{
		common.LabelLbIngressIpMode: common.LabelLbIngressIpModeVipValue,
	}
	lbService.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			IP:     "192.168.28.1",
			IPMode: nil,
		},
	}
	r.setServiceLbStatus(ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeVIP, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case IPMode is not set and label is set as proxy.
	lbService.Labels = map[string]string{
		common.LabelLbIngressIpMode: common.LabelLbIngressIpModeProxyValue,
	}
	lbService.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			IP:     "192.168.28.1",
			IPMode: nil,
		},
	}
	r.setServiceLbStatus(ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeProxy, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case IPMode is not set and label is not set
	lbService.Labels = nil
	lbService.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			IP:     "192.168.28.1",
			IPMode: nil,
		},
	}
	r.setServiceLbStatus(ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeProxy, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case Ingress.IP is not set
	lbService.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			IP:     "",
			IPMode: nil,
		},
	}
	r.setServiceLbStatus(ctx, lbService)
	assert.Equal(t, (*v1.LoadBalancerIPMode)(nil), lbService.Status.LoadBalancer.Ingress[0].IPMode)
}

func TestServiceLbReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &common.Service{
		NSXClient: &nsx.Client{},
		NSXConfig: &config.NSXOperatorConfig{
			CoeConfig: &config.CoeConfig{
				EnableVPCNetwork: true,
			},
			NsxConfig: &config.NsxConfig{
				EnforcementPoint: "vmc-enforcementpoint",
			},
		},
	}

	r := &ServiceLbReconciler{
		Client:   k8sClient,
		Scheme:   nil,
		Service:  service,
		Recorder: fakeRecorder{},
	}
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	// lb service not found obj case
	errNotFound := errors.New("not found")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	_, err := r.Reconcile(ctx, req)
	assert.Equal(t, err, errNotFound)

	// DeletionTimestamp.IsZero = true and service type is LoadBalancer
	lbService := &v1.Service{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), lbService).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1lbservice := obj.(*v1.Service)
		v1lbservice.Spec.Type = v1.ServiceTypeLoadBalancer
		return nil
	})
	_, err = r.Reconcile(ctx, req)
	assert.Equal(t, err, nil)

	// DeletionTimestamp.IsZero = false and service type is LoadBalancer
	k8sClient.EXPECT().Get(ctx, gomock.Any(), lbService).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1lbservice := obj.(*v1.Service)
		v1lbservice.Spec.Type = v1.ServiceTypeLoadBalancer
		time := metav1.Now()
		v1lbservice.ObjectMeta.DeletionTimestamp = &time
		return nil
	})
	_, err = r.Reconcile(ctx, req)
	assert.Equal(t, err, nil)

	// service type is not LoadBalancer
	k8sClient.EXPECT().Get(ctx, gomock.Any(), lbService).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1lbservice := obj.(*v1.Service)
		v1lbservice.Spec.Type = v1.ServiceTypeClusterIP
		time := metav1.Now()
		v1lbservice.ObjectMeta.DeletionTimestamp = &time
		return nil
	})
	_, err = r.Reconcile(ctx, req)
	assert.Equal(t, err, nil)
}

func TestStartServiceLbController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	commonService := common.Service{
		Client: fakeClient,
	}
	mockMgr := &MockManager{
		scheme: runtime.NewScheme(),
		config: &rest.Config{},
	}

	exitCalled := false // Variable to check if osExit was called
	testCases := []struct {
		name         string
		expectErrStr string
		patches      func() *gomonkey.Patches
	}{
		// expected no error when starting the serviceLb controller
		{
			name: "Start serviceLb Controller",
			patches: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(os.Exit, func(code int) {
					assert.FailNow(t, "os.Exit should not be called")
					return
				})
				patches.ApplyFunc(isServiceLbStatusIpModeSupported, func(c *rest.Config) bool {
					return true
				})
				patches.ApplyMethod(reflect.TypeOf(&ServiceLbReconciler{}), "Start", func(_ *ServiceLbReconciler, r ctrl.Manager) error {
					return nil
				})
				return patches
			},
		},
		{
			name:         "Start serviceLb controller return error",
			expectErrStr: "failed to setupWithManager",
			patches: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(os.Exit, func(code int) {
					exitCalled = true
					return
				})
				patches.ApplyFunc(isServiceLbStatusIpModeSupported, func(c *rest.Config) bool {
					return true
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(&ServiceLbReconciler{}), "setupWithManager", func(_ *ServiceLbReconciler, mgr ctrl.Manager) error {
					return errors.New("failed to setupWithManager")
				})
				return patches
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			patches := testCase.patches()
			defer patches.Reset()

			StartServiceLbController(mockMgr, commonService)

			if testCase.expectErrStr != "" {
				assert.Equal(t, exitCalled, true)
			} else {
				assert.Equal(t, exitCalled, false)
			}
		})
	}
}
