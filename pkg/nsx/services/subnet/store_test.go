package subnet

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type fakeQueryClient struct {
}

func (qIface *fakeQueryClient) List(_ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
	cursor := "2"
	resultCount := int64(1)
	return model.SearchResponse{
		Results: []*data.StructValue{data.NewStructValue("",
			map[string]data.DataValue{
				"resource_type": data.NewStringValue("VpcSubnet"),
				"id":            data.NewStringValue("subnet1"),
				"path":          data.NewStringValue("/orgs/default/projects/default/vpcs/vpc2/subnets/subnet2"),
				"parent_path":   data.NewStringValue("/orgs/default/projects/default/vpcs/vpc2"),
			})},
		Cursor: &cursor, ResultCount: &resultCount,
	}, nil
}

func Test_IndexFunc(t *testing.T) {
	id, tag, scope := "test_id", "cr_uid", common.TagScopeSubnetCRUID
	subnet := model.VpcSubnet{
		Id:   &id,
		Tags: []model.Tag{{Tag: &tag, Scope: &scope}},
	}
	t.Run("1", func(t *testing.T) {
		got, _ := subnetIndexFunc(&subnet)
		if !reflect.DeepEqual(got, []string{"cr_uid"}) {
			t.Errorf("subnetCRUIDScopeIndexFunc() = %v, want %v", got, model.Tag{Tag: &tag, Scope: &scope})
		}
	})
}

func Test_KeyFunc(t *testing.T) {
	id := "test_id"
	subnet := model.VpcSubnet{Id: &id}
	t.Run("subnetKeyFunc", func(t *testing.T) {
		got, _ := keyFunc(&subnet)
		if got != "test_id" {
			t.Errorf("keyFunc() = %v, want %v", got, "test_id")
		}
	})
}

func Test_InitializeSubnetStore(t *testing.T) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config2)
	rc := cluster.NewRestConnector()

	subnetCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{
		common.TagScopeSubnetCRUID: subnetIndexFunc,
	})
	service := SubnetService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient:   &fakeQueryClient{},
				RestConnector: rc,
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: "k8scl-one:test",
					},
				},
			},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
		SubnetStore: &SubnetStore{ResourceStore: common.ResourceStore{
			Indexer:     subnetCacheIndexer,
			BindingType: model.VpcSubnetBindingType(),
		}},
	}

	wg := sync.WaitGroup{}
	fatalErrors := make(chan error)
	wg.Add(3)

	var tc *bindings.TypeConverter
	patches2 := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.VpcSubnet{
				Id:   &mId,
				Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			var j interface{} = m
			return j, nil
		})
	defer patches2.Reset()

	service.InitializeResourceStore(&wg, fatalErrors, ResourceTypeSubnet, nil, service.SubnetStore)
}

func TestSubnetStore_Apply(t *testing.T) {
	subnetCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{
		common.TagScopeSubnetCRUID: subnetIndexFunc,
	})
	resourceStore := common.ResourceStore{
		Indexer:     subnetCacheIndexer,
		BindingType: model.SecurityPolicyBindingType(),
	}
	subnetStore := &SubnetStore{ResourceStore: resourceStore}
	type args struct {
		subnetVPC interface{}
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"subnet with id", args{subnetVPC: &model.VpcSubnet{Id: common.String("fake-subnet-id")}}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, subnetStore.Apply(tt.args.subnetVPC), fmt.Sprintf("Apply(%v)", tt.args.subnetVPC))
		})
	}
}
