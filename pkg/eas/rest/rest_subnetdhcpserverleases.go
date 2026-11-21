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

var subnetDHCPServerLeasesColumns = []metav1.TableColumnDefinition{
	{Name: "Name", Type: "string", Format: "name", Description: "Name of the Subnet"},
	{Name: "IPV4 LEASES", Type: "integer", Description: "Number of IPv4 DHCP server leases"},
	{Name: "IPV6 LEASES", Type: "integer", Description: "Number of IPv6 DHCP server leases"},
}

var (
	_ apirest.Getter         = (*subnetDHCPServerLeasesStorage)(nil)
	_ apirest.Scoper         = (*subnetDHCPServerLeasesStorage)(nil)
	_ apirest.TableConvertor = (*subnetDHCPServerLeasesStorage)(nil)
)

func NewSubnetDHCPServerLeasesStorage(store *storage.SubnetDHCPServerLeasesStorage) *subnetDHCPServerLeasesStorage {
	return &subnetDHCPServerLeasesStorage{store: store}
}

// subnetDHCPServerLeasesStorage supports Get by Subnet CR name.
type subnetDHCPServerLeasesStorage struct {
	store *storage.SubnetDHCPServerLeasesStorage
}

func (r *subnetDHCPServerLeasesStorage) New() runtime.Object {
	return &easv1alpha1.SubnetDHCPServerLeases{}
}
func (r *subnetDHCPServerLeasesStorage) Destroy()                {}
func (r *subnetDHCPServerLeasesStorage) NamespaceScoped() bool   { return true }
func (r *subnetDHCPServerLeasesStorage) GetSingularName() string { return "subnetdhcpserverleases" }

func (r *subnetDHCPServerLeasesStorage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	namespace, _ := request.NamespaceFrom(ctx)
	return r.store.Get(ctx, namespace, name)
}

func (r *subnetDHCPServerLeasesStorage) ConvertToTable(_ context.Context, object runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	leases, ok := object.(*easv1alpha1.SubnetDHCPServerLeases)
	if !ok {
		return nil, fmt.Errorf("unsupported type %T for SubnetDHCPServerLeases table", object)
	}
	return &metav1.Table{
		ColumnDefinitions: subnetDHCPServerLeasesColumns,
		Rows: []metav1.TableRow{
			tableRow(leases.Name, leases.Namespace, int64(len(leases.Leases)), int64(len(leases.IPv6Leases))),
		},
	}, nil
}
