/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	mockcache "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/cache"
	mockclient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extdnssrc "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/source"
)

const (
	testNSApp   = "app"
	testGW1Name = "gw1"
)

var testNNGW1 = types.NamespacedName{Namespace: testNSApp, Name: testGW1Name}

func TestBuildRouteDNSEndpointsForAggregation_table(t *testing.T) {
	ctx := context.Background()
	const ns, gwName = "app", "gw1"
	nsObj := testNamespaceAllowedDNSZones(ns, map[string]string{"example.com": "/zones/t"})
	gwWildcard := testGatewayManaged(ns, gwName, "*.example.com", "10.0.0.5", "")
	hrMeta := func(anno map[string]string, _ ...gatewayv1.Hostname) *metav1.ObjectMeta {
		return &metav1.ObjectMeta{Namespace: ns, Name: "hr1", Annotations: anno}
	}
	parentSection := gatewayv1.SectionName("http")
	prGw := gatewayv1.ParentReference{
		Group:       ptrGWGroup(gatewayv1.GroupName),
		Kind:        ptrGWKind("Gateway"),
		Name:        gatewayv1.ObjectName(gwName),
		SectionName: &parentSection,
	}
	parentStatusGw := []gatewayv1.RouteParentStatus{{
		ParentRef: prGw,
		Conditions: []metav1.Condition{{
			Type:   string(gatewayv1.RouteConditionAccepted),
			Status: metav1.ConditionTrue,
		}},
	}}
	lsRef := listenerSetParentRefForRoute(ns, ns, "ls1")
	parentStatusLS := []gatewayv1.RouteParentStatus{{
		ParentRef: lsRef,
		Conditions: []metav1.Condition{{
			Type:   string(gatewayv1.RouteConditionAccepted),
			Status: metav1.ConditionTrue,
		}},
	}}

	tests := []struct {
		name           string
		overrideGW     *gatewayv1.Gateway
		extraObjs      []client.Object
		parentRefs     []gatewayv1.ParentReference
		parentStatusIn []gatewayv1.RouteParentStatus
		meta           *metav1.ObjectMeta
		specHosts      []gatewayv1.Hostname
		// wantHosts empty/nil => expect no endpoints; otherwise each DNSName must appear (superset allowed).
		wantHosts []string
	}{
		{
			name:      "spec_hostname_matches_listener",
			meta:      hrMeta(nil, "svc.example.com"),
			specHosts: []gatewayv1.Hostname{"svc.example.com"},
			wantHosts: []string{"svc.example.com"},
		},
		{
			name: "annotation_hostname_overrides_spec",
			meta: hrMeta(map[string]string{
				servicecommon.AnnotationDNSHostnameSourceKey: extdnssrc.GatewayHostnameSourceAnnotationOnly,
				servicecommon.AnnotationDNSHostnameKey:       "anno-only.example.com",
			}),
			specHosts: []gatewayv1.Hostname{"invalid.example.com"},
			wantHosts: []string{"anno-only.example.com"},
		},
		{
			name: "spec_and_annotation_merged_defined_hosts",
			meta: hrMeta(map[string]string{
				servicecommon.AnnotationDNSHostnameKey: "extra.example.com",
			}),
			specHosts: []gatewayv1.Hostname{"route.example.com"},
			wantHosts: []string{"route.example.com", "extra.example.com"},
		},
		{
			name:       "infer_hostname_from_gateway_listener_no_route_hosts",
			overrideGW: testGatewayManaged(ns, gwName, "api.example.com", "10.0.0.5", ""),
			meta:       hrMeta(nil),
			specHosts:  nil,
			wantHosts:  []string{"api.example.com"},
		},
		{
			name:           "infer_hostname_via_listenerset_parent",
			extraObjs:      []client.Object{testListenerSet(ns, "ls1", ns, gwName)},
			parentRefs:     []gatewayv1.ParentReference{lsRef},
			parentStatusIn: parentStatusLS,
			meta:           hrMeta(nil),
			specHosts:      nil,
			wantHosts:      []string{"hello.example.com"},
		},
		{
			name:      "wildcard_listener_only_no_route_hosts_no_eps_without_wildcard_anno",
			meta:      hrMeta(nil),
			specHosts: nil,
			wantHosts: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tcGW := gwWildcard
			if tc.overrideGW != nil {
				tcGW = tc.overrideGW
			}
			objs := []client.Object{nsObj, tcGW}
			objs = append(objs, tc.extraObjs...)
			gr, fc := fakeGatewayReconciler(t, objs...)
			gr.Client = fc
			gr.apiResources = apiResourcesForTests(true, true, false, false)
			gr.ipCache = NewGatewayIPCache()
			gr.ipCacheWarmedOnStartup.Store(true)
			refreshIPCache(ctx, t, gr, tcGW)

			refs := tc.parentRefs
			if refs == nil {
				refs = []gatewayv1.ParentReference{prGw}
			}
			ps := tc.parentStatusIn
			if ps == nil {
				ps = parentStatusGw
			}
			owner := &dns.ResourceRef{Kind: dns.ResourceKindHTTPRoute, Object: tc.meta}
			eps, err := gr.buildRouteDNSEndpointsForAggregation(gr.Service, ns, owner, refs, ps, tc.meta, tc.specHosts)
			require.NoError(t, err)
			if len(tc.wantHosts) == 0 {
				require.Empty(t, eps)
				return
			}
			got := make([]string, 0, len(eps))
			for _, ep := range eps {
				got = append(got, ep.DNSName)
			}
			for _, w := range tc.wantHosts {
				require.Contains(t, got, w, "missing DNSName %q in %+v", w, got)
			}
		})
	}
}

func TestGatewayReconcile_table(t *testing.T) {
	ctx := context.Background()
	const ns = testNSApp
	nsObj := testNamespace(ns)
	nsObjDNS := testNamespaceAllowedDNSZones(ns, map[string]string{"example.com": "/zones/t"})
	gwOK := testGatewayManaged(ns, "gw1", "*.example.com", "10.0.0.1", "")
	gwOKDNS := withDNSHostnameAnnotation(gwOK.DeepCopy(), "gw-direct.example.com")

	gwDNSBuildFail := gwOK.DeepCopy()
	gwDNSBuildFail.Annotations = map[string]string{
		servicecommon.AnnotationDNSHostnameKey: "svc.no-such-zone.invalid",
	}

	gwNoIP := gwOK.DeepCopy()
	gwNoIP.Status.Addresses = nil

	gwUnmanaged := gwOK.DeepCopy()
	gwUnmanaged.Spec.GatewayClassName = "Contour"

	syncUpdateFail := []string{callIncreaseSyncTotal, callIncreaseUpdateTotal, callUpdateFail}
	syncUpdateOK := []string{callIncreaseSyncTotal, callIncreaseUpdateTotal, callUpdateSuccess}
	syncDeleteOK := []string{callIncreaseSyncTotal, callIncreaseDeleteTotal, callDeleteSuccess}

	tests := []struct {
		name            string
		objs            []client.Object
		clientWrap      func(client.Client) client.Client
		apiListenerSet  bool
		apiHTTPRoute    bool
		apiGRPCRoute    bool
		req             types.NamespacedName
		wantErr         bool
		validateStatus  bool
		wantStatusCalls []string
	}{
		{
			name:            "statusUpdaterOnDNSBuildFail",
			objs:            []client.Object{gwDNSBuildFail},
			apiListenerSet:  true,
			wantErr:         true,
			validateStatus:  true,
			wantStatusCalls: syncUpdateFail,
			req:             types.NamespacedName{Namespace: ns, Name: "gw1"},
		},
		{
			name:            "statusUpdater_success",
			objs:            []client.Object{nsObjDNS, gwOKDNS},
			apiListenerSet:  true,
			wantErr:         false,
			validateStatus:  true,
			wantStatusCalls: syncUpdateOK,
			req:             types.NamespacedName{Namespace: ns, Name: "gw1"},
		},
		{
			name:            "gatewayNotFound_runsDeletePath",
			objs:            nil,
			apiListenerSet:  true,
			apiHTTPRoute:    true,
			wantErr:         false,
			validateStatus:  true,
			wantStatusCalls: syncDeleteOK,
			req:             types.NamespacedName{Namespace: ns, Name: "missing-gw"},
		},
		{
			name:            "noUsableGatewayIP_triggersDelete",
			objs:            []client.Object{nsObj, gwNoIP},
			apiListenerSet:  true,
			wantErr:         false,
			validateStatus:  true,
			wantStatusCalls: syncDeleteOK,
			req:             types.NamespacedName{Namespace: ns, Name: "gw1"},
		},
		{
			name:            "unmanagedGatewayClass_triggersDelete",
			objs:            []client.Object{nsObj, gwUnmanaged},
			apiListenerSet:  true,
			wantErr:         false,
			validateStatus:  true,
			wantStatusCalls: syncDeleteOK,
			req:             types.NamespacedName{Namespace: ns, Name: "gw1"},
		},
		{
			name: "listListenerSetsFails",
			objs: []client.Object{nsObj, gwOK},
			clientWrap: func(c client.Client) client.Client {
				return &listErrorListenerSetClient{Client: c, listErr: fmt.Errorf("list listener sets failed")}
			},
			apiListenerSet: true,
			apiHTTPRoute:   true,
			req:            types.NamespacedName{Namespace: ns, Name: "gw1"},
			wantErr:        true,
			validateStatus: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := fakeClientForGatewayTests(tt.objs...)
			if tt.clientWrap != nil {
				fc = tt.clientWrap(fc)
			}
			gr := newTestGatewayReconciler(t, fc, apiResourcesForTests(tt.apiListenerSet, tt.apiHTTPRoute, tt.apiGRPCRoute, false))

			if tt.validateStatus {
				gr.StatusUpdater = newMockStatusUpdaterWithCallChain(t, tt.wantStatusCalls)
			} else {
				gr.StatusUpdater = newMockStatusUpdater(t)
			}
			gr.ipCacheWarmedOnStartup.Store(true)

			_, err := gr.Reconcile(ctx, ctrl.Request{NamespacedName: tt.req})
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGatewayIPCache_putGetDelete_andList(t *testing.T) {
	c := NewGatewayIPCache()
	nn := types.NamespacedName{Namespace: "ns", Name: "gw1"}
	row := extdnssrc.AdmissionHostCacheRow{Section: gatewayv1.SectionName("http"), Filter: "example.com"}
	e1 := gatewayDNSCacheEntry{
		IPs:             extdns.NewTargets("10.0.0.1"),
		AdmissionRows:   []extdnssrc.AdmissionHostCacheRow{row},
		GatewayResource: nn,
	}
	require.True(t, c.put(nn, e1), "first put should report material change")
	require.False(t, c.put(nn, e1), "identical put should not report change")

	e2 := gatewayDNSCacheEntry{
		IPs:             extdns.NewTargets("10.0.0.2"),
		AdmissionRows:   []extdnssrc.AdmissionHostCacheRow{row},
		GatewayResource: nn,
	}
	require.True(t, c.put(nn, e2), "changed IPs should report material change")

	got, ok := c.get(nn)
	require.True(t, ok)
	require.Equal(t, e2.IPs, got.IPs)

	listed := c.listGatewayNamespacedNames()
	require.Contains(t, listed, nn)

	require.True(t, c.delete(nn), "delete should report an entry existed")
	_, ok = c.get(nn)
	require.False(t, ok)
	require.False(t, c.delete(nn), "second delete is a no-op")
}

func TestGatewayDNSPureHelpers_table(t *testing.T) {
	t.Run("gatewayDNSCacheEntryEqual", func(t *testing.T) {
		nn := types.NamespacedName{Namespace: "a", Name: "b"}
		ar := extdnssrc.AdmissionHostCacheRow{Section: gatewayv1.SectionName("http"), Filter: "*.x"}
		base := gatewayDNSCacheEntry{IPs: extdns.NewTargets("10.0.0.1"), AdmissionRows: []extdnssrc.AdmissionHostCacheRow{ar}, GatewayResource: nn}
		otherIP := gatewayDNSCacheEntry{IPs: extdns.NewTargets("10.0.0.2"), AdmissionRows: []extdnssrc.AdmissionHostCacheRow{ar}, GatewayResource: nn}
		tests := []struct {
			name string
			a, b gatewayDNSCacheEntry
			want bool
		}{
			{"equal", base, base, true},
			{"different_targets", base, otherIP, false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				require.Equal(t, tt.want, gatewayDNSCacheEntryEqual(tt.a, tt.b))
			})
		}
	})
	t.Run("filterDirectDNSEndpointsAgainstSeenHostnames", func(t *testing.T) {
		seen := sets.New[string]()
		ep1 := dns.EndpointRow{Endpoint: &extdns.Endpoint{DNSName: "first.example.com", RecordType: extdns.RecordTypeA}}
		out := filterDirectDNSEndpointsAgainstSeenHostnames(seen, []dns.EndpointRow{ep1})
		require.Len(t, out, 1)
		out2 := filterDirectDNSEndpointsAgainstSeenHostnames(seen, []dns.EndpointRow{ep1})
		require.Empty(t, out2)
	})
	t.Run("listenersSortedByName", func(t *testing.T) {
		tests := []struct {
			name string
			in   []gatewayv1.Listener
			want []string
		}{
			{name: "nil", in: nil, want: nil},
			{
				name: "sorted_by_name",
				in: []gatewayv1.Listener{
					{Name: "z"},
					{Name: "a"},
				},
				want: []string{"a", "z"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				out := listenersSortedByName(tt.in)
				if tt.want == nil {
					require.Nil(t, out)
					return
				}
				names := make([]string, len(out))
				for i := range out {
					names[i] = string(out[i].Name)
				}
				require.Equal(t, tt.want, names)
			})
		}
	})
	t.Run("loopObjectList_nil_list_pointer", func(t *testing.T) {
		ctx := context.Background()
		fc := fakeClientForGatewayTests()
		var nilList *gatewayv1.HTTPRouteList
		err := loopObjectList[gatewayv1.HTTPRoute](ctx, fc, nilList, func(_ *gatewayv1.HTTPRoute) {})
		require.Error(t, err)
	})
}

func TestShouldProcessGateway_and_gatewayDNSIgnored_table(t *testing.T) {
	managed := testGatewayManaged("ns", "g", "*.x", "10.0.0.1", "")
	unmanaged := managed.DeepCopy()
	unmanaged.Spec.GatewayClassName = "SomeOtherClass"
	ignored := managed.DeepCopy()
	ignored.Annotations = map[string]string{servicecommon.AnnotationsDNSIgnoreGateway: "true"}

	tests := []struct {
		name           string
		gw             *gatewayv1.Gateway
		wantProcess    bool
		wantDNSIgnored bool
	}{
		{"managed_class", managed, true, false},
		{"unmanaged_class", unmanaged, false, false},
		{"ignore_annotation", ignored, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantProcess, shouldProcessGateway(tc.gw))
			require.Equal(t, tc.wantDNSIgnored, gatewayDNSIgnored(tc.gw))
		})
	}
}

func TestHasUsableGatewayIP_table(t *testing.T) {
	gwOK := testGatewayManaged("ns", "g", "*.x", "10.0.0.1", "")
	gwIPv6OK := testGatewayManaged("ns", "g", "*.x", "", "ab01::1234")
	gwBad := gwOK.DeepCopy()
	gwBad.Status.Addresses = nil
	gwHostname := gwOK.DeepCopy()
	gwHostname.Status.Addresses = []gatewayv1.GatewayStatusAddress{{Type: addrTypePtr(), Value: "not-an-ip"}}

	tests := []struct {
		name string
		gw   *gatewayv1.Gateway
		want bool
	}{
		{"has_ip", gwOK, true},
		{"has_ipv6", gwIPv6OK, true},
		{"no_addresses", gwBad, false},
		{"unparseable_ip", gwHostname, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, hasUsableGatewayIP(tt.gw))
		})
	}
}

func TestDeleteDNSRecordsOwnedByGateway_clearDNSConfigConditions(t *testing.T) {
	ctx := context.Background()
	nsObj := testNamespace("app")
	gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
	gw.Status.Conditions = []metav1.Condition{{Type: conditionTypeDNSConfig, Status: metav1.ConditionTrue, Reason: reasonDNSRecordConfigured}}
	ls := testListenerSet("app", "ls1", "app", "gw1")
	ls.Status.Conditions = []metav1.Condition{{Type: conditionTypeDNSConfig, Status: metav1.ConditionTrue, Reason: reasonDNSRecordConfigured}}
	fc := fakeClientForGatewayTests(nsObj, gw, ls)
	gr := newTestGatewayReconciler(t, fc, apiResourcesForTests(true, true, true, false))
	gr.StatusUpdater = newMockStatusUpdater(t)
	gr.ipCacheWarmedOnStartup.Store(true)

	_, err := gr.deleteUnmanagedGateway(ctx, gw, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: "gw1"}}, true)
	require.NoError(t, err)
}

func TestListenerSetToGatewayMapFunc_table(t *testing.T) {
	ctx := context.Background()
	ns := testNSApp
	nsObj := testNamespace(ns)
	gwManaged := testGatewayManaged(ns, testGW1Name, "*.example.com", "10.0.0.1", "")
	ls := testListenerSet(ns, "ls1", ns, testGW1Name)
	gwUnmanaged := gwManaged.DeepCopy()
	gwUnmanaged.Spec.GatewayClassName = "UnmanagedClass"

	tests := []struct {
		name    string
		objs    []client.Object
		subject client.Object
		wantNNs []types.NamespacedName
	}{
		{
			name:    "enqueues_managed_parent",
			objs:    []client.Object{nsObj, gwManaged, ls},
			subject: ls,
			wantNNs: []types.NamespacedName{testNNGW1},
		},
		{
			name:    "gateway_get_fails_returns_empty",
			objs:    []client.Object{nsObj, ls},
			subject: ls,
		},
		{
			name:    "non_listener_set_returns_empty",
			subject: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}},
		},
		{
			name:    "skips_unmanaged_parent_gateway",
			objs:    []client.Object{nsObj, gwUnmanaged, ls},
			subject: ls,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gr := newTestGatewayReconcilerFromObjects(t, apiResourcesForTests(true, true, false, false), tt.objs...)
			got := gr.listenerSetToGatewayMapFunc(ctx, tt.subject)
			var nns []types.NamespacedName
			for _, r := range got {
				nns = append(nns, r.NamespacedName)
			}
			require.Equal(t, tt.wantNNs, nns)
		})
	}
}

func TestPredicateFuncs_RouteDNS_and_Gateway_table(t *testing.T) {
	gr, _ := fakeGatewayReconciler(t)
	ad := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute](gr, dns.ResourceKindHTTPRoute, func() *HTTPRoute { return &HTTPRoute{} })
	routePred := ad.predicateFuncsRouteDNS()

	hrEmpty := testHTTPRouteAccepted("app", "hr", "u1", "", "app", "gw1")
	hrEmpty.Spec.Hostnames = nil
	hrOk := testHTTPRouteAccepted("app", "hr2", "u2", "h.example.com", "app", "gw1")
	hrOld := testHTTPRouteAccepted("app", "hr", "u1", "a.example.com", "app", "gw1")
	hrNewHost := hrOld.DeepCopy()
	hrNewHost.Spec.Hostnames = []gatewayv1.Hostname{"b.example.com"}
	hrAccOld := testHTTPRouteAccepted("app", "hr2", "u2", "x.example.com", "app", "gw1")
	hrAccNew := hrAccOld.DeepCopy()
	hrAccNew.Status.Parents[0].Conditions[0].Status = metav1.ConditionFalse
	hrStable := testHTTPRouteAccepted("app", "hr3", "u3", "z.example.com", "app", "gw1")

	gwOK := testGatewayManaged("app", "g1", "*.x", "10.0.0.1", "")
	gwNoIP := gwOK.DeepCopy()
	gwNoIP.Status.Addresses = nil
	gwBadClass := gwOK.DeepCopy()
	gwBadClass.Spec.GatewayClassName = "Other"
	gwNewAddr := gwOK.DeepCopy()
	gwNewAddr.Status.Addresses[0].Value = "10.0.0.2"
	gwG2 := testGatewayManaged("app", "g2", "*.x", "10.0.0.1", "")

	hrDel := testHTTPRouteAccepted(testNSApp, "h", "u1", "x.example.com", testNSApp, testGW1Name)
	gwDel := testGatewayManaged(testNSApp, "g", "*.x", "10.0.0.1", "")

	tests := []struct {
		name string
		want bool
		f    func() bool
	}{
		{"route_create_empty_host", false, func() bool { return routePred.CreateFunc(event.CreateEvent{Object: hrEmpty}) }},
		{"route_create_ok", true, func() bool { return routePred.CreateFunc(event.CreateEvent{Object: hrOk}) }},
		{"route_update_hostnames", true, func() bool { return routePred.UpdateFunc(event.UpdateEvent{ObjectOld: hrOld, ObjectNew: hrNewHost}) }},
		{"route_update_accepted", true, func() bool { return routePred.UpdateFunc(event.UpdateEvent{ObjectOld: hrAccOld, ObjectNew: hrAccNew}) }},
		{"route_update_noop", false, func() bool {
			return routePred.UpdateFunc(event.UpdateEvent{ObjectOld: hrStable, ObjectNew: hrStable.DeepCopy()})
		}},
		{"gateway_create_ok", true, func() bool { return predicateFuncsGateway.CreateFunc(event.CreateEvent{Object: gwOK}) }},
		{"gateway_create_no_ip", false, func() bool { return predicateFuncsGateway.CreateFunc(event.CreateEvent{Object: gwNoIP}) }},
		{"gateway_create_bad_class", false, func() bool { return predicateFuncsGateway.CreateFunc(event.CreateEvent{Object: gwBadClass}) }},
		{"gateway_update_address", true, func() bool {
			return predicateFuncsGateway.UpdateFunc(event.UpdateEvent{ObjectOld: gwOK, ObjectNew: gwNewAddr})
		}},
		{"gateway_update_noop", false, func() bool {
			return predicateFuncsGateway.UpdateFunc(event.UpdateEvent{ObjectOld: gwG2, ObjectNew: gwG2.DeepCopy()})
		}},
		{"gateway_delete", true, func() bool { return predicateFuncsGateway.DeleteFunc(event.DeleteEvent{Object: gwDel}) }},
		{"route_delete", true, func() bool { return routePred.DeleteFunc(event.DeleteEvent{Object: hrDel}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.f())
		})
	}
}

func TestGc_httpRouteDisabled_listOwnersAndPruneStore(t *testing.T) {
	ctx := context.Background()
	nsObj := testNamespace("app")
	hr := testHTTPRouteAccepted("app", "hr1", "u1", "x.example.com", "app", "gw1")
	fcList := fakeClientForGatewayTests(nsObj, hr)
	flagsPartial := apiResourcesForTests(true, false, true, false)
	got, err := gcListExistingOwners(ctx, fcList, flagsPartial)
	require.NoError(t, err)
	require.True(t, got[dns.ResourceKindHTTPRoute].Len() == 0)
	require.Equal(t, 0, got[dns.ResourceKindGRPCRoute].Len())

	nsObjDNS := testNamespaceAllowedDNSZones("app", map[string]string{"example.com": "/zones/z1"})
	gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
	hr2 := testHTTPRouteAccepted("app", "hr1", "uid-hr1", "gc-off.example.com", "app", "gw1")
	fc := fakeClientForGatewayTests(nsObjDNS, gw, hr2)
	svc := &dns.DNSRecordService{
		Service: servicecommon.Service{
			Client:    fc,
			NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}},
		},
		DNSRecordStore: dns.BuildDNSRecordStore(),
		VPCService:     gatewayTestVPCMock(),
		DNSZoneMap:     gatewayTestVPCDNSZoneMap(),
	}
	gr := newTestGatewayReconcilerWithService(t, fc, apiResourcesForTests(true, true, false, false), svc)
	gr.ipCache = NewGatewayIPCache()
	gr.ipCacheWarmedOnStartup.Store(true)
	refreshIPCache(ctx, t, gr, gw)
	ep := extdns.NewEndpoint("gc-off.example.com", extdns.RecordTypeA, "10.0.0.1")
	ep.WithLabel(dns.EndpointLabelParentGateway, "app/gw1")
	hrOwner := &dns.ResourceRef{Kind: dns.ResourceKindHTTPRoute, Object: hr2.GetObjectMeta()}
	stable := stableDNSRecordIDWithDNSZone("uid-hr1", "gc-off.example.com", extdns.RecordTypeA, "/zones/z1")
	row := dns.NewEndpointRow(ep, "/zones/z1", "/orgs/org1/projects/proj1/vpcs/vpc1", stable)
	_, err = svc.CreateOrUpdateDNSRecords(ctx, dns.NewOwnerScopedAggregatedRouteDNS(hrOwner, []dns.EndpointRow{*row}))
	require.NoError(t, err)
	storeKey := "/orgs/org1/projects/proj1/vpcs/vpc1/" + strings.ReplaceAll(stable, ".", "_") + "_" + strings.ToLower(string(extdns.RecordTypeA))
	require.NotNil(t, svc.DNSRecordStore.GetByKey(storeKey))
	flagsGC := apiResourcesForTests(true, false, false, false)
	require.NoError(t, gcOwnerMissingDNSRecords(ctx, gr, flagsGC, sets.New[types.NamespacedName]()))
	require.Nil(t, svc.DNSRecordStore.GetByKey(storeKey))
}

func TestCollectGarbage_deletesStaleGatewayIndex(t *testing.T) {
	ctx := context.Background()
	nsObj := testNamespace("app")
	gwAlive := testGatewayManaged("app", "alive", "*.example.com", "10.0.0.1", "")
	fc := fakeClientForGatewayTests(nsObj, gwAlive)
	svc := dnsTestServiceWithClient(fc)
	gr := newTestGatewayReconcilerWithService(t, fc, apiResourcesForTests(true, true, true, false), svc)
	gr.StatusUpdater = newMockStatusUpdater(t)
	gr.ipCacheWarmedOnStartup.Store(true)

	staleOwner := &dns.ResourceRef{
		Kind:   dns.ResourceKindHTTPRoute,
		Object: &metav1.ObjectMeta{Namespace: "ghost", Name: "gone", UID: types.UID("uid-stale-hr")},
	}
	ep := extdns.NewEndpoint("stale-gc.example.com", extdns.RecordTypeA, "192.0.2.55")
	ep.WithLabel(dns.EndpointLabelParentGateway, "ghost/gone")
	stableStale := stableDNSRecordIDWithDNSZone("uid-stale-hr", "stale-gc.example.com", extdns.RecordTypeA, "/zones/z1")
	staleRow := dns.NewEndpointRow(ep, "/zones/z1", "/orgs/org1/projects/proj1/vpcs/vpc1", stableStale)
	_, err := svc.CreateOrUpdateDNSRecords(ctx, dns.NewOwnerScopedAggregatedRouteDNS(staleOwner, []dns.EndpointRow{*staleRow}))
	require.NoError(t, err)
	const idxGatewayNN = "gatewayNamespacedName"
	before := svc.DNSRecordStore.GetByIndex(idxGatewayNN, "ghost/gone")
	require.NotEmpty(t, before)

	require.NoError(t, gr.CollectGarbage(ctx))
	after := svc.DNSRecordStore.GetByIndex(idxGatewayNN, "ghost/gone")
	require.Empty(t, after)
}

func TestParentGatewaysFromParentRefs_and_routeParentGatewayAcceptedByKey(t *testing.T) {
	pr := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("Gateway"),
		Name:  gatewayv1.ObjectName("gw1"),
	}
	ns := "team"
	got := parentGatewaysFromParentRefs(ns, []gatewayv1.ParentReference{pr})
	require.Len(t, got, 1)
	require.Equal(t, types.NamespacedName{Namespace: "team", Name: "gw1"}, got[0])

	ps := []gatewayv1.RouteParentStatus{{
		ParentRef: pr,
		Conditions: []metav1.Condition{{
			Type:   string(gatewayv1.RouteConditionAccepted),
			Status: metav1.ConditionTrue,
		}},
	}}
	m := routeParentGatewayAcceptedByKey(ns, ps)
	require.Equal(t, string(metav1.ConditionTrue), m[types.NamespacedName{Namespace: "team", Name: "gw1"}.String()])
}

func TestFindParentGatewayFromListenerSet_table(t *testing.T) {
	lsOK := testListenerSet("app", "ls1", "app", "gw1")
	lsBad := lsOK.DeepCopy()
	lsBad.Spec.ParentRef.Kind = ptrGWKind("NotAGateway")

	tests := []struct {
		name string
		obj  client.Object
		want *types.NamespacedName
	}{
		{"nil_object", nil, nil},
		{"not_listenerset", &corev1.Pod{}, nil},
		{"valid", lsOK, &types.NamespacedName{Namespace: "app", Name: "gw1"}},
		{"invalid_parent_ref", lsBad, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findParentGatewayFromListenerSet(tt.obj)
			require.Equal(t, tt.want, got)
			if tt.name == "valid" {
				ls := tt.obj.(*gatewayv1.ListenerSet)
				require.Equal(t, []string{"app/gw1"}, listenerSetParentGatewayIndexFunc(ls))
				p, ok := gatewayNamespacedNameFromListenerParentRef(&ls.Spec.ParentRef, ls.Namespace)
				require.True(t, ok)
				require.Equal(t, "app/gw1", p.String())
			}
		})
	}
	t.Run("gatewayNamespacedNameFromListenerParentRef_nil_parent", func(t *testing.T) {
		got, ok := gatewayNamespacedNameFromListenerParentRef(nil, "ns")
		require.False(t, ok)
		require.Equal(t, types.NamespacedName{}, got)
	})
}

func TestRouteParentListenerSetIndexFunc_duplicateRefsDeduped(t *testing.T) {
	pr := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("ListenerSet"),
		Name:  gatewayv1.ObjectName("ls1"),
	}
	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "app", Name: "hr", UID: types.UID("uid")},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{pr, pr}},
			Hostnames:       []gatewayv1.Hostname{"x.example.com"},
		},
	}
	require.Equal(t, []string{types.NamespacedName{Namespace: "app", Name: "ls1"}.String()}, routeParentListenerSetIndexFunc(hr))
}

func TestRouteParentIndexFuncs(t *testing.T) {
	hr := testHTTPRouteViaListenerSet("app", "hr", "u1", "h.example.com", "app", "ls1", "app", "gw1")
	require.Equal(t, []string{types.NamespacedName{Namespace: "app", Name: "ls1"}.String()}, routeParentListenerSetIndexFunc(hr))

	hr2 := testHTTPRouteAccepted("app", "hr2", "u2", "x.example.com", "app", "gw1")
	require.Equal(t, []string{"app/gw1"}, routeParentGatewayIndexFunc(hr2))

	gr := testGRPCRouteAccepted("app", "gr", "u3", "g.example.com", "app", "gw1")
	require.Equal(t, []string{"app/gw1"}, routeParentGatewayIndexFunc(gr))

	require.Nil(t, routeParentGatewayIndexFunc(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}}))
	require.Nil(t, routeParentListenerSetIndexFunc(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}}))
}

// getGatewayOrListenerSetForTest loads Gateway or ListenerSet for ref (tests).
func getGatewayOrListenerSetForTest(ctx context.Context, c client.Client, routeNS string, ref *gatewayv1.ParentReference) (*gatewayv1.Gateway, *gatewayv1.ListenerSet, error) {
	if ref == nil {
		return nil, nil, nil
	}
	if gwNN, ok := extdnssrc.GatewayNamespacedNameFromParentRef(ref, routeNS); ok {
		gw := &gatewayv1.Gateway{}
		if err := c.Get(ctx, gwNN, gw); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, nil, nil
			}
			return nil, nil, fmt.Errorf("get gateway %s: %w", gwNN.String(), err)
		}
		return gw, nil, nil
	}
	if lsNN, ok := extdnssrc.ListenerSetNamespacedNameFromParentRef(ref, routeNS); ok {
		ls := &gatewayv1.ListenerSet{}
		if err := c.Get(ctx, lsNN, ls); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, nil, nil
			}
			return nil, nil, fmt.Errorf("failed to get listenerset %s: %w", lsNN.String(), err)
		}
		return nil, ls, nil
	}
	return nil, nil, nil
}

// seedRouteDNSIPCacheFromGateway seeds ipCache like Gateway reconcile (ListenerSet→root tests).
func seedRouteDNSIPCacheFromGateway(gr *GatewayReconciler, gw *gatewayv1.Gateway, lss ...*gatewayv1.ListenerSet) {
	if gr.ipCache == nil {
		gr.ipCache = NewGatewayIPCache()
	}
	lsVals := make([]gatewayv1.ListenerSet, 0, len(lss))
	for _, ls := range lss {
		if ls != nil {
			lsVals = append(lsVals, *ls)
		}
	}
	nn := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}
	gr.ipCache.put(nn, gatewayDNSCacheEntry{
		IPs:             extdns.NewTargets("10.0.0.1"),
		AdmissionRows:   extdnssrc.BuildAdmissionHostCacheRows(gw, lsVals),
		GatewayResource: nn,
	})
}

func TestResolveRouteRootGatewayNNs_table(t *testing.T) {
	const ns = testNSApp
	nsObj := testNamespace(ns)
	gw := testGatewayManaged(ns, "gw1", "*.example.com", "10.0.0.1", "")
	ls := testListenerSet(ns, "ls1", ns, "gw1")
	prGW := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("Gateway"),
		Name:  gatewayv1.ObjectName("gw1"),
	}
	prLS := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("ListenerSet"),
		Name:  gatewayv1.ObjectName("ls1"),
	}
	prUnknown := gatewayv1.ParentReference{
		Group: ptrGWGroup("example.com"),
		Kind:  ptrGWKind("UnknownKind"),
		Name:  gatewayv1.ObjectName("x"),
	}

	tests := []struct {
		name          string
		objs          []client.Object
		refs          []gatewayv1.ParentReference
		want          []types.NamespacedName
		seedIPCacheGW bool // populate ipCache with gw+ls so ListenerSet parents resolve to root Gateway
	}{
		{
			name: "direct_gateway",
			objs: []client.Object{nsObj, gw},
			refs: []gatewayv1.ParentReference{prGW},
			want: []types.NamespacedName{{Namespace: ns, Name: "gw1"}},
		},
		{
			name:          "listenerset_chain",
			objs:          []client.Object{nsObj, gw, ls},
			refs:          []gatewayv1.ParentReference{prLS},
			want:          []types.NamespacedName{{Namespace: ns, Name: "gw1"}},
			seedIPCacheGW: true,
		},
		{
			name: "dedup_duplicate_gateway_refs",
			objs: []client.Object{nsObj, gw},
			refs: []gatewayv1.ParentReference{prGW, prGW},
			want: []types.NamespacedName{{Namespace: ns, Name: "gw1"}},
		},
		{
			name:          "gateway_and_listenerset_same_root",
			objs:          []client.Object{nsObj, gw, ls},
			refs:          []gatewayv1.ParentReference{prGW, prLS},
			want:          []types.NamespacedName{{Namespace: ns, Name: "gw1"}},
			seedIPCacheGW: true,
		},
		{
			name: "unknown_parent_skipped",
			objs: []client.Object{nsObj, gw},
			refs: []gatewayv1.ParentReference{prUnknown},
			want: nil,
		},
		{
			name: "listenerset_missing_skipped",
			objs: []client.Object{nsObj, gw},
			refs: []gatewayv1.ParentReference{prLS},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gr := newTestGatewayReconcilerFromObjects(t, gatewayAPIResources{}, tt.objs...)
			if tt.seedIPCacheGW {
				seedRouteDNSIPCacheFromGateway(gr, gw, ls)
			}
			seen := sets.New[string]()
			var got []types.NamespacedName
			for i := range tt.refs {
				gwNN, ok := gr.resolveParentRefToRootGatewayNN(ns, &tt.refs[i])
				if !ok || seen.Has(gwNN.String()) {
					continue
				}
				seen.Insert(gwNN.String())
				got = append(got, gwNN)
			}
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolveParentRefToRootGatewayNN_table(t *testing.T) {
	ctx := context.Background()
	const ns = testNSApp
	nsObj := testNamespace(ns)
	gw := testGatewayManaged(ns, "gw1", "*.example.com", "10.0.0.1", "")
	ls := testListenerSet(ns, "ls1", ns, "gw1")
	prGW := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("Gateway"),
		Name:  gatewayv1.ObjectName("gw1"),
	}
	prLS := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("ListenerSet"),
		Name:  gatewayv1.ObjectName("ls1"),
	}
	prUnknown := gatewayv1.ParentReference{
		Group: ptrGWGroup("example.com"),
		Kind:  ptrGWKind("Foo"),
		Name:  gatewayv1.ObjectName("x"),
	}

	t.Run("resolve_nil_ref", func(t *testing.T) {
		gr := newTestGatewayReconciler(t, fakeClientForGatewayTests(nsObj, gw), gatewayAPIResources{})
		got, ok := gr.resolveParentRefToRootGatewayNN(ns, nil)
		require.False(t, ok)
		require.Equal(t, types.NamespacedName{}, got)
	})

	resolveCases := []struct {
		name          string
		objs          []client.Object
		ref           gatewayv1.ParentReference
		wantNN        types.NamespacedName
		wantOK        bool
		seedIPCacheGW bool
	}{
		{
			name:   "gateway_direct",
			objs:   []client.Object{nsObj, gw},
			ref:    prGW,
			wantNN: types.NamespacedName{Namespace: ns, Name: "gw1"},
			wantOK: true,
		},
		{
			name:          "listenerset_resolves_root_gateway",
			objs:          []client.Object{nsObj, gw, ls},
			ref:           prLS,
			wantNN:        types.NamespacedName{Namespace: ns, Name: "gw1"},
			wantOK:        true,
			seedIPCacheGW: true,
		},
		{
			name:   "listenerset_not_found",
			objs:   []client.Object{nsObj, gw},
			ref:    prLS,
			wantNN: types.NamespacedName{},
			wantOK: false,
		},
		{
			name:   "unknown_kind",
			objs:   []client.Object{nsObj, gw},
			ref:    prUnknown,
			wantNN: types.NamespacedName{},
			wantOK: false,
		},
	}
	for _, tt := range resolveCases {
		t.Run("resolve_"+tt.name, func(t *testing.T) {
			gr := newTestGatewayReconciler(t, fakeClientForGatewayTests(tt.objs...), gatewayAPIResources{})
			if tt.seedIPCacheGW {
				seedRouteDNSIPCacheFromGateway(gr, gw, ls)
			}
			ref := tt.ref
			got, ok := gr.resolveParentRefToRootGatewayNN(ns, &ref)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantNN, got)
		})
	}

	loadCases := []struct {
		name   string
		objs   []client.Object
		ref    *gatewayv1.ParentReference
		wantGW bool
		wantLS bool
	}{
		{name: "nil_ref", objs: []client.Object{nsObj}, ref: nil},
		{name: "gateway_found", objs: []client.Object{nsObj, gw}, ref: &prGW, wantGW: true},
		{name: "listenerset_found", objs: []client.Object{nsObj, gw, ls}, ref: &prLS, wantLS: true},
		{name: "gateway_missing", objs: []client.Object{nsObj}, ref: &prGW},
	}
	for _, tt := range loadCases {
		t.Run("load_"+tt.name, func(t *testing.T) {
			gr := newTestGatewayReconciler(t, fakeClientForGatewayTests(tt.objs...), gatewayAPIResources{})
			gotGW, gotLS, err := getGatewayOrListenerSetForTest(ctx, gr.Client, ns, tt.ref)
			require.NoError(t, err)
			if tt.wantGW {
				require.NotNil(t, gotGW)
				require.Nil(t, gotLS)
				return
			}
			if tt.wantLS {
				require.Nil(t, gotGW)
				require.NotNil(t, gotLS)
				return
			}
			require.Nil(t, gotGW)
			require.Nil(t, gotLS)
		})
	}

	t.Run("load_gateway_get_internal_error", func(t *testing.T) {
		fc := fakeClientForGatewayTests(nsObj, gw)
		ec := &errGatewayGetClient{
			Client: fc,
			target: types.NamespacedName{Namespace: ns, Name: "gw1"},
			err:    fmt.Errorf("apiserver unavailable"),
		}
		gr := newTestGatewayReconciler(t, ec, gatewayAPIResources{})
		_, _, err := getGatewayOrListenerSetForTest(ctx, gr.Client, ns, &prGW)
		require.Error(t, err)
		require.Contains(t, err.Error(), "apiserver unavailable")
	})

	t.Run("load_listenerset_get_internal_error", func(t *testing.T) {
		fc := fakeClientForGatewayTests(nsObj, gw, ls)
		ec := &errListenerSetGetClient{
			Client: fc,
			target: types.NamespacedName{Namespace: ns, Name: "ls1"},
			err:    fmt.Errorf("etcd timeout"),
		}
		gr := newTestGatewayReconciler(t, ec, gatewayAPIResources{})
		_, _, err := getGatewayOrListenerSetForTest(ctx, gr.Client, ns, &prLS)
		require.Error(t, err)
		require.Contains(t, err.Error(), "etcd timeout")
	})
}

func acceptedRouteParentStatus(ref gatewayv1.ParentReference) []gatewayv1.RouteParentStatus {
	return []gatewayv1.RouteParentStatus{{
		ParentRef: ref,
		Conditions: []metav1.Condition{{
			Type:   string(gatewayv1.RouteConditionAccepted),
			Status: metav1.ConditionTrue,
		}},
	}}
}

func TestDistinctAcceptedRootGatewayNNs_table(t *testing.T) {
	const ns = testNSApp
	nsObj := testNamespace(ns)
	gw := testGatewayManaged(ns, "gw1", "*.example.com", "10.0.0.1", "")
	ls := testListenerSet(ns, "ls1", ns, "gw1")
	prGW := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("Gateway"),
		Name:  gatewayv1.ObjectName("gw1"),
	}
	prLS := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("ListenerSet"),
		Name:  gatewayv1.ObjectName("ls1"),
	}
	prGW2 := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("Gateway"),
		Name:  gatewayv1.ObjectName("gw2"),
	}
	gw2 := testGatewayManaged(ns, "gw2", "*.example.com", "10.0.0.2", "")

	tests := []struct {
		name          string
		objs          []client.Object
		refs          []gatewayv1.ParentReference
		status        []gatewayv1.RouteParentStatus
		want          []types.NamespacedName
		seedIPCacheGW bool
	}{
		{
			name:   "accepted_gateway_parent",
			objs:   []client.Object{nsObj, gw},
			refs:   []gatewayv1.ParentReference{prGW},
			status: acceptedRouteParentStatus(prGW),
			want:   []types.NamespacedName{{Namespace: ns, Name: "gw1"}},
		},
		{
			name:          "accepted_listenerset_chain",
			objs:          []client.Object{nsObj, gw, ls},
			refs:          []gatewayv1.ParentReference{prLS},
			status:        acceptedRouteParentStatus(prLS),
			want:          []types.NamespacedName{{Namespace: ns, Name: "gw1"}},
			seedIPCacheGW: true,
		},
		{
			name:   "not_accepted_skipped",
			objs:   []client.Object{nsObj, gw},
			refs:   []gatewayv1.ParentReference{prGW},
			status: nil,
			want:   nil,
		},
		{
			name:   "dedup_two_gateway_parents_same_root",
			objs:   []client.Object{nsObj, gw},
			refs:   []gatewayv1.ParentReference{prGW, prGW},
			status: append(acceptedRouteParentStatus(prGW), acceptedRouteParentStatus(prGW)...),
			want:   []types.NamespacedName{{Namespace: ns, Name: "gw1"}},
		},
		{
			name: "two_distinct_accepted_gateways",
			objs: []client.Object{nsObj, gw, gw2},
			refs: []gatewayv1.ParentReference{prGW, prGW2},
			status: func() []gatewayv1.RouteParentStatus {
				s := acceptedRouteParentStatus(prGW)
				s = append(s, acceptedRouteParentStatus(prGW2)...)
				return s
			}(),
			want: []types.NamespacedName{
				{Namespace: ns, Name: "gw1"},
				{Namespace: ns, Name: "gw2"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gr := newTestGatewayReconciler(t, fakeClientForGatewayTests(tt.objs...), gatewayAPIResources{})
			if tt.seedIPCacheGW {
				seedRouteDNSIPCacheFromGateway(gr, gw, ls)
			}
			got := gr.distinctAcceptedRootGatewayNNs(ns, tt.refs, tt.status)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGcListExistingOwners_clientListErrors_table(t *testing.T) {
	ctx := context.Background()
	nsObj := testNamespace(testNSApp)
	fc := fakeClientForGatewayTests(nsObj)
	flags := apiResourcesForTests(true, true, true, false)
	tests := []struct {
		name       string
		client     client.Client
		wantSubstr string
	}{
		{
			name:       "listener_set_list_fails",
			client:     &listErrorListenerSetClient{Client: fc, listErr: fmt.Errorf("listener set list failed")},
			wantSubstr: "listener set list failed",
		},
		{
			name:       "http_route_list_fails",
			client:     &listErrHTTPRouteClient{Client: fc, err: fmt.Errorf("httproute list failed")},
			wantSubstr: "httproute list failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gcListExistingOwners(ctx, tt.client, flags)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantSubstr)
		})
	}
}

type listErrHTTPRouteClient struct { // List *HTTPRouteList fails
	client.Client
	err error
}

func (c *listErrHTTPRouteClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	switch list.(type) {
	case *gatewayv1.HTTPRouteList:
		return c.err
	default:
		return c.Client.List(ctx, list, opts...)
	}
}

func TestGetNamespaceType_table(t *testing.T) {
	ctx := context.Background()
	nsObj := testNamespace("app")
	tests := []struct {
		name    string
		objs    []client.Object
		nsName  string
		wantErr bool
	}{
		{name: "namespace_exists", objs: []client.Object{nsObj}, nsName: "app", wantErr: false},
		{name: "namespace_missing", objs: nil, nsName: "missing", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := fakeClientForGatewayTests(tt.objs...)
			gr := newTestGatewayReconciler(t, fc, gatewayAPIResources{})
			_, err := gr.getNamespaceType(ctx, tt.nsName)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestListSortedListenerSetsForGateway_listError(t *testing.T) {
	ctx := context.Background()
	nsObj := testNamespace("app")
	gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
	fc := fakeClientForGatewayTests(nsObj, gw)
	ec := &listErrorListenerSetClient{Client: fc, listErr: fmt.Errorf("list listener sets failed")}
	gr := newTestGatewayReconciler(t, ec, gatewayAPIResources{})
	_, err := gr.listSortedListenerSetsForGateway(ctx, types.NamespacedName{Namespace: "app", Name: "gw1"})
	require.Error(t, err)
}

func TestResyncRouteDNS_table(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "by_gateway_parent_enqueues_http_route",
			run: func(t *testing.T) {
				nsObj := testNamespace("app")
				gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
				hr := testHTTPRouteAccepted("app", "hr1", "u1", "x.example.com", "app", "gw1")
				fc := fakeClientForGatewayTests(nsObj, gw, hr)
				ch := make(chan event.TypedGenericEvent[*gatewayv1.HTTPRoute], 10)
				resyncRouteDNS[gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](ctx, fc, types.NamespacedName{Namespace: "app", Name: "gw1"}, true, ch, "HTTPRoute", &gatewayv1.HTTPRouteList{})
				require.Len(t, ch, 1)
			},
		},
		{
			name: "by_listener_set_index_enqueues_http_route",
			run: func(t *testing.T) {
				nsObj := testNamespace("app")
				gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
				ls := testListenerSet("app", "ls1", "app", "gw1")
				hr := testHTTPRouteViaListenerSet("app", "hr1", "u1", "hello.example.com", "app", ls.Name, "app", "gw1")
				fc := fakeClientForGatewayTests(nsObj, gw, ls, hr)
				ch := make(chan event.TypedGenericEvent[*gatewayv1.HTTPRoute], 10)
				idx := types.NamespacedName{Namespace: ls.Namespace, Name: ls.Name}.String()
				resyncRouteDNSByListenerSetIndex[gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](ctx, fc, idx, true, ch, "HTTPRoute", &gatewayv1.HTTPRouteList{})
				require.Len(t, ch, 1)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t)
		})
	}
}

func TestGatewayReconcileMiscHooks_table(t *testing.T) {
	t.Run("RestoreReconcile_no_error", func(t *testing.T) {
		gr, _ := fakeGatewayReconciler(t)
		require.NotNil(t, gr.listenerSetEnqueueHandler())
		require.NoError(t, gr.RestoreReconcile())
	})
	t.Run("enqueueAllRoutesForDNSResyncOnStartup_nil_channels_no_panic", func(t *testing.T) {
		ctx := context.Background()
		gr, _ := fakeGatewayReconciler(t)
		nn := types.NamespacedName{Namespace: "app", Name: "gw1"}
		gr.ipCache.put(nn, gatewayDNSCacheEntry{
			IPs:             extdns.NewTargets("10.0.0.1"),
			AdmissionRows:   []extdnssrc.AdmissionHostCacheRow{{Filter: "*.x"}},
			GatewayResource: nn,
		})
		gr.enqueueAllRoutesForDNSResyncOnStartup(ctx)
	})
}

func TestUpdateGatewayDNSReadyCondition_and_removeGatewayDNSConfigCondition(t *testing.T) {
	ctx := context.Background()
	nsObj := testNamespace("app")
	gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
	gw.Status.Conditions = []metav1.Condition{{
		Type: conditionTypeDNSConfig, Status: metav1.ConditionTrue, Reason: reasonDNSRecordConfigured,
	}}
	fc := fakeClientForGatewayTests(nsObj, gw)
	gr := newTestGatewayReconciler(t, fc, gatewayAPIResources{})

	ownerKey := types.NamespacedName{Namespace: "app", Name: "gw1"}
	require.NoError(t, gr.updateGatewayDNSReadyCondition(ctx, ownerKey, nil))
	require.NoError(t, gr.removeGatewayDNSConfigCondition(ctx, ownerKey))
}

func TestCollectIPsFromGateway_table(t *testing.T) {
	gwOK := testGatewayManaged("app", "gw1", "*.x", "10.0.0.1", "2001:db8::1")
	gwInvalid := testGatewayManaged("app", "gw1", "*.x", "10.0.0.1", "")
	gwInvalid.Status.Addresses = []gatewayv1.GatewayStatusAddress{{Type: addrTypePtr(), Value: "not-a-valid-ip"}}
	gwHostname := testGatewayManaged("app", "gw1", "*.x", "10.0.0.1", "")
	ht := gatewayv1.HostnameAddressType
	gwHostname.Status.Addresses = []gatewayv1.GatewayStatusAddress{{Type: &ht, Value: "gw.example.com"}}

	tests := []struct {
		name string
		gw   *gatewayv1.Gateway
		want []string // sorted for stable compare
	}{
		{
			name: "valid_v4_v6",
			gw:   gwOK,
			want: []string{"10.0.0.1", "2001:db8::1"},
		},
		{name: "invalid_ip_string", gw: gwInvalid, want: nil},
		{name: "non_ip_address_type", gw: gwHostname, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectIPsFromGateway(tt.gw)
			var gotStr []string
			for _, ip := range got {
				gotStr = append(gotStr, ip.String())
			}
			slices.Sort(gotStr)
			if tt.want == nil {
				require.Empty(t, gotStr)
				return
			}
			want := slices.Clone(tt.want)
			slices.Sort(want)
			require.Equal(t, want, gotStr)
		})
	}
}

func TestIpsToTargets_skipsNilElements(t *testing.T) {
	require.Empty(t, ipsToTargets([]net.IP{nil}))
}

func TestFilterUsableGatewayCRs_mutatorError(t *testing.T) {
	gw1 := testGatewayManaged("app", "a", "*.x", "10.0.0.1", "")
	gw2 := testGatewayManaged("app", "b", "*.x", "10.0.0.2", "")
	list := &gatewayv1.GatewayList{Items: []gatewayv1.Gateway{*gw1, *gw2}}
	err := filterUsableGatewayCRs(list, func(gw *gatewayv1.Gateway) error {
		if gw.Name == "b" {
			return fmt.Errorf("mutator stop")
		}
		return nil
	})
	require.Error(t, err)
}

func TestWarmGatewayIPCacheOnStartup_table(t *testing.T) {
	ctx := context.Background()
	nsObj := testNamespace("app")
	gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
	ls := testListenerSet("app", "ls1", "app", "gw1")
	ar := gatewayAPIResources{gateway: true, listenerSet: false, httpRoute: true, grpcRoute: false, tlsRoute: false}
	arLS := gatewayAPIResources{gateway: true, listenerSet: true, httpRoute: true, grpcRoute: false, tlsRoute: false}

	for _, tt := range []struct {
		name       string
		newGr      func(t *testing.T) *GatewayReconciler
		wantErr    bool
		wantWarmed bool
		check      func(t *testing.T, gr *GatewayReconciler)
	}{
		{
			name: "populates_cache",
			newGr: func(t *testing.T) *GatewayReconciler {
				gr := newTestGatewayReconcilerFromObjects(t, ar, nsObj, gw)
				gr.ipCacheWarmedOnStartup.Store(false)
				return gr
			},
			wantErr:    false,
			wantWarmed: true,
			check: func(t *testing.T, gr *GatewayReconciler) {
				_, ok := gr.ipCache.get(types.NamespacedName{Namespace: "app", Name: "gw1"})
				require.True(t, ok)
			},
		},
		{
			name: "no_op_when_gateway_api_absent",
			newGr: func(t *testing.T) *GatewayReconciler {
				gr, _ := fakeGatewayReconciler(t)
				gr.apiResources.gateway = false
				return gr
			},
			wantErr:    false,
			wantWarmed: true,
		},
		{
			name: "gateway_list_error",
			newGr: func(t *testing.T) *GatewayReconciler {
				fc := fakeClientForGatewayTests(nsObj, gw)
				ec := &listErrorGatewayListClient{Client: fc, listErr: fmt.Errorf("cannot list gateways")}
				gr := newTestGatewayReconciler(t, ec, ar)
				gr.ipCacheWarmedOnStartup.Store(false)
				return gr
			},
			wantErr:    true,
			wantWarmed: false,
		},
		{
			name: "with_listener_set_admission_rows",
			newGr: func(t *testing.T) *GatewayReconciler {
				gr := newTestGatewayReconcilerFromObjects(t, arLS, nsObj, gw, ls)
				gr.ipCacheWarmedOnStartup.Store(false)
				return gr
			},
			wantErr:    false,
			wantWarmed: true,
			check: func(t *testing.T, gr *GatewayReconciler) {
				entry, ok := gr.ipCache.get(types.NamespacedName{Namespace: "app", Name: "gw1"})
				require.True(t, ok)
				require.NotEmpty(t, entry.AdmissionRows)
			},
		},
		{
			name: "listener_set_list_error",
			newGr: func(t *testing.T) *GatewayReconciler {
				fc := fakeClientForGatewayTests(nsObj, gw, ls)
				ec := &listErrorListenerSetClient{Client: fc, listErr: fmt.Errorf("cannot list listenersets")}
				gr := newTestGatewayReconciler(t, ec, arLS)
				gr.ipCacheWarmedOnStartup.Store(false)
				return gr
			},
			wantErr:    true,
			wantWarmed: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gr := tt.newGr(t)
			err := gr.warmGatewayIPCacheOnStartup(ctx)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantWarmed, gr.ipCacheWarmedOnStartup.Load())
			if tt.check != nil {
				tt.check(t, gr)
			}
		})
	}
}

func TestRegisterGatewayDNSFieldIndexes_table(t *testing.T) {
	tests := []struct {
		name       string
		api        gatewayAPIResources
		indexerErr error
		wantErr    bool
	}{
		{name: "success", api: apiResourcesForTests(true, true, true, false)},
		{
			name:       "listener_set_indexer_error",
			api:        apiResourcesForTests(true, false, false, false),
			indexerErr: fmt.Errorf("index registration failed"),
			wantErr:    true,
		},
		{
			name:       "route_indexer_error",
			api:        apiResourcesForTests(false, true, false, false),
			indexerErr: fmt.Errorf("route index failed"),
			wantErr:    true,
		},
		{name: "listener_set_disabled_ok", api: apiResourcesForTests(false, true, false, false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gctl := gomock.NewController(t)
			t.Cleanup(func() { gctl.Finish() })
			fc := fakeClientForGatewayTests()
			fi := mockclient.NewMockFieldIndexer(gctl)
			if tt.indexerErr != nil {
				fi.EXPECT().IndexField(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(tt.indexerErr).AnyTimes()
			} else {
				fi.EXPECT().IndexField(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			}
			c := mockcache.NewDelegating(fc)
			mgr := newGatewayGomockManager(t, gctl, fc, scheme, c, fi)
			gr := NewGatewayReconciler(mgr, dnsTestService())
			gr.apiResources = tt.api
			err := gr.registerGatewayDNSFieldIndexes(mgr)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRegisterRouteDNSControllers_table(t *testing.T) {
	svc := dnsTestService()
	tests := []struct {
		name           string
		ar             gatewayAPIResources
		wantHTTPResync bool
		wantGRPCResync bool
	}{
		{
			name: "no_route_kinds_allocates_no_resync_channels",
			ar: gatewayAPIResources{
				gateway: true, listenerSet: false, httpRoute: false, grpcRoute: false, tlsRoute: false,
			},
		},
		{
			name:           "http_only",
			ar:             apiResourcesForTests(false, true, false, false),
			wantHTTPResync: true,
		},
		{
			name:           "grpc_only",
			ar:             apiResourcesForTests(false, false, true, false),
			wantGRPCResync: true,
		},
		{
			name:           "http_and_grpc",
			ar:             apiResourcesForTests(false, true, true, false),
			wantHTTPResync: true, wantGRPCResync: true,
		},
		{
			name:           "http_with_listener_set_watch_still_registers",
			ar:             apiResourcesForTests(true, true, false, false),
			wantHTTPResync: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr, _ := createFakeManagerAndClient(t)
			gr := NewGatewayReconciler(mgr, svc)
			gr.apiResources = tc.ar
			require.NoError(t, gr.registerRouteDNSControllers(mgr))
			if tc.wantHTTPResync {
				require.NotNil(t, gr.httpRouteDNSResyncCh)
			} else {
				require.Nil(t, gr.httpRouteDNSResyncCh)
			}
			if tc.wantGRPCResync {
				require.NotNil(t, gr.grpcRouteDNSResyncCh)
			} else {
				require.Nil(t, gr.grpcRouteDNSResyncCh)
			}
			require.Nil(t, gr.tlsRouteDNSResyncCh)
		})
	}
}

func TestCheckGatewayCRDs_table(t *testing.T) {
	mgr, _ := createFakeManagerAndClient(t)
	tests := []struct {
		name        string
		discovery   *fakeDiscoveryClient
		preAPI      gatewayAPIResources
		wantErr     bool
		assertFlags func(t *testing.T, ar gatewayAPIResources)
	}{
		{
			name: "sets_all_flags_from_resource_list",
			discovery: &fakeDiscoveryClient{
				resources: &metav1.APIResourceList{
					GroupVersion: gatewayAPIGroupVersion,
					APIResources: []metav1.APIResource{
						{Name: "gateways"},
						{Name: "listenersets"},
						{Name: "httproutes"},
						{Name: "grpcroutes"},
					},
				},
			},
			assertFlags: func(t *testing.T, ar gatewayAPIResources) {
				require.True(t, ar.gateway)
				require.True(t, ar.listenerSet)
				require.True(t, ar.httpRoute)
				require.True(t, ar.grpcRoute)
				require.False(t, ar.tlsRoute)
			},
		},
		{
			name:      "not_found_returns_nil",
			discovery: &fakeDiscoveryClient{err: apierrors.NewNotFound(schema.GroupResource{Group: "gateway.networking.k8s.io"}, "x")},
		},
		{
			name:      "non_not_found_error",
			discovery: &fakeDiscoveryClient{err: fmt.Errorf("discovery unavailable")},
			wantErr:   true,
		},
		{
			name:      "nil_resource_list_zeros_flags",
			discovery: &fakeDiscoveryClient{resources: nil},
			preAPI:    gatewayAPIResources{gateway: true, httpRoute: true},
			assertFlags: func(t *testing.T, ar gatewayAPIResources) {
				require.False(t, ar.gateway)
				require.False(t, ar.httpRoute)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gr := NewGatewayReconciler(mgr, dnsTestService())
			gr.apiResources = tt.preAPI
			gr.discoveryClient = tt.discovery
			err := gr.checkGatewayCRDs(mgr)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.assertFlags != nil {
				tt.assertFlags(t, gr.apiResources)
			}
		})
	}
}

func TestStartController_skipsSetupWhenGatewayCRDNotPresent(t *testing.T) {
	old := gatewaySetupWithManager
	gatewaySetupWithManager = func(*GatewayReconciler, ctrl.Manager) error {
		require.FailNow(t, "gatewaySetupWithManager should not run when Gateway CRD is absent")
		return nil
	}
	defer func() { gatewaySetupWithManager = old }()

	mgr, _ := createFakeManagerAndClient(t)
	gr := NewGatewayReconciler(mgr, dnsTestService())
	gr.discoveryClient = &fakeDiscoveryClient{
		resources: &metav1.APIResourceList{
			GroupVersion: gatewayAPIGroupVersion,
			APIResources: []metav1.APIResource{{Name: "httproutes"}},
		},
	}
	require.NoError(t, gr.StartController(mgr, nil))
	require.False(t, gr.apiResources.gateway)
}

func TestStartController_registersWarmRunnableWithStubSetup(t *testing.T) {
	old := gatewaySetupWithManager
	gatewaySetupWithManager = func(*GatewayReconciler, ctrl.Manager) error { return nil }
	defer func() { gatewaySetupWithManager = old }()

	nsObj := testNamespace("app")
	gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
	mgr, _ := createFakeManagerAndClient(t, nsObj, gw)
	gr := NewGatewayReconciler(mgr, dnsTestService())
	gr.discoveryClient = &fakeDiscoveryClient{
		resources: &metav1.APIResourceList{
			GroupVersion: gatewayAPIGroupVersion,
			APIResources: []metav1.APIResource{{Name: "gateways"}},
		},
	}
	require.NoError(t, gr.StartController(mgr, nil))
	require.True(t, gr.apiResources.gateway)
}

func TestStartController_propagatesSetupError(t *testing.T) {
	old := gatewaySetupWithManager
	gatewaySetupWithManager = func(*GatewayReconciler, ctrl.Manager) error { return fmt.Errorf("setup failed") }
	defer func() { gatewaySetupWithManager = old }()

	mgr, _ := createFakeManagerAndClient(t)
	gr := NewGatewayReconciler(mgr, dnsTestService())
	gr.discoveryClient = &fakeDiscoveryClient{
		resources: &metav1.APIResourceList{
			GroupVersion: gatewayAPIGroupVersion,
			APIResources: []metav1.APIResource{{Name: "gateways"}},
		},
	}
	require.Error(t, gr.StartController(mgr, nil))
}

func TestRouteDNSRequestsForListenerSet(t *testing.T) {
	ctx := context.Background()
	nsObj := testNamespace("app")
	gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
	ls := testListenerSet("app", "ls1", "app", "gw1")
	hr := testHTTPRouteViaListenerSet("app", "h1", "u1", "h.example.com", "app", "ls1", "app", "gw1")
	grpcR := grpcRouteAttachedToListenerSet("app", "g1", "u2", "g.example.com", "app", "ls1")
	fc := fakeClientForGatewayTests(nsObj, gw, ls, hr, grpcR)
	gr := newTestGatewayReconciler(t, fc, apiResourcesForTests(true, true, true, false))

	t.Run("HTTPRoute", func(t *testing.T) {
		reqs := gr.routeDNSRequestsForListenerSet(dns.ResourceKindHTTPRoute)(ctx, ls)
		require.Len(t, reqs, 1)
		require.Equal(t, types.NamespacedName{Namespace: "app", Name: "h1"}, reqs[0].NamespacedName)
	})
	t.Run("GRPCRoute", func(t *testing.T) {
		reqs := gr.routeDNSRequestsForListenerSet(dns.ResourceKindGRPCRoute)(ctx, ls)
		require.Len(t, reqs, 1)
		require.Equal(t, types.NamespacedName{Namespace: "app", Name: "g1"}, reqs[0].NamespacedName)
	})
	t.Run("wrong object type", func(t *testing.T) {
		reqs := gr.routeDNSRequestsForListenerSet(dns.ResourceKindHTTPRoute)(ctx, nsObj)
		require.Nil(t, reqs)
	})
	t.Run("unknown route kind", func(t *testing.T) {
		reqs := gr.routeDNSRequestsForListenerSet("UnknownKind")(ctx, ls)
		require.Nil(t, reqs)
	})
	t.Run("HTTPRoute kind disabled", func(t *testing.T) {
		gr.apiResources.httpRoute = false
		t.Cleanup(func() { gr.apiResources.httpRoute = true })
		reqs := gr.routeDNSRequestsForListenerSet(dns.ResourceKindHTTPRoute)(ctx, ls)
		require.Nil(t, reqs)
	})
}

func TestConvertToRouteObject_branches(t *testing.T) {
	gr, _ := fakeGatewayReconciler(t)
	a := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute](gr, dns.ResourceKindHTTPRoute, func() *HTTPRoute { return &HTTPRoute{} })

	_, ok := a.convertToRouteObject(nil)
	require.False(t, ok)
	_, ok = a.convertToRouteObject(&corev1.Pod{})
	require.False(t, ok)

	hr := testHTTPRouteAccepted("app", "x", "ux", "x.example.com", "app", "gw1")
	got, ok := a.convertToRouteObject(hr)
	require.True(t, ok)
	require.Equal(t, hr.Name, got.GetBaseObject().GetName())
}

type errGatewayGetClient struct { // Get Gateway at target fails
	client.Client
	target types.NamespacedName
	err    error
}

func (c *errGatewayGetClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*gatewayv1.Gateway); ok {
		if key == c.target {
			return c.err
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

type errListenerSetGetClient struct { // Get ListenerSet at target fails
	client.Client
	target types.NamespacedName
	err    error
}

func (c *errListenerSetGetClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*gatewayv1.ListenerSet); ok {
		if key == c.target {
			return c.err
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

type listErrorListenerSetClient struct { // List *ListenerSetList fails
	client.Client
	listErr error
}

func (c *listErrorListenerSetClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	switch list.(type) {
	case *gatewayv1.ListenerSetList:
		return c.listErr
	default:
		return c.Client.List(ctx, list, opts...)
	}
}

type listErrorGatewayListClient struct { // List *GatewayList fails
	client.Client
	listErr error
}

func (c *listErrorGatewayListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	switch list.(type) {
	case *gatewayv1.GatewayList:
		return c.listErr
	default:
		return c.Client.List(ctx, list, opts...)
	}
}

func TestCollectGarbage_table(t *testing.T) {
	ctx := context.Background()
	nsObj := testNamespace(testNSApp)
	fc := fakeClientForGatewayTests(nsObj)
	ec := &listErrorGatewayListClient{Client: fc, listErr: fmt.Errorf("cannot list gateways for GC")}

	tests := []struct {
		name       string
		useFakeGR  bool
		client     client.Client
		gatewayAPI bool
		wantErr    bool
	}{
		{name: "skips_when_gateway_api_not_installed", useFakeGR: true},
		{name: "gateway_list_error", client: ec, gatewayAPI: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.useFakeGR {
				gr, _ := fakeGatewayReconciler(t)
				gr.apiResources.gateway = false
				require.NoError(t, gr.CollectGarbage(ctx))
				return
			}
			ar := gatewayAPIResources{}
			ar.gateway = tt.gatewayAPI
			gr := newTestGatewayReconciler(t, tt.client, ar)
			err := gr.CollectGarbage(ctx)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestEnqueueAttachedRoutesForGatewayDNSFromAPI_enqueuesRoutes(t *testing.T) {
	ctx := context.Background()
	nsObj := testNamespace("app")
	gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
	ls := testListenerSet("app", "ls1", "app", "gw1")
	hr := testHTTPRouteAccepted("app", "hr1", "u1", "a.example.com", "app", "gw1")
	hrLS := testHTTPRouteViaListenerSet("app", "hr2", "u2", "b.example.com", "app", ls.Name, "app", "gw1")
	grpcR := testGRPCRouteAccepted("app", "gr1", "ug", "c.example.com", "app", "gw1")
	fc := fakeClientForGatewayTests(nsObj, gw, ls, hr, hrLS, grpcR)
	gr := newTestGatewayReconciler(t, fc, apiResourcesForTests(true, true, true, false))
	gr.httpRouteDNSResyncCh = make(chan event.TypedGenericEvent[*gatewayv1.HTTPRoute], 256)
	gr.grpcRouteDNSResyncCh = make(chan event.TypedGenericEvent[*gatewayv1.GRPCRoute], 256)

	gr.enqueueAttachedRoutesForGatewayDNSFromAPI(ctx, types.NamespacedName{Namespace: "app", Name: "gw1"})

	drain := func(n int) int {
		c := 0
		for i := 0; i < n; i++ {
			select {
			case <-gr.httpRouteDNSResyncCh:
				c++
			default:
				return c
			}
		}
		return c
	}
	require.GreaterOrEqual(t, drain(32), 2)

	drainGRPC := func(n int) int {
		c := 0
		for i := 0; i < n; i++ {
			select {
			case <-gr.grpcRouteDNSResyncCh:
				c++
			default:
				return c
			}
		}
		return c
	}
	require.GreaterOrEqual(t, drainGRPC(32), 1)
}
func TestBuildDNSConfigCondition_table(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  metav1.ConditionStatus
		wantReason  string
		msgContains string
	}{
		{
			name:       "success",
			err:        nil,
			wantStatus: metav1.ConditionTrue,
			wantReason: reasonDNSRecordConfigured,
		},
		{
			name:        "failure",
			err:         errors.New("boom"),
			wantStatus:  metav1.ConditionFalse,
			wantReason:  reasonDNSRecordFailed,
			msgContains: "boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := buildDNSConfigCondition(tt.err)
			require.Equal(t, conditionTypeDNSConfig, c.Type)
			require.Equal(t, tt.wantStatus, c.Status)
			require.Equal(t, tt.wantReason, c.Reason)
			if tt.msgContains != "" {
				require.Contains(t, c.Message, tt.msgContains)
			}
		})
	}
}

func TestMergeRouteParentDNSCondition_table(t *testing.T) {
	ctx := context.Background()
	ns := testNSApp
	nsObj := testNamespace(ns)
	gw := testGatewayManaged(ns, testGW1Name, "*.example.com", "10.0.0.1", "")
	ls := testListenerSet(ns, "ls1", ns, testGW1Name)
	prGW := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("Gateway"),
		Name:  gatewayv1.ObjectName(testGW1Name),
	}
	prLS := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("ListenerSet"),
		Name:  gatewayv1.ObjectName("ls1"),
	}

	tests := []struct {
		name        string
		objs        []client.Object
		parents     []gatewayv1.RouteParentStatus
		seedCacheGW bool
		observedGen int64
		wantDNS     bool
		wantGen     int64
	}{
		{
			name: "sets_when_managed_gateway_parent",
			objs: []client.Object{nsObj, gw},
			parents: []gatewayv1.RouteParentStatus{{
				ParentRef: prGW,
				Conditions: []metav1.Condition{{
					Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue,
				}},
			}},
			observedGen: 3,
			wantDNS:     true,
			wantGen:     3,
		},
		{
			name: "sets_when_managed_listener_set_parent",
			objs: []client.Object{nsObj, gw, ls},
			parents: []gatewayv1.RouteParentStatus{{
				ParentRef: prLS,
				Conditions: []metav1.Condition{{
					Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue,
				}},
			}},
			seedCacheGW: true,
			observedGen: 2,
			wantDNS:     true,
			wantGen:     2,
		},
		{
			name: "strips_when_unmanaged_gateway_parent",
			objs: []client.Object{nsObj, gw},
			parents: []gatewayv1.RouteParentStatus{{
				ParentRef: prGW,
				Conditions: []metav1.Condition{
					{Type: conditionTypeDNSConfig, Status: metav1.ConditionTrue, Reason: reasonDNSRecordConfigured},
					{Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue},
				},
			}},
			observedGen: 1,
			wantDNS:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gr := newTestGatewayReconcilerFromObjects(t, apiResourcesForTests(true, true, false, false), tt.objs...)
			gr.ipCache = NewGatewayIPCache()
			if tt.seedCacheGW {
				seedRouteDNSIPCacheFromGateway(gr, gw, ls)
			} else if tt.wantDNS {
				gr.ipCache.put(testNNGW1, gatewayDNSCacheEntry{
					IPs:             extdns.NewTargets("10.0.0.1"),
					AdmissionRows:   []extdnssrc.AdmissionHostCacheRow{{Filter: "*.example.com"}},
					GatewayResource: testNNGW1,
				})
			}
			parents := slices.Clone(tt.parents)
			ad := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute](gr, dns.ResourceKindHTTPRoute, func() *HTTPRoute { return &HTTPRoute{} })
			changed := ad.mergeRouteParentDNSCondition(ctx, buildDNSConfigCondition(nil), ns, tt.observedGen, parents)
			require.True(t, changed)
			found := meta.FindStatusCondition(parents[0].Conditions, conditionTypeDNSConfig)
			if !tt.wantDNS {
				require.Nil(t, found)
				return
			}
			require.NotNil(t, found)
			require.Equal(t, metav1.ConditionTrue, found.Status)
			require.Equal(t, tt.wantGen, found.ObservedGeneration)
		})
	}
}

// reconcileRouteDNSCase selects the branch exercised by TestReconcileRouteDNS_table.
type reconcileRouteDNSCase int

const (
	reconcileRouteDNSNotFound reconcileRouteDNSCase = iota
	reconcileRouteDNSIPCacheCold
	reconcileRouteDNSBuildEndpointsErr
	reconcileRouteDNSSuccess
	reconcileRouteDNSStatusUpdateFail
)

func runReconcileRouteDNSCase(ctx context.Context, t *testing.T, br reconcileRouteDNSCase) {
	t.Helper()
	ar := apiResourcesForTests(true, true, false, false)
	httpAd := func(gr *GatewayReconciler) *routeReconcilerAdapter[*HTTPRoute, gatewayv1.HTTPRoute] {
		return newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute](gr, dns.ResourceKindHTTPRoute, func() *HTTPRoute { return &HTTPRoute{} })
	}

	switch br {
	case reconcileRouteDNSNotFound:
		nsObj := testNamespace("app")
		gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
		fc := fakeClientForGatewayTests(nsObj, gw)
		gr := newTestGatewayReconciler(t, fc, ar)
		gr.ipCache = NewGatewayIPCache()
		gr.ipCacheWarmedOnStartup.Store(true)
		refreshIPCache(ctx, t, gr, gw)
		res, err := httpAd(gr).Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: "missing-route"}})
		require.NoError(t, err)
		require.Equal(t, common.ResultNormal, res)

	case reconcileRouteDNSIPCacheCold:
		nsObj := testNamespace("app")
		gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
		hr := testHTTPRouteAccepted("app", "hr1", "u1", "x.example.com", "app", "gw1")
		fc := fakeClientForGatewayTests(nsObj, gw, hr)
		gr := newTestGatewayReconciler(t, fc, ar)
		gr.ipCache = NewGatewayIPCache()
		gr.ipCacheWarmedOnStartup.Store(false)
		res, err := httpAd(gr).Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: "hr1"}})
		require.NoError(t, err)
		require.True(t, res.Requeue)
		require.Equal(t, ipCacheStartupWarmRequeueAfter, res.RequeueAfter)

	case reconcileRouteDNSBuildEndpointsErr:
		nsInfra := testNamespace("infra")
		nsA := testNamespaceAllowedDNSZones("team-a", map[string]string{"example.com": "/zones/a"})
		gw := testGatewayManaged("infra", "gw1", "*.example.com", "10.0.0.1", "2001:db8::1")
		hr := testHTTPRouteAccepted("team-a", "hr-focus", "uid-focus", "foo.example.com", "infra", "gw1")
		fc := fakeClientForGatewayTests(nsInfra, nsA, gw, hr)
		mgr, _ := createFakeManagerAndClient(t)
		vpcM := &pkgmock.MockVPCServiceProvider{}
		nc := gatewayTestVPCNetworkConfiguration()
		nc.Spec.DNSZones = nil
		vpcM.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(nc, nil)
		t.Cleanup(func() { vpcM.AssertExpectations(t) })
		svc := &dns.DNSRecordService{
			Service:        servicecommon.Service{Client: fc},
			DNSRecordStore: dns.BuildDNSRecordStore(),
			VPCService:     vpcM,
		}
		gr := NewGatewayReconciler(mgr, svc)
		gr.Client = fc
		gr.apiResources = ar
		gr.ipCache = NewGatewayIPCache()
		gr.ipCacheWarmedOnStartup.Store(true)
		refreshIPCache(ctx, t, gr, gw)
		gr.StatusUpdater = newMockStatusUpdaterWithCallChain(t, []string{callUpdateFail})
		_, err := httpAd(gr).Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "team-a", Name: "hr-focus"}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "build endpoints")

	case reconcileRouteDNSSuccess:
		nsObj := testNamespaceAllowedDNSZones("app", map[string]string{"example.com": "/zones/test"})
		gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
		hr := testHTTPRouteAccepted("app", "hr1", "u1", "solo.example.com", "app", "gw1")
		fc := fakeClientForGatewayTests(nsObj, gw, hr)
		mgr, _ := createFakeManagerAndClient(t)
		svc := &dns.DNSRecordService{
			Service: servicecommon.Service{
				Client:    fc,
				NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}},
			},
			DNSRecordStore: dns.BuildDNSRecordStore(),
			VPCService:     gatewayTestVPCMock(),
			DNSZoneMap:     gatewayTestVPCDNSZoneMap(),
		}
		gr := NewGatewayReconciler(mgr, svc)
		gr.Client = fc
		gr.apiResources = ar
		gr.ipCache = NewGatewayIPCache()
		gr.ipCacheWarmedOnStartup.Store(true)
		refreshIPCache(ctx, t, gr, gw)
		gr.StatusUpdater = newMockStatusUpdaterWithCallChain(t, []string{callUpdateSuccess})
		_, err := httpAd(gr).Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: "hr1"}})
		require.NoError(t, err)

	case reconcileRouteDNSStatusUpdateFail:
		nsObj := testNamespaceAllowedDNSZones("app", map[string]string{"example.com": "/zones/test"})
		gw := testGatewayManaged("app", "gw1", "*.example.com", "10.0.0.1", "")
		hr := testHTTPRouteAccepted("app", "hr1", "u1", "ok.example.com", "app", "gw1")
		fc := fakeClientForGatewayTestsFailStatusUpdate(nsObj, gw, hr)
		gr := newTestGatewayReconciler(t, fc, ar)
		gr.StatusUpdater = newMockStatusUpdater(t)
		gr.ipCache = NewGatewayIPCache()
		gr.ipCacheWarmedOnStartup.Store(true)
		refreshIPCache(ctx, t, gr, gw)
		_, err := httpAd(gr).Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: "hr1"}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "update Route DNSConfig status")

	default:
		t.Fatalf("unknown branch %d", br)
	}
}

func TestReconcileRouteDNS_table(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		br   reconcileRouteDNSCase
	}{
		{name: "notFound_deletesOwnerRecords", br: reconcileRouteDNSNotFound},
		{name: "ipCacheNotWarmed_requeues", br: reconcileRouteDNSIPCacheCold},
		{name: "buildEndpointsError_returnsWrappedError", br: reconcileRouteDNSBuildEndpointsErr},
		{name: "success_callsStatusUpdater", br: reconcileRouteDNSSuccess},
		{name: "statusUpdateFailure_returnsError", br: reconcileRouteDNSStatusUpdateFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runReconcileRouteDNSCase(ctx, t, tc.br)
		})
	}
}
