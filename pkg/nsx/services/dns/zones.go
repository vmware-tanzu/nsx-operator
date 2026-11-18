/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extprovider "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/provider"
)

// NSX Policy path regexes:
// Standard project DNS zone: /orgs/{org}/projects/{project}/dns-services/{dnsService}/zones/{zoneId}
var dnsZonePathRe = regexp.MustCompile(`^/orgs/([^/]+)/projects/([^/]+)/dns-services/([^/]+)/zones/([^/]+)$`)

// parseDnsZonePath splits a Policy zone path into org, project, DNS service, and zone ID.
func parseDnsZonePath(zonePath string) (orgID, projectID, dnsServiceID, zoneID string, err error) {
	p := strings.TrimSpace(zonePath)
	if p == "" {
		return "", "", "", "", fmt.Errorf("empty DNS zone path")
	}
	if p != zonePath {
		return "", "", "", "", fmt.Errorf("invalid DNS zone path %q: surrounding whitespace is not allowed", zonePath)
	}
	if matches := dnsZonePathRe.FindStringSubmatch(p); len(matches) == 5 {
		return matches[1], matches[2], matches[3], matches[4], nil
	}
	if strings.Contains(p, "/dns-forwarder-zones/") {
		return "", "", "", "", fmt.Errorf("DNS forwarder zones are not supported for DNS records: %q; expected a DnsZone path under /orgs/{org}/projects/{project}/dns-services/{dns-service}/zones/{zone}", zonePath)
	}
	return "", "", "", "", fmt.Errorf("invalid DNS zone path %q: expected /orgs/{org}/projects/{project}/dns-services/{dns-service}/zones/{zone}", zonePath)
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
	if strings.Contains(name, "_") {
		return "", "", fmt.Errorf("hostname %q must not contain underscores", hostname)
	}

	zonePath, matchedDomain, normalizedFQDN := z.FindZone(name)
	if matchedDomain == "" {
		return "", "", fmt.Errorf("hostname %q does not match any allowed DNS domain in the namespace", hostname)
	}
	if normalizedFQDN == matchedDomain {
		return "@", zonePath, nil
	}
	suffix := "." + matchedDomain
	if !strings.HasSuffix(normalizedFQDN, suffix) || normalizedFQDN == suffix {
		return "", "", fmt.Errorf("hostname %q does not lie under matched zone %q", hostname, matchedDomain)
	}
	recordName := strings.TrimSuffix(normalizedFQDN, suffix)
	return recordName, zonePath, nil
}

// ValidateEndpointsByZone maps endpoints to permitted zones and returns the valid rows and path→domain map. Endpoint validation errors are returned alongside any valid rows, so callers can reconcile the valid subset. owner must be non-nil.
func (s *DNSRecordService) ValidateEndpointsByZone(namespace string, owner *ResourceRef, eps []*extdns.Endpoint) ([]EndpointRow, map[string]string, error) {
	if s.disabled {
		return nil, nil, nil
	}
	log.Info("Validating DNS endpoints by zone", "namespace", namespace,
		"owner", owner.GetName(), "endpoints", len(eps))
	vpcConfig, err := s.VPCService.GetVPCNetworkConfigByNamespace(namespace)
	if err != nil {
		if strings.Contains(err.Error(), "failed to locate default NetworkConfig") {
			return nil, nil, &DNSZoneValidationError{Msg: fmt.Sprintf("default VPCNetworkConfiguration for the Namespace %s does not exist", namespace), Cause: err}
		}
		return nil, nil, fmt.Errorf("failed to find VPCNetworkConfiguration for the Namespace %s: %w", namespace, err)
	}
	if vpcConfig == nil {
		return nil, nil, &DNSZoneValidationError{Msg: fmt.Sprintf("VPCNetworkConfiguration for the Namespace %s does not exist", namespace)}
	}

	allowedZones, err := s.SyncDNSZonesByVpcNetworkConfig(vpcConfig)
	if err != nil {
		return nil, nil, err
	}
	if len(allowedZones) == 0 {
		return nil, nil, &DNSZoneValidationError{Msg: fmt.Sprintf("no DNS zones are permitted for the namespace %s", namespace)}
	}
	z := generateZoneIdFromMap(allowedZones)

	var validRows []EndpointRow
	var validationErr error

	for i := range eps {
		ep := eps[i]
		if endpointDNSNameIsWildcard(ep.DNSName) {
			log.Info("Skipping DNS endpoint: wildcard DNS names are not supported for NSX DNS records", "dnsName", ep.DNSName)
			continue
		}
		recName, zonePath, parseErr := s.getZonePathForHostname(z, ep.DNSName)
		if parseErr != nil {
			if validationErr == nil {
				validationErr = &DNSZoneValidationError{Msg: parseErr.Error()}
			}
			continue
		}
		log.Debug("Mapped DNS endpoint to zone", "dnsName", ep.DNSName, "zonePath", zonePath, "recordName", recName)
		row := NewEndpointRow(ep, zonePath, recName)
		row.validatedZoneDomain = allowedZones[zonePath]
		validRows = append(validRows, *row)
	}
	return validRows, allowedZones, validationErr
}

// SyncDNSZonesByVpcNetworkConfig ensures DNSZoneMap entries for vpcConfig.Spec.DNSZones; returns path→domain map or err.
func (s *DNSRecordService) SyncDNSZonesByVpcNetworkConfig(vpcConfig *v1alpha1.VPCNetworkConfiguration) (map[string]string, error) {
	dnsZonePaths := vpcConfig.Spec.DNSZones
	if len(dnsZonePaths) == 0 || s.disabled {
		return nil, nil
	}
	log.Info("Syncing DNS zones for VPCNetworkConfig", "zones", len(dnsZonePaths))

	dnsZoneDomainMapping := make(map[string]string)
	domainZonePaths := make(map[string]string)
	normalizedZones := make(extprovider.ZoneIDName)
	for _, p := range dnsZonePaths {
		// Validate cached paths too, including entries produced by older versions.
		if _, _, _, _, err := parseDnsZonePath(p); err != nil {
			return nil, &DNSZoneValidationError{Msg: "invalid configured DNS zone path", Cause: err}
		}
		domain, err := s.getOrFetchDNSZone(p)
		if err != nil {
			var missing *DNSZoneNotFoundError
			if errors.As(err, &missing) {
				s.notifyDNSZoneChanges([]string{p})
				continue
			}
			return nil, err
		}
		// Match the same IDNA normalization used when selecting a zone for a record.
		normalizedZones.Add(p, domain)
		canonicalDomain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(normalizedZones[p]), "."))
		if existingPath, exists := domainZonePaths[canonicalDomain]; exists && existingPath != p {
			return nil, &DNSZoneValidationError{Msg: fmt.Sprintf("DNS domain %q is configured in multiple zones: %s and %s", canonicalDomain, existingPath, p)}
		}
		domainZonePaths[canonicalDomain] = p
		dnsZoneDomainMapping[p] = domain
	}
	return dnsZoneDomainMapping, nil
}

// RefreshDNSZone refreshes an empty zone, or bypasses the caches when requested.
// On a non-NotFound refresh failure, a non-forced lookup returns the cached
// domain alongside the error so callers can retain it while reporting uncertainty.
func (s *DNSRecordService) RefreshDNSZone(path string, bypassStore bool) (string, error) {
	key := dnsZoneLockKey(path)
	recordArbitrator.lock(key)
	defer recordArbitrator.unlock(key)
	cached, found := s.DNSZoneMap.get(path)
	if !bypassStore && found && s.DNSRecordStore != nil && s.DNSRecordStore.HasActiveRecordsForZone(path) {
		return cached, nil
	}
	domain, err := s.refreshDNSZoneFromNSXLocked(path)
	var missing *DNSZoneNotFoundError
	if err != nil && !bypassStore && found && cached != "" && !errors.As(err, &missing) {
		return cached, err
	}
	return domain, err
}

// The zone lock also covers record writes through their store updates. Keep the
// lookup and NSX response publication together so an older GET cannot win later.
func (s *DNSRecordService) getOrFetchDNSZone(path string) (string, error) {
	key := dnsZoneLockKey(path)
	recordArbitrator.lock(key)
	defer recordArbitrator.unlock(key)
	if domain, found := s.DNSZoneMap.get(path); found {
		return domain, nil
	}
	return s.refreshDNSZoneFromNSXLocked(path)
}

func (s *DNSRecordService) refreshDNSZoneFromNSXLocked(path string) (string, error) {
	zone, err := s.getDNSZoneFromNSX(path)
	if err != nil {
		var missing *DNSZoneNotFoundError
		if errors.As(err, &missing) {
			s.DNSZoneMap.delete(path)
		}
		return "", err
	}
	if zone.DnsDomainName == nil || strings.TrimSpace(*zone.DnsDomainName) == "" {
		return "", &DNSZoneValidationError{Msg: fmt.Sprintf("DNS zone %s returned from NSX with no domain name", path)}
	}
	domain := strings.TrimSpace(*zone.DnsDomainName)
	s.DNSZoneMap.set(path, domain)
	return domain, nil
}

func (s *DNSRecordService) getDNSZoneFromNSX(zonePath string) (*model.DnsZone, error) {
	orgID, projectID, dnsServiceID, zoneID, err := parseDnsZonePath(zonePath)
	if err != nil {
		return nil, err
	}
	z, err := s.NSXClient.DnsZoneClient.Get(orgID, projectID, dnsServiceID, zoneID)
	if err != nil {
		translated := nsxutil.TransNSXApiError(err)
		if nsxErr, ok := translated.(*nsxutil.NSXApiError); ok && nsxErr.Type() == apierrors.ErrorType_NOT_FOUND {
			indexNotReady := nsxErr.ApiError != nil && nsxErr.ErrorCode != nil && *nsxErr.ErrorCode == 500090
			if !indexNotReady {
				return nil, &DNSZoneNotFoundError{Path: zonePath, Err: translated}
			}
		}
		return nil, translated
	}
	out := z
	return &out, nil
}

func generateZoneIdFromMap(allowedZones map[string]string) extprovider.ZoneIDName {
	z := make(extprovider.ZoneIDName)
	for zonePath, domainName := range allowedZones {
		z.Add(zonePath, strings.TrimSuffix(strings.TrimSpace(domainName), "."))
	}
	return z
}
