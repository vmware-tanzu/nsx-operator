/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"

	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

// stableDNSRecordIDWithDNSZone matches production record naming when tests build EndpointRow rows manually.
func stableDNSRecordIDWithDNSZone(ownerUID, dnsName, recordType, zonePath string) string {
	zp := strings.TrimSpace(zonePath)
	dn := strings.TrimSpace(strings.ToLower(dnsName))
	rt := strings.TrimSpace(recordType)
	if zp == "" {
		return stableDNSRecordID(ownerUID, dn, rt)
	}
	return stableDNSRecordIDFromPreimage([]string{ownerUID, dn, rt, zp})
}

func stableDNSRecordID(ownerUID, dnsName, recordType string) string {
	return stableDNSRecordIDFromPreimage([]string{ownerUID, dnsName, recordType})
}

func stableDNSRecordIDFromPreimage(parts []string) string {
	preimage := strings.Join(parts, ":")
	h := sha256.Sum256([]byte(preimage))
	return "dns-" + hex.EncodeToString(h[:16])
}

func recordStoreKey(projectPath, recordName, recordType string) string {
	rec := strings.ReplaceAll(strings.TrimSpace(recordName), ".", "_")
	if strings.TrimSpace(recordType) != "" {
		rec = rec + "_" + strings.ToLower(strings.TrimSpace(recordType))
	}
	return strings.Join([]string{strings.TrimSpace(projectPath), rec}, "/")
}

func newTestDNSRecordService(store *RecordStore) *DNSRecordService {
	fc := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build()
	return &DNSRecordService{
		Service: servicecommon.Service{
			Client:    fc,
			NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}},
		},
		DNSRecordStore: store,
	}
}

// newTestDNSRecordServiceForZoneValidate is a service that can run ValidateEndpointsByDNSZone (VPC, DNS zone map, store).
func newTestDNSRecordServiceForZoneValidate(t *testing.T) (*DNSRecordService, *RecordStore) {
	t.Helper()
	store := BuildDNSRecordStore()
	m := &pkgmock.MockVPCServiceProvider{}
	m.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(testVPCNetworkConfiguration(), nil)
	t.Cleanup(func() { m.AssertExpectations(t) })
	fc := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build()
	return &DNSRecordService{
		Service: servicecommon.Service{
			Client:    fc,
			NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}},
		},
		VPCService:     m,
		DNSRecordStore: store,
		DNSZoneMap:     testDNSZoneMapForVPCFixture(),
	}, store
}

const testDNSProjectPath = "/orgs/org1/projects/proj1/vpcs/vpc1"

func requireNoErrCreateDNS(ctx context.Context, t *testing.T, svc *DNSRecordService, batch *AggregatedDNSEndponts) {
	t.Helper()
	_, err := svc.CreateOrUpdateDNSRecords(ctx, batch)
	require.NoError(t, err)
}

func requireNoErrDeleteDNS(ctx context.Context, t *testing.T, svc *DNSRecordService, kind, ns, name string) {
	t.Helper()
	_, err := svc.DeleteDNSRecordByOwnerNN(ctx, kind, ns, name)
	require.NoError(t, err)
}

func TestCreateOrUpdateDNSRecords_edgeCases(t *testing.T) {
	ctx := context.Background()
	store := BuildDNSRecordStore()
	epBad := extdns.NewEndpoint("x.example.com", extdns.RecordTypeA, "1.1.1.1")
	epBad.WithLabel(EndpointLabelParentGateway, "gw")
	rowBad := NewEndpointRow(epBad, "/z", testDNSProjectPath, stableDNSRecordIDWithDNSZone("u1", "x.example.com", extdns.RecordTypeA, "/z"))

	tests := []struct {
		name         string
		svc          *DNSRecordService
		batch        *AggregatedDNSEndponts
		errSubstring string
	}{
		{name: "nil batch skips", svc: newTestDNSRecordService(store), batch: nil},
		{
			name:         "nil owner with non empty rows",
			svc:          newTestDNSRecordService(BuildDNSRecordStore()),
			batch:        &AggregatedDNSEndponts{Namespace: "ns", Owner: nil, Rows: []EndpointRow{*rowBad}},
			errSubstring: "Owner is nil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.svc.CreateOrUpdateDNSRecords(ctx, tt.batch)
			if tt.errSubstring != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errSubstring)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateEndpointsByDNSZone_FQDN_takenInZone_differentTargetValues(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDNSRecordServiceForZoneValidate(t)
	gw1 := &ResourceRef{Kind: ResourceKindGateway, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "g1", UID: types.UID("gw-uid-1")}}
	ep1 := extdns.NewEndpoint("dup.example.com", extdns.RecordTypeA, "10.0.0.1")
	ep1.WithLabel(EndpointLabelParentGateway, "ns/g1")
	rec1 := stableDNSRecordIDWithDNSZone("g1-uid", "dup.example.com", string(extdns.RecordTypeA), "/zones/t")
	row1 := EndpointRow{Endpoint: ep1, zonePath: "/zones/t", projectPath: testDNSProjectPath, recordName: rec1}
	requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(gw1, []EndpointRow{row1}))
	gw2 := &ResourceRef{Kind: ResourceKindGateway, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "g2", UID: types.UID("gw-uid-2")}}
	ep2 := extdns.NewEndpoint("dup.example.com", extdns.RecordTypeA, "10.0.0.2")
	ep2.WithLabel(EndpointLabelParentGateway, "ns/g2")
	_, err := svc.ValidateEndpointsByDNSZone("ns", gw2, []*extdns.Endpoint{ep2})
	require.Error(t, err)
	require.Contains(t, err.Error(), "configured with different values")
}

func TestCreateOrUpdateDNSRecords_ownerScopedEmptyRowsPrunes(t *testing.T) {
	ctx := context.Background()
	store := BuildDNSRecordStore()
	svc := newTestDNSRecordService(store)
	owner := &ResourceRef{
		Kind:   ResourceKindGateway,
		Object: &metav1.ObjectMeta{Namespace: "ns", Name: "gw", UID: types.UID("gwuid")},
	}
	ep := extdns.NewEndpoint("gw.example.com", extdns.RecordTypeA, "192.0.2.1")
	ep.WithLabel(EndpointLabelParentGateway, "ns/gw")
	row := EndpointRow{Endpoint: ep, zonePath: "/zones/gw", projectPath: testDNSProjectPath, recordName: "gw"}

	requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{row}))
	key := recordStoreKey(row.projectPath, row.recordName, row.Endpoint.RecordType)
	require.NotNil(t, store.GetByKey(key))

	requireNoErrCreateDNS(ctx, t, svc, &AggregatedDNSEndponts{
		Namespace: "ns",
		Owner:     owner,
		Rows:      nil,
	})
	require.Nil(t, store.GetByKey(key))
}

func TestNewOwnerScopedAggregatedRouteDNS_table(t *testing.T) {
	o := &ResourceRef{Kind: ResourceKindGateway, Object: &metav1.ObjectMeta{Namespace: "n", Name: "g", UID: "u"}}
	tests := []struct {
		name    string
		owner   *ResourceRef
		rows    []EndpointRow
		wantNil bool
	}{
		{name: "nil_scope_owner_returns_nil", wantNil: true},
		{name: "nil_rows_still_scoped_to_owner", owner: o, rows: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewOwnerScopedAggregatedRouteDNS(tt.owner, tt.rows)
			if tt.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Empty(t, got.Rows)
			require.Equal(t, tt.owner, got.Owner)
		})
	}
}

func TestDNSRecord_tagsAndIndexing_table(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name   string
		run    func(t *testing.T, store *RecordStore, svc *DNSRecordService)
		assert func(t *testing.T, store *RecordStore)
	}{
		{
			name: "owner scoped HTTPRoute rows are indexed by route namespaced name",
			run: func(t *testing.T, _ *RecordStore, svc *DNSRecordService) {
				ns := "demo"
				fqdn := "svc.demo.example.com"
				zpath := "/zones/demo"
				rec := stableDNSRecordIDWithDNSZone("uid-hr1", fqdn, extdns.RecordTypeA, zpath)
				ep := extdns.NewEndpoint(fqdn, extdns.RecordTypeA, "192.0.2.10", "192.0.2.11")
				ep.WithLabel(EndpointLabelParentGateway, "demo/gw1")
				row := NewEndpointRow(ep, zpath, testDNSProjectPath, rec)
				hrOwner := &ResourceRef{
					Kind:   ResourceKindHTTPRoute,
					Object: &metav1.ObjectMeta{Namespace: ns, Name: "hr1", UID: types.UID("uid-hr1")},
				}
				requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(hrOwner, []EndpointRow{*row}))
			},
			assert: func(t *testing.T, st *RecordStore) {
				owners := st.GetByOwnerResourceNamespacedName(ResourceKindHTTPRoute, "demo", "hr1")
				require.NotEmpty(t, owners)
			},
		},
		{
			name: "second HTTPRoute owner for same FQDN is rejected",
			run: func(t *testing.T, _ *RecordStore, svc *DNSRecordService) {
				ns := "shared"
				fqdn := "x.example.com"
				rt := extdns.RecordTypeA
				zp := "/zones/shared"
				row := func(uid, _ string, targets []string) EndpointRow {
					id := stableDNSRecordIDWithDNSZone(uid, fqdn, rt, zp)
					ep := extdns.NewEndpoint(fqdn, rt, targets...)
					ep.WithLabel(EndpointLabelParentGateway, "shared/gw")
					r := NewEndpointRow(ep, zp, testDNSProjectPath, id)
					return *r
				}
				hr1 := &ResourceRef{
					Kind:   ResourceKindHTTPRoute,
					Object: &metav1.ObjectMeta{Namespace: ns, Name: "a", UID: types.UID("u1")},
				}
				requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(hr1, []EndpointRow{row("u1", "a", []string{"10.0.0.1"})}))
			},
			assert: func(t *testing.T, st *RecordStore) {
				fqdn := "x.example.com"
				rt := extdns.RecordTypeA
				zp := "/zones/shared"
				id1 := stableDNSRecordIDWithDNSZone("u1", fqdn, rt, zp)
				id2 := stableDNSRecordIDWithDNSZone("u2", fqdn, rt, zp)
				k1 := recordStoreKey(testDNSProjectPath, id1, rt)
				k2 := recordStoreKey(testDNSProjectPath, id2, rt)
				require.NotNil(t, st.GetByKey(k1))
				require.Nil(t, st.GetByKey(k2))
			},
		},
		{
			name: "second HTTPRoute for same FQDN with same targets creates a second store row with effective owner and contributing tag",
			run: func(t *testing.T, _ *RecordStore, _ *DNSRecordService) {
				svc, store := newTestDNSRecordServiceForZoneValidate(t)
				ns := "shared"
				fqdn := "same.example.com"
				rt := extdns.RecordTypeA
				zp := "/zones/t"
				targets := []string{"10.0.0.1", "10.0.0.2"}
				row := func(uid string) EndpointRow {
					id := stableDNSRecordIDWithDNSZone(uid, fqdn, rt, zp)
					ep := extdns.NewEndpoint(fqdn, rt, targets...)
					ep.WithLabel(EndpointLabelParentGateway, "shared/gw")
					r := NewEndpointRow(ep, zp, testDNSProjectPath, id)
					return *r
				}
				hr1 := &ResourceRef{
					Kind:   ResourceKindHTTPRoute,
					Object: &metav1.ObjectMeta{Namespace: ns, Name: "a", UID: types.UID("u1")},
				}
				hr2 := &ResourceRef{
					Kind:   ResourceKindHTTPRoute,
					Object: &metav1.ObjectMeta{Namespace: ns, Name: "b", UID: types.UID("u2")},
				}
				requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(hr1, []EndpointRow{row("u1")}))
				hr2Row := row("u2")
				valid, err := svc.ValidateEndpointsByDNSZone(ns, hr2, []*extdns.Endpoint{hr2Row.Endpoint})
				require.NoError(t, err)
				require.Len(t, valid, 1)
				requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(hr2, valid))
				id1 := stableDNSRecordIDWithDNSZone("u1", fqdn, rt, zp)
				k1 := recordStoreKey(testDNSProjectPath, id1, rt)
				rec1 := store.GetByKey(k1)
				require.NotNil(t, rec1)
				wantPrimaryNN := getDNSRecordOwnerNamespacedName(rec1)
				wantContrib := dnsRecordOwnerKey(servicecommon.TagValueDNSRecordForHTTPRoute, dnsRecordOwnerNamespacedNameKey(ns, "b"))
				require.NotNil(t, valid[0].effectiveOwner)
				require.Equal(t, wantPrimaryNN, ownerNNIndexKeyForResourceRef(valid[0].effectiveOwner))
				require.Contains(t, valid[0].contributingOwnerKeys, wantContrib)
				// The upserted row is still tagged with the effective (primary) owner's namespace/name, not the batch route's
				// (see buildDNSRecord). Resolve it via zone+FQDN+type index, then pick the record with a contributing tag.
				fqKey := dnsRecordZonePathFQDNIndexKey(zp, fqdn, string(rt))
				matching := store.GetByIndex(indexKeyDNSRecordZonePathFQDN, fqKey)
				require.GreaterOrEqual(t, len(matching), 2, "no adopt: expect separate store objects for two routes at same FQDN")
				var rec2 *Record
				for _, r := range matching {
					if firstTagValue(r.Tags, servicecommon.TagScopeDNSRecordContributingOwners) == wantContrib {
						rec2 = r
						break
					}
				}
				require.NotNil(t, rec2)
			},
			assert: func(_ *testing.T, _ *RecordStore) {},
		},
		{
			name: "Gateway_and_HTTPRoute_rows_tagged_with_dns_for_and_owner_namespace_name",
			run: func(t *testing.T, _ *RecordStore, svc *DNSRecordService) {
				gwOwner := &ResourceRef{
					Kind:   ResourceKindGateway,
					Object: &metav1.ObjectMeta{Namespace: "ns", Name: "gw", UID: types.UID("gwuid")},
				}
				gwEp := extdns.NewEndpoint("tagcheck.example.com", extdns.RecordTypeA, "192.0.2.1")
				gwEp.WithLabel(EndpointLabelParentGateway, "ns/gw")
				gwRow := EndpointRow{Endpoint: gwEp, zonePath: "/zones/z", projectPath: testDNSProjectPath, recordName: "tagcheck"}
				requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(gwOwner, []EndpointRow{gwRow}))
				hrOwner := &ResourceRef{
					Kind:   ResourceKindHTTPRoute,
					Object: &metav1.ObjectMeta{Namespace: "ns", Name: "hr1", UID: types.UID("route-uid")},
				}
				aggRec := stableDNSRecordIDWithDNSZone("route-uid", "agg.example.com", extdns.RecordTypeA, "/zones/z")
				aggEp := extdns.NewEndpoint("agg.example.com", extdns.RecordTypeA, "10.0.0.1")
				aggEp.WithLabel(EndpointLabelParentGateway, "ns/gw")
				hrRow := NewEndpointRow(aggEp, "/zones/z", testDNSProjectPath, aggRec)
				requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(hrOwner, []EndpointRow{*hrRow}))
			},
			assert: func(t *testing.T, st *RecordStore) {
				tagScopes := func(rec *Record) []string {
					var scopes []string
					for _, tg := range rec.Tags {
						if tg.Scope != nil {
							scopes = append(scopes, *tg.Scope)
						}
					}
					return scopes
				}
				for _, tc := range []struct {
					recordName string
					recordType string
					forbidden  []string
				}{
					{
						recordName: "tagcheck",
						recordType: string(extdns.RecordTypeA),
						forbidden: []string{
							"nsx-op/gateway_uid",
							"nsx-op/gateway_api_route_uid",
							"nsx-op/listenerset_uid",
							"nsx-op/service_uid",
						},
					},
					{
						recordName: stableDNSRecordIDWithDNSZone("route-uid", "agg.example.com", extdns.RecordTypeA, "/zones/z"),
						recordType: string(extdns.RecordTypeA),
						forbidden:  []string{"nsx-op/gateway_api_route_uid"},
					},
				} {
					rec := st.GetByKey(recordStoreKey(testDNSProjectPath, tc.recordName, tc.recordType))
					require.NotNil(t, rec)
					scopes := tagScopes(rec)
					require.Contains(t, scopes, servicecommon.TagScopeDNSRecordFor)
					require.Contains(t, scopes, servicecommon.TagScopeDNSRecordOwnerNamespace)
					require.Contains(t, scopes, servicecommon.TagScopeDNSRecordOwnerName)
					for _, s := range tc.forbidden {
						require.NotContains(t, scopes, s)
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := BuildDNSRecordStore()
			svc := newTestDNSRecordService(store)
			tt.run(t, store, svc)
			tt.assert(t, store)
		})
	}
}

func TestValidateEndpointsByDNSZone_table(t *testing.T) {
	intPtr := func(n int) *int { return &n }
	ep := extdns.NewEndpoint("svc.example.com", extdns.RecordTypeA, "10.0.0.1")
	ep.WithLabel(EndpointLabelParentGateway, "ns/gw")

	baseSvc := func(vpc servicecommon.VPCServiceProvider) *DNSRecordService {
		return &DNSRecordService{
			Service: servicecommon.Service{
				NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}},
			},
			VPCService: vpc,
			DNSZoneMap: testDNSZoneMapForVPCFixture(),
		}
	}

	tests := []struct {
		name     string
		buildVPC func(t *testing.T) servicecommon.VPCServiceProvider
		ns       string
		eps      []*extdns.Endpoint
		wantZone string // used when errSub is empty
		errSub   string
		wantN    *int // when non-nil, assert len(rows)==*wantN instead of len(eps)
	}{
		{
			name: "happy_path",
			buildVPC: func(t *testing.T) servicecommon.VPCServiceProvider {
				m := &pkgmock.MockVPCServiceProvider{}
				m.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(testVPCNetworkConfiguration(), nil).Once()
				t.Cleanup(func() { m.AssertExpectations(t) })
				return m
			},
			ns: "tenant", eps: []*extdns.Endpoint{ep}, wantZone: "/zones/t",
		},
		{
			name: "nil_VPCService",
			buildVPC: func(_ *testing.T) servicecommon.VPCServiceProvider {
				return nil
			},
			ns: "n", eps: []*extdns.Endpoint{ep}, errSub: "VPCService is not configured",
		},
		{
			name: "no_DNS_zones_in_VPC_spec",
			buildVPC: func(t *testing.T) servicecommon.VPCServiceProvider {
				m := &pkgmock.MockVPCServiceProvider{}
				nc := testVPCNetworkConfiguration()
				nc.Spec.DNSZones = nil
				m.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(nc, nil).Once()
				t.Cleanup(func() { m.AssertExpectations(t) })
				return m
			},
			ns: "n", eps: []*extdns.Endpoint{ep}, errSub: "no DNS zones are permitted for the namespace",
		},
		{
			name: "no_VPC_in_network_config_status",
			buildVPC: func(t *testing.T) servicecommon.VPCServiceProvider {
				m := &pkgmock.MockVPCServiceProvider{}
				nc := testVPCNetworkConfiguration()
				nc.Status.VPCs = nil
				m.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(nc, nil).Once()
				t.Cleanup(func() { m.AssertExpectations(t) })
				return m
			},
			ns: "n", eps: []*extdns.Endpoint{ep}, errSub: "faild to find VPC Networkconfigurations",
		},
		{
			name: "GetVPCNetworkConfigByNamespace_error",
			buildVPC: func(t *testing.T) servicecommon.VPCServiceProvider {
				m := &pkgmock.MockVPCServiceProvider{}
				m.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(nil, errors.New("vpc down")).Once()
				t.Cleanup(func() { m.AssertExpectations(t) })
				return m
			},
			ns: "n", eps: []*extdns.Endpoint{ep}, errSub: "faild to find VPC Networkconfigurations",
		},
		{
			name: "malformed_VPC_path_in_status",
			buildVPC: func(t *testing.T) servicecommon.VPCServiceProvider {
				m := &pkgmock.MockVPCServiceProvider{}
				nc := testVPCNetworkConfiguration()
				nc.Status.VPCs[0].VPCPath = "not-a-valid-vpc-path"
				m.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(nc, nil).Once()
				t.Cleanup(func() { m.AssertExpectations(t) })
				return m
			},
			ns: "n", eps: []*extdns.Endpoint{ep}, errSub: "invalid path",
		},
		{
			name: "wildcard_dns_name_skipped",
			buildVPC: func(t *testing.T) servicecommon.VPCServiceProvider {
				m := &pkgmock.MockVPCServiceProvider{}
				m.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(testVPCNetworkConfiguration(), nil).Once()
				t.Cleanup(func() { m.AssertExpectations(t) })
				return m
			},
			ns:    "tenant",
			eps:   []*extdns.Endpoint{extdns.NewEndpoint("*.apps.example.com", extdns.RecordTypeA, "10.0.0.1")},
			wantN: intPtr(0),
		},
		{
			name: "wildcard_skipped_concrete_hostname_kept",
			buildVPC: func(t *testing.T) servicecommon.VPCServiceProvider {
				m := &pkgmock.MockVPCServiceProvider{}
				m.On("GetVPCNetworkConfigByNamespace", mock.AnythingOfType("string")).Return(testVPCNetworkConfiguration(), nil).Once()
				t.Cleanup(func() { m.AssertExpectations(t) })
				return m
			},
			ns: "tenant",
			eps: []*extdns.Endpoint{
				extdns.NewEndpoint("*.apps.example.com", extdns.RecordTypeA, "10.0.0.1"),
				extdns.NewEndpoint("svc.example.com", extdns.RecordTypeA, "10.0.0.2"),
			},
			wantZone: "/zones/t",
			wantN:    intPtr(1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := baseSvc(tt.buildVPC(t))
			rows, err := svc.ValidateEndpointsByDNSZone(tt.ns, nil, tt.eps)
			if tt.errSub != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errSub)
				return
			}
			require.NoError(t, err)
			if tt.wantN != nil {
				require.Len(t, rows, *tt.wantN)
				if *tt.wantN > 0 {
					require.Equal(t, tt.wantZone, rows[0].zonePath)
					if tt.name == "wildcard_skipped_concrete_hostname_kept" {
						require.Equal(t, "svc.example.com", rows[0].DNSName)
					}
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
		run  func(t *testing.T, svc *DNSRecordService)
	}{
		{
			name: "empty_store_deleteByOwnerNN_noops_route_kinds",
			run: func(t *testing.T, svc *DNSRecordService) {
				for _, kind := range []string{ResourceKindHTTPRoute, ResourceKindGRPCRoute, ResourceKindTLSRoute} {
					requireNoErrDeleteDNS(ctx, t, svc, kind, "ns", "x")
				}
				require.NoError(t, svc.DeleteDNSRecords(ctx, nil))
				require.Equal(t, 0, svc.ListReferredGatewayNN().Len())
				require.Empty(t, svc.DNSRecordStore.GroupRecordsByResourceKind())
			},
		},
		{
			name: "DeleteDNSRecordByOwnerNN_gateway_removes_row_and_gateway_index",
			run: func(t *testing.T, svc *DNSRecordService) {
				owner := &ResourceRef{
					Kind:   ResourceKindGateway,
					Object: &metav1.ObjectMeta{Namespace: "app", Name: "gw1", UID: types.UID("g1")},
				}
				z := "/zones/z"
				id := stableDNSRecordIDWithDNSZone("g1", "gw.example.com", string(extdns.RecordTypeA), z)
				ep := extdns.NewEndpoint("gw.example.com", extdns.RecordTypeA, "10.0.0.1")
				ep.WithLabel(EndpointLabelParentGateway, "app/gw1")
				row := EndpointRow{Endpoint: ep, zonePath: z, projectPath: testDNSProjectPath, recordName: id}
				requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{row}))
				key := recordStoreKey(row.projectPath, row.recordName, row.Endpoint.RecordType)
				require.NotNil(t, svc.DNSRecordStore.GetByKey(key))
				requireNoErrDeleteDNS(ctx, t, svc, ResourceKindGateway, "app", "gw1")
				require.Nil(t, svc.DNSRecordStore.GetByKey(key))
			},
		},
		{
			name: "DeleteDNSRecordByOwnerNN_gateway_does_not_remove_HTTPRoute_rows",
			run: func(t *testing.T, svc *DNSRecordService) {
				gwOwner := &ResourceRef{
					Kind:   ResourceKindGateway,
					Object: &metav1.ObjectMeta{Namespace: "app", Name: "gw1", UID: types.UID("g1")},
				}
				z := "/zones/z"
				idG := stableDNSRecordIDWithDNSZone("g1", "gw.example.com", string(extdns.RecordTypeA), z)
				epG := extdns.NewEndpoint("gw.example.com", extdns.RecordTypeA, "10.0.0.1")
				epG.WithLabel(EndpointLabelParentGateway, "app/gw1")
				rowG := EndpointRow{Endpoint: epG, zonePath: z, projectPath: testDNSProjectPath, recordName: idG}
				requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(gwOwner, []EndpointRow{rowG}))
				hrRec := stableDNSRecordIDWithDNSZone("u-hr", "r.example.com", extdns.RecordTypeA, z)
				hrEp := extdns.NewEndpoint("r.example.com", extdns.RecordTypeA, "10.0.0.2")
				hrEp.WithLabel(EndpointLabelParentGateway, "app/gw1")
				hrRow := NewEndpointRow(hrEp, z, testDNSProjectPath, hrRec)
				hrOwner := &ResourceRef{
					Kind:   ResourceKindHTTPRoute,
					Object: &metav1.ObjectMeta{Namespace: "app", Name: "hr1", UID: types.UID("u-hr")},
				}
				requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(hrOwner, []EndpointRow{*hrRow}))
				requireNoErrDeleteDNS(ctx, t, svc, ResourceKindGateway, "app", "gw1")
				require.Nil(t, svc.DNSRecordStore.GetByKey(recordStoreKey(rowG.projectPath, rowG.recordName, rowG.Endpoint.RecordType)))
				require.NotNil(t, svc.DNSRecordStore.GetByKey(recordStoreKey(hrRow.projectPath, hrRow.recordName, hrRow.Endpoint.RecordType)))
			},
		},
		{
			name: "GroupRecordsByResourceKind_maps_HTTPRoute_owner",
			run: func(t *testing.T, svc *DNSRecordService) {
				ns := "demo"
				demoRec := stableDNSRecordIDWithDNSZone("uid-hr1", "svc.demo.example.com", extdns.RecordTypeA, "/zones/demo")
				demoEp := extdns.NewEndpoint("svc.demo.example.com", extdns.RecordTypeA, "192.0.2.10")
				demoEp.WithLabel(EndpointLabelParentGateway, "demo/gw1")
				row := NewEndpointRow(demoEp, "/zones/demo", testDNSProjectPath, demoRec)
				hrOwner := &ResourceRef{
					Kind:   ResourceKindHTTPRoute,
					Object: &metav1.ObjectMeta{Namespace: ns, Name: "hr1", UID: types.UID("uid-hr1")},
				}
				requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(hrOwner, []EndpointRow{*row}))
				require.True(t, svc.ListReferredGatewayNN().Has(types.NamespacedName{Namespace: "demo", Name: "gw1"}))
				groups := svc.DNSRecordStore.GroupRecordsByResourceKind()
				nn := types.NamespacedName{Namespace: ns, Name: "hr1"}
				require.True(t, groups[ResourceKindHTTPRoute].Has(nn), "got map %#v", groups)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestDNSRecordService(BuildDNSRecordStore())
			tt.run(t, svc)
		})
	}
}

func TestInitializeDNSRecordService_buildsStore(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build()
	vpcM := &pkgmock.MockVPCServiceProvider{}
	svc, err := InitializeDNSRecordService(servicecommon.Service{
		Client:    fc,
		NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}},
	}, vpcM)
	require.NoError(t, err)
	require.NotNil(t, svc.DNSRecordStore)
	require.NotNil(t, svc.VPCService)
}

func TestCreateOrUpdateDNSRecords_ownerScoped_mergeUpdatesTargets(t *testing.T) {
	ctx := context.Background()
	store := BuildDNSRecordStore()
	svc := newTestDNSRecordService(store)
	owner := &ResourceRef{
		Kind:   ResourceKindGateway,
		Object: &metav1.ObjectMeta{Namespace: "ns", Name: "gw", UID: types.UID("gwuid")},
	}
	z := "/zones/z"
	id := stableDNSRecordIDWithDNSZone("gwuid", "x.example.com", string(extdns.RecordTypeA), z)
	for _, targets := range [][]string{{"10.0.0.1"}, {"10.0.0.2"}} {
		ep := extdns.NewEndpoint("x.example.com", extdns.RecordTypeA, targets...)
		ep.WithLabel(EndpointLabelParentGateway, "ns/gw")
		row := EndpointRow{Endpoint: ep, zonePath: z, projectPath: testDNSProjectPath, recordName: id}
		requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{row}))
	}
	rec := store.GetByKey(recordStoreKey(testDNSProjectPath, id, string(extdns.RecordTypeA)))
	require.NotNil(t, rec)
	vals := append([]string(nil), rec.RecordValues...)
	require.Equal(t, []string{"10.0.0.2"}, vals)
}

func Test_CreateOrUpdateDNSRecords_Service_DeleteByOwnerNN(t *testing.T) {
	ctx := context.Background()
	store := BuildDNSRecordStore()
	svc := newTestDNSRecordService(store)
	meta := metav1.ObjectMeta{Name: "lbsvc", Namespace: "ns1", UID: types.UID("svc-uid-1")}
	owner := &ResourceRef{Kind: ResourceKindService, Object: &meta}
	z := "/zones/z"
	ep := extdns.NewEndpoint("x.example.com", extdns.RecordTypeA, "10.0.0.1")
	ep.WithLabel(EndpointLabelParentGateway, "ns1/lbsvc")
	id := stableDNSRecordIDWithDNSZone("svc-uid-1", "x.example.com", string(extdns.RecordTypeA), z)
	row := EndpointRow{Endpoint: ep, zonePath: z, projectPath: testDNSProjectPath, recordName: id}
	requireNoErrCreateDNS(ctx, t, svc, NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{row}))
	require.Len(t, svc.DNSRecordStore.GetByOwnerResourceNamespacedName(ResourceKindService, "ns1", "lbsvc"), 1)
	requireNoErrDeleteDNS(ctx, t, svc, ResourceKindService, "ns1", "lbsvc")
	assert.Empty(t, svc.DNSRecordStore.GetByOwnerResourceNamespacedName(ResourceKindService, "ns1", "lbsvc"))
}
