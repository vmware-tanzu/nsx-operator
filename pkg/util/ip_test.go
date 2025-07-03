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
	}{{"1", args{ip, offset1}, want1}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := calculateOffsetIP(tt.args.ip, tt.args.offset)
			if err != nil {
				t.Errorf("%s failed: %s", tt.name, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s failed: calculateOffsetIP got %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
