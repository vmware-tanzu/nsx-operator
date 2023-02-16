package vpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	vpcName1          = "ns1-vpc-1"
	vpcName2          = "ns1-vpc-2"
	vpcID1            = "ns-vpc-uid-1"
	vpcID2            = "ns-vpc-uid-2"
	IPv4Type          = "IPv4"
	cluster           = "k8scl-one"
	tagValueNS        = "ns1"
	tagScopeVPCCRName = common.TagScopeVPCCRName
	tagScopeVPCCRUID  = common.TagScopeVPCCRUID
	tagValueVPCCRName = "vpcA"
	tagValueVPCCRUID  = "uidA"
	tagScopeCluster   = common.TagScopeCluster
	tagScopeNamespace = common.TagScopeNamespace

	basicTags = []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &tagValueNS,
		},
		{
			Scope: &tagScopeVPCCRName,
			Tag:   &tagValueVPCCRName,
		},
		{
			Scope: &tagScopeVPCCRUID,
			Tag:   &tagValueVPCCRUID,
		},
	}
)

func TestVPC_GetVPCsByNamespace(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeVPCCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &VPCStore{ResourceStore: resourceStore}
	service := &VPCService{
		Service: common.Service{NSXClient: nil},
	}
	service.vpcStore = vpcStore
	type args struct {
		i interface{}
		j interface{}
	}
	ns1 := "test-ns-1"
	tag1 := []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &ns1,
		},
		{
			Scope: &tagScopeVPCCRName,
			Tag:   &tagValueVPCCRName,
		},
		{
			Scope: &tagScopeVPCCRUID,
			Tag:   &tagValueVPCCRUID,
		},
	}
	ns2 := "test-ns-2"
	tag2 := []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &ns2,
		},
		{
			Scope: &tagScopeVPCCRName,
			Tag:   &tagValueVPCCRName,
		},
		{
			Scope: &tagScopeVPCCRUID,
			Tag:   &tagValueVPCCRUID,
		},
	}
	vpc1 := model.Vpc{

		DisplayName:       &vpcName1,
		Id:                &vpcID1,
		Tags:              tag1,
		IpAddressType:     &IPv4Type,
		PrivateIpv4Blocks: []string{"1.1.1.0/24"},
		PublicIpv4Blocks:  []string{"2.2.2.0/24"},
	}
	vpc2 := model.Vpc{

		DisplayName:       &vpcName2,
		Id:                &vpcID2,
		Tags:              tag2,
		IpAddressType:     &IPv4Type,
		PrivateIpv4Blocks: []string{"3.3.3.0/24"},
		PublicIpv4Blocks:  []string{"4.4.4.0/24"},
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: vpc1, j: vpc2}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vpcStore.Operate(&vpc1)
			vpcStore.Operate(&vpc2)
			got := vpcStore.List()
			if len(got) != 2 {
				t.Errorf("size = %v, want %v", len(got), 2)
			}
			vpc_list_1 := service.GetVPCsByNamespace("invalid")
			if len(vpc_list_1) != 0 {
				t.Errorf("size = %v, want %v", len(vpc_list_1), 0)
			}
			vpc_list_2 := service.GetVPCsByNamespace(ns2)
			if len(vpc_list_2) != 1 && *vpc_list_2[0].DisplayName != vpcName2 {
				t.Errorf("size = %v, want %v, display = %s, want %s", len(vpc_list_2), 1, *vpc_list_2[0].DisplayName, vpcName2)
			}
		})
	}
}
