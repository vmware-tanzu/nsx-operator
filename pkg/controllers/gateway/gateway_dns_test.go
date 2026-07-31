/* Copyright (c) 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	mockclient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"

	mockdns "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordprovider"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

// stubValidatedRows mimics ValidateEndpointsByZone for *.example.com under /zones/t.
func stubValidatedRows(eps []*extdns.Endpoint) ([]dns.EndpointRow, map[string]string, error) {
	const zpath = "/orgs/org1/projects/proj1/dns-services/dns1/zones/t"
	var rows []dns.EndpointRow
	for _, ep := range eps {
		if ep == nil {
			continue
		}
		dn := strings.ToLower(strings.TrimSpace(ep.DNSName))
		if !strings.HasSuffix(dn, ".example.com") {
			return nil, nil, fmt.Errorf("hostname %q does not match stub allowed domain", ep.DNSName)
		}
		rel := strings.TrimSuffix(dn, ".example.com")
		rel = strings.TrimPrefix(rel, ".")
		rows = append(rows, *dns.NewEndpointRow(ep, zpath, rel))
	}
	return rows, map[string]string{zpath: "example.com"}, nil
}

func TestReconcileGatewayDNS(t *testing.T) {
	ctx := context.Background()

	makeGw := func(ns, name, uid, hostname, ip string) *gatewayv1.Gateway {
		gw := &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID(uid)},
		}
		if hostname != "" {
			gw.Annotations = map[string]string{servicecommon.AnnotationDNSHostnameKey: hostname}
		}
		if ip != "" {
			gw.Status.Addresses = []gatewayv1.GatewayStatusAddress{{Type: ptrGatewayAddressType(gatewayv1.IPAddressType), Value: ip}}
		}
		return gw
	}

	tests := []struct {
		name      string
		gw        *gatewayv1.Gateway
		setupMock func(m *mockdns.MockDNSRecordProvider)
		wantErr   bool
	}{
		{
			name: "hostname_with_ip_publishes",
			gw:   makeGw("ns1", "gw", "u1", "app.example.com", "203.0.113.5"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ string, _ *dns.ResourceRef, eps []*extdns.Endpoint) ([]dns.EndpointRow, map[string]string, error) {
						rows, allowed, err := stubValidatedRows(eps)
						return rows, allowed, err
					}).Times(1)
				m.EXPECT().CreateOrUpdateRecords(gomock.Any(), gomock.Any()).Return(true, nil).Times(1)
			},
		},
		{
			name: "dnsZoneValidationError_deletesOutsideAllowed",
			gw:   makeGw("ns1", "gw", "u4", "app.example.com", "203.0.113.6"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				allowed := map[string]string{"/zones/t": "example.com"}
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, allowed, &dns.DNSZoneValidationError{Msg: "zone mismatch"}).Times(1)
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "ns1", "gw").Return(false, nil).Times(1)
			},
		},
		{
			name: "empty_targets",
			gw:   makeGw("ns1", "gw-empty-targets", "u9", "app.example.com", ""),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "ns1", "gw-empty-targets").Return(true, nil).Times(1)
			},
		},
		{
			name: "stale_dns_records_deletes",
			gw:   makeGw("ns1", "gw-stale", "u6", "app.example.com", "203.0.113.6"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, nil).Times(1)
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "ns1", "gw-stale").Return(true, nil).Times(1)
			},
		},
		{
			name: "dnsZoneValidationError_with_valid_rows_applies_error",
			gw:   makeGw("ns1", "gw", "u1", "valid.example.com,invalid.other.com", "203.0.113.5"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ns string, owner *dns.ResourceRef, eps []*extdns.Endpoint) ([]dns.EndpointRow, map[string]string, error) {
						var valid []*extdns.Endpoint
						for _, ep := range eps {
							if ep.DNSName == "valid.example.com" {
								valid = append(valid, ep)
							}
						}
						rows, allowed, _ := stubValidatedRows(valid)
						return rows, allowed, &dns.DNSZoneValidationError{}
					}).Times(1)
				m.EXPECT().CreateOrUpdateRecords(gomock.Any(), gomock.Any()).Return(false, fmt.Errorf("mock update error")).Times(1)
			},
			wantErr: true, // we return the update error
		},
		{
			name: "dnsZoneValidationError_deletesOutsideAllowed_error",
			gw:   makeGw("ns1", "gw", "u1", "app.example.com", "203.0.113.5"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ns string, owner *dns.ResourceRef, eps []*extdns.Endpoint) ([]dns.EndpointRow, map[string]string, error) {
						return nil, nil, &dns.DNSZoneValidationError{}
					}).Times(1)
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "ns1", "gw").Return(false, fmt.Errorf("mock delete error")).Times(1)
			},
			wantErr: true, // return delete error
		},
		{
			name: "create_or_update_dns_records_error",
			gw:   makeGw("ns1", "gw-create-err", "u8", "app.example.com", "203.0.113.6"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ string, _ *dns.ResourceRef, eps []*extdns.Endpoint) ([]dns.EndpointRow, map[string]string, error) {
						rows, allowed, err := stubValidatedRows(eps)
						return rows, allowed, err
					}).Times(1)
				m.EXPECT().CreateOrUpdateRecords(gomock.Any(), gomock.Any()).Return(false, fmt.Errorf("mock create error")).Times(1)
			},
			wantErr: true,
		},
		{
			name: "update_condition_error",
			gw:   makeGw("ns1", "gw", "u1", "app.example.com", "203.0.113.5"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ns string, owner *dns.ResourceRef, eps []*extdns.Endpoint) ([]dns.EndpointRow, map[string]string, error) {
						rows, allowed, err := stubValidatedRows(eps)
						return rows, allowed, err
					}).Times(1)
				m.EXPECT().CreateOrUpdateRecords(gomock.Any(), gomock.Any()).Return(true, nil).Times(1)
			},
		},
		{
			name: "dnsZoneValidationError_with_valid_rows_applies",
			gw:   makeGw("ns1", "gw", "u1", "valid.example.com,invalid.other.com", "203.0.113.5"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ns string, owner *dns.ResourceRef, eps []*extdns.Endpoint) ([]dns.EndpointRow, map[string]string, error) {
						var valid []*extdns.Endpoint
						for _, ep := range eps {
							if ep.DNSName == "valid.example.com" {
								valid = append(valid, ep)
							}
						}
						rows, allowed, _ := stubValidatedRows(valid)
						return rows, allowed, &dns.DNSZoneValidationError{}
					}).Times(1)
				m.EXPECT().CreateOrUpdateRecords(gomock.Any(), gomock.Any()).Return(true, nil).Times(1)
			},
		},
		{
			name: "no_ip_targets_deletes",
			gw:   makeGw("ns1", "gw", "u2", "app.example.com", ""),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "ns1", "gw").Return(true, nil).Times(1)
			},
		},
		{
			name: "no_annotation_skips",
			gw:   makeGw("ns1", "gw", "u3", "", "203.0.113.5"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindGateway, "ns1", "gw").Return(false, nil).Times(1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrlMock := gomock.NewController(t)
			t.Cleanup(func() { ctrlMock.Finish() })
			m := mockdns.NewMockDNSRecordProvider(ctrlMock)

			// Setup fake client
			s := apimachineryruntime.NewScheme()
			_ = gatewayv1.Install(s)
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(tt.gw).WithStatusSubresource(&gatewayv1.Gateway{}).Build()

			if tt.setupMock != nil {
				tt.setupMock(m)
			}
			r := &GatewayReconciler{
				Client: c,
				DNS:    m,
			}
			err := r.reconcileGatewayDNS(ctx, tt.gw)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type mockStatusWriter struct {
	err error
}

func (m *mockStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return m.err
}

func (m *mockStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return m.err
}

func (m *mockStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return m.err
}

func (m *mockStatusWriter) Apply(ctx context.Context, obj apimachineryruntime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return m.err
}

func TestClearDNSAndConditionForGateway(t *testing.T) {
	ctx := context.Background()
	nn := types.NamespacedName{Namespace: "default", Name: "gw1"}

	tests := []struct {
		name      string
		setupMock func(*mockclient.MockClient, *mockdns.MockDNSRecordProvider)
		wantErr   bool
	}{
		{
			name: "success",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider) {
				d.EXPECT().DeleteRecordByOwnerNN(ctx, dns.ResourceKindGateway, nn.Namespace, nn.Name).Return(true, nil)
				c.EXPECT().Get(ctx, nn, gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "gw1"))
			},
			wantErr: false,
		},
		{
			name: "delete-error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider) {
				d.EXPECT().DeleteRecordByOwnerNN(ctx, dns.ResourceKindGateway, nn.Namespace, nn.Name).Return(false, errors.New("delete error"))
			},
			wantErr: true,
		},
		{
			name: "update-status-error",
			setupMock: func(c *mockclient.MockClient, d *mockdns.MockDNSRecordProvider) {
				d.EXPECT().DeleteRecordByOwnerNN(ctx, dns.ResourceKindGateway, nn.Namespace, nn.Name).Return(true, nil)
				gw := &gatewayv1.Gateway{
					ObjectMeta: metav1.ObjectMeta{Namespace: nn.Namespace, Name: nn.Name},
					Status: gatewayv1.GatewayStatus{
						Conditions: []metav1.Condition{{Type: string(conditionTypeDNSRecordReady), Status: metav1.ConditionTrue}},
					},
				}
				c.EXPECT().Get(ctx, nn, gomock.Any()).DoAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
					gw.DeepCopyInto(obj.(*gatewayv1.Gateway))
					return nil
				})
				sw := &mockStatusWriter{err: errors.New("update error")}
				c.EXPECT().Status().Return(sw)
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrlMock := gomock.NewController(t)
			c := mockclient.NewMockClient(ctrlMock)
			d := mockdns.NewMockDNSRecordProvider(ctrlMock)
			r := &GatewayReconciler{Client: c, DNS: d}

			tc.setupMock(c, d)
			err := r.clearDNSAndConditionForGateway(ctx, nn, "delete")
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
