package ippool

import (
	"reflect"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha2"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestIPPoolService_BuildIPPool(t *testing.T) {
	ipPoolService := fakeService()

	ipPool := &v1alpha2.IPPool{
		ObjectMeta: v1.ObjectMeta{
			Name: "ippool1",
			UID:  "uuid1",
		},
		Spec: v1alpha2.IPPoolSpec{
			Type: "public",
			Subnets: []v1alpha2.SubnetRequest{
				{
					Name:         "subnet1",
					PrefixLength: 24,
				},
			},
		},
	}

	want := &model.IpAddressPool{
		DisplayName: String("ipc-k8scl-one:test-ippool1"),
		Id:          String("ipc_uuid1"),
		Tags: []model.Tag{
			{Scope: String("nsx-op/cluster"), Tag: String("k8scl-one:test")},
			{Scope: String("nsx-op/version"), Tag: String(strings.Join(common.TagValueVersion, "."))},
			{Scope: String("nsx-op/namespace"), Tag: String("")},
			{Scope: String("nsx-op/ippool_name"), Tag: String("ippool1")},
			{
				Scope: String("nsx-op/ippool_uid"),
				Tag:   String("uuid1"),
			},
			{Scope: String("nsx-op/ippool_type"), Tag: String("public")},
		},
	}

	want2 := model.IpAddressPoolBlockSubnet{
		DisplayName: String("ibs-k8scl-one:test-ippool1-subnet1"),
		Id:          String("ibs_uuid1_subnet1"),
		IpBlockPath: String("/infra/ip-blocks/block-test"),
		Tags: []model.Tag{
			{Scope: String("nsx-op/cluster"), Tag: String("k8scl-one:test")},
			{Scope: String("nsx-op/version"), Tag: String(strings.Join(common.TagValueVersion, "."))},
			{Scope: String("nsx-op/namespace"), Tag: String("")},
			{Scope: String("nsx-op/ippool_name"), Tag: String("ippool1")},
			{Scope: String("nsx-op/ippool_uid"), Tag: String("uuid1")},
			{Scope: String("nsx-op/ipsubnet_name"), Tag: String("subnet1")},
		},
		Size: Int64(256),
	}

	vpcinfolist := []common.VPCResourceInfo{
		{ExternalIPv4Blocks: []string{"/infra/ip-blocks/block-test"}},
	}
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &vpc.VPCStore{ResourceStore: resourceStore}
	ipPoolService.VPCService = &vpc.VPCService{VpcStore: vpcStore}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(ipPoolService.VPCService), "ListVPCInfo", func(vpcService *vpc.VPCService,
		ns string,
	) []common.VPCResourceInfo {
		return vpcinfolist
	})
	defer patch.Reset()

	type args struct {
		IPPool *v1alpha2.IPPool
	}
	tests := []struct {
		name  string
		args  args
		want  *model.IpAddressPool
		want1 []*model.IpAddressPoolBlockSubnet
	}{
		{"test1", args{ipPool}, want, []*model.IpAddressPoolBlockSubnet{&want2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := ipPoolService.BuildIPPool(tt.args.IPPool)
			assert.Equalf(t, tt.want, got, "BuildIPPool(%v)", tt.args.IPPool)
			assert.Equalf(t, tt.want1, got1, "BuildIPPool(%v)", tt.args.IPPool)
		})
	}
}
