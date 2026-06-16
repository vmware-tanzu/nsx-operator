/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestIPAddressAllocationService_AddressBindingRestoreVPC(t *testing.T) {
	tests := []struct {
		name               string
		ip                 string
		restore            bool
		referenced         bool
		missingSubnetPort  bool
		missingVPC         bool
		existingAllocation bool
		wantCreate         bool
		wantError          string
	}{
		{name: "IPv4", ip: "198.51.100.8", restore: true, wantCreate: true},
		{name: "IPv6", ip: "2001:db8::8", restore: true, wantCreate: true},
		{name: "normal mode", ip: "198.51.100.8", missingVPC: true},
		{name: "referenced allocation", ip: "198.51.100.8", restore: true, referenced: true, missingVPC: true},
		{name: "missing SubnetPort", ip: "198.51.100.8", restore: true, missingSubnetPort: true, missingVPC: true},
		{name: "missing saved IP", restore: true, missingVPC: true},
		{name: "missing VPC", ip: "198.51.100.8", restore: true, missingVPC: true, wantError: "no valid org and project for ipaddressallocation"},
		{name: "existing reservation", ip: "198.51.100.8", restore: true, missingVPC: true, existingAllocation: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed, _, api := createIPAddressAllocationService(t)
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			const vpcPath = "/orgs/default/projects/project-1/vpcs/workload-vpc"
			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name: "tenant", UID: "namespace-uid",
				Annotations: map[string]string{common.AnnotationVPCNetworkConfig: "config"},
			}}
			nc := &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "config"},
				Spec: v1alpha1.VPCNetworkConfigurationSpec{
					VPC: vpcPath, LoadBalancerVPC: "/orgs/default/projects/lb-project/vpcs/lb-vpc",
				},
			}
			if tt.missingVPC {
				nc.Spec.VPC = ""
			}
			base := seed.Service
			base.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespace, nc).Build()
			vpcService, err := vpc.InitializeVPC(base)
			require.NoError(t, err)
			service, err := InitializeIPAddressAllocation(base, vpcService, false)
			require.NoError(t, err)

			ab := &v1alpha1.AddressBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: namespace.Name, UID: "binding-uid"},
				Spec:       v1alpha1.AddressBindingSpec{VMName: "vm", InterfaceName: "eth0"},
				Status:     v1alpha1.AddressBindingStatus{IPAddress: tt.ip},
			}
			if tt.referenced {
				ab.Spec.IPAddressAllocationName = "referenced-allocation"
			}
			sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: namespace.Name, UID: "port-uid"}}
			if tt.missingSubnetPort {
				sp = nil
			}
			if tt.existingAllocation {
				require.NoError(t, service.ipAddressAllocationStore.Apply(&model.VpcIpAddressAllocation{
					Id: String("existing"), AllocationIps: String(tt.ip), ParentPath: String(vpcPath),
					Tags: []model.Tag{{Scope: String(common.TagScopeAddressBindingCRUID), Tag: String(string(ab.UID))}},
				}))
			}
			if tt.wantCreate {
				var persisted model.VpcIpAddressAllocation
				api.EXPECT().Patch("default", "project-1", "workload-vpc", gomock.Any(), gomock.Any()).DoAndReturn(
					func(_, _, _, id string, allocation model.VpcIpAddressAllocation) error {
						persisted = allocation
						persisted.ParentPath = String(vpcPath)
						persisted.Path = String(vpcPath + "/ip-address-allocations/" + id)
						return nil
					}).Times(1)
				api.EXPECT().Get("default", "project-1", "workload-vpc", gomock.Any()).DoAndReturn(
					func(_, _, _, _ string) (model.VpcIpAddressAllocation, error) {
						return persisted, nil
					}).Times(1)
			}

			err = service.CreateIPAddressAllocationForAddressBinding(ab, sp, tt.restore)
			if tt.wantError != "" {
				require.EqualError(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			allocation, err := service.GetIPAddressAllocationByOwner(ab)
			require.NoError(t, err)
			if !tt.wantCreate && !tt.existingAllocation {
				require.Nil(t, allocation)
				return
			}
			require.NotNil(t, allocation)
			require.Equal(t, String(tt.ip), allocation.AllocationIps)
			require.Equal(t, String(vpcPath), allocation.ParentPath)
			if tt.wantCreate {
				byPort, err := service.GetIPAddressAllocationByOwner(sp)
				require.NoError(t, err)
				require.Equal(t, allocation, byPort)
				// A retry reuses the restored reservation without another NSX write.
				require.NoError(t, service.CreateIPAddressAllocationForAddressBinding(ab, sp, tt.restore))
			}
		})
	}
}
