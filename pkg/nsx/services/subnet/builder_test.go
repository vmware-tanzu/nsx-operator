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
		},
		{
			name:             "IPv6 SubnetSet",
			ipAddressType:    v1alpha1.IPAddressTypeIPv6,
			ipv4SubnetSize:   0,
			ipv6PrefixLength: 64,
			dhcpMode:         "",
			dhcpv6Mode:       v1alpha1.DHCPv6ConfigMode(v1alpha1.DHCPv6ConfigModeServer),
			expectIPv4Config: false,
			expectIPv6Config: true,
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
		})
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
