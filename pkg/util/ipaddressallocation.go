/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// IPAddressAllocationHasOwner checks the CR identity in an allocation's tags.
// Callers must separately verify the allocation's backend path and IPs.
func IPAddressAllocationHasOwner(allocation *model.VpcIpAddressAllocation, uid types.UID, namespace, name string) bool {
	return allocation != nil &&
		nsxutil.FindTag(allocation.Tags, common.TagScopeIPAddressAllocationCRUID) == string(uid) &&
		nsxutil.FindTag(allocation.Tags, common.TagScopeNamespace) == namespace &&
		nsxutil.FindTag(allocation.Tags, common.TagScopeIPAddressAllocationCRName) == name
}
