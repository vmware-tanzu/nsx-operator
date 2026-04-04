/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"net"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ResourceKindGateway     = "Gateway"
	ResourceKindListenerSet = "ListenerSet"
	ResourceKindService     = "Service"
)

// ResourceRef identifies a K8s resource by Kind, Namespace, Name, and UID.
// Used for both the address provider (e.g. Gateway) and the owner (Gateway or ListenerSet) of a Record.
type ResourceRef struct {
	metav1.Object
	Kind string
}

// Record represents one desired DNS mapping: a set of IPs (from the Gateway) and FQDNs
// (from Gateway or ListenerSet listeners), with references to the address provider and owner.
type Record struct {
	// Addresses are the IPs for A/AAAA records (from Gateway status.Addresses).
	Addresses []net.IP
	// Hostnames are the FQDNs to map (from Gateway or ListenerSet listener hostnames).
	Hostnames []string
	// AddressProvider identifies the resource that provides the IPs (the Gateway or Service).
	AddressProvider *ResourceRef
	// Owner identifies the resource that owns this record (Gateway or ListenerSet or Service).
	Owner        *ResourceRef
	ForSVService bool
}
