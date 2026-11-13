package subnetport

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func staticIPTestService(t *testing.T) (*SubnetPortService, *model.VpcSubnet, *CountInfo) {
	service := createSubnetPortService(t)
	subnet := &model.VpcSubnet{
		Id: common.String("subnet"), Path: common.String("/orgs/default/projects/default/vpcs/vpc/subnets/subnet"),
		IpAddresses:    []string{"192.0.2.0/24"},
		AdvancedConfig: &model.SubnetAdvancedConfig{StaticIpAllocation: &model.StaticIpAllocation{Enabled: common.Bool(true)}},
	}
	info := &CountInfo{totalStaticIP: 4}
	service.SubnetPortStore.PortCountInfo.Store(*subnet.Path, info)
	return service, subnet, info
}

func TestExplicitIPReservationAndRelease(t *testing.T) {
	service, subnet, info := staticIPTestService(t)
	bindings := []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10"}, {IPAddress: "192.0.2.11"}}
	allocate := func(b []v1alpha1.PortAddressBinding) (bool, error) {
		return service.AllocatePortFromSubnet(subnet, false, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeIPv4, b)
	}
	ok, err := allocate(bindings)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 2, info.dirtyStaticCount)
	require.Zero(t, info.dirtyStaticCountIPv6)

	// A partly overlapping request must not reserve its otherwise free address.
	ok, err = allocate([]v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.12"}, {IPAddress: "192.0.2.11"}})
	require.False(t, ok)
	require.ErrorContains(t, err, "already reserved")
	require.False(t, info.requestedIPs.Has("192.0.2.12"))
	require.Equal(t, 2, info.dirtyStaticCount)
	require.Zero(t, info.dirtyStaticCountIPv6)

	// Reconcile defers this release on both success and downstream failures.
	service.ReleasePortInSubnet(*subnet.Path, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeIPv4, bindings)
	require.Empty(t, info.requestedIPs)
	require.Zero(t, info.dirtyStaticCount)
	require.Zero(t, info.dirtyStaticCountIPv6)
	ok, err = allocate(bindings)
	require.NoError(t, err)
	require.True(t, ok, "retry after failure must be able to reserve the same addresses")
}

func TestExplicitIPConflictsAndCapacity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ips      []string
		existing string
		capacity int
		wantErr  string
	}{
		{name: "cached port conflict", ips: []string{"192.0.2.10"}, existing: "192.0.2.10", capacity: 4, wantErr: "already allocated"},
		{name: "duplicate addresses", ips: []string{"192.0.2.10", "192.0.2.10"}, capacity: 4, wantErr: "duplicate requested IP"},
		{name: "invalid address", ips: []string{"bad-ip"}, capacity: 4, wantErr: "invalid requested IP"},
		{name: "capacity exhausted", ips: []string{"192.0.2.10", "192.0.2.11"}, capacity: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, subnet, info := staticIPTestService(t)
			info.totalStaticIP = tc.capacity
			if tc.existing != "" {
				require.NoError(t, service.SubnetPortStore.Apply(&model.VpcSubnetPort{Id: common.String("existing"), ParentPath: subnet.Path, AddressBindings: []model.PortAddressBindingEntry{{IpAddress: &tc.existing}}}))
			}
			var bindings []v1alpha1.PortAddressBinding
			for _, ip := range tc.ips {
				bindings = append(bindings, v1alpha1.PortAddressBinding{IPAddress: ip})
			}
			ok, err := service.AllocatePortFromSubnet(subnet, false, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeIPv4, bindings)
			require.False(t, ok)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
			require.Empty(t, info.requestedIPs)
			require.Zero(t, info.dirtyStaticCount)
			require.Zero(t, info.dirtyStaticCountIPv6)
		})
	}
}

func TestConcurrentExplicitIPReservation(t *testing.T) {
	service, subnet, info := staticIPTestService(t)
	bindings := []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10"}}
	start := make(chan struct{})
	results := make(chan bool, 12)
	var wg sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, _ := service.AllocatePortFromSubnet(subnet, false, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeIPv4, bindings)
			results <- ok
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for ok := range results {
		if ok {
			winners++
		}
	}
	require.Equal(t, 1, winners)
	require.Equal(t, 1, info.dirtyStaticCount)
	service.ReleasePortInSubnet(*subnet.Path, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeIPv4, bindings)
	require.Empty(t, info.requestedIPs)
}

type conflictPortClient struct {
	fakePortClient
	calls int
	fail  bool
}

func (c *conflictPortClient) List(_, _, _, _ string, cursor *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcSubnetPortListResult, error) {
	c.calls++
	if c.fail {
		return model.VpcSubnetPortListResult{}, fmt.Errorf("cannot list shared ports")
	}
	if cursor == nil {
		return model.VpcSubnetPortListResult{Cursor: common.String("next")}, nil
	}
	return model.VpcSubnetPortListResult{Results: []model.VpcSubnetPort{{AddressBindings: []model.PortAddressBindingEntry{{IpAddress: common.String("192.0.2.10")}}}}}, nil
}

func TestSharedSubnetExplicitIPConflict(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("listFailure=%t", fail), func(t *testing.T) {
			service, subnet, info := staticIPTestService(t)
			ports := &conflictPortClient{fail: fail}
			service.NSXClient.PortClient = ports
			ok, err := service.AllocatePortFromSubnet(subnet, true, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeIPv4, []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10"}})
			require.False(t, ok)
			if fail {
				require.ErrorContains(t, err, "cannot list shared ports")
			} else {
				require.ErrorContains(t, err, "already allocated")
				require.Equal(t, 2, ports.calls, "conflict is on the second page")
			}
			require.Empty(t, info.requestedIPs)
			require.Zero(t, info.dirtyStaticCount)
		})
	}
}
