/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package inspection implements read-only source network inspection shared by
// the control-plane and namespace Evaluate handlers.
package inspection

import (
	"fmt"
	"net"
	"net/netip"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

// Addressing is validated source configuration, not an inventory of free IPs.
// Ranges may be DHCP or static pools; existing addresses need only lie in CIDR.
type Addressing struct {
	CIDR    netip.Prefix
	Gateway netip.Addr
	Ranges  []api.IPAddressRange
}

func ipv4(value string) (netip.Addr, error) {
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is4() || address.IsUnspecified() || address.IsMulticast() {
		return netip.Addr{}, fmt.Errorf("%q must be a unicast IPv4 address", value)
	}
	return address, nil
}

// ValidateAddressing checks supplied/discovered values without filling missing
// fields. CIDR is derived from the first range start and mask. The current
// source subnetMask contract is IPv4-only; there is no administrator CIDR input.
func ValidateAddressing(mask, gateway string, ranges []api.IPAddressRange) (Addressing, error) {
	var result Addressing
	maskAddress, err := netip.ParseAddr(mask)
	if err != nil || !maskAddress.Is4() {
		return result, fmt.Errorf("subnetMask must be a contiguous IPv4 mask")
	}
	maskBytes := maskAddress.As4()
	prefixLength, addressBitLength := net.IPMask(maskBytes[:]).Size()
	if addressBitLength != 32 {
		return result, fmt.Errorf("subnetMask must be contiguous")
	}
	gatewayAddress, err := ipv4(gateway)
	if err != nil {
		return result, fmt.Errorf("gatewayAddress: %w", err)
	}
	if len(ranges) == 0 {
		return result, fmt.Errorf("at least one source address range is required")
	}
	firstRangeStart, err := ipv4(ranges[0].Start)
	if err != nil {
		return result, fmt.Errorf("range start: %w", err)
	}
	sourceCIDR := netip.PrefixFrom(firstRangeStart, prefixLength).Masked()
	isUsableAddress := func(address netip.Addr) bool {
		if !sourceCIDR.Contains(address) {
			return false
		}
		// /31 and /32 have no network/broadcast exclusion (RFC 3021).
		if prefixLength >= 31 {
			return true
		}
		addressBytes := address.As4()
		networkBytes := sourceCIDR.Addr().As4()
		isBroadcastAddress := true
		for byteIndex := range addressBytes {
			isBroadcastAddress = isBroadcastAddress && addressBytes[byteIndex] == networkBytes[byteIndex]|^maskBytes[byteIndex]
		}
		return address != sourceCIDR.Addr() && !isBroadcastAddress
	}
	if !isUsableAddress(gatewayAddress) {
		return result, fmt.Errorf("gatewayAddress is not a usable address in %s", sourceCIDR)
	}
	for _, addressRange := range ranges {
		rangeStart, rangeStartErr := ipv4(addressRange.Start)
		rangeEnd, rangeEndErr := ipv4(addressRange.End)
		if rangeStartErr != nil || rangeEndErr != nil || rangeStart.Compare(rangeEnd) > 0 || !isUsableAddress(rangeStart) || !isUsableAddress(rangeEnd) {
			return result, fmt.Errorf("range %s-%s must be ordered usable IPv4 addresses within %s", addressRange.Start, addressRange.End, sourceCIDR)
		}
		if rangeStart.Compare(gatewayAddress) <= 0 && gatewayAddress.Compare(rangeEnd) <= 0 {
			return result, fmt.Errorf("gatewayAddress overlaps source allocation range")
		}
	}
	return Addressing{CIDR: sourceCIDR, Gateway: gatewayAddress, Ranges: append([]api.IPAddressRange(nil), ranges...)}, nil
}

// ValidateExistingAddresses deliberately does not require membership in a pool.
func (addressing Addressing) ValidateExistingAddresses(addresses []string) error {
	for _, address := range addresses {
		parsedAddress, err := ipv4(address)
		if err != nil || !addressing.CIDR.Contains(parsedAddress) {
			return fmt.Errorf("existing address %q is outside %s", address, addressing.CIDR)
		}
	}
	return nil
}
