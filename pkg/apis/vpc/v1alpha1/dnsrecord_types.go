/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DNSRecordType defines the supported DNS record types.
type DNSRecordType string

const (
	DNSRecordTypeA    DNSRecordType = "A"
	DNSRecordTypeAAAA DNSRecordType = "AAAA"
)

// DNSRecordSpec defines the desired state of DNSRecord.
type DNSRecordSpec struct {
	// DomainName specifies the DNS domain for this record.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="DomainName is immutable after creation"
	DomainName string `json:"domainName"`

	// RecordName specifies the hostname portion of the FQDN (e.g., "api" for "api.coke.com").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=255
	RecordName string `json:"recordName"`

	// RecordType specifies the DNS record type.
	// +kubebuilder:validation:Enum=A;AAAA
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="RecordType is immutable after creation"
	RecordType DNSRecordType `json:"recordType"`

	// RecordValues specifies the DNS record data values.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	RecordValues []string `json:"recordValues"`

	// TTL specifies the Time-To-Live in seconds for this DNS record.
	// Overrides the zone's default TTL. Range: 0-86400 seconds. Default: 300 (5 minutes).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=300
	// +optional
	TTL *int32 `json:"ttl,omitempty"`
}

// DNSRecordStatus defines the observed state of DNSRecord.
type DNSRecordStatus struct {
	// FQDN is the system-computed fully qualified domain name, formed by combining RecordName with DomainName.
	// +optional
	FQDN string `json:"fqdn,omitempty"`

	// Conditions represents the latest available observations of the DNSRecord's current state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:scope=Namespaced,shortName=dnsrec,categories=nsx
// +kubebuilder:printcolumn:name="DomainName",type="string",JSONPath=".spec.domainName",description="Domain name of DNS record"
// +kubebuilder:printcolumn:name="RecordName",type="string",JSONPath=".spec.recordName",description="Record name"
// +kubebuilder:printcolumn:name="RecordType",type="string",JSONPath=".spec.recordType",description="Record type"
// +kubebuilder:printcolumn:name="FQDN",type="string",JSONPath=".status.fqdn",description="FQDN of DNS record"

// DNSRecord is the Schema for the DNS record API.
type DNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DNSRecordSpec   `json:"spec,omitempty"`
	Status DNSRecordStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DNSRecordList contains a list of DNSRecord.
type DNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DNSRecord `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DNSRecord{}, &DNSRecordList{})
}
