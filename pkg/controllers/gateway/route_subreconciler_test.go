package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	mockclient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mockdns "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordprovider"
	mockgateway "github.com/vmware-tanzu/nsx-operator/pkg/mock/gateway"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
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
	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, &gatewayDNSCacheEntry{
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

	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, &gatewayDNSCacheEntry{
		IPs: extdns.Targets{"1.1.1.1"},
		AdmissionRows: []extdnssrc.AdmissionHostCacheRow{
			{Filter: "a.com"},
			{Filter: "b.com"},
		},
	})

	hostnames := sub.inferRouteDNSHostnamesFromAcceptedParents("default", refs, status)
	assert.ElementsMatch(t, []string{"a.com", "b.com"}, hostnames)
}

func TestGenericRouteReconciler_inferRouteDNSHostnamesFromAcceptedParents(t *testing.T) {
	env := setupTestEnv(t)
	r := env.reconciler
	sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })

	gwGroup := gatewayv1.Group("gateway.networking.k8s.io")
	gwKind := gatewayv1.Kind("Gateway")

	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, &gatewayDNSCacheEntry{
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

	hostnames := sub.inferRouteDNSHostnamesFromAcceptedParents("default", parentRefs, parentStatus)
	assert.Len(t, hostnames, 1)
	assert.Contains(t, hostnames, "a.com")

	// test cache miss
	parentRefsMiss := []gatewayv1.ParentReference{{Group: &gwGroup, Kind: &gwKind, Name: "gw_miss"}}
	parentStatusMiss := []gatewayv1.RouteParentStatus{
		{ParentRef: parentRefsMiss[0], Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}},
	}
	hostnamesMiss := sub.inferRouteDNSHostnamesFromAcceptedParents("default", parentRefsMiss, parentStatusMiss)
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

	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, &gatewayDNSCacheEntry{
		IPs: extdns.Targets{"1.1.1.1"},
		AdmissionRows: []extdnssrc.AdmissionHostCacheRow{
			{Filter: "a.com"},
		},
	})

	objMeta := &metav1.ObjectMeta{Namespace: "default", Name: "r1"}
	specHostnames := []gatewayv1.Hostname{"a.com"}

	mockDNS.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, map[string]string{"zone": "domain"}, nil)

	owner := &dns.ResourceRef{Kind: dns.ResourceKindHTTPRoute}
	rows, allowed, admission, err := sub.buildRouteDNSEndpointsForAggregation("default", owner, refs, status, objMeta, specHostnames)

	assert.NoError(t, err)
	assert.Len(t, allowed, 1)
	assert.Nil(t, rows)
	assert.True(t, admission.hasDNSEndpoints)
	assert.True(t, admission.hasUsableAcceptedParentGateway)
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
	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, &gatewayDNSCacheEntry{})

	c.EXPECT().List(ctx, gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
		routeList := list.(*gatewayv1.HTTPRouteList)
		routeList.Items = []gatewayv1.HTTPRoute{{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r1"}}}
		return nil
	}).Times(0)

	r.enqueueAllRoutesForDNSResyncOnStartup(ctx)
}

func TestGenericRouteReconciler_updateRouteConditions(t *testing.T) {
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

	err := sub.updateRouteConditions(ctx, types.NamespacedName{Namespace: "default", Name: "r1"}, 0, nil, nil, true)
	assert.NoError(t, err)
}

func TestGenericRouteReconciler_rejectedHostnameRemovesDNSReadyWithoutNetworkInfo(t *testing.T) {
	ctx := context.Background()
	route := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "r1"}}
	route.Status.Parents = []gatewayv1.RouteParentStatus{{
		ControllerName: nsxOperatorGatewayDNSController,
		ParentRef:      networkInfoParentRef("default"),
		Conditions:     []metav1.Condition{buildDNSRecordReadyCondition(nil)},
	}}
	c := fake.NewClientBuilder().
		WithScheme(getTestScheme()).
		WithObjects(route).
		WithStatusSubresource(&gatewayv1.HTTPRoute{}).
		Build()
	r := &GatewayReconciler{Client: c}
	sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })
	hostnameCondition := &metav1.Condition{
		Type:   conditionTypeHostnameAccepted,
		Status: metav1.ConditionFalse,
		Reason: reasonFQDNsNotMatchParentListener,
	}

	assert.NoError(t, sub.updateRouteConditions(ctx, types.NamespacedName{Namespace: "default", Name: "r1"}, 0, nil, hostnameCondition, true))

	updated := &gatewayv1.HTTPRoute{}
	assert.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "r1"}, updated))
	if assert.Len(t, updated.Status.Parents, 1) {
		conditions := updated.Status.Parents[0].Conditions
		assert.Nil(t, meta.FindStatusCondition(conditions, conditionTypeDNSRecordReady))
		assert.NotNil(t, meta.FindStatusCondition(conditions, conditionTypeHostnameAccepted))
	}
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

	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, &gatewayDNSCacheEntry{
		IPs: extdns.Targets{"1.1.1.1"},
		AdmissionRows: []extdnssrc.AdmissionHostCacheRow{
			{Section: "l1", Filter: "*.com"},
		},
	})

	parentRefs := []gatewayv1.ParentReference{gwRef}
	parentStatus := []gatewayv1.RouteParentStatus{
		{ParentRef: gwRef, Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}},
	}

	eps, listenerMatched := sub.buildRouteDNSEndpointsForHostname("default", "*.com", parentRefs, parentStatus, true)
	assert.Len(t, eps, 0)           // NSX DNS records do not support wildcard names.
	assert.True(t, listenerMatched) // Listener admission is independent of wildcard DNS generation.

	eps, listenerMatched = sub.buildRouteDNSEndpointsForHostname("default", "a.com", parentRefs, parentStatus, true)
	assert.Len(t, eps, 1)
	assert.True(t, listenerMatched)
	assert.Equal(t, "a.com", eps[0].DNSName)
	assert.Equal(t, extdns.Targets{"1.1.1.1"}, eps[0].Targets)

	// Test multiple gateways merge
	gwRef2 := gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw2"}
	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw2"}, &gatewayDNSCacheEntry{
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

	eps, listenerMatched = sub.buildRouteDNSEndpointsForHostname("default", "a.com", parentRefsMulti, parentStatusMulti, true)
	assert.Len(t, eps, 1)
	assert.True(t, listenerMatched)
	assert.Equal(t, "a.com", eps[0].DNSName)
	assert.Equal(t, extdns.Targets{"1.1.1.1", "2.2.2.2"}, eps[0].Targets)
	assert.Equal(t, "default/gw1,default/gw2", eps[0].Labels[dns.EndpointLabelParentGateway])

	// Test no merge due to allowMultiGatewayTargetMerge = false
	eps, listenerMatched = sub.buildRouteDNSEndpointsForHostname("default", "a.com", parentRefsMulti, parentStatusMulti, false)
	assert.Len(t, eps, 1)
	assert.True(t, listenerMatched)
	assert.Equal(t, "a.com", eps[0].DNSName)
	assert.Equal(t, extdns.Targets{"1.1.1.1"}, eps[0].Targets) // Picks best (default/gw1)

	// Add test for empty targets or no admission match
	eps, listenerMatched = sub.buildRouteDNSEndpointsForHostname("default", "b.org", parentRefs, parentStatus, true)
	assert.Len(t, eps, 0)
	assert.False(t, listenerMatched)

	// The helper reports listener matching; the caller also checks address availability.
	r.ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw-no-address"}, &gatewayDNSCacheEntry{
		AdmissionRows: []extdnssrc.AdmissionHostCacheRow{{Section: "l1", Filter: "no-address.com"}},
	})
	gwRefNoAddress := gatewayv1.ParentReference{Group: &gwGroup, Kind: &gwKind, Name: "gw-no-address"}
	parentStatusNoAddress := []gatewayv1.RouteParentStatus{
		{ParentRef: gwRefNoAddress, Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}},
	}
	eps, listenerMatched = sub.buildRouteDNSEndpointsForHostname("default", "no-address.com", []gatewayv1.ParentReference{gwRefNoAddress}, parentStatusNoAddress, true)
	assert.Empty(t, eps)
	assert.True(t, listenerMatched)
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

type fakeSubResourceWriter struct {
	client.SubResourceWriter
}

func (f *fakeSubResourceWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return nil
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
				c.EXPECT().Status().Return(&fakeSubResourceWriter{}).AnyTimes()
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
			wantRes: ctrl.Result{RequeueAfter: ipCacheStartupWarmRequeueAfter},
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
				c.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "default", Name: "default"}, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "default")).Times(1)
				c.EXPECT().Status().Return(&fakeSubResourceWriter{}).AnyTimes()
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
			name: "build record error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater, ipCache *gatewayIPCache, sub *genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]) {
				c.EXPECT().Get(gomock.Any(), req.NamespacedName, gomock.Any()).Return(nil).Times(2)
				// mock namespaceNetworkInfoExists
				c.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: req.Namespace, Name: req.Namespace}, gomock.Any()).Return(nil).Times(1)
				c.EXPECT().Status().Return(&fakeSubResourceWriter{}).AnyTimes()
				d.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, errors.New("build record error")).Times(1)
				s.EXPECT().UpdateFail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			},
			wantRes: common.ResultRequeueAfter10sec,
		},
		{
			name: "dns zone validation error no rows delete error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater, ipCache *gatewayIPCache, sub *genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]) {
				c.EXPECT().Get(gomock.Any(), req.NamespacedName, gomock.Any()).Return(nil).AnyTimes()
				c.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "default", Name: "default"}, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "default")).Times(1)
				d.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, &dns.DNSZoneValidationError{}).Times(1)
				s.EXPECT().UpdateFail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
				d.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), "HTTPRoute", "default", "r1").Return(false, errors.New("delete error")).Times(1)
				sw := &fakeSubResourceWriter{}
				c.EXPECT().Status().Return(sw).AnyTimes()
			},
			wantRes: common.ResultRequeueAfter10sec,
		},
		{
			name: "dns zone validation error preserves valid rows",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, s *mockgateway.MockStatusUpdater, ipCache *gatewayIPCache, sub *genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]) {
				ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, &gatewayDNSCacheEntry{
					IPs:           extdns.Targets{"1.1.1.1"},
					AdmissionRows: []extdnssrc.AdmissionHostCacheRow{{Filter: "a.com"}},
				})
				c.EXPECT().Get(gomock.Any(), req.NamespacedName, gomock.Any()).Return(nil).AnyTimes()
				c.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "default", Name: "default"}, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "default")).Times(1)
				c.EXPECT().Status().Return(&fakeSubResourceWriter{}).AnyTimes()
				d.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					[]dns.EndpointRow{{Endpoint: &extdns.Endpoint{DNSName: "a.com"}}},
					map[string]string{"zone": "example.com"},
					&dns.DNSZoneValidationError{Msg: "one hostname is outside the allowed DNS zones"},
				).Times(1)
				s.EXPECT().UpdateFail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
				d.EXPECT().CreateOrUpdateRecords(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, batch *dns.AggregatedDNSEndpoints) (bool, error) {
					if assert.Len(t, batch.Rows, 1) {
						assert.Equal(t, "a.com", batch.Rows[0].DNSName)
					}
					return true, nil
				}).Times(1)
			},
			wantRes: ctrl.Result{},
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
			ipCache.put(types.NamespacedName{Namespace: "default", Name: "gw1"}, &gatewayDNSCacheEntry{
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

func TestGenericRouteReconciler_PartialValidationRetriesDNSFailure(t *testing.T) {
	ctx := context.Background()
	ref := gatewayv1.ParentReference{Name: "gw"}
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "route"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{ref}},
			Hostnames:       []gatewayv1.Hostname{"old.revoked.test", "taken.example.com"},
		},
		Status: gatewayv1.HTTPRouteStatus{RouteStatus: gatewayv1.RouteStatus{Parents: []gatewayv1.RouteParentStatus{{
			ParentRef:  ref,
			Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}},
		}}}},
	}
	c := fake.NewClientBuilder().WithScheme(getTestScheme()).WithObjects(route).
		WithStatusSubresource(&gatewayv1.HTTPRoute{}).Build()
	ctrlMock := gomock.NewController(t)
	d := mockdns.NewMockDNSRecordProvider(ctrlMock)
	s := mockgateway.NewMockStatusUpdater(ctrlMock)
	allowed := map[string]string{"allowed-zone": "example.com"}
	for i := 0; i < 2; i++ {
		validate := d.EXPECT().ValidateEndpointsByZone("ns", gomock.Any(), gomock.Any()).Return(
			[]dns.EndpointRow{*dns.NewEndpointRow(extdns.NewEndpoint("taken.example.com", "A", "192.0.2.1"), "allowed-zone", "taken")},
			allowed, &dns.DNSZoneValidationError{Msg: "revoked zone"})
		s.EXPECT().UpdateFail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any())
		d.EXPECT().CreateOrUpdateRecords(ctx, gomock.Any()).Return(false, errors.New("Gateway conflict")).After(validate)
	}
	r := &GatewayReconciler{Client: c, DNS: d, StatusUpdater: s, ipCache: NewGatewayIPCache()}
	r.ipCacheWarmedOnStartup.Store(true)
	r.ipCache.put(types.NamespacedName{Namespace: "ns", Name: "gw"}, &gatewayDNSCacheEntry{
		IPs:           extdns.Targets{"192.0.2.1"},
		AdmissionRows: []extdnssrc.AdmissionHostCacheRow{{Filter: ""}},
	})
	sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })
	for i := 0; i < 2; i++ {
		result, err := sub.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "route"}})
		assert.NoError(t, err)
		assert.Equal(t, common.ResultRequeueAfter10sec, result)
	}
}

func TestGenericRouteReconciler_DNSPolicyConditions(t *testing.T) {
	for _, tc := range []struct {
		name                string
		hostname            string
		additionalHostnames []gatewayv1.Hostname
		annotations         map[string]string
		secondParent        bool
		upsertErr           error
		listener            string
		noAddress           bool
		noZones             bool
		configErr           error
		cleanupErr          error
		skipValidation      bool
		freshRoute          bool
		wantAccepted        bool
		wantReason          string
		wantRows            int
		wantDNSFailed       bool
	}{
		{name: "new wildcard-only route", hostname: "*.example.com", listener: "*.example.com", freshRoute: true, wantReason: reasonWildcardDNSNameUnsupported},
		{name: "wildcard clears stale DNS readiness", hostname: "*.example.com", listener: "*.example.com", wantReason: reasonWildcardDNSNameUnsupported},
		{name: "wildcard cleanup failure retries", hostname: "*.example.com", listener: "*.example.com", cleanupErr: errors.New("cleanup failed"), wantReason: reasonWildcardDNSNameUnsupported},
		{name: "annotation-only wildcard", hostname: "app.example.com", annotations: map[string]string{"external-dns.kubernetes.io/gateway-hostname-source": "annotation-only", "external-dns.kubernetes.io/hostname": "*.example.com"}, listener: "*.example.com", wantReason: reasonWildcardDNSNameUnsupported},
		{name: "annotation-only missing hostname", annotations: map[string]string{"external-dns.kubernetes.io/gateway-hostname-source": "annotation-only"}, listener: "listener.example.com", skipValidation: true, wantReason: reasonFQDNsNotMatchParentListener},
		{name: "normalized annotation-only missing hostname", annotations: map[string]string{"external-dns.kubernetes.io/gateway-hostname-source": " Annotation-Only "}, listener: "listener.example.com", skipValidation: true, wantReason: reasonFQDNsNotMatchParentListener},
		{name: "annotation-only empty hostname ignores spec", hostname: "spec.example.com", annotations: map[string]string{"external-dns.kubernetes.io/gateway-hostname-source": "annotation-only", "external-dns.kubernetes.io/hostname": ""}, listener: "listener.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "blank annotation uses listener", annotations: map[string]string{"external-dns.kubernetes.io/gateway-hostname-source": "annotation-only", "external-dns.kubernetes.io/hostname": "  "}, listener: "listener.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "legacy annotation-only missing hostname", annotations: map[string]string{"external-dns.alpha.kubernetes.io/gateway-hostname-source": "annotation-only"}, listener: "listener.example.com", skipValidation: true, wantReason: reasonFQDNsNotMatchParentListener},
		{name: "legacy annotation-only explicit empty hostname", annotations: map[string]string{"external-dns.alpha.kubernetes.io/gateway-hostname-source": "annotation-only", "external-dns.alpha.kubernetes.io/hostname": ""}, listener: "listener.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "defined-hosts-only empty spec", annotations: map[string]string{"external-dns.kubernetes.io/gateway-hostname-source": "defined-hosts-only", "external-dns.kubernetes.io/hostname": "annotation.example.com"}, listener: "listener.example.com", skipValidation: true, wantReason: reasonFQDNsNotMatchParentListener},
		{name: "default infers listener", listener: "listener.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "invalid source uses default inference", annotations: map[string]string{"external-dns.kubernetes.io/gateway-hostname-source": "invalid"}, listener: "listener.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "supported hostname", hostname: "app.example.com", listener: "*.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "wildcard listener matches nested concrete hostname", hostname: "app.team.example.com", listener: "*.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "matching and nonmatching hostnames", hostname: "app.example.com", additionalHostnames: []gatewayv1.Hostname{"app.other.test"}, listener: "*.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "concrete and wildcard hostnames", hostname: "app.example.com", additionalHostnames: []gatewayv1.Hostname{"*.example.com"}, listener: "*.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "one matching parent has no address", hostname: "app.example.com", additionalHostnames: []gatewayv1.Hostname{"app.other.test"}, secondParent: true, listener: "*.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "valid spec and nonmatching annotation", hostname: "app.example.com", annotations: map[string]string{"external-dns.kubernetes.io/hostname": "app.other.test"}, listener: "*.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "annotation-only concrete ignores spec", hostname: "app.other.test", annotations: map[string]string{"external-dns.kubernetes.io/gateway-hostname-source": "annotation-only", "external-dns.kubernetes.io/hostname": "app.example.com"}, listener: "*.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1},
		{name: "mixed hostnames retain DNS operation failure", hostname: "app.example.com", additionalHostnames: []gatewayv1.Hostname{"*.example.com"}, listener: "*.example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantRows: 1, upsertErr: errors.New("record conflict"), wantDNSFailed: true},
		{name: "wildcard does not hide zone failure for concrete hostname", hostname: "app.other.test", additionalHostnames: []gatewayv1.Hostname{"*.other.test"}, listener: "*.other.test", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantDNSFailed: true},
		{name: "zone not allowed", hostname: "app.other.test", listener: "*.other.test", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantDNSFailed: true},
		{name: "zone apex", hostname: "example.com", listener: "example.com", wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantDNSFailed: true},
		{name: "no allowed zones", hostname: "app.example.com", listener: "*.example.com", noZones: true, wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantDNSFailed: true},
		{name: "configuration lookup failure", hostname: "app.example.com", listener: "*.example.com", configErr: errors.New("configuration unavailable"), wantAccepted: true, wantReason: reasonDNSHostnamesAccepted, wantDNSFailed: true},
		{name: "matched listener without address", hostname: "app.example.com", listener: "*.example.com", noAddress: true, wantReason: reasonNoValidParentGateways},
		{name: "listener wildcard does not match domain itself", hostname: "example.com", listener: "*.example.com", wantReason: reasonFQDNsNotMatchParentListener},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ref := gatewayv1.ParentReference{Name: "gw"}
			route := &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "route", Generation: 2},
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{ref}},
				},
				Status: gatewayv1.HTTPRouteStatus{RouteStatus: gatewayv1.RouteStatus{Parents: []gatewayv1.RouteParentStatus{
					{ParentRef: ref, Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}},
					{ControllerName: nsxOperatorGatewayDNSController, ParentRef: networkInfoParentRef("ns"), Conditions: []metav1.Condition{buildDNSRecordReadyCondition(nil)}},
				}}},
			}
			route.Annotations = tc.annotations
			if tc.hostname != "" {
				route.Spec.Hostnames = []gatewayv1.Hostname{gatewayv1.Hostname(tc.hostname)}
			}
			if tc.freshRoute {
				route.Status.Parents = route.Status.Parents[:1]
			}
			route.Spec.Hostnames = append(route.Spec.Hostnames, tc.additionalHostnames...)
			if tc.secondParent {
				second := gatewayv1.ParentReference{Name: "gw-no-address"}
				route.Spec.ParentRefs = append(route.Spec.ParentRefs, second)
				route.Status.Parents = append(route.Status.Parents, gatewayv1.RouteParentStatus{
					ParentRef:  second,
					Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}},
				})
			}
			c := fake.NewClientBuilder().WithScheme(getTestScheme()).WithObjects(route,
				&v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "ns"}}).
				WithStatusSubresource(&gatewayv1.HTTPRoute{}).Build()
			ctrlMock := gomock.NewController(t)
			d := mockdns.NewMockDNSRecordProvider(ctrlMock)
			if !tc.skipValidation {
				// Keep zone/apex rejection tests on real validation logic.
				nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{"zone"}}}
				if tc.noZones {
					nc.Spec.DNSZones = nil
				}
				vpc := &pkgmock.MockVPCServiceProvider{}
				vpc.On("GetVPCNetworkConfigByNamespace", "ns").Return(nc, tc.configErr).Once()
				t.Cleanup(func() { vpc.AssertExpectations(t) })
				validator := &dns.DNSRecordService{VPCService: vpc, DNSZoneMap: dns.NewDNSZoneCacheFromMap(map[string]string{"zone": "example.com"})}
				d.EXPECT().ValidateEndpointsByZone("ns", gomock.Any(), gomock.Any()).DoAndReturn(validator.ValidateEndpointsByZone)
			}
			s := mockgateway.NewMockStatusUpdater(ctrlMock)
			if tc.wantDNSFailed {
				s.EXPECT().UpdateFail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any())
			}
			if tc.wantRows > 0 {
				d.EXPECT().CreateOrUpdateRecords(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, batch *dns.AggregatedDNSEndpoints) (bool, error) {
					assert.Len(t, batch.Rows, tc.wantRows)
					return tc.upsertErr == nil, tc.upsertErr
				})
				if tc.upsertErr == nil {
					s.EXPECT().UpdateSuccess(gomock.Any(), gomock.Any(), gomock.Any())
				}
			} else if tc.configErr == nil {
				d.EXPECT().DeleteRecordByOwnerNN(ctx, "HTTPRoute", "ns", "route").Return(tc.cleanupErr == nil, tc.cleanupErr)
			}
			r := &GatewayReconciler{Client: c, DNS: d, StatusUpdater: s, ipCache: NewGatewayIPCache()}
			r.ipCacheWarmedOnStartup.Store(true)
			entry := &gatewayDNSCacheEntry{AdmissionRows: []extdnssrc.AdmissionHostCacheRow{{Filter: tc.listener}}}
			if !tc.noAddress {
				entry.IPs = extdns.Targets{"192.0.2.1"}
			}
			r.ipCache.put(types.NamespacedName{Namespace: "ns", Name: "gw"}, entry)
			if tc.secondParent {
				r.ipCache.put(types.NamespacedName{Namespace: "ns", Name: "gw-no-address"}, &gatewayDNSCacheEntry{
					AdmissionRows: []extdnssrc.AdmissionHostCacheRow{{Filter: "*.other.test"}},
				})
			}
			sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })
			key := types.NamespacedName{Namespace: "ns", Name: "route"}
			result, err := sub.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			assert.NoError(t, err)
			if tc.configErr != nil || tc.upsertErr != nil || tc.cleanupErr != nil {
				assert.Equal(t, common.ResultRequeueAfter10sec, result)
			} else {
				assert.Equal(t, common.ResultNormal, result)
			}
			updated := &gatewayv1.HTTPRoute{}
			assert.NoError(t, c.Get(ctx, key, updated))
			found := false
			for _, parent := range updated.Status.Parents {
				if parent.ControllerName != nsxOperatorGatewayDNSController {
					continue
				}
				found = true
				hostname := meta.FindStatusCondition(parent.Conditions, conditionTypeHostnameAccepted)
				if assert.NotNil(t, hostname) {
					want := metav1.ConditionFalse
					if tc.wantAccepted {
						want = metav1.ConditionTrue
					}
					assert.Equal(t, want, hostname.Status)
					assert.Equal(t, tc.wantReason, hostname.Reason)
				}
				ready := meta.FindStatusCondition(parent.Conditions, conditionTypeDNSRecordReady)
				if !tc.wantAccepted {
					assert.Nil(t, ready)
				} else if assert.NotNil(t, ready) {
					if tc.wantDNSFailed {
						assert.Equal(t, metav1.ConditionFalse, ready.Status)
						assert.Equal(t, reasonDNSRecordFailed, ready.Reason)
					} else {
						assert.Equal(t, metav1.ConditionTrue, ready.Status)
						assert.Equal(t, reasonDNSRecordConfigured, ready.Reason)
					}
				}
			}
			assert.True(t, found, "controller parent status must exist")
		})
	}
}

func TestGenericRouteReconciler_StatusUsesProcessedGeneration(t *testing.T) {
	ctx := context.Background()
	ref := gatewayv1.ParentReference{Name: "gw"}
	route := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "route", Generation: 1}, Spec: gatewayv1.HTTPRouteSpec{CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{ref}}, Hostnames: []gatewayv1.Hostname{"old.example.com"}}, Status: gatewayv1.HTTPRouteStatus{RouteStatus: gatewayv1.RouteStatus{Parents: []gatewayv1.RouteParentStatus{{ParentRef: ref, Conditions: []metav1.Condition{{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue}}}}}}}
	c := fake.NewClientBuilder().WithScheme(getTestScheme()).WithObjects(route, &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "ns"}}).WithStatusSubresource(&gatewayv1.HTTPRoute{}).Build()
	d := mockdns.NewMockDNSRecordProvider(gomock.NewController(t))
	d.EXPECT().ValidateEndpointsByZone("ns", gomock.Any(), gomock.Any()).DoAndReturn(func(_ string, _ *dns.ResourceRef, eps []*extdns.Endpoint) ([]dns.EndpointRow, map[string]string, error) {
		return []dns.EndpointRow{*dns.NewEndpointRow(eps[0], "zone", "old")}, map[string]string{"zone": "example.com"}, nil
	})
	d.EXPECT().CreateOrUpdateRecords(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, batch *dns.AggregatedDNSEndpoints) (bool, error) {
		assert.Equal(t, "old.example.com", batch.Rows[0].DNSName)
		latest := &gatewayv1.HTTPRoute{}
		assert.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(route), latest))
		latest.Generation = 2
		latest.Spec.Hostnames = []gatewayv1.Hostname{"new.example.com"}
		assert.NoError(t, c.Update(ctx, latest))
		return true, nil
	})
	s := mockgateway.NewMockStatusUpdater(gomock.NewController(t))
	s.EXPECT().UpdateSuccess(gomock.Any(), gomock.Any(), gomock.Any())
	r := &GatewayReconciler{Client: c, DNS: d, StatusUpdater: s, ipCache: NewGatewayIPCache()}
	r.ipCacheWarmedOnStartup.Store(true)
	r.ipCache.put(types.NamespacedName{Namespace: "ns", Name: "gw"}, &gatewayDNSCacheEntry{IPs: extdns.Targets{"192.0.2.1"}, AdmissionRows: []extdnssrc.AdmissionHostCacheRow{{Filter: "*.example.com"}}})
	sub := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })
	_, err := sub.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(route)})
	assert.NoError(t, err)
	updated := &gatewayv1.HTTPRoute{}
	assert.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(route), updated))
	for _, parent := range updated.Status.Parents {
		if parent.ControllerName != nsxOperatorGatewayDNSController {
			continue
		}
		assert.Len(t, parent.Conditions, 2)
		for _, cond := range parent.Conditions {
			assert.Equal(t, int64(1), cond.ObservedGeneration, "must not certify an unprocessed generation")
		}
		return
	}
	t.Fatal("controller parent status is missing")
}
