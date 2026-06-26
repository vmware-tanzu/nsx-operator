/* Copyright © 2024-2025 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:validation:XValidation:rule="has(self.targetSubnetSetName) && !has(self.targetSubnetName) || !has(self.targetSubnetSetName) && has(self.targetSubnetName)",message="Only one of targetSubnetSetName or targetSubnetName can be specified"
// +kubebuilder:validation:XValidation:rule="!has(self.targetSubnetName) || (self.subnetName != self.targetSubnetName)",message="subnetName and targetSubnetName must be different"
type SubnetConnectionBindingMapSpec struct {
	// SubnetName is the Subnet name which this SubnetConnectionBindingMap is associated.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="subnetName is immutable"
	SubnetName string `json:"subnetName"`
	// TargetSubnetSetName specifies the target SubnetSet which a Subnet is connected to.
	// +kubebuilder:validation:Optional
	TargetSubnetSetName string `json:"targetSubnetSetName,omitempty"`
	// TargetSubnetName specifies the target Subnet which a Subnet is connected to.
	// +kubebuilder:validation:Optional
	TargetSubnetName string `json:"targetSubnetName,omitempty"`
	// VLANTrafficTag is the VLAN tag configured in the binding. Note, the value of VLANTrafficTag should be
	// unique on the target Subnet or SubnetSet. When omitted, the operator auto-allocates a VLAN ID.
	// +kubebuilder:validation:Maximum:=4094
	// +kubebuilder:validation:Minimum:=1
	// +kubebuilder:validation:Optional
	VLANTrafficTag *int64 `json:"vlanTrafficTag,omitempty"`
}

// SubnetConnectionBindingMapStatus defines the observed state of SubnetConnectionBindingMap.
type SubnetConnectionBindingMapStatus struct {
	// Conditions described if the SubnetConnectionBindingMaps is configured on NSX or not.
	// Condition type ""
	Conditions []Condition `json:"conditions,omitempty"`
	// VLANID is the VLAN ID realized in NSX for this binding.
	VLANID *int64 `json:"vlanID,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:scope="Namespaced",path=subnetconnectionbindingmaps,shortName=subnetbinding;subnetbindings
// +kubebuilder:selectablefield:JSONPath=`.spec.subnetName`

// SubnetConnectionBindingMap is the Schema for the SubnetConnectionBindingMap API.
// +kubebuilder:printcolumn:name="subnet",type=string,JSONPath=`.spec.subnetName`,description="The Subnet which the SubnetConnectionBindingMap is associated"
// +kubebuilder:printcolumn:name="targetSubnet",type=string,JSONPath=`.spec.targetSubnetName`,description="The target Subnet which the SubnetConnectionBindingMap is connected to"
// +kubebuilder:printcolumn:name="targetSubnetSet",type=string,JSONPath=`.spec.targetSubnetSetName`,description="The target SubnetSet which the SubnetConnectionBindingMap is connected to"
// +kubebuilder:printcolumn:name="vlanID",type=integer,JSONPath=`.status.vlanID`,description="Vlan used in the NSX SubnetConnectionBindingMap"
type SubnetConnectionBindingMap struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SubnetConnectionBindingMapSpec   `json:"spec,omitempty"`
	Status            SubnetConnectionBindingMapStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SubnetConnectionBindingMapList contains a list of SubnetConnectionBindingMap.
type SubnetConnectionBindingMapList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SubnetConnectionBindingMap `json:"items,omitempty"`
}

// VLANTrafficTagPtr returns a pointer to the given VLAN traffic tag value.
func VLANTrafficTagPtr(v int64) *int64 {
	return &v
}

// HasVlanTrafficTag reports whether spec.vlanTrafficTag is set.
func (s *SubnetConnectionBindingMapSpec) HasVlanTrafficTag() bool {
	return s.VLANTrafficTag != nil
}

func init() {
	SchemeBuilder.Register(&SubnetConnectionBindingMap{}, &SubnetConnectionBindingMapList{})
}
