/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	nsxmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = gatewayv1.Install(scheme)
}

var scheme = runtime.NewScheme()

// MockManager implements the controller-runtime Manager subset used by NewGatewayReconciler.
type MockManager struct {
	ctrl.Manager
	client client.Client
	scheme *runtime.Scheme
}

func newMockManager(c client.Client, s *runtime.Scheme) *MockManager {
	return &MockManager{client: c, scheme: s}
}
func (m *MockManager) GetClient() client.Client                        { return m.client }
func (m *MockManager) GetScheme() *runtime.Scheme                      { return m.scheme }
func (m *MockManager) GetEventRecorderFor(string) record.EventRecorder { return nil }
func (m *MockManager) Add(manager.Runnable) error                      { return nil }
func (m *MockManager) Start(context.Context) error                     { return nil }

func createFakeManagerAndClient(objs ...client.Object) (ctrl.Manager, client.Client) {
	b := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&gatewayv1.Gateway{}, &gatewayv1.ListenerSet{}).
		WithIndex(&gatewayv1.ListenerSet{}, listenerSetParentGatewayIndex, listenerSetParentGatewayIndexFunc)
	if len(objs) > 0 {
		b.WithObjects(objs...)
	}
	fc := b.Build()
	return newMockManager(fc, scheme), fc
}

// fakeGatewayReconciler builds a reconciler backed by a fake client containing objs.
// InitializeResourceStore is patched to prevent nil-NSXClient panics in tests.
func fakeGatewayReconciler(t *testing.T, objs ...client.Object) (*GatewayReconciler, client.Client) {
	t.Helper()
	mgr, fc := createFakeManagerAndClient(objs...)
	svc := servicecommon.Service{
		Client:    fc,
		NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{}},
	}
	p := gomonkey.ApplyMethod(reflect.TypeOf(&svc), "InitializeResourceStore",
		func(_ *servicecommon.Service, wg *sync.WaitGroup, _ chan error, _ string, _ []nsxmodel.Tag, _ servicecommon.Store) {
			wg.Done()
		})
	dnsService, err := dns.InitializeDNSRecordService(svc, nil)
	t.Cleanup(p.Reset)
	require.NoError(t, err)
	r := NewGatewayReconciler(mgr, dnsService)
	r.StatusUpdater = newFakeStatusUpdater()
	return r, fc
}

// statusUpdater method name constants used when validating calls in the status updater.
const (
	callIncreaseSyncTotal          = "IncreaseSyncTotal"
	callIncreaseUpdateTotal        = "IncreaseUpdateTotal"
	callIncreaseDeleteTotal        = "IncreaseDeleteTotal"
	callIncreaseDeleteSuccessTotal = "IncreaseDeleteSuccessTotal"
	callIncreaseDeleteFailTotal    = "IncreaseDeleteFailTotal"
	callUpdateSuccess              = "UpdateSuccess"
	callUpdateFail                 = "UpdateFail"
	callDeleteSuccess              = "DeleteSuccess"
	callDeleteFail                 = "DeleteFail"
)

type fakeStatusUpdater struct {
	calls []string
}

func (u *fakeStatusUpdater) UpdateSuccess(_ context.Context, _ client.Object, _ common.UpdateSuccessStatusFn, _ ...interface{}) {
	u.calls = append(u.calls, callUpdateSuccess)
}

func (u *fakeStatusUpdater) UpdateFail(_ context.Context, _ client.Object, _ error, _ string, _ common.UpdateFailStatusFn, _ ...interface{}) {
	u.calls = append(u.calls, callUpdateFail)
}

func (u *fakeStatusUpdater) DeleteSuccess(_ types.NamespacedName, _ client.Object) {
	u.calls = append(u.calls, callDeleteSuccess)
}

func (u *fakeStatusUpdater) IncreaseSyncTotal() {
	u.calls = append(u.calls, callIncreaseSyncTotal)
}

func (u *fakeStatusUpdater) IncreaseUpdateTotal() {
	u.calls = append(u.calls, callIncreaseUpdateTotal)
}

func (u *fakeStatusUpdater) IncreaseDeleteTotal() {
	u.calls = append(u.calls, callIncreaseDeleteTotal)
}
func (u *fakeStatusUpdater) IncreaseDeleteSuccessTotal() {
	u.calls = append(u.calls, callIncreaseDeleteSuccessTotal)
}

func (u *fakeStatusUpdater) IncreaseDeleteFailTotal() {
	u.calls = append(u.calls, callIncreaseDeleteFailTotal)
}

func (u *fakeStatusUpdater) DeleteFail(_ types.NamespacedName, _ client.Object, _ error) {
	u.calls = append(u.calls, callDeleteFail)
}

func (u *fakeStatusUpdater) validateCalls(t *testing.T, wantCalls []string) {
	assert.Equal(t, wantCalls, u.calls, "statusUpdater call sequence mismatch")
}

func newFakeStatusUpdater() statusUpdater {
	return &fakeStatusUpdater{calls: make([]string, 0)}
}

// managedGW returns a copy of base with each opt applied in order.
func managedGW(base gatewayv1.Gateway, extraOpts ...func(*gatewayv1.Gateway)) *gatewayv1.Gateway {
	gw := base
	for _, opt := range extraOpts {
		opt(&gw)
	}
	return &gw
}

func Test_shouldProcessGateway(t *testing.T) {
	tests := []struct {
		className string
		want      bool
	}{
		{common.ManagedK8sGatewayClassIstio, true},
		{"other", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.className, func(t *testing.T) {
			gw := &gatewayv1.Gateway{Spec: gatewayv1.GatewaySpec{GatewayClassName: gatewayv1.ObjectName(tt.className)}}
			assert.Equal(t, tt.want, shouldProcessGateway(gw))
		})
	}
}

func Test_hasUsableGatewayIP(t *testing.T) {
	tests := []struct {
		name string
		gw   *gatewayv1.Gateway
		want bool
	}{
		{"nil gateway", nil, false},
		{"no addresses", &gatewayv1.Gateway{}, false},
		{"valid IP", &gatewayv1.Gateway{Status: gatewayv1.GatewayStatus{
			Addresses: []gatewayv1.GatewayStatusAddress{{Type: &ipType, Value: "192.168.1.1"}},
		}}, true},
		{"invalid IP string", &gatewayv1.Gateway{Status: gatewayv1.GatewayStatus{
			Addresses: []gatewayv1.GatewayStatusAddress{{Type: &ipType, Value: "not-an-ip"}},
		}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasUsableGatewayIP(tt.gw))
		})
	}
}

func Test_collectIPsFromGateway(t *testing.T) {
	hostname := gatewayv1.HostnameAddressType
	tests := []struct {
		name    string
		gw      *gatewayv1.Gateway
		wantLen int
		wantIPs []net.IP //
	}{
		{"nil", nil, 0, nil},
		{"no addresses", &gatewayv1.Gateway{}, 0, nil},
		{"IPv4 with explicit type", &gatewayv1.Gateway{Status: gatewayv1.GatewayStatus{
			Addresses: []gatewayv1.GatewayStatusAddress{{Type: &ipType, Value: "10.0.0.1"}},
		}}, 1, []net.IP{net.ParseIP("10.0.0.1")}},
		// nil addr.Type is treated as IPAddressType per source
		{"nil addr type treated as IP", &gatewayv1.Gateway{Status: gatewayv1.GatewayStatus{
			Addresses: []gatewayv1.GatewayStatusAddress{{Type: nil, Value: "10.0.0.2"}},
		}}, 1, []net.IP{net.ParseIP("10.0.0.2")}},
		{"hostname type is skipped", &gatewayv1.Gateway{Status: gatewayv1.GatewayStatus{
			Addresses: []gatewayv1.GatewayStatusAddress{{Type: &hostname, Value: "foo.example.com"}},
		}}, 0, nil},
		{"invalid IP string is skipped", &gatewayv1.Gateway{Status: gatewayv1.GatewayStatus{
			Addresses: []gatewayv1.GatewayStatusAddress{{Type: &ipType, Value: "bad"}},
		}}, 0, nil},
		{"dual stack IPv4+IPv6", &gatewayv1.Gateway{Status: gatewayv1.GatewayStatus{
			Addresses: []gatewayv1.GatewayStatusAddress{
				{Type: &ipType, Value: "10.0.0.1"},
				{Type: &ipType, Value: "2001:db8::1"},
			},
		}}, 2, []net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("2001:db8::1")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectIPsFromGateway(tt.gw)
			require.Len(t, got, tt.wantLen)
			if tt.wantIPs != nil {
				assert.ElementsMatch(t, tt.wantIPs, got)
			}
		})
	}
}

func Test_collectHostnamesInListeners(t *testing.T) {
	h := gatewayv1.Hostname("svc.example.com")
	empty := gatewayv1.Hostname("")
	tests := []struct {
		name      string
		listeners []gatewayv1.Listener
		wantLen   int
	}{
		{"nil", nil, 0},
		{"one valid hostname", []gatewayv1.Listener{{Hostname: &h}}, 1},
		{"empty hostname skipped", []gatewayv1.Listener{{Hostname: &empty}}, 0},
		{"nil hostname skipped", []gatewayv1.Listener{{Hostname: nil}}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Len(t, collectHostnamesInListeners(tt.listeners), tt.wantLen)
		})
	}
}

func Test_collectHostnamesFromListenerSet(t *testing.T) {
	h := gatewayv1.Hostname("ls.example.com")
	ls := gatewayv1.ListenerSet{Spec: gatewayv1.ListenerSetSpec{
		Listeners: []gatewayv1.ListenerEntry{{Name: "l1", Hostname: &h}},
	}}
	got := collectHostnamesFromListenerSet(ls)
	require.Len(t, got, 1)
	assert.Equal(t, "ls.example.com", got[0])
}

func Test_mergeDNSReadyCondition(t *testing.T) {
	cond := metav1.Condition{Type: conditionTypeDNSReady, Status: metav1.ConditionTrue, Reason: reasonDNSRecordConfigured}
	t.Run("appended to empty slice", func(t *testing.T) {
		out := mergeDNSReadyCondition(nil, cond)
		require.Len(t, out, 1)
		assert.Equal(t, metav1.ConditionTrue, out[0].Status)
	})
	t.Run("existing DNSReady is updated in place", func(t *testing.T) {
		existing := []metav1.Condition{{Type: conditionTypeDNSReady, Status: metav1.ConditionFalse, Reason: reasonDNSRecordFailed}}
		out := mergeDNSReadyCondition(existing, cond)
		require.Len(t, out, 1)
		assert.Equal(t, metav1.ConditionTrue, out[0].Status)
	})
	t.Run("other conditions are preserved, DNSReady appended", func(t *testing.T) {
		existing := []metav1.Condition{{Type: "Other", Status: metav1.ConditionTrue}}
		out := mergeDNSReadyCondition(existing, cond)
		require.Len(t, out, 2)
		assert.Equal(t, conditionTypeDNSReady, out[1].Type)
	})
}

func Test_RestoreReconcile(t *testing.T) {
	r, _ := fakeGatewayReconciler(t)
	assert.NoError(t, r.RestoreReconcile())
}

var (
	ipType      = gatewayv1.IPAddressType
	testGateway = gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1", Name: "gw1",
		},
		Spec:   gatewayv1.GatewaySpec{GatewayClassName: gatewayv1.ObjectName(common.ManagedK8sGatewayClassIstio)},
		Status: gatewayv1.GatewayStatus{Addresses: []gatewayv1.GatewayStatusAddress{{Type: &ipType, Value: "10.0.0.1"}}},
	}
	h = gatewayv1.Hostname("svc.example.com")
)

func Test_Reconcile_Delete(t *testing.T) {
	wantCalls := []string{callIncreaseSyncTotal, callIncreaseDeleteTotal, callDeleteSuccess}
	for _, tt := range []struct {
		name string
		gw   *gatewayv1.Gateway
	}{
		{
			name: "deletion timestamp set",
			gw: managedGW(testGateway, func(gateway *gatewayv1.Gateway) {
				now := metav1.Now()
				gateway.ObjectMeta.DeletionTimestamp = &now
				gateway.ObjectMeta.Finalizers = []string{"x"}
			}),
		}, {
			name: "no usable IP",
			gw: managedGW(testGateway, func(gateway *gatewayv1.Gateway) {
				gateway.Status.Addresses = nil
			}),
		}, {
			name: "unmanaged gateway class",
			gw: managedGW(testGateway, func(gateway *gatewayv1.Gateway) {
				gateway.Status.Addresses = nil
			}),
		}, {
			name: "gateway not found",
			gw:   nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var objs []client.Object
			if tt.gw != nil {
				objs = append(objs, tt.gw)
			}
			r, _ := fakeGatewayReconciler(t, objs...)
			result, err := r.Reconcile(context.Background(),
				ctrl.Request{NamespacedName: types.NamespacedName{Namespace: testGateway.Namespace, Name: testGateway.Name}})
			assert.NoError(t, err)
			assert.False(t, result.Requeue)
			if wantCalls != nil {
				updater := r.StatusUpdater.(*fakeStatusUpdater)
				updater.validateCalls(t, wantCalls)
			}
		})
	}
}

func Test_Reconcile_Success(t *testing.T) {
	wantCalls := []string{callIncreaseSyncTotal, callIncreaseUpdateTotal, callUpdateSuccess}
	gw := managedGW(testGateway, func(gateway *gatewayv1.Gateway) {
		gateway.Spec.Listeners = []gatewayv1.Listener{{Name: "l1", Hostname: &h, Port: 80, Protocol: gatewayv1.HTTPProtocolType}}
	})
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}}
	r, _ := fakeGatewayReconciler(t, gw, ns)
	result, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "gw1"}})
	assert.NoError(t, err)
	assert.False(t, result.Requeue)
	updater := r.StatusUpdater.(*fakeStatusUpdater)
	updater.validateCalls(t, wantCalls)
}

func Test_Reconcile_Success_WithListenerSet(t *testing.T) {
	wantCalls := []string{callIncreaseSyncTotal, callIncreaseUpdateTotal, callUpdateSuccess}
	gwH := gatewayv1.Hostname("gw.example.com")
	lsH := gatewayv1.Hostname("ls.example.com")
	gwKind := gatewayv1.Kind("Gateway")
	gwGroup := gatewayv1.Group(gatewayv1.GroupName)
	gwNS := gatewayv1.Namespace("ns1")
	gw := managedGW(testGateway, func(gateway *gatewayv1.Gateway) {
		gateway.UID = "gw-uid-1"
		gateway.Spec.Listeners = []gatewayv1.Listener{{Name: "l1", Hostname: &gwH, Port: 80, Protocol: gatewayv1.HTTPProtocolType}}
	})
	ls := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ls1", UID: "ls-uid-1"},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{Kind: &gwKind, Group: &gwGroup, Name: "gw1", Namespace: &gwNS},
			Listeners: []gatewayv1.ListenerEntry{{Name: "l1", Hostname: &lsH}},
		},
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}}
	r, _ := fakeGatewayReconciler(t, gw, ls, ns)
	result, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "gw1"}})
	assert.NoError(t, err)
	assert.False(t, result.Requeue)
	updater := r.StatusUpdater.(*fakeStatusUpdater)
	updater.validateCalls(t, wantCalls)
}

func Test_Reconcile_DeleteFail(t *testing.T) {
	wantCalls := []string{callIncreaseSyncTotal, callIncreaseDeleteTotal, callDeleteFail}
	now := metav1.Now()
	gw := managedGW(testGateway, func(gateway *gatewayv1.Gateway) {
		gateway.ObjectMeta.DeletionTimestamp = &now
		gateway.ObjectMeta.Finalizers = []string{"x"}
	})
	r, _ := fakeGatewayReconciler(t, gw)
	deleteErr := fmt.Errorf("dns delete failed")
	p := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "DeleteAllDNSRecordsInGateway",
		func(_ *dns.DNSRecordService, _ context.Context, _, _ string) error { return deleteErr })
	defer p.Reset()

	result, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "gw1"}})
	assert.Equal(t, deleteErr, err)
	assert.True(t, result.Requeue || result.RequeueAfter > 0)
	updater := r.StatusUpdater.(*fakeStatusUpdater)
	updater.validateCalls(t, wantCalls)
}

func Test_Reconcile_CreateDNSRecordsFail(t *testing.T) {
	wantCalls := []string{callIncreaseSyncTotal, callIncreaseUpdateTotal, callUpdateFail}
	gw := managedGW(testGateway, func(gateway *gatewayv1.Gateway) {
		gateway.ObjectMeta.UID = "gw-uid-1"
		gateway.Spec.Listeners = []gatewayv1.Listener{{Name: "l1", Hostname: &h, Port: 80, Protocol: gatewayv1.HTTPProtocolType}}
	})
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}}
	r, _ := fakeGatewayReconciler(t, gw, ns)
	createErr := fmt.Errorf("dns create failed")
	p := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateDNSRecords",
		func(_ *dns.DNSRecordService, _ context.Context, _ *dns.Record) error { return createErr })
	defer p.Reset()

	result, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "gw1"}})
	assert.Equal(t, createErr, err)
	assert.True(t, result.Requeue || result.RequeueAfter > 0)
	updater := r.StatusUpdater.(*fakeStatusUpdater)
	updater.validateCalls(t, wantCalls)
}

// Test_Reconcile_BuildDNSRecordsFail covers the namespace-not-found branch in buildDNSRecordsForGateway.
func Test_Reconcile_BuildDNSRecordsFail(t *testing.T) {
	wantCalls := []string{callIncreaseSyncTotal, callIncreaseUpdateTotal}
	gw := managedGW(testGateway, func(gateway *gatewayv1.Gateway) {
		gateway.ObjectMeta.UID = "gw-uid-1"
		gateway.Spec.Listeners = []gatewayv1.Listener{{Name: "l1", Hostname: &h, Port: 80, Protocol: gatewayv1.HTTPProtocolType}}
	})
	r, _ := fakeGatewayReconciler(t, gw) // Namespace object deliberately absent
	_, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "gw1"}})
	assert.Error(t, err)
	updater := r.StatusUpdater.(*fakeStatusUpdater)
	updater.validateCalls(t, wantCalls)
}

// Test_Reconcile_OrphanDeleteFail covers the branch where DeleteOrphanedDNSRecordsInGateway fails
// but all CreateOrUpdateDNSRecords calls succeed: the reconciler requeues but returns nil error.
func Test_Reconcile_OrphanDeleteFail(t *testing.T) {
	wantCalls := []string{callIncreaseSyncTotal, callIncreaseUpdateTotal}
	gw := managedGW(testGateway, func(gateway *gatewayv1.Gateway) {
		gateway.ObjectMeta.UID = "gw-uid-1"
		gateway.Spec.Listeners = []gatewayv1.Listener{{Name: "l1", Hostname: &h, Port: 80, Protocol: gatewayv1.HTTPProtocolType}}
	})
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}}
	r, _ := fakeGatewayReconciler(t, gw, ns)
	p := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "DeleteOrphanedDNSRecordsInGateway",
		func(_ *dns.DNSRecordService, _ context.Context, _, _ string, _ []*dns.ResourceRef) error {
			return fmt.Errorf("orphan delete failed")
		})
	defer p.Reset()

	result, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "gw1"}})
	assert.NoError(t, err) // nil error despite orphan delete failure
	assert.True(t, result.Requeue || result.RequeueAfter > 0)
	updater := r.StatusUpdater.(*fakeStatusUpdater)
	updater.validateCalls(t, wantCalls)
}

func Test_PredicateFuncsGateway(t *testing.T) {
	withIP := &gatewayv1.Gateway{
		Spec:   gatewayv1.GatewaySpec{GatewayClassName: gatewayv1.ObjectName(common.ManagedK8sGatewayClassIstio)},
		Status: gatewayv1.GatewayStatus{Addresses: []gatewayv1.GatewayStatusAddress{{Type: &ipType, Value: "10.0.0.1"}}},
	}
	noIP := &gatewayv1.Gateway{Spec: gatewayv1.GatewaySpec{GatewayClassName: gatewayv1.ObjectName(common.ManagedK8sGatewayClassIstio)}}
	unmanaged := &gatewayv1.Gateway{Spec: gatewayv1.GatewaySpec{GatewayClassName: "other"}}

	t.Run("Create/managed+IP→true", func(t *testing.T) {
		assert.True(t, PredicateFuncsGateway.Create(event.CreateEvent{Object: withIP}))
	})
	t.Run("Create/managed+noIP→false", func(t *testing.T) {
		assert.False(t, PredicateFuncsGateway.Create(event.CreateEvent{Object: noIP}))
	})
	t.Run("Create/unmanaged→false", func(t *testing.T) {
		assert.False(t, PredicateFuncsGateway.Create(event.CreateEvent{Object: unmanaged}))
	})
	t.Run("Update/both unmanaged→false", func(t *testing.T) {
		assert.False(t, PredicateFuncsGateway.Update(event.UpdateEvent{ObjectOld: unmanaged, ObjectNew: unmanaged}))
	})
	t.Run("Update/no change→false", func(t *testing.T) {
		assert.False(t, PredicateFuncsGateway.Update(event.UpdateEvent{ObjectOld: withIP, ObjectNew: withIP}))
	})
	t.Run("Update/addresses changed→true", func(t *testing.T) {
		newGw := withIP.DeepCopy()
		newGw.Status.Addresses = nil
		assert.True(t, PredicateFuncsGateway.Update(event.UpdateEvent{ObjectOld: withIP, ObjectNew: newGw}))
	})
	t.Run("Update/listeners changed→true", func(t *testing.T) {
		h := gatewayv1.Hostname("new.example.com")
		newGw := withIP.DeepCopy()
		newGw.Spec.Listeners = []gatewayv1.Listener{{Name: "l1", Hostname: &h, Port: 80, Protocol: gatewayv1.HTTPProtocolType}}
		assert.True(t, PredicateFuncsGateway.Update(event.UpdateEvent{ObjectOld: withIP, ObjectNew: newGw}))
	})
	t.Run("Delete/managed→true", func(t *testing.T) {
		assert.True(t, PredicateFuncsGateway.Delete(event.DeleteEvent{Object: withIP}))
	})
	t.Run("Delete/unmanaged→false", func(t *testing.T) {
		assert.False(t, PredicateFuncsGateway.Delete(event.DeleteEvent{Object: unmanaged}))
	})
}

func Test_updateDNSRecordCondition(t *testing.T) {
	gwKind := gatewayv1.Kind("Gateway")
	gwGroup := gatewayv1.Group(gatewayv1.GroupName)
	gwNS := gatewayv1.Namespace("ns1")
	testGw := managedGW(testGateway)
	ls := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ls1"},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{Kind: &gwKind, Group: &gwGroup, Name: "gw1", Namespace: &gwNS},
		},
	}
	r, _ := fakeGatewayReconciler(t, testGw, ls)
	ctx := context.Background()

	t.Run("Gateway kind updates gateway status condition", func(t *testing.T) {
		owner := &dns.ResourceRef{Kind: dns.ResourceKindGateway, Object: testGateway.GetObjectMeta()}
		r.updateDNSRecordCondition(ctx, owner, nil)
		updated := &gatewayv1.Gateway{}
		require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Namespace: "ns1", Name: "gw1"}, updated))
		found := false
		for _, c := range updated.Status.Conditions {
			if c.Type == conditionTypeDNSReady {
				assert.Equal(t, metav1.ConditionTrue, c.Status)
				found = true
			}
		}
		assert.True(t, found, "DNSReady condition must be set on Gateway")
	})
	t.Run("ListenerSet kind updates listenerset status condition", func(t *testing.T) {
		owner := &dns.ResourceRef{Kind: dns.ResourceKindListenerSet, Object: ls.GetObjectMeta()}
		r.updateDNSRecordCondition(ctx, owner, nil)
		updated := &gatewayv1.ListenerSet{}
		require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Namespace: "ns1", Name: "ls1"}, updated))
		found := false
		for _, c := range updated.Status.Conditions {
			if c.Type == conditionTypeDNSReady {
				assert.Equal(t, metav1.ConditionTrue, c.Status)
				found = true
			}
		}
		assert.True(t, found, "DNSReady condition must be set on ListenerSet")
	})
	t.Run("unknown kind is silently skipped", func(t *testing.T) {
		owner := &dns.ResourceRef{Kind: "Unknown", Object: &metav1.ObjectMeta{Namespace: "ns1", Name: "gw1"}}
		r.updateDNSRecordCondition(ctx, owner, nil) // must not panic
	})
}

func Test_CollectGarbage(t *testing.T) {
	cachedSet := sets.New(types.NamespacedName{Namespace: "ns1", Name: "gw1"})

	tests := []struct {
		name         string
		k8sObjects   []client.Object
		patchService func(r *GatewayReconciler) func() // returns a reset func
		wantCalls    []string
		wantErr      bool
	}{
		{
			name:      "empty DNS cache, no-op",
			wantCalls: []string{},
		},
		{
			name: "orphaned gateway in cache triggers delete success",
			patchService: func(r *GatewayReconciler) func() {
				p := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListGatewayNamespacedName",
					func(_ *dns.DNSRecordService) sets.Set[types.NamespacedName] { return cachedSet })
				return p.Reset
			},
			wantCalls: []string{callDeleteSuccess},
		},
		{
			name: "orphaned gateway in cache, delete fails",
			patchService: func(r *GatewayReconciler) func() {
				p := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListGatewayNamespacedName",
					func(_ *dns.DNSRecordService) sets.Set[types.NamespacedName] { return cachedSet })
				p.ApplyMethod(reflect.TypeOf(r.Service), "DeleteAllDNSRecordsInGateway",
					func(_ *dns.DNSRecordService, _ context.Context, _, _ string) error {
						return fmt.Errorf("gc delete error")
					})
				return p.Reset
			},
			wantCalls: []string{callDeleteFail},
			wantErr:   true,
		},
		{
			name: "cached gateway still exists in K8s, orphan cleanup runs",
			k8sObjects: []client.Object{&gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "gw1"},
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: gatewayv1.ObjectName(common.ManagedK8sGatewayClassIstio),
					Listeners:        []gatewayv1.Listener{{Name: "l1", Hostname: &h, Port: 80, Protocol: gatewayv1.HTTPProtocolType}},
				},
				Status: gatewayv1.GatewayStatus{Addresses: []gatewayv1.GatewayStatusAddress{{Type: &ipType, Value: "10.0.0.1"}}},
			}},
			patchService: func(r *GatewayReconciler) func() {
				p := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListGatewayNamespacedName",
					func(_ *dns.DNSRecordService) sets.Set[types.NamespacedName] { return cachedSet })
				return p.Reset
			},
			wantCalls: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := fakeGatewayReconciler(t, tt.k8sObjects...)
			if tt.patchService != nil {
				reset := tt.patchService(r)
				defer reset()
			}
			err := r.CollectGarbage(context.Background())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			updater := r.StatusUpdater.(*fakeStatusUpdater)
			updater.validateCalls(t, tt.wantCalls)
		})
	}
}

func Test_findParentGatewayFromListenerSet(t *testing.T) {
	ns := gatewayv1.Namespace("other-ns")
	gwKind := gatewayv1.Kind("Gateway")
	gwGroup := gatewayv1.Group(gatewayv1.GroupName)
	wrongKind := gatewayv1.Kind("Service")
	wrongGroup := gatewayv1.Group("wrong.io")

	tests := []struct {
		name    string
		obj     client.Object
		wantNil bool
		wantNN  types.NamespacedName
	}{
		{
			name:    "non-ListenerSet returns nil",
			obj:     &gatewayv1.Gateway{},
			wantNil: true,
		},
		{
			name:    "wrong Kind returns nil",
			obj:     &gatewayv1.ListenerSet{Spec: gatewayv1.ListenerSetSpec{ParentRef: gatewayv1.ParentGatewayReference{Kind: &wrongKind, Name: "gw1"}}},
			wantNil: true,
		},
		{
			name:    "wrong Group returns nil",
			obj:     &gatewayv1.ListenerSet{Spec: gatewayv1.ListenerSetSpec{ParentRef: gatewayv1.ParentGatewayReference{Group: &wrongGroup, Name: "gw1"}}},
			wantNil: true,
		},
		{
			name: "empty Name returns nil",
			obj: &gatewayv1.ListenerSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
				Spec:       gatewayv1.ListenerSetSpec{ParentRef: gatewayv1.ParentGatewayReference{Name: ""}},
			},
			wantNil: true,
		},
		{
			name: "falls back to ListenerSet namespace when ParentRef.Namespace absent",
			obj: &gatewayv1.ListenerSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
				Spec:       gatewayv1.ListenerSetSpec{ParentRef: gatewayv1.ParentGatewayReference{Name: "gw1"}},
			},
			wantNN: types.NamespacedName{Namespace: "ns1", Name: "gw1"},
		},
		{
			name: "uses ParentRef.Namespace when set",
			obj: &gatewayv1.ListenerSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
				Spec:       gatewayv1.ListenerSetSpec{ParentRef: gatewayv1.ParentGatewayReference{Name: "gw1", Namespace: &ns}},
			},
			wantNN: types.NamespacedName{Namespace: "other-ns", Name: "gw1"},
		},
		{
			name: "explicit Gateway Kind and Group accepted",
			obj: &gatewayv1.ListenerSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
				Spec:       gatewayv1.ListenerSetSpec{ParentRef: gatewayv1.ParentGatewayReference{Kind: &gwKind, Group: &gwGroup, Name: "gw1"}},
			},
			wantNN: types.NamespacedName{Namespace: "ns1", Name: "gw1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findParentGatewayFromListenerSet(tt.obj)
			if tt.wantNil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, tt.wantNN, *got)
			}
		})
	}
}

func Test_listenerSetParentGatewayIndexFunc(t *testing.T) {
	t.Run("non-ListenerSet returns empty", func(t *testing.T) {
		assert.Empty(t, listenerSetParentGatewayIndexFunc(&gatewayv1.Gateway{}))
	})
	t.Run("valid ListenerSet returns namespaced key", func(t *testing.T) {
		ls := &gatewayv1.ListenerSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
			Spec:       gatewayv1.ListenerSetSpec{ParentRef: gatewayv1.ParentGatewayReference{Name: "gw1"}},
		}
		got := listenerSetParentGatewayIndexFunc(ls)
		require.Len(t, got, 1)
		assert.Equal(t, "ns1/gw1", got[0])
	})
}
