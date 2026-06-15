/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	dnsrecmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordsclient"
	dnszonemocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnszonesclient"
	orgrootmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	realizedmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/realizedentitiesclient"
	searchmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/searchclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

var _ servicecommon.VPCServiceProvider = (*pkgmock.MockVPCServiceProvider)(nil)

// Policy DNS zone paths must match projectDNSZonePathRe in zones.go:
const (
	testDNSZonePathT      = "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-t"
	testDNSZonePathGw     = "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-gw"
	testDNSZonePathZ      = "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-z"
	testDNSZonePathShared = "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-shared"
	testDNSZonePathDemo   = "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-demo"
	testDNSZonePathOld    = "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-old"
	testDNSZonePathKeep   = "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-keep"
	testDNSZonePathOther  = "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-other"
)

func testVPCNetworkConfiguration() *v1alpha1.VPCNetworkConfiguration {
	return &v1alpha1.VPCNetworkConfiguration{
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			DNSZones: []string{testDNSZonePathT},
		},
		Status: v1alpha1.VPCNetworkConfigurationStatus{
			VPCs: []v1alpha1.VPCInfo{{
				VPCPath: "/orgs/org1/projects/proj1/vpcs/vpc1",
			}},
		},
	}
}

// testDNSZoneMapForVPCFixture maps Spec.DNSZones paths to delegated DNS domain names (avoids live NSX Get in tests).
func testDNSZoneMapForVPCFixture() map[string]string {
	return map[string]string{
		testDNSZonePathT:      "example.com",
		testDNSZonePathGw:     "example.com",
		testDNSZonePathZ:      "example.com",
		testDNSZonePathShared: "example.com",
		testDNSZonePathDemo:   "demo.example.com",
		testDNSZonePathOld:    "example.com",
		testDNSZonePathKeep:   "example.com",
		testDNSZonePathOther:  "example.com",
	}
}

// testDNSSvc bundles a DNSRecordService with the NSX mock's body-registry so each test
// is hermetic: no package-level state is shared between test cases.
type testDNSSvc struct {
	*DNSRecordService
	bodies map[string]*model.DnsRecord
}

// registerBodiesForBatch pre-populates the mock Get registry with the records that
// would be returned by NSX after a successful PATCH for each row in batch.
func registerBodiesForBatch(t *testing.T, env *testDNSSvc, batch *AggregatedDNSEndpoints) {
	t.Helper()
	if batch == nil || batch.Owner == nil {
		return
	}
	for _, row := range batch.Rows {
		rec := env.BuildProjectDnsRecord(batch.Owner, row)
		if rec == nil || rec.Id == nil {
			continue
		}
		cp := *rec
		env.bodies[*rec.Id] = &cp
	}
}

func recordStoreKey(zonePath, recordName, recordType string) string {
	_, path, _ := getRecordIDAndPathAndType(recordName, recordType, zonePath)
	return path
}

func newTestNSXClient(t *testing.T, bodies map[string]*model.DnsRecord) *nsx.Client {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

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
			p := fmt.Sprintf("/orgs/%s/projects/%s/%s/%s", orgID, projectID, DNSRecordPathSegment, recordID)
			if d := bodies[recordID]; d != nil {
				out := *d
				out.Id = &recordID
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

func newTestDNSRecordService(t *testing.T, store *RecordStore) *testDNSSvc {
	t.Helper()
	bodies := make(map[string]*model.DnsRecord)
	fc := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build()
	builder, err := servicecommon.PolicyPathDnsRecord.NewPolicyTreeBuilder()
	require.NoError(t, err)
	svc := &DNSRecordService{
		Service: servicecommon.Service{
			Client:    fc,
			NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}},
			NSXClient: newTestNSXClient(t, bodies),
		},
		DNSRecordStore:   store,
		DnsRecordBuilder: builder,
	}
	return &testDNSSvc{DNSRecordService: svc, bodies: bodies}
}

func requireNoErrCreateDNS(ctx context.Context, t *testing.T, env *testDNSSvc, batch *AggregatedDNSEndpoints) {
	t.Helper()
	registerBodiesForBatch(t, env, batch)
	_, err := env.CreateOrUpdateRecords(ctx, batch)
	require.NoError(t, err)
}

func requireNoErrDeleteDNS(ctx context.Context, t *testing.T, env *testDNSSvc, kind, ns, name string) {
	t.Helper()
	toUpdate, toDelete, _ := env.calculateRecordsForDeletion(kind, ns, name)
	for _, rec := range toUpdate {
		cp := *rec
		env.bodies[*rec.Id] = &cp
	}
	for _, rec := range toDelete {
		delete(env.bodies, *rec.Id)
	}

	_, err := env.DNSRecordService.DeleteRecordByOwnerNN(ctx, kind, ns, name)
	require.NoError(t, err)
}

func TestCreateOrUpdateRecords_ownerScopedEmptyRowsPrunes(t *testing.T) {
	ctx := context.Background()
	store := BuildDNSRecordStore()
	env := newTestDNSRecordService(t, store)
	owner := &ResourceRef{
		Kind:   ResourceKindGateway,
		Object: &metav1.ObjectMeta{Namespace: "ns", Name: "gw", UID: types.UID("gwuid")},
	}
	ep := extdns.NewEndpoint("gw.example.com", extdns.RecordTypeA, "192.0.2.1")
	ep.WithLabel(EndpointLabelParentGateway, "ns/gw")
	row := EndpointRow{Endpoint: ep, zonePath: testDNSZonePathGw, nsxRecordName: "gw"}

	requireNoErrCreateDNS(ctx, t, env, NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{row}))
	key := recordStoreKey(row.zonePath, row.nsxRecordName, row.Endpoint.RecordType)
	require.NotNil(t, store.GetByKey(key))

	requireNoErrCreateDNS(ctx, t, env, &AggregatedDNSEndpoints{
		Namespace: "ns",
		Owner:     owner,
		Rows:      nil,
	})
	require.Nil(t, store.GetByKey(key))
}

func TestValidateEndpointsByZone_table(t *testing.T) {
	intPtr := func(n int) *int { return &n }
	ep := extdns.NewEndpoint("svc.example.com", extdns.RecordTypeA, "10.0.0.1")
	ep.WithLabel(EndpointLabelParentGateway, "ns/gw")
	tableOwner := func(ns string) *ResourceRef {
		return &ResourceRef{
			Kind:   ResourceKindGateway,
			Object: &metav1.ObjectMeta{Namespace: ns, Name: "gw-table", UID: "zone-table"},
		}
	}

	baseSvc := func(vpc servicecommon.VPCServiceProvider) *DNSRecordService {
		return &DNSRecordService{
			Service: servicecommon.Service{
				NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}},
			},
			VPCService:     vpc,
			DNSZoneMap:     NewDNSZoneCacheFromMap(testDNSZoneMapForVPCFixture()),
			DNSRecordStore: BuildDNSRecordStore(),
		}
	}
	buildVPC := func(t *testing.T, nc *v1alpha1.VPCNetworkConfiguration, retErr error) servicecommon.VPCServiceProvider {
		m := &pkgmock.MockVPCServiceProvider{}
		m.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(nc, retErr).Once()
		t.Cleanup(func() { m.AssertExpectations(t) })
		return m
	}

	ncWithoutDNSZones := testVPCNetworkConfiguration()
	ncWithoutDNSZones.Spec.DNSZones = nil

	tests := []struct {
		name             string
		nc               *v1alpha1.VPCNetworkConfiguration
		vpcErr           error
		buildOwner       func(ns string) *ResourceRef // nil → tableOwner(ns)
		ns               string
		eps              []*extdns.Endpoint
		wantZone         string
		errSub           string
		wantZoneValErr   bool              // error must be *DNSZoneValidationError
		wantAllowedOnErr map[string]string // non-nil: allowedZones must equal this on error
		wantN            *int              // when non-nil, assert len(rows)==*wantN instead of len(eps)
	}{
		{
			name: "happy_path",
			nc:   testVPCNetworkConfiguration(),
			ns:   "tenant", eps: []*extdns.Endpoint{ep}, wantZone: testDNSZonePathT,
			wantN: intPtr(1),
		},
		{
			name: "no_DNS_zones_in_VPC_spec",
			nc:   ncWithoutDNSZones,
			ns:   "n", eps: []*extdns.Endpoint{ep}, errSub: "no DNS zones are permitted for the namespace",
			wantZoneValErr: true,
		},
		{
			name:   "GetVPCNetworkConfigByNamespace_error",
			nc:     nil,
			vpcErr: errors.New("vpc down"),
			ns:     "n", eps: []*extdns.Endpoint{ep}, errSub: "failed to find VPCNetworkConfiguration",
		},
		{
			name:  "wildcard_dns_name_skipped",
			nc:    testVPCNetworkConfiguration(),
			ns:    "tenant",
			eps:   []*extdns.Endpoint{extdns.NewEndpoint("*.apps.example.com", extdns.RecordTypeA, "10.0.0.1")},
			wantN: intPtr(0),
		},
		{
			name: "wildcard_skipped_concrete_hostname_kept",
			nc:   testVPCNetworkConfiguration(),
			ns:   "tenant",
			eps: []*extdns.Endpoint{
				extdns.NewEndpoint("*.apps.example.com", extdns.RecordTypeA, "10.0.0.2"),
				ep,
			},
			wantZone: testDNSZonePathT,
			wantN:    intPtr(1),
		},
		{
			name: "unsupported_owner_kind_returns_validation_error",
			nc:   testVPCNetworkConfiguration(),
			buildOwner: func(ns string) *ResourceRef {
				return &ResourceRef{Kind: "UnknownKind", Object: &metav1.ObjectMeta{Namespace: ns, Name: "x"}}
			},
			ns:               "tenant",
			eps:              []*extdns.Endpoint{ep},
			errSub:           "unsupported resource kind",
			wantZoneValErr:   true,
			wantAllowedOnErr: map[string]string{testDNSZonePathT: "example.com"},
			wantN:            intPtr(0),
		},
		{
			// hostname does not lie under any allowed zone → must be *DNSZoneValidationError
			// and allowedZones must still be returned so the controller can clean up stale records.
			name:             "hostname_not_in_zone_is_DNSZoneValidationError_with_allowedZones",
			nc:               testVPCNetworkConfiguration(), // zone = example.com
			ns:               "tenant",
			eps:              []*extdns.Endpoint{extdns.NewEndpoint("svc.other.example", extdns.RecordTypeA, "10.0.0.1")},
			errSub:           "does not match any allowed DNS domain",
			wantZoneValErr:   true,
			wantAllowedOnErr: map[string]string{testDNSZonePathT: "example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := baseSvc(buildVPC(t, tt.nc, tt.vpcErr))
			owner := tableOwner(tt.ns)
			if tt.buildOwner != nil {
				owner = tt.buildOwner(tt.ns)
			}
			rows, allowed, err := svc.ValidateEndpointsByZone(tt.ns, owner, tt.eps)
			if tt.errSub != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errSub)
				if tt.wantZoneValErr {
					var zve *DNSZoneValidationError
					require.ErrorAs(t, err, &zve, "expected *DNSZoneValidationError")
				}
				if tt.wantAllowedOnErr != nil {
					require.Equal(t, tt.wantAllowedOnErr, allowed, "allowedZones must be returned even on error")
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, map[string]string{testDNSZonePathT: "example.com"}, allowed)
			if tt.wantN != nil {
				require.Len(t, rows, *tt.wantN)
				if *tt.wantN > 0 {
					require.Equal(t, tt.wantZone, rows[0].zonePath)
					require.Equal(t, "svc.example.com", rows[0].DNSName)
				}
				return
			}
			require.Len(t, rows, len(tt.eps))
			require.Equal(t, tt.wantZone, rows[0].zonePath)
		})
	}
}

func TestDNSRecordService_deletesAndQueries_table(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, env *testDNSSvc)
	}{
		{
			name: "DeleteRecordByOwnerNN_gateway_removes_row_and_gateway_index",
			run: func(t *testing.T, env *testDNSSvc) {
				owner := &ResourceRef{
					Kind:   ResourceKindGateway,
					Object: &metav1.ObjectMeta{Namespace: "app", Name: "gw1", UID: types.UID("g1")},
				}
				z := testDNSZonePathZ
				ep := extdns.NewEndpoint("gw.example.com", extdns.RecordTypeA, "10.0.0.1")
				ep.WithLabel(EndpointLabelParentGateway, "app/gw1")
				row := EndpointRow{Endpoint: ep, zonePath: z, nsxRecordName: "gw.example.com"}
				require.NotNil(t, env.BuildProjectDnsRecord(owner, row))
				requireNoErrCreateDNS(ctx, t, env, NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{row}))
				key := recordStoreKey(row.zonePath, row.nsxRecordName, row.Endpoint.RecordType)
				require.NotNil(t, env.DNSRecordStore.GetByKey(key))
				requireNoErrDeleteDNS(ctx, t, env, ResourceKindGateway, "app", "gw1")
				require.Nil(t, env.DNSRecordStore.GetByKey(key))
			},
		},
		{
			name: "DeleteRecordByOwnerNN_gateway_does_not_remove_HTTPRoute_rows",
			run: func(t *testing.T, env *testDNSSvc) {
				gwOwner := &ResourceRef{
					Kind:   ResourceKindGateway,
					Object: &metav1.ObjectMeta{Namespace: "app", Name: "gw1", UID: types.UID("g1")},
				}
				z := testDNSZonePathZ
				epG := extdns.NewEndpoint("gw.example.com", extdns.RecordTypeA, "10.0.0.1")
				epG.WithLabel(EndpointLabelParentGateway, "app/gw1")
				rowG := EndpointRow{Endpoint: epG, zonePath: z, nsxRecordName: "gw.example.com"}
				require.NotNil(t, env.BuildProjectDnsRecord(gwOwner, rowG))
				requireNoErrCreateDNS(ctx, t, env, NewOwnerScopedAggregatedRouteDNS(gwOwner, []EndpointRow{rowG}))
				hrEp := extdns.NewEndpoint("r.example.com", extdns.RecordTypeA, "10.0.0.2")
				hrEp.WithLabel(EndpointLabelParentGateway, "app/gw1")
				hrRow := NewEndpointRow(hrEp, z, "r.example.com")
				hrOwner := &ResourceRef{
					Kind:   ResourceKindHTTPRoute,
					Object: &metav1.ObjectMeta{Namespace: "app", Name: "hr1", UID: types.UID("u-hr")},
				}
				require.NotNil(t, env.BuildProjectDnsRecord(hrOwner, *hrRow))
				requireNoErrCreateDNS(ctx, t, env, NewOwnerScopedAggregatedRouteDNS(hrOwner, []EndpointRow{*hrRow}))
				requireNoErrDeleteDNS(ctx, t, env, ResourceKindGateway, "app", "gw1")
				require.Nil(t, env.DNSRecordStore.GetByKey(recordStoreKey(rowG.zonePath, rowG.nsxRecordName, rowG.Endpoint.RecordType)))
				require.NotNil(t, env.DNSRecordStore.GetByKey(recordStoreKey(hrRow.zonePath, hrRow.nsxRecordName, hrRow.Endpoint.RecordType)))
			},
		},
		{
			name: "ListRecordOwnerResource_maps_HTTPRoute_owner",
			run: func(t *testing.T, env *testDNSSvc) {
				ns := "demo"
				demoEp := extdns.NewEndpoint("svc.demo.example.com", extdns.RecordTypeA, "192.0.2.10")
				demoEp.WithLabel(EndpointLabelParentGateway, "demo/gw1")
				row := NewEndpointRow(demoEp, testDNSZonePathDemo, "svc.demo.example.com")
				hrOwner := &ResourceRef{
					Kind:   ResourceKindHTTPRoute,
					Object: &metav1.ObjectMeta{Namespace: ns, Name: "hr1", UID: types.UID("uid-hr1")},
				}
				require.NotNil(t, env.BuildProjectDnsRecord(hrOwner, *row))
				requireNoErrCreateDNS(ctx, t, env, NewOwnerScopedAggregatedRouteDNS(hrOwner, []EndpointRow{*row}))
				require.True(t, env.ListReferredGatewayNN().Has(types.NamespacedName{Namespace: "demo", Name: "gw1"}))
				groups := env.ListRecordOwnerResource()
				nn := types.NamespacedName{Namespace: ns, Name: "hr1"}
				require.True(t, groups[ResourceKindHTTPRoute].Has(nn), "got map %#v", groups)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestDNSRecordService(t, BuildDNSRecordStore())
			tt.run(t, env)
		})
	}
}

// dnsRecordStructValue converts rec to a *data.StructValue for use in mock SearchResponse.Results.
func dnsRecordStructValue(t *testing.T, rec model.DnsRecord) *data.StructValue {
	t.Helper()
	dv, errs := servicecommon.NewConverter().ConvertToVapi(rec, model.DnsRecordBindingType())
	require.Empty(t, errs, "ConvertToVapi failed")
	sv, ok := dv.(*data.StructValue)
	require.True(t, ok, "expected *data.StructValue from ConvertToVapi")
	return sv
}

func TestInitializeDNSRecordService_table(t *testing.T) {
	cfg := &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}}
	fc := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build()
	vpcM := &pkgmock.MockVPCServiceProvider{}

	// A minimal record whose ZonePath falls into testDNSZonePathT.
	recWithZone := model.DnsRecord{
		Id:       servicecommon.String("rec-warmup"),
		Path:     servicecommon.String("/orgs/org1/projects/proj1/dns-records/rec-warmup"),
		ZonePath: servicecommon.String(testDNSZonePathT),
	}

	tests := []struct {
		name      string
		setupMock func(t *testing.T, ctrl *gomock.Controller) *nsx.Client
		wantErr   bool
		checkSvc  func(t *testing.T, svc *DNSRecordService)
	}{
		{
			name: "store_search_error_propagates",
			setupMock: func(t *testing.T, ctrl *gomock.Controller) *nsx.Client {
				qc := searchmocks.NewMockQueryClient(ctrl)
				qc.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(model.SearchResponse{}, errors.New("nsx search down")).Times(1)
				return &nsx.Client{QueryClient: qc, NsxConfig: cfg}
			},
			wantErr: true,
		},
		{
			name: "empty_store_no_zone_warmup",
			setupMock: func(t *testing.T, ctrl *gomock.Controller) *nsx.Client {
				qc := searchmocks.NewMockQueryClient(ctrl)
				rc := int64(0)
				qc.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(model.SearchResponse{Results: nil, Cursor: nil, ResultCount: &rc}, nil).Times(1)
				// DnsZoneClient.Get must NOT be called: no records, no warm-up.
				zc := dnszonemocks.NewMockZonesClient(ctrl)
				return &nsx.Client{QueryClient: qc, DnsZoneClient: zc, NsxConfig: cfg}
			},
			checkSvc: func(t *testing.T, svc *DNSRecordService) {
				require.NotNil(t, svc.DNSRecordStore)
				require.NotNil(t, svc.VPCService)
				require.NotNil(t, svc.DnsRecordBuilder)
				require.NotNil(t, svc.DNSZoneMap)
				_, found := svc.DNSZoneMap.get(testDNSZonePathT)
				require.False(t, found, "zone map must be empty when store is empty")
			},
		},
		{
			name: "warmup_zone_domain_populated",
			setupMock: func(t *testing.T, ctrl *gomock.Controller) *nsx.Client {
				sv := dnsRecordStructValue(t, recWithZone)
				rc := int64(1)
				qc := searchmocks.NewMockQueryClient(ctrl)
				qc.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(model.SearchResponse{Results: []*data.StructValue{sv}, Cursor: nil, ResultCount: &rc}, nil).Times(1)
				domain := "example.com"
				zc := dnszonemocks.NewMockZonesClient(ctrl)
				zc.EXPECT().Get("org1", "proj1", "ds1", "zone-t").Return(model.DnsZone{DnsDomainName: &domain}, nil).Times(1)
				return &nsx.Client{QueryClient: qc, DnsZoneClient: zc, NsxConfig: cfg}
			},
			checkSvc: func(t *testing.T, svc *DNSRecordService) {
				got, found := svc.DNSZoneMap.get(testDNSZonePathT)
				require.True(t, found, "zone path must be in DNSZoneMap after successful warm-up")
				require.Equal(t, "example.com", got)
			},
		},
		{
			name: "warmup_zone_fetch_error_nonfatal",
			setupMock: func(t *testing.T, ctrl *gomock.Controller) *nsx.Client {
				sv := dnsRecordStructValue(t, recWithZone)
				rc := int64(1)
				qc := searchmocks.NewMockQueryClient(ctrl)
				qc.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(model.SearchResponse{Results: []*data.StructValue{sv}, Cursor: nil, ResultCount: &rc}, nil).Times(1)
				zc := dnszonemocks.NewMockZonesClient(ctrl)
				zc.EXPECT().Get("org1", "proj1", "ds1", "zone-t").Return(model.DnsZone{}, errors.New("nsx zone unavailable")).Times(1)
				return &nsx.Client{QueryClient: qc, DnsZoneClient: zc, NsxConfig: cfg}
			},
			checkSvc: func(t *testing.T, svc *DNSRecordService) {
				_, found := svc.DNSZoneMap.get(testDNSZonePathT)
				require.False(t, found, "zone map must not be populated when zone fetch errors")
			},
		},
		{
			name: "warmup_nil_domain_name_not_cached",
			setupMock: func(t *testing.T, ctrl *gomock.Controller) *nsx.Client {
				sv := dnsRecordStructValue(t, recWithZone)
				rc := int64(1)
				qc := searchmocks.NewMockQueryClient(ctrl)
				qc.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(model.SearchResponse{Results: []*data.StructValue{sv}, Cursor: nil, ResultCount: &rc}, nil).Times(1)
				zc := dnszonemocks.NewMockZonesClient(ctrl)
				zc.EXPECT().Get("org1", "proj1", "ds1", "zone-t").Return(model.DnsZone{DnsDomainName: nil}, nil).Times(1)
				return &nsx.Client{QueryClient: qc, DnsZoneClient: zc, NsxConfig: cfg}
			},
			checkSvc: func(t *testing.T, svc *DNSRecordService) {
				_, found := svc.DNSZoneMap.get(testDNSZonePathT)
				require.False(t, found, "zone map must not be populated when DnsDomainName is nil")
			},
		},
		{
			name: "warmup_dedup_same_zone_path_get_once",
			setupMock: func(t *testing.T, ctrl *gomock.Controller) *nsx.Client {
				rec2 := model.DnsRecord{
					Id:       servicecommon.String("rec-warmup-2"),
					Path:     servicecommon.String("/orgs/org1/projects/proj1/dns-records/rec-warmup-2"),
					ZonePath: servicecommon.String(testDNSZonePathT),
				}
				sv1 := dnsRecordStructValue(t, recWithZone)
				sv2 := dnsRecordStructValue(t, rec2)
				rc := int64(2)
				qc := searchmocks.NewMockQueryClient(ctrl)
				qc.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(model.SearchResponse{Results: []*data.StructValue{sv1, sv2}, Cursor: nil, ResultCount: &rc}, nil).Times(1)
				domain := "example.com"
				// Even though two records share the same zone path, Get must be called exactly once.
				zc := dnszonemocks.NewMockZonesClient(ctrl)
				zc.EXPECT().Get("org1", "proj1", "ds1", "zone-t").Return(model.DnsZone{DnsDomainName: &domain}, nil).Times(1)
				return &nsx.Client{QueryClient: qc, DnsZoneClient: zc, NsxConfig: cfg}
			},
			checkSvc: func(t *testing.T, svc *DNSRecordService) {
				got, found := svc.DNSZoneMap.get(testDNSZonePathT)
				require.True(t, found)
				require.Equal(t, "example.com", got)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			svc, err := InitializeDNSRecordService(servicecommon.Service{
				Client:    fc,
				NSXConfig: cfg,
				NSXClient: tt.setupMock(t, ctrl),
			}, vpcM)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, svc)
			if tt.checkSvc != nil {
				tt.checkSvc(t, svc)
			}
		})
	}
}

func TestCreateOrUpdateRecords_ownerScoped_mergeUpdatesTargets(t *testing.T) {
	ctx := context.Background()
	store := BuildDNSRecordStore()
	env := newTestDNSRecordService(t, store)
	owner := &ResourceRef{
		Kind:   ResourceKindGateway,
		Object: &metav1.ObjectMeta{Namespace: "ns", Name: "gw", UID: types.UID("gwuid")},
	}
	z := testDNSZonePathZ
	recordName := "x.example.com"
	ep0 := extdns.NewEndpoint("x.example.com", extdns.RecordTypeA, "10.0.0.1")
	ep0.WithLabel(EndpointLabelParentGateway, "ns/gw")
	require.NotNil(t, env.BuildProjectDnsRecord(owner, EndpointRow{Endpoint: ep0, zonePath: z, nsxRecordName: recordName}))
	for _, targets := range [][]string{{"10.0.0.1"}, {"10.0.0.2"}} {
		ep := extdns.NewEndpoint("x.example.com", extdns.RecordTypeA, targets...)
		ep.WithLabel(EndpointLabelParentGateway, "ns/gw")
		row := EndpointRow{Endpoint: ep, zonePath: z, nsxRecordName: recordName}
		requireNoErrCreateDNS(ctx, t, env, NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{row}))
	}
	rec := store.GetByKey(recordStoreKey(testDNSZonePathZ, recordName, extdns.RecordTypeA))
	require.NotNil(t, rec)
	vals := append([]string(nil), rec.RecordValues...)
	require.Equal(t, []string{"10.0.0.2"}, vals)
}

func Test_CreateOrUpdateRecords_Service_DeleteByOwnerNN(t *testing.T) {
	ctx := context.Background()
	store := BuildDNSRecordStore()
	env := newTestDNSRecordService(t, store)
	meta := metav1.ObjectMeta{Name: "lbsvc", Namespace: "ns1", UID: types.UID("svc-uid-1")}
	owner := &ResourceRef{Kind: ResourceKindService, Object: &meta}
	z := testDNSZonePathZ
	ep := extdns.NewEndpoint("x.example.com", extdns.RecordTypeA, "10.0.0.1")
	ep.WithLabel(EndpointLabelParentGateway, "ns1/lbsvc")
	row := EndpointRow{Endpoint: ep, zonePath: z, nsxRecordName: "x.example.com"}
	require.NotNil(t, env.BuildProjectDnsRecord(owner, row))
	requireNoErrCreateDNS(ctx, t, env, NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{row}))
	require.Len(t, env.DNSRecordStore.GetByOwnerResourceNamespacedName(ResourceKindService, "ns1", "lbsvc"), 1)
	requireNoErrDeleteDNS(ctx, t, env, ResourceKindService, "ns1", "lbsvc")
	assert.Empty(t, env.DNSRecordStore.GetByOwnerResourceNamespacedName(ResourceKindService, "ns1", "lbsvc"))
}

func TestParseDnsRecordPolicyPath_table(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantOrg string
		wantPrj string
		wantID  string
		errSub  string
	}{
		{
			name:    "valid",
			path:    "/orgs/acme/projects/p1/dns-records/rec-a",
			wantOrg: "acme",
			wantPrj: "p1",
			wantID:  "rec-a",
		},
		{
			name:   "missing_segment",
			path:   "/orgs/acme/projects/p1/projects/rec-a",
			errSub: "invalid DnsRecord path",
		},
		{
			name:   "empty_path",
			path:   "",
			errSub: "empty DnsRecord path",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org, prj, id, err := parseDnsRecordPolicyPath(tt.path)
			if tt.errSub != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errSub)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantOrg, org)
			require.Equal(t, tt.wantPrj, prj)
			require.Equal(t, tt.wantID, id)
		})
	}
}

func TestDedupeRecordsByPath(t *testing.T) {
	p := "/orgs/o/projects/p/dns-records/a"
	a := &model.DnsRecord{Path: servicecommon.String(p), Id: servicecommon.String("a")}
	b := &model.DnsRecord{Path: servicecommon.String(p), Id: servicecommon.String("b")}
	out := dedupeRecordsByPath([]*model.DnsRecord{a, b})
	require.Len(t, out, 1)
}

func TestDeleteDnsRecordOnNSX(t *testing.T) {
	env := newTestDNSRecordService(t, BuildDNSRecordStore())
	p := "/orgs/org1/projects/proj1/dns-records/rec1"
	err := env.deleteDnsRecordOnNSX(&model.DnsRecord{Path: &p})
	require.NoError(t, err)
}

func TestContributingHelpers_table(t *testing.T) {
	t.Run("parseContributingOwnersTag", func(t *testing.T) {
		require.Nil(t, parseContributingOwnersTag(""))
		got := parseContributingOwnersTag(" b/a/x , a/b/y ")
		require.Equal(t, []string{"a/b/y", "b/a/x"}, got)
	})
	t.Run("mergeContributingOwnerKeys", func(t *testing.T) {
		primary := "p"
		got := mergeContributingOwnerKeys(fmt.Sprintf("x,%s,y", primary), "z", primary)
		require.Equal(t, compressString("x,y,z"), got)
	})
	t.Run("parseOwnerNNIndexKey", func(t *testing.T) {
		cf, ns, n, ok := parseOwnerNNIndexKey("httproute/ns1/r1")
		require.True(t, ok)
		require.Equal(t, servicecommon.TagValueDNSRecordForHTTPRoute, cf)
		require.Equal(t, "ns1", ns)
		require.Equal(t, "r1", n)
	})
	t.Run("replaceContributingOwnersInTags", func(t *testing.T) {
		tags := []model.Tag{
			modelTag(servicecommon.TagScopeDNSRecordContributingOwners, compressString("old")),
			modelTag(servicecommon.TagScopeCluster, "c"),
		}
		out := replaceContributingOwnersInTags(tags, []string{"k1", "k2"})
		require.Len(t, out, 2)
		require.ElementsMatch(t, []model.Tag{
			modelTag(servicecommon.TagScopeCluster, "c"),
			modelTag(servicecommon.TagScopeDNSRecordContributingOwners, compressString("k1,k2")),
		}, out)
	})
}

func TestAppendRecordOwnershipTags_table(t *testing.T) {
	clusterTag := modelTag(servicecommon.TagScopeCluster, "cls")
	baseTags := []model.Tag{clusterTag}

	tests := []struct {
		name             string
		gwKey            string
		contributingKeys string
		wantScopes       []string
	}{
		{
			name:       "no gateway no contributing",
			wantScopes: []string{servicecommon.TagScopeCluster},
		},
		{
			name:       "gateway only",
			gwKey:      "ns1/gw1",
			wantScopes: []string{servicecommon.TagScopeCluster, servicecommon.TagScopeDNSRecordGatewayIndexList},
		},
		{
			name:             "contributing only",
			contributingKeys: "http_route/ns/r1",
			wantScopes:       []string{servicecommon.TagScopeCluster, servicecommon.TagScopeDNSRecordContributingOwners},
		},
		{
			name:             "gateway and contributing",
			gwKey:            "ns1/gw1",
			contributingKeys: "http_route/ns/r1,http_route/ns/r2",
			wantScopes:       []string{servicecommon.TagScopeCluster, servicecommon.TagScopeDNSRecordGatewayIndexList, servicecommon.TagScopeDNSRecordContributingOwners},
		},
		{
			name:       "whitespace-only gateway is skipped",
			gwKey:      "   ",
			wantScopes: []string{servicecommon.TagScopeCluster},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// pass a copy so we can verify the original is not mutated
			in := append([]model.Tag{}, baseTags...)
			got := appendGatewayAndContributionTags(in, tc.gwKey, tc.contributingKeys)
			gotScopes := make([]string, 0, len(got))
			for _, tg := range got {
				if tg.Scope != nil {
					gotScopes = append(gotScopes, *tg.Scope)
				}
			}
			require.Equal(t, tc.wantScopes, gotScopes)
			// original baseTags slice must not be mutated
			require.Len(t, baseTags, 1)
		})
	}
}

func TestGetNSXDnsRecordType_table(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{extdns.RecordTypeA, model.DnsRecord_RECORD_TYPE_A},
		{extdns.RecordTypeAAAA, model.DnsRecord_RECORD_TYPE_AAAA},
		{extdns.RecordTypeCNAME, model.DnsRecord_RECORD_TYPE_CNAME},
		{extdns.RecordTypeNS, model.DnsRecord_RECORD_TYPE_NS},
		{extdns.RecordTypePTR, model.DnsRecord_RECORD_TYPE_PTR},
		{"UNKNOWN", model.DnsRecord_RECORD_TYPE_A},
		{"", model.DnsRecord_RECORD_TYPE_A},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			require.Equal(t, tc.want, getNSXDnsRecordType(tc.input))
		})
	}
}

func TestResourceKindFromCreatedForTag_table(t *testing.T) {
	tests := []struct {
		tag  string
		want string
	}{
		{servicecommon.TagValueDNSRecordForHTTPRoute, ResourceKindHTTPRoute},
		{servicecommon.TagValueDNSRecordForGRPCRoute, ResourceKindGRPCRoute},
		{servicecommon.TagValueDNSRecordForTLSRoute, ResourceKindTLSRoute},
		{servicecommon.TagValueDNSRecordForGateway, ResourceKindGateway},
		{servicecommon.TagValueDNSRecordForService, ResourceKindService},
		{"unknown_kind", ""},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.tag, func(t *testing.T) {
			require.Equal(t, tc.want, resourceKindFromCreatedForTag(tc.tag))
		})
	}
}

func TestSortedNormalizedTagsForCompare_table(t *testing.T) {
	contrib := servicecommon.TagScopeDNSRecordContributingOwners
	cluster := servicecommon.TagScopeCluster
	gw := servicecommon.TagScopeDNSRecordGatewayIndexList

	tests := []struct {
		name           string
		tags           []model.Tag
		wantScopes     []string
		wantContribVal string
	}{
		{
			name:       "nil tags returns empty",
			tags:       nil,
			wantScopes: []string{},
		},
		{
			name:       "already sorted is unchanged",
			tags:       []model.Tag{modelTag(cluster, "c"), modelTag(gw, "v")},
			wantScopes: []string{cluster, gw},
		},
		{
			name:       "unsorted tags are sorted by scope",
			tags:       []model.Tag{modelTag(gw, "v"), modelTag(cluster, "c"), modelTag(contrib, "a/y,b/x")},
			wantScopes: []string{cluster, contrib, gw},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sortedNormalizedTagsForCompare(tc.tags)
			scopes := make([]string, 0, len(got))
			for _, tg := range got {
				if tg.Scope != nil {
					scopes = append(scopes, *tg.Scope)
				}
			}
			require.Equal(t, tc.wantScopes, scopes)
		})
	}
}

func TestRecordAfterPrimaryDeletePromotion_table(t *testing.T) {
	makeOwnerRec := func(kind, ns, name, path string) *model.DnsRecord {
		return &model.DnsRecord{
			Path: servicecommon.String(path),
			Tags: []model.Tag{
				modelTag(servicecommon.TagScopeDNSRecordFor, resourceKindToCreatedFor(kind)),
				modelTag(servicecommon.TagScopeDNSRecordOwnerNamespace, ns),
				modelTag(servicecommon.TagScopeDNSRecordOwnerName, name),
			},
		}
	}

	keyA := dnsRecordOwnerKey(servicecommon.TagValueDNSRecordForHTTPRoute, dnsRecordOwnerNamespacedNameKey("ns", "a"))
	keyB := dnsRecordOwnerKey(servicecommon.TagValueDNSRecordForHTTPRoute, dnsRecordOwnerNamespacedNameKey("ns", "b"))
	storeRecA := makeOwnerRec(ResourceKindHTTPRoute, "ns", "a", "/orgs/org1/projects/proj1/dns-records/for_a")
	sharedPath := servicecommon.String("/orgs/org1/projects/proj1/dns-records/shared")

	tests := []struct {
		name           string
		sortedContribs []string
		rec            *model.DnsRecord
		storeRecords   []*model.DnsRecord
		wantErr        bool
		wantOwnerName  string
		wantGateway    string
		wantContribs   []string
	}{
		{
			name:           "invalid contrib key format returns error",
			sortedContribs: []string{"badformat"},
			rec:            &model.DnsRecord{},
			wantErr:        true,
		},
		{
			name:           "unknown kind in contrib key returns error",
			sortedContribs: []string{"unknown_kind/ns/name"},
			rec:            &model.DnsRecord{},
			wantErr:        true,
		},
		{
			name:           "sole contributor promoted, no remaining contribs",
			sortedContribs: []string{keyA},
			rec:            &model.DnsRecord{Path: sharedPath},
			storeRecords:   []*model.DnsRecord{storeRecA},
			wantOwnerName:  "a",
			wantContribs:   nil,
		},
		{
			name:           "first of two contributors promoted, second remains",
			sortedContribs: []string{keyA, keyB},
			rec:            &model.DnsRecord{Path: sharedPath},
			storeRecords:   []*model.DnsRecord{storeRecA},
			wantOwnerName:  "a",
			wantContribs:   []string{keyB},
		},
		{
			name:           "gateway tag from original record is preserved",
			sortedContribs: []string{keyA},
			rec: &model.DnsRecord{
				Path: sharedPath,
				Tags: []model.Tag{modelTag(servicecommon.TagScopeDNSRecordGatewayIndexList, "ns/gw1")},
			},
			storeRecords:  []*model.DnsRecord{storeRecA},
			wantOwnerName: "a",
			wantGateway:   "ns/gw1",
			wantContribs:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := BuildDNSRecordStore()
			for _, r := range tc.storeRecords {
				require.NoError(t, store.Add(r))
			}
			env := newTestDNSRecordService(t, store)

			out, err := env.recordAfterPrimaryDeletePromotion(tc.rec, tc.sortedContribs)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, out)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, out)
			require.Nil(t, out.MarkedForDelete)
			require.Equal(t, tc.wantOwnerName, firstTagValue(out.Tags, servicecommon.TagScopeDNSRecordOwnerName))
			require.Equal(t, tc.wantGateway, firstTagValue(out.Tags, servicecommon.TagScopeDNSRecordGatewayIndexList))
			require.Equal(t, tc.wantContribs, parseContributingOwnersFromRecord(out))
		})
	}
}

func TestClassifyOwnerRemoval_table(t *testing.T) {
	keyA := dnsRecordOwnerKey(servicecommon.TagValueDNSRecordForHTTPRoute, dnsRecordOwnerNamespacedNameKey("ns", "a"))
	keyB := dnsRecordOwnerKey(servicecommon.TagValueDNSRecordForHTTPRoute, dnsRecordOwnerNamespacedNameKey("ns", "b"))

	makeRec := func(ownerKey string, contribs []string, path string) *model.DnsRecord {
		createdFor, ns, name, _ := parseOwnerNNIndexKey(ownerKey)
		tags := []model.Tag{
			modelTag(servicecommon.TagScopeDNSRecordFor, createdFor),
			modelTag(servicecommon.TagScopeDNSRecordOwnerNamespace, ns),
			modelTag(servicecommon.TagScopeDNSRecordOwnerName, name),
		}
		if len(contribs) > 0 {
			tags = append(tags, modelTag(servicecommon.TagScopeDNSRecordContributingOwners, formatContributingOwnersTag(contribs)))
		}
		return &model.DnsRecord{Path: servicecommon.String(path), Tags: tags}
	}

	// Store record for the promoted owner (needed when primary is deleted with contribs)
	storeRecA := &model.DnsRecord{
		Path: servicecommon.String("/orgs/org1/projects/proj1/dns-records/for_a"),
		Tags: []model.Tag{
			modelTag(servicecommon.TagScopeDNSRecordFor, servicecommon.TagValueDNSRecordForHTTPRoute),
			modelTag(servicecommon.TagScopeDNSRecordOwnerNamespace, "ns"),
			modelTag(servicecommon.TagScopeDNSRecordOwnerName, "a"),
		},
	}

	tests := []struct {
		name          string
		rec           *model.DnsRecord
		deletedKey    string
		storeRecords  []*model.DnsRecord
		wantDeleteLen int
		wantUpdateLen int
		wantMarked    bool
		wantContribs  []string
	}{
		{
			name:          "primary owner, no contribs: marked for delete",
			rec:           makeRec(keyB, nil, "/orgs/org1/projects/proj1/dns-records/sole"),
			deletedKey:    keyB,
			wantDeleteLen: 1,
			wantMarked:    true,
		},
		{
			name:          "primary owner, has contribs: first contrib promoted",
			rec:           makeRec(keyB, []string{keyA}, "/orgs/org1/projects/proj1/dns-records/shared"),
			deletedKey:    keyB,
			storeRecords:  []*model.DnsRecord{storeRecA},
			wantUpdateLen: 1,
			wantContribs:  nil,
		},
		{
			name:          "contributing owner: removed from contrib list",
			rec:           makeRec(keyB, []string{keyA}, "/orgs/org1/projects/proj1/dns-records/shared"),
			deletedKey:    keyA,
			wantUpdateLen: 1,
			wantContribs:  nil,
		},
		{
			name:       "unrelated owner: no-op",
			rec:        makeRec(keyB, nil, "/orgs/org1/projects/proj1/dns-records/other"),
			deletedKey: keyA,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := BuildDNSRecordStore()
			for _, r := range tc.storeRecords {
				require.NoError(t, store.Add(r))
			}
			env := newTestDNSRecordService(t, store)

			var toDelete, toUpdate []*model.DnsRecord
			err := env.classifyOwnerRemoval(tc.rec, tc.deletedKey, &toDelete, &toUpdate)
			require.NoError(t, err)
			require.Len(t, toDelete, tc.wantDeleteLen)
			require.Len(t, toUpdate, tc.wantUpdateLen)
			if tc.wantMarked && len(toDelete) > 0 {
				require.NotNil(t, toDelete[0].MarkedForDelete)
				require.True(t, *toDelete[0].MarkedForDelete)
			}
			if len(toUpdate) > 0 {
				require.Equal(t, tc.wantContribs, parseContributingOwnersFromRecord(toUpdate[0]))
			}
		})
	}
}

func TestCompareRecords_toUpsertAndRemove(t *testing.T) {
	z := testDNSZonePathZ
	id1 := "id1"
	id2 := "id2"
	d1 := &model.DnsRecord{
		Id:           &id1,
		Path:         servicecommon.String("/orgs/o/projects/p/dns-records/" + id1),
		RecordName:   servicecommon.String("n1"),
		RecordType:   servicecommon.String(model.DnsRecord_RECORD_TYPE_A),
		ZonePath:     &z,
		RecordValues: []string{"1.1.1.1"},
	}
	e1 := &model.DnsRecord{
		Id:           &id1,
		Path:         servicecommon.String("/orgs/o/projects/p/dns-records/" + id1),
		RecordName:   servicecommon.String("n1"),
		RecordType:   servicecommon.String(model.DnsRecord_RECORD_TYPE_A),
		ZonePath:     &z,
		RecordValues: []string{"9.9.9.9"},
	}
	stale := &model.DnsRecord{
		Id:           &id2,
		Path:         servicecommon.String("/orgs/o/projects/p/dns-records/" + id2),
		RecordName:   servicecommon.String("n2"),
		RecordType:   servicecommon.String(model.DnsRecord_RECORD_TYPE_A),
		ZonePath:     &z,
		RecordValues: []string{"2.2.2.2"},
	}
	up, rm := compareRecords([]*model.DnsRecord{d1}, []*model.DnsRecord{e1, stale})
	require.Len(t, rm, 1)
	require.Equal(t, id2, *rm[0].Id)
	require.Len(t, up, 1)
	require.Equal(t, id1, *up[0].Id)
}

func TestCleanupInfraResources(t *testing.T) {
	ctx := context.Background()
	store := BuildDNSRecordStore()
	env := newTestDNSRecordService(t, store)
	owner := &ResourceRef{
		Kind:   ResourceKindGateway,
		Object: &metav1.ObjectMeta{Namespace: "ns", Name: "gw", UID: types.UID("gwuid")},
	}
	z := testDNSZonePathZ
	ep := extdns.NewEndpoint("cl.example.com", extdns.RecordTypeA, "192.0.2.1")
	ep.WithLabel(EndpointLabelParentGateway, "ns/gw")
	row := EndpointRow{Endpoint: ep, zonePath: z, nsxRecordName: "cl.example.com"}
	require.NotNil(t, env.BuildProjectDnsRecord(owner, row))
	requireNoErrCreateDNS(ctx, t, env, NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{row}))
	require.NotEmpty(t, store.ListDNSRecords())

	err := env.CleanupInfraResources(ctx)
	require.NoError(t, err)
	require.Empty(t, store.ListDNSRecords())
}

func TestValidateEndpointRowConflict_table(t *testing.T) {
	const fqdn = "svc.example.com"
	zonePath := testDNSZonePathT

	ownerA := &ResourceRef{Kind: ResourceKindService, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "svcA"}}
	ownerB := &ResourceRef{Kind: ResourceKindService, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "svcB"}}
	epA := extdns.NewEndpoint(fqdn, extdns.RecordTypeA, "10.0.0.1")

	makeStoreRec := func(owner *ResourceRef, path string, values []string) *model.DnsRecord {
		createdFor := resourceKindToCreatedFor(owner.Kind)
		return &model.DnsRecord{
			Path:         servicecommon.String(path),
			ZonePath:     servicecommon.String(zonePath),
			RecordType:   servicecommon.String(model.DnsRecord_RECORD_TYPE_A),
			RecordName:   servicecommon.String("svc"),
			Fqdn:         servicecommon.String(fqdn),
			RecordValues: values,
			Tags: []model.Tag{
				modelTag(servicecommon.TagScopeDNSRecordFor, createdFor),
				modelTag(servicecommon.TagScopeDNSRecordOwnerNamespace, owner.GetNamespace()),
				modelTag(servicecommon.TagScopeDNSRecordOwnerName, owner.GetName()),
			},
		}
	}

	tests := []struct {
		name       string
		storeRecs  []*model.DnsRecord
		owner      *ResourceRef
		wantErr    bool
		errSub     string
		wantShared bool
	}{
		{
			name:  "no existing record: new row returned",
			owner: ownerA,
		},
		{
			name:      "own record in store: returns row directly",
			storeRecs: []*model.DnsRecord{makeStoreRec(ownerA, "/orgs/org1/projects/proj1/dns-records/r-svcA", []string{"10.0.0.1"})},
			owner:     ownerA,
		},
		{
			name:      "FQDN conflict: different target values for different owner",
			storeRecs: []*model.DnsRecord{makeStoreRec(ownerB, "/orgs/org1/projects/proj1/dns-records/r-svcB", []string{"10.0.0.99"})},
			owner:     ownerA,
			wantErr:   true,
			errSub:    "configured with different values",
		},
		{
			name:       "adoption: same values from different owner",
			storeRecs:  []*model.DnsRecord{makeStoreRec(ownerB, "/orgs/org1/projects/proj1/dns-records/r-svcB", []string{"10.0.0.1"})},
			owner:      ownerA,
			wantShared: true,
		},
		{
			name: "adoption fails: same values but incomplete owner metadata",
			storeRecs: []*model.DnsRecord{{
				Path:         servicecommon.String("/orgs/org1/projects/proj1/dns-records/r-noowner"),
				ZonePath:     servicecommon.String(zonePath),
				RecordType:   servicecommon.String(model.DnsRecord_RECORD_TYPE_A),
				RecordName:   servicecommon.String("svc"),
				Fqdn:         servicecommon.String(fqdn),
				RecordValues: []string{"10.0.0.1"},
				Tags:         []model.Tag{},
			}},
			owner:   ownerA,
			wantErr: true,
			errSub:  "incomplete owner metadata",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestDNSRecordService(t, BuildDNSRecordStore())
			for _, r := range tc.storeRecs {
				require.NoError(t, env.DNSRecordStore.Add(r))
			}
			row, err := env.validateEndpointRowConflict(zonePath, epA, "svc", tc.owner)
			if tc.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errSub)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, row)
			if tc.wantShared {
				require.NotNil(t, row.effectiveOwner, "adoption: effectiveOwner should be set")
				require.NotEmpty(t, row.contributingOwnerKeys)
			}
		})
	}
}

func TestCreateOrUpdateRecords_errorPaths_table(t *testing.T) {
	ctx := context.Background()

	t.Run("applyDNSUpsertRows error propagates", func(t *testing.T) {
		env := newTestDNSRecordService(t, BuildDNSRecordStore())
		// nil Owner with non-empty Rows triggers an error from applyDNSUpsertRows.
		batch := &AggregatedDNSEndpoints{
			Rows: []EndpointRow{{Endpoint: extdns.NewEndpoint("x.example.com", extdns.RecordTypeA, "1.1.1.1")}},
		}
		_, err := env.CreateOrUpdateRecords(ctx, batch)
		require.Error(t, err)
	})

	t.Run("no rows and no existing records: no-op returns false", func(t *testing.T) {
		env := newTestDNSRecordService(t, BuildDNSRecordStore())
		owner := &ResourceRef{Kind: ResourceKindService, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "svc"}}
		mut, err := env.CreateOrUpdateRecords(ctx, NewOwnerScopedAggregatedRouteDNS(owner, nil))
		require.NoError(t, err)
		require.False(t, mut)
	})
}

func TestDeleteRecordByOwnerNN_unknownKind_noOp(t *testing.T) {
	ctx := context.Background()
	env := newTestDNSRecordService(t, BuildDNSRecordStore())
	mut, err := env.DeleteRecordByOwnerNN(ctx, "UnknownKind", "ns", "name")
	require.NoError(t, err)
	require.False(t, mut)
}

func TestDeleteRecordByOwnerNN_noRecords_noOp(t *testing.T) {
	ctx := context.Background()
	env := newTestDNSRecordService(t, BuildDNSRecordStore())
	mut, err := env.DeleteRecordByOwnerNN(ctx, ResourceKindService, "ns", "svc")
	require.NoError(t, err)
	require.False(t, mut)
}

func TestApplyDNSUpsertRows_unsupportedOwnerKind(t *testing.T) {
	env := newTestDNSRecordService(t, BuildDNSRecordStore())
	owner := &ResourceRef{Kind: "UnknownKind", Object: &metav1.ObjectMeta{Namespace: "ns", Name: "obj"}}
	ep := extdns.NewEndpoint("a.example.com", extdns.RecordTypeA, "1.1.1.1")
	batch := NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{
		{Endpoint: ep, zonePath: testDNSZonePathT, nsxRecordName: "a"},
	})
	// collectRecordsByOwner returns ("", nil) for unknown kind, so ownerNNKey="" → toUpsert is non-empty
	// but syncDnsRecordsInNSX will be called. The important thing is no panic.
	_, _, err := env.applyDNSUpsertRows(batch)
	require.NoError(t, err)
}

func TestSyncDNSZonesByVpcNetworkConfig_table(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	zc := dnszonemocks.NewMockZonesClient(ctrl)

	builder, err := servicecommon.PolicyPathDnsRecord.NewPolicyTreeBuilder()
	require.NoError(t, err)
	cfg := &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}}
	svc := &DNSRecordService{
		Service: servicecommon.Service{
			Client:    fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build(),
			NSXConfig: cfg,
			NSXClient: &nsx.Client{
				NsxConfig:     cfg,
				DnsZoneClient: zc,
			},
		},
		DNSZoneMap:       newDNSZoneCache(),
		DnsRecordBuilder: builder,
	}

	// No error returns when VPCNetworkConfiguration does not set DNS zones.
	m, err := svc.SyncDNSZonesByVpcNetworkConfig(&v1alpha1.VPCNetworkConfiguration{})
	require.NoError(t, err)
	require.Nil(t, m)

	domain := "fetched.example"
	zp := testDNSZonePathZ
	zc.EXPECT().Get("org1", "proj1", "ds1", "zone-z").Return(model.DnsZone{
		DnsDomainName: &domain,
	}, nil).Times(1)

	vpc := &v1alpha1.VPCNetworkConfiguration{
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			DNSZones: []string{zp},
		},
	}
	m, err = svc.SyncDNSZonesByVpcNetworkConfig(vpc)
	require.NoError(t, err)
	require.Equal(t, map[string]string{zp: domain}, m)
	got, ok := svc.DNSZoneMap.get(zp)
	require.True(t, ok)
	require.Equal(t, domain, got)

	// No NSX call for the second sync
	m, err = svc.SyncDNSZonesByVpcNetworkConfig(vpc)
	require.NoError(t, err)
	require.Equal(t, map[string]string{zp: domain}, m)
}

func TestRouteRecordWithGatewayAndContributions(t *testing.T) {
	store := BuildDNSRecordStore()
	env := newTestDNSRecordService(t, store)
	ctx := context.Background()
	owner1 := &ResourceRef{
		Kind:   ResourceKindGRPCRoute,
		Object: &metav1.ObjectMeta{Namespace: "app", Name: "route1", UID: types.UID("r1")},
	}
	owner2 := &ResourceRef{
		Kind:   ResourceKindHTTPRoute,
		Object: &metav1.ObjectMeta{Namespace: "app", Name: "route2", UID: types.UID("r2")},
	}
	z := testDNSZonePathZ
	ep1 := extdns.NewEndpoint("rec1.example.com", extdns.RecordTypeA, "10.0.0.1,10.0.0.2")
	ep1.WithLabel(EndpointLabelParentGateway, "app/gw1,app/gw2")
	row := EndpointRow{Endpoint: ep1, zonePath: z, nsxRecordName: "rec1"}
	requireNoErrCreateDNS(ctx, t, env, NewOwnerScopedAggregatedRouteDNS(owner1, []EndpointRow{row}))
	require.Len(t, store.GetByOwnerResourceNamespacedName(ResourceKindGRPCRoute, "app", "route1"), 1)

	ep2 := extdns.NewEndpoint("rec2.example.com", extdns.RecordTypeA, "10.0.0.2")
	ep1.WithLabel(EndpointLabelParentGateway, "app/gw2")
	row2 := EndpointRow{Endpoint: ep2, zonePath: z, nsxRecordName: "rec2"}
	requireNoErrCreateDNS(ctx, t, env, NewOwnerScopedAggregatedRouteDNS(owner2, []EndpointRow{row2}))
	require.Len(t, store.GetByOwnerResourceNamespacedName(ResourceKindHTTPRoute, "app", "route2"), 1)

	require.Len(t, store.List(), 2)
	referredGateways := env.ListReferredGatewayNN()
	require.Equal(t, referredGateways.Len(), 2)
	require.True(t, referredGateways.HasAll(types.NamespacedName{Namespace: "app", Name: "gw1"}, types.NamespacedName{Namespace: "app", Name: "gw2"}))

	// Test owner3 contributes to rec2.example.com
	owner3 := &ResourceRef{
		Kind:   ResourceKindGRPCRoute,
		Object: &metav1.ObjectMeta{Namespace: "app", Name: "route3", UID: types.UID("r3")},
	}
	owner3NNKey := ownerNNIndexKeyForResourceRef(owner3)
	row3 := EndpointRow{Endpoint: ep2, zonePath: z, nsxRecordName: "rec2", effectiveOwner: owner2, contributingOwnerKeys: compressString(owner3NNKey)}
	requireNoErrCreateDNS(ctx, t, env, NewOwnerScopedAggregatedRouteDNS(owner3, []EndpointRow{row3}))
	recByOwner2 := store.GetByOwnerResourceNamespacedName(ResourceKindHTTPRoute, "app", "route2")
	require.Len(t, recByOwner2, 1)
	require.Empty(t, store.GetByOwnerResourceNamespacedName(ResourceKindGRPCRoute, "app", "route3"))
	_, recByOwner3 := env.collectRecordsByOwner(ResourceKindGRPCRoute, "app", "route3")
	require.Len(t, recByOwner3, 1)
	require.Equal(t, recByOwner2[0], recByOwner3[0])

	requireNoErrDeleteDNS(ctx, t, env, ResourceKindHTTPRoute, "app", "route2")
	recByOwner2 = store.GetByOwnerResourceNamespacedName(ResourceKindHTTPRoute, "app", "route2")
	require.Empty(t, recByOwner2)
	recByOwner3 = store.GetByOwnerResourceNamespacedName(ResourceKindGRPCRoute, "app", "route3")
	require.Len(t, recByOwner3, 1)
	require.Empty(t, store.ListRecordsReferencingContributingOwner(owner3NNKey))
}
