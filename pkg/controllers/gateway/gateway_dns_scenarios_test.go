/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

// gatewayDNSSuite drives Gateway/Route DNS tests with a shared fake client, reconciler, and DNS store.
type gatewayDNSSuite struct {
	suite.Suite
	ctx    context.Context
	ns     string
	gr     *GatewayReconciler
	fc     client.Client
	dnsSvc *dns.DNSRecordService
}

// TestDNSRecordComprehensiveSuite runs gatewayDNSSuite (DNS store scenarios + GC + IP cache warm).
func TestDNSRecordComprehensiveSuite(t *testing.T) {
	suite.Run(t, new(gatewayDNSSuite))
}

func (s *gatewayDNSSuite) SetupTest() {
	s.ctx = context.Background()
	s.ns = testNSApp
}

func (s *gatewayDNSSuite) resetWithObjects(objs ...client.Object) {
	ns := testNamespaceAllowedDNSZones(s.ns, map[string]string{"example.com": "/zones/z1"})
	all := append([]client.Object{ns}, objs...)
	s.fc = fakeClientForGatewayTests(all...)
	s.dnsSvc = &dns.DNSRecordService{
		Service: servicecommon.Service{
			Client:    s.fc,
			NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}},
		},
		DNSRecordStore: dns.BuildDNSRecordStore(),
		VPCService:     gatewayTestVPCMock(),
		DNSZoneMap:     gatewayTestVPCDNSZoneMap(),
	}
	s.gr = newTestGatewayReconcilerWithService(s.T(), s.fc, apiResourcesForTests(true, true, true, false), s.dnsSvc)
	s.gr.StatusUpdater = newMockStatusUpdater(s.T())
	s.gr.ipCache = NewGatewayIPCache()
	s.gr.ipCacheWarmedOnStartup.Store(true)
}

func (s *gatewayDNSSuite) gwNN(name string) types.NamespacedName {
	return types.NamespacedName{Namespace: s.ns, Name: name}
}

// setupGatewayWithIPs updates Gateway status addresses, persists, reloads, and syncs ipCache (atomic for DNS tests).
func (s *gatewayDNSSuite) setupGatewayWithIPs(name string, ips []string) *gatewayv1.Gateway {
	updateGatewayStatusWithIPs(s.ctx, s.T(), s.fc, s.gr, s.gwNN(name), ips)
	gw := &gatewayv1.Gateway{}
	s.Require().NoError(s.fc.Get(s.ctx, s.gwNN(name), gw))
	return gw
}

func (s *gatewayDNSSuite) reconcileGateway(name string) {
	_, err := s.gr.Reconcile(s.ctx, ctrl.Request{NamespacedName: s.gwNN(name)})
	s.Require().NoError(err)
}

func (s *gatewayDNSSuite) reconcileHTTPRoute(name string) {
	ad := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute](s.gr, dns.ResourceKindHTTPRoute, func() *HTTPRoute { return &HTTPRoute{} })
	_, err := ad.Reconcile(s.ctx, ctrl.Request{NamespacedName: s.gwNN(name)})
	s.Require().NoError(err)
}

func (s *gatewayDNSSuite) reconcileGRPCRoute(name string) {
	ad := newRouteReconciler[*GRPCRoute, gatewayv1.GRPCRoute](s.gr, dns.ResourceKindGRPCRoute, func() *GRPCRoute { return &GRPCRoute{} })
	_, err := ad.Reconcile(s.ctx, ctrl.Request{NamespacedName: s.gwNN(name)})
	s.Require().NoError(err)
}

// assertDNSRows asserts store rows for owner kind/name in the suite namespace (nil rows => expect none).
func (s *gatewayDNSSuite) assertDNSRows(ownerKind, ownerName string, rows []expectedDNSRow) {
	assertOwnerDNSRecordTargets(s.T(), s.dnsSvc.DNSRecordStore, ownerKind, s.ns, ownerName, wantMapFromRows(rows))
}

type dnsReconcileKind int

const (
	reconcileGateway dnsReconcileKind = iota
	reconcileHTTP
	reconcileGRPC
)

type dnsReconcileStep struct {
	kind dnsReconcileKind
	name string
}

type dnsOwnerExpect struct {
	kind      string
	name      string
	wantRows  []expectedDNSRow
	wantEmpty bool
}

type dnsScenarioCase struct {
	name        string
	objects     []client.Object
	gatewayIPs  map[string][]string // gateway name -> IPs applied via setupGatewayWithIPs after reset
	steps       []dnsReconcileStep
	expect      []dnsOwnerExpect
	extra       func(*gatewayDNSSuite) // optional second phase (e.g. delete + reconcile)
	expectAfter []dnsOwnerExpect       // assertions after extra
}

func (s *gatewayDNSSuite) TestDNSRecordComprehensive() {
	cases := []dnsScenarioCase{
		{
			name: "Gateway direct DNS annotation dual-stack",
			objects: []client.Object{
				withDNSHostnameAnnotation(makeGateway("gw1",
					WithGatewayNamespace(s.ns),
					WithListenerHostname("*.example.com"),
					WithGatewayStatusAddressPair("10.0.0.1", "2001:db8::1"),
				), "gw-direct.example.com"),
			},
			gatewayIPs: map[string][]string{"gw1": {"10.0.0.1", "2001:db8::1"}},
			steps:      []dnsReconcileStep{{kind: reconcileGateway, name: "gw1"}},
			expect: []dnsOwnerExpect{{
				kind: dns.ResourceKindGateway, name: "gw1",
				wantRows: []expectedDNSRow{
					{FQDN: "gw-direct.example.com", Type: "A", Targets: []string{"10.0.0.1"}},
					{FQDN: "gw-direct.example.com", Type: "AAAA", Targets: []string{"2001:db8::1"}},
				},
			}},
		},
		{
			name: "Route spec hostnames only (HTTP and GRPC share one Gateway)",
			objects: []client.Object{
				makeGateway("gw1", WithGatewayNamespace(s.ns), WithListenerHostname("*.example.com"), WithGatewayStatusAddressPair("10.0.0.1", "")),
				makeHTTPRoute("hr1",
					httpRouteWithNamespace(s.ns),
					httpRouteWithUID("uid-hr1"),
					httpRouteWithParentGateway(s.ns, "gw1"),
					httpRouteWithHostnames("http-spec.example.com"),
				),
				makeGRPCRoute("gr1",
					grpcRouteWithNamespace(s.ns),
					grpcRouteWithUID("uid-gr1"),
					grpcRouteWithParentGateway(s.ns, "gw1"),
					grpcRouteWithHostnames("grpc-spec.example.com"),
				),
			},
			gatewayIPs: map[string][]string{"gw1": {"10.0.0.1"}},
			steps: []dnsReconcileStep{
				{kind: reconcileHTTP, name: "hr1"},
				{kind: reconcileGRPC, name: "gr1"},
			},
			expect: []dnsOwnerExpect{
				{
					kind: dns.ResourceKindHTTPRoute, name: "hr1",
					wantRows: []expectedDNSRow{{FQDN: "http-spec.example.com", Type: "A", Targets: []string{"10.0.0.1"}}},
				},
				{
					kind: dns.ResourceKindGRPCRoute, name: "gr1",
					wantRows: []expectedDNSRow{{FQDN: "grpc-spec.example.com", Type: "A", Targets: []string{"10.0.0.1"}}},
				},
			},
		},
		{
			name: "Annotation and spec union deduped",
			objects: []client.Object{
				makeGateway("gw1", WithGatewayNamespace(s.ns), WithListenerHostname("*.example.com"), WithGatewayStatusAddressPair("10.0.0.1", "")),
				makeHTTPRoute("hr-dedupe",
					httpRouteWithNamespace(s.ns),
					httpRouteWithUID("uid-dedupe"),
					httpRouteWithParentGateway(s.ns, "gw1"),
					httpRouteWithHostnames("a.example.com"),
					httpRouteWithDNSHostnameAnnotation("a.example.com,extra.example.com"),
				),
			},
			gatewayIPs: map[string][]string{"gw1": {"10.0.0.1"}},
			steps:      []dnsReconcileStep{{kind: reconcileHTTP, name: "hr-dedupe"}},
			expect: []dnsOwnerExpect{{
				kind: dns.ResourceKindHTTPRoute, name: "hr-dedupe",
				wantRows: []expectedDNSRow{
					{FQDN: "a.example.com", Type: "A", Targets: []string{"10.0.0.1"}},
					{FQDN: "extra.example.com", Type: "A", Targets: []string{"10.0.0.1"}},
				},
			}},
		},
		{
			name: "Multi-parent target union",
			objects: []client.Object{
				makeGateway("gw1", WithGatewayNamespace(s.ns), WithListenerHostname("*.example.com"), WithGatewayStatusAddressPair("10.0.0.1", "")),
				makeGateway("gw2", WithGatewayNamespace(s.ns), WithListenerHostname("*.example.com"), WithGatewayStatusAddressPair("10.0.0.2", "")),
				makeHTTPRoute("hr-mp",
					httpRouteWithNamespace(s.ns),
					httpRouteWithUID("uid-mp"),
					httpRouteWithParentGateways(s.ns, "gw1", "gw2"),
					httpRouteWithHostnames("shared-mp.example.com"),
				),
			},
			gatewayIPs: map[string][]string{"gw1": {"10.0.0.1"}, "gw2": {"10.0.0.2"}},
			steps:      []dnsReconcileStep{{kind: reconcileHTTP, name: "hr-mp"}},
			expect: []dnsOwnerExpect{{
				kind: dns.ResourceKindHTTPRoute, name: "hr-mp",
				wantRows: []expectedDNSRow{{FQDN: "shared-mp.example.com", Type: "A", Targets: []string{"10.0.0.1", "10.0.0.2"}}},
			}},
		},
		{
			name: "Inherited hostnames from listener",
			objects: []client.Object{
				makeGateway("gw1", WithGatewayNamespace(s.ns), WithListenerHostname("inherit.example.com"), WithGatewayStatusAddressPair("10.0.0.1", "")),
				makeHTTPRoute("hr-inherit",
					httpRouteWithNamespace(s.ns),
					httpRouteWithUID("uid-inherit"),
					httpRouteWithParentGateway(s.ns, "gw1"),
				),
			},
			gatewayIPs: map[string][]string{"gw1": {"10.0.0.1"}},
			steps:      []dnsReconcileStep{{kind: reconcileHTTP, name: "hr-inherit"}},
			expect: []dnsOwnerExpect{{
				kind: dns.ResourceKindHTTPRoute, name: "hr-inherit",
				wantRows: []expectedDNSRow{{FQDN: "inherit.example.com", Type: "A", Targets: []string{"10.0.0.1"}}},
			}},
		},
		{
			name: "Negative DNS ignore annotation on Gateway",
			objects: func() []client.Object {
				gw := withDNSHostnameAnnotation(
					makeGateway("gw1", WithGatewayNamespace(s.ns), WithListenerHostname("*.example.com"), WithGatewayStatusAddressPair("10.0.0.1", "")),
					"ignored.example.com",
				)
				if gw.Annotations == nil {
					gw.Annotations = map[string]string{}
				}
				gw.Annotations[servicecommon.AnnotationsDNSIgnoreGateway] = "true"
				return []client.Object{gw}
			}(),
			gatewayIPs: map[string][]string{"gw1": {"10.0.0.1"}},
			steps:      []dnsReconcileStep{{kind: reconcileGateway, name: "gw1"}},
			expect: []dnsOwnerExpect{{
				kind: dns.ResourceKindGateway, name: "gw1", wantEmpty: true,
			}},
		},
		{
			name: "Negative unmanaged GatewayClass",
			objects: []client.Object{
				withDNSHostnameAnnotation(
					makeGateway("gw1",
						WithGatewayNamespace(s.ns),
						WithListenerHostname("*.example.com"),
						WithGatewayStatusAddressPair("10.0.0.1", ""),
						WithGatewayClass("Contour"),
					),
					"unmanaged.example.com",
				),
			},
			gatewayIPs: map[string][]string{"gw1": {"10.0.0.1"}},
			steps:      []dnsReconcileStep{{kind: reconcileGateway, name: "gw1"}},
			expect: []dnsOwnerExpect{{
				kind: dns.ResourceKindGateway, name: "gw1", wantEmpty: true,
			}},
		},
		{
			name: "Negative route not Accepted",
			objects: []client.Object{
				makeGateway("gw1", WithGatewayNamespace(s.ns), WithListenerHostname("*.example.com"), WithGatewayStatusAddressPair("10.0.0.1", "")),
				makeHTTPRoute("hr-na",
					httpRouteWithNamespace(s.ns),
					httpRouteWithUID("uid-na"),
					httpRouteWithParentGateway(s.ns, "gw1"),
					httpRouteWithHostnames("na.example.com"),
					httpRouteWithAccepted(false),
				),
			},
			gatewayIPs: map[string][]string{"gw1": {"10.0.0.1"}},
			steps:      []dnsReconcileStep{{kind: reconcileHTTP, name: "hr-na"}},
			expect: []dnsOwnerExpect{{
				kind: dns.ResourceKindHTTPRoute, name: "hr-na", wantEmpty: true,
			}},
		},
		{
			name: "Garbage collection: HTTPRoute delete removes DNS store rows",
			objects: []client.Object{
				makeGateway("gw1", WithGatewayNamespace(s.ns), WithListenerHostname("*.example.com"), WithGatewayStatusAddressPair("10.0.0.1", "")),
				makeHTTPRoute("hr-gc",
					httpRouteWithNamespace(s.ns),
					httpRouteWithUID("uid-gc"),
					httpRouteWithParentGateway(s.ns, "gw1"),
					httpRouteWithHostnames("gc.example.com"),
				),
			},
			gatewayIPs: map[string][]string{"gw1": {"10.0.0.1"}},
			steps:      []dnsReconcileStep{{kind: reconcileHTTP, name: "hr-gc"}},
			expect: []dnsOwnerExpect{{
				kind: dns.ResourceKindHTTPRoute, name: "hr-gc",
				wantRows: []expectedDNSRow{{FQDN: "gc.example.com", Type: "A", Targets: []string{"10.0.0.1"}}},
			}},
			extra: func(s *gatewayDNSSuite) {
				s.Require().NotEmpty(s.dnsSvc.DNSRecordStore.GetByOwnerResourceNamespacedName(dns.ResourceKindHTTPRoute, s.ns, "hr-gc"))
				hr := &gatewayv1.HTTPRoute{}
				s.Require().NoError(s.fc.Get(s.ctx, s.gwNN("hr-gc"), hr))
				s.Require().NoError(s.fc.Delete(s.ctx, hr))
				s.reconcileHTTPRoute("hr-gc")
			},
			expectAfter: []dnsOwnerExpect{{
				kind: dns.ResourceKindHTTPRoute, name: "hr-gc", wantEmpty: true,
			}},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.resetWithObjects(tc.objects...)
			for gwn, ips := range tc.gatewayIPs {
				s.setupGatewayWithIPs(gwn, ips)
			}
			for _, st := range tc.steps {
				switch st.kind {
				case reconcileGateway:
					s.reconcileGateway(st.name)
				case reconcileHTTP:
					s.reconcileHTTPRoute(st.name)
				case reconcileGRPC:
					s.reconcileGRPCRoute(st.name)
				}
			}
			for _, ex := range tc.expect {
				if ex.wantEmpty {
					s.assertDNSRows(ex.kind, ex.name, nil)
					continue
				}
				s.assertDNSRows(ex.kind, ex.name, ex.wantRows)
			}
			if tc.extra != nil {
				tc.extra(s)
				for _, ex := range tc.expectAfter {
					if ex.wantEmpty {
						s.assertDNSRows(ex.kind, ex.name, nil)
						continue
					}
					s.assertDNSRows(ex.kind, ex.name, ex.wantRows)
				}
			}
		})
	}
}
