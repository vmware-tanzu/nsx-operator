/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SubnetPortSettingSpec defines the desired state of SubnetPortSetting.
type SubnetPortSettingSpec struct {
	// SubnetName defines the parent Subnet name of the SubnetPortSetting.
	SubnetName string `json:"subnetName,omitempty"`
}

// SubnetPortSettingStatus defines the observed state of SubnetPortSetting.
type SubnetPortSettingStatus struct {
	// Conditions describes current state of SubnetPortSetting.
	Conditions []Condition `json:"conditions,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:selectablefield:JSONPath=`.spec.subnetName`

// SubnetPortSetting is the Schema for the subnetportsettings API.
// +kubebuilder:printcolumn:name="SubnetName",type=string,JSONPath=`.spec.subnetName`,description="Parent Subnet name"
type SubnetPortSetting struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubnetPortSettingSpec   `json:"spec,omitempty"`
	Status SubnetPortSettingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SubnetPortSettingList contains a list of SubnetPortSetting.
type SubnetPortSettingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SubnetPortSetting `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SubnetPortSetting{}, &SubnetPortSettingList{})
}
