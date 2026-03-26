/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
)

// TODO: remove this file after NSX DNS resource APIs are ready
// DNSRecord is a mock for the NSX DNS record resource type.
// Replace with the real model type when the SDK provides it (e.g. model.DNSRecord).
type DNSRecord struct {
	Id              *string
	Path            *string
	DisplayName     *string
	MarkedForDelete *bool
	Tags            []model.Tag
	// DNS-specific fields; adjust when real API is known.
	Fqdn         *string
	IpAddress    *string
	RecordType   *string
	DnsZonePath  *string
	RecordValues []string
}
