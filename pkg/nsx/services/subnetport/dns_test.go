/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"errors"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects"
	dns_services "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/dns_services"
	"go.uber.org/mock/gomock"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	dnszonemocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnszonesclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	dnsTestSubnetPath = "/orgs/org1/projects/project1/vpcs/vpc1/subnets/subnet1"
	dnsTestBlockPath1 = "/infra/ip-blocks/block1"
	dnsTestBlockPath2 = "/infra/ip-blocks/block2"
	dnsTestZonePath1  = "/orgs/org1/projects/project1/dns-services/dns1/zones/zone1"
	dnsTestZonePath2  = "/orgs/org1/projects/project1/dns-services/dns1/zones/zone2"
)

type dnsAutoRecordConfigsClientStub struct {
	projects.DnsAutoRecordConfigsClient
	list        func(cursor *string) (model.DnsAutoRecordConfigListResult, error)
	listProject func(orgID, projectID string, cursor *string) (model.DnsAutoRecordConfigListResult, error)
}

func (stub *dnsAutoRecordConfigsClientStub) List(orgID string, projectID string, cursor *string, _ *bool, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.DnsAutoRecordConfigListResult, error) {
	if stub.listProject != nil {
		return stub.listProject(orgID, projectID, cursor)
	}
	return stub.list(cursor)
}

func newDNSTestService(autoRecordClient projects.DnsAutoRecordConfigsClient, zoneClient dns_services.ZonesClient) *SubnetPortService {
	return &SubnetPortService{Service: servicecommon.Service{NSXClient: &nsx.Client{
		DnsAutoRecordConfigsClient: autoRecordClient,
		DnsZoneClient:              zoneClient,
	}}}
}

func dnsAutoRecordConfig(ipBlockPath, zonePath string) model.DnsAutoRecordConfig {
	return model.DnsAutoRecordConfig{IpBlockPath: ptr.To(ipBlockPath), ARecordZonePath: ptr.To(zonePath)}
}

func TestGetDNSAutoRecordConfigZonePaths(t *testing.T) {
	t.Run("paginates and caches project mappings", func(t *testing.T) {
		calls := 0
		auto := &dnsAutoRecordConfigsClientStub{list: func(cursor *string) (model.DnsAutoRecordConfigListResult, error) {
			calls++
			if cursor == nil {
				return model.DnsAutoRecordConfigListResult{
					Cursor: ptr.To("next"),
					Results: []model.DnsAutoRecordConfig{
						dnsAutoRecordConfig(dnsTestBlockPath1, dnsTestZonePath1),
						{IpBlockPath: nil, ARecordZonePath: ptr.To(dnsTestZonePath1)},
						{IpBlockPath: ptr.To(dnsTestBlockPath2), ARecordZonePath: nil},
					},
				}, nil
			}
			assert.Equal(t, "next", *cursor)
			return model.DnsAutoRecordConfigListResult{Results: []model.DnsAutoRecordConfig{
				dnsAutoRecordConfig(dnsTestBlockPath2, dnsTestZonePath2),
			}}, nil
		}}
		service := newDNSTestService(auto, nil)
		mappings, err := service.loadDNSAutoRecordConfigZonePaths("org1", "project1", false)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{dnsTestBlockPath1: dnsTestZonePath1, dnsTestBlockPath2: dnsTestZonePath2}, mappings)
		cached, err := service.loadDNSAutoRecordConfigZonePaths("org1", "project1", false)
		require.NoError(t, err)
		assert.Equal(t, mappings, cached)
		assert.Equal(t, 2, calls)
	})
	t.Run("refreshes expired project cache", func(t *testing.T) {
		calls := 0
		auto := &dnsAutoRecordConfigsClientStub{list: func(_ *string) (model.DnsAutoRecordConfigListResult, error) {
			calls++
			return model.DnsAutoRecordConfigListResult{Results: []model.DnsAutoRecordConfig{
				dnsAutoRecordConfig(dnsTestBlockPath1, dnsTestZonePath2),
			}}, nil
		}}
		service := newDNSTestService(auto, nil)
		cache := service.getDNSDomainCache()
		cache.autoRecordData["org1/project1"] = dnsAutoRecordConfigCacheEntry{
			zoneByIPBlock: map[string]string{dnsTestBlockPath1: dnsTestZonePath1},
			expiresAt:     time.Now().Add(-time.Second),
		}
		mappings, err := service.loadDNSAutoRecordConfigZonePaths("org1", "project1", false)
		require.NoError(t, err)
		assert.Equal(t, dnsTestZonePath2, mappings[dnsTestBlockPath1])
		assert.Equal(t, 1, calls)
	})
	t.Run("returns list error", func(t *testing.T) {
		auto := &dnsAutoRecordConfigsClientStub{list: func(_ *string) (model.DnsAutoRecordConfigListResult, error) {
			return model.DnsAutoRecordConfigListResult{}, errors.New("list failed")
		}}
		service := newDNSTestService(auto, nil)
		_, err := service.loadDNSAutoRecordConfigZonePaths("org1", "project1", false)
		require.Error(t, err)
	})
	t.Run("missing client", func(t *testing.T) {
		service := &SubnetPortService{Service: servicecommon.Service{NSXClient: &nsx.Client{}}}
		_, err := service.loadDNSAutoRecordConfigZonePaths("org1", "project1", false)
		require.Error(t, err)
	})
	t.Run("rejects repeated cursor", func(t *testing.T) {
		calls := 0
		auto := &dnsAutoRecordConfigsClientStub{list: func(_ *string) (model.DnsAutoRecordConfigListResult, error) {
			calls++
			return model.DnsAutoRecordConfigListResult{Cursor: ptr.To("same")}, nil
		}}
		service := newDNSTestService(auto, nil)
		_, err := service.loadDNSAutoRecordConfigZonePaths("org1", "project1", false)
		require.ErrorContains(t, err, "repeated cursor")
		assert.Equal(t, 2, calls)
	})
}

func TestGetDNSZoneDomainName(t *testing.T) {
	t.Run("gets and caches zone domain", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		zones := dnszonemocks.NewMockZonesClient(ctrl)
		zones.EXPECT().Get("org1", "project1", "dns1", "zone1").Return(model.DnsZone{DnsDomainName: ptr.To("example.com")}, nil).Times(1)
		service := newDNSTestService(nil, zones)
		domain, err := service.getDNSZoneDomainName(dnsTestZonePath1)
		require.NoError(t, err)
		assert.Equal(t, "example.com", domain)
		domain, err = service.getDNSZoneDomainName(dnsTestZonePath1)
		require.NoError(t, err)
		assert.Equal(t, "example.com", domain)
	})
	t.Run("refreshes expired zone cache", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		zones := dnszonemocks.NewMockZonesClient(ctrl)
		zones.EXPECT().Get("org1", "project1", "dns1", "zone1").Return(model.DnsZone{DnsDomainName: ptr.To("new.example.com")}, nil).Times(1)
		service := newDNSTestService(nil, zones)
		service.getDNSDomainCache().zoneDomains[dnsTestZonePath1] = dnsZoneDomainCacheEntry{
			domainName: "old.example.com",
			expiresAt:  time.Now().Add(-time.Second),
		}
		domain, err := service.getDNSZoneDomainName(dnsTestZonePath1)
		require.NoError(t, err)
		assert.Equal(t, "new.example.com", domain)
	})
	t.Run("invalid zone path", func(t *testing.T) {
		service := &SubnetPortService{}
		_, err := service.getDNSZoneDomainName("invalid")
		require.Error(t, err)
	})
	t.Run("missing client", func(t *testing.T) {
		service := &SubnetPortService{Service: servicecommon.Service{NSXClient: &nsx.Client{}}}
		_, err := service.getDNSZoneDomainName(dnsTestZonePath1)
		require.Error(t, err)
	})
	t.Run("returns API error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		zones := dnszonemocks.NewMockZonesClient(ctrl)
		zones.EXPECT().Get("org1", "project1", "dns1", "zone1").Return(model.DnsZone{}, errors.New("get failed"))
		service := newDNSTestService(nil, zones)
		_, err := service.getDNSZoneDomainName(dnsTestZonePath1)
		require.Error(t, err)
	})
	t.Run("rejects empty domain", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		zones := dnszonemocks.NewMockZonesClient(ctrl)
		zones.EXPECT().Get("org1", "project1", "dns1", "zone1").Return(model.DnsZone{DnsDomainName: ptr.To(" ")}, nil)
		service := newDNSTestService(nil, zones)
		_, err := service.getDNSZoneDomainName(dnsTestZonePath1)
		require.Error(t, err)
	})
}

func TestRefreshDNSAutoRecordConfigZoneHandling(t *testing.T) {
	t.Run("reuses a zone shared by multiple blocks across refreshes", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		zones := dnszonemocks.NewMockZonesClient(ctrl)
		zones.EXPECT().Get("org1", "project1", "dns1", "zone1").
			Return(model.DnsZone{DnsDomainName: ptr.To("example.com")}, nil).Times(1)
		auto := &dnsAutoRecordConfigsClientStub{list: func(_ *string) (model.DnsAutoRecordConfigListResult, error) {
			return model.DnsAutoRecordConfigListResult{Results: []model.DnsAutoRecordConfig{
				dnsAutoRecordConfig(dnsTestBlockPath1, dnsTestZonePath1),
				dnsAutoRecordConfig(dnsTestBlockPath2, dnsTestZonePath1),
			}}, nil
		}}
		service := newDNSTestService(auto, zones)
		require.NoError(t, service.RefreshDNSAutoRecordConfig("org1", "project1"))
		require.NoError(t, service.RefreshDNSAutoRecordConfig("org1", "project1"))
	})

	t.Run("propagates zone lookup failures", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		zones := dnszonemocks.NewMockZonesClient(ctrl)
		zones.EXPECT().Get("org1", "project1", "dns1", "zone1").
			Return(model.DnsZone{}, errors.New("zone lookup failed"))
		auto := &dnsAutoRecordConfigsClientStub{list: func(_ *string) (model.DnsAutoRecordConfigListResult, error) {
			return model.DnsAutoRecordConfigListResult{Results: []model.DnsAutoRecordConfig{
				dnsAutoRecordConfig(dnsTestBlockPath1, dnsTestZonePath1),
			}}, nil
		}}
		service := newDNSTestService(auto, zones)
		require.Error(t, service.RefreshDNSAutoRecordConfig("org1", "project1"))
	})
}

func TestResolveSubnetPortFQDNs(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		blocks                                     []string
		sameDomain, noConfig, zoneError, listError bool
		want                                       []string
	}{
		{name: "one block", blocks: []string{dnsTestBlockPath1}, want: []string{"pod.ns.v4.example.com"}},
		{name: "other block", blocks: []string{dnsTestBlockPath2}, want: []string{"pod.ns.v6.example.com"}},
		{name: "two domains", blocks: []string{dnsTestBlockPath1, dnsTestBlockPath2}, want: []string{"pod.ns.v4.example.com", "pod.ns.v6.example.com"}},
		{name: "deduplicate domains", blocks: []string{dnsTestBlockPath2, dnsTestBlockPath1, dnsTestBlockPath1}, sameDomain: true, want: []string{"pod.ns.example.com"}},
		{name: "no config", blocks: []string{dnsTestBlockPath1}, noConfig: true},
		{name: "unconfigured block", blocks: []string{"/infra/ip-blocks/unconfigured"}},
		{name: "no subnet blocks"},
		{name: "zone API failure", blocks: []string{dnsTestBlockPath1}, zoneError: true},
		{name: "list API failure", blocks: []string{dnsTestBlockPath1}, listError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			zones := dnszonemocks.NewMockZonesClient(ctrl)
			zones.EXPECT().Get("org1", "project1", "dns1", gomock.Any()).DoAndReturn(func(_, _, _, zoneID string) (model.DnsZone, error) {
				if tc.zoneError {
					return model.DnsZone{}, errors.New("zone failed")
				}
				domain := "v4.example.com"
				if zoneID == "zone2" {
					domain = "v6.example.com"
				}
				if tc.sameDomain {
					domain = "example.com"
				}
				return model.DnsZone{DnsDomainName: &domain}, nil
			}).AnyTimes()
			auto := &dnsAutoRecordConfigsClientStub{list: func(_ *string) (model.DnsAutoRecordConfigListResult, error) {
				if tc.listError {
					return model.DnsAutoRecordConfigListResult{}, errors.New("list failed")
				}
				if tc.noConfig {
					return model.DnsAutoRecordConfigListResult{}, nil
				}
				return model.DnsAutoRecordConfigListResult{Results: []model.DnsAutoRecordConfig{
					dnsAutoRecordConfig(dnsTestBlockPath1, dnsTestZonePath1), dnsAutoRecordConfig(dnsTestBlockPath2, dnsTestZonePath2),
				}}, nil
			}}
			service := newDNSTestService(auto, zones)
			refreshErr := service.RefreshDNSAutoRecordConfig("org1", "project1")
			if tc.zoneError || tc.listError {
				require.Error(t, refreshErr)
			} else {
				require.NoError(t, refreshErr)
			}
			assert.Nil(t, service.NSXClient.IPBlockClient, "subnet IPBlock paths are authoritative")
			// Resolution must work with the published snapshot and no API clients.
			service.NSXClient = nil
			subnet := &model.VpcSubnet{Path: ptr.To(dnsTestSubnetPath), IpBlocks: tc.blocks}
			got, err := service.ResolveSubnetPortFQDNs(subnet, "pod.ns")
			if tc.zoneError || tc.listError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	service := &SubnetPortService{}
	for _, subnet := range []*model.VpcSubnet{nil, {}, {Path: ptr.To(dnsTestSubnetPath)}} {
		got, err := service.ResolveSubnetPortFQDNs(subnet, "")
		require.NoError(t, err)
		assert.Empty(t, got)
	}
	_, err := service.ResolveSubnetPortFQDNs(&model.VpcSubnet{Path: ptr.To("bad")}, "host")
	require.Error(t, err)
}

func TestResolveSubnetPortFQDNsUsesLastRefreshedSnapshot(t *testing.T) {
	service := &SubnetPortService{}
	subnet := &model.VpcSubnet{Path: ptr.To(dnsTestSubnetPath), IpBlocks: []string{dnsTestBlockPath1}}
	_, err := service.ResolveSubnetPortFQDNs(subnet, "vm.eth0.ns")
	require.ErrorContains(t, err, "has not been loaded")
	cache := service.getDNSDomainCache()
	cache.autoRecordData["org1/project1"] = dnsAutoRecordConfigCacheEntry{
		zoneByIPBlock: map[string]string{dnsTestBlockPath1: dnsTestZonePath1},
		expiresAt:     time.Now().Add(-time.Minute),
	}
	cache.zoneDomains["org1/project1\x00"+dnsTestZonePath1] = dnsZoneDomainCacheEntry{
		domainName: "example.com", expiresAt: time.Now().Add(-time.Minute),
	}
	fqdns, err := service.ResolveSubnetPortFQDNs(subnet, "vm.eth0.ns")
	require.NoError(t, err)
	require.Equal(t, []string{"vm.eth0.ns.example.com"}, fqdns)
}

func TestBuildFQDN(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		domain   string
		want     string
	}{
		{name: "join and normalize", hostname: "MyHost", domain: "Example.COM.", want: "myhost.example.com"},
		{name: "already qualified", hostname: "host.example.com", domain: "example.com", want: "host.example.com"},
		{name: "unrelated suffix", hostname: "host.other.com", domain: "example.com", want: "host.other.com.example.com"},
		{name: "empty hostname", domain: "example.com", want: ""},
		{name: "empty domain", hostname: "host", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildFQDN(tt.hostname, tt.domain))
		})
	}
}

func TestRefreshAndPruneDNSCache(t *testing.T) {
	calls := 0
	fail := false
	auto := &dnsAutoRecordConfigsClientStub{list: func(_ *string) (model.DnsAutoRecordConfigListResult, error) {
		calls++
		if fail {
			return model.DnsAutoRecordConfigListResult{}, errors.New("unavailable")
		}
		if calls == 1 {
			return model.DnsAutoRecordConfigListResult{}, nil
		}
		return model.DnsAutoRecordConfigListResult{Results: []model.DnsAutoRecordConfig{dnsAutoRecordConfig(dnsTestBlockPath1, dnsTestZonePath1)}}, nil
	}}
	ctrl := gomock.NewController(t)
	zones := dnszonemocks.NewMockZonesClient(ctrl)
	zones.EXPECT().Get("org1", "project1", "dns1", "zone1").Return(model.DnsZone{DnsDomainName: ptr.To("example.com")}, nil)
	service := newDNSTestService(auto, zones)
	mappings, err := service.loadDNSAutoRecordConfigZonePaths("org1", "project1", false)
	require.NoError(t, err)
	assert.Empty(t, mappings)
	require.NoError(t, service.RefreshDNSAutoRecordConfig("org1", "project1"))
	mappings, err = service.loadDNSAutoRecordConfigZonePaths("org1", "project1", false)
	require.NoError(t, err)
	assert.Equal(t, dnsTestZonePath1, mappings[dnsTestBlockPath1])
	assert.Equal(t, 2, calls)
	fail = true
	require.Error(t, service.RefreshDNSAutoRecordConfig("org1", "project1"))
	mappings, err = service.loadDNSAutoRecordConfigZonePaths("org1", "project1", false)
	require.NoError(t, err)
	assert.Equal(t, dnsTestZonePath1, mappings[dnsTestBlockPath1], "failed refresh preserves the snapshot")
	cache := service.getDNSDomainCache()
	cache.zoneDomains["org1/project1\x00"+dnsTestZonePath1] = dnsZoneDomainCacheEntry{}
	cache.zoneDomains["org1/unused\x00zone"] = dnsZoneDomainCacheEntry{}
	service.PruneDNSCache(map[string]bool{"org1/project1": true})
	assert.Len(t, cache.autoRecordData, 1)
	assert.Len(t, cache.zoneDomains, 1)
	service.PruneDNSCache(nil)
	assert.Empty(t, cache.autoRecordData)
	assert.Empty(t, cache.zoneDomains)
}

func TestCreateSubnetPortDNSHostnameWithoutMapping(t *testing.T) {
	for _, tc := range []struct {
		name, hostname string
		enabled        bool
		want           *string
	}{
		{name: "no config still sets hostname", hostname: "pod.ns", enabled: true, want: ptr.To("pod.ns")},
		{name: "controller disables feature by passing no hostname"},
		{name: "empty owner hostname", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := createSubnetPortService(t)
			patches := gomonkey.ApplyFunc(nsx.DNSAutoRecordFeatureEnabled, func(*nsx.Client, *config.NSXOperatorConfig) bool { return tc.enabled })
			defer patches.Reset()
			subnet := &model.VpcSubnet{Path: ptr.To(dnsTestSubnetPath)}
			desired := model.VpcSubnetPort{Id: ptr.To("dns-port"), Path: ptr.To(dnsTestSubnetPath + "/ports/dns-port"), ParentPath: subnet.Path,
				Attachment: &model.PortAttachment{Id: ptr.To("attachment")}}
			patches.ApplyFunc((*SubnetPortService).buildSubnetPort, func(_ *SubnetPortService, _ interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, _, _ bool, _ v1alpha1.IPAddressType) (*model.VpcSubnetPort, error) {
				copy := desired
				return &copy, nil
			})
			var sent model.VpcSubnetPort
			patches.ApplyFunc((*fakePortClient).Patch, func(_ *fakePortClient, _, _, _, _, _ string, port model.VpcSubnetPort) error { sent = port; return nil })
			patches.ApplyFunc((*fakePortClient).Get, func(_ *fakePortClient, _, _, _, _, _ string) (model.VpcSubnetPort, error) { return sent, nil })
			patches.ApplyFunc((*SubnetPortService).CheckSubnetPortState, func(_ *SubnetPortService, _ interface{}, _ string) (*model.SegmentPortState, error) {
				return &model.SegmentPortState{}, nil
			})
			_, err := service.CreateOrUpdateSubnetPort(&v1alpha1.SubnetPort{}, subnet, "", nil, false, false, "", tc.hostname)
			require.NoError(t, err)
			assert.Equal(t, tc.want, sent.DnsHostname)
			assert.Nil(t, service.NSXClient.DnsAutoRecordConfigsClient, "missing DNS config client must not fail port creation")
		})
	}
}

func TestRefreshDNSAutoRecordConfigsIsolatesProjectFailures(t *testing.T) {
	calls := make(map[string]int)
	auto := &dnsAutoRecordConfigsClientStub{listProject: func(org, project string, _ *string) (model.DnsAutoRecordConfigListResult, error) {
		calls[org+"/"+project]++
		if project == "failed" {
			return model.DnsAutoRecordConfigListResult{}, errors.New("unavailable")
		}
		return model.DnsAutoRecordConfigListResult{}, nil
	}}
	service := newDNSTestService(auto, nil)
	refreshed, err := service.RefreshDNSAutoRecordConfigs(map[string]bool{
		"org1/healthy": true, "org1/failed": true, "org1/inactive": false, "invalid": true,
	})
	require.ErrorContains(t, err, "unavailable")
	require.ErrorContains(t, err, "invalid DNS project key")
	require.Equal(t, map[string]bool{"org1/healthy": true}, refreshed)
	require.Equal(t, map[string]int{"org1/healthy": 1, "org1/failed": 1}, calls)
}

func TestDNSCacheSeparatesProjectsSharingIPBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	zones := dnszonemocks.NewMockZonesClient(ctrl)
	for _, owner := range []struct{ org, project, domain string }{
		{"org1", "project1", "one.example.com"},
		{"org1", "project2", "two.example.com"},
		{"org2", "project1", "other-org.example.com"},
	} {
		zones.EXPECT().Get(owner.org, owner.project, "dns1", "zone").Return(model.DnsZone{DnsDomainName: ptr.To(owner.domain)}, nil).Times(1)
	}
	calls := make(map[string]int)
	removed := false
	auto := &dnsAutoRecordConfigsClientStub{listProject: func(orgID, projectID string, _ *string) (model.DnsAutoRecordConfigListResult, error) {
		calls[orgID+"/"+projectID]++
		if removed && orgID == "org1" && projectID == "project1" {
			return model.DnsAutoRecordConfigListResult{}, nil
		}
		zonePath := "/orgs/" + orgID + "/projects/" + projectID + "/dns-services/dns1/zones/zone"
		return model.DnsAutoRecordConfigListResult{Results: []model.DnsAutoRecordConfig{dnsAutoRecordConfig(dnsTestBlockPath1, zonePath)}}, nil
	}}
	service := newDNSTestService(auto, zones)
	refreshed, err := service.RefreshDNSAutoRecordConfigs(map[string]bool{"org1/project1": true, "org1/project2": true, "org2/project1": true})
	require.NoError(t, err)
	require.Len(t, refreshed, 3)
	resolve := func(org, project, domain string) {
		t.Helper()
		subnet := &model.VpcSubnet{Path: ptr.To("/orgs/" + org + "/projects/" + project + "/vpcs/vpc1/subnets/subnet1"), IpBlocks: []string{dnsTestBlockPath1}}
		got, err := service.ResolveSubnetPortFQDNs(subnet, "pod.ns")
		require.NoError(t, err)
		if domain == "" {
			require.Empty(t, got)
		} else {
			require.Equal(t, []string{"pod.ns." + domain}, got)
		}
	}
	for i := 0; i < 2; i++ {
		resolve("org1", "project1", "one.example.com")
		resolve("org1", "project2", "two.example.com")
		resolve("org2", "project1", "other-org.example.com")
	}
	require.Equal(t, map[string]int{"org1/project1": 1, "org1/project2": 1, "org2/project1": 1}, calls)
	// Refreshing/removing project1 must not invalidate project2 or the other org.
	removed = true
	require.NoError(t, service.RefreshDNSAutoRecordConfig("org1", "project1"))
	resolve("org1", "project1", "")
	resolve("org1", "project2", "two.example.com")
	resolve("org2", "project1", "other-org.example.com")
	service.PruneDNSCache(map[string]bool{"org1/project2": true, "org2/project1": true})
	resolve("org1", "project2", "two.example.com")
	resolve("org2", "project1", "other-org.example.com")
	require.Equal(t, map[string]int{"org1/project1": 2, "org1/project2": 1, "org2/project1": 1}, calls)
}

func TestCreateOrUpdateSubnetPortLeavesFQDNStatusUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name      string
		existing  bool
		hostname  string
		wantPatch int
	}{
		{name: "created port", hostname: "pod.ns", wantPatch: 1},
		{name: "unchanged realized port", existing: true, hostname: "pod.ns"},
		{name: "update hostname", existing: true, hostname: "new.ns", wantPatch: 1},
		{name: "no hostname supplied preserves existing port", existing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := createSubnetPortService(t)
			patches := gomonkey.ApplyFunc(nsx.DNSAutoRecordFeatureEnabled, func(*nsx.Client, *config.NSXOperatorConfig) bool { return true })
			defer patches.Reset()
			subnet := &model.VpcSubnet{Path: ptr.To(dnsTestSubnetPath), IpBlocks: []string{dnsTestBlockPath1, dnsTestBlockPath2}}
			desired := model.VpcSubnetPort{Id: ptr.To("dns-port"), Path: ptr.To(dnsTestSubnetPath + "/ports/dns-port"), ParentPath: subnet.Path,
				Attachment: &model.PortAttachment{Id: ptr.To("attachment")}}
			if tc.existing {
				desired.DnsHostname = ptr.To("pod.ns")
				require.NoError(t, service.SubnetPortStore.Add(&desired))
			}
			patches.ApplyFunc((*SubnetPortService).buildSubnetPort, func(_ *SubnetPortService, _ interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, _, _ bool, _ v1alpha1.IPAddressType) (*model.VpcSubnetPort, error) {
				copy := desired
				return &copy, nil
			})
			patches.ApplyFunc((*SubnetPortService).portAlreadyRealized, func(*SubnetPortService, interface{}, *model.VpcSubnetPort) bool { return true })
			var sent model.VpcSubnetPort
			patchCalls, stateCalls := 0, 0
			patches.ApplyFunc((*fakePortClient).Patch, func(_ *fakePortClient, _, _, _, _, _ string, port model.VpcSubnetPort) error {
				patchCalls++
				sent = port
				return nil
			})
			patches.ApplyFunc((*fakePortClient).Get, func(_ *fakePortClient, _, _, _, _, _ string) (model.VpcSubnetPort, error) { return sent, nil })
			patches.ApplyFunc((*SubnetPortService).CheckSubnetPortState, func(_ *SubnetPortService, _ interface{}, _ string) (*model.SegmentPortState, error) {
				stateCalls++
				return &model.SegmentPortState{RealizedBindings: []model.AddressBindingEntry{
					{Binding: &model.PacketAddressClassifier{IpAddress: ptr.To("10.0.0.8")}},
					{Binding: &model.PacketAddressClassifier{IpAddress: ptr.To("2001:db8::8")}},
				}}, nil
			})
			port := &v1alpha1.SubnetPort{Spec: v1alpha1.SubnetPortSpec{InterfaceIPType: v1alpha1.IPAddressTypeIPv4IPv6}}
			port.Status.FQDNs = []string{"stale.example.com"}
			state, err := service.CreateOrUpdateSubnetPort(port, subnet, "", nil, false, false, v1alpha1.IPAddressTypeIPv4IPv6, tc.hostname)
			require.NoError(t, err)
			require.Equal(t, []string{"stale.example.com"}, port.Status.FQDNs)
			require.Equal(t, tc.wantPatch, patchCalls)
			if tc.wantPatch == 0 {
				require.Nil(t, state)
				require.Zero(t, stateCalls)
			} else {
				require.NotNil(t, state)
				require.Equal(t, 1, stateCalls)
				if tc.hostname == "" {
					require.Nil(t, sent.DnsHostname)
					require.Nil(t, service.SubnetPortStore.GetByKey("dns-port").DnsHostname)
					_, err = service.CreateOrUpdateSubnetPort(port, subnet, "", nil, false, false, v1alpha1.IPAddressTypeIPv4IPv6, "")
					require.NoError(t, err)
					require.Equal(t, 1, patchCalls, "cleared hostname must remain cleared without another PATCH")
				} else {
					require.Equal(t, tc.hostname, *sent.DnsHostname)
				}
			}
		})
	}
}
