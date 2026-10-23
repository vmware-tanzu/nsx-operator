/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dnsrecord

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctlcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	orgrootmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

func TestDNSRecordReadyStatusFailureRetries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		recordName string
		recordType v1alpha1.DNSRecordType
		wantReady  metav1.ConditionStatus
	}{
		{"ready true", "api", v1alpha1.DNSRecordTypeA, metav1.ConditionTrue},
		{"unsupported type", "api", "CNAME", metav1.ConditionFalse},
		{"wildcard", "*", v1alpha1.DNSRecordTypeA, metav1.ConditionFalse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			obj := &v1alpha1.DNSRecord{
				ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default"},
				Spec: v1alpha1.DNSRecordSpec{
					DomainName: "example.com", RecordName: tc.recordName,
					RecordType: tc.recordType, RecordValues: []string{"192.0.2.1"},
				},
				Status: v1alpha1.DNSRecordStatus{FQDN: calculateFQDN(tc.recordName, "example.com")},
			}
			statusErr := errors.New("status update unavailable")
			attempts := 0
			c := fake.NewClientBuilder().WithScheme(setupScheme()).
				WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						attempts++
						if attempts == 1 {
							return statusErr
						}
						return c.SubResource(subresource).Update(ctx, obj, opts...)
					},
				}).Build()
			r := &DNSRecordReconciler{
				Client:        c,
				StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
			}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
			result, err := r.Reconcile(ctx, req)
			require.ErrorIs(t, err, statusErr)
			require.Equal(t, ResultRequeue, result)

			result, err = r.Reconcile(ctx, req)
			require.NoError(t, err)
			require.Equal(t, ResultNormal, result)
			require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
			condition := meta.FindStatusCondition(obj.Status.Conditions, string(v1alpha1.Ready))
			require.NotNil(t, condition)
			require.Equal(t, tc.wantReady, condition.Status)
			require.Equal(t, 2, attempts)
		})
	}
}

func TestDNSRecordWildcardPreservesExistingRecord(t *testing.T) {
	ctx := context.Background()
	obj := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default"},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName: "example.com", RecordName: "*",
			RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"},
		},
		Status: v1alpha1.DNSRecordStatus{FQDN: "api.example.com"},
	}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj).Build()
	store := dns.BuildDNSRecordStore()
	existing := &model.DnsRecord{
		Path: servicecommon.String("/orgs/o/projects/p/dns-records/api_z_a"),
		Tags: []model.Tag{
			{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordFor), Tag: servicecommon.String(servicecommon.TagValueDNSRecordForDNSRecord)},
			{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordOwnerNamespace), Tag: servicecommon.String(obj.Namespace)},
			{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordOwnerName), Tag: servicecommon.String(obj.Name)},
		},
	}
	require.NoError(t, store.Add(existing))
	r := &DNSRecordReconciler{
		Client: c,
		// Unconfigured dependencies ensure invalid input cannot reach NSX processing.
		Service:       &dns.DNSRecordService{DNSRecordStore: store},
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	condition := meta.FindStatusCondition(obj.Status.Conditions, string(v1alpha1.Ready))
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Contains(t, condition.Message, "wildcard DNS names are not supported")
	require.Equal(t, []*model.DnsRecord{existing}, store.ListDNSRecords())
}

func TestDNSRecordInvalidTTLIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		ttl  int32
	}{
		{"negative", -1},
		{"zero", 0},
		{"below minimum", 29},
		{"above maximum", 86401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			obj := &v1alpha1.DNSRecord{
				ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default"},
				Spec: v1alpha1.DNSRecordSpec{
					DomainName: "example.com", RecordName: "api",
					RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"},
					TTL: &tc.ttl,
				},
			}
			c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj).Build()
			r := &DNSRecordReconciler{
				Client: c,
				// Invalid TTL must be rejected before invoking any NSX dependency.
				Service:       &dns.DNSRecordService{},
				StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
			}
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
			require.NoError(t, err)
			require.Equal(t, ResultNormal, result)
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
			condition := meta.FindStatusCondition(obj.Status.Conditions, string(v1alpha1.Ready))
			require.NotNil(t, condition)
			require.Equal(t, metav1.ConditionFalse, condition.Status)
			require.Contains(t, condition.Message, "TTL must be between 30 and 86400")
			require.Equal(t, tc.ttl, *obj.Spec.TTL, "invalid TTL must not be silently rewritten")
		})
	}
}

func TestDNSRecordGCReportsAllDeleteFailures(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(setupScheme()).Build()
	store := dns.BuildDNSRecordStore()
	for _, name := range []string{"gone1", "gone2"} {
		rec := &model.DnsRecord{
			Id:         servicecommon.String(name),
			Path:       servicecommon.String("/orgs/o/projects/p/dns-records/" + name),
			RecordType: servicecommon.String("A"),
			ZonePath:   servicecommon.String("/orgs/o/projects/p/dns-services/s/zones/z"),
			Tags: []model.Tag{
				{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordFor), Tag: servicecommon.String(servicecommon.TagValueDNSRecordForDNSRecord)},
				{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordOwnerNamespace), Tag: servicecommon.String("default")},
				{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordOwnerName), Tag: servicecommon.String(name)},
			},
		}
		require.NoError(t, store.Add(rec))
	}
	patchClient := orgrootmocks.NewMockOrgRootClient(gomock.NewController(t))
	patchClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(errors.New("NSX unavailable")).Times(2)
	builder, err := servicecommon.PolicyPathDnsRecord.NewPolicyTreeBuilder()
	require.NoError(t, err)
	r := &DNSRecordReconciler{
		Client: c,
		Service: &dns.DNSRecordService{
			Service:          servicecommon.Service{NSXClient: &nsx.Client{OrgRootClient: patchClient}},
			DNSRecordStore:   store,
			DnsRecordBuilder: builder,
		},
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	err = r.CollectGarbage(context.Background())
	require.ErrorContains(t, err, "default/gone1")
	require.ErrorContains(t, err, "default/gone2")
	require.ErrorContains(t, err, "NSX unavailable")
	require.Len(t, store.ListDNSRecords(), 2, "failed deletions must remain available for retry")
}
