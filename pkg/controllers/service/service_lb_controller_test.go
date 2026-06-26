/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	machineryversion "k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mockdns "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordprovider"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	ctrlcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

func emptyDNSRecordService() *dns.DNSRecordService {
	return &dns.DNSRecordService{DNSRecordStore: dns.BuildDNSRecordStore()}
}

// serviceLbFakeClient builds a fake client; scheme must match the reconciler's Scheme (same *runtime.Scheme instance).
func serviceLbFakeClient(scheme *runtime.Scheme, withStatus bool, objs ...client.Object) client.Client {
	b := fake.NewClientBuilder().WithScheme(scheme)
	if withStatus {
		b = b.WithStatusSubresource(&v1.Service{})
	}
	if len(objs) > 0 {
		b = b.WithObjects(objs...)
	}
	return b.Build()
}

func assignDNSListStubs(m *mockdns.MockDNSRecordProvider) {
	m.EXPECT().ListReferredGatewayNN().Return(sets.New[types.NamespacedName]()).AnyTimes()
	m.EXPECT().ListRecordOwnerResource().Return(nil).AnyTimes()
}

func NewFakeServiceLbReconciler() *ServiceLbReconciler {
	s := runtime.NewScheme()
	if err := v1.AddToScheme(s); err != nil {
		panic(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).Build()
	return &ServiceLbReconciler{
		Client:   c,
		Scheme:   s,
		Service:  nil,
		DNS:      emptyDNSRecordService(),
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

func (m *MockManager) GetControllerOptions() ctrlconfig.Controller {
	return ctrlconfig.Controller{}
}

func (m *MockManager) GetCache() cache.Cache {
	return nil
}

func (m *MockManager) GetRESTMapper() meta.RESTMapper {
	return nil
}

func (m *MockManager) GetLogger() logr.Logger {
	return logr.Discard()
}

func (m *MockManager) Add(runnable manager.Runnable) error {
	return nil
}

func (m *MockManager) Start(context.Context) error {
	return nil
}

func TestServiceLbReconciler_setServiceLbStatus_table(t *testing.T) {
	vipMode := v1.LoadBalancerIPModeVIP
	proxyMode := v1.LoadBalancerIPModeProxy

	makeSvc := func(ipMode *v1.LoadBalancerIPMode, ip string, labelValue string) *v1.Service {
		svc := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
			Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
		}
		svc.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{{IP: ip, IPMode: ipMode}}
		if labelValue != "" {
			svc.Labels = map[string]string{common.LabelLbIngressIpMode: labelValue}
		}
		return svc
	}

	tests := []struct {
		name       string
		svc        *v1.Service
		wantIPMode *v1.LoadBalancerIPMode
	}{
		{
			name:       "vip_label_keeps_vip_mode",
			svc:        makeSvc(&vipMode, "192.168.28.1", common.LabelLbIngressIpModeVipValue),
			wantIPMode: &vipMode,
		},
		{
			name:       "proxy_label_overrides_to_proxy",
			svc:        makeSvc(&vipMode, "192.168.28.1", common.LabelLbIngressIpModeProxyValue),
			wantIPMode: &proxyMode,
		},
		{
			name:       "no_label_defaults_to_proxy",
			svc:        makeSvc(&vipMode, "192.168.28.1", ""),
			wantIPMode: &proxyMode,
		},
		{
			name:       "nil_ipmode_vip_label_sets_vip",
			svc:        makeSvc(nil, "192.168.28.1", common.LabelLbIngressIpModeVipValue),
			wantIPMode: &vipMode,
		},
		{
			name:       "nil_ipmode_proxy_label_sets_proxy",
			svc:        makeSvc(nil, "192.168.28.1", common.LabelLbIngressIpModeProxyValue),
			wantIPMode: &proxyMode,
		},
		{
			name:       "nil_ipmode_no_label_sets_proxy",
			svc:        makeSvc(nil, "192.168.28.1", ""),
			wantIPMode: &proxyMode,
		},
		{
			name:       "empty_ip_leaves_nil_ipmode",
			svc:        makeSvc(nil, "", ""),
			wantIPMode: nil,
		},
	}

	ctx := context.TODO()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewFakeServiceLbReconciler()
			err := r.Client.Create(ctx, tt.svc)
			require.NoError(t, err)

			r.setServiceLbStatus(ctx, tt.svc)

			updatedSvc := &v1.Service{}
			err = r.Client.Get(ctx, types.NamespacedName{Name: tt.svc.Name, Namespace: tt.svc.Namespace}, updatedSvc)
			require.NoError(t, err)
			assert.Equal(t, tt.wantIPMode, updatedSvc.Status.LoadBalancer.Ingress[0].IPMode)
		})
	}
}

func serviceLbTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(s))
	require.NoError(t, v1alpha1.AddToScheme(s))
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

func TestUpdateSuccess(t *testing.T) {
	ctx := context.Background()
	scheme := serviceLbTestScheme(t)

	t.Run("success", func(t *testing.T) {
		svc := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lb", ResourceVersion: "1"},
		}
		c := serviceLbFakeClient(scheme, false, svc)
		r := &ServiceLbReconciler{Client: c, Service: testNSXServiceForLb(), Recorder: fakeRecorder{}}
		err := updateSuccess(r, ctx, svc)
		assert.NoError(t, err)
	})

	t.Run("error_update", func(t *testing.T) {
		svc := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lb-err", ResourceVersion: "1"},
			Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
			Status: v1.ServiceStatus{
				LoadBalancer: v1.LoadBalancerStatus{Ingress: []v1.LoadBalancerIngress{{IP: "1.1.1.1"}}},
			},
		}

		fClient := serviceLbFakeClient(scheme, false, svc)
		c := &failingClient{Client: fClient}

		r := &ServiceLbReconciler{Client: c, Service: testNSXServiceForLb(), Recorder: fakeRecorder{}}
		err := updateSuccess(r, ctx, svc)
		assert.Error(t, err)
	})

	t.Run("clear_dns_and_condition_error", func(t *testing.T) {
		svc := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns", Name: "lb-err-rec", ResourceVersion: "1",
				DeletionTimestamp: func() *metav1.Time { t := metav1.Now(); return &t }(),
				Finalizers:        []string{"test.finalizer/nsx-operator"},
			},
			Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
			Status: v1.ServiceStatus{
				LoadBalancer: v1.LoadBalancerStatus{Ingress: []v1.LoadBalancerIngress{{IP: "1.1.1.1"}}},
				Conditions: []metav1.Condition{{
					Type:   serviceDNSReadyConditionType,
					Status: metav1.ConditionTrue,
				}},
			},
		}

		fClient := serviceLbFakeClient(scheme, false, svc)
		fc := failingClient{Client: fClient}

		mockCtl := gomock.NewController(t)
		t.Cleanup(func() { mockCtl.Finish() })
		m := mockdns.NewMockDNSRecordProvider(mockCtl)
		m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns", "lb-err-rec").Return(true, nil).Times(1)

		r := &ServiceLbReconciler{Client: &fc, Service: testNSXServiceForLb(), Recorder: fakeRecorder{}, DNS: m}
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "lb-err-rec"}})
		assert.NoError(t, err)
		assert.Equal(t, ctrlcommon.ResultRequeueAfter10sec, res)
	})
}

type failingClient struct {
	client.Client
}

type failingStatusWriter struct {
	client.SubResourceWriter
}

func (w failingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return fmt.Errorf("fail")
}
func (c *failingClient) Status() client.SubResourceWriter {
	return failingStatusWriter{}
}

func TestServiceLbReconciler_Reconcile_table(t *testing.T) {
	ctx := context.Background()
	scheme := serviceLbTestScheme(t)
	nsxSvc := testNSXServiceForLb()

	tests := []struct {
		name      string
		objs      []client.Object
		setupMock func(m *mockdns.MockDNSRecordProvider)
		req       types.NamespacedName
		wantRes   *ctrl.Result
		verify    func(t *testing.T, r *ServiceLbReconciler)
	}{
		{
			name: "not_found_deletes_dns",
			objs: nil,
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "dummy", "missing").Return(false, nil).Times(1)
			},
			req:     types.NamespacedName{Namespace: "dummy", Name: "missing"},
			wantRes: &ResultNormal,
		},
		{
			name: "not_found_deletes_dns_error",
			objs: nil,
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "dummy", "missing").Return(false, fmt.Errorf("mock err")).Times(1)
			},
			req:     types.NamespacedName{Namespace: "dummy", Name: "missing"},
			wantRes: &ctrlcommon.ResultRequeueAfter10sec,
		},
		{
			name: "loadbalancer_active",
			objs: []client.Object{&v1.Service{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lb", ResourceVersion: "1"},
				Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
				Status: v1.ServiceStatus{
					LoadBalancer: v1.LoadBalancerStatus{Ingress: []v1.LoadBalancerIngress{{IP: "192.168.28.1"}}},
				},
			}},
			req: types.NamespacedName{Namespace: "ns", Name: "lb"},
		},
		{
			name: "loadbalancer_with_deletion_timestamp_clears_dns",
			objs: []client.Object{&v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "lb-del", ResourceVersion: "1",
					DeletionTimestamp: func() *metav1.Time { t := metav1.Now(); return &t }(),
					Finalizers:        []string{"test.finalizer/nsx-operator"},
				},
				Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
			}},
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns", "lb-del").Return(false, nil).Times(1)
			},
			req:     types.NamespacedName{Namespace: "ns", Name: "lb-del"},
			wantRes: &ResultNormal,
		},
		{
			name: "loadbalancer_with_deletion_timestamp_clears_dns_error",
			objs: []client.Object{&v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "lb-del-err", ResourceVersion: "1",
					DeletionTimestamp: func() *metav1.Time { t := metav1.Now(); return &t }(),
					Finalizers:        []string{"test.finalizer/nsx-operator"},
				},
				Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
			}},
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns", "lb-del-err").Return(false, fmt.Errorf("mock err")).Times(1)
			},
			req:     types.NamespacedName{Namespace: "ns", Name: "lb-del-err"},
			wantRes: &ctrlcommon.ResultRequeueAfter10sec,
		},
		{
			name: "skip_annotation_clears_dns",
			objs: []client.Object{&v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       "ns",
					Name:            "lb-skip",
					ResourceVersion: "1",
					Annotations:     map[string]string{common.AnnotationsDNSSkip: "true", common.AnnotationDNSHostnameKey: "a.com"},
				},
				Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
			}},
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns", "lb-skip").Return(false, nil).Times(1)
			},
			req:     types.NamespacedName{Namespace: "ns", Name: "lb-skip"},
			wantRes: &ResultNormal,
		},
		{
			name: "reconcile_dns_error_still_updates_status",
			objs: []client.Object{&v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "lb-dns-err", ResourceVersion: "1",
					Annotations: map[string]string{common.AnnotationDNSHostnameKey: "a.com"},
				},
				Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
				Status: v1.ServiceStatus{
					LoadBalancer: v1.LoadBalancerStatus{Ingress: []v1.LoadBalancerIngress{{IP: "192.168.28.1"}}},
				},
			}},
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, fmt.Errorf("mock dns error")).Times(1)
			},
			req:     types.NamespacedName{Namespace: "ns", Name: "lb-dns-err"},
			wantRes: &ctrlcommon.ResultRequeueAfter10sec,
			verify: func(t *testing.T, r *ServiceLbReconciler) {
				svc := &v1.Service{}
				err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "lb-dns-err"}, svc)
				require.NoError(t, err)
				require.NotNil(t, svc.Status.LoadBalancer.Ingress[0].IPMode)
				assert.Equal(t, v1.LoadBalancerIPModeProxy, *svc.Status.LoadBalancer.Ingress[0].IPMode)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCtl := gomock.NewController(t)
			t.Cleanup(func() { mockCtl.Finish() })
			m := mockdns.NewMockDNSRecordProvider(mockCtl)
			assignDNSListStubs(m)
			if tt.setupMock != nil {
				tt.setupMock(m)
			}

			var dnsProvider dns.DNSRecordProvider = m
			// For cases without mock expectations, use the empty in-memory service.
			if tt.setupMock == nil {
				dnsProvider = emptyDNSRecordService()
			}
			r := &ServiceLbReconciler{
				Client:   serviceLbFakeClient(scheme, true, tt.objs...),
				Scheme:   scheme,
				Service:  nsxSvc,
				DNS:      dnsProvider,
				Recorder: fakeRecorder{},
			}
			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: tt.req})
			require.NoError(t, err)
			if tt.wantRes != nil {
				require.Equal(t, *tt.wantRes, res)
			}
			if tt.verify != nil {
				tt.verify(t, r)
			}
		})
	}
}

func TestServiceLbReconciler_CollectGarbage_table(t *testing.T) {
	ctx := context.Background()
	scheme := serviceLbTestScheme(t)

	tests := []struct {
		name      string
		dns       dns.DNSRecordProvider
		objs      []client.Object
		setupMock func(m *mockdns.MockDNSRecordProvider)
		wantErr   bool
	}{
		{
			name: "nil_dns_is_noop",
			dns:  nil,
		},
		{
			name: "empty_store_is_noop",
			dns:  emptyDNSRecordService(),
		},
		{
			name: "prunes_stale_service_owners",
			objs: []client.Object{&v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "keep",
					Annotations: map[string]string{common.AnnotationDNSHostnameKey: "app.example.com"},
				},
				Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
			}},
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				keepNN := types.NamespacedName{Namespace: "ns", Name: "keep"}
				staleNN := types.NamespacedName{Namespace: "ns", Name: "stale"}
				m.EXPECT().ListReferredGatewayNN().Return(sets.New[types.NamespacedName]()).AnyTimes()
				m.EXPECT().ListRecordOwnerResource().Return(map[string]sets.Set[types.NamespacedName]{
					dns.ResourceKindService: sets.New(keepNN, staleNN),
				}).Times(1)
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, staleNN.Namespace, staleNN.Name).Return(true, nil).Times(1)
			},
		},
		{
			name: "prunes_stale_service_owners_error",
			objs: []client.Object{&v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "keep",
					Annotations: map[string]string{common.AnnotationDNSHostnameKey: "app.example.com"},
				},
				Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
			}},
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				keepNN := types.NamespacedName{Namespace: "ns", Name: "keep"}
				staleNN := types.NamespacedName{Namespace: "ns", Name: "stale"}
				m.EXPECT().ListReferredGatewayNN().Return(sets.New[types.NamespacedName]()).AnyTimes()
				m.EXPECT().ListRecordOwnerResource().Return(map[string]sets.Set[types.NamespacedName]{
					dns.ResourceKindService: sets.New(keepNN, staleNN),
				}).Times(1)
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, staleNN.Namespace, staleNN.Name).Return(false, fmt.Errorf("mock error")).Times(1)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dnsProvider := tt.dns
			if tt.setupMock != nil {
				mockCtl := gomock.NewController(t)
				t.Cleanup(func() { mockCtl.Finish() })
				m := mockdns.NewMockDNSRecordProvider(mockCtl)
				tt.setupMock(m)
				dnsProvider = m
			}
			r := &ServiceLbReconciler{
				Client: serviceLbFakeClient(scheme, true, tt.objs...),
				Scheme: scheme,
				DNS:    dnsProvider,
			}
			err := r.CollectGarbage(ctx)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPredicateNetworkInfoAllowedDNSDomainsChanged(t *testing.T) {
	p := predicateNetworkInfoAllowedDNSDomainsChanged()

	assert.False(t, p.Create(event.CreateEvent{}))
	assert.False(t, p.Delete(event.DeleteEvent{}))
	assert.False(t, p.Generic(event.GenericEvent{}))

	// Test Update
	tests := []struct {
		name string
		old  client.Object
		new  client.Object
		want bool
	}{
		{
			name: "different types",
			old:  &v1.Service{},
			new:  &v1.Service{},
			want: false,
		},
		{
			name: "domains same",
			old:  &v1alpha1.NetworkInfo{AllowedDNSDomains: []string{"a"}},
			new:  &v1alpha1.NetworkInfo{AllowedDNSDomains: []string{"a"}},
			want: false,
		},
		{
			name: "domains changed",
			old:  &v1alpha1.NetworkInfo{AllowedDNSDomains: []string{"a"}},
			new:  &v1alpha1.NetworkInfo{AllowedDNSDomains: []string{"b"}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.Update(event.UpdateEvent{ObjectOld: tt.old, ObjectNew: tt.new})
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestServiceLbReconciler_RestoreReconcile(t *testing.T) {
	r := &ServiceLbReconciler{}
	err := r.RestoreReconcile()
	assert.NoError(t, err)
}

func TestServiceLbReconciler_StartController(t *testing.T) {
	mockMgr := &MockManager{scheme: runtime.NewScheme(), config: &rest.Config{}}
	r := &ServiceLbReconciler{}
	// will return error from Start because dependencies for complete are missing
	err := r.StartController(mockMgr, nil)
	assert.Error(t, err)
}

func TestServiceLbReconciler_setupWithManager(t *testing.T) {
	mockMgr := &MockManager{scheme: runtime.NewScheme(), config: &rest.Config{}}
	r := &ServiceLbReconciler{}
	err := r.Start(mockMgr)
	assert.Error(t, err)
}

func TestNewServiceLbReconciler_whenIpModeSupported(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	commonService := common.Service{Client: fakeClient}
	mockMgr := &MockManager{scheme: runtime.NewScheme(), config: &rest.Config{}}

	patches := gomonkey.ApplyFunc(isServiceLbStatusIpModeSupported, func(c *rest.Config) (bool, error) { return true, nil })
	defer patches.Reset()

	r := NewServiceLbReconciler(mockMgr, commonService, nil)
	require.NotNil(t, r)
}

func TestGetLoadBalancerServicesWithDNS(t *testing.T) {
	ctx := context.Background()
	scheme := serviceLbTestScheme(t)

	nonLBSvc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "non-lb", Annotations: map[string]string{
			common.AnnotationDNSHostnameKey: "no-lb.example.com",
		}},
		Spec: v1.ServiceSpec{Type: v1.ServiceTypeClusterIP},
	}

	makeLbSvc := func(name string, annotations map[string]string) *v1.Service {
		return &v1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name, Annotations: annotations},
			Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
		}
	}

	objs := []client.Object{
		nonLBSvc,
		makeLbSvc("eligible", map[string]string{common.AnnotationDNSHostnameKey: "a.example.com"}),
		makeLbSvc("multi-hostnames", map[string]string{common.AnnotationDNSHostnameKey: "a.example.com,b.example.com"}),
		makeLbSvc("opted-out", map[string]string{
			common.AnnotationDNSHostnameKey: "c.example.com",
			common.AnnotationsDNSSkip:       "",
		}),
		makeLbSvc("no-annotation", map[string]string{}),
	}
	wantNNs := []types.NamespacedName{{Namespace: "ns", Name: "eligible"}, {Namespace: "ns", Name: "multi-hostnames"}}
	wantMiss := []types.NamespacedName{{Namespace: "ns", Name: "non-lb"}, {Namespace: "ns", Name: "no-annotation"},
		{Namespace: "ns", Name: "opted-out"}}

	c := serviceLbFakeClient(scheme, false, objs...)
	gotSvcs, err := getLoadBalancerServicesWithDNS(ctx, c)
	require.NoError(t, err)
	got := sets.New[types.NamespacedName]()
	for _, svc := range gotSvcs {
		got.Insert(types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name})
	}
	for _, nn := range wantNNs {
		require.True(t, got.Has(nn), "expected %v to be in set", nn)
	}
	for _, nn := range wantMiss {
		require.False(t, got.Has(nn), "expected %v NOT to be in set", nn)
	}
}

func TestEnqueueLBServiceRequestsFromNetworkInfo_skipAnnotation(t *testing.T) {
	ctx := context.Background()
	scheme := serviceLbTestScheme(t)
	ni := &v1alpha1.NetworkInfo{}

	makeLbSvc := func(name string, annotations map[string]string) client.Object {
		return &v1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name, Annotations: annotations},
			Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
		}
	}

	// "eligible" has a hostname annotation and no skip → should be enqueued.
	// "skipped" has both annotations → should NOT be enqueued.
	c := serviceLbFakeClient(scheme, false,
		makeLbSvc("eligible", map[string]string{common.AnnotationDNSHostnameKey: "app.example.com"}),
		makeLbSvc("skipped", map[string]string{
			common.AnnotationDNSHostnameKey: "app.example.com",
			common.AnnotationsDNSSkip:       "",
		}),
	)
	r := &ServiceLbReconciler{Client: c, DNS: emptyDNSRecordService()}
	reqs := r.enqueueLBServiceRequestsFromNetworkInfo(ctx, ni)
	require.Len(t, reqs, 1)
	require.Equal(t, types.NamespacedName{Namespace: "ns", Name: "eligible"}, reqs[0].NamespacedName)

	t.Run("invalid_object", func(t *testing.T) {
		reqs := r.enqueueLBServiceRequestsFromNetworkInfo(ctx, &v1.Pod{})
		assert.Nil(t, reqs)
	})

	t.Run("list_error", func(t *testing.T) {
		fc := &failingClient{Client: c}
		rErr := &ServiceLbReconciler{Client: fc, DNS: emptyDNSRecordService()}
		reqs := rErr.enqueueLBServiceRequestsFromNetworkInfo(ctx, ni)
		assert.Nil(t, reqs)
	})
}

// failingClient needs to override List to return an error for this test
func (c *failingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return fmt.Errorf("list error")
}

func TestIsServiceLbStatusIpModeSupported(t *testing.T) {
	c := &rest.Config{}

	t.Run("NewForConfig returns error", func(t *testing.T) {
		patches := gomonkey.ApplyFunc(clientset.NewForConfig, func(_ *rest.Config) (*clientset.Clientset, error) {
			return nil, errors.New("mock NewForConfig error")
		})
		defer patches.Reset()

		supported, err := isServiceLbStatusIpModeSupported(c)
		assert.False(t, supported)
		assert.ErrorContains(t, err, "mock NewForConfig error")
	})

	t.Run("ServerVersion returns error", func(t *testing.T) {
		// Mock discovery client's ServerVersion method
		patches := gomonkey.ApplyMethodFunc(reflect.TypeOf(&discovery.DiscoveryClient{}), "ServerVersion", func() (*machineryversion.Info, error) {
			return nil, errors.New("mock ServerVersion error")
		})
		defer patches.Reset()

		supported, err := isServiceLbStatusIpModeSupported(c)
		assert.False(t, supported)
		assert.ErrorContains(t, err, "mock ServerVersion error")
	})

	t.Run("ParseGeneric returns error", func(t *testing.T) {
		patches := gomonkey.ApplyMethodFunc(reflect.TypeOf(&discovery.DiscoveryClient{}), "ServerVersion", func() (*machineryversion.Info, error) {
			return &machineryversion.Info{GitVersion: "invalid"}, nil
		})
		defer patches.Reset()

		supported, err := isServiceLbStatusIpModeSupported(c)
		assert.False(t, supported)
		assert.ErrorContains(t, err, "could not parse")
	})

	t.Run("version less than 1.29.0", func(t *testing.T) {
		patches := gomonkey.ApplyMethodFunc(reflect.TypeOf(&discovery.DiscoveryClient{}), "ServerVersion", func() (*machineryversion.Info, error) {
			return &machineryversion.Info{GitVersion: "v1.28.0"}, nil
		})
		defer patches.Reset()

		supported, err := isServiceLbStatusIpModeSupported(c)
		assert.False(t, supported)
		assert.NoError(t, err)
	})

	t.Run("version greater than 1.29.0", func(t *testing.T) {
		patches := gomonkey.ApplyMethodFunc(reflect.TypeOf(&discovery.DiscoveryClient{}), "ServerVersion", func() (*machineryversion.Info, error) {
			return &machineryversion.Info{GitVersion: "v1.30.0"}, nil
		})
		defer patches.Reset()

		supported, err := isServiceLbStatusIpModeSupported(c)
		assert.True(t, supported)
		assert.NoError(t, err)
	})
}
