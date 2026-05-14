/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// CleanupInfraResources deletes all cached ProjectDnsRecord objects on NSX (Hierarchy / OrgRoot patch) and removes them from the local store.
func (s *DNSRecordService) CleanupInfraResources(ctx context.Context) error {
	objs := s.DNSRecordStore.ListDNSRecords()
	if len(objs) == 0 {
		return nil
	}
	toDelete := make([]*model.ProjectDnsRecord, 0, len(objs))
	for _, rec := range objs {
		if rec == nil {
			continue
		}
		cp := *rec
		cp.MarkedForDelete = common.Bool(true)
		toDelete = append(toDelete, &cp)
	}
	log.Info("Cleaning up ProjectDnsRecord resources on NSX", "count", len(toDelete))
	return s.ProjectDnsRecordBuilder.PagingUpdateResources(ctx, toDelete, common.DefaultHAPIChildrenCount, s.NSXClient, func(deletedObjs []*model.ProjectDnsRecord) {
		s.DNSRecordStore.DeleteMultipleObjects(deletedObjs)
	})
}
