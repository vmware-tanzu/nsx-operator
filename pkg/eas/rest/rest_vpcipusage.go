/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"fmt"
	"strings"

	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/endpoints/request"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
)

var vpcIPUsageColumns = []metav1.TableColumnDefinition{
	{Name: "Name", Type: "string", Format: "name", Description: "Name of the resource"},
	{Name: "IPBLOCKS", Type: "string", Description: "IP blocks summary"},
}

func vpcIPBlocksSummary(usage *easv1alpha1.VPCIPAddressUsage) string {
	var parts []string
	for _, b := range usage.IPBlocks {
		cidr := ""
		if len(b.CIDRs) > 0 {
			cidr = b.CIDRs[0]
		}
		parts = append(parts, fmt.Sprintf("%s(%s%%)", cidr, b.PercentageUsed))
	}
	return truncateCol(strings.Join(parts, ","))
}

func NewVPCIPUsageStorage(store *storage.VPCIPAddressUsageStorage, provider eas.VPCInfoProvider) *vpcIPUsageStorage {
	return &vpcIPUsageStorage{store: store, vpcProvider: provider}
}

type vpcIPUsageStorage struct {
	store       *storage.VPCIPAddressUsageStorage
	vpcProvider eas.VPCInfoProvider
}

func (r *vpcIPUsageStorage) New() runtime.Object     { return &easv1alpha1.VPCIPAddressUsage{} }
func (r *vpcIPUsageStorage) Destroy()                {}
func (r *vpcIPUsageStorage) NamespaceScoped() bool   { return true }
func (r *vpcIPUsageStorage) NewList() runtime.Object { return &easv1alpha1.VPCIPAddressUsageList{} }
func (r *vpcIPUsageStorage) GetSingularName() string { return "vpcipaddressusage" }

func (r *vpcIPUsageStorage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	ns, _ := request.NamespaceFrom(ctx)
	return r.store.Get(ctx, ns, name)
}

func (r *vpcIPUsageStorage) List(ctx context.Context, _ *metainternalversion.ListOptions) (runtime.Object, error) {
	if ns, ok := request.NamespaceFrom(ctx); ok {
		return r.store.List(ctx, ns)
	}
	merged := &easv1alpha1.VPCIPAddressUsageList{}
	for _, ns := range r.vpcProvider.ListAllVPCNamespaces() {
		result, err := r.store.List(ctx, ns)
		if err != nil {
			continue
		}
		merged.Items = append(merged.Items, result.Items...)
	}
	return merged, nil
}

func (r *vpcIPUsageStorage) ConvertToTable(_ context.Context, object runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{ColumnDefinitions: vpcIPUsageColumns}
	switch obj := object.(type) {
	case *easv1alpha1.VPCIPAddressUsage:
		table.Rows = []metav1.TableRow{tableRow(obj.Name, obj.Namespace, vpcIPBlocksSummary(obj))}
	case *easv1alpha1.VPCIPAddressUsageList:
		for i := range obj.Items {
			item := &obj.Items[i]
			table.Rows = append(table.Rows, tableRow(item.Name, item.Namespace, vpcIPBlocksSummary(item)))
		}
	default:
		return nil, fmt.Errorf("unsupported type %T for VPCIPAddressUsage table", object)
	}
	return table, nil
}
