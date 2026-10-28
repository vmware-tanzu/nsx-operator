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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/event"

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
				Service:       &dns.DNSRecordService{DNSRecordStore: dns.BuildDNSRecordStore()},
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
	statusUpdates := 0
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(dnsRecord).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
				t.Fatal("FQDN reconciliation must not update spec")
				return nil
			},
			SubResourceUpdate: func(_ context.Context, _ client.Client, subresource string, _ client.Object, _ ...client.SubResourceUpdateOption) error {
				assert.Equal(t, "status", subresource)
				statusUpdates++
				return updateErr
			},
		}).Build()
	dnsSvc, _ := setupTestDNSRecordService(t, fakeClient)
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	res, err := r.Reconcile(context.Background(), controllerruntime.Request{NamespacedName: client.ObjectKeyFromObject(dnsRecord)})
	assert.ErrorIs(t, err, updateErr)
	assert.Equal(t, ResultRequeue, res)
	assert.Equal(t, 1, statusUpdates)
}

func TestDNSRecordReconciler_WritesFQDNAndConditionTogether(t *testing.T) {
	ctx := context.Background()
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "status-once", Generation: 1},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName: "example.com", RecordName: "api", RecordType: v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.1"},
		},
	}
	statusUpdates := 0
	fakeClient := fake.NewClientBuilder().WithScheme(setupScheme()).
		WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(dnsRecord).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				require.Equal(t, "status", subresource)
				statusUpdates++
				return c.SubResource(subresource).Update(ctx, obj, opts...)
			},
		}).Build()
	dnsSvc, _ := setupTestDNSRecordService(t, fakeClient)
	r := &DNSRecordReconciler{
		Client: fakeClient, Service: dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	key := client.ObjectKeyFromObject(dnsRecord)
	req := controllerruntime.Request{NamespacedName: key}
	for _, want := range []struct {
		name    string
		fqdn    string
		updates int
	}{
		{"initial", "api.example.com", 1},
		{"unchanged", "api.example.com", 1},
		{"renamed", "new.example.com", 2},
	} {
		t.Run(want.name, func(t *testing.T) {
			if want.name == "renamed" {
				current := &v1alpha1.DNSRecord{}
				require.NoError(t, fakeClient.Get(ctx, key, current))
				current.Spec.RecordName = "new"
				require.NoError(t, fakeClient.Update(ctx, current))
			}
			res, err := r.Reconcile(ctx, req)
			require.NoError(t, err)
			require.Equal(t, ResultNormal, res)
			current := &v1alpha1.DNSRecord{}
			require.NoError(t, fakeClient.Get(ctx, key, current))
			assert.Equal(t, want.fqdn, current.Status.FQDN)
			cond := meta.FindStatusCondition(current.Status.Conditions, string(v1alpha1.Ready))
			require.NotNil(t, cond)
			assert.Equal(t, metav1.ConditionTrue, cond.Status)
			assert.Equal(t, want.updates, statusUpdates)
		})
	}
}

func TestPredicateDNSRecordDesiredStateChanged(t *testing.T) {
	p := predicateDNSRecordDesiredStateChanged()
	oldRecord := &v1alpha1.DNSRecord{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "api", Generation: 1}}
	statusOnly := oldRecord.DeepCopy()
	statusOnly.Status.FQDN = "api.example.com"
	assert.False(t, p.Update(event.UpdateEvent{ObjectOld: oldRecord, ObjectNew: statusOnly}))

	specChanged := oldRecord.DeepCopy()
	specChanged.Generation++
	assert.True(t, p.Update(event.UpdateEvent{ObjectOld: oldRecord, ObjectNew: specChanged}))

	deleting := oldRecord.DeepCopy()
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	assert.True(t, p.Update(event.UpdateEvent{ObjectOld: oldRecord, ObjectNew: deleting}))
	assert.True(t, p.Create(event.CreateEvent{Object: oldRecord}))
	assert.True(t, p.Delete(event.DeleteEvent{Object: oldRecord}))
}

func TestDNSRecordMixedModeNamespaceTransition(t *testing.T) {
	previous := config.IsPerNamespaceProvidersSupported()
	config.SetPerNamespaceProvidersSupportedForTest(true)
	t.Cleanup(func() { config.SetPerNamespaceProvidersSupportedForTest(previous) })

	ctx := context.Background()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: "mixed-ns", Annotations: map[string]string{config.VPCNetworkConfigAnnotation: "default"},
	}}
	dnsRecord := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace.Name, Name: "api", Generation: 1},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName: "example.com", RecordName: "api", RecordType: v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"192.0.2.1"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).
		WithObjects(namespace, dnsRecord).Build()
	dnsSvc, store := setupTestDNSRecordService(t, c)
	r := &DNSRecordReconciler{Client: c, Service: dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord")}
	req := controllerruntime.Request{NamespacedName: client.ObjectKeyFromObject(dnsRecord)}
	result, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.Len(t, store.ListDNSRecords(), 1)

	namespace.Annotations = map[string]string{config.T1DefaultConfigAnnotation: "true"}
	require.NoError(t, c.Update(ctx, namespace))

	// Configuration events can enqueue every DNSRecord, including ones in T1 namespaces.
	result, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.Empty(t, store.ListDNSRecords())
	current := &v1alpha1.DNSRecord{}
	require.NoError(t, c.Get(ctx, req.NamespacedName, current))
	condition := meta.FindStatusCondition(current.Status.Conditions, string(v1alpha1.Ready))
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Contains(t, condition.Message, "not an active VPC Namespace")

	result, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.Empty(t, store.ListDNSRecords())
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
	assert.NoError(t, err)
	assert.Equal(t, ResultNormal, res)

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
		APIReader:     fakeClient,
		Scheme:        scheme,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(fakeClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}

	ctx := context.Background()
	err = r.CollectGarbage(ctx)
	assert.NoError(t, err)
}

type staleDNSRecordListClient struct{ client.Client }

func (c staleDNSRecordListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if records, ok := list.(*v1alpha1.DNSRecordList); ok {
		records.Items = nil
		return nil
	}
	return c.Client.List(ctx, list, opts...)
}

func TestDNSRecordGCRechecksExistenceAfterStaleList(t *testing.T) {
	ctx := context.Background()
	nn := types.NamespacedName{Namespace: "default", Name: "new-record"}
	cr := &v1alpha1.DNSRecord{ObjectMeta: metav1.ObjectMeta{Namespace: nn.Namespace, Name: nn.Name}}
	apiClient := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(cr).Build()
	store := dns.BuildDNSRecordStore()
	rec := &model.DnsRecord{
		Path: servicecommon.String("/orgs/o/projects/p/dns-records/new-record"),
		Tags: []model.Tag{
			{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordFor), Tag: servicecommon.String(servicecommon.TagValueDNSRecordForDNSRecord)},
			{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordOwnerNamespace), Tag: servicecommon.String(nn.Namespace)},
			{Scope: servicecommon.String(servicecommon.TagScopeDNSRecordOwnerName), Tag: servicecommon.String(nn.Name)},
		},
	}
	require.NoError(t, store.Add(rec))
	r := &DNSRecordReconciler{
		Client:        staleDNSRecordListClient{Client: apiClient},
		APIReader:     apiClient,
		Service:       &dns.DNSRecordService{DNSRecordStore: store},
		StatusUpdater: ctlcommon.NewStatusUpdater(apiClient, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	require.NoError(t, r.CollectGarbage(ctx))
	require.Len(t, store.ListDNSRecords(), 1, "GC must keep a record whose CR appeared after its initial list")
}

func TestDNSRecordReconciler_RestoreAndGC(t *testing.T) {
	scheme := setupScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &DNSRecordReconciler{
		Client:        fakeClient,
		APIReader:     fakeClient,
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
	mockMgr.EXPECT().GetAPIReader().Return(fakeClient).AnyTimes()
	mockMgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mockMgr.EXPECT().GetEventRecorderFor("dnsrecord-controller").Return(record.NewFakeRecorder(100)).AnyTimes()

	r1 := NewDNSRecordReconciler(mockMgr, dnsSvc)
	assert.NotNil(t, r1)
	assert.Equal(t, dnsSvc, r1.Service)

	r2 := NewDNSRecordReconciler(mockMgr, nil)
	assert.Nil(t, r2)
}

func TestNetworkConfigDNSZoneChangeEnqueuesRecords(t *testing.T) {
	zoneA := "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-a"
	zoneB := "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-b"
	oldNC := &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "network-config"},
		Spec:       v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{zoneA}},
	}
	newNC := oldNC.DeepCopy()
	newNC.Spec.DNSZones = []string{zoneB}
	p := predicateNetworkConfigDNSSettingsChanged()
	require.True(t, p.Update(event.UpdateEvent{ObjectOld: oldNC, ObjectNew: newNC}))
	require.False(t, p.Update(event.UpdateEvent{ObjectOld: oldNC, ObjectNew: oldNC.DeepCopy()}))
	defaultNC := oldNC.DeepCopy()
	defaultNC.Annotations = map[string]string{servicecommon.AnnotationDefaultNetworkConfig: "true"}
	require.True(t, p.Update(event.UpdateEvent{ObjectOld: oldNC, ObjectNew: defaultNC}))
	projectNC := oldNC.DeepCopy()
	projectNC.Spec.NSXProject = "/orgs/org1/projects/proj2"
	require.True(t, p.Update(event.UpdateEvent{ObjectOld: oldNC, ObjectNew: projectNC}))

	rec1 := &v1alpha1.DNSRecord{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "rec1"}}
	rec2 := &v1alpha1.DNSRecord{ObjectMeta: metav1.ObjectMeta{Namespace: "ns2", Name: "rec2"}}
	recOther := &v1alpha1.DNSRecord{ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "unrelated"}}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(rec1, rec2, recOther).Build()
	r := &DNSRecordReconciler{Client: c}
	reqs := r.networkConfigToDNSRecordRequests(context.Background(), newNC)
	require.ElementsMatch(t, []controllerruntime.Request{
		{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "rec1"}},
		{NamespacedName: types.NamespacedName{Namespace: "ns2", Name: "rec2"}},
		{NamespacedName: types.NamespacedName{Namespace: "other", Name: "unrelated"}},
	}, reqs)
}

func TestNetworkConfigRecordListFailureIsRetried(t *testing.T) {
	failList := true
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, reader client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*v1alpha1.DNSRecordList); ok && failList {
				return errors.New("DNSRecord list unavailable")
			}
			return reader.List(ctx, list, opts...)
		},
	}).Build()
	r := &DNSRecordReconciler{Client: c}
	nc := &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "network-config"}}
	reqs := r.networkConfigToDNSRecordRequests(context.Background(), nc)
	require.Equal(t, []controllerruntime.Request{{NamespacedName: types.NamespacedName{
		Namespace: networkConfigRetryNamespace, Name: nc.Name,
	}}}, reqs)
	_, err := r.Reconcile(context.Background(), reqs[0])
	require.ErrorContains(t, err, "DNSRecord list unavailable")
	failList = false
	result, err := r.Reconcile(context.Background(), reqs[0])
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
}

func TestDeletedDefaultNetworkConfigEnqueuesAllDNSRecords(t *testing.T) {
	rec := &v1alpha1.DNSRecord{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "rec1"}}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(rec).Build()
	r := &DNSRecordReconciler{
		Client: c,
	}
	reqs, err := r.requestsForNetworkConfig(context.Background(), "deleted-default")
	require.NoError(t, err)
	require.Equal(t, []controllerruntime.Request{{NamespacedName: client.ObjectKeyFromObject(rec)}}, reqs)
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
	var runnable = r.gcRunnable()
	require.NotNil(t, runnable)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runnable.Start(ctx)
	assert.NoError(t, err)
}
