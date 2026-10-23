/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dnsrecmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/dnsrecordsclient"
	orgrootmocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	extdns "github.com/vmware-tanzu/nsx-operator/pkg/third_party/externaldns/endpoint"
)

func TestDNSRecordPatchFailurePreservesCacheAndRetries(t *testing.T) {
	for _, operation := range []string{"create", "delete"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			store := BuildDNSRecordStore()
			env := newTestDNSRecordService(t, store)
			owner := &ResourceRef{
				Kind:   ResourceKindDNSRecord,
				Object: &metav1.ObjectMeta{Namespace: "ns", Name: "record"},
			}
			ep := extdns.NewEndpoint("api.example.com", extdns.RecordTypeA, "192.0.2.1")
			row := NewEndpointRow(ep, testDNSZonePathT, "api")
			batch := NewOwnerScopedAggregatedRouteDNS(owner, []EndpointRow{*row})
			registerBodiesForBatch(t, env, batch)
			if operation == "delete" {
				_, err := env.CreateOrUpdateRecords(ctx, batch)
				require.NoError(t, err)
			}

			patchClient := orgrootmocks.NewMockOrgRootClient(gomock.NewController(t))
			gomock.InOrder(
				patchClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(errors.New("NSX unavailable")),
				patchClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil),
			)
			env.NSXClient.OrgRootClient = patchClient
			sync := func() (bool, error) {
				if operation == "delete" {
					return env.DeleteRecordByOwnerNN(ctx, owner.Kind, owner.GetNamespace(), owner.GetName())
				}
				return env.CreateOrUpdateRecords(ctx, batch)
			}

			before := store.ListDNSRecords()
			changed, err := sync()
			require.ErrorContains(t, err, "NSX unavailable")
			require.False(t, changed)
			require.Equal(t, before, store.ListDNSRecords(), "failed PATCH must not change the cache")

			changed, err = sync()
			require.NoError(t, err)
			require.True(t, changed, "retry must perform the failed operation")
			if operation == "delete" {
				require.Empty(t, store.ListDNSRecords())
			} else {
				require.Len(t, store.ListDNSRecords(), 1)
			}
		})
	}
}

func TestDNSRecordGetFailureOnlyAppliesConfirmedRecords(t *testing.T) {
	env := newTestDNSRecordService(t, BuildDNSRecordStore())
	record := func(id string) *model.DnsRecord {
		return &model.DnsRecord{
			Id:           servicecommon.String(id),
			Path:         servicecommon.String("/orgs/org1/projects/proj1/dns-records/" + id),
			RecordName:   servicecommon.String(id),
			RecordType:   servicecommon.String("A"),
			ZonePath:     servicecommon.String(testDNSZonePathT),
			RecordValues: []string{"192.0.2.1"},
		}
	}
	failed, confirmed := record("failed"), record("confirmed")
	getClient := dnsrecmocks.NewMockDnsRecordsClient(gomock.NewController(t))
	getClient.EXPECT().Get("org1", "proj1", "failed").Return(model.DnsRecord{}, errors.New("NSX GET unavailable"))
	getClient.EXPECT().Get("org1", "proj1", "confirmed").Return(*confirmed, nil)
	env.NSXClient.DnsRecordsClient = getClient

	applied, err := env.syncDnsRecordsInNSX(context.Background(), []*model.DnsRecord{failed, confirmed}, nil)
	require.ErrorContains(t, err, "NSX GET unavailable")
	require.Equal(t, []*model.DnsRecord{confirmed}, applied)
}

func TestDNSRecordApexIDDoesNotCollideWithHostname(t *testing.T) {
	for _, recordType := range []string{"A", "AAAA"} {
		t.Run(recordType, func(t *testing.T) {
			_, apexPath, _ := getRecordIDAndPathAndType("@", recordType, testDNSZonePathT)
			paths := map[string]bool{apexPath: true}
			for _, name := range []string{"apex", "_apex", "__apex"} {
				_, hostPath, _ := getRecordIDAndPathAndType(name, recordType, testDNSZonePathT)
				require.False(t, paths[hostPath], "record %q must have a distinct NSX path", name)
				paths[hostPath] = true
			}
		})
	}
}
