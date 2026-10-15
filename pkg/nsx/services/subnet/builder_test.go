package subnet

import (
	"errors"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	controllerscommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/test/unittests"
)

var (
	fakeVpcPath = "/orgs/default/projects/default/vpcs/vpc-1"
)

func TestBuildSubnetName(t *testing.T) {
	svc := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "cluster1",
				},
			},
		},
		SubnetStore: buildSubnetStore(),
	}
	subnet := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			UID:  "uuid1",
			Name: "subnet1",
		},
	}
	name := svc.BuildSubnetID(subnet)
	expName := "subnet1_huzpt"
	assert.Equal(t, expName, name)
	id := svc.BuildSubnetID(subnet)
	expId := "subnet1_huzpt"
	assert.Equal(t, expId, id)
}

func TestBuildSubnetSetName(t *testing.T) {
	svc := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "b720ee2c-5788-4680-9796-0f93db33d8a9",
				},
			},
		},
		SubnetStore: buildSubnetStore(),
	}
	subnetset := &v1alpha1.SubnetSet{
		ObjectMeta: v1.ObjectMeta{
			UID:  "28e85c0b-21e4-4cab-b1c3-597639dfe752",
			Name: "pod-default",
		},
	}
	index := "0c5d588b"
	id := svc.buildSubnetSetID(subnetset, index)
	expId := "pod-default-0c5d588b_iqha2"
	assert.Equal(t, expId, id)

	expName := "pod-default-0c5d588b_iqha2"
	name := svc.buildSubnetSetName(subnetset, index)
	assert.Equal(t, expName, name)
	assert.True(t, len(name) <= 80)
}

func TestBuildSubnetForSubnetSet(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		SubnetStore: buildSubnetStore(),
	}
	tags := []model.Tag{
		{
			Scope: common.String("nsx-op/namespace"),
			Tag:   common.String("ns-1"),
		},
		{
			Scope: common.String("nsx-op/vm_namespace_uid"),
			Tag:   common.String("34ef6790-0fe5-48ba-812f-048d429751ee"),
		},
	}

	testCases := []struct {
		name             string
		ipAddressType    v1alpha1.IPAddressType
		ipv4SubnetSize   int
		ipv6PrefixLength int
		dhcpMode         v1alpha1.DHCPConfigMode
		dhcpv6Mode       v1alpha1.DHCPv6ConfigMode
		expectIPv4Config bool
		expectIPv6Config bool
		expectStaticIP   bool
	}{
		{
			name:             "IPv4 SubnetSet",
			ipAddressType:    v1alpha1.IPAddressTypeIPv4,
			ipv4SubnetSize:   24,
			ipv6PrefixLength: 0,
			dhcpMode:         "",
			dhcpv6Mode:       "",
			expectIPv4Config: true,
			expectIPv6Config: false,
			expectStaticIP:   true,
		},
		{
			name:             "IPv6 SubnetSet with DHCPv6 disables static allocation",
			ipAddressType:    v1alpha1.IPAddressTypeIPv6,
			ipv4SubnetSize:   0,
			ipv6PrefixLength: 64,
			dhcpMode:         "",
			dhcpv6Mode:       v1alpha1.DHCPv6ConfigMode(v1alpha1.DHCPv6ConfigModeServer),
			expectIPv4Config: false,
			expectIPv6Config: true,
			expectStaticIP:   false,
		},
		{
			name:             "IPv4IPv6 dual stack",
			ipAddressType:    v1alpha1.IPAddressTypeIPv4IPv6,
			ipv4SubnetSize:   24,
			ipv6PrefixLength: 64,
			dhcpMode:         v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
			dhcpv6Mode:       v1alpha1.DHCPv6ConfigMode(v1alpha1.DHCPv6ConfigModeServerStateless),
			expectIPv4Config: true,
			expectIPv6Config: true,
			expectStaticIP:   false,
		},
		{
			name:             "IPv6 SubnetSet without DHCPv6 keeps static allocation",
			ipAddressType:    v1alpha1.IPAddressTypeIPv6,
			ipv4SubnetSize:   0,
			ipv6PrefixLength: 64,
			dhcpMode:         "",
			dhcpv6Mode:       "",
			expectIPv4Config: false,
			expectIPv6Config: true,
			expectStaticIP:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			subnetSet := &v1alpha1.SubnetSet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnetset-1",
					Namespace: "ns-1",
					UID:       types.UID("1828a1d3-4d10-48d2-a8e8-dceb9bd66502"),
				},
				Spec: v1alpha1.SubnetSetSpec{
					IPAddressType:    tc.ipAddressType,
					IPv4SubnetSize:   tc.ipv4SubnetSize,
					IPv6PrefixLength: tc.ipv6PrefixLength,
					SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
						Mode: tc.dhcpMode,
					},
					SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{
						Mode: tc.dhcpv6Mode,
					},
				},
			}

			subnet, err := service.buildSubnet(subnetSet, tags, []string{})
			assert.Nil(t, err)
			assert.NotNil(t, subnet)

			// Verify IP address type is set correctly
			assert.NotNil(t, subnet.IpAddressType)
			expectedIPType := controllerscommon.ConvertCRIPAddressTypeToNSX(tc.ipAddressType)
			assert.Equal(t, expectedIPType, *subnet.IpAddressType)

			// Verify IPv4 subnet size
			if tc.ipv4SubnetSize > 0 {
				assert.NotNil(t, subnet.Ipv4SubnetSize)
				assert.Equal(t, int64(tc.ipv4SubnetSize), *subnet.Ipv4SubnetSize)
			} else {
				assert.Nil(t, subnet.Ipv4SubnetSize)
			}

			// Verify IPv6 prefix length
			if tc.ipv6PrefixLength > 0 {
				assert.NotNil(t, subnet.Ipv6PrefixLength)
				assert.Equal(t, int64(tc.ipv6PrefixLength), *subnet.Ipv6PrefixLength)
			} else {
				assert.Nil(t, subnet.Ipv6PrefixLength)
			}

			// Verify DHCPv4 configuration
			if tc.expectIPv4Config {
				assert.NotNil(t, subnet.SubnetDhcpConfig)
				assert.NotNil(t, subnet.SubnetDhcpConfig.Mode)
			} else {
				assert.Nil(t, subnet.SubnetDhcpConfig)
			}

			// Verify DHCPv6 configuration
			if tc.expectIPv6Config {
				assert.NotNil(t, subnet.SubnetDhcpv6Config)
				assert.NotNil(t, subnet.SubnetDhcpv6Config.Mode)
			} else {
				assert.Nil(t, subnet.SubnetDhcpv6Config)
			}

			// Verify static IP allocation reflects DHCPv4/DHCPv6 state
			assert.NotNil(t, subnet.AdvancedConfig)
			assert.NotNil(t, subnet.AdvancedConfig.StaticIpAllocation)
			assert.NotNil(t, subnet.AdvancedConfig.StaticIpAllocation.Enabled)
			assert.Equal(t, tc.expectStaticIP, *subnet.AdvancedConfig.StaticIpAllocation.Enabled)
		})
	}
}

// TestBuildSubnetStaticIPAllocationParity verifies that, for the default cases
// (no user-specified IP/MAC, so static allocation is DHCP-driven), a SubnetSet
// produces the same StaticIpAllocation.Enabled and DHCP config as an equivalent
// Subnet across IPv4, IPv6, and dual-stack with every DHCP mode combination.
// This is what drives the SubnetPort default allocateAddresses, so parity here
// guarantees parity of SubnetPort behavior between Subnet- and SubnetSet-derived
// subnets.
func TestBuildSubnetStaticIPAllocationParity(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{Cluster: "k8scl-one:test"},
			},
		},
		SubnetStore: buildSubnetStore(),
	}
	tags := []model.Tag{
		{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns-1")},
		{Scope: common.String("nsx-op/vm_namespace_uid"), Tag: common.String("34ef6790-0fe5-48ba-812f-048d429751ee")},
	}

	dhcpModes := []v1alpha1.DHCPConfigMode{"", v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer)}
	dhcpv6Modes := []v1alpha1.DHCPv6ConfigMode{"", v1alpha1.DHCPv6ConfigMode(v1alpha1.DHCPv6ConfigModeServer)}
	ipTypes := []v1alpha1.IPAddressType{
		v1alpha1.IPAddressTypeIPv4,
		v1alpha1.IPAddressTypeIPv6,
		v1alpha1.IPAddressTypeIPv4IPv6,
	}

	for _, ipType := range ipTypes {
		for _, dhcp := range dhcpModes {
			for _, dhcpv6 := range dhcpv6Modes {
				name := fmt.Sprintf("%s_dhcp=%q_dhcpv6=%q", ipType, dhcp, dhcpv6)
				t.Run(name, func(t *testing.T) {
					subnet := &v1alpha1.Subnet{
						ObjectMeta: v1.ObjectMeta{Name: "subnet-1", Namespace: "ns-1", UID: types.UID("1828a1d3-4d10-48d2-a8e8-dceb9bd66502")},
						Spec: v1alpha1.SubnetSpec{
							IPAddressType:      ipType,
							SubnetDHCPConfig:   v1alpha1.SubnetDHCPConfig{Mode: dhcp},
							SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{Mode: dhcpv6},
						},
					}
					subnetSet := &v1alpha1.SubnetSet{
						ObjectMeta: v1.ObjectMeta{Name: "subnetset-1", Namespace: "ns-1", UID: types.UID("1828a1d3-4d10-48d2-a8e8-dceb9bd66502")},
						Spec: v1alpha1.SubnetSetSpec{
							IPAddressType:      ipType,
							SubnetDHCPConfig:   v1alpha1.SubnetDHCPConfig{Mode: dhcp},
							SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{Mode: dhcpv6},
						},
					}

					nsxSubnet, err := service.buildSubnet(subnet, tags, []string{})
					assert.Nil(t, err)
					nsxSubnetSet, err := service.buildSubnet(subnetSet, tags, []string{})
					assert.Nil(t, err)

					// Static IP allocation must match.
					assert.Equal(t,
						*nsxSubnet.AdvancedConfig.StaticIpAllocation.Enabled,
						*nsxSubnetSet.AdvancedConfig.StaticIpAllocation.Enabled,
						"StaticIpAllocation.Enabled must match for %s", name)

					// DHCPv4 presence must match.
					assert.Equal(t, nsxSubnet.SubnetDhcpConfig == nil, nsxSubnetSet.SubnetDhcpConfig == nil,
						"DHCPv4 config presence must match for %s", name)
					// DHCPv6 presence must match.
					assert.Equal(t, nsxSubnet.SubnetDhcpv6Config == nil, nsxSubnetSet.SubnetDhcpv6Config == nil,
						"DHCPv6 config presence must match for %s", name)
				})
			}
		}
	}
}

func TestBuildSubnetForSubnet(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}
	tags := []model.Tag{
		{
			Scope: common.String("nsx-op/namespace"),
			Tag:   common.String("ns-1"),
		},
	}
	subnet1 := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddressType: v1alpha1.IPAddressTypeIPv4,
			IPAddresses:   []string{"10.0.0.0/28"},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
		},
	}

	subnet, err := service.buildSubnet(subnet1, tags, []string{})
	assert.Nil(t, err)
	assert.Equal(t, "DHCP_DEACTIVATED", *subnet.SubnetDhcpConfig.Mode)
	assert.Equal(t, true, *subnet.AdvancedConfig.StaticIpAllocation.Enabled)
	assert.Equal(t, []string{"10.0.0.0/28"}, subnet.IpAddresses)
	assert.Nil(t, subnet.Ipv4SubnetSize)

	subnet2 := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddressType: v1alpha1.IPAddressTypeIPv4,
			IPAddresses:   []string{"10.0.0.0/28"},
			SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
				Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
				DHCPServerAdditionalConfig: v1alpha1.DHCPServerAdditionalConfig{
					ReservedIPRanges: []string{"10.0.0.4-10.0.0.10"},
				},
			},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(false),
				},
			},
		},
		Status: v1alpha1.SubnetStatus{
			NetworkAddresses: []string{"10.0.0.0/28"},
		},
	}

	subnet, err = service.buildSubnet(subnet2, tags, []string{})
	assert.Nil(t, err)
	assert.Equal(t, "DHCP_SERVER", *subnet.SubnetDhcpConfig.Mode)
	assert.Equal(t, false, *subnet.AdvancedConfig.StaticIpAllocation.Enabled)
	assert.Equal(t, []string{"10.0.0.0/28"}, subnet.IpAddresses)

	subnet.ParentPath = String(fakeVpcPath)
	// Validate the re-generation after collision
	err = service.SubnetStore.Add(subnet)
	require.NoError(t, err)
	newSubnet, err := service.buildSubnet(subnet2, tags, []string{})
	assert.Nil(t, err)
	assert.NotEqual(t, *subnet.Id, *newSubnet.Id)
	assert.Equal(t, []string{"10.0.0.4-10.0.0.10"}, subnet.SubnetDhcpConfig.DhcpServerAdditionalConfig.ReservedIpRanges)

	// has both IPAddresses, Ipv4SubnetSize, only IPAddresses used
	subnet1 = &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddressType: v1alpha1.IPAddressTypeIPv4,
			IPAddresses:   []string{"10.0.0.0/28"},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
			IPv4SubnetSize: 28,
		},
	}

	subnet, err = service.buildSubnet(subnet1, tags, []string{})
	assert.Nil(t, err)
	assert.Equal(t, "DHCP_DEACTIVATED", *subnet.SubnetDhcpConfig.Mode)
	assert.Equal(t, true, *subnet.AdvancedConfig.StaticIpAllocation.Enabled)
	assert.Equal(t, []string{"10.0.0.0/28"}, subnet.IpAddresses)
	assert.Equal(t, Int64(28), subnet.Ipv4SubnetSize)
}

func TestBuildSubnetWithConnectivityState(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}
	tags := []model.Tag{
		{
			Scope: common.String("nsx-op/namespace"),
			Tag:   common.String("ns-1"),
		},
	}

	// Test with ConnectivityStateConnected
	subnetConnected := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-connected",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddresses: []string{"10.0.0.0/28"},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				ConnectivityState: v1alpha1.ConnectivityStateConnected,
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
		},
	}
	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()
	subnet, err := service.buildSubnet(subnetConnected, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, subnet.AdvancedConfig.ConnectivityState)
	assert.Equal(t, "CONNECTED", *subnet.AdvancedConfig.ConnectivityState)

	// Test with ConnectivityStateDisconnected
	subnetDisconnected := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-disconnected",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddresses: []string{"10.0.0.0/28"},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				ConnectivityState: v1alpha1.ConnectivityStateDisconnected,
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
		},
	}

	subnet, err = service.buildSubnet(subnetDisconnected, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, subnet.AdvancedConfig.ConnectivityState)
	assert.Equal(t, "DISCONNECTED", *subnet.AdvancedConfig.ConnectivityState)

	// Test with empty ConnectivityState (should not set the field)
	subnetEmpty := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-empty",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddresses: []string{"10.0.0.0/28"},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
		},
	}

	subnet, err = service.buildSubnet(subnetEmpty, tags, []string{})
	assert.Nil(t, err)
	assert.Nil(t, subnet.AdvancedConfig.ConnectivityState)
}

func TestBuildSubnetWithCustomGatewayAddresses(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}
	tags := []model.Tag{
		{
			Scope: common.String("nsx-op/namespace"),
			Tag:   common.String("ns-1"),
		},
	}

	// Test case 2: Subnet without custom gateway addresses - this should work without nil pointer issues
	subnet2 := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-no-gateway",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddresses: []string{"10.0.0.0/28"},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
		},
	}

	nsxSubnet2, err := service.buildSubnet(subnet2, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet2.AdvancedConfig)
	assert.Equal(t, true, *nsxSubnet2.AdvancedConfig.StaticIpAllocation.Enabled)

	// Test case 3: Subnet with empty gateway addresses slice - this should also work
	subnet3 := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-empty-gateway",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddresses: []string{"10.0.0.0/28"},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				GatewayAddresses: []string{},
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
		},
	}

	nsxSubnet3, err := service.buildSubnet(subnet3, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet3.AdvancedConfig)
	assert.Equal(t, true, *nsxSubnet3.AdvancedConfig.StaticIpAllocation.Enabled)
}

func TestBuildSubnetWithCustomDHCPServerAddresses(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}
	tags := []model.Tag{
		{
			Scope: common.String("nsx-op/namespace"),
			Tag:   common.String("ns-1"),
		},
	}

	// Test case 1: DHCPv6 Server active + custom DHCP server addresses (should be set)
	subnet1 := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-dhcpv6-custom",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddresses: []string{"fd00:1234:5678:9abc::/64"},
			SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{
				Mode: v1alpha1.DHCPv6ConfigModeServer,
			},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				DHCPServerAddresses: []string{"fd00:1234:5678:9abc::3"},
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(false),
				},
			},
		},
	}

	nsxSubnet1, err := service.buildSubnet(subnet1, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet1.AdvancedConfig)
	assert.Equal(t, []string{"fd00:1234:5678:9abc::3"}, nsxSubnet1.AdvancedConfig.DhcpServerAddresses)

	// Test case 2: Static IP allocation enabled + custom DHCP server addresses (should NOT be set) - this should work without nil pointer issues
	subnet2 := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-dhcp-static",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddresses: []string{"10.0.0.0/28"},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				DHCPServerAddresses: []string{"10.0.0.3", "10.0.0.4"},
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
		},
	}
	patches.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})

	nsxSubnet2, err := service.buildSubnet(subnet2, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet2.AdvancedConfig)
	assert.Equal(t, true, *nsxSubnet2.AdvancedConfig.StaticIpAllocation.Enabled)

	// Test case 3: Static IP allocation disabled but no custom DHCP server addresses - this should also work
	subnet3 := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-dhcp-no-addresses",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddresses: []string{"10.0.0.0/28"},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(false),
				},
			},
		},
	}

	nsxSubnet3, err := service.buildSubnet(subnet3, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet3.AdvancedConfig)
	assert.Equal(t, false, *nsxSubnet3.AdvancedConfig.StaticIpAllocation.Enabled)

	// Test case 4: Static IP allocation disabled + empty DHCP server addresses slice - this should work too
	subnet4 := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-dhcp-empty-addresses",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddresses: []string{"10.0.0.0/28"},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				DHCPServerAddresses: []string{},
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(false),
				},
			},
		},
	}

	nsxSubnet4, err := service.buildSubnet(subnet4, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet4.AdvancedConfig)
	assert.Equal(t, false, *nsxSubnet4.AdvancedConfig.StaticIpAllocation.Enabled)

	// Test case 5: DHCPv6 Server Stateless active + custom DHCP server addresses (should be set)
	subnet5 := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-dhcpv6-stateless-custom",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddresses: []string{"fd00:1234:5678:9abc::/64"},
			SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{
				Mode: v1alpha1.DHCPv6ConfigModeServerStateless,
			},
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				DHCPServerAddresses: []string{"fd00:1234:5678:9abc::5"},
			},
		},
	}

	nsxSubnet5, err := service.buildSubnet(subnet5, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet5.AdvancedConfig)
	assert.Equal(t, []string{"fd00:1234:5678:9abc::5"}, nsxSubnet5.AdvancedConfig.DhcpServerAddresses)
}

// ─── IPv6 / dual-stack builder tests ────────────────────────────────────────
func newIPv6BuildSubnetService(k8sClient client.Client) *SubnetService {
	return &SubnetService{
		Service: common.Service{
			Client:    k8sClient,
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{Cluster: "k8scl-one:test"},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}
}

func TestBuildSubnetWithIPv6(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) { return false, nil })
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	service := newIPv6BuildSubnetService(k8sClient)
	tags := []model.Tag{{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns-1")}}

	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{Name: "subnet-ipv6", Namespace: "ns-1"},
		Spec: v1alpha1.SubnetSpec{
			IPAddressType:    "IPv6",
			IPv6PrefixLength: 64,
			AccessMode:       v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{Enabled: common.Bool(true)},
			},
		},
	}

	nsxSubnet, err := service.buildSubnet(subnetCR, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet.IpAddressType)
	assert.Equal(t, "IPV6", *nsxSubnet.IpAddressType)
	assert.NotNil(t, nsxSubnet.Ipv6PrefixLength)
	assert.Equal(t, int64(64), *nsxSubnet.Ipv6PrefixLength)
	assert.Nil(t, nsxSubnet.Ipv4SubnetSize)
	assert.NotNil(t, nsxSubnet.SubnetDhcpv6Config)
	assert.NotNil(t, nsxSubnet.SubnetDhcpv6Config.Mode)
	assert.Equal(t, "DHCP_DEACTIVATED", *nsxSubnet.SubnetDhcpv6Config.Mode)
}

func TestBuildSubnetWithDualStack(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) { return false, nil })
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	service := newIPv6BuildSubnetService(k8sClient)
	tags := []model.Tag{{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns-1")}}

	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{Name: "subnet-dual", Namespace: "ns-1"},
		Spec: v1alpha1.SubnetSpec{
			IPAddressType:    "IPv4IPv6",
			IPv4SubnetSize:   64,
			IPv6PrefixLength: 64,
			AccessMode:       v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{Enabled: common.Bool(true)},
			},
		},
	}

	nsxSubnet, err := service.buildSubnet(subnetCR, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet.IpAddressType)
	assert.Equal(t, "IPV4_IPV6", *nsxSubnet.IpAddressType)
	assert.NotNil(t, nsxSubnet.Ipv4SubnetSize)
	assert.Equal(t, int64(64), *nsxSubnet.Ipv4SubnetSize)
	assert.NotNil(t, nsxSubnet.Ipv6PrefixLength)
	assert.Equal(t, int64(64), *nsxSubnet.Ipv6PrefixLength)
}

func TestBuildSubnetIPv4DefaultAndExplicit(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) { return false, nil })
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	service := newIPv6BuildSubnetService(k8sClient)
	tags := []model.Tag{{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns-1")}}

	// When IPAddressType is empty, IpAddressType should not be set (let NSX default to IPV4).
	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{Name: "subnet-ipv4-default", Namespace: "ns-1"},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 32,
			AccessMode:     v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{Enabled: common.Bool(true)},
			},
		},
	}

	nsxSubnet, err := service.buildSubnet(subnetCR, tags, []string{})
	assert.Nil(t, err)
	assert.Nil(t, nsxSubnet.IpAddressType, "IpAddressType should be nil when not specified")
	assert.Nil(t, nsxSubnet.Ipv6PrefixLength)

	// Explicit "IPv4" should be passed to NSX.
	subnetCR.Spec.IPAddressType = "IPv4"
	nsxSubnet, err = service.buildSubnet(subnetCR, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet.IpAddressType)
	assert.Equal(t, "IPV4", *nsxSubnet.IpAddressType)
}

func TestBuildSubnetWithDHCPv6Config(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) { return false, nil })
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	service := newIPv6BuildSubnetService(k8sClient)
	tags := []model.Tag{{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns-1")}}

	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{Name: "subnet-dhcpv6", Namespace: "ns-1"},
		Spec: v1alpha1.SubnetSpec{
			IPAddressType:    "IPv6",
			IPv6PrefixLength: 64,
			AccessMode:       v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{Enabled: common.Bool(false)},
			},
			SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{
				Mode: v1alpha1.DHCPv6ConfigModeServer,
				DHCPv6ServerAdditionalConfig: v1alpha1.DHCPv6ServerAdditionalConfig{
					ReservedIPRanges: []string{"2001:db8::10-2001:db8::20"},
				},
			},
		},
	}

	nsxSubnet, err := service.buildSubnet(subnetCR, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet.IpAddressType)
	assert.Equal(t, "IPV6", *nsxSubnet.IpAddressType)
	assert.NotNil(t, nsxSubnet.SubnetDhcpv6Config)
	assert.NotNil(t, nsxSubnet.SubnetDhcpv6Config.Mode)
	assert.Equal(t, "DHCP_SERVER", *nsxSubnet.SubnetDhcpv6Config.Mode)
	assert.NotNil(t, nsxSubnet.SubnetDhcpv6Config.Dhcpv6ServerAdditionalConfig)
	assert.Equal(t, []string{"2001:db8::10-2001:db8::20"},
		nsxSubnet.SubnetDhcpv6Config.Dhcpv6ServerAdditionalConfig.ReservedIpRanges)

	// No DHCPv6 mode → SubnetDhcpv6Config mode should be DHCP_DEACTIVATED.
	subnetCR.Spec.SubnetDHCPv6Config = v1alpha1.SubnetDHCPv6Config{}
	nsxSubnet, err = service.buildSubnet(subnetCR, tags, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet.SubnetDhcpv6Config)
	assert.NotNil(t, nsxSubnet.SubnetDhcpv6Config.Mode)
	assert.Equal(t, "DHCP_DEACTIVATED", *nsxSubnet.SubnetDhcpv6Config.Mode)
}

func TestBuildSubnetMixedModeIPAM(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) { return false, nil })
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			Client: k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{Cluster: "k8scl-one:test"},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}
	tags := []model.Tag{{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns-1")}}

	t.Run("mixed mode: DHCPServer + Static + PoolRanges", func(t *testing.T) {
		subnet := &v1alpha1.Subnet{
			ObjectMeta: v1.ObjectMeta{Name: "subnet-mixed", Namespace: "ns-1"},
			Spec: v1alpha1.SubnetSpec{
				IPAddresses: []string{"172.26.2.0/28"},
				SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
					Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
				},
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					DHCPServerAddresses: []string{"172.26.2.3"},
					StaticIPAllocation: v1alpha1.StaticIPAllocation{
						Enabled:    common.Bool(true),
						PoolRanges: []string{"172.26.2.2-172.26.2.8", "172.26.2.10"},
					},
				},
			},
		}
		got, err := service.buildSubnet(subnet, tags, []string{})
		assert.NoError(t, err)
		assert.Equal(t, "DHCP_SERVER", *got.SubnetDhcpConfig.Mode)
		assert.Equal(t, true, *got.AdvancedConfig.StaticIpAllocation.Enabled)
		assert.Equal(t, []string{"172.26.2.2-172.26.2.8", "172.26.2.10"}, got.AdvancedConfig.StaticIpAllocation.PoolRanges)
		// Custom DhcpServerAddresses must be forwarded in mixed mode.
		assert.Equal(t, []string{"172.26.2.3"}, got.AdvancedConfig.DhcpServerAddresses)
	})

	t.Run("IPv6 range serialized unchanged", func(t *testing.T) {
		subnet := &v1alpha1.Subnet{
			ObjectMeta: v1.ObjectMeta{Name: "subnet-v6", Namespace: "ns-1"},
			Spec: v1alpha1.SubnetSpec{
				IPAddresses: []string{"2001:db8::/64"},
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					StaticIPAllocation: v1alpha1.StaticIPAllocation{
						Enabled:    common.Bool(true),
						PoolRanges: []string{"2001:db8::1-2001:db8::ff"},
					},
				},
			},
		}
		got, err := service.buildSubnet(subnet, tags, []string{})
		assert.NoError(t, err)
		assert.Equal(t, []string{"2001:db8::1-2001:db8::ff"}, got.AdvancedConfig.StaticIpAllocation.PoolRanges)
	})

	t.Run("no PoolRanges -> nil on NSX model", func(t *testing.T) {
		subnet := &v1alpha1.Subnet{
			ObjectMeta: v1.ObjectMeta{Name: "subnet-none", Namespace: "ns-1"},
			Spec: v1alpha1.SubnetSpec{
				IPAddresses: []string{"10.0.0.0/28"},
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					StaticIPAllocation: v1alpha1.StaticIPAllocation{Enabled: common.Bool(true)},
				},
			},
		}
		got, err := service.buildSubnet(subnet, tags, []string{})
		assert.NoError(t, err)
		assert.Nil(t, got.AdvancedConfig.StaticIpAllocation.PoolRanges)
	})
}

// TestBuildSubnetIPv6StaticIPAllocation verifies that the builder correctly derives
// staticIpAllocation for IPv6-only and dual-stack Subnets based on DHCP config,
// not just IPv4 DHCP state (regression test for the bug where an IPv6-only Subnet
// with DHCPv6 Server got staticIpAllocation=true because only IPv4 DHCP was checked).
func TestBuildSubnetIPv6StaticIPAllocation(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) { return false, nil })
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool { return true })
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{Cluster: "k8scl-one:test"},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}
	tags := []model.Tag{{Scope: common.String("nsx-op/namespace"), Tag: common.String("ns-1")}}

	testCases := []struct {
		name                  string
		ipAddressType         v1alpha1.IPAddressType
		dhcpMode              v1alpha1.DHCPConfigMode
		dhcpv6Mode            v1alpha1.DHCPv6ConfigMode
		explicitStaticEnabled *bool
		wantStaticEnabled     bool
	}{
		{
			name:              "IPv6-only with DHCPv6 Server → staticIpAllocation false",
			ipAddressType:     v1alpha1.IPAddressTypeIPv6,
			dhcpv6Mode:        v1alpha1.DHCPv6ConfigModeServer,
			wantStaticEnabled: false,
		},
		{
			name:              "IPv6-only with DHCPv6 Deactivated → staticIpAllocation true",
			ipAddressType:     v1alpha1.IPAddressTypeIPv6,
			dhcpv6Mode:        v1alpha1.DHCPv6ConfigModeDeactivated,
			wantStaticEnabled: true,
		},
		{
			name:              "IPv6-only with empty DHCPv6 mode (defaults to deactivated) → staticIpAllocation true",
			ipAddressType:     v1alpha1.IPAddressTypeIPv6,
			wantStaticEnabled: true,
		},
		{
			name:              "dual-stack both DHCP active → staticIpAllocation false",
			ipAddressType:     v1alpha1.IPAddressTypeIPv4IPv6,
			dhcpMode:          v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
			dhcpv6Mode:        v1alpha1.DHCPv6ConfigModeServer,
			wantStaticEnabled: false,
		},
		{
			name:              "dual-stack only IPv4 DHCP active → staticIpAllocation false",
			ipAddressType:     v1alpha1.IPAddressTypeIPv4IPv6,
			dhcpMode:          v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
			dhcpv6Mode:        v1alpha1.DHCPv6ConfigModeDeactivated,
			wantStaticEnabled: false,
		},
		{
			name:              "dual-stack only IPv6 DHCP active → staticIpAllocation false",
			ipAddressType:     v1alpha1.IPAddressTypeIPv4IPv6,
			dhcpMode:          v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated),
			dhcpv6Mode:        v1alpha1.DHCPv6ConfigModeServer,
			wantStaticEnabled: false,
		},
		{
			name:              "dual-stack both DHCP deactivated → staticIpAllocation true",
			ipAddressType:     v1alpha1.IPAddressTypeIPv4IPv6,
			dhcpMode:          v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated),
			dhcpv6Mode:        v1alpha1.DHCPv6ConfigModeDeactivated,
			wantStaticEnabled: true,
		},
		{
			name:                  "explicit override takes precedence over computed default",
			ipAddressType:         v1alpha1.IPAddressTypeIPv6,
			dhcpv6Mode:            v1alpha1.DHCPv6ConfigModeServer,
			explicitStaticEnabled: common.Bool(true),
			wantStaticEnabled:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			subnet := &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{Name: "test-subnet", Namespace: "ns-1"},
				Spec: v1alpha1.SubnetSpec{
					IPAddressType: tc.ipAddressType,
					SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
						Mode: tc.dhcpMode,
					},
					SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{
						Mode: tc.dhcpv6Mode,
					},
					AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
						StaticIPAllocation: v1alpha1.StaticIPAllocation{
							Enabled: tc.explicitStaticEnabled,
						},
					},
				},
			}
			got, err := service.buildSubnet(subnet, tags, []string{})
			assert.NoError(t, err)
			assert.NotNil(t, got.AdvancedConfig.StaticIpAllocation.Enabled)
			assert.Equal(t, tc.wantStaticEnabled, *got.AdvancedConfig.StaticIpAllocation.Enabled)
		})
	}
}

func TestBuildSubnetWithExceedTagsLimit(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}

	// Create more than MaxTagsCount (26) tags to trigger the error
	tags := make([]model.Tag, common.MaxTagsCount+1)
	for i := 0; i < common.MaxTagsCount+1; i++ {
		tags[i] = model.Tag{
			Scope: common.String("test-scope"),
			Tag:   common.String("test-tag-" + string(rune('0'+i))),
		}
	}

	subnet := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-exceed-tags",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddresses: []string{"10.0.0.0/28"},
		},
	}
	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()
	nsxSubnet, err := service.buildSubnet(subnet, tags, []string{})
	assert.Nil(t, nsxSubnet)
	assert.NotNil(t, err)

	// Verify that the error is of type ExceedTagsError
	var exceedTagsErr nsxutil.ExceedTagsError
	assert.True(t, errors.As(err, &exceedTagsErr))
	assert.Contains(t, exceedTagsErr.Desc, "tags cannot exceed maximum size 26")
}

func TestBuildSubnetTags(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}

	tags := []model.Tag{
		{
			Scope: common.String("nsx-op/namespace"),
			Tag:   common.String("ns-1"),
		},
	}

	testCases := []struct {
		name      string
		isTepLess bool
		expectTag bool
		expectErr bool
	}{
		{
			name:      "TEP-less mode should add ultra tag",
			isTepLess: true,
			expectTag: true,
			expectErr: false,
		},
		{
			name:      "Non-TEP-less mode should not add ultra tag",
			isTepLess: false,
			expectTag: false,
			expectErr: false,
		},
		{
			name:      "Error when IsNamespaceInTepLessMode fails",
			isTepLess: false,
			expectTag: false,
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
				func(_ client.Client, _ string) (bool, error) {
					if tc.expectErr {
						return false, fmt.Errorf("failed to list NetworkInfo")
					}
					return tc.isTepLess, nil
				})
			patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
				return true
			})
			defer patches.Reset()

			subnet := &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-test",
					Namespace: "ns-1",
				},
			}

			resultTags, err := service.buildSubnetTags(subnet, tags)
			if tc.expectErr {
				assert.NotNil(t, err)
				return
			}
			assert.Nil(t, err)
			assert.NotNil(t, resultTags)

			found := false
			for _, tag := range resultTags {
				if *tag.Scope == common.TagScopeEnable && *tag.Tag == common.TagValueL3InVlanBackedVPCMode {
					found = true
					break
				}
			}
			assert.Equal(t, tc.expectTag, found)
		})
	}
}

func TestBuildSubnetTagsExceedMaxTags(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
	}

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	// Create more than MaxTagsCount (26) tags to trigger the error
	tags := make([]model.Tag, common.MaxTagsCount+1)
	for i := 0; i < common.MaxTagsCount+1; i++ {
		tags[i] = model.Tag{
			Scope: common.String("test-scope"),
			Tag:   common.String("test-tag-" + string(rune('0'+i))),
		}
	}

	subnet := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-exceed-tags",
			Namespace: "ns-1",
		},
	}

	resultTags, err := service.buildSubnetTags(subnet, tags)
	assert.Nil(t, resultTags)
	assert.NotNil(t, err)

	// Verify that the error is of type ExceedTagsError
	var exceedTagsErr nsxutil.ExceedTagsError
	assert.True(t, errors.As(err, &exceedTagsErr))
	assert.Contains(t, exceedTagsErr.Desc, "tags cannot exceed maximum size 26")
}

func TestBuildSubnetForSubnet_IPv6(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}

	tests := []struct {
		name             string
		ipAddressType    v1alpha1.IPAddressType
		dhcpv6Mode       v1alpha1.DHCPv6ConfigMode
		ipv6PrefixLength int
		expectedDHCPv6   bool
	}{
		{
			name:             "IPv6 subnet with DHCPv6 Server",
			ipAddressType:    v1alpha1.IPAddressTypeIPv6,
			dhcpv6Mode:       v1alpha1.DHCPv6ConfigMode(v1alpha1.DHCPv6ConfigModeServer),
			ipv6PrefixLength: 64,
			expectedDHCPv6:   true,
		},
		{
			name:             "IPv4IPv6 dual stack with DHCPv6",
			ipAddressType:    v1alpha1.IPAddressTypeIPv4IPv6,
			dhcpv6Mode:       v1alpha1.DHCPv6ConfigMode(v1alpha1.DHCPv6ConfigModeServerStateless),
			ipv6PrefixLength: 80,
			expectedDHCPv6:   true,
		},
		{
			name:             "IPv6 subnet without DHCPv6 config",
			ipAddressType:    v1alpha1.IPAddressTypeIPv6,
			dhcpv6Mode:       "",
			ipv6PrefixLength: 64,
			expectedDHCPv6:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subnet := &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test-ipv6-subnet",
					Namespace: "ns-1",
				},
				Spec: v1alpha1.SubnetSpec{
					IPAddressType:    tt.ipAddressType,
					IPv6PrefixLength: tt.ipv6PrefixLength,
					IPAddresses:      []string{"fd00:1234:5678:9abc::/64"},
					SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{
						Mode: tt.dhcpv6Mode,
					},
				},
			}

			tags := []model.Tag{}

			// Call buildSubnet to test IPv6 and DHCPv6 configuration handling
			nsxSubnet, err := service.buildSubnet(subnet, tags, []string{})
			assert.NoError(t, err)
			assert.NotNil(t, nsxSubnet)

			// Verify IPv6PrefixLength is set
			if tt.ipv6PrefixLength > 0 {
				assert.NotNil(t, nsxSubnet.Ipv6PrefixLength)
				assert.Equal(t, int64(tt.ipv6PrefixLength), *nsxSubnet.Ipv6PrefixLength)
			}

			// Verify IPAddressType is set
			assert.NotNil(t, nsxSubnet.IpAddressType)
			expectedIPType := controllerscommon.ConvertCRIPAddressTypeToNSX(tt.ipAddressType)
			assert.Equal(t, expectedIPType, *nsxSubnet.IpAddressType)

			// Verify DHCPv6 config if present
			if tt.expectedDHCPv6 && tt.dhcpv6Mode != "" {
				assert.NotNil(t, nsxSubnet.SubnetDhcpv6Config)
				// For DHCPv6 modes, verify the mode is set (not empty)
				assert.NotEmpty(t, *nsxSubnet.SubnetDhcpv6Config.Mode)
			}
		})
	}
}

func TestBuildSubnetWithDescriptionIPBlockNamesAndLabels(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})
	patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			Client:    k8sClient,
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		SubnetStore: &SubnetStore{
			ResourceStore: common.ResourceStore{
				Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetCRUID:    subnetIndexFunc,
					common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
					common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
					common.TagScopeNamespace:      subnetIndexNamespaceFunc,
				}),
				BindingType: model.VpcSubnetBindingType(),
			},
		},
	}

	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-desc-test",
			Namespace: "ns-1",
			Labels: map[string]string{
				"app": "frontend",
				"env": "production",
			},
		},
		Spec: v1alpha1.SubnetSpec{
			IPAddressType: v1alpha1.IPAddressTypeIPv4,
			Description:   "Frontend Subnet for Production",
			IPBlockNames:  []string{":ipblock-1", ":ipblock-2"},
		},
	}

	nsxSubnet, err := service.buildSubnet(subnetCR, []model.Tag{}, []string{})
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnet)
	assert.Equal(t, "Frontend Subnet for Production", *nsxSubnet.Description)
	assert.Equal(t, []string{"/infra/ip-blocks/ipblock-1", "/infra/ip-blocks/ipblock-2"}, nsxSubnet.IpBlocks)

	vpcInfo := &common.VPCResourceInfo{
		OrgID:     "org1",
		ProjectID: "proj1",
		VPCID:     "vpc1",
	}

	subnetCR.Spec.IPBlockNames = []string{"vpc-block-1", "proj-block-1", ":ext-block-1"}
	nsxSubnetWithVPC, err := service.buildSubnet(subnetCR, []model.Tag{}, []string{}, vpcInfo)
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnetWithVPC)
	assert.Equal(t, []string{
		"/orgs/org1/projects/proj1/infra/ip-blocks/vpc-block-1",
		"/orgs/org1/projects/proj1/infra/ip-blocks/proj-block-1",
		"/infra/ip-blocks/ext-block-1",
	}, nsxSubnetWithVPC.IpBlocks)

	// Check translated labels as NSX tags
	tagMap := make(map[string]string)
	for _, tag := range nsxSubnet.Tags {
		if tag.Scope != nil && tag.Tag != nil {
			tagMap[*tag.Scope] = *tag.Tag
		}
	}
	assert.Equal(t, "frontend", tagMap["app"])
	assert.Equal(t, "production", tagMap["env"])

	// Test clearing description
	subnetCR.Spec.Description = ""
	nsxSubnetEmptyDesc, err := service.buildSubnet(subnetCR, []model.Tag{}, []string{}, vpcInfo)
	assert.Nil(t, err)
	assert.NotNil(t, nsxSubnetEmptyDesc)
	assert.NotNil(t, nsxSubnetEmptyDesc.Description)
	assert.Equal(t, "", *nsxSubnetEmptyDesc.Description)

	// Test buildSubnet propagates buildIPBlockPaths error for invalid IPBlock name
	subnetCR.Spec.IPBlockNames = []string{"invalid block with space"}
	_, errInvalidBlock := service.buildSubnet(subnetCR, []model.Tag{}, []string{}, vpcInfo)
	assert.Error(t, errInvalidBlock)
	assert.Contains(t, errInvalidBlock.Error(), "invalid IPBlock name")
}

func TestBuildIPBlockPaths_Branches(t *testing.T) {
	service := &SubnetService{}

	// 1. Empty ipBlockNames
	res, err := service.buildIPBlockPaths([]string{}, nil)
	assert.Nil(t, err)
	assert.Nil(t, res)

	// 2. Path starting with :
	res, err = service.buildIPBlockPaths([]string{":ext-b2"}, nil)
	assert.Nil(t, err)
	assert.Equal(t, []string{"/infra/ip-blocks/ext-b2"}, res)

	// 3. Project-scoped IPBlock
	vpcInfoFull := &common.VPCResourceInfo{OrgID: "org1", ProjectID: "proj1", VPCID: "vpc1"}
	res, err = service.buildIPBlockPaths([]string{"sys-block"}, vpcInfoFull)
	assert.Nil(t, err)
	assert.Equal(t, []string{"/orgs/org1/projects/proj1/infra/ip-blocks/sys-block"}, res)

	// 4. Valid names with surrounding spaces should be trimmed
	res, err = service.buildIPBlockPaths([]string{"  :ext-b2  ", "  sys-block  "}, vpcInfoFull)
	assert.Nil(t, err)
	assert.Equal(t, []string{"/infra/ip-blocks/ext-b2", "/orgs/org1/projects/proj1/infra/ip-blocks/sys-block"}, res)

	// 5. Missing org or project info error
	_, errMissing := service.buildIPBlockPaths([]string{"proj-block"}, nil)
	assert.Error(t, errMissing)
	assert.Contains(t, errMissing.Error(), "org or project info is missing")

	vpcInfoNoOrg := &common.VPCResourceInfo{OrgID: "", ProjectID: "proj1", VPCID: "vpc1"}
	_, errNoOrg := service.buildIPBlockPaths([]string{"proj-block"}, vpcInfoNoOrg)
	assert.Error(t, errNoOrg)
	assert.Contains(t, errNoOrg.Error(), "org or project info is missing")

	vpcInfoNoProj := &common.VPCResourceInfo{OrgID: "org1", ProjectID: "", VPCID: "vpc1"}
	_, errNoProj := service.buildIPBlockPaths([]string{"proj-block"}, vpcInfoNoProj)
	assert.Error(t, errNoProj)
	assert.Contains(t, errNoProj.Error(), "org or project info is missing")

	// 6. Invalid ipBlockName (empty, spaces, tabs, empty infra block ID)
	_, errEmpty := service.buildIPBlockPaths([]string{"   "}, vpcInfoFull)
	assert.Error(t, errEmpty)
	assert.Contains(t, errEmpty.Error(), "invalid IPBlock name")

	_, errSpace := service.buildIPBlockPaths([]string{"invalid block name"}, vpcInfoFull)
	assert.Error(t, errSpace)
	assert.Contains(t, errSpace.Error(), "invalid IPBlock name")

	_, errTab := service.buildIPBlockPaths([]string{"invalid\tblock"}, vpcInfoFull)
	assert.Error(t, errTab)
	assert.Contains(t, errTab.Error(), "invalid IPBlock name")

	_, errNewline := service.buildIPBlockPaths([]string{"invalid\nblock"}, vpcInfoFull)
	assert.Error(t, errNewline)
	assert.Contains(t, errNewline.Error(), "invalid IPBlock name")

	_, errInfraEmpty := service.buildIPBlockPaths([]string{":"}, vpcInfoFull)
	assert.Error(t, errInfraEmpty)
	assert.Contains(t, errInfraEmpty.Error(), "invalid infra IPBlock name")

	_, errInfraSpaces := service.buildIPBlockPaths([]string{":   "}, vpcInfoFull)
	assert.Error(t, errInfraSpaces)
	assert.Contains(t, errInfraSpaces.Error(), "invalid infra IPBlock name")

	_, errMixedInvalid := service.buildIPBlockPaths([]string{":valid-ext", "invalid block"}, vpcInfoFull)
	assert.Error(t, errMixedInvalid)
	assert.Contains(t, errMixedInvalid.Error(), "invalid IPBlock name")
}

func TestBuildSubnet_SubnetCR_IntegrationMatrix(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	fakeVPCInfo := &common.VPCResourceInfo{
		OrgID:     "default-org",
		ProjectID: "default-project",
		VPCID:     "vpc-1",
	}

	testCases := []struct {
		name            string
		obj             client.Object
		inputTags       []model.Tag
		vpcInfo         *common.VPCResourceInfo
		isTepLess       bool
		tepLessErr      error
		nsxVersionIPv6  bool
		expectErr       bool
		expectErrSubstr string
		validate        func(t *testing.T, subnet *model.VpcSubnet)
	}{
		{
			name: "full_featured_ipv4_subnet_cr",
			obj: &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-ipv4-full",
					Namespace: "ns-prod",
					UID:       "uid-ipv4-full-12345",
					Labels: map[string]string{
						"app": "backend",
						"env": "production",
					},
				},
				Spec: v1alpha1.SubnetSpec{
					AccessMode:     v1alpha1.AccessMode(v1alpha1.AccessModeProject),
					IPAddressType:  v1alpha1.IPAddressTypeIPv4,
					IPv4SubnetSize: 64,
					IPAddresses:    []string{"192.168.1.0/26"},
					Description:    "Production IPv4 Subnet with PrivateTGW",
					SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
						Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
						DHCPServerAdditionalConfig: v1alpha1.DHCPServerAdditionalConfig{
							ReservedIPRanges: []string{"192.168.1.10-192.168.1.20"},
						},
					},
					AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
						ConnectivityState: v1alpha1.ConnectivityStateConnected,
						StaticIPAllocation: v1alpha1.StaticIPAllocation{
							Enabled:    common.Bool(true),
							PoolRanges: []string{"192.168.1.30-192.168.1.40"},
						},
						GatewayAddresses:    []string{"192.168.1.1"},
						DHCPServerAddresses: []string{"192.168.1.2"},
					},
					IPBlockNames: []string{":infra-block-v4"},
				},
			},
			inputTags: []model.Tag{
				{Scope: common.String("custom-scope"), Tag: common.String("custom-tag")},
			},
			vpcInfo:        fakeVPCInfo,
			nsxVersionIPv6: true,
			validate: func(t *testing.T, s *model.VpcSubnet) {
				assert.Equal(t, AccessModeProjectInNSX, *s.AccessMode)
				assert.Equal(t, "IPV4", *s.IpAddressType)
				assert.Equal(t, int64(64), *s.Ipv4SubnetSize)
				assert.Equal(t, []string{"192.168.1.0/26"}, s.IpAddresses)
				assert.Equal(t, "Production IPv4 Subnet with PrivateTGW", *s.Description)
				require.NotNil(t, s.SubnetDhcpConfig)
				assert.Equal(t, "DHCP_SERVER", *s.SubnetDhcpConfig.Mode)
				require.NotNil(t, s.SubnetDhcpConfig.DhcpServerAdditionalConfig)
				assert.Equal(t, []string{"192.168.1.10-192.168.1.20"}, s.SubnetDhcpConfig.DhcpServerAdditionalConfig.ReservedIpRanges)
				assert.Nil(t, s.SubnetDhcpv6Config)
				assert.Nil(t, s.Ipv6PrefixLength)
				require.NotNil(t, s.AdvancedConfig)
				assert.Equal(t, "CONNECTED", *s.AdvancedConfig.ConnectivityState)
				require.NotNil(t, s.AdvancedConfig.StaticIpAllocation)
				assert.Equal(t, true, *s.AdvancedConfig.StaticIpAllocation.Enabled)
				assert.Equal(t, []string{"192.168.1.30-192.168.1.40"}, s.AdvancedConfig.StaticIpAllocation.PoolRanges)
				assert.Equal(t, []string{"192.168.1.1"}, s.AdvancedConfig.GatewayAddresses)
				assert.Equal(t, []string{"192.168.1.2"}, s.AdvancedConfig.DhcpServerAddresses)
				assert.Equal(t, []string{"/infra/ip-blocks/infra-block-v4"}, s.IpBlocks)
			},
		},
		{
			name: "ipv6_subnet_cr_stateful_dhcp_with_reserved_ranges",
			obj: &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-ipv6-stateful",
					Namespace: "ns-ipv6",
					UID:       "uid-ipv6-stateful-11223",
				},
				Spec: v1alpha1.SubnetSpec{
					AccessMode:       v1alpha1.AccessMode(v1alpha1.AccessModePublic),
					IPAddressType:    v1alpha1.IPAddressTypeIPv6,
					IPv6PrefixLength: 64,
					IPAddresses:      []string{"2001:db8::/64"},
					Description:      "IPv6 Subnet with stateful DHCPv6",
					SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{
						Mode: v1alpha1.DHCPv6ConfigModeServer,
						DHCPv6ServerAdditionalConfig: v1alpha1.DHCPv6ServerAdditionalConfig{
							ReservedIPRanges: []string{"2001:db8::10-2001:db8::20"},
						},
					},
					AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
						ConnectivityState: v1alpha1.ConnectivityStateConnected,
						StaticIPAllocation: v1alpha1.StaticIPAllocation{
							Enabled: common.Bool(false),
						},
						GatewayAddresses:    []string{"2001:db8::1"},
						DHCPServerAddresses: []string{"2001:db8::2"},
					},
				},
			},
			vpcInfo:        fakeVPCInfo,
			nsxVersionIPv6: true,
			validate: func(t *testing.T, s *model.VpcSubnet) {
				assert.Equal(t, "Public", *s.AccessMode)
				assert.Equal(t, "IPV6", *s.IpAddressType)
				assert.Equal(t, int64(64), *s.Ipv6PrefixLength)
				assert.Nil(t, s.Ipv4SubnetSize)
				assert.Nil(t, s.SubnetDhcpConfig)
				require.NotNil(t, s.SubnetDhcpv6Config)
				assert.Equal(t, "DHCP_SERVER", *s.SubnetDhcpv6Config.Mode)
				require.NotNil(t, s.SubnetDhcpv6Config.Dhcpv6ServerAdditionalConfig)
				assert.Equal(t, []string{"2001:db8::10-2001:db8::20"}, s.SubnetDhcpv6Config.Dhcpv6ServerAdditionalConfig.ReservedIpRanges)
				require.NotNil(t, s.AdvancedConfig)
				assert.Equal(t, "CONNECTED", *s.AdvancedConfig.ConnectivityState)
				require.NotNil(t, s.AdvancedConfig.StaticIpAllocation)
				assert.Equal(t, false, *s.AdvancedConfig.StaticIpAllocation.Enabled)
				assert.Equal(t, []string{"2001:db8::1"}, s.AdvancedConfig.GatewayAddresses)
				assert.Equal(t, []string{"2001:db8::2"}, s.AdvancedConfig.DhcpServerAddresses)
			},
		},
		{
			name: "ipv6_subnet_cr_with_stateless_dhcp_and_whitespace_trimming",
			obj: &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-ipv6-stateless",
					Namespace: "ns-ipv6",
					UID:       "uid-ipv6-stateless-67890",
				},
				Spec: v1alpha1.SubnetSpec{
					AccessMode:       v1alpha1.AccessMode(v1alpha1.AccessModePublic),
					IPAddressType:    v1alpha1.IPAddressTypeIPv6,
					IPv6PrefixLength: 64,
					IPAddresses:      []string{"2001:db8::/64"},
					Description:      "IPv6 Subnet with stateless DHCP",
					SubnetDHCPv6Config: v1alpha1.SubnetDHCPv6Config{
						Mode: v1alpha1.DHCPv6ConfigModeServerStateless,
					},
					AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
						ConnectivityState: v1alpha1.ConnectivityStateDisconnected,
						StaticIPAllocation: v1alpha1.StaticIPAllocation{
							Enabled:    common.Bool(true),
							PoolRanges: []string{"2001:db8::50-2001:db8::60", "2001:db8::100"},
						},
						GatewayAddresses:    []string{"2001:db8::1"},
						DHCPServerAddresses: []string{"2001:db8::5"},
					},
				},
			},
			vpcInfo:        fakeVPCInfo,
			nsxVersionIPv6: true,
			validate: func(t *testing.T, s *model.VpcSubnet) {
				assert.Equal(t, "Public", *s.AccessMode)
				assert.Equal(t, "IPV6", *s.IpAddressType)
				assert.Equal(t, int64(64), *s.Ipv6PrefixLength)
				assert.Nil(t, s.Ipv4SubnetSize)
				assert.Nil(t, s.SubnetDhcpConfig)
				require.NotNil(t, s.SubnetDhcpv6Config)
				assert.Equal(t, "DHCP_SERVER_STATELESS", *s.SubnetDhcpv6Config.Mode)
				assert.Nil(t, s.SubnetDhcpv6Config.Dhcpv6ServerAdditionalConfig)
				require.NotNil(t, s.AdvancedConfig)
				assert.Equal(t, "DISCONNECTED", *s.AdvancedConfig.ConnectivityState)
				require.NotNil(t, s.AdvancedConfig.StaticIpAllocation)
				assert.Equal(t, true, *s.AdvancedConfig.StaticIpAllocation.Enabled)
				assert.Equal(t, []string{"2001:db8::50-2001:db8::60", "2001:db8::100"}, s.AdvancedConfig.StaticIpAllocation.PoolRanges)
				assert.Equal(t, []string{"2001:db8::1"}, s.AdvancedConfig.GatewayAddresses)
				assert.Equal(t, []string{"2001:db8::5"}, s.AdvancedConfig.DhcpServerAddresses)
			},
		},
		{
			name: "pure_static_allocation_ipv4_subnet_cr",
			obj: &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-ipv4-static-only",
					Namespace: "ns-static",
					UID:       "uid-ipv4-static-88888",
				},
				Spec: v1alpha1.SubnetSpec{
					AccessMode:    v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
					IPAddressType: v1alpha1.IPAddressTypeIPv4,
					IPAddresses:   []string{"10.100.0.0/27"},
					SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
						Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated),
					},
					AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
						ConnectivityState: v1alpha1.ConnectivityStateConnected,
						StaticIPAllocation: v1alpha1.StaticIPAllocation{
							Enabled: common.Bool(true),
						},
					},
				},
			},
			vpcInfo:        fakeVPCInfo,
			nsxVersionIPv6: true,
			validate: func(t *testing.T, s *model.VpcSubnet) {
				assert.Equal(t, "Private", *s.AccessMode)
				assert.Equal(t, "IPV4", *s.IpAddressType)
				assert.Equal(t, []string{"10.100.0.0/27"}, s.IpAddresses)
				require.NotNil(t, s.SubnetDhcpConfig)
				assert.Equal(t, "DHCP_DEACTIVATED", *s.SubnetDhcpConfig.Mode)
				require.NotNil(t, s.AdvancedConfig)
				assert.Equal(t, "CONNECTED", *s.AdvancedConfig.ConnectivityState)
				require.NotNil(t, s.AdvancedConfig.StaticIpAllocation)
				assert.Equal(t, true, *s.AdvancedConfig.StaticIpAllocation.Enabled)
			},
		},
		{
			name: "dual_stack_with_status_network_addresses_fallback_and_tepless",
			obj: &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-dual-tepless",
					Namespace: "ns-dual",
					UID:       "uid-dual-tepless-99999",
				},
				Spec: v1alpha1.SubnetSpec{
					AccessMode:       v1alpha1.AccessMode(v1alpha1.AccessModePublic),
					IPAddressType:    v1alpha1.IPAddressTypeIPv4IPv6,
					IPv4SubnetSize:   32,
					IPv6PrefixLength: 64,
					IPAddresses:      nil,
				},
				Status: v1alpha1.SubnetStatus{
					NetworkAddresses: []string{"10.244.0.0/27", "fd00:10:244::/64"},
				},
			},
			vpcInfo:        fakeVPCInfo,
			isTepLess:      true,
			nsxVersionIPv6: true,
			validate: func(t *testing.T, s *model.VpcSubnet) {
				assert.Equal(t, "Public", *s.AccessMode)
				assert.Equal(t, "IPV4_IPV6", *s.IpAddressType)
				assert.Equal(t, int64(32), *s.Ipv4SubnetSize)
				assert.Equal(t, int64(64), *s.Ipv6PrefixLength)
				assert.Equal(t, []string{"10.244.0.0/27", "fd00:10:244::/64"}, s.IpAddresses)
				require.NotNil(t, s.SubnetDhcpConfig)
				assert.Equal(t, "DHCP_DEACTIVATED", *s.SubnetDhcpConfig.Mode)
				require.NotNil(t, s.SubnetDhcpv6Config)
				assert.Equal(t, "DHCP_DEACTIVATED", *s.SubnetDhcpv6Config.Mode)
				require.NotNil(t, s.AdvancedConfig)
				require.NotNil(t, s.AdvancedConfig.StaticIpAllocation)
				assert.Equal(t, true, *s.AdvancedConfig.StaticIpAllocation.Enabled)
				var foundTepLessTag bool
				for _, tag := range s.Tags {
					if tag.Scope != nil && *tag.Scope == common.TagScopeEnable &&
						tag.Tag != nil && *tag.Tag == common.TagValueL3InVlanBackedVPCMode {
						foundTepLessTag = true
						break
					}
				}
				assert.True(t, foundTepLessTag, "Expected TEP-less tag to be present")
			},
		},
		{
			name: "dual_stack_subnet_with_ipv4_spec_and_static_ip_allocation",
			obj: &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-dual-spec-v4",
					Namespace: "ns-dual",
					UID:       "uid-dual-spec-v4-123",
				},
				Spec: v1alpha1.SubnetSpec{
					AccessMode:       v1alpha1.AccessMode(v1alpha1.AccessModePublic),
					IPAddressType:    v1alpha1.IPAddressTypeIPv4IPv6,
					IPv4SubnetSize:   32,
					IPv6PrefixLength: 64,
					IPAddresses:      []string{"10.244.0.0/27"},
					AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
						GatewayAddresses: []string{"10.244.0.1"},
						StaticIPAllocation: v1alpha1.StaticIPAllocation{
							Enabled: common.Bool(true),
						},
					},
				},
				Status: v1alpha1.SubnetStatus{
					NetworkAddresses: []string{"fd00:10:244::/64"},
					GatewayAddresses: []string{"fd00:10:244::1"},
				},
			},
			vpcInfo:        fakeVPCInfo,
			nsxVersionIPv6: true,
			validate: func(t *testing.T, s *model.VpcSubnet) {
				assert.Equal(t, "Public", *s.AccessMode)
				assert.Equal(t, "IPV4_IPV6", *s.IpAddressType)
				assert.Equal(t, int64(32), *s.Ipv4SubnetSize)
				assert.Equal(t, int64(64), *s.Ipv6PrefixLength)
				assert.Equal(t, []string{"10.244.0.0/27"}, s.IpAddresses)
				require.NotNil(t, s.AdvancedConfig)
				require.NotNil(t, s.AdvancedConfig.StaticIpAllocation)
				assert.Equal(t, true, *s.AdvancedConfig.StaticIpAllocation.Enabled)
				assert.Equal(t, []string{"10.244.0.1"}, s.AdvancedConfig.GatewayAddresses)
			},
		},
		{
			name: "backward_compatibility_no_ipv6_version_check",
			obj: &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-legacy",
					Namespace: "ns-legacy",
					UID:       "uid-legacy-11111",
				},
				Spec: v1alpha1.SubnetSpec{
					AccessMode:    v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
					IPAddressType: v1alpha1.IPAddressTypeIPv4,
					IPAddresses:   []string{"10.0.0.0/24"},
				},
			},
			vpcInfo:        fakeVPCInfo,
			nsxVersionIPv6: false,
			validate: func(t *testing.T, s *model.VpcSubnet) {
				assert.Nil(t, s.IpAddressType, "IpAddressType must be nil when NSXCheckVersion(IPv6) is false")
			},
		},
		{
			name: "error_tepless_check_failure",
			obj: &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-err-tepless",
					Namespace: "ns-err",
				},
			},
			tepLessErr:      errors.New("failed to list NetworkInfo"),
			expectErr:       true,
			expectErrSubstr: "failed to list NetworkInfo",
		},
		{
			name: "error_exceed_tags_limit",
			obj: &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-exceed-tags",
					Namespace: "ns-tags",
					Labels: map[string]string{
						"k01": "v", "k02": "v", "k03": "v", "k04": "v", "k05": "v",
						"k06": "v", "k07": "v", "k08": "v", "k09": "v", "k10": "v",
						"k11": "v", "k12": "v", "k13": "v", "k14": "v", "k15": "v",
						"k16": "v", "k17": "v", "k18": "v", "k19": "v", "k20": "v",
						"k21": "v", "k22": "v", "k23": "v", "k24": "v", "k25": "v",
					},
				},
			},
			expectErr:       true,
			expectErrSubstr: "tags cannot exceed maximum size 26",
		},
		{
			name: "error_invalid_ipblock_name",
			obj: &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-invalid-ipblock",
					Namespace: "ns-ipblock",
				},
				Spec: v1alpha1.SubnetSpec{
					IPBlockNames: []string{":valid-infra", "invalid space name"},
				},
			},
			vpcInfo:         fakeVPCInfo,
			expectErr:       true,
			expectErrSubstr: "invalid IPBlock name",
		},
		{
			name: "error_missing_vpcinfo_for_project_ipblock",
			obj: &v1alpha1.Subnet{
				ObjectMeta: v1.ObjectMeta{
					Name:      "subnet-missing-vpcinfo",
					Namespace: "ns-ipblock",
				},
				Spec: v1alpha1.SubnetSpec{
					IPBlockNames: []string{"project-block-only"},
				},
			},
			vpcInfo:         nil,
			expectErr:       true,
			expectErrSubstr: "org or project info is missing",
		},
		{
			name: "error_unsupported_object_type",
			obj: &v1alpha1.SubnetPort{
				ObjectMeta: v1.ObjectMeta{
					Name:      "port-unsupported",
					Namespace: "ns-port",
				},
			},
			expectErr:       true,
			expectErrSubstr: "unsupported type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
				func(_ client.Client, _ string) (bool, error) {
					return tc.isTepLess, tc.tepLessErr
				})
			patches.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
				return tc.nsxVersionIPv6
			})
			defer patches.Reset()

			service := &SubnetService{
				Service: common.Service{
					Client:    k8sClient,
					NSXClient: &nsx.Client{},
					NSXConfig: &config.NSXOperatorConfig{
						CoeConfig: &config.CoeConfig{
							Cluster: "k8scl-one:test",
						},
					},
				},
				SubnetStore: &SubnetStore{
					ResourceStore: common.ResourceStore{
						Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
							common.TagScopeSubnetCRUID:    subnetIndexFunc,
							common.TagScopeSubnetSetCRUID: subnetSetIndexFunc,
							common.TagScopeVMNamespace:    subnetIndexVMNamespaceFunc,
							common.TagScopeNamespace:      subnetIndexNamespaceFunc,
						}),
						BindingType: model.VpcSubnetBindingType(),
					},
				},
			}

			guard := unittests.NewGuard(t, unittests.Metadata{
				Feature:     "subnet",
				Component:   "subnet_builder",
				Scenario:    "build_subnet_cr_integration",
				Description: fmt.Sprintf("Verify buildSubnet under scenario: %s", tc.name),
			})

			var subnet *model.VpcSubnet
			var err error
			if tc.vpcInfo != nil {
				subnet, err = service.buildSubnet(tc.obj, tc.inputTags, []string{}, tc.vpcInfo)
			} else {
				subnet, err = service.buildSubnet(tc.obj, tc.inputTags, []string{})
			}

			if tc.expectErr {
				require.Error(t, err)
				if tc.expectErrSubstr != "" {
					assert.Contains(t, err.Error(), tc.expectErrSubstr)
				}
				guard.AssertOutput(err.Error())
			} else {
				require.NoError(t, err)
				require.NotNil(t, subnet)
				if tc.validate != nil {
					tc.validate(t, subnet)
				}
				guard.AssertOutput(subnet)
			}
		})
	}
}
