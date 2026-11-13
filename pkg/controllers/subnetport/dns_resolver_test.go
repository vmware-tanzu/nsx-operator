/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"testing"

	"github.com/stretchr/testify/require"
	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestShouldSetDNSResolverIPs(t *testing.T) {
	tests := []struct {
		name           string
		owners         []metav1.OwnerReference
		vmLabels       map[string]string
		subnetPortTags map[string]string
		vmAvailable    bool
		wantSet        bool
	}{
		{
			name:        "regular VM owner",
			vmAvailable: true,
			owners: []metav1.OwnerReference{{
				APIVersion: vmv1alpha1.GroupVersion.String(),
				Kind:       "VirtualMachine",
			}},
			wantSet: true,
		},
		{
			name:        "image fetcher VM label",
			vmAvailable: true,
			vmLabels:    map[string]string{"iaas.vmware.com/image-fetcher": "true"},
			owners: []metav1.OwnerReference{{
				APIVersion: vmv1alpha1.GroupVersion.String(),
				Kind:       "VirtualMachine",
			}},
		},
		{
			name:           "image fetcher SubnetPort label",
			vmAvailable:    true,
			subnetPortTags: map[string]string{"iaas.vmware.com/image-fetcher": "true"},
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
			name:        "same kind from another API group",
			vmAvailable: true,
			owners: []metav1.OwnerReference{{
				APIVersion: "other.example.com/v1",
				Kind:       "VirtualMachine",
			}},
		},
		{name: "owner VM not available"},
		{name: "no owner", vmAvailable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: tt.owners,
				Labels:          tt.subnetPortTags,
			}}
			var vm *vmv1alpha1.VirtualMachine
			if tt.vmAvailable {
				vm = &vmv1alpha1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Labels: tt.vmLabels}}
			}
			require.Equal(t, tt.wantSet, shouldSetDNSResolverIPs(sp, vm))
		})
	}

	require.False(t, shouldSetDNSResolverIPs(nil, &vmv1alpha1.VirtualMachine{}))
}
