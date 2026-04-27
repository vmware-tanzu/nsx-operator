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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

// noopRouteDNS implements dns.RouteDNSWrite for unit tests that do not assert on DNS calls.
type noopRouteDNS struct{}

func (noopRouteDNS) CreateOrUpdateDNSRecords(context.Context, *dns.AggregatedDNSEndponts) (bool, error) {
	return false, nil
}
func (noopRouteDNS) DeleteDNSRecordByOwnerNN(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (noopRouteDNS) ValidateEndpointsByDNSZone(string, *dns.ResourceRef, []*extdns.Endpoint) ([]dns.EndpointRow, error) {
	return nil, nil
}

func NewFakeServiceLbReconciler() *ServiceLbReconciler {
	return &ServiceLbReconciler{
		Client:    fake.NewClientBuilder().Build(),
		Scheme:    fake.NewClientBuilder().Build().Scheme(),
		Service:   nil,
		DNSWriter: noopRouteDNS{},
		Recorder:  fakeRecorder{},
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

func serviceLbTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(s))
	return s
}

func testNSXServiceForLb() *common.Service {
	return &common.Service{
		NSXClient: &nsx.Client{},
		NSXConfig: &config.NSXOperatorConfig{
			CoeConfig: &config.CoeConfig{EnableVPCNetwork: true},
			NsxConfig: &config.NsxConfig{EnforcementPoint: "vmc-enforcementpoint"},
		},
	}
}

func TestServiceLbReconciler_Reconcile(t *testing.T) {
	ctx := context.Background()
	scheme := serviceLbTestScheme(t)
	nsxSvc := testNSXServiceForLb()

	t.Run("not_found_deletes_dns", func(t *testing.T) {
		spy := &spyDNSWriter{}
		r := &ServiceLbReconciler{
			Client:    fake.NewClientBuilder().WithScheme(scheme).Build(),
			Scheme:    scheme,
			Service:   nsxSvc,
			DNSWriter: spy,
			Recorder:  fakeRecorder{},
		}
		req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "missing"}}
		_, err := r.Reconcile(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, 1, spy.deleteCalls)
	})

	t.Run("loadbalancer_active", func(t *testing.T) {
		svc := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lb", ResourceVersion: "1"},
			Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
			Status: v1.ServiceStatus{
				LoadBalancer: v1.LoadBalancerStatus{
					Ingress: []v1.LoadBalancerIngress{{IP: "192.168.28.1"}},
				},
			},
		}
		r := &ServiceLbReconciler{
			Client:    fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1.Service{}).WithObjects(svc).Build(),
			Scheme:    scheme,
			Service:   nsxSvc,
			DNSWriter: noopRouteDNS{},
			Recorder:  fakeRecorder{},
		}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "lb"}})
		require.NoError(t, err)
	})

	t.Run("loadbalancer_with_deletion_timestamp", func(t *testing.T) {
		ts := metav1.Now()
		svc := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:         "ns",
				Name:              "lb-del",
				ResourceVersion:   "1",
				DeletionTimestamp: &ts,
				Finalizers:        []string{"test.finalizer/nsx-operator"},
			},
			Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
		}
		r := &ServiceLbReconciler{
			Client:    fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1.Service{}).WithObjects(svc).Build(),
			Scheme:    scheme,
			Service:   nsxSvc,
			DNSWriter: noopRouteDNS{},
			Recorder:  fakeRecorder{},
		}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "lb-del"}})
		require.NoError(t, err)
	})

	t.Run("cluster_ip_clears_dns", func(t *testing.T) {
		ts := metav1.Now()
		svc := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:         "ns",
				Name:              "ci",
				ResourceVersion:   "1",
				DeletionTimestamp: &ts,
				Finalizers:        []string{"test.finalizer/nsx-operator"},
			},
			Spec: v1.ServiceSpec{Type: v1.ServiceTypeClusterIP},
		}
		r := &ServiceLbReconciler{
			Client:    fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1.Service{}).WithObjects(svc).Build(),
			Scheme:    scheme,
			Service:   nsxSvc,
			DNSWriter: noopRouteDNS{},
			Recorder:  fakeRecorder{},
		}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ci"}})
		require.NoError(t, err)
	})
}

func TestServiceLbReconciler_StartController_success(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	commonService := common.Service{Client: fakeClient}
	mockMgr := &MockManager{scheme: runtime.NewScheme(), config: &rest.Config{}}

	patches := gomonkey.ApplyFunc(os.Exit, func(code int) {
		assert.FailNow(t, "os.Exit should not be called")
	})
	patches.ApplyFunc(isServiceLbStatusIpModeSupported, func(c *rest.Config) bool { return true })
	patches.ApplyPrivateMethod(reflect.TypeOf(&ServiceLbReconciler{}), "setupWithManager", func(_ *ServiceLbReconciler, mgr ctrl.Manager) error {
		return nil
	})
	defer patches.Reset()

	r := NewServiceLbReconciler(mockMgr, commonService, nil)
	err := r.StartController(mockMgr, nil)
	assert.Nil(t, err)
}

func TestServiceLbReconciler_StartController_setupError(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	commonService := common.Service{Client: fakeClient}
	mockMgr := &MockManager{scheme: runtime.NewScheme(), config: &rest.Config{}}

	patches := gomonkey.ApplyFunc(os.Exit, func(code int) {})
	patches.ApplyFunc(isServiceLbStatusIpModeSupported, func(c *rest.Config) bool { return true })
	patches.ApplyPrivateMethod(reflect.TypeOf(&ServiceLbReconciler{}), "setupWithManager", func(_ *ServiceLbReconciler, mgr ctrl.Manager) error {
		return errors.New("failed to setupWithManager")
	})
	defer patches.Reset()

	r := NewServiceLbReconciler(mockMgr, commonService, nil)
	err := r.StartController(mockMgr, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to setupWithManager")
}
