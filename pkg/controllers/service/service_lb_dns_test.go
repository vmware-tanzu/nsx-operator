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
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

func TestParseDNSHostnamesFromServiceAnnotation_commaSeparated(t *testing.T) {
	ann := map[string]string{
		servicecommon.AnnotationDNSHostnameKey: "a.example.com, b.example.com ",
	}
	got := parseDNSHostnamesFromServiceAnnotation(ann)
	require.Len(t, got, 2)
	assert.Equal(t, "a.example.com", got[0])
	assert.Equal(t, "b.example.com", got[1])
}

func TestTargetsFromLoadBalancerIngress(t *testing.T) {
	got := targetsFromLoadBalancerIngress([]v1.LoadBalancerIngress{
		{IP: "10.0.0.1"},
		{Hostname: "lb.vendor.example"},
	})
	assert.ElementsMatch(t, []string{"10.0.0.1", "lb.vendor.example"}, []string(got))
}

// stubValidatedRows mimics ValidateEndpointsByDNSZone for *.example.com under /zones/t (same stub domain as dns.getDNSZones).
func stubValidatedRows(eps []*extdns.Endpoint) ([]dns.EndpointRow, error) {
	const zpath = "/zones/t"
	const proj = "/orgs/org1/projects/proj1/vpcs/vpc1"
	var rows []dns.EndpointRow
	for _, ep := range eps {
		if ep == nil {
			continue
		}
		dn := strings.ToLower(strings.TrimSpace(ep.DNSName))
		if !strings.HasSuffix(dn, ".example.com") {
			return nil, fmt.Errorf("hostname %q does not match stub allowed domain", ep.DNSName)
		}
		rel := strings.TrimSuffix(dn, ".example.com")
		rel = strings.TrimPrefix(rel, ".")
		rows = append(rows, *dns.NewEndpointRow(ep, zpath, proj, rel))
	}
	return rows, nil
}

func TestBuildLoadBalancerServiceDNSBatch(t *testing.T) {
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "svc1",
			UID:       types.UID("uid-1"),
			Annotations: map[string]string{
				servicecommon.AnnotationDNSHostnameKey: "h1.example.com,h2.example.com",
			},
		},
		Status: v1.ServiceStatus{
			LoadBalancer: v1.LoadBalancerStatus{
				Ingress: []v1.LoadBalancerIngress{{IP: "192.0.2.1"}},
			},
		},
	}
	w := &spyDNSWriter{}
	batch, err := buildLoadBalancerServiceDNSBatch(svc, w)
	require.NoError(t, err)
	require.NotNil(t, batch)
	require.Equal(t, dns.ResourceKindService, batch.Owner.Kind)
	require.Len(t, batch.Rows, 2)
	var names []string
	for _, row := range batch.Rows {
		names = append(names, row.DNSName)
		assert.Contains(t, row.Targets, "192.0.2.1")
		assert.Equal(t, extdns.RecordTypeA, row.RecordType)
	}
	assert.ElementsMatch(t, []string{"h1.example.com", "h2.example.com"}, names)
}

type spyDNSWriter struct {
	createCalls int
	deleteCalls int
	lastBatch   *dns.AggregatedDNSEndponts
}

func (s *spyDNSWriter) ValidateEndpointsByDNSZone(_ string, _ *dns.ResourceRef, eps []*extdns.Endpoint) ([]dns.EndpointRow, error) {
	return stubValidatedRows(eps)
}

func (s *spyDNSWriter) CreateOrUpdateDNSRecords(_ context.Context, batch *dns.AggregatedDNSEndponts) (bool, error) {
	s.createCalls++
	s.lastBatch = batch
	return true, nil
}

func (s *spyDNSWriter) DeleteDNSRecordByOwnerNN(context.Context, string, string, string) (bool, error) {
	s.deleteCalls++
	return true, nil
}

func TestServiceLbReconciler_reconcileLoadBalancerServiceDNS(t *testing.T) {
	ctx := context.Background()
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "lb",
			UID:       "u1",
			Annotations: map[string]string{
				servicecommon.AnnotationDNSHostnameKey: "app.example.com",
			},
		},
		Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
		Status: v1.ServiceStatus{
			LoadBalancer: v1.LoadBalancerStatus{
				Ingress: []v1.LoadBalancerIngress{{IP: "203.0.113.5"}},
			},
		},
	}
	scheme := schemeForServiceTests(t)
	r := &ServiceLbReconciler{
		Client:    fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc).Build(),
		DNSWriter: &spyDNSWriter{},
	}
	published, err := r.reconcileLoadBalancerServiceDNS(ctx, svc)
	require.NoError(t, err)
	assert.True(t, published)
	spy := r.DNSWriter.(*spyDNSWriter)
	assert.Equal(t, 1, spy.createCalls)
	assert.Equal(t, 0, spy.deleteCalls)
	require.NotNil(t, spy.lastBatch)
	require.Len(t, spy.lastBatch.Rows, 1)
}

func TestServiceLbReconciler_reconcileLoadBalancerServiceDNS_deletesWhenNoTargets(t *testing.T) {
	ctx := context.Background()
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "lb",
			Annotations: map[string]string{
				servicecommon.AnnotationDNSHostnameKey: "app.example.com",
			},
		},
		Spec:   v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
		Status: v1.ServiceStatus{},
	}
	scheme := schemeForServiceTests(t)
	r := &ServiceLbReconciler{
		Client:    fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc).Build(),
		DNSWriter: &spyDNSWriter{},
	}
	published, err := r.reconcileLoadBalancerServiceDNS(ctx, svc)
	require.NoError(t, err)
	assert.False(t, published)
	spy := r.DNSWriter.(*spyDNSWriter)
	assert.Equal(t, 0, spy.createCalls)
	assert.Equal(t, 1, spy.deleteCalls)
}

func TestServiceLbReconciler_Reconcile_dnsOnDelete(t *testing.T) {
	ctx := context.Background()
	spy := &spyDNSWriter{}
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	r := &ServiceLbReconciler{
		Client:    fake.NewClientBuilder().WithScheme(scheme).Build(),
		DNSWriter: spy,
		Recorder:  fakeRecorder{},
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "gone"}}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 1, spy.deleteCalls)
}

func schemeForServiceTests(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(s))
	return s
}

func TestServiceLbReconciler_updateServiceDNSReadyCondition(t *testing.T) {
	ctx := context.Background()
	scheme := schemeForServiceTests(t)
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "lb", Generation: 3},
		Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
	}
	r := &ServiceLbReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1.Service{}).WithObjects(svc).Build(),
	}
	nn := types.NamespacedName{Namespace: "ns1", Name: "lb"}
	require.NoError(t, r.updateServiceDNSReadyCondition(ctx, nn, fmt.Errorf("zone validation failed")))
	got := &v1.Service{}
	require.NoError(t, r.Client.Get(ctx, nn, got))
	c := meta.FindStatusCondition(got.Status.Conditions, serviceDNSReadyConditionType)
	require.NotNil(t, c)
	assert.Equal(t, metav1.ConditionFalse, c.Status)
	assert.Equal(t, reasonServiceDNSRecordFailed, c.Reason)
	assert.Contains(t, c.Message, "zone validation failed")
	assert.Equal(t, int64(3), c.ObservedGeneration)

	require.NoError(t, r.updateServiceDNSReadyCondition(ctx, nn, nil))
	require.NoError(t, r.Client.Get(ctx, nn, got))
	c = meta.FindStatusCondition(got.Status.Conditions, serviceDNSReadyConditionType)
	require.NotNil(t, c)
	assert.Equal(t, metav1.ConditionTrue, c.Status)
	assert.Equal(t, reasonServiceDNSRecordConfigured, c.Reason)
}

func TestServiceLbReconciler_removeServiceDNSReadyCondition(t *testing.T) {
	ctx := context.Background()
	scheme := schemeForServiceTests(t)
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "lb"},
		Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
		Status: v1.ServiceStatus{
			Conditions: []metav1.Condition{{
				Type: serviceDNSReadyConditionType, Status: metav1.ConditionTrue, Reason: reasonServiceDNSRecordConfigured,
			}},
		},
	}
	r := &ServiceLbReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1.Service{}).WithObjects(svc).Build(),
	}
	nn := types.NamespacedName{Namespace: "ns1", Name: "lb"}
	require.NoError(t, r.removeServiceDNSReadyCondition(ctx, nn))
	got := &v1.Service{}
	require.NoError(t, r.Client.Get(ctx, nn, got))
	assert.Nil(t, meta.FindStatusCondition(got.Status.Conditions, serviceDNSReadyConditionType))
}
