/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/endpoints/request"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
)

var subnetDHCPColumns = []metav1.TableColumnDefinition{
	{Name: "Name", Type: "string", Format: "name", Description: "Name of the resource"},
	{Name: "IP_POOL_STATS", Type: "string", Description: "IP pool statistics"},
}

func subnetDHCPStatsSummary(stats *easv1alpha1.SubnetDHCPServerStats) string {
	var parts []string
	for i, p := range stats.IPPoolStats {
		parts = append(parts, fmt.Sprintf("pool-%d(alloc:%d%%,size:%d)", i, p.AllocatedPercentage, p.PoolSize))
	}
	return truncateCol(strings.Join(parts, ","))
}

func NewSubnetDHCPStatsStorage(store *storage.SubnetDHCPStatsStorage) *subnetDHCPStatsStorage {
	return &subnetDHCPStatsStorage{store: store}
}

// subnetDHCPStatsStorage supports Get-by-name only; List is not exposed for this resource.
type subnetDHCPStatsStorage struct {
	store *storage.SubnetDHCPStatsStorage
}

func (r *subnetDHCPStatsStorage) New() runtime.Object     { return &easv1alpha1.SubnetDHCPServerStats{} }
func (r *subnetDHCPStatsStorage) Destroy()                {}
func (r *subnetDHCPStatsStorage) NamespaceScoped() bool   { return true }
func (r *subnetDHCPStatsStorage) GetSingularName() string { return "subnetdhcpserverstats" }

func (r *subnetDHCPStatsStorage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	ns, _ := request.NamespaceFrom(ctx)
	return r.store.Get(ctx, ns, name)
}

func (r *subnetDHCPStatsStorage) ConvertToTable(_ context.Context, object runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{ColumnDefinitions: subnetDHCPColumns}
	obj, ok := object.(*easv1alpha1.SubnetDHCPServerStats)
	if !ok {
		return nil, fmt.Errorf("unsupported type %T for SubnetDHCPServerStats table", object)
	}
	table.Rows = []metav1.TableRow{tableRow(obj.Name, obj.Namespace, subnetDHCPStatsSummary(obj))}
	return table, nil
}
