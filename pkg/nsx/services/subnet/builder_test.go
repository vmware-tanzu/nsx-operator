package subnet

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mockClient "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
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
	assert.Equal(t, true, *subnet.AdvancedConfig.StaticIpAllocation.Enabled)

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
		},
	}

	subnet, err := service.buildSubnet(subnet1, tags, []string{})
	assert.Nil(t, err)
	assert.Equal(t, "DHCP_DEACTIVATED", *subnet.SubnetDhcpConfig.Mode)
	assert.Equal(t, true, *subnet.AdvancedConfig.StaticIpAllocation.Enabled)
	assert.Equal(t, []string{"10.0.0.0/28"}, subnet.IpAddresses)

	subnet2 := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
				Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
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
}
