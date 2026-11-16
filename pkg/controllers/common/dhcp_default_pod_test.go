package common

import (
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/mock"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestDefaultPodAllocationOptOut(t *testing.T) {
	for _, tc := range []struct {
		name     string
		advanced *model.SubnetAdvancedConfig
		want     v1alpha1.StaticIPAllocationType
	}{
		{"omitted advanced config", nil, v1alpha1.StaticIPAllocationTypeIPv4},
		{"omitted allocation", &model.SubnetAdvancedConfig{}, v1alpha1.StaticIPAllocationTypeIPv4},
		{"omitted enabled", &model.SubnetAdvancedConfig{StaticIpAllocation: &model.StaticIpAllocation{}}, v1alpha1.StaticIPAllocationTypeIPv4},
		{"explicit true", &model.SubnetAdvancedConfig{StaticIpAllocation: &model.StaticIpAllocation{Enabled: ptr.To(true)}}, v1alpha1.StaticIPAllocationTypeIPv4},
		{"explicit false", &model.SubnetAdvancedConfig{StaticIpAllocation: &model.StaticIpAllocation{Enabled: ptr.To(false)}}, v1alpha1.StaticIPAllocationTypeNone},
	} {
		for _, mode := range []string{model.SubnetDhcpConfig_MODE_DEACTIVATED, model.SubnetDhcpConfig_MODE_SERVER, model.SubnetDhcpConfig_MODE_RELAY} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				subnet := &model.VpcSubnet{AdvancedConfig: tc.advanced, SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: ptr.To(mode)}}
				require.Equal(t, tc.want, ResolveEffectiveStaticIPAllocationType("", subnet, v1alpha1.IPAddressTypeIPv4, true))
				// Explicit port requests, including reservation transfer targets, keep precedence.
				require.Equal(t, v1alpha1.StaticIPAllocationTypeIPv4, ResolveEffectiveStaticIPAllocationType(v1alpha1.StaticIPAllocationTypeIPv4, subnet, v1alpha1.IPAddressTypeIPv4, true))
				// VM and legacy callers retain the pre-existing default, including mixed mode.
				if mode != model.SubnetDhcpConfig_MODE_DEACTIVATED {
					require.Equal(t, v1alpha1.StaticIPAllocationTypeNone, ResolveEffectiveStaticIPAllocationType("", subnet, v1alpha1.IPAddressTypeIPv4))
				}
			})
		}
	}
}

func TestDefaultPodAllocationUsesSelectedSubnet(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	ss := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "default-pod", Namespace: "ns", UID: "ss-uid",
		Labels: map[string]string{servicecommon.LabelDefaultNetwork: servicecommon.DefaultPodNetwork}},
		Spec: v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"removed", "target"}}}
	removed := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "removed", Namespace: "ns"}}
	target := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "ns"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ss, removed, target).Build()
	oldSubnet := &model.VpcSubnet{Path: ptr.To("/orgs/default/projects/default/vpcs/vpc/subnets/removed")}
	newSubnet := &model.VpcSubnet{Path: ptr.To("/orgs/default/projects/default/vpcs/vpc/subnets/target"),
		AdvancedConfig: &model.SubnetAdvancedConfig{StaticIpAllocation: &model.StaticIpAllocation{Enabled: ptr.To(false)}}}
	patches := gomonkey.ApplyFunc(GetVpcNetworkConfig, func(servicecommon.VPCServiceProvider, string) (*v1alpha1.VPCNetworkConfiguration, error) {
		return &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{Subnets: []v1alpha1.SharedSubnet{{Path: *newSubnet.Path, PodDefault: true}}}}, nil
	})
	defer patches.Reset()
	subnets, ports := &mock.MockSubnetServiceProvider{}, &mock.MockSubnetPortServiceProvider{}
	subnets.On("GetSubnetByCR", testifymock.MatchedBy(func(s *v1alpha1.Subnet) bool { return s.Name == "removed" })).Return(oldSubnet, nil).Once()
	subnets.On("GetSubnetByCR", testifymock.MatchedBy(func(s *v1alpha1.Subnet) bool { return s.Name == "target" })).Return(newSubnet, nil).Once()
	bindings := []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10", MACAddress: "02:00:00:00:00:10"}}
	ports.On("AllocatePortFromSubnet", newSubnet, false, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, bindings).Return(true, nil).Once()
	path, allocation, uid, lock, err := AllocateSubnetFromSubnetSet(c, c, ss, &mock.MockVPCServiceProvider{}, subnets, ports, v1alpha1.IPAddressTypeIPv4, "", bindings, true)
	if lock != nil {
		defer RUnlockSubnetSet(*uid, lock)
	}
	require.NoError(t, err)
	require.Equal(t, *newSubnet.Path, path)
	require.Equal(t, v1alpha1.StaticIPAllocationTypeNone, allocation)
	subnets.AssertExpectations(t)
	ports.AssertExpectations(t)
}
