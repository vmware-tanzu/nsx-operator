/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// NATAction defines the direction of NAT for this object. Each NAT CR uses exactly one action.
type NATAction string

const (
	NATActionSNAT NATAction = "SNAT"
	NATActionDNAT NATAction = "DNAT"
)

// NATSpec defines the desired state of NAT.
// translatedNetworkAllocation vs translatedNetwork, and destinationNetworkAllocation vs destinationNetwork, are mutually exclusive pairs.
// Allocation fields reference an IPAddressAllocation CR by metadata.name in the same namespace as this NAT.
//
// SNAT is modeled with sourceNetwork plus a translated side (allocation or literal). DNAT adds a destination side.
// If you later need destination-specific SNAT, extend this spec explicitly rather than overloading DNAT-only fields.
//
// +kubebuilder:validation:XValidation:rule=`!((has(self.translatedNetworkAllocation) && self.translatedNetworkAllocation != "") && (has(self.translatedNetwork) && self.translatedNetwork != ""))`,message="Pick one on the translated side: set translatedNetwork (static IP/CIDR) OR translatedNetworkAllocation (IPAddressAllocation name), not both."
// +kubebuilder:validation:XValidation:rule=`!((has(self.destinationNetworkAllocation) && self.destinationNetworkAllocation != "") && (has(self.destinationNetwork) && self.destinationNetwork != ""))`,message="Pick one on the destination side: set destinationNetwork (static IP/CIDR) OR destinationNetworkAllocation (IPAddressAllocation name), not both."
// +kubebuilder:validation:XValidation:rule=`self.action != "SNAT" || (has(self.sourceNetwork) && self.sourceNetwork != "" && ((has(self.translatedNetworkAllocation) && self.translatedNetworkAllocation != "") != (has(self.translatedNetwork) && self.translatedNetwork != "")))`,message="For SNAT: set sourceNetwork, and set exactly one of translatedNetwork or translatedNetworkAllocation."
// +kubebuilder:validation:XValidation:rule=`self.action != "DNAT" || (((has(self.destinationNetworkAllocation) && self.destinationNetworkAllocation != "") != (has(self.destinationNetwork) && self.destinationNetwork != "")) && ((has(self.translatedNetworkAllocation) && self.translatedNetworkAllocation != "") != (has(self.translatedNetwork) && self.translatedNetwork != "")))`,message="For DNAT: set exactly one of destinationNetwork or destinationNetworkAllocation, and exactly one of translatedNetwork or translatedNetworkAllocation."
type NATSpec struct {
	// Action is the NAT direction. One of SNAT or DNAT; create separate NAT objects if both are needed.
	// +kubebuilder:validation:Enum=SNAT;DNAT
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	Action NATAction `json:"action"`
	// SourceNetwork is the source CIDR or IP for SNAT (required when action is SNAT).
	// Changing this in place can break existing sessions; treat updates as invalid and recreate the NAT if needed.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	SourceNetwork string `json:"sourceNetwork,omitempty"`
	// TranslatedNetworkAllocation is the metadata.name of an IPAddressAllocation in the same namespace
	// whose allocated address is used as the post-NAT address on the translated side (mutually exclusive with TranslatedNetwork).
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	TranslatedNetworkAllocation string `json:"translatedNetworkAllocation,omitempty"`
	// TranslatedNetwork is a literal IP or CIDR for the translated side (mutually exclusive with TranslatedNetworkAllocation).
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	TranslatedNetwork string `json:"translatedNetwork,omitempty"`
	// DestinationNetworkAllocation is the metadata.name of an IPAddressAllocation in the same namespace
	// for the destination side on DNAT (mutually exclusive with DestinationNetwork).
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	DestinationNetworkAllocation string `json:"destinationNetworkAllocation,omitempty"`
	// DestinationNetwork is a literal IP or CIDR for the destination side on DNAT (mutually exclusive with DestinationNetworkAllocation).
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	DestinationNetwork string `json:"destinationNetwork,omitempty"`
}

// NATStatus defines the observed state of NAT.
// Conditions use the shared Condition type (same as other vpc v1alpha1 CRDs).
// Controllers should use the Ready condition (Reason/Message carry finer-grained states until more condition types are needed).
type NATStatus struct {
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
	// RealizedCIDR surfaces the effective IP or CIDR after realization (especially useful when spec only names an IPAddressAllocation).
	// +optional
	RealizedCIDR string `json:"realizedCIDR,omitempty"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:storageversion
//+kubebuilder:resource:scope=Namespaced,path=nats,singular=nat,shortName=nat

// NAT is the Schema for configuring SNAT or DNAT on a VPC (one action per object).
// +kubebuilder:printcolumn:name="Action",type=string,JSONPath=`.spec.action`,description="SNAT or DNAT"
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.sourceNetwork`,description="Source CIDR or IP (SNAT)"
// +kubebuilder:printcolumn:name="Dest",type=string,JSONPath=`.spec.destinationNetwork`,description="Destination CIDR or IP literal for DNAT (empty if using allocation)"
// +kubebuilder:printcolumn:name="DestAlloc",type=string,JSONPath=`.spec.destinationNetworkAllocation`,description="IPAddressAllocation name for DNAT destination (empty if using literal)"
// +kubebuilder:printcolumn:name="RealizedIP",type=string,JSONPath=`.status.realizedCIDR`,description="Effective IP/CIDR observed on NSX or from allocation"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`,description="Ready condition status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type NAT struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NATSpec   `json:"spec,omitempty"`
	Status NATStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// NATList contains a list of NAT.
type NATList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NAT `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NAT{}, &NATList{})
}
