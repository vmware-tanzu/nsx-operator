/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

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

func TestTargetsFromLoadBalancerIngress_table(t *testing.T) {
	tests := []struct {
		name    string
		ingress []v1.LoadBalancerIngress
		want    []string
	}{
		{
			name: "ip_and_hostname",
			ingress: []v1.LoadBalancerIngress{
				{IP: "10.0.0.1"},
				{Hostname: "lb.vendor.example"},
			},
			want: []string{"10.0.0.1", "lb.vendor.example"},
		},
		{
			name:    "empty",
			ingress: nil,
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := targetsFromLoadBalancerIngress(tt.ingress)
			if tt.want == nil {
				require.Empty(t, got)
				return
			}
			assert.ElementsMatch(t, tt.want, []string(got))
		})
	}
}

func TestReconcileLoadBalancerServiceDNS(t *testing.T) {
	ctx := context.Background()
	scheme := serviceLbTestScheme(t)

	makeLbSvc := func(ns, name, uid, hostname, ip string) *v1.Service {
		svc := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID(uid)},
			Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
		}
		if hostname != "" {
			svc.Annotations = map[string]string{servicecommon.AnnotationDNSHostnameKey: hostname}
		}
		if ip != "" {
			svc.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{{IP: ip}}
		}
		return svc
	}

	tests := []struct {
		name      string
		svc       *v1.Service
		setupMock func(m *mockdns.MockDNSRecordProvider)
		wantErr   bool
	}{
		{
			name: "hostname_with_ip_publishes",
			svc:  makeLbSvc("ns1", "lb", "u1", "app.example.com", "203.0.113.5"),
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
			svc:  makeLbSvc("ns1", "lb", "u4", "app.example.com", "203.0.113.6"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				allowed := map[string]string{"/zones/t": "example.com"}
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, allowed, &dns.DNSZoneValidationError{Msg: "zone mismatch"}).Times(1)
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "lb").Return(false, nil).Times(1)
			},
		},
		{
			name: "empty_targets",
			svc:  makeLbSvc("ns1", "lb-empty-targets", "u9", "app.example.com", ""),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				// No interactions expected, just returns nil, nil, nil internally
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "lb-empty-targets").Return(true, nil).Times(1)
			},
		},
		{
			name: "dnsZoneValidationError_deletesOutsideAllowed_error",
			svc:  makeLbSvc("ns1", "lb-err2", "u5", "app.example.com", "203.0.113.6"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				allowed := map[string]string{"/zones/t": "example.com"}
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, allowed, &dns.DNSZoneValidationError{Msg: "zone mismatch"}).Times(1)
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "lb-err2").Return(false, fmt.Errorf("mock delete error")).Times(1)
			},
			wantErr: true,
		},
		{
			name: "dnsZoneValidationError_updateCondition_error",
			svc:  makeLbSvc("ns1", "lb-err-cond", "u5-cond", "app.example.com", "203.0.113.6"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				allowed := map[string]string{"/zones/t": "example.com"}
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, allowed, &dns.DNSZoneValidationError{Msg: "zone mismatch"}).Times(1)
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "lb-err-cond").Return(false, fmt.Errorf("mock delete error")).Times(1)
			},
			wantErr: true,
		},
		{
			name: "validateEndpoints_error_updateCondition_error",
			svc:  makeLbSvc("ns1", "lb-val-cond-err", "u11", "app.example.com", "203.0.113.6"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, fmt.Errorf("mock val error")).Times(1)
			},
			wantErr: true,
		},
		{
			name: "stale_dns_records_deletes",
			svc:  makeLbSvc("ns1", "lb-stale", "u6", "app.example.com", "203.0.113.6"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, nil).Times(1)
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "lb-stale").Return(true, nil).Times(1)
			},
		},
		{
			name: "stale_dns_records_deletes_error",
			svc:  makeLbSvc("ns1", "lb-stale-err", "u7", "app.example.com", "203.0.113.6"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, nil).Times(1)
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "lb-stale-err").Return(false, fmt.Errorf("mock error")).Times(1)
			},
			wantErr: true,
		},
		{
			name: "create_or_update_dns_records_error",
			svc:  makeLbSvc("ns1", "lb-create-err", "u8", "app.example.com", "203.0.113.6"),
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
			name: "validate_endpoints_error",
			svc:  makeLbSvc("ns1", "lb-val-err", "u10", "app.example.com", "203.0.113.6"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, fmt.Errorf("mock val error")).Times(1)
			},
			wantErr: true,
		},
		{
			name: "targetsFromLoadBalancerIngress_empty_skip_batch",
			svc: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1", Name: "lb-no-target", UID: "u11",
					Annotations: map[string]string{servicecommon.AnnotationDNSHostnameKey: "a.com"},
				},
				Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
				Status: v1.ServiceStatus{
					LoadBalancer: v1.LoadBalancerStatus{Ingress: []v1.LoadBalancerIngress{}}, // empty targets
				},
			},
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "lb-no-target").Return(true, nil).Times(1)
			},
		},
		{
			name: "no_ip_targets_deletes",
			svc:  makeLbSvc("ns1", "lb", "u2", "app.example.com", ""),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "lb").Return(true, nil).Times(1)
			},
		},
		{
			name: "no_annotation_skips",
			svc:  makeLbSvc("ns1", "lb", "u3", "", "203.0.113.5"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "lb").Return(false, nil).Times(1)
			},
		},
		{
			name: "invalid_annotation_extra_commas_and_spaces",
			svc:  makeLbSvc("ns1", "lb", "u4", "  app.example.com  , ,  ", "203.0.113.5"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				allowed := map[string]string{"/zones/t": "example.com"}
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(namespace string, owner *dns.ResourceRef, eps []*extdns.Endpoint) ([]dns.EndpointRow, []*extdns.Endpoint, map[string]string, error) {
						require.Len(t, eps, 3)
						require.Equal(t, "app.example.com", eps[0].DNSName)
						require.Equal(t, "", eps[1].DNSName)
						require.Equal(t, "", eps[2].DNSName)
						return []dns.EndpointRow{*dns.NewEndpointRow(&extdns.Endpoint{DNSName: "app.example.com", Targets: []string{"203.0.113.5"}, RecordType: "A"}, "/zones/t", "app")}, nil, allowed, nil
					}).Times(1)
				m.EXPECT().CreateOrUpdateRecords(gomock.Any(), gomock.Any()).Return(true, nil).Times(1)
			},
		},
		{
			name: "invalid_annotation_wildcard",
			svc:  makeLbSvc("ns1", "lb", "u5", "*.example.com", "203.0.113.5"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				allowed := map[string]string{"/zones/t": "example.com"}
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(namespace string, owner *dns.ResourceRef, eps []*extdns.Endpoint) ([]dns.EndpointRow, map[string]string, error) {
						require.Len(t, eps, 1)
						require.Equal(t, "*.example.com", eps[0].DNSName)
						return nil, allowed, nil
					}).Times(1)
				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "lb").Return(false, nil).Times(1)
			},
		},
		{
			name: "invalid_annotation_empty_hostname",
			svc:  makeLbSvc("ns1", "lb", "u6", " , ", "203.0.113.5"),
			setupMock: func(m *mockdns.MockDNSRecordProvider) {
				allowed := map[string]string{"/zones/t": "example.com"}
				m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, allowed, &dns.DNSZoneValidationError{Msg: "empty hostname"}).Times(1)

				m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "lb").Return(false, nil).Times(1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCtl := gomock.NewController(t)
			t.Cleanup(func() { mockCtl.Finish() })
			m := mockdns.NewMockDNSRecordProvider(mockCtl)
			assignDNSListStubs(m)
			tt.setupMock(m)
			r := &ServiceLbReconciler{
				Client: serviceLbFakeClient(scheme, false, tt.svc),
				DNS:    m,
			}
			err := r.reconcileLoadBalancerServiceDNS(ctx, tt.svc)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestServiceLbReconciler_Reconcile_dnsOnDelete(t *testing.T) {
	ctx := context.Background()
	scheme := serviceLbTestScheme(t)
	mockCtl := gomock.NewController(t)
	t.Cleanup(func() { mockCtl.Finish() })
	m := mockdns.NewMockDNSRecordProvider(mockCtl)
	assignDNSListStubs(m)
	m.EXPECT().DeleteRecordByOwnerNN(gomock.Any(), dns.ResourceKindService, "ns1", "gone").Return(false, nil).Times(1)
	r := &ServiceLbReconciler{
		Client:   serviceLbFakeClient(scheme, false),
		DNS:      m,
		Recorder: fakeRecorder{},
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "gone"}})
	require.NoError(t, err)
}

// TestReconcileLoadBalancerServiceDNS_nonZoneValidationErrUpdatesCondition verifies that when
// buildLoadBalancerServiceDNSBatch returns a plain (non-DNSZoneValidationError) error, the
// reconciler still sets the Ready condition to False/DNSRecordFailed and returns the error.
func TestReconcileLoadBalancerServiceDNS_nonZoneValidationErrUpdatesCondition(t *testing.T) {
	ctx := context.Background()
	scheme := serviceLbTestScheme(t)
	mockCtl := gomock.NewController(t)
	t.Cleanup(func() { mockCtl.Finish() })
	m := mockdns.NewMockDNSRecordProvider(mockCtl)
	assignDNSListStubs(m)

	infraErr := fmt.Errorf("vpc service unavailable")
	m.EXPECT().ValidateEndpointsByZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil, infraErr).Times(1)

	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "ns1",
			Name:        "lb",
			UID:         "u-infra",
			Annotations: map[string]string{servicecommon.AnnotationDNSHostnameKey: "app.example.com"},
			Generation:  2,
		},
		Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
		Status: v1.ServiceStatus{
			LoadBalancer: v1.LoadBalancerStatus{Ingress: []v1.LoadBalancerIngress{{IP: "203.0.113.5"}}},
		},
	}
	r := &ServiceLbReconciler{
		Client: serviceLbFakeClient(scheme, true, svc),
		DNS:    m,
	}
	err := r.reconcileLoadBalancerServiceDNS(ctx, svc)
	require.ErrorIs(t, err, infraErr)

	got := &v1.Service{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Namespace: "ns1", Name: "lb"}, got))
	c := meta.FindStatusCondition(got.Status.Conditions, serviceDNSReadyConditionType)
	require.NotNil(t, c, "Ready condition must be set even for non-DNSZoneValidationError")
	assert.Equal(t, metav1.ConditionFalse, c.Status)
	assert.Equal(t, reasonServiceDNSRecordFailed, c.Reason)
	assert.Contains(t, c.Message, "vpc service unavailable")
}

func TestServiceLbReconciler_updateServiceDNSReadyCondition_table(t *testing.T) {
	ctx := context.Background()
	scheme := serviceLbTestScheme(t)

	tests := []struct {
		name       string
		err        error
		wantStatus metav1.ConditionStatus
		wantReason string
		msgSub     string
	}{
		{
			name:       "success_sets_true",
			err:        nil,
			wantStatus: metav1.ConditionTrue,
			wantReason: reasonServiceDNSRecordConfigured,
		},
		{
			name:       "error_sets_false",
			err:        fmt.Errorf("zone validation failed"),
			wantStatus: metav1.ConditionFalse,
			wantReason: reasonServiceDNSRecordFailed,
			msgSub:     "zone validation failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &v1.Service{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "lb", Generation: 3},
				Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
			}
			r := &ServiceLbReconciler{Client: serviceLbFakeClient(scheme, true, svc)}
			nn := types.NamespacedName{Namespace: "ns1", Name: "lb"}
			require.NoError(t, r.updateServiceDNSReadyCondition(ctx, nn, tt.err))
			got := &v1.Service{}
			require.NoError(t, r.Client.Get(ctx, nn, got))
			c := meta.FindStatusCondition(got.Status.Conditions, serviceDNSReadyConditionType)
			require.NotNil(t, c)
			assert.Equal(t, tt.wantStatus, c.Status)
			assert.Equal(t, tt.wantReason, c.Reason)
			if tt.msgSub != "" {
				assert.Contains(t, c.Message, tt.msgSub)
			}
			assert.Equal(t, int64(3), c.ObservedGeneration)
		})
	}
}

func TestServiceLbReconciler_removeServiceDNSReadyCondition(t *testing.T) {
	ctx := context.Background()
	scheme := serviceLbTestScheme(t)
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "lb"},
		Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
		Status: v1.ServiceStatus{
			Conditions: []metav1.Condition{{
				Type: serviceDNSReadyConditionType, Status: metav1.ConditionTrue, Reason: reasonServiceDNSRecordConfigured,
			}},
		},
	}
	r := &ServiceLbReconciler{Client: serviceLbFakeClient(scheme, true, svc)}
	nn := types.NamespacedName{Namespace: "ns1", Name: "lb"}
	require.NoError(t, r.removeServiceDNSReadyCondition(ctx, nn))
	got := &v1.Service{}
	require.NoError(t, r.Client.Get(ctx, nn, got))
	assert.Nil(t, meta.FindStatusCondition(got.Status.Conditions, serviceDNSReadyConditionType))
}
