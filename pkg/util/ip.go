/* Copyright © 2025 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"errors"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"

	"github.com/apparentlymart/go-cidr/cidr"
)

// RemoveIPPrefix remove the prefix from an IP address, e.g.
// "1.2.3.4/24" -> "1.2.3.4"
func RemoveIPPrefix(ipAddress string) (string, error) {
	ip := strings.Split(ipAddress, "/")[0]
	if net.ParseIP(ip) == nil {
		return "", errors.New("invalid IP address")
	}
	return ip, nil
}

// GetIPPrefix get the prefix from an IP address, e.g.
// "1.2.3.4/24" -> 24
func GetIPPrefix(ipAddress string) (int, error) {
	num, err := strconv.Atoi(strings.Split(ipAddress, "/")[1])
	if err != nil {
		return -1, err
	}
	return num, err
}

// GetSubnetMask returns the IPv4 dotted-decimal mask for a given prefix length, e.g.
// 24 -> "255.255.255.0". Only valid for IPv4 prefix lengths (0-32).
func GetSubnetMask(subnetLength int) (string, error) {
	if subnetLength < 0 || subnetLength > 32 {
		return "", errors.New("invalid subnet mask length")
	}
	subnetBinary := uint32(0xffffffff) << (32 - subnetLength)
	subnetMask := net.IPv4(byte(subnetBinary>>24), byte(subnetBinary>>16), byte(subnetBinary>>8), byte(subnetBinary))
	return subnetMask.String(), nil
}

// IsIPv6CIDR returns true if the given CIDR string is an IPv6 CIDR.
func IsIPv6CIDR(cidrStr string) bool {
	ip, _, err := net.ParseCIDR(cidrStr)
	if err != nil {
		return false
	}
	return ip.To4() == nil
}

// IsIPv6 returns true if the given IP address string is IPv6.
func IsIPv6(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	return ip.To4() == nil
}

func CalculateIPFromCIDRs(IPAddresses []string) (int, error) {
	total := 0
	for _, addr := range IPAddresses {
		_, ipNet, err := net.ParseCIDR(addr)
		if err != nil {
			return -1, err
		}
		total += int(cidr.AddressCount(ipNet))
	}
	return total, nil
}

func parseCIDRRange(cidr string) (startIP, endIP net.IP, err error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, nil, err
	}
	startIP = normalizeIP(ipnet.IP)
	endIP = make(net.IP, len(startIP))
	copy(endIP, startIP)
	for i := len(startIP) - 1; i >= 0; i-- {
		endIP[i] = startIP[i] | ^ipnet.Mask[i]
	}
	return startIP, endIP, nil
}

// normalizeIP returns the canonical form of an IP address:
// 4 bytes for IPv4, 16 bytes for IPv6.
func normalizeIP(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip.To16()
}

func ipToBigInt(ip net.IP) *big.Int {
	return new(big.Int).SetBytes(normalizeIP(ip))
}

func bigIntToIP(n *big.Int, ipLen int) net.IP {
	ip := make(net.IP, ipLen)
	if n.Sign() <= 0 {
		return ip
	}
	b := n.Bytes()
	if len(b) > ipLen {
		b = b[len(b)-ipLen:]
	}
	copy(ip[ipLen-len(b):], b)
	return ip
}

func calculateOffsetIP(ip net.IP, offset int) (net.IP, error) {
	ip = normalizeIP(ip)
	n := ipToBigInt(ip)
	n.Add(n, big.NewInt(int64(offset)))
	return bigIntToIP(n, len(ip)), nil
}

func compareIP(ip1, ip2 net.IP) bool {
	return ipToBigInt(ip1).Cmp(ipToBigInt(ip2)) < 0
}

func equalIP(ip1, ip2 net.IP) bool {
	return normalizeIP(ip1).Equal(normalizeIP(ip2))
}

func rangeAppend(ranges [][]net.IP, appendRange []net.IP) [][]net.IP {
	if !compareIP(appendRange[1], appendRange[0]) {
		ranges = append(ranges, appendRange)
	}
	return ranges
}

func rangesAbstractRange(ranges [][]net.IP, except []net.IP) [][]net.IP {
	// ranges: [[172.0.0.1 172.0.255.255] [172.2.0.1 172.2.255.255]]
	// except: [172.0.100.1 172.0.100.255]
	// return: [[172.0.0.1 172.0.100.0] [172.0.101.0 172.0.255.255] [172.2.0.1 172.2.255.255]]
	results := [][]net.IP{}
	except[0] = normalizeIP(except[0])
	except[1] = normalizeIP(except[1])
	const (
		// Location identifiers for the except range point in relation to the given range
		LocationBeforeStart = iota // 0: before rng[0]
		LocationAtStart            // 1: at rng[0]
		LocationBetween            // 2: between rng[0] and rng[1]
		LocationAtEnd              // 3: at rng[1]
		LocationAfterEnd           // 4: after rng[1]
	)
	for _, r := range ranges {
		rng := r
		// Define a function to determine the position of the except IPs in relation to the range,
		// so that we can use a function to identify the location of the except range point in relation to the given range
		// to cover all the cases.
		getIPPositionInRange := func(ip net.IP) int {
			var position int
			if compareIP(ip, rng[0]) {
				position = LocationBeforeStart
			} else if equalIP(ip, rng[0]) {
				position = LocationAtStart
			} else if compareIP(ip, rng[1]) {
				position = LocationBetween
			} else if equalIP(ip, rng[1]) {
				position = LocationAtEnd
			} else {
				position = LocationAfterEnd
			}
			return position
		}
		rng[0] = normalizeIP(rng[0])
		rng[1] = normalizeIP(rng[1])
		exceptPrev, _ := calculateOffsetIP(except[0], -1)
		exceptNext, _ := calculateOffsetIP(except[1], 1)
		if getIPPositionInRange(except[0]) == LocationBeforeStart {
			if getIPPositionInRange(except[1]) == LocationBeforeStart {
				results = rangeAppend(results, []net.IP{rng[0], rng[1]})
			} else if getIPPositionInRange(except[1]) == LocationAtStart || getIPPositionInRange(except[1]) == LocationBetween {
				results = rangeAppend(results, []net.IP{exceptNext, rng[1]})
			}
		} else if getIPPositionInRange(except[0]) == LocationAtStart {
			if getIPPositionInRange(except[1]) == LocationAtStart || getIPPositionInRange(except[1]) == LocationBetween {
				results = rangeAppend(results, []net.IP{exceptNext, rng[1]})
			}
		} else if getIPPositionInRange(except[0]) == LocationBetween {
			if getIPPositionInRange(except[1]) == LocationBetween {
				results = rangeAppend(results, []net.IP{rng[0], exceptPrev})
				results = rangeAppend(results, []net.IP{exceptNext, rng[1]})
			} else {
				results = rangeAppend(results, []net.IP{rng[0], exceptPrev})
			}
		} else if getIPPositionInRange(except[0]) == LocationAtEnd {
			results = rangeAppend(results, []net.IP{rng[0], exceptPrev})
		} else {
			results = rangeAppend(results, []net.IP{rng[0], rng[1]})
		}
	}
	return results
}

func GetCIDRRangesWithExcept(cidr string, excepts []string) ([]string, error) {
	var calculatedRanges [][]net.IP
	var resultRanges []string
	mainStartIP, mainEndIP, err := parseCIDRRange(cidr)
	calculatedRanges = append(calculatedRanges, []net.IP{mainStartIP, mainEndIP})
	if err != nil {
		return nil, err
	}
	for _, ept := range excepts {
		except := ept
		exceptStartIP, exceptEndIP, err := parseCIDRRange(except)
		if err != nil {
			return nil, err
		}
		newCalculatedRanges := rangesAbstractRange(calculatedRanges, []net.IP{exceptStartIP, exceptEndIP})
		calculatedRanges = newCalculatedRanges
		log.Trace("Abstracted ranges after removing excepts", "except", except, "ranges", calculatedRanges)
	}
	for _, rng := range calculatedRanges {
		resultRanges = append(resultRanges, fmt.Sprintf("%s-%s", rng[0], rng[1]))
	}
	return resultRanges, nil
}
