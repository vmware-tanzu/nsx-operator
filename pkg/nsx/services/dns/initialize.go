/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"fmt"
	"sync"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// InitializeDNSRecordService constructs DNSRecordService, then loads DNS records created by nsx-operator into
// DNSRecordStore via InitializeResourceStore.
func InitializeDNSRecordService(commonService common.Service, vpcService common.VPCServiceProvider) (*DNSRecordService, error) {
	s := &DNSRecordService{
		Service:        commonService,
		DNSRecordStore: BuildDNSRecordStore(),
	}

	var wg sync.WaitGroup
	fatalErrors := make(chan error, 1)
	wgDone := make(chan struct{})

	wg.Add(1)
	// TODO: switch to use NSX DNS record
	go s.Service.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeDNSRecord, nil, s.DNSRecordStore)

	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		return s, nil
	case err := <-fatalErrors:
		return s, fmt.Errorf("initialize DNS service: %w", err)
	}
}
