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
	apirest "k8s.io/apiserver/pkg/registry/rest"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

var subnetStatusColumns = []metav1.TableColumnDefinition{
	{Name: "Name", Type: "string", Format: "name", Description: "Name of the resource"},
	{Name: "GATEWAY ADDRESSES", Type: "string", Description: "Subnet gateway addresses"},
}

var (
	_ apirest.Getter         = (*subnetStatusStorage)(nil)
	_ apirest.Lister         = (*subnetStatusStorage)(nil)
	_ apirest.Scoper         = (*subnetStatusStorage)(nil)
	_ apirest.TableConvertor = (*subnetStatusStorage)(nil)
)

func NewSubnetStatusStorage(store *storage.SubnetStatusStorage, provider eas.VPCInfoProvider) *subnetStatusStorage {
	return &subnetStatusStorage{store: store, vpcProvider: provider}
}

type subnetStatusStorage struct {
	store       *storage.SubnetStatusStorage
	vpcProvider eas.VPCInfoProvider
}

func (r *subnetStatusStorage) New() runtime.Object { return &easv1alpha1.SubnetStatus{} }
func (r *subnetStatusStorage) NewList() runtime.Object {
	return &easv1alpha1.SubnetStatusList{}
}
func (r *subnetStatusStorage) Destroy()                {}
func (r *subnetStatusStorage) NamespaceScoped() bool   { return true }
func (r *subnetStatusStorage) GetSingularName() string { return "subnetstatus" }

func (r *subnetStatusStorage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	namespace, _ := request.NamespaceFrom(ctx)
	return r.store.Get(ctx, namespace, name)
}

func (r *subnetStatusStorage) List(ctx context.Context, _ *metainternalversion.ListOptions) (runtime.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if namespace, ok := request.NamespaceFrom(ctx); ok && namespace != "" {
		return r.store.List(ctx, namespace)
	}
	merged := &easv1alpha1.SubnetStatusList{
		TypeMeta: metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "SubnetStatusList"},
		Items:    make([]easv1alpha1.SubnetStatus, 0),
	}
	for _, namespace := range r.vpcProvider.ListAllVPCNamespaces() {
		result, err := r.store.List(ctx, namespace)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			logger.Log.Warn("Failed to list subnet statuses", "namespace", namespace, "error", err)
			continue
		}
		merged.Items = append(merged.Items, result.Items...)
	}
	return merged, nil
}

func (r *subnetStatusStorage) ConvertToTable(_ context.Context, object runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{ColumnDefinitions: subnetStatusColumns}
	addRow := func(status *easv1alpha1.SubnetStatus) {
		// Namespace is row metadata; only name and gateway summary become cells.
		table.Rows = append(table.Rows, tableRow(status.Name, status.Namespace, truncateCol(strings.Join(status.GatewayAddresses, ","))))
	}
	switch object := object.(type) {
	case *easv1alpha1.SubnetStatus:
		addRow(object)
	case *easv1alpha1.SubnetStatusList:
		for i := range object.Items {
			addRow(&object.Items[i])
		}
	default:
		return nil, fmt.Errorf("unsupported type %T for SubnetStatus table", object)
	}
	return table, nil
}
