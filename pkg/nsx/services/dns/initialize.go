/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// InitializeDNSRecordService constructs a DNSRecordService and restores records
// from both NSX search and project-scoped Policy lists. Some NSX versions do not
// return DNSRecord objects in search results.
func InitializeDNSRecordService(commonService common.Service, vpcService common.VPCServiceProvider, apiReaders ...client.Reader) (*DNSRecordService, error) {
	builder, err := common.PolicyPathDnsRecord.NewPolicyTreeBuilder()
	if err != nil {
		return nil, fmt.Errorf("creating DNS record tree builder: %w", err)
	}
	s := &DNSRecordService{
		Service:          commonService,
		DNSRecordStore:   BuildDNSRecordStore(),
		VPCService:       vpcService,
		DNSZoneMap:       newDNSZoneCache(),
		DnsRecordBuilder: builder,
	}

	cluster := commonService.NSXConfig.CoeConfig.Cluster
	tags := []model.Tag{{Scope: common.String(common.TagScopeCluster), Tag: common.String(cluster)}}

	wg := sync.WaitGroup{}
	fatalErrors := make(chan error, 1)

	wg.Add(1)
	commonService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeDnsRecord, tags, s.DNSRecordStore)
	wg.Wait()
	select {
	case err := <-fatalErrors:
		return s, err
	default:
	}

	// DNSRecord is absent from NSX search results on some versions. Read the
	// project-scoped Policy API so deletes and GC can find pre-restart records.
	var apiReader client.Reader
	if len(apiReaders) > 0 {
		apiReader = apiReaders[0]
	}
	if err := s.restoreDNSRecordsFromProjects(cluster, apiReader); err != nil {
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

func (s *DNSRecordService) restoreDNSRecordsFromProjects(cluster string, apiReader client.Reader) error {
	projects := make(map[string][2]string)
	addProject := func(org, project string) {
		if org != "" && project != "" {
			projects[org+"/"+project] = [2]string{org, project}
		}
	}
	if apiReader != nil {
		configs := &v1alpha1.VPCNetworkConfigurationList{}
		if err := apiReader.List(context.Background(), configs); err != nil {
			return fmt.Errorf("listing VPCNetworkConfigurations for DNS record restore: %w", err)
		}
		for i := range configs.Items {
			nc := &configs.Items[i]
			if nc.Spec.NSXProject != "" {
				org, project, err := common.NSXProjectPathToId(nc.Spec.NSXProject)
				if err == nil {
					addProject(org, project)
				}
			}
			for _, zonePath := range nc.Spec.DNSZones {
				org, project, _, _, err := parseDnsZonePath(zonePath)
				if err == nil {
					addProject(org, project)
				}
			}
			for _, vpc := range nc.Status.VPCs {
				info, err := common.ParseVPCResourcePath(vpc.VPCPath)
				if err == nil {
					addProject(info.OrgID, info.ProjectID)
				}
			}
		}
	}
	// Configurations are not a complete inventory: their projects disappear after
	// deletion or migration. Enumerate NSX projects so startup GC and clean mode
	// can still discover DNS records in those projects.
	if s.NSXClient.OrgsClient != nil && s.NSXClient.ProjectClient != nil {
		var orgCursor *string
		for {
			orgPage, err := s.NSXClient.OrgsClient.List(orgCursor, nil, nil, nil, nil, nil, nil)
			if err != nil {
				log.Warn("Failed to list NSX organizations for DNS record restore; skipping global project enumeration", "err", err)
				break
			}
			for _, org := range orgPage.Results {
				if org.Id == nil {
					continue
				}
				var projectCursor *string
				for {
					projectPage, err := s.NSXClient.ProjectClient.List(*org.Id, projectCursor, nil, nil, nil, nil, nil, nil, nil)
					if err != nil {
						log.Warn("Failed to list NSX projects in organization for DNS record restore", "org", *org.Id, "err", err)
						break
					}
					for _, project := range projectPage.Results {
						if project.Id != nil {
							addProject(*org.Id, *project.Id)
						}
					}
					if projectPage.Cursor == nil || *projectPage.Cursor == "" {
						break
					}
					projectCursor = projectPage.Cursor
				}
			}
			if orgPage.Cursor == nil || *orgPage.Cursor == "" {
				break
			}
			orgCursor = orgPage.Cursor
		}
	}
	for _, ids := range projects {
		org, project := ids[0], ids[1]
		var cursor *string
		for {
			page, err := s.NSXClient.DnsRecordsClient.List(org, project, cursor, nil, nil, nil, nil, nil, nil, nil)
			if err != nil {
				return fmt.Errorf("listing DNS records in project %s/%s for restore: %w", org, project, err)
			}
			var records []*model.DnsRecord
			for i := range page.Results {
				rec := &page.Results[i]
				if rec.Path == nil || !slices.Contains(filterTagBy(rec.Tags, common.TagScopeCluster), cluster) {
					continue
				}
				records = append(records, rec)
			}
			if err := s.DNSRecordStore.Apply(records); err != nil {
				return fmt.Errorf("restoring DNS records in project %s/%s: %w", org, project, err)
			}
			if page.Cursor == nil || *page.Cursor == "" {
				break
			}
			cursor = page.Cursor
		}
	}
	return nil
}
