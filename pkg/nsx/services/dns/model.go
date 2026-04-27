/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

// Record is the in-memory NSX DNS row shape until real DNS APIs land (TODO: swap for SDK model).
type Record struct {
	Id              *string
	Path            *string
	DisplayName     *string
	MarkedForDelete *bool
	Tags            []model.Tag
	Fqdn            *string
	RecordType      *string
	DnsZonePath     *string
	RecordValues    []string
}

// Zone is delegated DNS zone metadata (stub until NSX zone API is wired).
type Zone struct {
	Path       *string
	DomainName *string
}
