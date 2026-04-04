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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
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
	client  client.Client
	scheme  *runtime.Scheme
	indexer client.FieldIndexer
}

func newMockManager(c client.Client, s *runtime.Scheme) *MockManager {
	return &MockManager{client: c, scheme: s}
}
func (m *MockManager) GetClient() client.Client                        { return m.client }
func (m *MockManager) GetScheme() *runtime.Scheme                      { return m.scheme }
func (m *MockManager) GetEventRecorderFor(string) record.EventRecorder { return nil }
func (m *MockManager) Add(manager.Runnable) error                      { return nil }
func (m *MockManager) Start(context.Context) error                     { return nil }
func (m *MockManager) GetFieldIndexer() client.FieldIndexer {
	if m.indexer != nil {
		return m.indexer
	}
	return &mockFieldIndexer{}
}

// mockFieldIndexer implements client.FieldIndexer for testing; IndexField always succeeds unless err is set.
type mockFieldIndexer struct{ err error }

func (f *mockFieldIndexer) IndexField(_ context.Context, _ client.Object, _ string, _ client.IndexerFunc) error {
	return f.err
}

// fakeDiscoveryClient implements discovery.DiscoveryInterface for testing.
// Only ServerResourcesForGroupVersion is overridden; calling any other method panics.
type fakeDiscoveryClient struct {
	discovery.DiscoveryInterface
	resources *metav1.APIResourceList
	err       error
}

func (f *fakeDiscoveryClient) ServerResourcesForGroupVersion(_ string) (*metav1.APIResourceList, error) {
	return f.resources, f.err
}

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
	dnsService := &dns.DNSRecordService{
		Service:        svc,
		DNSRecordStore: dns.BuildDNSRecordStore(),
	}
	r := NewGatewayReconciler(mgr, dnsService)
	r.StatusUpdater = newMockStatusUpdater()
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

var (
	ipType = gatewayv1.IPAddressType
)

type mockStatusUpdater struct {
	mu    sync.RWMutex
	calls []string
}

func (u *mockStatusUpdater) record(status string) {
	u.mu.Lock()
	u.calls = append(u.calls, status)
	u.mu.Unlock()
}

func (u *mockStatusUpdater) UpdateSuccess(_ context.Context, _ client.Object, _ common.UpdateSuccessStatusFn, _ ...interface{}) {
	u.record(callUpdateSuccess)
}

func (u *mockStatusUpdater) UpdateFail(_ context.Context, _ client.Object, _ error, _ string, _ common.UpdateFailStatusFn, _ ...interface{}) {
	u.record(callUpdateFail)
}

func (u *mockStatusUpdater) DeleteSuccess(_ types.NamespacedName, _ client.Object) {
	u.record(callDeleteSuccess)
}

func (u *mockStatusUpdater) IncreaseSyncTotal() {
	u.record(callIncreaseSyncTotal)
}

func (u *mockStatusUpdater) IncreaseUpdateTotal() {
	u.record(callIncreaseUpdateTotal)
}

func (u *mockStatusUpdater) IncreaseDeleteTotal() {
	u.record(callIncreaseDeleteTotal)
}
func (u *mockStatusUpdater) IncreaseDeleteSuccessTotal() {
	u.record(callIncreaseDeleteSuccessTotal)
}

func (u *mockStatusUpdater) IncreaseDeleteFailTotal() {
	u.record(callIncreaseDeleteFailTotal)
}

func (u *mockStatusUpdater) DeleteFail(_ types.NamespacedName, _ client.Object, _ error) {
	u.record(callDeleteFail)
}

func (u *mockStatusUpdater) validateCalls(t *testing.T, wantCalls []string) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	assert.Equal(t, wantCalls, u.getCalls(), "statusUpdater call sequence mismatch")
}

func (u *mockStatusUpdater) getCalls() []string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	cp := make([]string, len(u.calls))
	copy(cp, u.calls)
	return cp
}

func newMockStatusUpdater() statusUpdater {
	return &mockStatusUpdater{calls: make([]string, 0)}
}

func newTestGateway(name, ns, ip string, deleting bool, mutateFn func(gateway *gatewayv1.Gateway)) *gatewayv1.Gateway {
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			UID:       types.UID("uid-" + name),
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(common.ManagedK8sGatewayClassIstio),
		},
	}
	if ip != "" {
		gw.Status.Addresses = []gatewayv1.GatewayStatusAddress{{Type: &ipType, Value: ip}}
	}
	if deleting {
		now := metav1.Now()
		gw.DeletionTimestamp = &now
		gw.Finalizers = []string{"dns.temp/finalizer"}
	}

	if mutateFn != nil {
		mutateFn(gw)
	}
	return gw
}

func newTestListenerSet(name, ns, gwName, hostname string) *gatewayv1.ListenerSet {
	gwKind := gatewayv1.Kind("Gateway")
	gwGroup := gatewayv1.Group(gatewayv1.GroupName)
	return &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID("uid-" + name)},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{Kind: &gwKind, Group: &gwGroup, Name: gatewayv1.ObjectName(gwName)},
			Listeners: []gatewayv1.ListenerEntry{{Name: "dns-l1", Hostname: ptr(gatewayv1.Hostname(hostname))}},
		},
	}
}

func ptr[T any](v T) *T { return &v }

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
		{"dual stack", &gatewayv1.Gateway{Status: gatewayv1.GatewayStatus{
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

func TestGatewayReconciler_Reconcile(t *testing.T) {
	tests := []struct {
		name         string
		existingObjs []client.Object
		reqName      string
		mockDNSFail  bool
		expectResult ctrl.Result
		expectStatus []string
		expectErr    bool
	}{
		{
			name: "Scenario 1: Full success with Gateway and ListenerSet",
			existingObjs: []client.Object{
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}},
				newTestGateway("gw1", "ns1", "10.10.1.1", false, func(gateway *gatewayv1.Gateway) {
					gateway.Spec.Listeners = []gatewayv1.Listener{{Name: "dns-l1", Hostname: ptr(gatewayv1.Hostname("svc.example.com"))}}
				}),
				newTestListenerSet("ls1", "ns1", "gw1", "app.example.com"),
			},
			reqName:      "gw1",
			expectStatus: []string{callIncreaseSyncTotal, callIncreaseUpdateTotal, callUpdateSuccess},
			expectResult: ResultNormal,
		},
		{
			name: "Scenario 2: Successful deletion and DNS record cleanup",
			existingObjs: []client.Object{
				newTestGateway("gw1", "ns1", "10.10.1.1", true, nil),
			},
			reqName:      "gw1",
			expectStatus: []string{callIncreaseSyncTotal, callIncreaseDeleteTotal, callDeleteSuccess},
			expectResult: ResultNormal,
		},
		{
			name: "Scenario 3: DNS configuration error (UpdateFail)",
			existingObjs: []client.Object{
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}},
				newTestGateway("gw1", "ns1", "10.10.1.1", false, func(gateway *gatewayv1.Gateway) {
					gateway.Spec.Listeners = []gatewayv1.Listener{{Name: "dns-l1", Hostname: ptr(gatewayv1.Hostname("svc.example.com"))}}
				}),
			},
			reqName:      "gw1",
			mockDNSFail:  true,
			expectStatus: []string{callIncreaseSyncTotal, callIncreaseUpdateTotal, callUpdateFail},
			expectErr:    true,
			expectResult: common.ResultRequeueAfter10sec,
		},
		{
			name: "Scenario 4: Namespace NotFound Error",
			existingObjs: []client.Object{
				newTestGateway("gw1", "ns1", "10.10.1.1", false, func(gateway *gatewayv1.Gateway) {
					gateway.Spec.Listeners = []gatewayv1.Listener{{Name: "dns-l1", Hostname: ptr(gatewayv1.Hostname("svc.example.com"))}}
				}),
			},
			reqName:      "gw1",
			mockDNSFail:  false,
			expectStatus: []string{callIncreaseSyncTotal, callIncreaseUpdateTotal},
			expectErr:    true,
			expectResult: common.ResultRequeueAfter10sec,
		},
		{
			name: "Scenario 4: Missing IP address - should trigger Delete/Cleanup",
			existingObjs: []client.Object{
				newTestGateway("gw1", "ns1", "", false, nil),
			},
			reqName:      "gw1",
			expectStatus: []string{callIncreaseSyncTotal, callIncreaseDeleteTotal, callDeleteSuccess},
			expectResult: ResultNormal,
		},
		{
			name:         "Scenario_5: Gateway_NotFound_GracefulExit",
			existingObjs: []client.Object{},
			reqName:      "non-existent",
			expectStatus: []string{callIncreaseSyncTotal, callIncreaseDeleteTotal, callDeleteSuccess},
			expectResult: ResultNormal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.existingObjs...).
				WithStatusSubresource(&gatewayv1.Gateway{}, &gatewayv1.ListenerSet{}).
				WithIndex(&gatewayv1.ListenerSet{}, listenerSetParentGatewayIndex, listenerSetParentGatewayIndexFunc).
				Build()

			dnsSvc := &dns.DNSRecordService{
				DNSRecordStore: dns.BuildDNSRecordStore(),
			}

			updater := newMockStatusUpdater().(*mockStatusUpdater)

			r := &GatewayReconciler{
				Client:        fc,
				Scheme:        scheme,
				Service:       dnsSvc,
				StatusUpdater: updater,
				Recorder:      record.NewFakeRecorder(10),
			}

			// Localized Patching with gomonkey
			if tt.mockDNSFail {
				p := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateDNSRecords",
					func(_ *dns.DNSRecordService, _ context.Context, _ *dns.Record) error {
						return fmt.Errorf("dns provider unreachable")
					})
				defer p.Reset()
			} else {
				p := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateDNSRecords",
					func(_ *dns.DNSRecordService, _ context.Context, rec *dns.Record) error {
						return nil
					})
				defer p.Reset()
			}

			req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: tt.reqName}}
			res, err := r.Reconcile(context.TODO(), req)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectResult, res)
			assert.ElementsMatch(t, tt.expectStatus, updater.getCalls())
		})
	}
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
	testGw := newTestGateway("gw1", "ns1", "10.10.1.1", false, nil)
	ls := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ls1"},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{Kind: &gwKind, Group: &gwGroup, Name: "gw1", Namespace: &gwNS},
		},
	}
	r, _ := fakeGatewayReconciler(t, testGw, ls)
	ctx := context.Background()

	t.Run("Gateway kind updates gateway status condition", func(t *testing.T) {
		owner := &dns.ResourceRef{Kind: dns.ResourceKindGateway, Object: testGw.GetObjectMeta()}
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
	h := gatewayv1.Hostname("svc.example.com")

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
			// CollectGarbage is a no-op until crdReady=true; set it here to exercise real GC logic.
			r.crdReady = true
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
			updater := r.StatusUpdater.(*mockStatusUpdater)
			updater.validateCalls(t, tt.wantCalls)
		})
	}
}

func Test_CollectGarbage_crdNotReady(t *testing.T) {
	r, _ := fakeGatewayReconciler(t)
	// crdReady defaults to false; CollectGarbage should return nil without any action.
	assert.False(t, r.crdReady)
	assert.NoError(t, r.CollectGarbage(context.Background()))
	r.StatusUpdater.(*mockStatusUpdater).validateCalls(t, []string{})
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

func Test_checkGatewayCRDs(t *testing.T) {
	tests := []struct {
		name            string
		resources       *metav1.APIResourceList
		discoveryErr    error
		wantGateway     bool
		wantListenerSet bool
		wantErr         bool
	}{
		{
			name: "both gateway and listenerset present",
			resources: &metav1.APIResourceList{
				APIResources: []metav1.APIResource{{Name: "gateways"}, {Name: "listenersets"}},
			},
			wantGateway:     true,
			wantListenerSet: true,
		},
		{
			name: "only gateway present",
			resources: &metav1.APIResourceList{
				APIResources: []metav1.APIResource{{Name: "gateways"}},
			},
			wantGateway:     true,
			wantListenerSet: false,
		},
		{
			name:      "empty resource list - neither present",
			resources: &metav1.APIResourceList{},
		},
		{
			name: "nil resource list returned by discovery",
		},
		{
			name:         "404 not-found treated as neither present",
			discoveryErr: apierrors.NewNotFound(schema.GroupResource{Group: "gateway.k8s.io", Resource: "v1"}, ""),
		},
		{
			name:         "non-404 error is propagated",
			discoveryErr: fmt.Errorf("connection refused"),
			wantErr:      true,
		},
		{
			name: "extra unrecognised resources are ignored",
			resources: &metav1.APIResourceList{
				APIResources: []metav1.APIResource{{Name: "gateways"}, {Name: "httproutes"}},
			},
			wantGateway:     true,
			wantListenerSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &GatewayReconciler{
				discoveryClient: &fakeDiscoveryClient{resources: tt.resources, err: tt.discoveryErr},
			}
			gotGW, gotLS, err := r.checkGatewayCRDs(nil)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantGateway, gotGW)
			assert.Equal(t, tt.wantListenerSet, gotLS)
		})
	}
}

func Test_StartController(t *testing.T) {
	tests := []struct {
		name          string
		resources     *metav1.APIResourceList
		discoveryErr  error
		setupErr      error
		wantErr       bool
		wantCrdReady  bool
		wantLSEnabled bool
	}{
		{
			name:      "gateway CRD absent - controller skipped",
			resources: &metav1.APIResourceList{},
		},
		{
			name:         "discovery error - returns error",
			discoveryErr: fmt.Errorf("discovery unavailable"),
			wantErr:      true,
		},
		{
			name: "only gateway present - listenerset disabled",
			resources: &metav1.APIResourceList{
				APIResources: []metav1.APIResource{{Name: "gateways"}},
			},
			wantCrdReady:  true,
			wantLSEnabled: false,
		},
		{
			name: "both CRDs present - listenerset enabled",
			resources: &metav1.APIResourceList{
				APIResources: []metav1.APIResource{{Name: "gateways"}, {Name: "listenersets"}},
			},
			wantCrdReady:  true,
			wantLSEnabled: true,
		},
		{
			name: "setupWithManager error - crdReady already set",
			resources: &metav1.APIResourceList{
				APIResources: []metav1.APIResource{{Name: "gateways"}},
			},
			setupErr:     fmt.Errorf("controller setup failed"),
			wantErr:      true,
			wantCrdReady: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := fakeGatewayReconciler(t)
			r.discoveryClient = &fakeDiscoveryClient{resources: tt.resources, err: tt.discoveryErr}

			mgr := &MockManager{client: r.Client, scheme: scheme}

			p := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "setupWithManager",
				func(_ *GatewayReconciler, _ ctrl.Manager) error { return tt.setupErr })
			defer p.Reset()

			err := r.StartController(mgr, nil)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantCrdReady, r.crdReady, "crdReady mismatch")
			assert.Equal(t, tt.wantLSEnabled, r.listenerSetEnabled, "listenerSetEnabled mismatch")
		})
	}
}

// Test_setupWithManager_indexFieldError verifies that setupWithManager propagates an IndexField
// registration failure when ListenerSet support is enabled.
func Test_setupWithManager_indexFieldError(t *testing.T) {
	r, _ := fakeGatewayReconciler(t)
	r.listenerSetEnabled = true
	mgr := &MockManager{
		client:  r.Client,
		scheme:  scheme,
		indexer: &mockFieldIndexer{err: fmt.Errorf("index registration failed")},
	}
	err := r.setupWithManager(mgr)
	require.Error(t, err)
}
