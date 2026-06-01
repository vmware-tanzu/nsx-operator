/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/endpoints/request"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
)

var subnetIPPoolsColumns = []metav1.TableColumnDefinition{
	{Name: "Name", Type: "string", Format: "name", Description: "Name of the resource"},
	{Name: "RESULTS", Type: "string", Description: "IP pool results"},
}

func subnetIPPoolsSummary(pools *easv1alpha1.SubnetIPPools) string {
	id := pools.Name
	if id == "" && pools.PoolUsage == nil && pools.IPAddressType == "" {
		return "<none>"
	}
	var availableIPs int64
	if pools.PoolUsage != nil {
		availableIPs = pools.PoolUsage.AvailableIPs
	}
	return truncateCol(fmt.Sprintf("%s(type:%s,availableIPs:%d)", id, pools.IPAddressType, availableIPs))
}

func NewSubnetIPPoolsStorage(store *storage.SubnetIPPoolsStorage) *subnetIPPoolsStorage {
	return &subnetIPPoolsStorage{store: store}
}

// subnetIPPoolsStorage supports Get-by-name only; List is not exposed for this resource.
type subnetIPPoolsStorage struct {
	store *storage.SubnetIPPoolsStorage
}

func (r *subnetIPPoolsStorage) New() runtime.Object     { return &easv1alpha1.SubnetIPPools{} }
func (r *subnetIPPoolsStorage) Destroy()                {}
func (r *subnetIPPoolsStorage) NamespaceScoped() bool   { return true }
func (r *subnetIPPoolsStorage) GetSingularName() string { return "subnetippools" }

func (r *subnetIPPoolsStorage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	ns, _ := request.NamespaceFrom(ctx)
	return r.store.Get(ctx, ns, name)
}

func (r *subnetIPPoolsStorage) ConvertToTable(_ context.Context, object runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{ColumnDefinitions: subnetIPPoolsColumns}
	obj, ok := object.(*easv1alpha1.SubnetIPPools)
	if !ok {
		return nil, fmt.Errorf("unsupported type %T for SubnetIPPools table", object)
	}
	table.Rows = []metav1.TableRow{tableRow(obj.Name, obj.Namespace, subnetIPPoolsSummary(obj))}
	return table, nil
}
