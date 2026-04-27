/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	mockcache "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/cache"
	mockclient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mockmgr "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/manager"
	mockgateway "github.com/vmware-tanzu/nsx-operator/pkg/mock/gateway"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

// stableDNSRecordIDWithDNSZone matches dns test preimage rules (gateway cannot import dns *_test).
func stableDNSRecordIDWithDNSZone(ownerUID, dnsName, recordType, zonePath string) string {
	zp := strings.TrimSpace(zonePath)
	dn := strings.TrimSpace(strings.ToLower(dnsName))
	rt := strings.TrimSpace(recordType)
	if zp == "" {
		preimage := strings.Join([]string{ownerUID, dn, rt}, ":")
		h := sha256.Sum256([]byte(preimage))
		return "dns-" + hex.EncodeToString(h[:16])
	}
	preimage := strings.Join([]string{ownerUID, dn, rt, zp}, ":")
	h := sha256.Sum256([]byte(preimage))
	return "dns-" + hex.EncodeToString(h[:16])
}

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = gatewayv1.Install(scheme)
}

var scheme = runtime.NewScheme()

var _ servicecommon.VPCServiceProvider = (*pkgmock.MockVPCServiceProvider)(nil)

func gatewayTestVPCNetworkConfiguration() *v1alpha1.VPCNetworkConfiguration {
	return &v1alpha1.VPCNetworkConfiguration{
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			DNSZones: []string{"/zones/z1"},
		},
		Status: v1alpha1.VPCNetworkConfigurationStatus{
			VPCs: []v1alpha1.VPCInfo{{
				VPCPath: "/orgs/org1/projects/proj1/vpcs/vpc1",
			}},
		},
	}
}

// gatewayTestVPCMock: GetVPCNetworkConfigByNamespace → gatewayTestVPCNetworkConfiguration.
func gatewayTestVPCMock() *pkgmock.MockVPCServiceProvider {
	m := &pkgmock.MockVPCServiceProvider{}
	m.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(gatewayTestVPCNetworkConfiguration(), nil)
	return m
}

// gatewayTestVPCDNSZoneMap: zone path → domain for ValidateEndpointsByDNSZone in tests.
func gatewayTestVPCDNSZoneMap() map[string]string {
	return map[string]string{"/zones/z1": "example.com"}
}

// apiResourcesForTests: Gateway true; toggles ListenerSet and Route kinds.
func apiResourcesForTests(listenerSet, httpRoute, grpcRoute, tlsRoute bool) gatewayAPIResources {
	return gatewayAPIResources{
		gateway:     true,
		listenerSet: listenerSet,
		httpRoute:   httpRoute,
		grpcRoute:   grpcRoute,
		tlsRoute:    tlsRoute,
	}
}

type fakeDiscoveryClient struct {
	discovery.DiscoveryInterface
	resources *metav1.APIResourceList
	err       error
}

func (f *fakeDiscoveryClient) ServerResourcesForGroupVersion(string) (*metav1.APIResourceList, error) {
	return f.resources, f.err
}

func dnsTestService() *dns.DNSRecordService {
	return dnsTestServiceWithClient(fakeClientForGatewayTests())
}

// dnsTestServiceWithClient builds a DNSRecordService whose Service.Client is fc (same as reconciler tests expect).
func dnsTestServiceWithClient(fc client.Client) *dns.DNSRecordService {
	return &dns.DNSRecordService{
		Service: servicecommon.Service{
			Client:    fc,
			NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}},
		},
		DNSRecordStore: dns.BuildDNSRecordStore(),
		VPCService:     gatewayTestVPCMock(),
		DNSZoneMap:     gatewayTestVPCDNSZoneMap(),
	}
}

// newTestGatewayReconciler builds mgr + GatewayReconciler with DNS service tied to fc, then sets Client and api flags.
func newTestGatewayReconciler(t *testing.T, fc client.Client, ar gatewayAPIResources) *GatewayReconciler {
	t.Helper()
	mgr, _ := createFakeManagerAndClient(t)
	gr := NewGatewayReconciler(mgr, dnsTestServiceWithClient(fc))
	gr.Client = fc
	gr.apiResources = ar
	return gr
}

func newTestGatewayReconcilerFromObjects(t *testing.T, ar gatewayAPIResources, objs ...client.Object) *GatewayReconciler {
	return newTestGatewayReconciler(t, fakeClientForGatewayTests(objs...), ar)
}

// newTestGatewayReconcilerWithService is like newTestGatewayReconciler but uses an existing DNS service (shared store).
func newTestGatewayReconcilerWithService(t *testing.T, fc client.Client, ar gatewayAPIResources, svc *dns.DNSRecordService) *GatewayReconciler {
	t.Helper()
	mgr, _ := createFakeManagerAndClient(t)
	gr := NewGatewayReconciler(mgr, svc)
	gr.Client = fc
	gr.apiResources = ar
	return gr
}

func fakeGatewayReconciler(t *testing.T, objs ...client.Object) (*GatewayReconciler, client.Client) {
	t.Helper()
	mgr, fc := createFakeManagerAndClient(t, objs...)
	r := NewGatewayReconciler(mgr, dnsTestServiceWithClient(fc))
	r.StatusUpdater = newMockStatusUpdater(t)
	// Unit tests do not run warmGatewayIPCacheOnStartup; behave as after a successful warm.
	r.ipCacheWarmedOnStartup.Store(true)
	return r, fc
}

func gatewayFakeClientBuilder() *fake.ClientBuilder {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&gatewayv1.Gateway{}, &gatewayv1.ListenerSet{}, &gatewayv1.HTTPRoute{}, &gatewayv1.GRPCRoute{}, &gatewayv1.TLSRoute{}).
		WithIndex(&gatewayv1.ListenerSet{}, listenerSetParentGatewayIndex, listenerSetParentGatewayIndexFunc).
		WithIndex(&gatewayv1.HTTPRoute{}, routeParentGatewayIndex, routeParentGatewayIndexFunc).
		WithIndex(&gatewayv1.HTTPRoute{}, routeParentListenerSetIndex, routeParentListenerSetIndexFunc).
		WithIndex(&gatewayv1.GRPCRoute{}, routeParentGatewayIndex, routeParentGatewayIndexFunc).
		WithIndex(&gatewayv1.GRPCRoute{}, routeParentListenerSetIndex, routeParentListenerSetIndexFunc).
		WithIndex(&gatewayv1.TLSRoute{}, routeParentGatewayIndex, routeParentGatewayIndexFunc).
		WithIndex(&gatewayv1.TLSRoute{}, routeParentListenerSetIndex, routeParentListenerSetIndexFunc)
}

func fakeClientForGatewayTests(objs ...client.Object) client.Client {
	b := gatewayFakeClientBuilder()
	if len(objs) > 0 {
		b = b.WithObjects(objs...)
	}
	return b.Build()
}

// fakeClientForGatewayTestsFailStatusUpdate is like fakeClientForGatewayTests but SubResource("status").Update fails (for Route status patch error paths).
func fakeClientForGatewayTestsFailStatusUpdate(objs ...client.Object) client.Client {
	b := gatewayFakeClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if subResourceName == "status" {
				return fmt.Errorf("injected status writer update failure")
			}
			return c.SubResource(subResourceName).Update(ctx, obj, opts...)
		},
	})
	if len(objs) > 0 {
		b = b.WithObjects(objs...)
	}
	return b.Build()
}

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

// expectStatusUpdaterCallChain wires ordered gomock expectations for StatusUpdater (matches legacy validateCalls order).
func expectStatusUpdaterCallChain(m *mockgateway.MockStatusUpdater, want []string) {
	var prev *gomock.Call
	for _, w := range want {
		var c *gomock.Call
		switch w {
		case callIncreaseSyncTotal:
			c = m.EXPECT().IncreaseSyncTotal()
		case callIncreaseUpdateTotal:
			c = m.EXPECT().IncreaseUpdateTotal()
		case callIncreaseDeleteTotal:
			c = m.EXPECT().IncreaseDeleteTotal()
		case callIncreaseDeleteSuccessTotal:
			c = m.EXPECT().IncreaseDeleteSuccessTotal()
		case callIncreaseDeleteFailTotal:
			c = m.EXPECT().IncreaseDeleteFailTotal()
		case callUpdateSuccess:
			c = m.EXPECT().UpdateSuccess(gomock.Any(), gomock.Any(), gomock.Any())
		case callUpdateFail:
			c = m.EXPECT().UpdateFail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any())
		case callDeleteSuccess:
			c = m.EXPECT().DeleteSuccess(gomock.Any(), gomock.Any())
		case callDeleteFail:
			c = m.EXPECT().DeleteFail(gomock.Any(), gomock.Any(), gomock.Any())
		default:
			panic("gateway test: unknown statusUpdater call marker " + w)
		}
		if prev != nil {
			c.After(prev)
		}
		prev = c
	}
}

// newMockStatusUpdater returns a permissive gomock-backed StatusUpdater for tests that do not assert call order.
func newMockStatusUpdater(t *testing.T) StatusUpdater {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	m := mockgateway.NewMockStatusUpdater(ctrl)
	m.EXPECT().UpdateSuccess(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.EXPECT().UpdateFail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.EXPECT().DeleteSuccess(gomock.Any(), gomock.Any()).AnyTimes()
	m.EXPECT().DeleteFail(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.EXPECT().IncreaseSyncTotal().AnyTimes()
	m.EXPECT().IncreaseUpdateTotal().AnyTimes()
	m.EXPECT().IncreaseDeleteTotal().AnyTimes()
	m.EXPECT().IncreaseDeleteSuccessTotal().AnyTimes()
	m.EXPECT().IncreaseDeleteFailTotal().AnyTimes()
	return m
}

// newMockStatusUpdaterWithCallChain returns a StatusUpdater with exact ordered expectations (gomock verifies on t cleanup).
func newMockStatusUpdaterWithCallChain(t *testing.T, want []string) StatusUpdater {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	m := mockgateway.NewMockStatusUpdater(ctrl)
	expectStatusUpdaterCallChain(m, want)
	return m
}
func ptrGWGroup(g string) *gatewayv1.Group {
	gg := gatewayv1.Group(g)
	return &gg
}

func ptrGWKind(k string) *gatewayv1.Kind {
	kk := gatewayv1.Kind(k)
	return &kk
}

func testNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// --- Test factories (functional options; defaults: namespace app, managed GatewayClass, standard listener) ---

// GatewayOption mutates a Gateway under construction.
type GatewayOption func(*gatewayv1.Gateway)

// WithGatewayNamespace sets Gateway namespace (default testNSApp).
func WithGatewayNamespace(ns string) GatewayOption {
	return func(g *gatewayv1.Gateway) { g.Namespace = ns }
}

// WithListenerHostname sets the first listener hostname.
func WithListenerHostname(host string) GatewayOption {
	h := gatewayv1.Hostname(host)
	return func(g *gatewayv1.Gateway) {
		if len(g.Spec.Listeners) == 0 {
			lname := gatewayv1.SectionName("http")
			g.Spec.Listeners = []gatewayv1.Listener{{
				Name: lname, Hostname: &h, Port: 80, Protocol: gatewayv1.HTTPProtocolType,
			}}
			return
		}
		g.Spec.Listeners[0].Hostname = &h
	}
}

// WithGatewayClass sets GatewayClassName (default managed Istio class).
func WithGatewayClass(name gatewayv1.ObjectName) GatewayOption {
	return func(g *gatewayv1.Gateway) { g.Spec.GatewayClassName = name }
}

// WithGatewayStatusAddressPair mirrors legacy tests: two address slots (IPv4 then IPv6, either may be empty string).
func WithGatewayStatusAddressPair(ipv4, ipv6 string) GatewayOption {
	t := gatewayv1.IPAddressType
	return func(g *gatewayv1.Gateway) {
		g.Status.Addresses = []gatewayv1.GatewayStatusAddress{
			{Type: &t, Value: ipv4},
			{Type: &t, Value: ipv6},
		}
	}
}

// makeGateway returns a managed Gateway with one HTTP listener; apply options to specialize.
func makeGateway(name string, opts ...GatewayOption) *gatewayv1.Gateway {
	h := gatewayv1.Hostname("*.example.com")
	lname := gatewayv1.SectionName("http")
	g := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNSApp,
			Name:      name,
			UID:       types.UID(name + "-uid"),
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(common.ManagedK8sGatewayClassIstio),
			Listeners: []gatewayv1.Listener{{
				Name:     lname,
				Hostname: &h,
				Port:     80,
				Protocol: gatewayv1.HTTPProtocolType,
			}},
		},
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

// parentRefGateway returns a ParentReference to a Gateway, setting Namespace when it differs from routeNS.
func parentRefGateway(gwNS, gwName, routeNS string) gatewayv1.ParentReference {
	pr := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("Gateway"),
		Name:  gatewayv1.ObjectName(gwName),
	}
	if gwNS != routeNS {
		nsCopy := gatewayv1.Namespace(gwNS)
		pr.Namespace = &nsCopy
	}
	return pr
}

func routeHostnames(hosts ...string) []gatewayv1.Hostname {
	out := make([]gatewayv1.Hostname, 0, len(hosts))
	for _, x := range hosts {
		out = append(out, gatewayv1.Hostname(x))
	}
	return out
}

func ensureRouteParentConditions(rs *gatewayv1.RouteStatus, refs []gatewayv1.ParentReference, accepted bool) {
	st := metav1.ConditionTrue
	if !accepted {
		st = metav1.ConditionFalse
	}
	rs.Parents = nil
	for _, pr := range refs {
		rs.Parents = append(rs.Parents, gatewayv1.RouteParentStatus{
			ParentRef: pr,
			Conditions: []metav1.Condition{{
				Type: string(gatewayv1.RouteConditionAccepted), Status: st,
			}},
		})
	}
}

// httpRouteOption mutates an HTTPRoute under construction.
type httpRouteOption func(*gatewayv1.HTTPRoute)

func httpRouteWithNamespace(ns string) httpRouteOption {
	return func(h *gatewayv1.HTTPRoute) { h.Namespace = ns }
}

func httpRouteWithUID(uid string) httpRouteOption {
	return func(h *gatewayv1.HTTPRoute) { h.ObjectMeta.UID = types.UID(uid) }
}

func httpRouteWithParentGateway(gwNS, gwName string) httpRouteOption {
	return func(h *gatewayv1.HTTPRoute) {
		h.Spec.ParentRefs = []gatewayv1.ParentReference{parentRefGateway(gwNS, gwName, h.Namespace)}
	}
}

func httpRouteWithParentGateways(gwNS string, gwNames ...string) httpRouteOption {
	return func(h *gatewayv1.HTTPRoute) {
		refs := make([]gatewayv1.ParentReference, 0, len(gwNames))
		for _, gn := range gwNames {
			refs = append(refs, parentRefGateway(gwNS, gn, h.Namespace))
		}
		h.Spec.ParentRefs = refs
	}
}

func httpRouteWithHostnames(hosts ...string) httpRouteOption {
	return func(h *gatewayv1.HTTPRoute) { h.Spec.Hostnames = routeHostnames(hosts...) }
}

func httpRouteWithDNSHostnameAnnotation(csv string) httpRouteOption {
	return func(h *gatewayv1.HTTPRoute) {
		if h.Annotations == nil {
			h.Annotations = map[string]string{}
		}
		h.Annotations[servicecommon.AnnotationDNSHostnameKey] = csv
	}
}

func httpRouteWithAccepted(accepted bool) httpRouteOption {
	return func(h *gatewayv1.HTTPRoute) {
		ensureRouteParentConditions(&h.Status.RouteStatus, h.Spec.ParentRefs, accepted)
	}
}

// makeHTTPRoute builds an HTTPRoute with defaults (namespace app); options should set parent refs and hostnames.
func makeHTTPRoute(name string, opts ...httpRouteOption) *gatewayv1.HTTPRoute {
	h := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNSApp, Name: name, UID: types.UID(name + "-uid")},
	}
	for _, o := range opts {
		o(h)
	}
	if len(h.Status.Parents) == 0 && len(h.Spec.ParentRefs) > 0 {
		ensureRouteParentConditions(&h.Status.RouteStatus, h.Spec.ParentRefs, true)
	}
	return h
}

// grpcRouteOption mutates a GRPCRoute under construction.
type grpcRouteOption func(*gatewayv1.GRPCRoute)

func grpcRouteWithNamespace(ns string) grpcRouteOption {
	return func(r *gatewayv1.GRPCRoute) { r.Namespace = ns }
}

func grpcRouteWithUID(uid string) grpcRouteOption {
	return func(r *gatewayv1.GRPCRoute) { r.ObjectMeta.UID = types.UID(uid) }
}

func grpcRouteWithParentGateway(gwNS, gwName string) grpcRouteOption {
	return func(r *gatewayv1.GRPCRoute) {
		r.Spec.ParentRefs = []gatewayv1.ParentReference{parentRefGateway(gwNS, gwName, r.Namespace)}
	}
}

func grpcRouteWithHostnames(hosts ...string) grpcRouteOption {
	return func(r *gatewayv1.GRPCRoute) { r.Spec.Hostnames = routeHostnames(hosts...) }
}

func makeGRPCRoute(name string, opts ...grpcRouteOption) *gatewayv1.GRPCRoute {
	r := &gatewayv1.GRPCRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNSApp, Name: name, UID: types.UID(name + "-uid")},
	}
	for _, o := range opts {
		o(r)
	}
	if len(r.Status.Parents) == 0 && len(r.Spec.ParentRefs) > 0 {
		ensureRouteParentConditions(&r.Status.RouteStatus, r.Spec.ParentRefs, true)
	}
	return r
}

// testNamespaceAllowedDNSZones: ns annotation domain → NSX zone path JSON.
func testNamespaceAllowedDNSZones(name string, domainToZonePath map[string]string) *corev1.Namespace {
	raw, err := json.Marshal(domainToZonePath)
	if err != nil {
		panic(err)
	}
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				"nsx.vmware.com/allowed-dns-zones": string(raw),
			},
		},
	}
}

func testGatewayManaged(ns, name string, listenerHost string, ipv4, ipv6 string) *gatewayv1.Gateway {
	return makeGateway(name,
		WithGatewayNamespace(ns),
		WithListenerHostname(listenerHost),
		WithGatewayStatusAddressPair(ipv4, ipv6),
	)
}

func addrTypePtr() *gatewayv1.AddressType {
	t := gatewayv1.IPAddressType
	return &t
}

func withDNSHostnameAnnotation(gw *gatewayv1.Gateway, hosts string) *gatewayv1.Gateway {
	g := gw.DeepCopy()
	if g.Annotations == nil {
		g.Annotations = map[string]string{}
	}
	g.Annotations[servicecommon.AnnotationDNSHostnameKey] = hosts
	return g
}

func testHTTPRouteAccepted(ns, name, uid, hostname string, gwNS, gwName string) *gatewayv1.HTTPRoute {
	return makeHTTPRoute(name,
		httpRouteWithNamespace(ns),
		httpRouteWithUID(uid),
		httpRouteWithParentGateway(gwNS, gwName),
		httpRouteWithHostnames(hostname),
	)
}

func testGRPCRouteAccepted(ns, name, uid, hostname string, gwNS, gwName string) *gatewayv1.GRPCRoute {
	return makeGRPCRoute(name,
		grpcRouteWithNamespace(ns),
		grpcRouteWithUID(uid),
		grpcRouteWithParentGateway(gwNS, gwName),
		grpcRouteWithHostnames(hostname),
	)
}

func testListenerSet(ns, name, _, gwName string) *gatewayv1.ListenerSet {
	h := gatewayv1.Hostname("hello.example.com")
	pn := gatewayv1.PortNumber(80)
	return &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID(name + "-ls-uid")},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{
				Group: ptrGWGroup(gatewayv1.GroupName),
				Kind:  ptrGWKind("Gateway"),
				Name:  gatewayv1.ObjectName(gwName),
			},
			Listeners: []gatewayv1.ListenerEntry{{
				Name:     "l1",
				Hostname: &h,
				Port:     pn,
				Protocol: gatewayv1.HTTPProtocolType,
			}},
		},
	}
}

func testHTTPRouteViaListenerSet(ns, routeName, uid, hostname, lsNS, lsName, _, _ string) *gatewayv1.HTTPRoute {
	lsRef := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("ListenerSet"),
		Name:  gatewayv1.ObjectName(lsName),
	}
	if lsNS != ns {
		n := gatewayv1.Namespace(lsNS)
		lsRef.Namespace = &n
	}
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: routeName, UID: types.UID(uid)},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{lsRef}},
			Hostnames:       routeHostnames(hostname),
		},
		Status: gatewayv1.HTTPRouteStatus{
			RouteStatus: gatewayv1.RouteStatus{Parents: routeParentsAcceptedTrue(lsRef)},
		},
	}
}

func listenerSetParentRefForRoute(routeNS, lsNS, lsName string) gatewayv1.ParentReference {
	lsRef := gatewayv1.ParentReference{
		Group: ptrGWGroup(gatewayv1.GroupName),
		Kind:  ptrGWKind("ListenerSet"),
		Name:  gatewayv1.ObjectName(lsName),
	}
	if lsNS != routeNS {
		n := gatewayv1.Namespace(lsNS)
		lsRef.Namespace = &n
	}
	return lsRef
}

func routeParentsAcceptedTrue(refs ...gatewayv1.ParentReference) []gatewayv1.RouteParentStatus {
	ps := make([]gatewayv1.RouteParentStatus, 0, len(refs))
	for _, pr := range refs {
		ps = append(ps, gatewayv1.RouteParentStatus{
			ParentRef: pr,
			Conditions: []metav1.Condition{{
				Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue,
			}},
		})
	}
	return ps
}

func grpcRouteAttachedToListenerSet(ns, name, uid, host, lsNS, lsName string) *gatewayv1.GRPCRoute {
	lsRef := listenerSetParentRefForRoute(ns, lsNS, lsName)
	return &gatewayv1.GRPCRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID(uid)},
		Spec: gatewayv1.GRPCRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{lsRef}},
			Hostnames:       routeHostnames(host),
		},
		Status: gatewayv1.GRPCRouteStatus{
			RouteStatus: gatewayv1.RouteStatus{Parents: routeParentsAcceptedTrue(lsRef)},
		},
	}
}

func refreshIPCache(ctx context.Context, t *testing.T, r *GatewayReconciler, gw *gatewayv1.Gateway) {
	t.Helper()
	nn := types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}
	var ls []gatewayv1.ListenerSet
	var err error
	if r.apiResources.listenerSet {
		ls, err = r.listSortedListenerSetsForGateway(ctx, nn)
		require.NoError(t, err)
	}
	_, _ = r.refreshGatewayIPCache(gw, ls)
}

// updateGatewayStatusWithIPs writes Gateway status.addresses from ips (IPv4 and/or IPv6), persists via the API
// client, reloads the Gateway, then refreshIPCache so gr.ipCache matches the cluster object (DNS paths read the cache).
func updateGatewayStatusWithIPs(ctx context.Context, t *testing.T, c client.Client, gr *GatewayReconciler, gwNN types.NamespacedName, ips []string) {
	t.Helper()
	gw := &gatewayv1.Gateway{}
	require.NoError(t, c.Get(ctx, gwNN, gw))
	ipTyp := gatewayv1.IPAddressType
	var addrs []gatewayv1.GatewayStatusAddress
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		addrs = append(addrs, gatewayv1.GatewayStatusAddress{Type: &ipTyp, Value: ip})
	}
	gw.Status.Addresses = addrs
	require.NoError(t, c.Status().Update(ctx, gw))
	require.NoError(t, c.Get(ctx, gwNN, gw))
	refreshIPCache(ctx, t, gr, gw)
}

// newGatewayGomockManager wires a *mockmgr.MockManager with permissive expectations for gateway unit tests.
func newGatewayGomockManager(t *testing.T, gctl *gomock.Controller, fc client.Client, sch *runtime.Scheme, c cache.Cache, fieldIndexer client.FieldIndexer) *mockmgr.MockManager {
	t.Helper()
	m := mockmgr.NewMockManager(gctl)
	if fieldIndexer == nil {
		fi := mockclient.NewMockFieldIndexer(gctl)
		fi.EXPECT().IndexField(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		fieldIndexer = fi
	}
	elected := make(chan struct{})
	close(elected)
	rec := record.NewFakeRecorder(1000)

	m.EXPECT().GetClient().Return(fc).AnyTimes()
	m.EXPECT().GetScheme().Return(sch).AnyTimes()
	m.EXPECT().GetCache().Return(c).AnyTimes()
	m.EXPECT().GetFieldIndexer().Return(fieldIndexer).AnyTimes()
	m.EXPECT().GetEventRecorderFor(gomock.Any()).Return(rec).AnyTimes()
	m.EXPECT().GetControllerOptions().Return(ctrlconfig.Controller{SkipNameValidation: ptr.To(true)}).AnyTimes()
	m.EXPECT().GetLogger().Return(logr.Discard()).AnyTimes()
	m.EXPECT().GetConfig().Return(&rest.Config{}).AnyTimes()
	m.EXPECT().GetHTTPClient().Return(http.DefaultClient).AnyTimes()
	m.EXPECT().GetRESTMapper().Return(meta.MultiRESTMapper{}).AnyTimes()
	m.EXPECT().GetAPIReader().Return(fc).AnyTimes()
	m.EXPECT().Elected().Return(elected).AnyTimes()
	m.EXPECT().Add(gomock.Any()).Return(nil).AnyTimes()
	m.EXPECT().AddMetricsServerExtraHandler(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.EXPECT().AddHealthzCheck(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.EXPECT().AddReadyzCheck(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.EXPECT().Start(gomock.Any()).Return(nil).AnyTimes()
	var nilWebhook webhook.Server
	m.EXPECT().GetWebhookServer().Return(nilWebhook).AnyTimes()

	return m
}

func createFakeManagerAndClient(t *testing.T, objs ...client.Object) (ctrl.Manager, client.Client) {
	t.Helper()
	gctl := gomock.NewController(t)
	t.Cleanup(func() { gctl.Finish() })
	fc := fakeClientForGatewayTests(objs...)
	c := mockcache.NewDelegating(fc)
	m := newGatewayGomockManager(t, gctl, fc, scheme, c, nil)
	return m, fc
}

// dnsRecordAssertKey joins normalized FQDN and record type for map keys in scenario assertions.
func dnsRecordAssertKey(fqdn, recordType string) string {
	return strings.ToLower(strings.TrimSpace(fqdn)) + "|" + strings.TrimSpace(recordType)
}

// assertOwnerDNSRecordTargets asserts in-memory DNSRecord store rows for owner kind/ns/name.
// want maps "fqdn|RecordType" -> target IPs (order-insensitive per key). nil want means expect no rows.
func assertOwnerDNSRecordTargets(t testing.TB, store *dns.RecordStore, kind, ns, name string, want map[string][]string) {
	recs := store.GetByOwnerResourceNamespacedName(kind, ns, name)
	got := make(map[string][]string)
	for _, r := range recs {
		if r == nil || r.Fqdn == nil || r.RecordType == nil {
			continue
		}
		k := dnsRecordAssertKey(*r.Fqdn, *r.RecordType)
		cp := append([]string(nil), r.RecordValues...)
		slices.Sort(cp)
		got[k] = cp
	}
	if len(want) == 0 {
		if len(got) != 0 {
			t.Errorf("expected no DNS rows for %s/%s/%s, got %#v", kind, ns, name, got)
		}
		return
	}
	if len(got) != len(want) {
		t.Errorf("owner %s/%s/%s: want %d records got %d: %#v", kind, ns, name, len(want), len(got), got)
		return
	}
	for k, wv := range want {
		gv, ok := got[k]
		if !ok {
			t.Errorf("owner %s/%s/%s: missing key %q (got %#v)", kind, ns, name, k, got)
			continue
		}
		slices.Sort(wv)
		if !slices.Equal(wv, gv) {
			t.Errorf("owner %s/%s/%s key %q: want targets %v got %v", kind, ns, name, k, wv, gv)
		}
	}
}

// expectedDNSRow is one semantic DNS assertion row (FQDN + RR type + targets).
type expectedDNSRow struct {
	FQDN    string
	Type    string // e.g. A, AAAA
	Targets []string
}

func wantMapFromRows(rows []expectedDNSRow) map[string][]string {
	if len(rows) == 0 {
		return nil
	}
	m := make(map[string][]string, len(rows))
	for _, r := range rows {
		m[dnsRecordAssertKey(r.FQDN, r.Type)] = append([]string(nil), r.Targets...)
	}
	return m
}
