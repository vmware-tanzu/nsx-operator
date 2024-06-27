/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"

	vpcnetworktesting "github.com/vmware-tanzu/nsx-operator/pkg/controllers/vpcnetwork/testing"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func NewFakeServiceLbReconciler() *ServiceLbReconciler {
	return &ServiceLbReconciler{
		Client:          fake.NewClientBuilder().Build(),
		Scheme:          fake.NewClientBuilder().Build().Scheme(),
		Service:         nil,
		Recorder:        fakeRecorder{},
		NetworkProvider: &vpcnetworktesting.FakeVPCNetworkProvider{},
	}
}

func TestServiceLbController_setServiceLbStatus(t *testing.T) {
	r := NewFakeServiceLbReconciler()
	ctx := context.TODO()
	lbService := &v1.Service{}
	lbService.Spec.Type = v1.ServiceTypeLoadBalancer
	lbService.Labels = map[string]string{
		servicecommon.LabelLbIngressIpMode: servicecommon.LabelLbIngressIpModeVipValue,
	}
	vipIpMode := v1.LoadBalancerIPModeVIP
	lbService.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			IP:     "192.168.28.1",
			IPMode: &vipIpMode,
		},
	}

	// Case: IPMode is set and ingress-ip-mode label is set as vip.
	r.setServiceLbStatus(&ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeVIP, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case: IPMode is set and ingress-ip-mode label is set as proxy.
	lbService.Labels = map[string]string{
		servicecommon.LabelLbIngressIpMode: servicecommon.LabelLbIngressIpModeProxyValue,
	}
	r.setServiceLbStatus(&ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeProxy, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case: IPMode is set and ingress-ip-mode label is not set.
	lbService.Labels = nil
	lbService.Status.LoadBalancer.Ingress[0].IPMode = &vipIpMode
	r.setServiceLbStatus(&ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeProxy, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case IPMode is not set and label is set as VIP.
	lbService.Labels = map[string]string{
		servicecommon.LabelLbIngressIpMode: servicecommon.LabelLbIngressIpModeVipValue,
	}
	lbService.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			IP:     "192.168.28.1",
			IPMode: nil,
		},
	}
	r.setServiceLbStatus(&ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeVIP, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case IPMode is not set and label is set as proxy.
	lbService.Labels = map[string]string{
		servicecommon.LabelLbIngressIpMode: servicecommon.LabelLbIngressIpModeProxyValue,
	}
	lbService.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			IP:     "192.168.28.1",
			IPMode: nil,
		},
	}
	r.setServiceLbStatus(&ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeProxy, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case IPMode is not set and label is not set
	lbService.Labels = nil
	lbService.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			IP:     "192.168.28.1",
			IPMode: nil,
		},
	}
	r.setServiceLbStatus(&ctx, lbService)
	assert.Equal(t, v1.LoadBalancerIPModeProxy, *lbService.Status.LoadBalancer.Ingress[0].IPMode)

	// Case Ingress.IP is not set
	lbService.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			IP:     "",
			IPMode: nil,
		},
	}
	r.setServiceLbStatus(&ctx, lbService)
	assert.Equal(t, (*v1.LoadBalancerIPMode)(nil), lbService.Status.LoadBalancer.Ingress[0].IPMode)
}

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func TestSecurityPolicyReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &servicecommon.Service{
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
		Client:          k8sClient,
		Scheme:          nil,
		Service:         service,
		Recorder:        fakeRecorder{},
		NetworkProvider: &vpcnetworktesting.FakeVPCNetworkProvider{},
	}
	ctx := context.Background()
	req := controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	// case not found obj
	errNotFound := errors.New("not found")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), &v1.Service{}).Return(errNotFound)
	_, err := r.Reconcile(ctx, req)
	assert.Equal(t, err, errNotFound)

	lbService := &v1.Service{}

	mockNamespaceGetFunc := func(annotation map[string]string) {
		k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), &v1.Namespace{}).Return(nil).Do(
			func(_ context.Context, key client.ObjectKey, obj client.Object, option ...client.GetOption) error {
				nsObj := obj.(*v1.Namespace)
				nsObj.Namespace = key.Namespace
				nsObj.Name = key.Name
				nsObj.Annotations = annotation
				return nil
			},
		)
	}

	// case service type is LoadBalancer, error when checking system Namespace.
	k8sClient.EXPECT().Get(ctx, gomock.Any(), lbService).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1lbservice := obj.(*v1.Service)
		v1lbservice.Spec.Type = v1.ServiceTypeLoadBalancer
		return nil
	})
	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), &v1.Namespace{}).Return(errors.New("invalid Namespace"))
	_, err = r.Reconcile(ctx, req)
	assert.EqualError(t, err, "invalid Namespace")

	// case service type is LoadBalancer in system Namespace, loadBalancerClass is not set
	k8sClient.EXPECT().Get(ctx, gomock.Any(), lbService).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1lbservice := obj.(*v1.Service)
		v1lbservice.Spec.Type = v1.ServiceTypeLoadBalancer
		return nil
	})
	mockNamespaceGetFunc(map[string]string{"nsx.vmware.com/nsx_network_config": "true"})
	_, err = r.Reconcile(ctx, req)
	assert.Equal(t, err, nil)

	// case service type is LoadBalancer in system Namespace, loadBalancerClass is not VPC
	nonVPCClass := "not-vpc"
	k8sClient.EXPECT().Get(ctx, gomock.Any(), lbService).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1lbservice := obj.(*v1.Service)
		v1lbservice.Spec.Type = v1.ServiceTypeLoadBalancer
		v1lbservice.Spec.LoadBalancerClass = &nonVPCClass
		return nil
	})
	mockNamespaceGetFunc(map[string]string{"nsx.vmware.com/nsx_network_config": "true"})
	_, err = r.Reconcile(ctx, req)
	assert.Equal(t, err, nil)

	// case DeletionTimestamp.IsZero = false and service type is LoadBalancer in system Namespace with VPC
	k8sClient.EXPECT().Get(ctx, gomock.Any(), lbService).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1lbservice := obj.(*v1.Service)
		v1lbservice.Spec.Type = v1.ServiceTypeLoadBalancer
		v1lbservice.Spec.LoadBalancerClass = &LBServiceClassForVPC
		time := metav1.Now()
		v1lbservice.ObjectMeta.DeletionTimestamp = &time
		return nil
	})
	mockNamespaceGetFunc(map[string]string{"nsx.vmware.com/nsx_network_config": "true"})
	_, err = r.Reconcile(ctx, req)
	assert.Equal(t, err, nil)

	// case DeletionTimestamp.IsZero = false and service type is LoadBalancer in non-system Namespace.
	k8sClient.EXPECT().Get(ctx, gomock.Any(), lbService).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1lbservice := obj.(*v1.Service)
		v1lbservice.Spec.Type = v1.ServiceTypeLoadBalancer
		time := metav1.Now()
		v1lbservice.ObjectMeta.DeletionTimestamp = &time
		return nil
	})
	mockNamespaceGetFunc(nil)
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

func TestSecurityPolicyReconciler_Start(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &servicecommon.Service{}
	var mgr controllerruntime.Manager
	r := &ServiceLbReconciler{
		Client:          k8sClient,
		Scheme:          nil,
		Service:         service,
		Recorder:        fakeRecorder{},
		NetworkProvider: &vpcnetworktesting.FakeVPCNetworkProvider{},
	}

	// Case Manager is initialized
	mgr, _ = controllerruntime.NewManager(&rest.Config{}, manager.Options{})
	err := r.Start(mgr)
	assert.Equal(t, nil, err)
}

func TestListLBServices(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	r := &ServiceLbReconciler{
		Client: k8sClient,
		Scheme: nil,
	}
	testNS := "ns1"
	// Validate error case
	k8sClient.EXPECT().List(gomock.Any(), &v1.ServiceList{}, gomock.Any()).Return(fmt.Errorf("unable to list Services"))
	_, err := r.listLBServices(testNS)
	assert.EqualError(t, err, "unable to list Services")
	// Validate only LoadBalancer type Services are returned
	k8sClient.EXPECT().List(gomock.Any(), &v1.ServiceList{}, gomock.Any()).Return(nil).Do(
		func(_ context.Context, obj client.ObjectList, opts ...client.ListOption) error {
			netList := obj.(*v1.ServiceList)
			netList.Items = []v1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "clusterIP-svc", Namespace: testNS},
					Spec:       v1.ServiceSpec{Type: v1.ServiceTypeClusterIP},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "nodePort-svc", Namespace: testNS},
					Spec:       v1.ServiceSpec{Type: v1.ServiceTypeNodePort},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "lb-svc", Namespace: testNS},
					Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
				},
			}
			return nil
		})

	objs, err := r.listLBServices(testNS)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(objs))
	svc := objs[0]
	assert.Equal(t, "lb-svc", svc.Name)
	assert.Equal(t, testNS, svc.Namespace)
}
