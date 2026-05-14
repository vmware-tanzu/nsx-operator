/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extprovider "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/provider"
)

// NSX Policy path: /orgs/{org}/projects/{project}/dns-services/{dnsService}/zones/{zoneId}
var projectDNSZonePathRe = regexp.MustCompile(`^/orgs/([^/]+)/projects/([^/]+)/dns-services/([^/]+)/zones/([^/]+)$`)

// parseProjectDNSZonePath splits a Policy zone path into org, project, DNS service, and zone ID.
func parseProjectDNSZonePath(zonePath string) (orgID, projectID, dnsServiceID, zoneID string, err error) {
	p := strings.TrimSpace(zonePath)
	if p == "" {
		return "", "", "", "", fmt.Errorf("empty DNS zone path")
	}
	matches := projectDNSZonePathRe.FindStringSubmatch(p)
	if len(matches) != 5 {
		return "", "", "", "", fmt.Errorf("invalid DNS zone path %q: expected /orgs/{org}/projects/{project}/dns-services/{dns-service}/zones/{zone}", zonePath)
	}
	return matches[1], matches[2], matches[3], matches[4], nil
}

// endpointDNSNameIsWildcard reports whether dnsName requests a wildcard apex record (e.g. "*.example.com").
// NSX DNS policy does not publish such names; ValidateEndpointsByZone skips them without error.
func endpointDNSNameIsWildcard(dnsName string) bool {
	h := strings.TrimSpace(dnsName)
	return strings.HasPrefix(strings.ToLower(h), "*.")
}

// dnsNameForZoneMatch normalizes hostname for FindZone (trim, strip trailing dot, strip leading "*." only).
func dnsNameForZoneMatch(hostname string) string {
	h := strings.TrimSpace(hostname)
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(h), "*.") && len(h) >= 2 {
		return h[2:]
	}
	return h
}

// getZonePathForHostname returns (relativeRecordName, zonePath, err) for hostname against z.
func (s *DNSRecordService) getZonePathForHostname(z extprovider.ZoneIDName, hostname string) (string, string, error) {
	name := dnsNameForZoneMatch(hostname)
	if name == "" {
		return "", "", fmt.Errorf("empty hostname")
	}

	zonePath, matchedDomain, normalizedFQDN := z.FindZone(name)
	if matchedDomain == "" {
		return "", "", fmt.Errorf("hostname %q does not match any allowed DNS domain in the namespace", hostname)
	}
	if normalizedFQDN == matchedDomain {
		return "", "", fmt.Errorf("hostname %q must not equal to the allowed DNS domain %q", hostname, matchedDomain)
	}
	suffix := "." + matchedDomain
	if !strings.HasSuffix(normalizedFQDN, suffix) || normalizedFQDN == suffix {
		return "", "", fmt.Errorf("hostname %q does not lie under matched zone %q", hostname, matchedDomain)
	}
	recordName := strings.TrimSuffix(normalizedFQDN, suffix)
	return recordName, zonePath, nil
}

// ValidateEndpointsByZone maps each endpoint to a zone and row; returns validated rows, path→domain map for permitted zones (from sync), and err. owner must be non-nil.
func (s *DNSRecordService) ValidateEndpointsByZone(namespace string, owner *ResourceRef, eps []*extdns.Endpoint) ([]EndpointRow, map[string]string, error) {
	log.Info("Validating DNS endpoints by zone", "namespace", namespace,
		"owner", owner.GetName(), "endpoints", len(eps))
	vpcConfig, err := s.VPCService.GetVPCNetworkConfigByNamespace(namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find VPCNetworkConfiguration for the Namespace %s", namespace)
	}

	allowedZones, err := s.SyncDNSZonesByVpcNetworkConfig(vpcConfig)
	if err != nil {
		return nil, nil, err
	}
	if len(allowedZones) == 0 {
		return nil, nil, &DNSZoneValidationError{Msg: fmt.Sprintf("no DNS zones are permitted for the namespace %s", namespace)}
	}
	z := generateZoneIdFromMap(allowedZones)

	var rows []EndpointRow
	for i := range eps {
		ep := eps[i]
		if endpointDNSNameIsWildcard(ep.DNSName) {
			log.Info("Skipping DNS endpoint: wildcard DNS names are not supported for NSX DNS records", "dnsName", ep.DNSName)
			continue
		}
		recName, zonePath, parseErr := s.getZonePathForHostname(z, ep.DNSName)
		if parseErr != nil {
			return nil, allowedZones, &DNSZoneValidationError{Msg: parseErr.Error()}
		}
		log.Debug("Mapped DNS endpoint to zone", "dnsName", ep.DNSName, "zonePath", zonePath, "recordName", recName)
		row, validErr := s.validateEndpointRowConflict(zonePath, ep, recName, owner)
		if validErr != nil {
			return nil, allowedZones, &DNSZoneValidationError{Msg: "DNS endpoint validation failed for DNS zone policy", Cause: validErr}
		}
		rows = append(rows, *row)
	}
	return rows, allowedZones, nil
}

// SyncDNSZonesByVpcNetworkConfig ensures DNSZoneMap entries for vpcConfig.Spec.DNSZones; returns path→domain map or err.
func (s *DNSRecordService) SyncDNSZonesByVpcNetworkConfig(vpcConfig *v1alpha1.VPCNetworkConfiguration) (map[string]string, error) {
	dnsZonePaths := vpcConfig.Spec.DNSZones
	if len(dnsZonePaths) == 0 {
		return nil, nil
	}
	log.Info("Syncing DNS zones for VPCNetworkConfig", "zones", len(dnsZonePaths))

	dnsZoneDomainMapping := make(map[string]string)
	for _, p := range dnsZonePaths {
		domain, found := s.DNSZoneMap.get(p)
		if found {
			log.Debug("DNS zone domain resolved from cache", "path", p, "domain", domain)
			dnsZoneDomainMapping[p] = domain
			continue
		}
		log.Info("Fetching DNS zone domain from NSX", "path", p)
		zone, err := s.getDNSZoneFromNSX(p)
		if err != nil {
			log.Error(err, "failed to retrieve DNS zone from NSX", "path", p)
			return nil, err
		}
		if zone.DnsDomainName == nil {
			return nil, fmt.Errorf("DNS zone %s returned from NSX with no domain name", p)
		}
		domain = strings.TrimSpace(*zone.DnsDomainName)
		log.Info("DNS zone domain fetched from NSX and cached", "path", p, "domain", domain)
		s.DNSZoneMap.set(p, domain)
		dnsZoneDomainMapping[p] = domain
	}
	return dnsZoneDomainMapping, nil
}

func (s *DNSRecordService) getDNSZoneFromNSX(zonePath string) (*model.ProjectDnsZone, error) {
	orgID, projectID, dnsServiceID, zoneID, err := parseProjectDNSZonePath(zonePath)
	if err != nil {
		return nil, err
	}
	z, err := s.NSXClient.ProjectDnsZoneClient.Get(orgID, projectID, dnsServiceID, zoneID)
	if err != nil {
		return nil, nsxutil.TransNSXApiError(err)
	}
	out := z
	return &out, nil
}

func generateZoneIdFromMap(allowedZones map[string]string) extprovider.ZoneIDName {
	z := make(extprovider.ZoneIDName)
	for zonePath, domainName := range allowedZones {
		z.Add(zonePath, domainName)
	}
	return z
}
