/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package storage

import (
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// findEndpointByUID searches NSX using the owning CR's UID tag. The NSX endpoint
// ID is generated independently of the CR UID, so use the returned policy path.
func findEndpointByUID(client *nsx.Client, resourceType, uidScope string, uid types.UID) (*common.VPCResourceInfo, error) {
	if uid == "" {
		return nil, fmt.Errorf("endpoint CR has no UID")
	}
	query := fmt.Sprintf("resource_type:%s AND tags.scope:%s AND tags.tag:%s AND marked_for_delete:false",
		resourceType, strings.ReplaceAll(uidScope, "/", "\\/"), string(uid))
	var cursor *string
	for {
		response, err := client.QueryClient.List(query, cursor, nil, nil, nil, nil)
		if err != nil {
			return nil, err
		}
		for _, result := range response.Results {
			resource, errs := common.NewConverter().ConvertToGolang(result, model.PolicyResourceBindingType())
			if len(errs) > 0 {
				return nil, fmt.Errorf("failed to decode NSX endpoint search result: %v", errs)
			}
			endpoint := resource.(model.PolicyResource)
			// Check the exact tag pair, including when search returns stale resources
			// for an earlier CR with the same name.
			if nsxTagValue(endpoint.Tags, uidScope) != string(uid) {
				continue
			}
			info, err := common.ParseVPCResourcePath(DerefString(endpoint.Path))
			if err != nil {
				return nil, err
			}
			return &info, nil
		}
		if response.Cursor == nil || *response.Cursor == "" {
			return nil, nil
		}
		cursor = response.Cursor
	}
}
