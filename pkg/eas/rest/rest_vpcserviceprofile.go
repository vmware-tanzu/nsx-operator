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

var vpcServiceProfileColumns = []metav1.TableColumnDefinition{
	{Name: "Name", Type: "string", Format: "name", Description: "Name of the resource"},
	{Name: "DEFAULT", Type: "boolean", Description: "Whether this is a default profile"},
	{Name: "SERVICE SUBNET CIDRS", Type: "string", Description: "Service subnet CIDRs"},
}

var (
	_ apirest.Getter         = (*vpcServiceProfileStorage)(nil)
	_ apirest.Lister         = (*vpcServiceProfileStorage)(nil)
	_ apirest.Scoper         = (*vpcServiceProfileStorage)(nil)
	_ apirest.TableConvertor = (*vpcServiceProfileStorage)(nil)
)

func NewVPCServiceProfileStorage(store *storage.VPCServiceProfileStorage, provider eas.VPCInfoProvider) *vpcServiceProfileStorage {
	return &vpcServiceProfileStorage{store: store, vpcProvider: provider}
}

type vpcServiceProfileStorage struct {
	store       *storage.VPCServiceProfileStorage
	vpcProvider eas.VPCInfoProvider
}

func (r *vpcServiceProfileStorage) New() runtime.Object { return &easv1alpha1.VPCServiceProfile{} }
func (r *vpcServiceProfileStorage) NewList() runtime.Object {
	return &easv1alpha1.VPCServiceProfileList{}
}
func (r *vpcServiceProfileStorage) Destroy()                {}
func (r *vpcServiceProfileStorage) NamespaceScoped() bool   { return true }
func (r *vpcServiceProfileStorage) GetSingularName() string { return "vpcserviceprofile" }

func (r *vpcServiceProfileStorage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	namespace, _ := request.NamespaceFrom(ctx)
	return r.store.Get(ctx, namespace, name)
}

func (r *vpcServiceProfileStorage) List(ctx context.Context, _ *metainternalversion.ListOptions) (runtime.Object, error) {
	if namespace, ok := request.NamespaceFrom(ctx); ok && namespace != "" {
		return r.store.List(ctx, namespace)
	}
	merged := &easv1alpha1.VPCServiceProfileList{
		TypeMeta: metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: "VPCServiceProfileList"},
		Items:    make([]easv1alpha1.VPCServiceProfile, 0),
	}
	for _, namespace := range r.vpcProvider.ListAllVPCNamespaces() {
		result, err := r.store.List(ctx, namespace)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			logger.Log.Warn("Failed to list VPC service profiles", "namespace", namespace, "error", err)
			continue
		}
		merged.Items = append(merged.Items, result.Items...)
	}
	return merged, nil
}

func (r *vpcServiceProfileStorage) ConvertToTable(_ context.Context, object runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	table := &metav1.Table{ColumnDefinitions: vpcServiceProfileColumns}
	addRow := func(profile *easv1alpha1.VPCServiceProfile) {
		table.Rows = append(table.Rows, tableRow(profile.Name, profile.Namespace, profile.IsDefault, truncateCol(strings.Join(profile.ServiceSubnetCIDRs, ","))))
	}
	switch object := object.(type) {
	case *easv1alpha1.VPCServiceProfile:
		addRow(object)
	case *easv1alpha1.VPCServiceProfileList:
		for i := range object.Items {
			addRow(&object.Items[i])
		}
	default:
		return nil, fmt.Errorf("unsupported type %T for VPCServiceProfile table", object)
	}
	return table, nil
}
