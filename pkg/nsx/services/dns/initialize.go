/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// InitializeDNSRecordService returns a new DNSRecordService with empty store and DNSZoneMap.
func InitializeDNSRecordService(commonService common.Service, vpcService common.VPCServiceProvider) (*DNSRecordService, error) {
	s := &DNSRecordService{
		Service:        commonService,
		DNSRecordStore: BuildDNSRecordStore(),
		VPCService:     vpcService,
		DNSZoneMap:     map[string]string{},
	}

	// TODO [VCFN-2809]: initialize s.RecordStore from NSX DNS records after NSX DNS are ready
	return s, nil
}
