/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	dnszonemocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnszonesclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	common "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

func waitDNSResult[T any](t *testing.T, ch <-chan T, description string) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func TestZonePathForHostnameFromMap_table(t *testing.T) {
	tests := []struct {
		name       string
		zones      map[string]string // zonePath → domainName
		hostname   string
		wantRecord string
		wantPath   string
		errSub     string // non-empty => require error containing substring
	}{
		{
			name: "nested_host_longest_suffix_zone",
			zones: map[string]string{
				"/z1": "example.com",
				"/z2": "foo.example.com",
			},
			hostname:   "a.foo.example.com",
			wantRecord: "a",
			wantPath:   "/z2",
		},
		{
			name: "shorter_zone_when_longer_suffix_not_configured",
			zones: map[string]string{
				"/z1": "example.com",
				"/z2": "foo.example.com",
			},
			hostname:   "bar.example.com",
			wantRecord: "bar",
			wantPath:   "/z1",
		},
		{
			name:       "wildcard_prefix_stripped_before_FindZone",
			zones:      map[string]string{"/z": "example.com"},
			hostname:   "*.apps.example.com",
			wantRecord: "apps",
			wantPath:   "/z",
		},
		{
			name:       "apex_hostname_allowed_with_trailing_dot_and_mixed_case",
			zones:      map[string]string{"/z": "example.com"},
			hostname:   "EXAMPLE.COM.",
			wantRecord: "@",
			wantPath:   "/z",
		},
		{
			name:       "zone_domain_with_trailing_dot",
			zones:      map[string]string{"/z": "example.com."},
			hostname:   "api.example.com",
			wantRecord: "api",
			wantPath:   "/z",
		},
		{
			name:     "leading_dot_only_zone_suffix_rejected_normalized_equals_suffix",
			zones:    map[string]string{"/z": "example.com"},
			hostname: ".example.com",
			errSub:   "does not lie under matched zone",
		},
		{
			name:     "hostname_not_under_any_zone",
			zones:    map[string]string{"/z": "example.com"},
			hostname: "svc.other.example",
			errSub:   "does not match any allowed DNS domain",
		},
		{
			name:     "no_zones_configured",
			zones:    map[string]string{},
			hostname: "a.example.com",
			errSub:   "does not match any allowed DNS domain",
		},
		{
			name:     "empty_hostname_after_trim",
			zones:    map[string]string{"/z": "example.com"},
			hostname: "  ",
			errSub:   "empty hostname",
		},
		{
			name: "punycode_hostname_matches_unicode_zone_longest_suffix",
			zones: map[string]string{
				"/z1": "example.com",
				"/z2": "xn--testcass-e1ae.fr",
				"/z3": "app.xn--testcass-e1ae.fr",
			},
			hostname:   "svc.app.xn--testcass-e1ae.fr",
			wantRecord: "svc",
			wantPath:   "/z3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &DNSRecordService{}
			zid := generateZoneIdFromMap(tt.zones)
			rec, zp, err := svc.getZonePathForHostname(zid, tt.hostname)
			if tt.errSub != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errSub)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantPath, zp, "zone path")
			require.Equal(t, tt.wantRecord, rec, "relative record name")
		})
	}
}

func TestSyncDNSZonesRejectsDuplicateDomain(t *testing.T) {
	zoneA := "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-a"
	zoneB := "/orgs/org1/projects/proj1/dns-services/ds1/zones/zone-b"
	svc := &DNSRecordService{DNSZoneMap: NewDNSZoneCacheFromMap(map[string]string{
		zoneA: "example.com",
		zoneB: "EXAMPLE.COM.",
	})}
	nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{
		DNSZones: []string{zoneA, zoneB},
	}}
	_, err := svc.SyncDNSZonesByVpcNetworkConfig(nc)
	require.ErrorContains(t, err, "configured in multiple zones")
	var validationErr *DNSZoneValidationError
	require.True(t, errors.As(err, &validationErr))
}

func TestSyncDNSZonesRejectsMalformedPath(t *testing.T) {
	svc := &DNSRecordService{DNSZoneMap: NewDNSZoneCacheFromMap(nil)}
	nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{
		DNSZones: []string{"/invalid/zone/path"},
	}}
	_, err := svc.SyncDNSZonesByVpcNetworkConfig(nc)
	require.ErrorContains(t, err, "invalid configured DNS zone path")
	var validationErr *DNSZoneValidationError
	require.True(t, errors.As(err, &validationErr))
}

func TestSyncDNSZonesRejectsZoneWithNilDomainName(t *testing.T) {
	ctrl := gomock.NewController(t)
	zoneMock := dnszonemocks.NewMockZonesClient(ctrl)
	zonePath := "/orgs/default/projects/project-1/dns-services/dns-svc-1/zones/zone-1"
	zoneMock.EXPECT().Get("default", "project-1", "dns-svc-1", "zone-1").Return(model.DnsZone{}, nil)
	svc := &DNSRecordService{
		Service: common.Service{
			NSXClient: &nsx.Client{DnsZoneClient: zoneMock},
		},
		DNSZoneMap: NewDNSZoneCacheFromMap(nil),
	}
	nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{
		DNSZones: []string{zonePath},
	}}
	_, err := svc.SyncDNSZonesByVpcNetworkConfig(nc)
	require.ErrorContains(t, err, "returned from NSX with no domain name")
	var validationErr *DNSZoneValidationError
	require.True(t, errors.As(err, &validationErr))
}

func TestParseDnsZonePath_table(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantOrg    string
		wantProj   string
		wantDNSSvc string
		wantZone   string
		wantErr    bool
	}{
		{
			name:       "valid path",
			path:       "/orgs/default/projects/project-1/dns-services/dns-svc-1/zones/zone-1",
			wantOrg:    "default",
			wantProj:   "project-1",
			wantDNSSvc: "dns-svc-1",
			wantZone:   "zone-1",
		},
		{
			name:    "project dns forwarder zone rejected",
			path:    "/orgs/default/projects/project-quality/infra/dns-forwarder-zones/default-dns-service",
			wantErr: true,
		},
		{
			name:    "infra dns forwarder zone rejected",
			path:    "/infra/dns-forwarder-zones/example-zone",
			wantErr: true,
		},
		{
			name:    "wrong segment (vpcs instead of dns-services)",
			path:    "/orgs/a/projects/p/vpcs/vpc1/zones/z",
			wantErr: true,
		},
		{
			name:    "surrounding whitespace",
			path:    " /orgs/a/projects/p/dns-services/d/zones/z ",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			org, proj, dnsSvc, zone, err := parseDnsZonePath(tc.path)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantOrg, org)
			require.Equal(t, tc.wantProj, proj)
			require.Equal(t, tc.wantDNSSvc, dnsSvc)
			require.Equal(t, tc.wantZone, zone)
		})
	}
}

func TestRefreshDNSZoneNotFoundInvalidatesCache(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		wantMissing bool
	}{
		{name: "NSX not found", err: nsxutil.NewNSXApiError(nil, apierrors.ErrorType_NOT_FOUND), wantMissing: true},
		{name: "index not ready", err: nsxutil.NewNSXApiError(&model.ApiError{ErrorCode: common.Int64(500090)}, apierrors.ErrorType_NOT_FOUND)},
		{name: "NSX unavailable", err: fmt.Errorf("NSX unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestDNSRecordService(t, BuildDNSRecordStore())
			env.DNSZoneMap = newDNSZoneCache()
			env.DNSZoneMap.set(testDNSZonePathZ, "old.example")
			zc := dnszonemocks.NewMockZonesClient(gomock.NewController(t))
			env.NSXClient.DnsZoneClient = zc
			zc.EXPECT().Get("org1", "proj1", "ds1", "zone-z").Return(model.DnsZone{}, tc.err)
			_, err := env.RefreshDNSZone(testDNSZonePathZ, false)
			var missing *DNSZoneNotFoundError
			require.Equal(t, tc.wantMissing, errors.As(err, &missing))
			cached, ok := env.DNSZoneMap.get(testDNSZonePathZ)
			require.Equal(t, !tc.wantMissing, ok)
			if ok {
				require.Equal(t, "old.example", cached)
			}
		})
	}
}

func TestRefreshDNSZoneBypassesStore(t *testing.T) {
	env := newTestDNSRecordService(t, BuildDNSRecordStore())
	env.DNSZoneMap = NewDNSZoneCacheFromMap(map[string]string{testDNSZonePathZ: "old.example"})
	require.NoError(t, env.DNSRecordStore.Add(&model.DnsRecord{
		Path: common.String("/orgs/org1/projects/proj1/dns-records/record"), ZonePath: common.String(testDNSZonePathZ),
	}))
	zc := dnszonemocks.NewMockZonesClient(gomock.NewController(t))
	env.NSXClient.DnsZoneClient = zc
	gomock.InOrder(
		zc.EXPECT().Get("org1", "proj1", "ds1", "zone-z").Return(model.DnsZone{DnsDomainName: common.String("new.example")}, nil),
		zc.EXPECT().Get("org1", "proj1", "ds1", "zone-z").Return(model.DnsZone{}, errors.New("NSX unavailable")),
	)
	domain, err := env.RefreshDNSZone(testDNSZonePathZ, false)
	require.NoError(t, err)
	require.Equal(t, "old.example", domain)
	domain, err = env.RefreshDNSZone(testDNSZonePathZ, true)
	require.NoError(t, err)
	require.Equal(t, "new.example", domain)
	domain, err = env.RefreshDNSZone(testDNSZonePathZ, true)
	require.Error(t, err)
	require.Empty(t, domain, "forced refresh must not report a cached domain")
	cached, ok := env.DNSZoneMap.get(testDNSZonePathZ)
	require.True(t, ok)
	require.Equal(t, "new.example", cached)
}

func TestSyncDNSZonesSkipsMissingZoneAndNotifies(t *testing.T) {
	env := newTestDNSRecordService(t, BuildDNSRecordStore())
	env.DNSZoneMap = newDNSZoneCache()
	zc := dnszonemocks.NewMockZonesClient(gomock.NewController(t))
	env.NSXClient.DnsZoneClient = zc
	zc.EXPECT().Get("org1", "proj1", "ds1", "zone-z").Return(model.DnsZone{}, nsxutil.NewNSXApiError(nil, apierrors.ErrorType_NOT_FOUND))
	zc.EXPECT().Get("org1", "proj1", "ds1", "zone-t").Return(model.DnsZone{DnsDomainName: common.String("healthy.example")}, nil)
	notifications := make(chan string, 1)
	env.SetDNSZoneChangeHandler(func(path string) { notifications <- path })
	nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{testDNSZonePathZ, testDNSZonePathT}}}
	domains, err := env.SyncDNSZonesByVpcNetworkConfig(nc)
	require.NoError(t, err)
	require.Equal(t, map[string]string{testDNSZonePathT: "healthy.example"}, domains)
	require.Equal(t, testDNSZonePathZ, waitDNSResult(t, notifications, "missing zone notification"))
}

func TestZoneRefreshSerializesNSXResponses(t *testing.T) {
	env := newTestDNSRecordService(t, BuildDNSRecordStore())
	env.DNSZoneMap = newDNSZoneCache()
	zc := dnszonemocks.NewMockZonesClient(gomock.NewController(t))
	env.NSXClient.DnsZoneClient = zc
	started, release, secondQuery := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	first := zc.EXPECT().Get("org1", "proj1", "ds1", "zone-z").DoAndReturn(func(_, _, _, _ string) (model.DnsZone, error) {
		close(started)
		<-release
		return model.DnsZone{DnsDomainName: common.String("old.example")}, nil
	})
	zc.EXPECT().Get("org1", "proj1", "ds1", "zone-z").After(first).DoAndReturn(func(_, _, _, _ string) (model.DnsZone, error) {
		close(secondQuery)
		return model.DnsZone{DnsDomainName: common.String("new.example")}, nil
	})
	nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{testDNSZonePathZ}}}
	done := make(chan error, 2)
	go func() { _, err := env.SyncDNSZonesByVpcNetworkConfig(nc); done <- err }()
	waitDNSResult(t, started, "first zone query")
	secondStarted := make(chan struct{})
	go func() { close(secondStarted); _, err := env.RefreshDNSZone(testDNSZonePathZ, false); done <- err }()
	waitDNSResult(t, secondStarted, "second zone refresh to start")
	select {
	case <-secondQuery:
		releaseOnce.Do(func() { close(release) })
		t.Fatal("second NSX GET overlapped an unpublished response")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	require.NoError(t, waitDNSResult(t, done, "first zone refresh to finish"))
	require.NoError(t, waitDNSResult(t, done, "second zone refresh to finish"))
	cached, ok := env.DNSZoneMap.get(testDNSZonePathZ)
	require.True(t, ok)
	require.Equal(t, "new.example", cached)
}

func TestZoneRefreshCoordinatesRecordWrites(t *testing.T) {
	for _, operation := range []string{"create", "GC delete"} {
		t.Run(operation, func(t *testing.T) {
			env := newTestDNSRecordService(t, BuildDNSRecordStore())
			owner := &ResourceRef{Kind: ResourceKindGateway, Object: &metav1.ObjectMeta{Name: "gw", Namespace: "ns"}}
			row := EndpointRow{Endpoint: extdns.NewEndpoint("host.example.com", extdns.RecordTypeA, "192.0.2.1"), zonePath: testDNSZonePathZ, nsxRecordName: "host"}
			batch := NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{row})
			registerBodiesForBatch(t, env, batch)
			if operation != "create" {
				requireNoErrCreateDNS(context.Background(), t, env, batch)
			}
			// A cache miss must fetch even for a populated zone.
			env.DNSZoneMap = newDNSZoneCache()
			zc := dnszonemocks.NewMockZonesClient(gomock.NewController(t))
			env.NSXClient.DnsZoneClient = zc
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			zc.EXPECT().Get("org1", "proj1", "ds1", "zone-z").DoAndReturn(func(_, _, _, _ string) (model.DnsZone, error) {
				close(started)
				<-release
				return model.DnsZone{DnsDomainName: common.String("example.com")}, nil
			})
			readDone, writeDone := make(chan error, 1), make(chan error, 1)
			go func() { _, err := env.RefreshDNSZone(testDNSZonePathZ, false); readDone <- err }()
			waitDNSResult(t, started, "zone query")
			writeStarted := make(chan struct{})
			go func() {
				close(writeStarted)
				var err error
				switch operation {
				case "create":
					_, err = env.CreateOrUpdateRecords(context.Background(), batch)
				case "GC delete":
					_, err = env.DeleteRecordByOwnerNN(context.Background(), owner.Kind, owner.GetNamespace(), owner.GetName())
				}
				writeDone <- err
			}()
			waitDNSResult(t, writeStarted, "record operation to start")
			select {
			case err := <-writeDone:
				releaseOnce.Do(func() { close(release) })
				waitDNSResult(t, readDone, "zone refresh to finish")
				t.Fatalf("record write completed during zone GET: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			releaseOnce.Do(func() { close(release) })
			require.NoError(t, waitDNSResult(t, readDone, "zone refresh to finish"))
			require.NoError(t, waitDNSResult(t, writeDone, "record operation to finish"))
			require.Equal(t, operation == "create", env.DNSRecordStore.HasActiveRecordsForZone(testDNSZonePathZ))
		})
	}
}
