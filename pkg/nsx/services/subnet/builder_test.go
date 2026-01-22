package subnet

import (
	"errors"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	controllerscommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	fakeVpcPath = "/orgs/default/projects/default/vpcs/vpc-1"
)

func TestBuildSubnetName(t *testing.T) {
	svc := &SubnetService{
		Service: common.Service{
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
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			Client: k8sClient,
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

	subnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnetset-1",
			Namespace: "ns-1",
			UID:       types.UID("1828a1d3-4d10-48d2-a8e8-dceb9bd66502"),
		},
	}

	subnet, err := service.buildSubnet(subnetSet, tags, []string{})
	assert.Nil(t, err)
	assert.Equal(t, "DHCP_DEACTIVATED", *subnet.SubnetDhcpConfig.Mode)
	if subnet.AdvancedConfig != nil {
		assert.Equal(t, true, *subnet.AdvancedConfig.StaticIpAllocation.Enabled)
	}

	subnet.ParentPath = String(fakeVpcPath)
	err = service.SubnetStore.Add(subnet)
	require.NoError(t, err)
	newIdx := "abcdef01"
	newId := service.buildSubnetSetID(subnetSet, newIdx)
	assert.Equal(t, "subnetset-1-abcdef01_5tnj0", newId)
}

func TestBuildSubnetForSubnet(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	patches := gomonkey.ApplyFunc(controllerscommon.IsNamespaceInTepLessMode,
		func(_ client.Client, _ string) (bool, error) {
			return false, nil
		})
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			Client: k8sClient,
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
			IPAddresses: []string{"10.0.0.0/28"},
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
			IPAddresses: []string{"10.0.0.0/28"},
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
			IPAddresses: []string{"10.0.0.0/28"},
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
			Client: k8sClient,
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
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			Client: k8sClient,
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
	defer patches.Reset()

	service := &SubnetService{
		Service: common.Service{
			Client: k8sClient,
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

func TestBuildSubnetWithExceedTagsLimit(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mockClient.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	service := &SubnetService{
		Service: common.Service{
			Client: k8sClient,
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
			Client: k8sClient,
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
			Client: k8sClient,
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
