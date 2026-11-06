/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"hash/fnv"
	"sort"

	"k8s.io/apimachinery/pkg/types"
)

func (service *IPAddressAllocationService) allocationLockIndex(uid types.UID) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(uid))
	return int(hash.Sum32() % uint32(len(service.allocationLocks)))
}

// Lock both owners while transferring an allocation. Bounded stripes avoid
// retaining one mutex per historical UID; a stable order prevents deadlocks.
func (service *IPAddressAllocationService) lockAllocationUIDs(uids ...types.UID) func() {
	indexes := make([]int, 0, len(uids))
	seen := make(map[int]bool, len(uids))
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		index := service.allocationLockIndex(uid)
		if !seen[index] {
			indexes = append(indexes, index)
			seen[index] = true
		}
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		service.allocationLocks[index].Lock()
	}
	return func() {
		for i := len(indexes) - 1; i >= 0; i-- {
			service.allocationLocks[indexes[i]].Unlock()
		}
	}
}

func (service *IPAddressAllocationService) readAllocationUID(uid types.UID) func() {
	lock := &service.allocationLocks[service.allocationLockIndex(uid)]
	lock.RLock()
	return lock.RUnlock
}
