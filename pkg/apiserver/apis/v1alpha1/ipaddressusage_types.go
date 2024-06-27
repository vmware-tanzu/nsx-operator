/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	External Visibility = "EXTERNAL"
	Project  Visibility = "PROJECT"
	Private  Visibility = "PRIVATE"
)

type Visibility string

type IPAddress struct {
	Address string `json:"address,omitempty" protobuf:"bytes,1,opt,name=address"`
	Subnet  string `json:"subnet,omitempty" protobuf:"bytes,2,opt,name=subnet"`
}

type AllocatedByVPC struct {
	Count       int         `json:"count,omitempty" protobuf:"bytes,1,opt,name=count"`
	IPAddresses []IPAddress `json:"ipAddresses,omitempty" protobuf:"bytes,2,opt,name=ipAddresses"`
	CIDR        string      `json:"cidr,omitempty" protobuf:"bytes,3,opt,name=cidr"`
	Visibility  Visibility  `json:"visibility,omitempty" protobuf:"bytes,4,opt,name=visibility"`
}

// IPBlock contains the used IPAddresses and CIDR.
type IPBlock struct {
	Path      string `json:"path,omitempty" protobuf:"bytes,1,rep,name=path"`
	Available int    `json:"available,omitempty" protobuf:"bytes,2,rep,name=available"`
	// AllocatedByVPC contains the cidr and used IP Range.
	Allocated AllocatedByVPC `json:"allocatedByVpc,omitempty" protobuf:"bytes,3,rep,name=allocatedByVpc"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// IPAddressUsage
// +k8s:openapi-gen=true
// +genclient:Namespaced
// +genclient:onlyVerbs=get
type IPAddressUsage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	IPBlocks []IPBlock `json:"ipBlocks" protobuf:"bytes,2,rep,name=ipBlocks"`
}

// IPAddressUsageList
// +k8s:openapi-gen=true
// +genclient:Namespaced
// +genclient:onlyVerbs=get
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type IPAddressUsageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []IPAddressUsage `json:"items"`
}
