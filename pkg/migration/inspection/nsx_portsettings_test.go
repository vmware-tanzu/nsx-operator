/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

type portSettingsClientStub struct {
	pages []model.VpcSubnetPortSettingListResult
	err   error
	calls int
}

func (client *portSettingsClientStub) List(_, _, _, _ string, _ *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcSubnetPortSettingListResult, error) {
	client.calls++
	if client.calls > len(client.pages) {
		return model.VpcSubnetPortSettingListResult{}, client.err
	}
	return client.pages[client.calls-1], nil
}

func TestNSXPortSettingReader(t *testing.T) {
	ptr := func(value string) *string { return &value }
	migrationTag, err := DVPGTag("vc-uuid", "dvpg-1")
	require.NoError(t, err)
	require.Equal(t, DVPGTagScope, *migrationTag.Scope)
	require.Equal(t, "vc-uuid:dvpg-1", *migrationTag.Tag)
	client := &portSettingsClientStub{pages: []model.VpcSubnetPortSettingListResult{
		{Cursor: ptr("next"), Results: []model.VpcSubnetPortSetting{{Id: ptr("matching"), Tags: []model.Tag{migrationTag}}, {Id: ptr("unrelated")}}},
		{Results: []model.VpcSubnetPortSetting{{Id: ptr("also-matching"), Tags: []model.Tag{migrationTag}}}},
	}}
	reader := NSXPortSettingReader{Client: client}
	settings, err := reader.List(context.Background(), "default", "project", "vpc", "subnet", "vc-uuid", "dvpg-1")
	require.NoError(t, err)
	require.Len(t, settings, 2)
	require.Equal(t, "matching", *settings[0].Id)
	require.Equal(t, "also-matching", *settings[1].Id)

	client = &portSettingsClientStub{pages: []model.VpcSubnetPortSettingListResult{{Cursor: ptr("next"), Results: settings}}, err: errors.New("NSX unavailable")}
	reader.Client = client
	settings, err = reader.List(context.Background(), "default", "project", "vpc", "subnet", "vc-uuid", "dvpg-1")
	require.ErrorContains(t, err, "NSX unavailable")
	require.Nil(t, settings)

	client = &portSettingsClientStub{pages: []model.VpcSubnetPortSettingListResult{{Cursor: ptr("next")}, {Cursor: ptr("next")}}}
	reader.Client = client
	_, err = reader.List(context.Background(), "default", "project", "vpc", "subnet", "vc-uuid", "dvpg-1")
	require.ErrorContains(t, err, "repeated cursor")
}

func TestDVPGTagRejectsAmbiguousIdentity(t *testing.T) {
	for _, identity := range [][2]string{{"", "dvpg-1"}, {"vc-1", ""}, {"vc:1", "dvpg-1"}, {"vc-1", "dvpg:1"}} {
		_, err := DVPGTag(identity[0], identity[1])
		require.Error(t, err)
	}
}
