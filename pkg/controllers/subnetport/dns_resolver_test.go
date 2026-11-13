/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"testing"

	"github.com/stretchr/testify/require"
	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSubnetPortSupportsDNS(t *testing.T) {
	tests := []struct {
		name             string
		owners           []metav1.OwnerReference
		subnetPortLabels map[string]string
		wantSet          bool
	}{
		{
			name: "regular VM owner",
			owners: []metav1.OwnerReference{{
				APIVersion: vmv1alpha1.GroupVersion.String(),
				Kind:       "VirtualMachine",
			}},
			wantSet: true,
		},
		{
			name:             "image fetcher SubnetPort label",
			subnetPortLabels: map[string]string{servicecommon.LabelImageFetcher: "true"},
			owners: []metav1.OwnerReference{{
				APIVersion: vmv1alpha1.GroupVersion.String(),
				Kind:       "VirtualMachine",
			}},
		},
		{
			name: "Pod owner",
			owners: []metav1.OwnerReference{{
				APIVersion: "v1",
				Kind:       "Pod",
			}},
		},
		{
			name: "same kind from another API group",
			owners: []metav1.OwnerReference{{
				APIVersion: "other.example.com/v1",
				Kind:       "VirtualMachine",
			}},
		},
		{
			name: "VM owner with malformed API version",
			owners: []metav1.OwnerReference{{
				APIVersion: "vmoperator.vmware.com/v1alpha1/extra",
				Kind:       "VirtualMachine",
			}},
		},
		{name: "no owner"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: tt.owners,
				Labels:          tt.subnetPortLabels,
			}}
			require.Equal(t, tt.wantSet, subnetPortSupportsDNS(sp))
		})
	}

	require.False(t, subnetPortSupportsDNS(nil))
}

func TestFilterDNSResolverIPsByIPType(t *testing.T) {
	resolverIPs := []string{"192.0.2.53", "2001:db8::53"}
	tests := []struct {
		name    string
		ipType  v1alpha1.IPAddressType
		wantIPs []string
	}{
		{name: "IPv4 port", ipType: v1alpha1.IPAddressTypeIPv4, wantIPs: []string{"192.0.2.53"}},
		{name: "IPv6 port", ipType: v1alpha1.IPAddressTypeIPv6, wantIPs: []string{"2001:db8::53"}},
		{name: "dual-stack port", ipType: v1alpha1.IPAddressTypeIPv4IPv6, wantIPs: resolverIPs},
		{name: "unsupported family", ipType: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantIPs, filterDNSResolverIPsByIPType(resolverIPs, tt.ipType))
		})
	}
	require.Nil(t, filterDNSResolverIPsByIPType(nil, v1alpha1.IPAddressTypeIPv4))
	require.Equal(t, []string{"192.0.2.53"}, filterDNSResolverIPsByIPType([]string{"invalid", "192.0.2.53"}, v1alpha1.IPAddressTypeIPv4))
}

func TestFilterDNSResolverIPsByIPTypeSkipsIPv4MappedIPv6(t *testing.T) {
	resolverIPs := []string{"192.0.2.53", "2001:db8::53", "::ffff:192.0.2.54"}
	require.Equal(t, []string{"192.0.2.53"}, filterDNSResolverIPsByIPType(resolverIPs, v1alpha1.IPAddressTypeIPv4))
	require.Equal(t, []string{"2001:db8::53"}, filterDNSResolverIPsByIPType(resolverIPs, v1alpha1.IPAddressTypeIPv6))
	require.Equal(t, []string{"192.0.2.53", "2001:db8::53"}, filterDNSResolverIPsByIPType(resolverIPs, v1alpha1.IPAddressTypeIPv4IPv6))
}
