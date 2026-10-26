/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dnsrecord

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctlcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	mockmanager "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/manager"
	mockdns "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordprovider"
	dnsrecmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordsclient"
	orgrootmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	realizedmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/realizedentitiesclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/dns"
)

func setupScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	return scheme
}

func newTestNSXClient(t *testing.T) *nsx.Client {
	t.Helper()
	ctrl := gomock.NewController(t)

	bodies := make(map[string]*model.DnsRecord)
	var captureRecords func([]*data.StructValue, int)
	captureRecords = func(children []*data.StructValue, depth int) {
		for _, child := range children {
			if depth < 2 {
				value, errs := servicecommon.NewConverter().ConvertToGolang(child, model.ChildResourceReferenceBindingType())
				require.Empty(t, errs)
				captureRecords(value.(model.ChildResourceReference).Children, depth+1)
				continue
			}
			value, errs := servicecommon.NewConverter().ConvertToGolang(child, model.ChildDnsRecordBindingType())
			require.Empty(t, errs)
			rec := value.(model.ChildDnsRecord).DnsRecord
			if rec != nil && rec.Id != nil {
				bodies[*rec.Id] = rec
			}
		}
	}
	orgRoot := orgrootmocks.NewMockOrgRootClient(ctrl)
	orgRoot.EXPECT().Patch(gomock.Any(), gomock.Any()).DoAndReturn(func(root model.OrgRoot, _ *bool) error {
		captureRecords(root.Children, 0)
		return nil
	}).AnyTimes()

	realized := realizedmocks.NewMockRealizedEntitiesClient(ctrl)
	st := model.GenericPolicyRealizedResource_STATE_REALIZED
	realized.EXPECT().List(gomock.Any(), gomock.Any()).Return(model.GenericPolicyRealizedResourceListResult{
		Results: []model.GenericPolicyRealizedResource{{State: &st}},
	}, nil).AnyTimes()

	dnsRec := dnsrecmocks.NewMockDnsRecordsClient(ctrl)
	dnsRec.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(orgID, projectID, recordID string) (model.DnsRecord, error) {
			p := fmt.Sprintf("/orgs/%s/projects/%s/%s/%s", orgID, projectID, dns.DNSRecordPathSegment, recordID)
			if rec := bodies[recordID]; rec != nil {
				out := *rec
				out.Path = &p
				return out, nil
			}
			rid := recordID
			return model.DnsRecord{Id: &rid, Path: &p}, nil
		}).AnyTimes()
	dnsRec.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	return &nsx.Client{
		OrgRootClient:          orgRoot,
		RealizedEntitiesClient: realized,
		DnsRecordsClient:       dnsRec,
	}
}

func setupTestDNSRecordService(t *testing.T, fakeClient client.Client) (*dns.DNSRecordService, *dns.RecordStore) {
	t.Helper()
	mockVPC := &pkgmock.MockVPCServiceProvider{}
	nc := &v1alpha1.VPCNetworkConfiguration{
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			DNSZones: []string{"/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-1"},
		},
	}
	mockVPC.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(nc, nil)

	zoneCache := dns.NewDNSZoneCacheFromMap(map[string]string{
		"/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-1": "example.com",
	})
	builder, err := servicecommon.PolicyPathDnsRecord.NewPolicyTreeBuilder()
	require.NoError(t, err)
	nsxCfg := &config.NSXOperatorConfig{
		CoeConfig: &config.CoeConfig{Cluster: "unit-test"},
		NsxConfig: &config.NsxConfig{},
	}
	nsxClient := newTestNSXClient(t)
	store := dns.BuildDNSRecordStore()

	dnsSvc := &dns.DNSRecordService{
		Service: servicecommon.Service{
			Client:    fakeClient,
			NSXConfig: nsxCfg,
			NSXClient: nsxClient,
		},
		VPCService:       mockVPC,
		DNSRecordStore:   store,
		DNSZoneMap:       zoneCache,
		DnsRecordBuilder: builder,
	}
	return dnsSvc, store
}

func TestCalculateFQDN(t *testing.T) {
	tests := []struct {
		name       string
		recordName string
		domainName string
		want       string
	}{
		{
			name:       "standard record and domain",
			recordName: "api",
			domainName: "example.com",
			want:       "api.example.com",
		},
		{
			name:       "apex record @",
			recordName: "@",
			domainName: "example.com",
			want:       "example.com",
		},
		{
			name:       "empty record name",
			recordName: "",
			domainName: "example.com.",
			want:       "example.com",
		},
		{
			name:       "uppercase and trailing dots",
			recordName: "Web.",
			domainName: "EXAMPLE.COM.",
			want:       "web.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateFQDN(tt.recordName, tt.domainName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDNSRecordReconciler_Reconcile_NotFound(t *testing.T) {
	scheme := setupScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	store := dns.BuildDNSRecordStore()
	dnsSvc := &dns.DNSRecordService{
		DNSRecordStore: store,
	}
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "nonexistent"},
	}

	res, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)
}

func TestDNSRecordReconciler_Reconcile_NotFoundWithService(t *testing.T) {
	scheme := setupScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	store := dns.BuildDNSRecordStore()
	dnsSvc := &dns.DNSRecordService{
		DNSRecordStore: store,
	}

	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "nonexistent"},
	}

	res, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)
}

func TestDNSRecordReconciler_Reconcile_CreateAndUpdate(t *testing.T) {
	scheme := setupScheme()
	ttl := int32(600)
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "test-dnsrecord",
			Generation: 1,
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "example.com",
			RecordName:   "api",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
			TTL:          &ttl,
		},
	}
	nsObj := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
			UID:  "default-ns-uid",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(dnsRecord, nsObj).Build()
	dnsSvc, _ := setupTestDNSRecordService(t, fakeClient)

	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-dnsrecord"},
	}

	// First reconcile computes FQDN without adding finalizer
	res, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)

	updatedCR := &v1alpha1.DNSRecord{}
	err = fakeClient.Get(ctx, req.NamespacedName, updatedCR)
	assert.NoError(t, err)
	assert.Empty(t, updatedCR.Finalizers)
	assert.Equal(t, "api.example.com", updatedCR.Status.FQDN)
	assert.Len(t, updatedCR.Status.Conditions, 1)
	assert.Equal(t, string(v1alpha1.Ready), updatedCR.Status.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionTrue, updatedCR.Status.Conditions[0].Status)
	assert.Equal(t, "DNSRecordReady", updatedCR.Status.Conditions[0].Reason)
	assert.Equal(t, dnsRecord.Spec, updatedCR.Spec)

	updatedCR.Spec.RecordName = "web"
	updatedCR.Generation++
	require.NoError(t, fakeClient.Update(ctx, updatedCR))
	res, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, ResultNormal, res)
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updatedCR))
	assert.Equal(t, "web.example.com", updatedCR.Status.FQDN)
	assert.Equal(t, "web", updatedCR.Spec.RecordName)
}

func TestDNSRecordReconciler_Reconcile_UnsupportedRecordType(t *testing.T) {
	for _, recordType := range []v1alpha1.DNSRecordType{"CNAME", "PTR", "NS", "TXT", "MX", ""} {
		t.Run(string(recordType), func(t *testing.T) {
			scheme := setupScheme()
			dnsRecord := &v1alpha1.DNSRecord{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "unsupported-record"},
				Spec: v1alpha1.DNSRecordSpec{
					DomainName:   "example.com",
					RecordName:   "api",
					RecordType:   recordType,
					RecordValues: []string{"10.0.0.1"},
				},
			}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(dnsRecord).Build()
			r := &DNSRecordReconciler{
				Client: fakeClient,
				// An unconfigured service would fail if reconciliation reached NSX processing.
				Service:       &dns.DNSRecordService{},
				StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
			}

			key := client.ObjectKeyFromObject(dnsRecord)
			res, err := r.Reconcile(context.Background(), controllerruntime.Request{NamespacedName: key})
			require.NoError(t, err)
			assert.Equal(t, ResultNormal, res)

			updated := &v1alpha1.DNSRecord{}
			require.NoError(t, fakeClient.Get(context.Background(), key, updated))
			cond := meta.FindStatusCondition(updated.Status.Conditions, string(v1alpha1.Ready))
			require.NotNil(t, cond)
			assert.Equal(t, metav1.ConditionFalse, cond.Status)
			assert.Contains(t, cond.Message, "only A and AAAA are supported")
			assert.Equal(t, dnsRecord.Spec, updated.Spec)
		})
	}
}

func TestDNSRecordReconciler_Reconcile_FQDNStatusUpdateFailure(t *testing.T) {
	scheme := setupScheme()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "status-failure"},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "example.com",
			RecordName:   "api",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
		},
	}
	updateErr := fmt.Errorf("status update failed")
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(dnsRecord).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
				t.Fatal("FQDN reconciliation must not update spec")
				return nil
			},
			SubResourceUpdate: func(_ context.Context, _ client.Client, subresource string, _ client.Object, _ ...client.SubResourceUpdateOption) error {
				assert.Equal(t, "status", subresource)
				return updateErr
			},
		}).Build()
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Service:       &dns.DNSRecordService{},
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	res, err := r.Reconcile(context.Background(), controllerruntime.Request{NamespacedName: client.ObjectKeyFromObject(dnsRecord)})
	assert.ErrorIs(t, err, updateErr)
	assert.Equal(t, ResultRequeue, res)
}

func TestDNSRecordReconciler_Reconcile_Delete(t *testing.T) {
	scheme := setupScheme()
	now := metav1.Now()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-dnsrecord",
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "example.com",
			RecordName:   "api",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dnsRecord).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if err := c.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				if rec, ok := obj.(*v1alpha1.DNSRecord); ok {
					rec.DeletionTimestamp = &now
				}
				return nil
			},
		}).Build()
	store := dns.BuildDNSRecordStore()
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       &dns.DNSRecordService{DNSRecordStore: store},
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-dnsrecord"},
	}

	res, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)
}

func TestDNSRecordReconciler_Reconcile_DeleteWithService(t *testing.T) {
	scheme := setupScheme()
	now := metav1.Now()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-dnsrecord",
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "example.com",
			RecordName:   "api",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dnsRecord).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if err := c.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				if rec, ok := obj.(*v1alpha1.DNSRecord); ok {
					rec.DeletionTimestamp = &now
				}
				return nil
			},
		}).Build()
	store := dns.BuildDNSRecordStore()
	rec := &model.DnsRecord{
		Path: servicecommon.String("/orgs/o/projects/p/dns-records/api_z_a"),
		Tags: []model.Tag{
			{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordFor), Tag: servicecommon.String(servicecommon.TagValueDNSRecordForDNSRecord)},
			{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordOwnerNamespace), Tag: servicecommon.String("default")},
			{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordOwnerName), Tag: servicecommon.String("test-dnsrecord")},
		},
	}
	require.NoError(t, store.Add(rec))
	builder, err := servicecommon.PolicyPathDnsRecord.NewPolicyTreeBuilder()
	require.NoError(t, err)
	nsxClient := newTestNSXClient(t)
	dnsSvc := &dns.DNSRecordService{
		Service: servicecommon.Service{
			NSXClient: nsxClient,
			NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}},
		},
		DNSRecordStore:   store,
		DnsRecordBuilder: builder,
	}

	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-dnsrecord"},
	}

	res, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)
	assert.Empty(t, store.ListDNSRecords())
}

func TestDNSRecordReconciler_Reconcile_ServiceZoneValidationError(t *testing.T) {
	scheme := setupScheme()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "invalid-zone-record",
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "invalid-domain.com",
			RecordName:   "test",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(dnsRecord).Build()

	mockVPC := &pkgmock.MockVPCServiceProvider{}
	nc := &v1alpha1.VPCNetworkConfiguration{
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			DNSZones: []string{"/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-1"},
		},
	}
	mockVPC.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(nc, nil)

	zoneCache := dns.NewDNSZoneCacheFromMap(map[string]string{
		"/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-1": "example.com",
	})
	dnsSvc := &dns.DNSRecordService{
		VPCService:     mockVPC,
		DNSRecordStore: dns.BuildDNSRecordStore(),
		DNSZoneMap:     zoneCache,
	}

	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "invalid-zone-record"},
	}

	res, err := r.Reconcile(ctx, req)
	assert.Error(t, err)
	assert.Equal(t, ResultRequeue, res)

	updatedCR := &v1alpha1.DNSRecord{}
	err = fakeClient.Get(ctx, req.NamespacedName, updatedCR)
	assert.NoError(t, err)
	assert.Len(t, updatedCR.Status.Conditions, 1)
	assert.Equal(t, string(v1alpha1.Ready), updatedCR.Status.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionFalse, updatedCR.Status.Conditions[0].Status)
	assert.Equal(t, ReasonDNSRecordValidationFailed, updatedCR.Status.Conditions[0].Reason)
	assert.Contains(t, updatedCR.Status.Conditions[0].Message, "does not match any allowed DNS domain")
}

func TestDNSRecordReconciler_Reconcile_ServiceZoneValidationSuccess(t *testing.T) {
	minTTL, maxTTL := int32(30), int32(86400)
	for _, tc := range []struct {
		recordType v1alpha1.DNSRecordType
		address    string
		ttl        *int32
		wantTTL    int64
	}{
		{v1alpha1.DNSRecordTypeA, "10.0.0.1", nil, 300},
		{v1alpha1.DNSRecordTypeAAAA, "2001:db8::1", nil, 300},
		{v1alpha1.DNSRecordTypeA, "10.0.0.1", &minTTL, 30},
		{v1alpha1.DNSRecordTypeAAAA, "2001:db8::1", &maxTTL, 86400},
	} {
		t.Run(string(tc.recordType), func(t *testing.T) {
			scheme := setupScheme()
			dnsRecord := &v1alpha1.DNSRecord{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "default",
					Name:       "valid-zone-record",
					Generation: 1,
				},
				Spec: v1alpha1.DNSRecordSpec{
					DomainName:   "example.com",
					RecordName:   "api",
					RecordType:   tc.recordType,
					RecordValues: []string{tc.address},
					TTL:          tc.ttl,
				},
			}
			nsObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
					UID:  "default-ns-uid",
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(dnsRecord, nsObj).Build()

			mockVPC := &pkgmock.MockVPCServiceProvider{}
			nc := &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					DNSZones: []string{"/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-1"},
				},
			}
			mockVPC.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(nc, nil)

			zoneCache := dns.NewDNSZoneCacheFromMap(map[string]string{
				"/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-1": "example.com",
			})
			builder, err := servicecommon.PolicyPathDnsRecord.NewPolicyTreeBuilder()
			require.NoError(t, err)
			nsxCfg := &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{Cluster: "unit-test"},
				NsxConfig: &config.NsxConfig{},
			}
			nsxClient := newTestNSXClient(t)

			dnsSvc := &dns.DNSRecordService{
				Service: servicecommon.Service{
					Client:    fakeClient,
					NSXConfig: nsxCfg,
					NSXClient: nsxClient,
				},
				VPCService:       mockVPC,
				DNSRecordStore:   dns.BuildDNSRecordStore(),
				DNSZoneMap:       zoneCache,
				DnsRecordBuilder: builder,
			}

			r := &DNSRecordReconciler{
				Client:        fakeClient,
				Scheme:        scheme,
				Service:       dnsSvc,
				StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, nsxCfg, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
			}

			ctx := context.Background()
			req := controllerruntime.Request{
				NamespacedName: types.NamespacedName{Namespace: "default", Name: "valid-zone-record"},
			}

			res, err := r.Reconcile(ctx, req)
			assert.NoError(t, err)
			assert.Equal(t, ResultNormal, res)

			updatedCR := &v1alpha1.DNSRecord{}
			err = fakeClient.Get(ctx, req.NamespacedName, updatedCR)
			assert.NoError(t, err)
			assert.Len(t, updatedCR.Status.Conditions, 1)
			assert.Equal(t, string(v1alpha1.Ready), updatedCR.Status.Conditions[0].Type)
			assert.Equal(t, metav1.ConditionTrue, updatedCR.Status.Conditions[0].Status)
			assert.Equal(t, "DNSRecordReady", updatedCR.Status.Conditions[0].Reason)
			assert.Equal(t, "api.example.com", updatedCR.Status.FQDN)
			records := dnsSvc.DNSRecordStore.GetByOwnerResourceNamespacedName(dns.ResourceKindDNSRecord, "default", "valid-zone-record")
			require.Len(t, records, 1)
			assert.Equal(t, string(tc.recordType), *records[0].RecordType)
			assert.Equal(t, []string{tc.address}, records[0].RecordValues)
			assert.Equal(t, tc.wantTTL, *records[0].Ttl)

			updatedCR.Spec.RecordName = "web"
			updatedCR.Generation++ // The fake client does not advance generation on spec updates.
			require.NoError(t, fakeClient.Update(ctx, updatedCR))
			require.Less(t, updatedCR.Status.Conditions[0].ObservedGeneration, updatedCR.Generation)
			res, err = r.Reconcile(ctx, req)
			require.NoError(t, err)
			require.Equal(t, ResultNormal, res)
			require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updatedCR))
			assert.Equal(t, "web.example.com", updatedCR.Status.FQDN)
			assert.Equal(t, updatedCR.Generation, updatedCR.Status.Conditions[0].ObservedGeneration)
			records = dnsSvc.DNSRecordStore.GetByOwnerResourceNamespacedName(dns.ResourceKindDNSRecord, "default", "valid-zone-record")
			require.Len(t, records, 1, "renaming must remove the old NSX record")
			assert.Equal(t, "web", *records[0].RecordName)

			require.NoError(t, fakeClient.Delete(ctx, updatedCR))
			res, err = r.Reconcile(ctx, req)
			require.NoError(t, err)
			require.Equal(t, ResultNormal, res)
			require.Empty(t, dnsSvc.DNSRecordStore.ListDNSRecords())
			require.Error(t, fakeClient.Get(ctx, req.NamespacedName, &v1alpha1.DNSRecord{}))
		})
	}
}

func TestDNSRecordReconciler_CollectGarbage_WithStore(t *testing.T) {
	scheme := setupScheme()
	activeCR := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "active-record",
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(activeCR).Build()

	store := dns.BuildDNSRecordStore()
	path1 := "/orgs/org1/projects/proj1/dns-records/stale-rec"
	path2 := "/orgs/org1/projects/proj1/dns-records/active-rec"
	path3 := "/orgs/org1/projects/proj1/dns-records/gw-rec"

	forScope := servicecommon.TagScopeDNSRecordFor
	forValDNSRecord := servicecommon.TagValueDNSRecordForDNSRecord
	forValGateway := servicecommon.TagValueDNSRecordForGateway
	nsScope := servicecommon.TagScopeDNSRecordOwnerNamespace
	nameScope := servicecommon.TagScopeDNSRecordOwnerName
	nsVal := "default"
	staleNameVal := "stale-record"
	activeNameVal := "active-record"

	staleRec := &model.DnsRecord{
		Path: &path1,
		Tags: []model.Tag{
			{Scope: &forScope, Tag: &forValDNSRecord},
			{Scope: &nsScope, Tag: &nsVal},
			{Scope: &nameScope, Tag: &staleNameVal},
		},
	}

	activeRec := &model.DnsRecord{
		Path: &path2,
		Tags: []model.Tag{
			{Scope: &forScope, Tag: &forValDNSRecord},
			{Scope: &nsScope, Tag: &nsVal},
			{Scope: &nameScope, Tag: &activeNameVal},
		},
	}

	gwRec := &model.DnsRecord{
		Path: &path3,
		Tags: []model.Tag{
			{Scope: &forScope, Tag: &forValGateway},
			{Scope: &nsScope, Tag: &nsVal},
			{Scope: &nameScope, Tag: &staleNameVal},
		},
	}

	require.NoError(t, store.Add(staleRec))
	require.NoError(t, store.Add(activeRec))
	require.NoError(t, store.Add(gwRec))

	builder, err := servicecommon.PolicyPathDnsRecord.NewPolicyTreeBuilder()
	require.NoError(t, err)
	nsxClient := newTestNSXClient(t)

	dnsSvc := &dns.DNSRecordService{
		Service: servicecommon.Service{
			NSXClient: nsxClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{Cluster: "unit-test"},
				NsxConfig: &config.NsxConfig{},
			},
		},
		DNSRecordStore:   store,
		DnsRecordBuilder: builder,
	}

	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	err = r.CollectGarbage(ctx)
	assert.NoError(t, err)
}

func TestDNSRecordReconciler_RestoreAndGC(t *testing.T) {
	scheme := setupScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       &dns.DNSRecordService{DNSRecordStore: dns.BuildDNSRecordStore()},
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	assert.NoError(t, r.RestoreReconcile())
	assert.NoError(t, r.CollectGarbage(ctx))
}

func TestDNSRecordReconciler_UpdateStatusConditions(t *testing.T) {
	scheme := setupScheme()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-status",
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(dnsRecord).Build()
	ctx := context.Background()

	require.NoError(t, setDNSRecordReadyStatusTrue(fakeClient, ctx, dnsRecord, metav1.Now()))
	updated := &v1alpha1.DNSRecord{}
	err := fakeClient.Get(ctx, client.ObjectKeyFromObject(dnsRecord), updated)
	assert.NoError(t, err)
	assert.Len(t, updated.Status.Conditions, 1)
	assert.Equal(t, metav1.ConditionTrue, updated.Status.Conditions[0].Status)

	require.NoError(t, setDNSRecordReadyStatusFalse(fakeClient, ctx, dnsRecord, metav1.Now(), ReasonDNSRecordFailed, fmt.Errorf("fake error")))
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(dnsRecord), updated)
	assert.NoError(t, err)
	assert.Len(t, updated.Status.Conditions, 1)
	assert.Equal(t, metav1.ConditionFalse, updated.Status.Conditions[0].Status)
	assert.Equal(t, ReasonDNSRecordFailed, updated.Status.Conditions[0].Reason)

	require.NoError(t, setDNSRecordReadyStatusFalse(fakeClient, ctx, dnsRecord, metav1.Now(), ReasonDNSRecordValidationFailed, fmt.Errorf("validation error")))
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(dnsRecord), updated)
	assert.NoError(t, err)
	assert.Len(t, updated.Status.Conditions, 1)
	assert.Equal(t, metav1.ConditionFalse, updated.Status.Conditions[0].Status)
	assert.Equal(t, ReasonDNSRecordValidationFailed, updated.Status.Conditions[0].Reason)
}

func TestDNSRecordReconciler_MockDNSRecordProvider(t *testing.T) {
	ctrlMock := gomock.NewController(t)
	defer ctrlMock.Finish()

	_ = mockdns.NewMockDNSRecordProvider(ctrlMock)
	time.Sleep(10 * time.Millisecond)
}

func TestNewDNSRecordReconciler(t *testing.T) {
	scheme := setupScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	nsxCfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}
	dnsSvc := &dns.DNSRecordService{
		Service: servicecommon.Service{
			NSXConfig: nsxCfg,
		},
	}

	ctrlMock := gomock.NewController(t)
	defer ctrlMock.Finish()
	mockMgr := mockmanager.NewMockManager(ctrlMock)
	mockMgr.EXPECT().GetClient().Return(fakeClient).AnyTimes()
	mockMgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mockMgr.EXPECT().GetEventRecorderFor("dnsrecord-controller").Return(record.NewFakeRecorder(100)).AnyTimes()

	r1 := NewDNSRecordReconciler(mockMgr, dnsSvc)
	assert.NotNil(t, r1)
	assert.Equal(t, dnsSvc, r1.Service)

	r2 := NewDNSRecordReconciler(mockMgr, nil)
	assert.Nil(t, r2)
}

func TestPredicateNetworkInfoAllowedDNSDomainsChanged(t *testing.T) {
	p := predicateNetworkInfoAllowedDNSDomainsChanged()

	assert.False(t, p.Delete(event.DeleteEvent{}))
	assert.False(t, p.Generic(event.GenericEvent{}))

	t.Run("CreateFunc", func(t *testing.T) {
		assert.False(t, p.Create(event.CreateEvent{Object: &v1alpha1.DNSRecord{}}))
		assert.False(t, p.Create(event.CreateEvent{Object: &v1alpha1.NetworkInfo{}}))
		assert.True(t, p.Create(event.CreateEvent{
			Object: &v1alpha1.NetworkInfo{AllowedDNSDomains: []string{"example.com"}},
		}))
	})

	t.Run("UpdateFunc", func(t *testing.T) {
		assert.False(t, p.Update(event.UpdateEvent{
			ObjectOld: &v1alpha1.DNSRecord{},
			ObjectNew: &v1alpha1.DNSRecord{},
		}))

		oldNI := &v1alpha1.NetworkInfo{AllowedDNSDomains: []string{"example.com"}}
		sameNI := &v1alpha1.NetworkInfo{AllowedDNSDomains: []string{"example.com"}}
		assert.False(t, p.Update(event.UpdateEvent{ObjectOld: oldNI, ObjectNew: sameNI}))

		changedNI := &v1alpha1.NetworkInfo{AllowedDNSDomains: []string{"example.com", "corp.local"}}
		assert.True(t, p.Update(event.UpdateEvent{ObjectOld: oldNI, ObjectNew: changedNI}))
	})
}

func TestNetworkInfoToDNSRecordRequests(t *testing.T) {
	scheme := setupScheme()
	rec1 := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "rec1"},
	}
	rec2 := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "rec2"},
	}
	recOther := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns2", Name: "recOther"},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rec1, rec2, recOther).Build()
	r := &DNSRecordReconciler{Client: fakeClient}

	ni := &v1alpha1.NetworkInfo{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ni"},
	}

	reqs := r.networkInfoToDNSRecordRequests(context.Background(), ni)
	require.Len(t, reqs, 2)
	assert.ElementsMatch(t, []ctrl.Request{
		{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "rec1"}},
		{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "rec2"}},
	}, reqs)

	t.Run("nil or invalid object", func(t *testing.T) {
		assert.Nil(t, r.networkInfoToDNSRecordRequests(context.Background(), nil))
		assert.Nil(t, r.networkInfoToDNSRecordRequests(context.Background(), &v1alpha1.DNSRecord{}))
	})

	t.Run("client list error", func(t *testing.T) {
		errClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				return errors.New("list failed")
			},
		}).Build()
		rErr := &DNSRecordReconciler{Client: errClient}
		assert.Nil(t, rErr.networkInfoToDNSRecordRequests(context.Background(), ni))
	})
}

func TestDNSRecordReconciler_StartController(t *testing.T) {
	scheme := setupScheme()
	ctrlMock := gomock.NewController(t)
	defer ctrlMock.Finish()

	mockMgr := mockmanager.NewMockManager(ctrlMock)
	mockMgr.EXPECT().GetControllerOptions().Return(ctrlconfig.Controller{SkipNameValidation: servicecommon.Bool(true)}).AnyTimes()
	mockMgr.EXPECT().GetLogger().Return(logr.Discard()).AnyTimes()
	mockMgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mockMgr.EXPECT().Add(gomock.Any()).Return(errors.New("add controller failed")).Times(1)

	r := &DNSRecordReconciler{}
	err := r.StartController(mockMgr, nil)
	assert.ErrorContains(t, err, "add controller failed")
}

func TestDNSRecordReconciler_GCRunnable(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).Build()
	r := &DNSRecordReconciler{
		Client:  fakeClient,
		Service: &dns.DNSRecordService{DNSRecordStore: dns.BuildDNSRecordStore()},
	}
	var runnable manager.Runnable = r.gcRunnable()
	require.NotNil(t, runnable)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runnable.Start(ctx)
	assert.NoError(t, err)
}
