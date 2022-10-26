/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NSXServiceAccountSpec defines the desired state of NSXServiceAccount
type NSXServiceAccountSpec struct {
	VPCName string `json:"vpcName,omitempty"`
}

type NSXProxyEndpointAddress struct {
	Hostname string `json:"hostname,omitempty"`
	//+kubebuilder:validation:Format=ip
	IP string `json:"ip,omitempty"`
}

type NSXProxyProtocol string

const (
	NSXProxyProtocolTCP NSXProxyProtocol = "TCP"
)

type NSXProxyEndpointPort struct {
	Name     string           `json:"name,omitempty"`
	Port     uint16           `json:"port,omitempty"`
	Protocol NSXProxyProtocol `json:"protocol,omitempty"`
}

type NSXProxyEndpoint struct {
	Addresses []NSXProxyEndpointAddress `json:"addresses,omitempty"`
	Ports     []NSXProxyEndpointPort    `json:"ports,omitempty"`
}

type NSXSecret struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type NSXServiceAccountPhase string

const (
	NSXServiceAccountPhaseRealized   NSXServiceAccountPhase = "realized"
	NSXServiceAccountPhaseInProgress NSXServiceAccountPhase = "inProgress"
	NSXServiceAccountPhaseFailed     NSXServiceAccountPhase = "failed"
)

// NSXServiceAccountStatus defines the observed state of NSXServiceAccount
type NSXServiceAccountStatus struct {
	Phase          NSXServiceAccountPhase `json:"phase,omitempty"`
	Reason         string                 `json:"reason,omitempty"`
	VPCPath        string                 `json:"vpcPath,omitempty"`
	NSXManagers    []string               `json:"nsxManagers,omitempty"`
	ProxyEndpoints []NSXProxyEndpoint     `json:"proxyEndpoints,omitempty"`
	ClusterID      string                 `json:"clusterID,omitempty"`
	ClusterName    string                 `json:"clusterName,omitempty"`
	Secrets        []NSXSecret            `json:"secrets,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// NSXServiceAccount is the Schema for the nsxserviceaccounts API
type NSXServiceAccount struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NSXServiceAccountSpec   `json:"spec,omitempty"`
	Status NSXServiceAccountStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// NSXServiceAccountList contains a list of NSXServiceAccount
type NSXServiceAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NSXServiceAccount `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NSXServiceAccount{}, &NSXServiceAccountList{})
}
