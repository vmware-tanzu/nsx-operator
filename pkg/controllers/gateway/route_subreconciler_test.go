package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	mockclient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mockdns "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordprovider"
	mockgateway "github.com/vmware-tanzu/nsx-operator/pkg/mock/gateway"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

func TestRouteParentGatewayIndexFunc(t *testing.T) {
	gwGroup := gatewayv1.Group("gateway.networking.k8s.io")
	gwKind := gatewayv1.Kind("Gateway")

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
						ParentRefs: []gatewayv1.ParentReference{{Group: &gwGroup, Kind: &gwKind, Name: "gw1"}},
					},
				},
			},
			want: []string{"default/gw1"},
		},
		{
			name: "GRPCRoute",
			obj: &gatewayv1.GRPCRoute{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
				Spec: gatewayv1.GRPCRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{{Group: &gwGroup, Kind: &gwKind, Name: "gw1"}},
					},
				},
			},
			want: []string{"default/gw1"},
		},
		{
			name: "TLSRoute",
			obj: &gatewayv1.TLSRoute{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
				Spec: gatewayv1.TLSRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{{Group: &gwGroup, Kind: &gwKind, Name: "gw1"}},
					},
				},
			},
			want: []string{"default/gw1"},
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
				keys = (&genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]{}).routeParentGatewayIndexFunc(tc.obj)
			case "GRPCRoute":
				keys = (&genericRouteReconciler[*GRPCRoute, gatewayv1.GRPCRoute, *gatewayv1.GRPCRoute]{}).routeParentGatewayIndexFunc(tc.obj)
			case "TLSRoute":
				keys = (&genericRouteReconciler[*TLSRoute, gatewayv1.TLSRoute, *gatewayv1.TLSRoute]{}).routeParentGatewayIndexFunc(tc.obj)
			default:
				keys = (&genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]{}).routeParentGatewayIndexFunc(tc.obj)
			}
			assert.Equal(t, tc.want, keys)
		})
	}
}

func TestGenericRouteReconciler_resolveParentRefToRootGatewayNN(t *testing.T) {
	gwGroup := gatewayv1.Group("gateway.networking.k8s.io")
	gwKind := gatewayv1.Kind("Gateway")
	lsKind := gatewayv1.Kind("ListenerSet")

	r := &GatewayReconciler{ipCache: NewGatewayIPCache()}
	sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })

	// 1. Direct Gateway
	ref := &gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw1"}
	nn, ok := sub.resolveParentRefToRootGatewayNN("default", ref)
	assert.True(t, ok)
	assert.Equal(t, types.NamespacedName{Namespace: "default", Name: "gw1"}, nn)

	// 2. Unknown Kind
	unknownKind := gatewayv1.Kind("Unknown")
	ref = &gatewayv1.ParentReference{Group: &gwGroup, Kind: &unknownKind, Name: "uk"}
	_, ok = sub.resolveParentRefToRootGatewayNN("default", ref)
	assert.False(t, ok)

	// 3. ListenerSet not in cache
	ref = &gatewayv1.ParentReference{Group: &gwGroup, Kind: &lsKind, Name: "ls1"}
	_, ok = sub.resolveParentRefToRootGatewayNN("default", ref)
	assert.False(t, ok)

	// 4. ListenerSet in cache
	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, gatewayDNSCacheEntry{
		AdmissionRows: []extdnssrc.AdmissionHostCacheRow{
			{ListenerSet: types.NamespacedName{Namespace: "default", Name: "ls1"}, FromListenerSet: true},
		},
	})
	nn, ok = sub.resolveParentRefToRootGatewayNN("default", ref)
	assert.True(t, ok)
	assert.Equal(t, types.NamespacedName{Namespace: "default", Name: "gw1"}, nn)

	// 5. Cache is nil
	subNilCache := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](&GatewayReconciler{}, "HTTPRoute", newHTTPRoute, nil)
	_, ok = subNilCache.resolveParentRefToRootGatewayNN("default", ref)
	assert.False(t, ok)
}

func TestDistinctAcceptedRootGatewayNNs(t *testing.T) {
	gwGroup := gatewayv1.Group("gateway.networking.k8s.io")
	gwKind := gatewayv1.Kind("Gateway")

	r := &GatewayReconciler{ipCache: NewGatewayIPCache()}
	sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })

	refs := []gatewayv1.ParentReference{
		{Group: &gwGroup, Kind: &gwKind, Name: "gw1"},
		{Group: &gwGroup, Kind: &gwKind, Name: "gw2"},
		{Group: &gwGroup, Kind: &gwKind, Name: "gw3"},
	}

	status := []gatewayv1.RouteParentStatus{
		{
			ParentRef: refs[0],
			Conditions: []metav1.Condition{
				{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue},
			},
		},
		{
			ParentRef: refs[1],
			Conditions: []metav1.Condition{
				{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionFalse}, // Not accepted
			},
		},
	}

	nns := sub.distinctAcceptedRootGatewayNNs("default", refs, status)
	assert.Len(t, nns, 1)
	assert.Equal(t, types.NamespacedName{Namespace: "default", Name: "gw1"}, nns[0])
}

func TestInferRouteDNSHostnamesFromAcceptedParents(t *testing.T) {
	gwGroup := gatewayv1.Group("gateway.networking.k8s.io")
	gwKind := gatewayv1.Kind("Gateway")

	r := &GatewayReconciler{ipCache: NewGatewayIPCache()}
	sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })

	refs := []gatewayv1.ParentReference{
		{Group: &gwGroup, Kind: &gwKind, Name: "gw1"},
	}

	status := []gatewayv1.RouteParentStatus{
		{
			ParentRef: refs[0],
			Conditions: []metav1.Condition{
				{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue},
			},
		},
	}

	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, gatewayDNSCacheEntry{
		IPs: extdns.Targets{"1.1.1.1"},
		AdmissionRows: []extdnssrc.AdmissionHostCacheRow{
			{Filter: "a.com"},
			{Filter: "b.com"},
		},
	})

	hostnames, err := sub.inferRouteDNSHostnamesFromAcceptedParents("default", refs, status, &metav1.ObjectMeta{Namespace: "default", Name: "r1"})
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"a.com", "b.com"}, hostnames)
}

func TestGenericRouteReconciler_inferRouteDNSHostnamesFromAcceptedParents(t *testing.T) {
	env := setupTestEnv(t)
	r := env.reconciler
	sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })

	gwGroup := gatewayv1.Group("gateway.networking.k8s.io")
	gwKind := gatewayv1.Kind("Gateway")

	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, gatewayDNSCacheEntry{
		IPs:             extdns.Targets{"1.1.1.1"},
		GatewayResource: types.NamespacedName{Namespace: "default", Name: "gw1"},
		AdmissionRows: []extdnssrc.AdmissionHostCacheRow{
			{Section: "l1", Filter: "a.com"},
		},
	})

	parentRefs := []gatewayv1.ParentReference{{Group: &gwGroup, Kind: &gwKind, Name: "gw1"}}
	parentStatus := []gatewayv1.RouteParentStatus{
		{ParentRef: parentRefs[0], Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}},
	}

	meta := &metav1.ObjectMeta{Namespace: "default", Name: "r1"}

	hostnames, err := sub.inferRouteDNSHostnamesFromAcceptedParents("default", parentRefs, parentStatus, meta)
	assert.NoError(t, err)
	assert.Len(t, hostnames, 1)
	assert.Contains(t, hostnames, "a.com")

	// test cache miss
	parentRefsMiss := []gatewayv1.ParentReference{{Group: &gwGroup, Kind: &gwKind, Name: "gw_miss"}}
	parentStatusMiss := []gatewayv1.RouteParentStatus{
		{ParentRef: parentRefsMiss[0], Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}},
	}
	hostnamesMiss, err := sub.inferRouteDNSHostnamesFromAcceptedParents("default", parentRefsMiss, parentStatusMiss, meta)
	assert.NoError(t, err)
	assert.Len(t, hostnamesMiss, 0)
}

func TestBuildEndpoints(t *testing.T) {
	hostnames := []string{"a.com", "b.com"}
	targets := extdns.Targets{"1.1.1.1"}

	eps := buildEndpoints(hostnames, targets, "parent-gw")

	assert.Len(t, eps, 2)
	assert.Equal(t, "a.com", eps[0].DNSName)
	assert.Equal(t, "parent-gw", eps[0].Labels[dns.EndpointLabelParentGateway])
	assert.Equal(t, "b.com", eps[1].DNSName)
	assert.Equal(t, "parent-gw", eps[1].Labels[dns.EndpointLabelParentGateway])
}

func TestBuildRouteDNSEndpointsForAggregation(t *testing.T) {
	gwGroup := gatewayv1.Group("gateway.networking.k8s.io")
	gwKind := gatewayv1.Kind("Gateway")

	r := &GatewayReconciler{ipCache: NewGatewayIPCache()}

	// Create mock provider
	ctrlMock := gomock.NewController(t)
	mockDNS := mockdns.NewMockDNSRecordProvider(ctrlMock)
	r.DNS = mockDNS

	sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })

	refs := []gatewayv1.ParentReference{
		{Group: &gwGroup, Kind: &gwKind, Name: "gw1"},
	}

	status := []gatewayv1.RouteParentStatus{
		{
			ParentRef: refs[0],
			Conditions: []metav1.Condition{
				{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue},
			},
		},
	}

	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, gatewayDNSCacheEntry{
		IPs: extdns.Targets{"1.1.1.1"},
		AdmissionRows: []extdnssrc.AdmissionHostCacheRow{
			{Filter: "a.com"},
		},
	})

	objMeta := &metav1.ObjectMeta{Namespace: "default", Name: "r1"}
	specHostnames := []gatewayv1.Hostname{"a.com"}

	mockDNS.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, map[string]string{"zone": "domain"}, nil)

	owner := &dns.ResourceRef{Kind: dns.ResourceKindHTTPRoute}
	rows, allowed, err := sub.buildRouteDNSEndpointsForAggregation("default", owner, refs, status, objMeta, specHostnames)

	assert.NoError(t, err)
	assert.Len(t, allowed, 1)
	assert.Nil(t, rows)
}

func TestMergeTargetsUnion(t *testing.T) {
	a := extdns.Targets{"1.1.1.1", "2.2.2.2"}
	b := extdns.Targets{"2.2.2.2", "3.3.3.3"}

	res := mergeTargetsUnion(a, b)
	assert.Len(t, res, 3)
	assert.Contains(t, res, "1.1.1.1")
	assert.Contains(t, res, "2.2.2.2")
	assert.Contains(t, res, "3.3.3.3")
}

func TestNamespaceNetworkInfoExists(t *testing.T) {
	ctx := context.Background()
	ctrlMock := gomock.NewController(t)

	c := mockclient.NewMockClient(ctrlMock)

	// Success
	c.EXPECT().Get(ctx, types.NamespacedName{Namespace: "default", Name: "default"}, gomock.Any()).Return(nil)
	assert.True(t, namespaceNetworkInfoExists(ctx, c, "default"))

	// Not Found
	c.EXPECT().Get(ctx, types.NamespacedName{Namespace: "default", Name: "default"}, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "default"))
	assert.False(t, namespaceNetworkInfoExists(ctx, c, "default"))

	// Other error
	c.EXPECT().Get(ctx, types.NamespacedName{Namespace: "default", Name: "default"}, gomock.Any()).Return(errors.New("other err"))
	assert.False(t, namespaceNetworkInfoExists(ctx, c, "default"))
}

func TestNetworkInfoParentRef(t *testing.T) {
	ref := networkInfoParentRef("default")
	assert.Equal(t, "crd.nsx.vmware.com", string(*ref.Group))
	assert.Equal(t, "NetworkInfo", string(*ref.Kind))
	assert.Equal(t, "default", string(ref.Name))
	assert.Equal(t, "default", string(*ref.Namespace))
}

type dummySubResourceWriter struct {
	client.SubResourceWriter
}

func (d *dummySubResourceWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return errors.New("status update error")
}

func TestGenericRouteReconciler_newRouteReconciler(t *testing.T) {
	env := setupTestEnv(t)
	r := env.reconciler
	r.StatusUpdater = setupMockStatusUpdater(env.ctrl)
	r.DNS = &dns.DNSRecordService{Service: servicecommon.Service{NSXConfig: &config.NSXOperatorConfig{}}}

	gr := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })
	assert.NotNil(t, gr)
	assert.Equal(t, "HTTPRoute", gr.kind)
	assert.NotNil(t, gr.statusUpdater)
	assert.NotEqual(t, r.StatusUpdater, gr.statusUpdater)

	// test without DNSRecordService (uses default status updater)
	r2 := &GatewayReconciler{Client: env.client, DNS: mockdns.NewMockDNSRecordProvider(env.ctrl), StatusUpdater: setupMockStatusUpdater(env.ctrl)}
	gr2 := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r2, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })
	assert.Equal(t, r2.StatusUpdater, gr2.statusUpdater)
}

func TestGatewayReconciler_NewGatewayReconciler(t *testing.T) {
	c := mockclient.NewMockClient(gomock.NewController(t))
	r := NewGatewayReconciler(setupMockManager(gomock.NewController(t), c), &dns.DNSRecordService{Service: servicecommon.Service{NSXConfig: &config.NSXOperatorConfig{}}})
	assert.NotNil(t, r)
	assert.NotNil(t, r.StatusUpdater)
}

func TestGatewayReconciler_registerGatewayDNSFieldIndexes(t *testing.T) {
	env := setupTestEnv(t)
	r := env.reconciler
	c := env.client
	r.apiResources.listenerSetEnabled = true

	idx := mockclient.NewMockFieldIndexer(env.ctrl)
	mgr := setupMockManager(env.ctrl, c)
	mgr.EXPECT().GetFieldIndexer().Return(idx).AnyTimes()

	idx.EXPECT().IndexField(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
	err := r.registerGatewayDNSFieldIndexes(mgr)
	assert.NoError(t, err)

	idx.EXPECT().IndexField(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("idx err")).Times(1)
	err = r.registerGatewayDNSFieldIndexes(mgr)
	assert.Error(t, err)

	r.apiResources.listenerSetEnabled = false
	err = r.registerGatewayDNSFieldIndexes(mgr)
	assert.NoError(t, err)
}

func TestGatewayReconciler_RestoreReconcile(t *testing.T) {
	r := &GatewayReconciler{}
	err := r.RestoreReconcile()
	assert.NoError(t, err)
}

func TestGatewayReconciler_registerRouteDNSControllers(t *testing.T) {
	env := setupTestEnv(t)
	r := env.reconciler
	c := env.client
	r.apiResources.listenerSetEnabled = false

	idx := mockclient.NewMockFieldIndexer(env.ctrl)
	mgr := setupMockManager(env.ctrl, c)
	mgr.EXPECT().GetFieldIndexer().Return(idx).AnyTimes()
	idx.EXPECT().IndexField(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	b := builder.ControllerManagedBy(mgr).For(&gatewayv1.Gateway{})
	_ = b
	err := r.registerRouteWatchers(mgr)
	assert.NoError(t, err)
}

func TestGatewayReconciler_enqueueAllRoutesForDNSResyncOnStartup(t *testing.T) {
	ctx := context.Background()
	ctrlMock := gomock.NewController(t)
	c := mockclient.NewMockClient(ctrlMock)
	r := &GatewayReconciler{Client: c, apiResources: gatewayAPIResources{}, ipCache: NewGatewayIPCache()}
	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, gatewayDNSCacheEntry{})

	c.EXPECT().List(ctx, gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
		routeList := list.(*gatewayv1.HTTPRouteList)
		routeList.Items = []gatewayv1.HTTPRoute{{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r1"}}}
		return nil
	}).Times(0)

	r.enqueueAllRoutesForDNSResyncOnStartup(ctx)
}

func TestGenericRouteReconciler_updateRouteParentConditions(t *testing.T) {
	ctx := context.Background()
	ctrlMock := gomock.NewController(t)

	c := mockclient.NewMockClient(ctrlMock)
	r := &GatewayReconciler{Client: c}
	sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })

	// mock namespaceNetworkInfoExists returning true
	c.EXPECT().Get(ctx, types.NamespacedName{Namespace: "default", Name: "default"}, gomock.Any()).Return(nil).Times(1)

	// mock Get route
	c.EXPECT().Get(ctx, types.NamespacedName{Namespace: "default", Name: "r1"}, gomock.Any()).DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		route := obj.(*gatewayv1.HTTPRoute)
		cond := buildDNSRecordReadyCondition(nil)
		cond.ObservedGeneration = 0
		route.Status.Parents = []gatewayv1.RouteParentStatus{
			{
				ControllerName: nsxOperatorGatewayDNSController,
				ParentRef:      networkInfoParentRef("default"),
				Conditions:     []metav1.Condition{cond},
			},
		}
		return nil
	}).Times(1)

	err := sub.updateRouteParentConditions(ctx, types.NamespacedName{Namespace: "default", Name: "r1"}, nil)
	assert.NoError(t, err)
}

func TestGenericRouteReconciler_buildRouteDNSEndpointsForHostname(t *testing.T) {
	ctrlMock := gomock.NewController(t)

	c := mockclient.NewMockClient(ctrlMock)
	r := &GatewayReconciler{Client: c, ipCache: NewGatewayIPCache()}
	sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })

	// mock ipCache
	gwGroup := gatewayv1.Group("gateway.networking.k8s.io")
	gwKind := gatewayv1.Kind("Gateway")
	gwRef := gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw1"}

	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, gatewayDNSCacheEntry{
		IPs: extdns.Targets{"1.1.1.1"},
		AdmissionRows: []extdnssrc.AdmissionHostCacheRow{
			{Section: "l1", Filter: "*.com"},
		},
	})

	parentRefs := []gatewayv1.ParentReference{gwRef}
	parentStatus := []gatewayv1.RouteParentStatus{
		{ParentRef: gwRef, Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}},
	}

	eps := sub.buildRouteDNSEndpointsForHostname("default", "*.com", parentRefs, parentStatus, false, true)
	assert.Len(t, eps, 0) // Should skip wildcard if allowWild is false

	eps = sub.buildRouteDNSEndpointsForHostname("default", "a.com", parentRefs, parentStatus, false, true)
	assert.Len(t, eps, 1)
	assert.Equal(t, "a.com", eps[0].DNSName)
	assert.Equal(t, extdns.Targets{"1.1.1.1"}, eps[0].Targets)

	// Test multiple gateways merge
	gwRef2 := gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw2"}
	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw2"}, gatewayDNSCacheEntry{
		IPs: extdns.Targets{"2.2.2.2"},
		AdmissionRows: []extdnssrc.AdmissionHostCacheRow{
			{Section: "l1", Filter: "*.com"},
		},
	})

	parentRefsMulti := []gatewayv1.ParentReference{gwRef, gwRef2}
	parentStatusMulti := []gatewayv1.RouteParentStatus{
		{ParentRef: gwRef, Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}},
		{ParentRef: gwRef2, Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}},
	}

	eps = sub.buildRouteDNSEndpointsForHostname("default", "a.com", parentRefsMulti, parentStatusMulti, false, true)
	assert.Len(t, eps, 1)
	assert.Equal(t, "a.com", eps[0].DNSName)
	assert.Equal(t, extdns.Targets{"1.1.1.1", "2.2.2.2"}, eps[0].Targets)
	assert.Equal(t, "default/gw1,default/gw2", eps[0].Labels[dns.EndpointLabelParentGateway])

	// Test no merge due to allowMultiGatewayTargetMerge = false
	eps = sub.buildRouteDNSEndpointsForHostname("default", "a.com", parentRefsMulti, parentStatusMulti, false, false)
	assert.Len(t, eps, 1)
	assert.Equal(t, "a.com", eps[0].DNSName)
	assert.Equal(t, extdns.Targets{"1.1.1.1"}, eps[0].Targets) // Picks best (default/gw1)

	// Add test for empty targets or no admission match
	eps = sub.buildRouteDNSEndpointsForHostname("default", "b.org", parentRefs, parentStatus, false, true)
	assert.Len(t, eps, 0)
}

func TestGenericRouteReconciler_fetchExistingOwnerNNSet(t *testing.T) {
	ctx := context.Background()
	ctrlMock := gomock.NewController(t)

	c := mockclient.NewMockClient(ctrlMock)
	sub := genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]{
		client:  c,
		newList: func() client.ObjectList { return &gatewayv1.HTTPRouteList{} },
	}

	c.EXPECT().List(ctx, gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
		routeList := list.(*gatewayv1.HTTPRouteList)
		routeList.Items = []gatewayv1.HTTPRoute{
			{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r1"}},
			{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r2"}},
		}
		return nil
	})

	nnSet, err := sub.fetchExistingOwnerNNSet(ctx, c)
	assert.NoError(t, err)
	assert.Equal(t, 2, nnSet.Len())
	assert.True(t, nnSet.Has(types.NamespacedName{Namespace: "default", Name: "r1"}))
	assert.True(t, nnSet.Has(types.NamespacedName{Namespace: "default", Name: "r2"}))
}

func TestGenericRouteReconciler_setRouteIndexField(t *testing.T) {
	ctrlMock := gomock.NewController(t)

	idx := mockclient.NewMockFieldIndexer(ctrlMock)
	mgr := setupMockManager(ctrlMock, nil)
	mgr.EXPECT().GetFieldIndexer().Return(idx).AnyTimes()

	sub := genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]{
		objCreator: newHTTPRoute,
		kind:       dns.ResourceKindHTTPRoute,
	}

	idx.EXPECT().IndexField(gomock.Any(), gomock.Any(), routeParentGatewayIndex, gomock.Any()).Return(nil)
	err := sub.setRouteIndexField(mgr, false)
	assert.NoError(t, err)

	idx.EXPECT().IndexField(gomock.Any(), gomock.Any(), routeParentGatewayIndex, gomock.Any()).Return(nil)
	idx.EXPECT().IndexField(gomock.Any(), gomock.Any(), routeParentListenerSetIndex, gomock.Any()).Return(nil)
	err = sub.setRouteIndexField(mgr, true)
	assert.NoError(t, err)
}

func TestGenericRouteReconciler_resyncRouteDNS(t *testing.T) {
	ctx := context.Background()
	ctrlMock := gomock.NewController(t)

	c := mockclient.NewMockClient(ctrlMock)
	resyncCh := make(chan event.TypedGenericEvent[*gatewayv1.HTTPRoute], 10)

	sub := genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]{
		client:     c,
		newList:    func() client.ObjectList { return &gatewayv1.HTTPRouteList{} },
		resyncCh:   resyncCh,
		objCreator: newHTTPRoute,
	}

	gwNN := types.NamespacedName{Namespace: "default", Name: "gw1"}
	lsList := []gatewayv1.ListenerSet{
		{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ls1"}},
	}

	c.EXPECT().List(ctx, gomock.Any(), client.MatchingFields{routeParentGatewayIndex: gwNN.String()}).DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
		routeList := list.(*gatewayv1.HTTPRouteList)
		routeList.Items = []gatewayv1.HTTPRoute{
			{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r1"}},
		}
		return nil
	})

	c.EXPECT().List(ctx, gomock.Any(), client.MatchingFields{routeParentListenerSetIndex: "default/ls1"}).DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
		routeList := list.(*gatewayv1.HTTPRouteList)
		routeList.Items = []gatewayv1.HTTPRoute{
			{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r1"}}, // Duplicate to test unique
			{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r2"}},
		}
		return nil
	})

	sub.resyncRouteDNS(ctx, gwNN, lsList)

	close(resyncCh)
	count := 0
	for range resyncCh {
		count++
	}
	assert.Equal(t, 2, count)
}

func TestGenericRouteReconciler_registerWatcher(t *testing.T) {
	ctrlMock := gomock.NewController(t)

	mgr := setupMockManager(ctrlMock, nil)
	sub := genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]{
		objCreator: newHTTPRoute,
		newList:    func() client.ObjectList { return &gatewayv1.HTTPRouteList{} },
	}

	opts := controller.Options{}
	err := sub.registerWatcher(mgr, opts, true)
	assert.NoError(t, err)
}

func TestGenericRouteReconciler_Reconcile(t *testing.T) {
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "r1"}}
	gwGroup := gatewayv1.Group("gateway.networking.k8s.io")
	gwKind := gatewayv1.Kind("Gateway")

	tests := []struct {
		name      string
		setupMock func(*mockclient.MockClient, *mockdns.MockDNSRecordProvider, *mockgateway.MockStatusUpdater, *gatewayIPCache, *genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute])
		wantRes   ctrl.Result
		wantErr   bool
	}{
		{
			name: "success without networkinfo",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater, ipCache *gatewayIPCache, sub *genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]) {
				c.EXPECT().Get(gomock.Any(), req.NamespacedName, gomock.Any()).Return(nil).AnyTimes()
				d.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return([]dns.EndpointRow{{Endpoint: &extdns.Endpoint{DNSName: "a.com"}}}, map[string]string{"a.com": ""}, nil).Times(1)
				d.EXPECT().CreateOrUpdateRecords(gomock.Any(), gomock.Any()).Return(true, nil).Times(1)
				c.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "default", Name: "default"}, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "default")).AnyTimes()
				s.EXPECT().UpdateSuccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			},
			wantRes: ctrl.Result{},
		},
		{
			name: "get error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater, ipCache *gatewayIPCache, sub *genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]) {
				c.EXPECT().Get(gomock.Any(), req.NamespacedName, gomock.Any()).Return(errors.New("get error")).Times(1)
			},
			wantRes: common.ResultRequeueAfter10sec,
		},
		{
			name: "cache not warmed",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater, ipCache *gatewayIPCache, sub *genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]) {
				sub.ipCacheWarmedOnStartup.Store(false)
				c.EXPECT().Get(gomock.Any(), req.NamespacedName, gomock.Any()).Return(nil).Times(1)
			},
			wantRes: ctrl.Result{RequeueAfter: 500 * time.Millisecond},
		},
		{
			name: "empty batch",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater, ipCache *gatewayIPCache, sub *genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]) {
				sub.objCreator = func(v *gatewayv1.HTTPRoute) *HTTPRoute {
					if v == nil {
						return &HTTPRoute{
							HTTPRoute: gatewayv1.HTTPRoute{
								ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r1"},
							},
						}
					}
					return &HTTPRoute{HTTPRoute: *v}
				}
				c.EXPECT().Get(gomock.Any(), req.NamespacedName, gomock.Any()).Return(nil).AnyTimes()
				d.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), "HTTPRoute", "default", "r1").Return(true, nil).Times(1)
			},
			wantRes: ctrl.Result{},
		},

		{
			name: "not found delete error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater, ipCache *gatewayIPCache, sub *genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]) {
				c.EXPECT().Get(gomock.Any(), req.NamespacedName, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "")).Times(1)
				d.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), "HTTPRoute", "default", "r1").Return(false, errors.New("delete error")).Times(1)
			},
			wantRes: common.ResultRequeueAfter10sec,
		},
		{
			name: "build error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater, ipCache *gatewayIPCache, sub *genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]) {
				c.EXPECT().Get(gomock.Any(), req.NamespacedName, gomock.Any()).Return(nil).AnyTimes()
				d.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, errors.New("build error")).Times(1)
				s.EXPECT().UpdateFail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			},
			wantRes: common.ResultRequeueAfter10sec,
		},
		{
			name: "dns zone validation error no rows delete error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater, ipCache *gatewayIPCache, sub *genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]) {
				c.EXPECT().Get(gomock.Any(), req.NamespacedName, gomock.Any()).Return(nil).AnyTimes()
				c.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "default", Name: "default"}, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "default")).AnyTimes()
				d.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, &dns.DNSZoneValidationError{}).Times(1)
				s.EXPECT().UpdateFail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
				d.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), "HTTPRoute", "default", "r1").Return(false, errors.New("delete error")).Times(1)
				sw := &dummySubResourceWriter{}
				c.EXPECT().Status().Return(sw).AnyTimes()
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

			ipCache := NewGatewayIPCache()
			ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, gatewayDNSCacheEntry{
				IPs: extdns.Targets{"1.1.1.1"},
			})
			dummyParent := &GatewayReconciler{Client: c, DNS: d, ipCache: ipCache, StatusUpdater: s}
			dummyParent.ipCacheWarmedOnStartup.Store(true)

			sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](
				dummyParent,
				"HTTPRoute",
				func(v *gatewayv1.HTTPRoute) *HTTPRoute {
					return &HTTPRoute{
						HTTPRoute: gatewayv1.HTTPRoute{
							ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r1"},
							Spec: gatewayv1.HTTPRouteSpec{
								CommonRouteSpec: gatewayv1.CommonRouteSpec{
									ParentRefs: []gatewayv1.ParentReference{{Group: &gwGroup, Kind: &gwKind, Name: "gw1"}},
								},
								Hostnames: []gatewayv1.Hostname{"a.com"},
							},
							Status: gatewayv1.HTTPRouteStatus{
								RouteStatus: gatewayv1.RouteStatus{
									Parents: []gatewayv1.RouteParentStatus{
										{
											ParentRef:  gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw1"},
											Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}},
										},
									},
								},
							},
						},
					}
				},
				func() client.ObjectList { return &gatewayv1.HTTPRouteList{} },
			)

			if tc.setupMock != nil {
				tc.setupMock(c, d, s, ipCache, sub)
			}

			res, err := sub.Reconcile(ctx, req)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.wantRes, res)
			}
		})
	}
}
