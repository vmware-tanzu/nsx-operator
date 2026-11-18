/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package rest

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/endpoints/request"
	apirest "k8s.io/apiserver/pkg/registry/rest"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
)

var vpcIPBlockAllocationStateColumns = []metav1.TableColumnDefinition{
	{Name: "Name", Type: "string", Format: "name", Description: "Name of the IPBlock"},
	{Name: "VISIBILITY", Type: "string", Description: "Visibility of the IPBlock"},
	{Name: "IP ADDRESS TYPE", Type: "string", Description: "IP address type of the IPBlock"},
}

var (
	_ apirest.Getter         = (*vpcIPBlockAllocationStateStorage)(nil)
	_ apirest.Scoper         = (*vpcIPBlockAllocationStateStorage)(nil)
	_ apirest.TableConvertor = (*vpcIPBlockAllocationStateStorage)(nil)
)

func NewVPCIPBlockAllocationStateStorage(store *storage.VPCIPBlockAllocationStateStorage) *vpcIPBlockAllocationStateStorage {
	return &vpcIPBlockAllocationStateStorage{store: store}
}

// vpcIPBlockAllocationStateStorage supports Get by IPBlock ID, with a ":"
// prefix for infra IPBlocks.
type vpcIPBlockAllocationStateStorage struct {
	store *storage.VPCIPBlockAllocationStateStorage
}

func (r *vpcIPBlockAllocationStateStorage) New() runtime.Object {
	return &easv1alpha1.VPCIPBlockAllocationState{}
}
func (r *vpcIPBlockAllocationStateStorage) Destroy()              {}
func (r *vpcIPBlockAllocationStateStorage) NamespaceScoped() bool { return true }
func (r *vpcIPBlockAllocationStateStorage) GetSingularName() string {
	return "vpcipblockallocationstate"
}

func (r *vpcIPBlockAllocationStateStorage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	namespace, _ := request.NamespaceFrom(ctx)
	return r.store.Get(ctx, namespace, name)
}

func (r *vpcIPBlockAllocationStateStorage) ConvertToTable(_ context.Context, object runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	state, ok := object.(*easv1alpha1.VPCIPBlockAllocationState)
	if !ok {
		return nil, fmt.Errorf("unsupported type %T for VPCIPBlockAllocationState table", object)
	}
	// Namespace is row metadata, outside the three displayed cells.
	return &metav1.Table{
		ColumnDefinitions: vpcIPBlockAllocationStateColumns,
		Rows: []metav1.TableRow{
			tableRow(state.Name, state.Namespace, string(state.Visibility), string(state.IPAddressType)),
		},
	}, nil
}
