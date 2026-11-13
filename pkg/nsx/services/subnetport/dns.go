/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"golang.org/x/sync/singleflight"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// DNSAutoRecordSyncInterval bounds mapping cache freshness and drives the
// controller's periodic sync of FQDN status.
const (
	DNSAutoRecordSyncInterval = 10 * time.Minute
	dnsDomainCacheTTL         = 10 * time.Minute
)

var dnsZonePathPattern = regexp.MustCompile(`^/orgs/([^/]+)/projects/([^/]+)/dns-services/([^/]+)/zones/([^/]+)$`)

type dnsAutoRecordConfigCacheEntry struct {
	zoneByIPBlock map[string]string
	expiresAt     time.Time
}

type dnsZoneDomainCacheEntry struct {
	domainName string
	expiresAt  time.Time
}

// dnsDomainCache scopes mappings by consuming org/project before IPBlock path.
// The same shared IPBlock can map to different DNS zones in different projects.
// Zone entries also include the consuming project in their keys.
type dnsDomainCache struct {
	mu             sync.RWMutex
	autoRecordData map[string]dnsAutoRecordConfigCacheEntry
	zoneDomains    map[string]dnsZoneDomainCacheEntry
	refreshGroup   singleflight.Group
}

func newDNSDomainCache() *dnsDomainCache {
	return &dnsDomainCache{
		autoRecordData: make(map[string]dnsAutoRecordConfigCacheEntry),
		zoneDomains:    make(map[string]dnsZoneDomainCacheEntry),
	}
}

func (service *SubnetPortService) getDNSDomainCache() *dnsDomainCache {
	service.dnsDomainCacheOnce.Do(func() {
		service.dnsDomainCache = newDNSDomainCache()
	})
	return service.dnsDomainCache
}

// PruneDNSCache drops projects with no remaining ports using DNS hostnames.
func (service *SubnetPortService) PruneDNSCache(activeProjects map[string]bool) {
	cache := service.getDNSDomainCache()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for project := range cache.autoRecordData {
		if !activeProjects[project] {
			delete(cache.autoRecordData, project)
		}
	}
	for key := range cache.zoneDomains {
		project, _, _ := strings.Cut(key, "\x00")
		if !activeProjects[project] {
			delete(cache.zoneDomains, key)
		}
	}

}

// ResolveSubnetPortFQDNs reads the cached project mappings and zone domains
// without querying NSX or modifying a Kubernetes CR. It follows IPBlock paths to
// auto-record configs and their DNS zones. IP addresses and CIDRs are not needed:
// the subnet already identifies its blocks, including blocks using ranges.
func (service *SubnetPortService) ResolveSubnetPortFQDNs(nsxSubnet *model.VpcSubnet, hostname string) ([]string, error) {
	if strings.TrimSpace(hostname) == "" || nsxSubnet == nil || nsxSubnet.Path == nil {
		return nil, nil
	}
	vpcInfo, err := servicecommon.ParseVPCResourcePath(*nsxSubnet.Path)
	if err != nil {
		return nil, err
	}
	projectKey := vpcInfo.OrgID + "/" + vpcInfo.ProjectID
	cache := service.getDNSDomainCache()
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	entry, loaded := cache.autoRecordData[projectKey]
	if !loaded {
		return nil, fmt.Errorf("DNS auto-record mapping for project %s has not been loaded", projectKey)
	}
	var fqdns []string
	seen := make(map[string]bool)
	for _, ipBlockPath := range nsxSubnet.IpBlocks {
		zonePath, configured := entry.zoneByIPBlock[ipBlockPath]
		if !configured {
			continue
		}
		domain, loaded := cache.zoneDomains[projectKey+"\x00"+zonePath]
		if !loaded {
			return nil, fmt.Errorf("DNS domain for zone %s has not been loaded", zonePath)
		}
		fqdn := buildFQDN(hostname, domain.domainName)
		if fqdn != "" && !seen[fqdn] {
			seen[fqdn] = true
			fqdns = append(fqdns, fqdn)
		}
	}
	slices.Sort(fqdns)
	return fqdns, nil
}

// RefreshDNSAutoRecordConfigs refreshes each active project once and returns
// the successful projects so a failed project does not block the others.
func (service *SubnetPortService) RefreshDNSAutoRecordConfigs(activeProjects map[string]bool) (map[string]bool, error) {
	refreshedProjects := make(map[string]bool)
	var refreshErrors []error
	for projectKey, active := range activeProjects {
		if !active {
			continue
		}
		orgID, projectID, valid := strings.Cut(projectKey, "/")
		if !valid || orgID == "" || projectID == "" {
			refreshErrors = append(refreshErrors, fmt.Errorf("invalid DNS project key %q", projectKey))
			continue
		}
		if err := service.RefreshDNSAutoRecordConfig(orgID, projectID); err != nil {
			refreshErrors = append(refreshErrors, fmt.Errorf("refresh DNS project %s: %w", projectKey, err))
			continue
		}
		refreshedProjects[projectKey] = true
	}
	return refreshedProjects, errors.Join(refreshErrors...)
}

// RefreshDNSAutoRecordConfig refreshes one project's mapping independently of
// its TTL, so a periodic status sync sees newly configured mappings immediately.
func (service *SubnetPortService) RefreshDNSAutoRecordConfig(orgID, projectID string) error {
	zoneByIPBlock, err := service.loadDNSAutoRecordConfigZonePaths(orgID, projectID, true)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, zonePath := range zoneByIPBlock {
		if seen[zonePath] {
			continue
		}
		seen[zonePath] = true
		if _, err := service.getDNSZoneDomainName(zonePath, orgID+"/"+projectID); err != nil {
			return err
		}
	}
	return nil
}

func (service *SubnetPortService) loadDNSAutoRecordConfigZonePaths(orgID, projectID string, force bool) (map[string]string, error) {
	cache := service.getDNSDomainCache()
	projectKey := orgID + "/" + projectID
	if entry, found := cache.getAutoRecordConfigEntry(projectKey); found && !force {
		return entry.zoneByIPBlock, nil
	}
	value, err, _ := cache.refreshGroup.Do("project:"+projectKey, func() (interface{}, error) {
		if entry, found := cache.getAutoRecordConfigEntry(projectKey); found && !force {
			return entry.zoneByIPBlock, nil
		}
		if service.NSXClient == nil || service.NSXClient.DnsAutoRecordConfigsClient == nil {
			return nil, fmt.Errorf("DNS auto-record-config client is not initialized")
		}

		zoneByIPBlock := make(map[string]string)
		pageSize := int64(1000)
		var cursor *string
		for {
			result, err := service.NSXClient.DnsAutoRecordConfigsClient.List(orgID, projectID, cursor, nil, nil, nil, &pageSize, nil, nil)
			if err != nil {
				return nil, nsxutil.TransNSXApiError(err)
			}
			for _, config := range result.Results {
				if config.IpBlockPath == nil || *config.IpBlockPath == "" || config.ARecordZonePath == nil || *config.ARecordZonePath == "" {
					continue
				}
				zoneByIPBlock[*config.IpBlockPath] = *config.ARecordZonePath
			}
			if result.Cursor == nil || strings.TrimSpace(*result.Cursor) == "" {
				break
			}
			if cursor != nil && *cursor == *result.Cursor {
				return nil, fmt.Errorf("DNS auto-record-config list returned a repeated cursor %q", *result.Cursor)
			}
			nextCursor := *result.Cursor
			cursor = &nextCursor
		}

		cache.mu.Lock()
		if oldEntry, found := cache.autoRecordData[projectKey]; found {
			for _, oldZonePath := range oldEntry.zoneByIPBlock {
				stillUsed := false
				for _, newZonePath := range zoneByIPBlock {
					if oldZonePath == newZonePath {
						stillUsed = true
						break
					}
				}
				if !stillUsed {
					delete(cache.zoneDomains, projectKey+"\x00"+oldZonePath)
				}
			}
		}
		cache.autoRecordData[projectKey] = dnsAutoRecordConfigCacheEntry{
			zoneByIPBlock: zoneByIPBlock,
			expiresAt:     time.Now().Add(DNSAutoRecordSyncInterval),
		}
		cache.mu.Unlock()
		return zoneByIPBlock, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(map[string]string), nil
}

func (service *SubnetPortService) getDNSZoneDomainName(zonePath string, projectKeys ...string) (string, error) {
	cache := service.getDNSDomainCache()
	cacheKey := zonePath
	if len(projectKeys) > 0 && projectKeys[0] != "" {
		cacheKey = projectKeys[0] + "\x00" + zonePath
	}
	if entry, found := cache.getZoneDomainEntry(cacheKey); found {
		return entry.domainName, nil
	}
	value, err, _ := cache.refreshGroup.Do("zone:"+cacheKey, func() (interface{}, error) {
		if entry, found := cache.getZoneDomainEntry(cacheKey); found {
			return entry.domainName, nil
		}
		matches := dnsZonePathPattern.FindStringSubmatch(strings.TrimSpace(zonePath))
		if len(matches) != 5 {
			return "", fmt.Errorf("invalid DNS zone path %q", zonePath)
		}
		if service.NSXClient == nil || service.NSXClient.DnsZoneClient == nil {
			return "", fmt.Errorf("DNS zone client is not initialized")
		}
		zone, err := service.NSXClient.DnsZoneClient.Get(matches[1], matches[2], matches[3], matches[4])
		if err != nil {
			return "", nsxutil.TransNSXApiError(err)
		}
		if zone.DnsDomainName == nil || strings.TrimSpace(*zone.DnsDomainName) == "" {
			return "", fmt.Errorf("DNS zone %s has no domain name", zonePath)
		}
		domainName := strings.TrimSpace(*zone.DnsDomainName)
		cache.mu.Lock()
		cache.zoneDomains[cacheKey] = dnsZoneDomainCacheEntry{
			domainName: domainName,
			expiresAt:  time.Now().Add(dnsDomainCacheTTL),
		}
		cache.mu.Unlock()
		return domainName, nil
	})
	if err != nil {
		return "", err
	}
	return value.(string), nil
}

func (cache *dnsDomainCache) getAutoRecordConfigEntry(projectKey string) (dnsAutoRecordConfigCacheEntry, bool) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	entry, found := cache.autoRecordData[projectKey]
	return entry, found && time.Now().Before(entry.expiresAt)
}

func (cache *dnsDomainCache) getZoneDomainEntry(zonePath string) (dnsZoneDomainCacheEntry, bool) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	entry, found := cache.zoneDomains[zonePath]
	return entry, found && time.Now().Before(entry.expiresAt)
}

// buildFQDN combines a host label or partial name with the domain. If the
// supplied hostname is already under the domain, it is returned unchanged.
func buildFQDN(hostname, domainName string) string {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domainName)), ".")
	if host == "" {
		return ""
	}
	if domain == "" {
		return ""
	}
	if strings.EqualFold(host, domain) || strings.HasSuffix(host, "."+domain) {
		return host
	}
	return host + "." + domain
}
