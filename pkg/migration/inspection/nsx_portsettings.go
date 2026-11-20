/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"fmt"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs/subnets"
)

// PortSettingsClient is implemented by the NSX SDK PortSettingsClient.
type PortSettingsClient interface {
	List(orgID, projectID, vpcID, subnetID string, cursor *string, includeConflicts, includeMarkedForDelete *bool, includedFields *string, pageSize *int64, sortAscending *bool, sortBy *string) (model.VpcSubnetPortSettingListResult, error)
}

var _ PortSettingsClient = (subnets.PortSettingsClient)(nil)

// DVPGTagScope identifies the vCenter and DVPG used to create a PortSetting.
const DVPGTagScope = "migration.network.vmware.com/dvpg"

// DVPGTag is shared by the conversion handler and inspection so the identity
// written to the PortSetting is exactly the identity inspection searches for.
func DVPGTag(vcenterUUID, dvpgID string) (model.Tag, error) {
	identity, err := DVPGIdentity(vcenterUUID, dvpgID)
	if err != nil {
		return model.Tag{}, err
	}
	scope := DVPGTagScope
	return model.Tag{Scope: &scope, Tag: &identity}, nil
}

// DVPGIdentity returns the canonical source identity used in the migration tag.
func DVPGIdentity(vcenterUUID, dvpgID string) (string, error) {
	if vcenterUUID == "" || dvpgID == "" || strings.ContainsAny(vcenterUUID, ": \t\r\n") || strings.ContainsAny(dvpgID, ": \t\r\n") {
		return "", fmt.Errorf("vCenter UUID and DVPG managed-object ID are required")
	}
	value := dvpgIdentity(vcenterUUID, dvpgID)
	if len(value) > 256 {
		return "", fmt.Errorf("vCenter and DVPG identity exceeds NSX tag length")
	}
	return value, nil
}

func dvpgIdentity(vcenterUUID, dvpgID string) string {
	return vcenterUUID + ":" + dvpgID
}

// NSXPortSettingReader reads PortSettings tagged for migration from one subnet.
type NSXPortSettingReader struct {
	Client PortSettingsClient
}

// List returns all PortSettings with the configured migration tag. A partial
// result is never returned as a complete inventory when a page fails.
func (reader NSXPortSettingReader) List(ctx context.Context, orgID, projectID, vpcID, subnetID, vcenterUUID, dvpgID string) ([]model.VpcSubnetPortSetting, error) {
	if reader.Client == nil || orgID == "" || projectID == "" || vpcID == "" || subnetID == "" {
		return nil, fmt.Errorf("NSX PortSettings client and subnet identity are required")
	}
	migrationTag, err := DVPGTag(vcenterUUID, dvpgID)
	if err != nil {
		return nil, err
	}
	allSettings, err := nsxPages(ctx, func(cursor *string) ([]model.VpcSubnetPortSetting, *string, error) {
		page, err := reader.Client.List(orgID, projectID, vpcID, subnetID, cursor, nil, nil, nil, nil, nil, nil)
		return page.Results, page.Cursor, err
	})
	if err != nil {
		return nil, fmt.Errorf("list NSX PortSettings for subnet %s: %w", subnetID, err)
	}
	var matches []model.VpcSubnetPortSetting
	for _, setting := range allSettings {
		for _, tag := range setting.Tags {
			if tag.Scope != nil && tag.Tag != nil && *tag.Scope == *migrationTag.Scope && *tag.Tag == *migrationTag.Tag {
				matches = append(matches, setting)
				break
			}
		}
	}
	return matches, nil
}
