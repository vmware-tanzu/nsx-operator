package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery/fake"
	k8stesting "k8s.io/client-go/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mockclient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mockdns "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordprovider"
	mockgateway "github.com/vmware-tanzu/nsx-operator/pkg/mock/gateway"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

func TestGatewayReconciler_updateGatewayDNSReadyCondition(t *testing.T) {
	ctx := context.Background()
	gwNN := types.NamespacedName{Namespace: "default", Name: "gw"}

	tests := []struct {
		name      string
		errInput  error
		setupMock func(*mockclient.MockClient)
		wantErr   bool
	}{
		{
			name:     "ignore-not-found",
			errInput: nil,
			setupMock: func(c *mockclient.MockClient) {
				c.EXPECT().Get(ctx, gwNN, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "gw")).Times(1)
			},
			wantErr: false,
		},
		{
			name:     "no-condition-change-needed",
			errInput: nil,
			setupMock: func(c *mockclient.MockClient) {
				c.EXPECT().Get(ctx, gwNN, gomock.Any()).DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj *gatewayv1.Gateway, opts ...client.GetOption) error {
					obj.Status.Conditions = []metav1.Condition{buildDNSRecordReadyCondition(nil)}
					return nil
				}).Times(1)
			},
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTestEnv(t)
			r := env.reconciler
			tc.setupMock(env.client)
			err := r.updateGatewayDNSReadyCondition(ctx, gwNN, tc.errInput)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGatewayReconciler_removeGatewayDNSConfigCondition(t *testing.T) {
	ctx := context.Background()
	env := setupTestEnv(t)
	r := env.reconciler
	c := env.client

	gwNN := types.NamespacedName{Namespace: "default", Name: "gw"}

	// Should ignore NotFound
	c.EXPECT().Get(ctx, gwNN, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "gw")).Times(1)
	err := r.removeGatewayDNSConfigCondition(ctx, gwNN)
	assert.NoError(t, err)
}

func TestGatewayReconciler_upsertGatewayIPCache(t *testing.T) {
	r := &GatewayReconciler{ipCache: NewGatewayIPCache()}

	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "gw"},
		Status: gatewayv1.GatewayStatus{
			Addresses: []gatewayv1.GatewayStatusAddress{
				{Type: ptrGatewayAddressType(gatewayv1.IPAddressType), Value: "1.1.1.1"},
			},
		},
	}

	lsList := []gatewayv1.ListenerSet{} // no listeners

	_, changed := r.upsertGatewayIPCache(gw, lsList)
	assert.True(t, changed)

	entry, ok := r.ipCache.get(types.NamespacedName{Namespace: "default", Name: "gw"})
	assert.True(t, ok)
	assert.Equal(t, extdns.Targets{"1.1.1.1"}, entry.IPs)

	// Try same update, shouldn't change
	_, changed = r.upsertGatewayIPCache(gw, lsList)
	assert.False(t, changed)
}

func TestGatewayReconciler_enqueueAttachedRoutesForGatewayDNSFromAPI(t *testing.T) {
	ctx := context.Background()
	gwNN := types.NamespacedName{Namespace: "default", Name: "gw1"}

	tests := []struct {
		name      string
		setupMock func(*mockclient.MockClient)
	}{
		{
			name: "list-error",
			setupMock: func(c *mockclient.MockClient) {
				c.EXPECT().List(ctx, gomock.Any(), gomock.Any()).Return(errors.New("list error")).AnyTimes()
			},
		},
		{
			name: "success",
			setupMock: func(c *mockclient.MockClient) {
				c.EXPECT().List(ctx, gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
					switch l := list.(type) {
					case *gatewayv1.HTTPRouteList:
						l.Items = []gatewayv1.HTTPRoute{{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r1"}}}
					case *gatewayv1.GRPCRouteList:
						l.Items = []gatewayv1.GRPCRoute{{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r1"}}}
					case *gatewayv1.TLSRouteList:
						l.Items = []gatewayv1.TLSRoute{{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r1"}}}
					case *gatewayv1.ListenerSetList:
						l.Items = []gatewayv1.ListenerSet{{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ls1"}}}
					}
					return nil
				}).AnyTimes()
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTestEnv(t)
			r := env.reconciler
			tc.setupMock(env.client)
			r.enqueueAttachedRoutesForGatewayDNSFromAPI(ctx, gwNN)
		})
	}
}

func TestGatewayReconciler_listSortedListenerSetsForGateway(t *testing.T) {
	ctx := context.Background()
	ctrlMock := gomock.NewController(t)

	c := mockclient.NewMockClient(ctrlMock)
	r := &GatewayReconciler{Client: c}

	c.EXPECT().List(ctx, gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
		lsList := list.(*gatewayv1.ListenerSetList)
		lsList.Items = []gatewayv1.ListenerSet{
			{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ls2"}},
			{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ls1"}},
			{ObjectMeta: metav1.ObjectMeta{Namespace: "ns2", Name: "ls1"}},
		}
		return nil
	}).Times(1)

	list, err := r.listSortedListenerSetsForGateway(ctx, types.NamespacedName{Namespace: "default", Name: "gw"})
	assert.NoError(t, err)
	assert.Len(t, list, 3)
	assert.Equal(t, "default", list[0].Namespace)
	assert.Equal(t, "ls1", list[0].Name)
	assert.Equal(t, "default", list[1].Namespace)
	assert.Equal(t, "ls2", list[1].Name)
	assert.Equal(t, "ns2", list[2].Namespace)
	assert.Equal(t, "ls1", list[2].Name)
}

func TestGatewayReconciler_routeParentListenerSetIndexFunc(t *testing.T) {
	gwGroup := gatewayv1.Group("gateway.networking.k8s.io")
	gwKind := gatewayv1.Kind("ListenerSet")

	testCases := []struct {
		name string
		obj  client.Object
		want []string
	}{
		{
			name: "HTTPRoute",
			obj: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{{Group: &gwGroup, Kind: &gwKind, Name: "ls1"}},
					},
				},
			},
			want: []string{"default/ls1"},
		},
		{
			name: "GRPCRoute",
			obj: &gatewayv1.GRPCRoute{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
				Spec: gatewayv1.GRPCRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{{Group: &gwGroup, Kind: &gwKind, Name: "ls1"}},
					},
				},
			},
			want: []string{"default/ls1"},
		},
		{
			name: "TLSRoute",
			obj: &gatewayv1.TLSRoute{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
				Spec: gatewayv1.TLSRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{{Group: &gwGroup, Kind: &gwKind, Name: "ls1"}},
					},
				},
			},
			want: []string{"default/ls1"},
		},
		{
			name: "Unknown",
			obj:  &gatewayv1.Gateway{},
			want: nil,
		},
		{
			name: "Empty Parents",
			obj:  &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}},
			want: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var keys []string
			switch tc.name {
			case "HTTPRoute", "Empty Parents":
				keys = (&genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]{}).routeParentListenerSetIndexFunc(tc.obj)
			case "GRPCRoute":
				keys = (&genericRouteReconciler[*GRPCRoute, gatewayv1.GRPCRoute, *gatewayv1.GRPCRoute]{}).routeParentListenerSetIndexFunc(tc.obj)
			case "TLSRoute":
				keys = (&genericRouteReconciler[*TLSRoute, gatewayv1.TLSRoute, *gatewayv1.TLSRoute]{}).routeParentListenerSetIndexFunc(tc.obj)
			default:
				keys = (&genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]{}).routeParentListenerSetIndexFunc(tc.obj)
			}
			assert.Equal(t, tc.want, keys)
		})
	}
}

func TestGatewayReconciler_checkGatewayCRDs(t *testing.T) {
	tests := []struct {
		name                string
		apiResources        []*metav1.APIResourceList
		wantGatewayEnabled  bool
		wantListenerEnabled bool
	}{
		{
			name: "Full",
			apiResources: []*metav1.APIResourceList{
				{
					GroupVersion: gatewayv1.GroupVersion.String(),
					APIResources: []metav1.APIResource{
						{Name: "gateways"},
						{Name: "listenersets"},
						{Name: "httproutes"},
						{Name: "grpcroutes"},
						{Name: "tlsroutes"},
					},
				},
			},
			wantGatewayEnabled:  true,
			wantListenerEnabled: true,
		},
		{
			name:                "NotFound",
			apiResources:        []*metav1.APIResourceList{},
			wantGatewayEnabled:  false,
			wantListenerEnabled: false,
		},
		{
			name: "NilResources",
			apiResources: []*metav1.APIResourceList{
				{
					GroupVersion: gatewayv1.GroupVersion.String(),
					APIResources: nil,
				},
			},
			wantGatewayEnabled:  false,
			wantListenerEnabled: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrlMock := gomock.NewController(t)
			c := mockclient.NewMockClient(ctrlMock)
			r := &GatewayReconciler{Client: c}

			fakeDiscovery := &fake.FakeDiscovery{Fake: &k8stesting.Fake{}}
			fakeDiscovery.Resources = tc.apiResources
			r.discoveryClient = fakeDiscovery

			mgr := setupMockManager(ctrlMock, c)
			err := r.checkGatewayCRDs(mgr)
			assert.NoError(t, err)

			assert.Equal(t, tc.wantGatewayEnabled, r.apiResources.gatewayEnabled)
			assert.Equal(t, tc.wantListenerEnabled, r.apiResources.listenerSetEnabled)
		})
	}
}

func TestGatewayReconciler_warmGatewayIPCacheOnStartup(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name               string
		gatewayEnabled     bool
		listenerSetEnabled bool
		setupMock          func(*mockclient.MockClient)
		wantErr            bool
	}{
		{
			"NoGateway", false, false, func(c *mockclient.MockClient) {}, false,
		},
		{
			"ListError", true, false, func(c *mockclient.MockClient) {
				c.EXPECT().List(ctx, gomock.Any()).Return(errors.New("list error"))
			}, true,
		},
		{
			"ListListenerSetError", true, true, func(c *mockclient.MockClient) {
				c.EXPECT().List(ctx, gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
					if _, ok := list.(*gatewayv1.ListenerSetList); ok {
						return errors.New("list ls err")
					}
					if gwList, ok := list.(*gatewayv1.GatewayList); ok {
						gwList.Items = []gatewayv1.Gateway{
							{
								ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "gw1"},
								Spec:       gatewayv1.GatewaySpec{GatewayClassName: "istio"},
								Status: gatewayv1.GatewayStatus{
									Addresses: []gatewayv1.GatewayStatusAddress{{Type: ptrGatewayAddressType(gatewayv1.IPAddressType), Value: "1.2.3.4"}},
								},
							},
						}
						return nil
					}
					return nil
				}).AnyTimes()
			}, true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrlMock := gomock.NewController(t)
			c := mockclient.NewMockClient(ctrlMock)
			r := &GatewayReconciler{Client: c}
			r.apiResources.gatewayEnabled = tc.gatewayEnabled
			r.apiResources.listenerSetEnabled = tc.listenerSetEnabled
			tc.setupMock(c)
			err := r.warmGatewayIPCacheOnStartup(ctx)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGatewayReconciler_StartController(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*mockclient.MockClient) *GatewayReconciler
		wantErr   bool
	}{
		{
			"GatewayNotEnabled",
			func(c *mockclient.MockClient) *GatewayReconciler {
				r := &GatewayReconciler{Client: c}
				fakeDiscovery := &fake.FakeDiscovery{Fake: &k8stesting.Fake{}}
				fakeDiscovery.Resources = []*metav1.APIResourceList{}
				r.discoveryClient = fakeDiscovery
				return r
			},
			false,
		},
		{
			"CheckGatewayCRDsError",
			func(c *mockclient.MockClient) *GatewayReconciler {
				r := &GatewayReconciler{Client: c}
				fakeDiscovery := &fake.FakeDiscovery{Fake: &k8stesting.Fake{}}
				fakeDiscovery.PrependReactor("get", "resource", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, errors.New("discovery error")
				})
				r.discoveryClient = fakeDiscovery
				return r
			},
			true,
		},
		{
			"SetupWithManagerError",
			func(c *mockclient.MockClient) *GatewayReconciler {
				r := &GatewayReconciler{Client: c}
				r.apiResources.gatewayEnabled = true
				fakeDiscovery := &fake.FakeDiscovery{Fake: &k8stesting.Fake{}}
				fakeDiscovery.Resources = []*metav1.APIResourceList{
					{
						GroupVersion: gatewayv1.GroupVersion.String(),
						APIResources: []metav1.APIResource{{Name: "gateways"}, {Name: "listenersets"}},
					},
				}
				r.discoveryClient = fakeDiscovery
				return r
			},
			true, // error triggered by mock setup within test
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrlMock := gomock.NewController(t)
			c := mockclient.NewMockClient(ctrlMock)
			r := tc.setupMock(c)
			mgr := setupMockManager(ctrlMock, c)

			if tc.name == "SetupWithManagerError" {
				idx := mockclient.NewMockFieldIndexer(ctrlMock)
				mgr.EXPECT().GetFieldIndexer().Return(idx).AnyTimes()
				idx.EXPECT().IndexField(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("idx err")).Times(1)
			}

			err := r.StartController(mgr, nil)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGatewayReconciler_Reconcile(t *testing.T) {
	gwReq := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "gw1"}}

	tests := []struct {
		name      string
		setupMock func(*mockclient.MockClient, *mockdns.MockDNSRecordProvider, *mockgateway.MockStatusUpdater)
		wantRes   ctrl.Result
		wantErr   bool
	}{
		{
			name: "delete",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater) {
				c.EXPECT().Get(gomock.Any(), gwReq.NamespacedName, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "gw1")).AnyTimes()
				d.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "default", "gw1").Return(true, nil).AnyTimes()
				c.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				s.EXPECT().IncreaseSyncTotal().AnyTimes()
				s.EXPECT().IncreaseDeleteTotal().AnyTimes()
				s.EXPECT().DeleteSuccess(gomock.Any(), gomock.Any()).AnyTimes()
			},
			wantRes: ctrl.Result{},
		},
		{
			name: "unmanaged gateway",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater) {
				c.EXPECT().Get(gomock.Any(), gwReq.NamespacedName, gomock.Any()).DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					gw := obj.(*gatewayv1.Gateway)
					gw.Spec.GatewayClassName = "unmanaged"
					gw.Name = "gw1"
					gw.Namespace = "default"
					return nil
				}).AnyTimes()
				d.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "default", "gw1").Return(true, nil).Times(1)
				c.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				s.EXPECT().IncreaseSyncTotal().AnyTimes()
				s.EXPECT().IncreaseDeleteTotal().AnyTimes()
				s.EXPECT().DeleteSuccess(gomock.Any(), gomock.Any()).AnyTimes()
			},
			wantRes: ctrl.Result{},
		},
		{
			name: "managed gateway with hostnames",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater) {
				c.EXPECT().Get(gomock.Any(), gwReq.NamespacedName, gomock.Any()).DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					gw := obj.(*gatewayv1.Gateway)
					gw.Spec.GatewayClassName = "avi-lb"
					gw.Name = "gw1"
					gw.Namespace = "default"
					hostname := gatewayv1.Hostname("a.com")
					gw.Spec.Listeners = []gatewayv1.Listener{{Hostname: &hostname, Name: "l1"}}
					gw.Status.Addresses = []gatewayv1.GatewayStatusAddress{{Type: ptrGatewayAddressType(gatewayv1.IPAddressType), Value: "1.1.1.1"}}
					return nil
				}).AnyTimes()
				c.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				d.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return([]dns.EndpointRow{{Endpoint: &extdns.Endpoint{DNSName: "a.com"}}}, map[string]string{"a.com": ""}, nil).AnyTimes()
				d.EXPECT().CreateOrUpdateRecords(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
				d.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "default", "gw1").Return(true, nil).AnyTimes()
				s.EXPECT().IncreaseSyncTotal().AnyTimes()
				s.EXPECT().IncreaseUpdateTotal().AnyTimes()
				s.EXPECT().UpdateSuccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
				s.EXPECT().DeleteSuccess(gomock.Any(), gomock.Any()).AnyTimes()
			},
			wantRes: ctrl.Result{},
		},
		{
			name: "client get error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater) {
				c.EXPECT().Get(gomock.Any(), gwReq.NamespacedName, gomock.Any()).Return(errors.New("get error"))
				s.EXPECT().IncreaseSyncTotal().AnyTimes()
			},
			wantRes: common.ResultRequeueAfter10sec,
		},
		{
			name: "not found delete error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater) {
				c.EXPECT().Get(gomock.Any(), gwReq.NamespacedName, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, ""))
				d.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "default", "gw1").Return(false, errors.New("delete error"))
				s.EXPECT().IncreaseSyncTotal().AnyTimes()
				s.EXPECT().IncreaseDeleteTotal().AnyTimes()
				s.EXPECT().DeleteFail(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			},
			wantRes: common.ResultRequeueAfter10sec,
		},
		{
			name: "deletion timestamp delete error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater) {
				c.EXPECT().Get(gomock.Any(), gwReq.NamespacedName, gomock.Any()).DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
					gw := obj.(*gatewayv1.Gateway)
					gw.Name = "gw1"
					gw.Namespace = "default"
					gw.DeletionTimestamp = &metav1.Time{Time: time.Now()}
					return nil
				})
				d.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "default", "gw1").Return(false, errors.New("delete error"))
				s.EXPECT().IncreaseSyncTotal().AnyTimes()
				s.EXPECT().IncreaseDeleteTotal().AnyTimes()
				s.EXPECT().DeleteFail(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			},
			wantRes: common.ResultRequeueAfter10sec,
		},
		{
			name: "not managed delete error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater) {
				c.EXPECT().Get(gomock.Any(), gwReq.NamespacedName, gomock.Any()).DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
					gw := obj.(*gatewayv1.Gateway)
					gw.Name = "gw1"
					gw.Namespace = "default"
					gw.Spec.GatewayClassName = "other-class"
					return nil
				})
				d.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "default", "gw1").Return(false, errors.New("delete error"))
				s.EXPECT().IncreaseSyncTotal().AnyTimes()
				s.EXPECT().IncreaseDeleteTotal().AnyTimes()
				s.EXPECT().DeleteFail(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			},
			wantRes: common.ResultRequeueAfter10sec,
		},
		{
			name: "no usable ip delete error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater) {
				c.EXPECT().Get(gomock.Any(), gwReq.NamespacedName, gomock.Any()).DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
					gw := obj.(*gatewayv1.Gateway)
					gw.Name = "gw1"
					gw.Namespace = "default"
					gw.Spec.GatewayClassName = "nsx"
					gw.Status.Addresses = []gatewayv1.GatewayStatusAddress{}
					return nil
				})
				d.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "default", "gw1").Return(false, errors.New("delete error"))
				s.EXPECT().IncreaseSyncTotal().AnyTimes()
				s.EXPECT().IncreaseDeleteTotal().AnyTimes()
				s.EXPECT().DeleteFail(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			},
			wantRes: common.ResultRequeueAfter10sec,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrlMock := gomock.NewController(t)
			c := mockclient.NewMockClient(ctrlMock)
			d := mockdns.NewMockDNSRecordProvider(ctrlMock)
			s := mockgateway.NewMockStatusUpdater(ctrlMock)
			r := &GatewayReconciler{
				Client:        c,
				DNS:           d,
				StatusUpdater: s,
				ipCache:       NewGatewayIPCache(),
			}

			if tc.setupMock != nil {
				tc.setupMock(c, d, s)
			}

			res, err := r.Reconcile(ctx, gwReq)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.wantRes, res)
			}
		})
	}
}

func TestSetupWithManager(t *testing.T) {
	ctrlMock := gomock.NewController(t)
	c := mockclient.NewMockClient(ctrlMock)

	r := &GatewayReconciler{
		Client: c,
	}

	// Since we mock the manager, the actual setup will fail because it cannot find the Scheme/Cache,
	// but it should return an error gracefully rather than panicking.
	err := r.setupWithManager(setupMockManager(ctrlMock, nil))
	assert.NoError(t, err)
}
