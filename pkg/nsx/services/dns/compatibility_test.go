/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"errors"
	"reflect"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	searchmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/searchclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestDNSInitializationFeatureGate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		supported bool
		flag      *bool
		searchErr error
	}{
		{name: "old NSX with enabled flag", flag: common.Bool(true)},
		{name: "old NSX without flag"},
		{name: "omitted flag", supported: true},
		{name: "disabled flag", supported: true, flag: common.Bool(false)},
		{name: "enabled", supported: true, flag: common.Bool(true)},
		{name: "enabled Search failure", supported: true, flag: common.Bool(true), searchErr: errors.New("Search unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.ApplyMethod(reflect.TypeOf(&nsx.Client{}), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
				require.Equal(t, nsx.DNSRecord, feature)
				return tc.supported
			})
			defer patches.Reset()
			query := searchmocks.NewMockQueryClient(gomock.NewController(t))
			enabled := tc.supported
			if enabled {
				query.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(model.SearchResponse{}, tc.searchErr)
			}
			cfg := &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "unit-test"}, NsxConfig: &config.NsxConfig{VpcWcpEnhance: tc.flag}}
			svc, err := InitializeDNSRecordService(common.Service{NSXConfig: cfg, NSXClient: &nsx.Client{QueryClient: query}}, nil)
			require.ErrorIs(t, err, tc.searchErr)
			if tc.searchErr != nil {
				return
			}
			require.Equal(t, !enabled, svc.disabled)
			if enabled {
				require.NoError(t, svc.CheckDNSRecordSupport())
				return
			}
			require.ErrorContains(t, svc.CheckDNSRecordSupport(), "NSX 9.2.0")
			rows, _, err := svc.ValidateEndpointsByZone("ns", nil, nil)
			require.NoError(t, err)
			require.Empty(t, rows)
			zones, err := svc.SyncDNSZonesByVpcNetworkConfig(&v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{DNSZones: []string{testDNSZonePathT}}})
			require.NoError(t, err, "disabled DNS must not block NetworkInfo readiness")
			require.Empty(t, zones)
			changed, err := svc.CreateOrUpdateRecords(context.Background(), nil)
			require.NoError(t, err)
			require.False(t, changed)
			changed, err = svc.DeleteRecordByOwnerNN(context.Background(), ResourceKindService, "ns", "no-dns")
			require.NoError(t, err)
			require.False(t, changed)
			require.NoError(t, svc.CleanupInfraResources(context.Background()))
		})
	}
}
