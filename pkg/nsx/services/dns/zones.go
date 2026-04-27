/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"fmt"
	"strings"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
	extprovider "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/provider"
)

// endpointDNSNameIsWildcard reports whether dnsName requests a wildcard apex record (e.g. "*.example.com").
// NSX DNS policy does not publish such names; ValidateEndpointsByDNSZone skips them without error.
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

	zonePath, matchedDomain, normalized := z.FindZone(name)
	if matchedDomain == "" {
		return "", "", fmt.Errorf("hostname %q does not match any allowed DNS domain in the namespace", hostname)
	}
	if normalized == matchedDomain {
		return "", "", fmt.Errorf("hostname %q must not equal allowed DNS domain %q (use a host name under the domain, not the domain itself)", hostname, matchedDomain)
	}
	suffix := "." + matchedDomain
	if !strings.HasSuffix(normalized, suffix) {
		return "", "", fmt.Errorf("hostname %q does not lie under matched zone %q", hostname, matchedDomain)
	}
	record := strings.TrimSuffix(normalized, suffix)
	if record == "" {
		return "", "", fmt.Errorf("hostname %q must not equal allowed DNS domain %q (use a host name under the domain, not the domain itself)", hostname, matchedDomain)
	}
	return record, zonePath, nil
}

// ValidateEndpointsByDNSZone maps each endpoint to a zone and row; returns rows or err.
func (s *DNSRecordService) ValidateEndpointsByDNSZone(namespace string, owner *ResourceRef, eps []*extdns.Endpoint) ([]EndpointRow, error) {
	if s.VPCService == nil {
		return nil, fmt.Errorf("VPCService is not configured")
	}
	vpcConfig, err := s.VPCService.GetVPCNetworkConfigByNamespace(namespace)
	if err != nil || len(vpcConfig.Status.VPCs) == 0 {
		return nil, fmt.Errorf("faild to find VPC Networkconfigurations for the Namespace %s", namespace)
	}
	vpcPath := vpcConfig.Status.VPCs[0].VPCPath
	vpcInfo, err := common.ParseVPCResourcePath(vpcPath)
	if err != nil {
		return nil, err
	}
	projectPath := fmt.Sprintf(common.VPCKey, vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID)

	allowedZones, err := s.SyncDNSZonesByVpcNetworkConfig(vpcConfig)
	if err != nil {
		return nil, err
	} else if len(allowedZones) == 0 {
		return nil, fmt.Errorf("no DNS zones are permitted for the namespace %s", namespace)
	}

	z := make(extprovider.ZoneIDName)
	for i := range allowedZones {
		zonePath := allowedZones[i]
		domainName := s.DNSZoneMap[zonePath]
		z.Add(zonePath, domainName)
	}

	var rows []EndpointRow
	for i := range eps {
		ep := eps[i]
		if endpointDNSNameIsWildcard(ep.DNSName) {
			log.Info("Skipping DNS endpoint: wildcard DNS names are not supported for NSX DNS records", "dnsName", ep.DNSName)
			continue
		}
		recName, zonePath, parseErr := s.getZonePathForHostname(z, ep.DNSName)
		if parseErr != nil {
			return nil, parseErr
		}
		row, validErr := s.validateEndpointRowConflict(zonePath, ep, projectPath, recName, owner)
		if validErr != nil {
			return nil, validErr
		}
		if row == nil {
			continue // caller skips this endpoint
		}
		rows = append(rows, *row)
	}
	return rows, nil
}

// SyncDNSZonesByVpcNetworkConfig ensures DNSZoneMap entries for vpcConfig.Spec.DNSZones; returns zone paths or err.
func (s *DNSRecordService) SyncDNSZonesByVpcNetworkConfig(vpcConfig *v1alpha1.VPCNetworkConfiguration) ([]string, error) {
	ctx := context.Background()
	dnsZonePaths := vpcConfig.Spec.DNSZones
	if len(dnsZonePaths) == 0 {
		return nil, nil // no zones in spec
	}

	for _, p := range dnsZonePaths {
		_, found := s.DNSZoneMap[p]
		if !found {
			zone, err := s.getDNSZoneFromNSX(ctx, p)
			if err != nil {
				log.Error(err, "failed to retrieve DNS zone with path %s", p)
				return dnsZonePaths, err
			}
			s.DNSZoneMap[p] = *zone.DomainName
		}
	}
	return dnsZonePaths, nil
}
