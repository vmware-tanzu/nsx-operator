/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dnsrecord

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
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

	corev1 "k8s.io/api/core/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctlcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
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
		wantReason string
	}{
		{"ready true", "api", v1alpha1.DNSRecordTypeA, metav1.ConditionTrue, ReasonDNSRecordReady},
		{"unsupported type", "api", "CNAME", metav1.ConditionFalse, ReasonDNSRecordValidationFailed},
		{"wildcard", "*", v1alpha1.DNSRecordTypeA, metav1.ConditionFalse, ReasonDNSRecordValidationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			obj := &v1alpha1.DNSRecord{
				ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default", Generation: 1},
				Spec: v1alpha1.DNSRecordSpec{
					DomainName: "example.com", RecordName: tc.recordName,
					RecordType: tc.recordType, RecordValues: []string{"192.0.2.1"},
				},
				Status: v1alpha1.DNSRecordStatus{FQDN: calculateFQDN(tc.recordName, "example.com")},
			}
			nsObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
					UID:  "default-ns-uid",
				},
			}
			statusErr := errors.New("status update unavailable")
			attempts := 0
			c := fake.NewClientBuilder().WithScheme(setupScheme()).
				WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj, nsObj).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						attempts++
						if attempts == 1 {
							return statusErr
						}
						return c.SubResource(subresource).Update(ctx, obj, opts...)
					},
				}).Build()
			dnsSvc, _ := setupTestDNSRecordService(t, c)
			r := &DNSRecordReconciler{
				Client:        c,
				Service:       dnsSvc,
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
			require.Equal(t, tc.wantReason, condition.Reason)
			require.Equal(t, 2, attempts)
		})
	}
}

func TestDNSRecordWildcardRemovesExistingRecord(t *testing.T) {
	ctx := context.Background()
	obj := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default", Generation: 1},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName: "example.com", RecordName: "api",
			RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj).Build()
	dnsSvc, store := setupTestDNSRecordService(t, c)
	r := &DNSRecordReconciler{
		Client:        c,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	result, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.Len(t, store.ListDNSRecords(), 1)
	require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
	obj.Spec.RecordName = "*"
	obj.Generation++
	require.NoError(t, c.Update(ctx, obj))

	result, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
	condition := meta.FindStatusCondition(obj.Status.Conditions, string(v1alpha1.Ready))
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, ReasonDNSRecordValidationFailed, condition.Reason)
	require.Contains(t, condition.Message, "wildcard DNS names are not supported")
	require.Equal(t, "*.example.com", obj.Status.FQDN)
	require.Empty(t, store.ListDNSRecords())
}

func TestDNSRecordZoneRevocationRemovesExistingRecord(t *testing.T) {
	ctx := context.Background()
	obj := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default", Generation: 1},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName: "example.com", RecordName: "api",
			RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj).Build()
	dnsSvc, store := setupTestDNSRecordService(t, c)
	nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{
		DNSZones: []string{"/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-1"},
	}}
	vpcSvc := &pkgmock.MockVPCServiceProvider{}
	vpcSvc.On("GetVPCNetworkConfigByNamespace", mock.Anything).Return(nc, nil)
	dnsSvc.VPCService = vpcSvc
	r := &DNSRecordReconciler{
		Client:        c,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	result, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.Len(t, store.ListDNSRecords(), 1)

	nc.Spec.DNSZones = nil
	result, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.Empty(t, store.ListDNSRecords())
	require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
	condition := meta.FindStatusCondition(obj.Status.Conditions, string(v1alpha1.Ready))
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, ReasonDNSRecordValidationFailed, condition.Reason)
}

func TestDNSRecordInvalidConfiguredZoneRemovesExistingRecord(t *testing.T) {
	ctx := context.Background()
	obj := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default", Generation: 1},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName: "example.com", RecordName: "api",
			RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj).Build()
	dnsSvc, store := setupTestDNSRecordService(t, c)
	nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{
		DNSZones: []string{"/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-1"},
	}}
	vpcSvc := &pkgmock.MockVPCServiceProvider{}
	vpcSvc.On("GetVPCNetworkConfigByNamespace", mock.Anything).Return(nc, nil)
	dnsSvc.VPCService = vpcSvc
	r := &DNSRecordReconciler{Client: c, Service: dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord")}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	result, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.Len(t, store.ListDNSRecords(), 1)

	nc.Spec.DNSZones = []string{"/invalid/zone/path"}
	result, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.Empty(t, store.ListDNSRecords())
	require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
	condition := meta.FindStatusCondition(obj.Status.Conditions, string(v1alpha1.Ready))
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, ReasonDNSRecordValidationFailed, condition.Reason)
}

func TestDNSRecordMissingNetworkConfigRemovesExistingRecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "named config deleted"},
		{name: "default config deleted", err: errors.New("failed to locate default NetworkConfig")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			obj := &v1alpha1.DNSRecord{
				ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default", Generation: 1},
				Spec: v1alpha1.DNSRecordSpec{DomainName: "example.com", RecordName: "api",
					RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"}},
			}
			c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj).Build()
			dnsSvc, store := setupTestDNSRecordService(t, c)
			r := &DNSRecordReconciler{Client: c, Service: dnsSvc,
				StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord")}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
			_, err := r.Reconcile(ctx, req)
			require.NoError(t, err)
			require.Len(t, store.ListDNSRecords(), 1)

			vpcSvc := &pkgmock.MockVPCServiceProvider{}
			vpcSvc.On("GetVPCNetworkConfigByNamespace", mock.Anything).Return((*v1alpha1.VPCNetworkConfiguration)(nil), tc.err)
			dnsSvc.VPCService = vpcSvc
			result, err := r.Reconcile(ctx, req)
			require.NoError(t, err)
			require.Equal(t, ResultNormal, result)
			require.Empty(t, store.ListDNSRecords())
		})
	}
}

func TestDNSRecordInvalidAddressRemovesExistingRecord(t *testing.T) {
	ctx := context.Background()
	obj := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default", Generation: 1},
		Spec: v1alpha1.DNSRecordSpec{DomainName: "example.com", RecordName: "api",
			RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"}},
	}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj).Build()
	dnsSvc, store := setupTestDNSRecordService(t, c)
	r := &DNSRecordReconciler{Client: c, Service: dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord")}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Len(t, store.ListDNSRecords(), 1)
	require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
	obj.Spec.RecordValues = []string{"2001:db8::1"}
	require.NoError(t, c.Update(ctx, obj))
	result, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.Empty(t, store.ListDNSRecords())
}

func TestValidateRecordValues(t *testing.T) {
	for _, tc := range []struct {
		name       string
		recordType v1alpha1.DNSRecordType
		values     []string
		valid      bool
	}{
		{"A", v1alpha1.DNSRecordTypeA, []string{"192.0.2.1"}, true},
		{"AAAA", v1alpha1.DNSRecordTypeAAAA, []string{"2001:db8::1"}, true},
		{"distinct A", v1alpha1.DNSRecordTypeA, []string{"192.0.2.1", "192.0.2.2"}, true},
		{"empty", v1alpha1.DNSRecordTypeA, nil, false},
		{"duplicate A", v1alpha1.DNSRecordTypeA, []string{"192.0.2.1", "192.0.2.1"}, false},
		{"duplicate canonical AAAA", v1alpha1.DNSRecordTypeAAAA, []string{"2001:db8::1", "2001:0db8:0:0:0:0:0:1"}, false},
		{"bad A", v1alpha1.DNSRecordTypeA, []string{"2001:db8::1"}, false},
		{"bad AAAA", v1alpha1.DNSRecordTypeAAAA, []string{"192.0.2.1"}, false},
		{"mapped IPv4", v1alpha1.DNSRecordTypeAAAA, []string{"::ffff:192.0.2.1"}, false},
		{"scoped IPv6", v1alpha1.DNSRecordTypeAAAA, []string{"fe80::1%eth0"}, false},
		{"not an address", v1alpha1.DNSRecordTypeA, []string{"example.com"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRecordValues(tc.recordType, tc.values)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestValidateRecordFQDN(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fqdn  string
		valid bool
	}{
		{"ordinary", "api.example.com", true},
		{"apex", "example.com", true},
		{"unicode", "api.测试.example", true},
		{"underscore", "_api.example.com", true},
		{"underscore with unicode domain", "_api.测试.example", true},
		{"wildcard", "*.example.com", false},
		{"embedded wildcard with underscore", "a_*.example.com", false},
		{"space with underscore", "a_ b.example.com", false},
		{"slash with underscore", "a_/b.example.com", false},
		{"leading hyphen with underscore", "-a_b.example.com", false},
		{"trailing hyphen with underscore", "a_b-.example.com", false},
		{"empty", "", false},
		{"empty label", "api..example.com", false},
		{"long label", strings.Repeat("a", 64) + ".example.com", false},
		{"long fqdn", strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62) + ".com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRecordFQDN(tc.fqdn)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestDNSRecordMovesWhenZonePathChangesWithoutDomainChange(t *testing.T) {
	ctx := context.Background()
	zoneA := "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-1"
	zoneB := "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-2"
	obj := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default", Generation: 1},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName: "example.com", RecordName: "api",
			RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj).Build()
	dnsSvc, store := setupTestDNSRecordService(t, c)
	dnsSvc.DNSZoneMap = dns.NewDNSZoneCacheFromMap(map[string]string{zoneA: "example.com", zoneB: "example.com"})
	nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{zoneA}}}
	vpcSvc := &pkgmock.MockVPCServiceProvider{}
	vpcSvc.On("GetVPCNetworkConfigByNamespace", mock.Anything).Return(nc, nil)
	dnsSvc.VPCService = vpcSvc
	r := &DNSRecordReconciler{
		Client:        c,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Len(t, store.ListDNSRecords(), 1)
	require.Equal(t, zoneA, *store.ListDNSRecords()[0].ZonePath)

	nc.Spec.DNSZones = []string{zoneB}
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Len(t, store.ListDNSRecords(), 1)
	require.Equal(t, zoneB, *store.ListDNSRecords()[0].ZonePath)
}

func TestDNSRecordInvalidCleanupFailureRetries(t *testing.T) {
	ctx := context.Background()
	obj := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default", Generation: 1},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName: "example.com", RecordName: "api",
			RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj).Build()
	dnsSvc, store := setupTestDNSRecordService(t, c)
	r := &DNSRecordReconciler{
		Client:        c,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Len(t, store.ListDNSRecords(), 1)
	require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
	obj.Spec.RecordName = "*"
	obj.Generation++
	require.NoError(t, c.Update(ctx, obj))

	patchClient := orgrootmocks.NewMockOrgRootClient(gomock.NewController(t))
	gomock.InOrder(
		patchClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(errors.New("NSX unavailable")),
		patchClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil),
	)
	dnsSvc.NSXClient.OrgRootClient = patchClient
	result, err := r.Reconcile(ctx, req)
	require.ErrorContains(t, err, "NSX unavailable")
	require.Equal(t, ResultRequeue, result)
	require.Len(t, store.ListDNSRecords(), 1)

	result, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	require.Empty(t, store.ListDNSRecords())
}

func TestDNSRecordTransientZoneLookupFailureKeepsExistingRecord(t *testing.T) {
	ctx := context.Background()
	obj := &v1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: "default", Generation: 1},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName: "example.com", RecordName: "api",
			RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(obj).Build()
	dnsSvc, store := setupTestDNSRecordService(t, c)
	r := &DNSRecordReconciler{
		Client:        c,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Len(t, store.ListDNSRecords(), 1)

	vpcSvc := &pkgmock.MockVPCServiceProvider{}
	vpcSvc.On("GetVPCNetworkConfigByNamespace", mock.Anything).Return((*v1alpha1.VPCNetworkConfiguration)(nil), errors.New("VPC lookup unavailable"))
	dnsSvc.VPCService = vpcSvc
	result, err := r.Reconcile(ctx, req)
	require.ErrorContains(t, err, "failed to find VPCNetworkConfiguration")
	require.Equal(t, ResultRequeue, result)
	require.Len(t, store.ListDNSRecords(), 1)
}

func TestDNSRecordNameConflictRetriesAfterOwnerDeletion(t *testing.T) {
	ctx := context.Background()
	newRecord := func(name, recordName string) *v1alpha1.DNSRecord {
		return &v1alpha1.DNSRecord{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Generation: 1},
			Spec: v1alpha1.DNSRecordSpec{
				DomainName: "example.com", RecordName: recordName,
				RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"192.0.2.1"},
			},
		}
	}
	holder := newRecord("holder", "taken")
	contender := newRecord("contender", "old")
	c := fake.NewClientBuilder().WithScheme(setupScheme()).WithStatusSubresource(&v1alpha1.DNSRecord{}).WithObjects(holder, contender).Build()
	dnsSvc, store := setupTestDNSRecordService(t, c)
	r := &DNSRecordReconciler{
		Client:        c,
		Service:       dnsSvc,
		StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
	}
	holderReq := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(holder)}
	contenderReq := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(contender)}
	_, err := r.Reconcile(ctx, holderReq)
	require.NoError(t, err)
	_, err = r.Reconcile(ctx, contenderReq)
	require.NoError(t, err)
	require.Len(t, store.ListDNSRecords(), 2)

	require.NoError(t, c.Get(ctx, contenderReq.NamespacedName, contender))
	contender.Spec.RecordName = "taken"
	contender.Generation++
	require.NoError(t, c.Update(ctx, contender))
	result, err := r.Reconcile(ctx, contenderReq)
	require.ErrorContains(t, err, "already owned")
	require.Equal(t, ResultRequeue, result)
	require.Len(t, store.ListDNSRecords(), 2, "the old name must remain while the new name is occupied")
	require.NoError(t, c.Get(ctx, contenderReq.NamespacedName, contender))
	condition := meta.FindStatusCondition(contender.Status.Conditions, string(v1alpha1.Ready))
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, ReasonDNSRecordValidationFailed, condition.Reason)

	require.NoError(t, c.Delete(ctx, holder))
	_, err = r.Reconcile(ctx, holderReq)
	require.NoError(t, err)
	result, err = r.Reconcile(ctx, contenderReq)
	require.NoError(t, err)
	require.Equal(t, ResultNormal, result)
	records := store.ListDNSRecords()
	require.Len(t, records, 1)
	require.Equal(t, "taken", *records[0].RecordName)
	require.NoError(t, c.Get(ctx, contenderReq.NamespacedName, contender))
	condition = meta.FindStatusCondition(contender.Status.Conditions, string(v1alpha1.Ready))
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
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
				Service:       &dns.DNSRecordService{DNSRecordStore: dns.BuildDNSRecordStore()},
				StatusUpdater: ctlcommon.NewStatusUpdater(c, &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}, record.NewFakeRecorder(100), ctlcommon.MetricResTypeDNSRecord, "DNSRecord", "DNSRecord"),
			}
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
			require.NoError(t, err)
			require.Equal(t, ResultNormal, result)
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
			condition := meta.FindStatusCondition(obj.Status.Conditions, string(v1alpha1.Ready))
			require.NotNil(t, condition)
			require.Equal(t, metav1.ConditionFalse, condition.Status)
			require.Equal(t, ReasonDNSRecordValidationFailed, condition.Reason)
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
		Client:    c,
		APIReader: c,
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
