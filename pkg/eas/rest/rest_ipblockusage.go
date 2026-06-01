/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"encoding/json"
	"fmt"

	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/endpoints/request"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
)

var ipBlockUsageColumns = []metav1.TableColumnDefinition{
	{Name: "Name", Type: "string", Format: "name", Description: "Name of the resource"},
	{Name: "USED IP RANGES", Type: "string", Description: "Used IP ranges"},
	{Name: "AVAILABLE IP RANGES", Type: "string", Description: "Available IP ranges"},
}

func ipBlockRangesSummary(ranges []string) string {
	if len(ranges) == 0 {
		return ""
	}
	data, _ := json.Marshal(ranges)
	return truncateCol(string(data))
}

func NewIPBlockUsageStorage(store *storage.IPBlockUsageStorage, provider eas.VPCInfoProvider) *ipBlockUsageStorage {
	return &ipBlockUsageStorage{store: store, vpcProvider: provider}
}

type ipBlockUsageStorage struct {
	store       *storage.IPBlockUsageStorage
	vpcProvider eas.VPCInfoProvider
}

func (r *ipBlockUsageStorage) New() runtime.Object     { return &easv1alpha1.IPBlockUsage{} }
func (r *ipBlockUsageStorage) Destroy()                {}
func (r *ipBlockUsageStorage) NamespaceScoped() bool   { return true }
func (r *ipBlockUsageStorage) NewList() runtime.Object { return &easv1alpha1.IPBlockUsageList{} }
func (r *ipBlockUsageStorage) GetSingularName() string { return "ipblockusage" }

func (r *ipBlockUsageStorage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	ns, _ := request.NamespaceFrom(ctx)
	return r.store.Get(ctx, ns, name)
}

func (r *ipBlockUsageStorage) List(ctx context.Context, _ *metainternalversion.ListOptions) (runtime.Object, error) {
	if ns, ok := request.NamespaceFrom(ctx); ok {
		return r.store.List(ctx, ns)
	}
	merged := &easv1alpha1.IPBlockUsageList{}
	for _, ns := range r.vpcProvider.ListAllVPCNamespaces() {
		result, err := r.store.List(ctx, ns)
		if err != nil {
			continue
		}
		merged.Items = append(merged.Items, result.Items...)
	}
	return merged, nil
}

func (r *ipBlockUsageStorage) ConvertToTable(_ context.Context, object runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{ColumnDefinitions: ipBlockUsageColumns}
	switch obj := object.(type) {
	case *easv1alpha1.IPBlockUsage:
		table.Rows = []metav1.TableRow{
			tableRow(obj.Name, obj.Namespace, ipBlockRangesSummary(obj.UsedIPRanges), ipBlockRangesSummary(obj.AvailableIPRanges)),
		}
	case *easv1alpha1.IPBlockUsageList:
		for i := range obj.Items {
			item := &obj.Items[i]
			table.Rows = append(table.Rows,
				tableRow(item.Name, item.Namespace, ipBlockRangesSummary(item.UsedIPRanges), ipBlockRangesSummary(item.AvailableIPRanges)),
			)
		}
	default:
		return nil, fmt.Errorf("unsupported type %T for IPBlockUsage table", object)
	}
	return table, nil
}
