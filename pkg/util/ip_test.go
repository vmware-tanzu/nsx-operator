/* Copyright © 2025 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"net"
	"reflect"
	"testing"
)

func Test_rangesAbstractRange(t *testing.T) {
	empty := [][]net.IP{}
	type args struct {
		ranges [][]net.IP
		except []net.IP
	}
	tests := []struct {
		name string
		args args
		want [][]net.IP
	}{
		{
			name: "except range is completely included in the ranges",
			args: args{
				ranges: [][]net.IP{
					{
						net.ParseIP("172.0.0.1"),
						net.ParseIP("172.0.255.255"),
					},
					{
						net.ParseIP("172.2.0.1"),
						net.ParseIP("172.2.255.255"),
					},
				},
				except: []net.IP{
					net.ParseIP("172.0.100.1"),
					net.ParseIP("172.0.100.255"),
				},
			},
			want: [][]net.IP{
				{
					net.ParseIP("172.0.0.1").To4(),
					net.ParseIP("172.0.100.0").To4(),
				},
				{
					net.ParseIP("172.0.101.0").To4(),
					net.ParseIP("172.0.255.255").To4(),
				},
				{
					net.ParseIP("172.2.0.1").To4(),
					net.ParseIP("172.2.255.255").To4(),
				},
			},
		},
		{
			name: "except spans multiple ranges",
			args: args{
				ranges: [][]net.IP{
					{
						net.ParseIP("172.0.0.1"),
						net.ParseIP("172.0.255.255"),
					},
					{
						net.ParseIP("172.2.0.1"),
						net.ParseIP("172.2.255.255"),
					},
				},
				except: []net.IP{
					net.ParseIP("172.0.100.1"),
					net.ParseIP("172.2.100.255"),
				},
			},
			want: [][]net.IP{
				{
					net.ParseIP("172.0.0.1").To4(),
					net.ParseIP("172.0.100.0").To4(),
				},
				{
					net.ParseIP("172.2.101.0").To4(),
					net.ParseIP("172.2.255.255").To4(),
				},
			},
		},
		{
			name: "except is completely in the outside of the range",
			args: args{
				ranges: [][]net.IP{
					{
						net.ParseIP("172.0.0.1"),
						net.ParseIP("172.0.255.255"),
					},
				},
				except: []net.IP{
					net.ParseIP("171.0.100.1"),
					net.ParseIP("171.1.100.255"),
				},
			},
			want: [][]net.IP{
				{
					net.ParseIP("172.0.0.1").To4(),
					net.ParseIP("172.0.255.255").To4(),
				},
			},
		},
		{
			name: "except is partially in the range",
			args: args{
				ranges: [][]net.IP{
					{
						net.ParseIP("172.0.0.1"),
						net.ParseIP("172.0.255.255"),
					},
				},
				except: []net.IP{
					net.ParseIP("171.0.100.1"),
					net.ParseIP("172.0.100.255"),
				},
			},
			want: [][]net.IP{
				{
					net.ParseIP("172.0.101.0").To4(),
					net.ParseIP("172.0.255.255").To4(),
				},
			},
		},
		{
			name: "range is completely in the except",
			args: args{
				ranges: [][]net.IP{
					{
						net.ParseIP("172.0.0.1"),
						net.ParseIP("172.0.255.255"),
					},
				},
				except: []net.IP{
					net.ParseIP("171.0.100.1"),
					net.ParseIP("173.0.100.255"),
				},
			},
			want: empty,
		},
		{
			name: "except is exactly the same as the range",
			args: args{
				ranges: [][]net.IP{
					{
						net.ParseIP("10.0.0.1"),
						net.ParseIP("10.0.0.10"),
					},
				},
				except: []net.IP{
					net.ParseIP("10.0.0.1"),
					net.ParseIP("10.0.0.10"),
				},
			},
			want: [][]net.IP{},
		},
		{
			name: "except is a single IP inside the range",
			args: args{
				ranges: [][]net.IP{
					{
						net.ParseIP("192.168.1.1"),
						net.ParseIP("192.168.1.10"),
					},
				},
				except: []net.IP{
					net.ParseIP("192.168.1.5"),
					net.ParseIP("192.168.1.5"),
				},
			},
			want: [][]net.IP{
				{
					net.ParseIP("192.168.1.1").To4(),
					net.ParseIP("192.168.1.4").To4(),
				},
				{
					net.ParseIP("192.168.1.6").To4(),
					net.ParseIP("192.168.1.10").To4(),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rangesAbstractRange(tt.args.ranges, tt.args.except)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s failed: rangesAbstractRange got %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestGetCIDRRangesWithExcept(t *testing.T) {
	type args struct {
		cidr    string
		excepts []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "1",
			args: args{
				cidr:    "172.17.0.0/16",
				excepts: []string{"172.17.1.0/24"}},
			want: []string{"172.17.0.0-172.17.0.255", "172.17.2.0-172.17.255.255"},
		},
		{
			name: "2",
			args: args{
				cidr:    "172.0.0.0/16",
				excepts: []string{"172.0.100.0/24", "172.0.102.0/24"}},
			want: []string{"172.0.0.0-172.0.99.255", "172.0.101.0-172.0.101.255", "172.0.103.0-172.0.255.255"},
		},
		{
			name: "3",
			args: args{
				cidr:    "40.0.0.0/8",
				excepts: []string{"40.100.100.11/32", "40.100.100.15/32", "40.100.100.31/32", "40.100.100.32/32"},
			},
			want: []string{"40.0.0.0-40.100.100.10", "40.100.100.12-40.100.100.14", "40.100.100.16-40.100.100.30", "40.100.100.33-40.255.255.255"}},
	}
	for _, tt := range tests {
		got, err := GetCIDRRangesWithExcept(tt.args.cidr, tt.args.excepts)
		if err != nil {
			t.Errorf("%s failed: %s", tt.name, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s failed: GetCIDRRangesWithExcept got %s, want %s", tt.name, got, tt.want)
		}
	}
}

func Test_calculateOffsetIP(t *testing.T) {
	ip := net.ParseIP("192.168.0.1")
	offset1 := 1
	want1 := net.ParseIP("192.168.0.2").To4()
	type args struct {
		ip     net.IP
		offset int
	}
	tests := []struct {
		name string
		args args
		want net.IP
	}{
		{"IPv4 offset +1", args{ip, offset1}, want1},
		{"IPv6 offset +1", args{net.ParseIP("2001:db8::1"), 1}, net.ParseIP("2001:db8::2")},
		{"IPv6 offset -1", args{net.ParseIP("2001:db8::ff"), -1}, net.ParseIP("2001:db8::fe")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateOffsetIP(tt.args.ip, tt.args.offset)
			want := normalizeIP(tt.want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s failed: calculateOffsetIP got %v, want %v", tt.name, got, want)
			}
		})
	}
}

func Test_rangesAbstractRange_IPv6(t *testing.T) {
	empty := [][]net.IP{}
	type args struct {
		ranges [][]net.IP
		except []net.IP
	}
	tests := []struct {
		name string
		args args
		want [][]net.IP
	}{
		{
			name: "IPv6: except range is completely included in the range",
			args: args{
				ranges: [][]net.IP{
					{
						net.ParseIP("2001:db8::1"),
						net.ParseIP("2001:db8::ffff"),
					},
				},
				except: []net.IP{
					net.ParseIP("2001:db8::100"),
					net.ParseIP("2001:db8::1ff"),
				},
			},
			want: [][]net.IP{
				{
					net.ParseIP("2001:db8::1"),
					net.ParseIP("2001:db8::ff"),
				},
				{
					net.ParseIP("2001:db8::200"),
					net.ParseIP("2001:db8::ffff"),
				},
			},
		},
		{
			name: "IPv6: except is exactly the same as the range",
			args: args{
				ranges: [][]net.IP{
					{
						net.ParseIP("2001:db8::1"),
						net.ParseIP("2001:db8::10"),
					},
				},
				except: []net.IP{
					net.ParseIP("2001:db8::1"),
					net.ParseIP("2001:db8::10"),
				},
			},
			want: empty,
		},
		{
			name: "IPv6: except is completely outside the range",
			args: args{
				ranges: [][]net.IP{
					{
						net.ParseIP("2001:db8:1::1"),
						net.ParseIP("2001:db8:1::ffff"),
					},
				},
				except: []net.IP{
					net.ParseIP("2001:db8:2::1"),
					net.ParseIP("2001:db8:2::ff"),
				},
			},
			want: [][]net.IP{
				{
					net.ParseIP("2001:db8:1::1"),
					net.ParseIP("2001:db8:1::ffff"),
				},
			},
		},
		{
			name: "IPv6: except is a single IP inside the range",
			args: args{
				ranges: [][]net.IP{
					{
						net.ParseIP("fd00::1"),
						net.ParseIP("fd00::a"),
					},
				},
				except: []net.IP{
					net.ParseIP("fd00::5"),
					net.ParseIP("fd00::5"),
				},
			},
			want: [][]net.IP{
				{
					net.ParseIP("fd00::1"),
					net.ParseIP("fd00::4"),
				},
				{
					net.ParseIP("fd00::6"),
					net.ParseIP("fd00::a"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rangesAbstractRange(tt.args.ranges, tt.args.except)
			normalizeRanges := func(ranges [][]net.IP) [][]net.IP {
				for i := range ranges {
					for j := range ranges[i] {
						ranges[i][j] = normalizeIP(ranges[i][j])
					}
				}
				return ranges
			}
			if !reflect.DeepEqual(got, normalizeRanges(tt.want)) {
				t.Errorf("%s failed: rangesAbstractRange got %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestGetCIDRRangesWithExcept_IPv6(t *testing.T) {
	type args struct {
		cidr    string
		excepts []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "IPv6 single except",
			args: args{
				cidr:    "2001:db8::/32",
				excepts: []string{"2001:db8:1::/48"},
			},
			want: []string{"2001:db8::-2001:db8:0:ffff:ffff:ffff:ffff:ffff", "2001:db8:2::-2001:db8:ffff:ffff:ffff:ffff:ffff:ffff"},
		},
		{
			name: "IPv6 multiple excepts",
			args: args{
				cidr:    "fd00::/112",
				excepts: []string{"fd00::a/128", "fd00::14/128"},
			},
			want: []string{"fd00::-fd00::9", "fd00::b-fd00::13", "fd00::15-fd00::ffff"},
		},
	}
	for _, tt := range tests {
		got, err := GetCIDRRangesWithExcept(tt.args.cidr, tt.args.excepts)
		if err != nil {
			t.Errorf("%s failed: %s", tt.name, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s failed: GetCIDRRangesWithExcept got %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestIsIPv6CIDR(t *testing.T) {
	tests := []struct {
		cidr string
		want bool
	}{
		{"192.168.0.0/24", false},
		{"10.0.0.0/8", false},
		{"2001:db8::/32", true},
		{"fd00::/64", true},
		{"::1/128", true},
		{"invalid", false},
	}
	for _, tt := range tests {
		t.Run(tt.cidr, func(t *testing.T) {
			if got := IsIPv6CIDR(tt.cidr); got != tt.want {
				t.Errorf("IsIPv6CIDR(%q) = %v, want %v", tt.cidr, got, tt.want)
			}
		})
	}
}

func TestIsIPv6(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"192.168.0.1", false},
		{"10.0.0.1", false},
		{"2001:db8::1", true},
		{"::1", true},
		{"fe80::1", true},
		{"invalid", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := IsIPv6(tt.addr); got != tt.want {
				t.Errorf("IsIPv6(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestCalculateIPFromCIDRs_IPv6(t *testing.T) {
	tests := []struct {
		name    string
		cidrs   []string
		want    int
		wantErr bool
	}{
		{
			name:  "IPv6 /128",
			cidrs: []string{"2001:db8::1/128"},
			want:  1,
		},
		{
			name:  "IPv6 /126",
			cidrs: []string{"2001:db8::/126"},
			want:  4,
		},
		{
			name:  "IPv4 /24",
			cidrs: []string{"192.168.1.0/24"},
			want:  256,
		},
		{
			name:  "mixed IPv4 and IPv6",
			cidrs: []string{"192.168.1.0/24", "2001:db8::/126"},
			want:  260,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CalculateIPFromCIDRs(tt.cidrs)
			if (err != nil) != tt.wantErr {
				t.Errorf("CalculateIPFromCIDRs() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("CalculateIPFromCIDRs() = %v, want %v", got, tt.want)
			}
		})
	}
}
