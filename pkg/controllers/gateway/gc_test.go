package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	mockclient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mockdns "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordprovider"
)

func TestCollectGarbage(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		gatewayEnabled bool
		setupMock      func(*mockclient.MockClient, *mockdns.MockDNSRecordProvider, *GatewayReconciler)
		wantErr        bool
	}{
		{
			name:           "success",
			gatewayEnabled: true,
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, r *GatewayReconciler) {
				c.EXPECT().List(ctx, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				c.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				d.EXPECT().ListRecordOwnerResource().Return(map[string]sets.Set[types.NamespacedName]{
					"Gateway": sets.New(types.NamespacedName{Namespace: "ns", Name: "gw1"}),
				}).AnyTimes()
				d.EXPECT().ListReferredGatewayNN().Return(sets.New(types.NamespacedName{Namespace: "ns", Name: "gw1"})).AnyTimes()
				d.EXPECT().DeleteRecordByOwnerNN(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
			},
			wantErr: false,
		},
		{
			name:           "gateway-disabled",
			gatewayEnabled: false,
			setupMock:      func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, r *GatewayReconciler) {},
			wantErr:        false,
		},
		{
			name:           "list-gateway-error",
			gatewayEnabled: true,
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, r *GatewayReconciler) {
				c.EXPECT().List(ctx, gomock.Any(), gomock.Any()).Return(assert.AnError).Times(1)
			},
			wantErr: true,
		},
		{
			name:           "list-route-error",
			gatewayEnabled: true,
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider, r *GatewayReconciler) {
				rr := newRouteReconciler[*HTTPRoute, gatewayv1.HTTPRoute, *gatewayv1.HTTPRoute](
					r, "HTTPRoute", newHTTPRoute, func() client.ObjectList { return &gatewayv1.HTTPRouteList{} })
				r.apiResources.routeReconcilers = []routeReconciler{rr}

				// Return nil for Gateway list, then error for Route list
				c.EXPECT().List(ctx, gomock.AssignableToTypeOf(&gatewayv1.GatewayList{}), gomock.Any()).Return(nil).Times(1)
				c.EXPECT().List(ctx, gomock.AssignableToTypeOf(&gatewayv1.HTTPRouteList{}), gomock.Any()).Return(assert.AnError).Times(1)

				d.EXPECT().ListRecordOwnerResource().Return(nil).AnyTimes()
				d.EXPECT().ListReferredGatewayNN().Return(nil).AnyTimes()
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTestEnv(t)
			r := env.reconciler
			r.apiResources.gatewayEnabled = tc.gatewayEnabled
			r.apiResources.listenerSetEnabled = tc.gatewayEnabled
			r.StatusUpdater = setupMockStatusUpdater(env.ctrl)

			tc.setupMock(env.client, env.dns, r)

			err := r.CollectGarbage(env.ctx)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
