/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +genclient
// +genclient:onlyVerbs=get
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:scope=Namespaced

// SubnetIPPoolAllocations lists all IP allocations from a specific static IP pool.
type SubnetIPPoolAllocations struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// IPAddressAllocations contains the list of IP addresses currently allocated from the pool.
	// +optional
	// +listType=atomic
	IPAddressAllocations []IPAddressAllocationEntry `json:"ipAddressAllocations,omitempty"`
}

// IPAddressAllocationEntry represents a single IP address allocation from a static IP pool.
type IPAddressAllocationEntry struct {
	// Name is the identifier of the allocation.
	Name string `json:"name"`
	// AllocationIP is the user-specified IP that was requested from the pool at creation time.
	// Empty if no specific IP was requested, which is the common case for auto-allocated static IPs.
	// +optional
	AllocationIP string `json:"allocationIP,omitempty"`
	// AllocatedIP is the actual IP address that has been realized and committed for this allocation.
	// +optional
	AllocatedIP string `json:"allocatedIP,omitempty"`
}

func init() {
	SchemeBuilder.Register(&SubnetIPPoolAllocations{})
}
