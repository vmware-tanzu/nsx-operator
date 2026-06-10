/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SubnetPortConfigSpec defines the desired state of SubnetPortConfig.
type SubnetPortConfigSpec struct {
	// Subnet defines the parent Subnet name of the SubnetPortConfig.
	Subnet string `json:"subnet,omitempty"`
}

// SubnetPortConfigStatus defines the observed state of SubnetPortConfig.
type SubnetPortConfigStatus struct {
	// Conditions describes current state of SubnetPortConfig.
	Conditions []Condition `json:"conditions,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:selectablefield:JSONPath=`.spec.subnet`

// SubnetPortConfig is the Schema for the subnetportconfigs API.
// +kubebuilder:printcolumn:name="Subnet",type=string,JSONPath=`.spec.subnet`,description="Parent Subnet name"
type SubnetPortConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubnetPortConfigSpec   `json:"spec,omitempty"`
	Status SubnetPortConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SubnetPortConfigList contains a list of SubnetPortConfig.
type SubnetPortConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SubnetPortConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SubnetPortConfig{}, &SubnetPortConfigList{})
}
