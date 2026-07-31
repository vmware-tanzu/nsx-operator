package gateway

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/config"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	mockclient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mockmanager "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/manager"
	mockdns "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordprovider"
	mockgateway "github.com/vmware-tanzu/nsx-operator/pkg/mock/gateway"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

func TestAllowedZonePathSet(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected []string
	}{
		{
			name:     "empty map",
			input:    map[string]string{},
			expected: nil,
		},
		{
			name:     "single element",
			input:    map[string]string{"zone1": "domain1"},
			expected: []string{"zone1"},
		},
		{
			name:     "multiple elements",
			input:    map[string]string{"zone1": "domain1", "zone2": "domain2"},
			expected: []string{"zone1", "zone2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := allowedZonePathSet(tc.input)
			assert.Equal(t, len(tc.expected), s.Len())
			for _, e := range tc.expected {
				assert.True(t, s.Has(e))
			}
		})
	}
}

func TestLoopObjectList(t *testing.T) {
	ctx := context.Background()
	fc := getFakeClient(&gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "default"},
	}, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route2", Namespace: "default"},
	})

	tests := []struct {
		name    string
		list    client.ObjectList
		wantErr bool
	}{
		{
			name:    "valid HTTPRouteList",
			list:    &gatewayv1.HTTPRouteList{},
			wantErr: false,
		},
		{
			name:    "nil list",
			list:    (*gatewayv1.HTTPRouteList)(nil),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			err := loopObjectList(ctx, fc, tc.list, func(r *gatewayv1.HTTPRoute) {
				count++
			})

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 2, count)
			}
		})
	}
}

func TestRouteObjectWrappers(t *testing.T) {
	tests := []struct {
		name    string
		obj     interface{}
		wrapper func() interface {
			GetObject() client.Object
			GetObjectMeta() *metav1.ObjectMeta
			GetSpecHostnames() []gatewayv1.Hostname
			GetParentRefs() []gatewayv1.ParentReference
			GetRouteParentStatus() []gatewayv1.RouteParentStatus
			SetRouteParentStatus([]gatewayv1.RouteParentStatus)
			GetResourceRef() *dns.ResourceRef
		}
		expectedName string
		expectedKind string
	}{
		{
			name: "HTTPRoute",
			wrapper: func() interface {
				GetObject() client.Object
				GetObjectMeta() *metav1.ObjectMeta
				GetSpecHostnames() []gatewayv1.Hostname
				GetParentRefs() []gatewayv1.ParentReference
				GetRouteParentStatus() []gatewayv1.RouteParentStatus
				SetRouteParentStatus([]gatewayv1.RouteParentStatus)
				GetResourceRef() *dns.ResourceRef
			} {
				return &HTTPRoute{
					HTTPRoute: gatewayv1.HTTPRoute{
						ObjectMeta: metav1.ObjectMeta{Name: "http"},
						Spec: gatewayv1.HTTPRouteSpec{
							Hostnames: []gatewayv1.Hostname{"http.example.com"},
							CommonRouteSpec: gatewayv1.CommonRouteSpec{
								ParentRefs: []gatewayv1.ParentReference{{Name: "gw"}},
							},
						},
						Status: gatewayv1.HTTPRouteStatus{
							RouteStatus: gatewayv1.RouteStatus{
								Parents: []gatewayv1.RouteParentStatus{{ParentRef: gatewayv1.ParentReference{Name: "gw"}}},
							},
						},
					},
				}
			},
			expectedName: "http",
			expectedKind: dns.ResourceKindHTTPRoute,
		},
		{
			name: "GRPCRoute",
			wrapper: func() interface {
				GetObject() client.Object
				GetObjectMeta() *metav1.ObjectMeta
				GetSpecHostnames() []gatewayv1.Hostname
				GetParentRefs() []gatewayv1.ParentReference
				GetRouteParentStatus() []gatewayv1.RouteParentStatus
				SetRouteParentStatus([]gatewayv1.RouteParentStatus)
				GetResourceRef() *dns.ResourceRef
			} {
				return &GRPCRoute{
					GRPCRoute: gatewayv1.GRPCRoute{
						ObjectMeta: metav1.ObjectMeta{Name: "grpc"},
						Spec: gatewayv1.GRPCRouteSpec{
							Hostnames: []gatewayv1.Hostname{"grpc.example.com"},
							CommonRouteSpec: gatewayv1.CommonRouteSpec{
								ParentRefs: []gatewayv1.ParentReference{{Name: "gw"}},
							},
						},
					},
				}
			},
			expectedName: "grpc",
			expectedKind: dns.ResourceKindGRPCRoute,
		},
		{
			name: "TLSRoute",
			wrapper: func() interface {
				GetObject() client.Object
				GetObjectMeta() *metav1.ObjectMeta
				GetSpecHostnames() []gatewayv1.Hostname
				GetParentRefs() []gatewayv1.ParentReference
				GetRouteParentStatus() []gatewayv1.RouteParentStatus
				SetRouteParentStatus([]gatewayv1.RouteParentStatus)
				GetResourceRef() *dns.ResourceRef
			} {
				return &TLSRoute{
					TLSRoute: gatewayv1.TLSRoute{
						ObjectMeta: metav1.ObjectMeta{Name: "tls"},
						Spec: gatewayv1.TLSRouteSpec{
							Hostnames: []gatewayv1.Hostname{"tls.example.com"},
							CommonRouteSpec: gatewayv1.CommonRouteSpec{
								ParentRefs: []gatewayv1.ParentReference{{Name: "gw"}},
							},
						},
					},
				}
			},
			expectedName: "tls",
			expectedKind: dns.ResourceKindTLSRoute,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.wrapper()

			assert.Equal(t, tc.expectedName, r.GetObject().GetName())
			assert.Equal(t, tc.expectedName, r.GetObjectMeta().Name)
			assert.Equal(t, 1, len(r.GetSpecHostnames()))
			assert.Equal(t, 1, len(r.GetParentRefs()))

			// check that RouteParentStatus starts at expected size
			if tc.name == "HTTPRoute" {
				assert.Equal(t, 1, len(r.GetRouteParentStatus()))
			} else {
				assert.Equal(t, 0, len(r.GetRouteParentStatus()))
			}

			// test setting empty/nil
			r.SetRouteParentStatus(nil)
			assert.Equal(t, 0, len(r.GetRouteParentStatus()))

			// test setting value
			r.SetRouteParentStatus([]gatewayv1.RouteParentStatus{{}})
			assert.Equal(t, 1, len(r.GetRouteParentStatus()))

			resRef := r.GetResourceRef()
			assert.Equal(t, tc.expectedName, resRef.GetName())
			assert.Equal(t, tc.expectedKind, resRef.Kind)
		})
	}
}

func getTestScheme() *apimachineryruntime.Scheme {
	s := apimachineryruntime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = gatewayv1.Install(s)
	_ = v1alpha1.AddToScheme(s)
	return s
}

func getFakeClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(getTestScheme()).
		WithIndex(&gatewayv1.HTTPRoute{}, routeParentGatewayIndex, (&genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]{}).routeParentGatewayIndexFunc).
		WithIndex(&gatewayv1.GRPCRoute{}, routeParentGatewayIndex, (&genericRouteReconciler[*GRPCRoute, gatewayv1.GRPCRoute, *gatewayv1.GRPCRoute]{}).routeParentGatewayIndexFunc).
		WithIndex(&gatewayv1.TLSRoute{}, routeParentGatewayIndex, (&genericRouteReconciler[*TLSRoute, gatewayv1.TLSRoute, *gatewayv1.TLSRoute]{}).routeParentGatewayIndexFunc).
		WithIndex(&gatewayv1.HTTPRoute{}, routeParentListenerSetIndex, (&genericRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute]{}).routeParentListenerSetIndexFunc).
		WithIndex(&gatewayv1.GRPCRoute{}, routeParentListenerSetIndex, (&genericRouteReconciler[*GRPCRoute, gatewayv1.GRPCRoute, *gatewayv1.GRPCRoute]{}).routeParentListenerSetIndexFunc).
		WithIndex(&gatewayv1.TLSRoute{}, routeParentListenerSetIndex, (&genericRouteReconciler[*TLSRoute, gatewayv1.TLSRoute, *gatewayv1.TLSRoute]{}).routeParentListenerSetIndexFunc).
		WithIndex(&gatewayv1.ListenerSet{}, listenerSetParentGatewayIndex, listenerSetParentGatewayIndexFunc).
		WithObjects(objs...).
		Build()
}

func setupMockManager(ctrlMock *gomock.Controller, c client.Client) *mockmanager.MockManager {
	mgr := mockmanager.NewMockManager(ctrlMock)
	if c != nil {
		mgr.EXPECT().GetClient().Return(c).AnyTimes()
	}
	s := apimachineryruntime.NewScheme()
	_ = gatewayv1.Install(s)
	mgr.EXPECT().GetScheme().Return(s).AnyTimes()
	mgr.EXPECT().GetConfig().Return(&rest.Config{}).AnyTimes()
	mgr.EXPECT().GetEventRecorderFor(gomock.Any()).Return(record.NewFakeRecorder(100)).AnyTimes()
	mgr.EXPECT().Add(gomock.Any()).Return(nil).AnyTimes()
	mgr.EXPECT().GetControllerOptions().Return(config.Controller{}).AnyTimes()
	mgr.EXPECT().GetLogger().Return(logr.Discard()).AnyTimes()
	mgr.EXPECT().GetCache().Return(nil).AnyTimes()

	return mgr
}
func setupMockStatusUpdater(ctrlMock *gomock.Controller) *mockgateway.MockStatusUpdater {
	su := mockgateway.NewMockStatusUpdater(ctrlMock)
	su.EXPECT().IncreaseSyncTotal().AnyTimes()
	su.EXPECT().IncreaseUpdateTotal().AnyTimes()
	su.EXPECT().IncreaseDeleteTotal().AnyTimes()
	su.EXPECT().IncreaseDeleteSuccessTotal().AnyTimes()
	su.EXPECT().IncreaseDeleteFailTotal().AnyTimes()
	su.EXPECT().UpdateSuccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	su.EXPECT().UpdateFail(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	su.EXPECT().DeleteSuccess(gomock.Any(), gomock.Any()).AnyTimes()
	su.EXPECT().DeleteFail(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	return su
}

// testEnv contains common initialized variables for tests to reduce boilerplate.
type testEnv struct {
	ctx        context.Context
	ctrl       *gomock.Controller
	client     *mockclient.MockClient
	dns        *mockdns.MockDNSRecordProvider
	reconciler *GatewayReconciler
}

// setupTestEnv initializes a common test environment with a GatewayReconciler and mock dependencies.
func setupTestEnv(t *testing.T) *testEnv {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	c := mockclient.NewMockClient(ctrl)
	d := mockdns.NewMockDNSRecordProvider(ctrl)

	r := &GatewayReconciler{
		Client: c,
		DNS:    d,
		apiResources: gatewayAPIResources{
			gatewayEnabled:     true,
			listenerSetEnabled: true,
		},
		ipCache:       NewGatewayIPCache(),
		StatusUpdater: setupMockStatusUpdater(ctrl),
	}

	return &testEnv{
		ctx:        ctx,
		ctrl:       ctrl,
		client:     c,
		dns:        d,
		reconciler: r,
	}
}
