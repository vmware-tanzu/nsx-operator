/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dnsrecord

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctlcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
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

	orgRoot := orgrootmocks.NewMockOrgRootClient(ctrl)
	orgRoot.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	realized := realizedmocks.NewMockRealizedEntitiesClient(ctrl)
	st := model.GenericPolicyRealizedResource_STATE_REALIZED
	realized.EXPECT().List(gomock.Any(), gomock.Any()).Return(model.GenericPolicyRealizedResourceListResult{
		Results: []model.GenericPolicyRealizedResource{{State: &st}},
	}, nil).AnyTimes()

	dnsRec := dnsrecmocks.NewMockDnsRecordsClient(ctrl)
	dnsRec.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(orgID, projectID, recordID string) (model.DnsRecord, error) {
			p := fmt.Sprintf("/orgs/%s/projects/%s/%s/%s", orgID, projectID, dns.DNSRecordPathSegment, recordID)
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
			name:       "PTR record name",
			recordName: "10",
			domainName: "0.0.10.in-addr.arpa",
			want:       "10.0.0.10.in-addr.arpa",
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
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       nil,
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
			Namespace: "default",
			Name:      "test-dnsrecord",
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "example.com",
			RecordName:   "api",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
			TTL:          &ttl,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(dnsRecord).Build()
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       nil, // Service is nil -> tests controller FQDN calculation & finalizer addition
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-dnsrecord"},
	}

	// First reconcile adds finalizer and computes FQDN
	res, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)

	updatedCR := &v1alpha1.DNSRecord{}
	err = fakeClient.Get(ctx, req.NamespacedName, updatedCR)
	assert.NoError(t, err)
	assert.Contains(t, updatedCR.Finalizers, servicecommon.DNSRecordFinalizerName)
	assert.Equal(t, "api.example.com", updatedCR.Spec.FQDN)
	assert.Len(t, updatedCR.Status.Conditions, 1)
	assert.Equal(t, string(v1alpha1.Ready), updatedCR.Status.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionTrue, updatedCR.Status.Conditions[0].Status)
	assert.Equal(t, "DNSRecordReady", updatedCR.Status.Conditions[0].Reason)
}

func TestDNSRecordReconciler_Reconcile_Delete(t *testing.T) {
	scheme := setupScheme()
	now := metav1.Now()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "default",
			Name:              "test-dnsrecord",
			DeletionTimestamp: &now,
			Finalizers:        []string{servicecommon.DNSRecordFinalizerName},
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "example.com",
			RecordName:   "api",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
			FQDN:         "api.example.com",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dnsRecord).Build()
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       nil,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	req := controllerruntime.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-dnsrecord"},
	}

	res, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)

	updatedCR := &v1alpha1.DNSRecord{}
	err = fakeClient.Get(ctx, req.NamespacedName, updatedCR)
	assert.Error(t, err)
}

func TestDNSRecordReconciler_Reconcile_DeleteWithService(t *testing.T) {
	scheme := setupScheme()
	now := metav1.Now()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "default",
			Name:              "test-dnsrecord",
			DeletionTimestamp: &now,
			Finalizers:        []string{servicecommon.DNSRecordFinalizerName},
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "example.com",
			RecordName:   "api",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
			FQDN:         "api.example.com",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dnsRecord).Build()
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
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-dnsrecord"},
	}

	res, err := r.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)

	updatedCR := &v1alpha1.DNSRecord{}
	err = fakeClient.Get(ctx, req.NamespacedName, updatedCR)
	assert.Error(t, err)
}

func TestDNSRecordReconciler_Reconcile_ServiceZoneValidationError(t *testing.T) {
	scheme := setupScheme()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "invalid-zone-record",
			Finalizers: []string{servicecommon.DNSRecordFinalizerName},
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "invalid-domain.com",
			RecordName:   "test",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
			FQDN:         "test.invalid-domain.com",
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
	assert.Equal(t, "DNSRecordFailed", updatedCR.Status.Conditions[0].Reason)
	assert.Contains(t, updatedCR.Status.Conditions[0].Message, "does not match any allowed DNS domain")
}

func TestDNSRecordReconciler_Reconcile_ServiceZoneValidationSuccess(t *testing.T) {
	scheme := setupScheme()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "valid-zone-record",
			Finalizers: []string{servicecommon.DNSRecordFinalizerName},
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "example.com",
			RecordName:   "api",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
			FQDN:         "api.example.com",
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
		Service:       nil,
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

	setDNSRecordReadyStatusTrue(fakeClient, ctx, dnsRecord, metav1.Now())
	updated := &v1alpha1.DNSRecord{}
	err := fakeClient.Get(ctx, client.ObjectKeyFromObject(dnsRecord), updated)
	assert.NoError(t, err)
	assert.Len(t, updated.Status.Conditions, 1)
	assert.Equal(t, metav1.ConditionTrue, updated.Status.Conditions[0].Status)

	setDNSRecordReadyStatusFalse(fakeClient, ctx, dnsRecord, metav1.Now(), fmt.Errorf("fake error"))
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(dnsRecord), updated)
	assert.NoError(t, err)
	assert.Len(t, updated.Status.Conditions, 1)
	assert.Equal(t, metav1.ConditionFalse, updated.Status.Conditions[0].Status)
	assert.Equal(t, "DNSRecordFailed", updated.Status.Conditions[0].Reason)
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

	recorder := record.NewFakeRecorder(100)
	r1 := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       dnsSvc,
		Recorder:      recorder,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, nsxCfg, recorder, ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	assert.NotNil(t, r1)
	assert.Equal(t, dnsSvc, r1.Service)

	r2 := &DNSRecordReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Service:       nil,
		Recorder:      recorder,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, nsxCfg, recorder, ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	assert.NotNil(t, r2)
	assert.Nil(t, r2.Service)
}
