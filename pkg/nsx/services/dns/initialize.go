/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"fmt"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// InitializeDNSRecordService constructs a DNSRecordService and hydrates DNSRecordStore from NSX Policy search.
func InitializeDNSRecordService(commonService common.Service, vpcService common.VPCServiceProvider) (*DNSRecordService, error) {
	builder, err := common.PolicyPathProjectDnsRecord.NewPolicyTreeBuilder()
	if err != nil {
		return nil, fmt.Errorf("creating DNS record tree builder: %w", err)
	}
	s := &DNSRecordService{
		Service:                 commonService,
		DNSRecordStore:          BuildDNSRecordStore(),
		VPCService:              vpcService,
		DNSZoneMap:              newDNSZoneCache(),
		ProjectDnsRecordBuilder: builder,
	}

	cluster := commonService.NSXConfig.CoeConfig.Cluster
	tags := []model.Tag{{Scope: common.String(common.TagScopeCluster), Tag: common.String(cluster)}}

	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error, 1)

	wg.Add(1)
	go commonService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeProjectDnsRecord, tags, s.DNSRecordStore)

	go func() {
		wg.Wait()
		close(wgDone)
	}()
	select {
	case <-wgDone:
	case err := <-fatalErrors:
		return s, err
	}

	// Warm up DNSZoneMap from zone paths used by records already in the store.
	for zonePath := range s.DNSRecordStore.ListZonePaths() {
		if _, found := s.DNSZoneMap.get(zonePath); found {
			continue
		}
		zone, err := s.getDNSZoneFromNSX(zonePath)
		if err != nil {
			log.Error(err, "failed to warm up DNS zone cache during initialization", "path", zonePath)
			continue // non-fatal: will be re-fetched on demand by SyncDNSZonesByVpcNetworkConfig
		}
		if zone.DnsDomainName != nil {
			s.DNSZoneMap.set(zonePath, strings.TrimSpace(*zone.DnsDomainName))
		}
	}

	return s, nil
}
