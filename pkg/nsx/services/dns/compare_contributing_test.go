/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package dns

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// wrongComparable implements common.Comparable but is not *dnsRecordComparable.
type wrongComparable struct{}

func (w *wrongComparable) Key() string           { return "" }
func (w *wrongComparable) Value() data.DataValue { return nil }

func TestDNSRecordComparable_Key_table(t *testing.T) {
	path := "/orgs/o/projects/p/dns-records/r1"
	tests := []struct {
		name string
		d    *dnsRecordComparable
		want string
	}{
		{"nil receiver", nil, ""},
		{"nil Path field", (*dnsRecordComparable)(&model.DnsRecord{}), ""},
		{"valid path", (*dnsRecordComparable)(&model.DnsRecord{Path: &path}), path},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.d.Key())
		})
	}
}

func TestDNSRecordComparable_Value_nil_receiver(t *testing.T) {
	var d *dnsRecordComparable
	require.Nil(t, d.Value())
}

func TestSortedNormalizedTagsForCompare_nilFields(t *testing.T) {
	// Tags with nil Scope and nil Tag value should sort without panic (treated as "").
	nilScope := model.Tag{Scope: nil, Tag: servicecommon.String("v")}
	nilTag := model.Tag{Scope: servicecommon.String("z"), Tag: nil}
	normal := model.Tag{Scope: servicecommon.String("a"), Tag: servicecommon.String("x")}

	got := sortedNormalizedTagsForCompare([]model.Tag{nilScope, nilTag, normal})
	require.Len(t, got, 3)
	// nil Scope sorts as "" → comes before "a" and "z".
	require.Nil(t, got[0].Scope)
}

func TestComparableToDnsRecord_table(t *testing.T) {
	t.Run("nil interface returns nil", func(t *testing.T) {
		require.Nil(t, comparableToDnsRecord(nil))
	})
	t.Run("wrong type returns nil", func(t *testing.T) {
		require.Nil(t, comparableToDnsRecord(&wrongComparable{}))
	})
}

// --- contributing.go tests ---

func TestResourceRefFromDNSRecord_table(t *testing.T) {
	validTags := func(kind, ns, name string) []model.Tag {
		return []model.Tag{
			modelTag(servicecommon.TagScopeDNSRecordFor, resourceKindToCreatedFor(kind)),
			modelTag(servicecommon.TagScopeDNSRecordOwnerNamespace, ns),
			modelTag(servicecommon.TagScopeDNSRecordOwnerName, name),
		}
	}

	tests := []struct {
		name     string
		rec      *model.DnsRecord
		wantOk   bool
		wantKind string
		wantNS   string
		wantName string
	}{
		{
			name:   "nil record",
			rec:    nil,
			wantOk: false,
		},
		{
			name:   "no owner tags",
			rec:    &model.DnsRecord{Tags: []model.Tag{}},
			wantOk: false,
		},
		{
			name: "missing name tag",
			rec: &model.DnsRecord{Tags: []model.Tag{
				modelTag(servicecommon.TagScopeDNSRecordFor, servicecommon.TagValueDNSRecordForService),
				modelTag(servicecommon.TagScopeDNSRecordOwnerNamespace, "ns"),
				// TagScopeDNSRecordOwnerName is missing
			}},
			wantOk: false,
		},
		{
			name: "unknown dns_for kind",
			rec: &model.DnsRecord{Tags: []model.Tag{
				modelTag(servicecommon.TagScopeDNSRecordFor, "custom_unknown_kind"),
				modelTag(servicecommon.TagScopeDNSRecordOwnerNamespace, "ns"),
				modelTag(servicecommon.TagScopeDNSRecordOwnerName, "svc"),
			}},
			wantOk: false,
		},
		{
			name:     "valid Service record",
			rec:      &model.DnsRecord{Tags: validTags(ResourceKindService, "ns1", "svcA")},
			wantOk:   true,
			wantKind: ResourceKindService,
			wantNS:   "ns1",
			wantName: "svcA",
		},
		{
			name:     "valid HTTPRoute record",
			rec:      &model.DnsRecord{Tags: validTags(ResourceKindHTTPRoute, "app", "route1")},
			wantOk:   true,
			wantKind: ResourceKindHTTPRoute,
			wantNS:   "app",
			wantName: "route1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := resourceRefFromDNSRecord(tc.rec)
			require.Equal(t, tc.wantOk, ok)
			if !tc.wantOk {
				require.Nil(t, ref)
				return
			}
			require.NotNil(t, ref)
			require.Equal(t, tc.wantKind, ref.Kind)
			require.Equal(t, tc.wantNS, ref.GetNamespace())
			require.Equal(t, tc.wantName, ref.GetName())
		})
	}
}

func TestPrimaryOwnerNNIndexKeyFromRecord_table(t *testing.T) {
	tests := []struct {
		name    string
		rec     *model.DnsRecord
		wantKey string
	}{
		{
			name:    "record with no owner tags returns empty",
			rec:     &model.DnsRecord{Tags: []model.Tag{}},
			wantKey: "",
		},
		{
			name: "record with valid owner tags returns index key",
			rec: &model.DnsRecord{Tags: []model.Tag{
				modelTag(servicecommon.TagScopeDNSRecordFor, servicecommon.TagValueDNSRecordForService),
				modelTag(servicecommon.TagScopeDNSRecordOwnerNamespace, "ns"),
				modelTag(servicecommon.TagScopeDNSRecordOwnerName, "svc"),
			}},
			wantKey: "service/ns/svc",
		},
		{
			name: "HTTPRoute owner returns correct key",
			rec: &model.DnsRecord{Tags: []model.Tag{
				modelTag(servicecommon.TagScopeDNSRecordFor, servicecommon.TagValueDNSRecordForHTTPRoute),
				modelTag(servicecommon.TagScopeDNSRecordOwnerNamespace, "app"),
				modelTag(servicecommon.TagScopeDNSRecordOwnerName, "route1"),
			}},
			wantKey: "httproute/app/route1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantKey, primaryOwnerNNIndexKeyFromRecord(tc.rec))
		})
	}
}

func TestOwnerNNIndexKeyForResourceRef_table(t *testing.T) {
	tests := []struct {
		name  string
		owner *ResourceRef
		want  string
	}{
		{
			name:  "nil owner returns empty",
			owner: nil,
			want:  "",
		},
		{
			name:  "unknown kind returns empty",
			owner: &ResourceRef{Kind: "UnknownKind", Object: &metav1.ObjectMeta{Namespace: "ns", Name: "obj"}},
			want:  "",
		},
		{
			name:  "Service kind returns key",
			owner: &ResourceRef{Kind: ResourceKindService, Object: &metav1.ObjectMeta{Namespace: "ns", Name: "svc"}},
			want:  "service/ns/svc",
		},
		{
			name:  "GRPCRoute kind returns key",
			owner: &ResourceRef{Kind: ResourceKindGRPCRoute, Object: &metav1.ObjectMeta{Namespace: "app", Name: "gr1"}},
			want:  "grpcroute/app/gr1",
		},
		{
			name:  "TLSRoute kind returns key",
			owner: &ResourceRef{Kind: ResourceKindTLSRoute, Object: &metav1.ObjectMeta{Namespace: "app", Name: "tls1"}},
			want:  "tlsroute/app/tls1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ownerNNIndexKeyForResourceRef(tc.owner))
		})
	}
}
