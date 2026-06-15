/* Copyright © 2024-2025 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	// SubnetAssociationTrunk means targetSubnetName is the parent (trunk) Subnet in the binding.
	// This is the default when subnetAssociation is unset (legacy same-VPC workflow).
	SubnetAssociationTrunk = "TRUNK"
	// SubnetAssociationBranch means targetSubnetName is the child (branch) Subnet in the binding.
	// Used for cross-VPC VLAN extension; NSX SubnetConnectionBindingMap is created under subnetName (parent).
	SubnetAssociationBranch = "BRANCH"
)

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
	// SubnetAssociation indicates the role of targetSubnetName in the binding.
	// TRUNK: targetSubnetName is the parent Subnet (default, legacy behavior).
	// BRANCH: targetSubnetName is the child Subnet; subnetName is the parent and hosts the NSX binding map.
	// +kubebuilder:validation:Enum=TRUNK;BRANCH
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="subnetAssociation is immutable"
	SubnetAssociation string `json:"subnetAssociation,omitempty"`
	// VLANTrafficTag is the VLAN tag configured in the binding. Note, the value of VLANTrafficTag should be
	// unique on the target Subnet or SubnetSet.
	// +kubebuilder:validation:Maximum:=4094
	// +kubebuilder:validation:Minimum:=0
	// +kubebuilder:validation:Required
	VLANTrafficTag int64 `json:"vlanTrafficTag"`
}

// SubnetConnectionBindingMapStatus defines the observed state of SubnetConnectionBindingMap.
type SubnetConnectionBindingMapStatus struct {
	// Conditions described if the SubnetConnectionBindingMaps is configured on NSX or not.
	// Condition type ""
	Conditions []Condition `json:"conditions,omitempty"`
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
// +kubebuilder:printcolumn:name="vlanTrafficTag",type=integer,JSONPath=`.spec.vlanTrafficTag`,description="Vlan used in the NSX SubnetConnectionBindingMap"
// +kubebuilder:printcolumn:name="subnetAssociation",type=string,JSONPath=`.spec.subnetAssociation`,description="TRUNK or BRANCH association for targetSubnetName"
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

func init() {
	SchemeBuilder.Register(&SubnetConnectionBindingMap{}, &SubnetConnectionBindingMapList{})
}

// IsBranchAssociation reports whether targetSubnetName is the child (branch) Subnet.
func (s SubnetConnectionBindingMapSpec) IsBranchAssociation() bool {
	return s.SubnetAssociation == SubnetAssociationBranch
}
