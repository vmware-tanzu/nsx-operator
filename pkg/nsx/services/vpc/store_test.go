package vpc

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type fakeQueryClient struct {
}

func (qIface *fakeQueryClient) List(_ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
	cursor := "1"
	resultCount := int64(1)
	return model.SearchResponse{
		Results: []*data.StructValue{data.NewStructValue("",
			map[string]data.DataValue{
				"resource_type":     data.NewStringValue("Vpc"),
				"id":                data.NewStringValue("vpc1"),
				"path":              data.NewStringValue("/orgs/default/projects/default/vpcs/vpc1"),
				"connectivity_path": data.NewStringValue("/orgs/default/projects/project-quality/vpc-connectivity-profiles/default"),
			})},
		Cursor: &cursor, ResultCount: &resultCount,
	}, nil
}

func Test_filterTag(t *testing.T) {
	mTag, mScope := "test_tag", "nsx-op/namespace_uid"
	mTag2, mScope2 := "test_tag", "nsx"
	tags := []model.Tag{{Scope: &mScope, Tag: &mTag}}
	tags2 := []model.Tag{{Scope: &mScope2, Tag: &mTag2}}
	var res []string
	var res2 []string
	type args struct {
		v   []model.Tag
		res []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{"1", args{v: tags, res: res}, []string{"test_tag"}},
		{"1", args{v: tags2, res: res2}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filterTagBy(tt.args.v, common.TagScopeNamespaceUID); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_InitializeVPCStore(t *testing.T) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(config2)
	rc := cluster.NewRestConnector()
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	vpcStore := &VPCStore{ResourceStore: common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}}

	service := VPCService{
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
		VpcStore: vpcStore,
	}

	wg := sync.WaitGroup{}
	fatalErrors := make(chan error)
	wg.Add(3)

	var tc *bindings.TypeConverter
	patches2 := gomonkey.ApplyMethod(reflect.TypeOf(tc), "ConvertToGolang",
		func(_ *bindings.TypeConverter, d data.DataValue, b bindings.BindingType) (interface{}, []error) {
			mId, mTag, mScope := "test_id", "test_tag", "test_scope"
			m := model.Vpc{
				Id:   &mId,
				Tags: []model.Tag{{Tag: &mTag, Scope: &mScope}},
			}
			var j interface{} = m
			return j, nil
		})
	defer patches2.Reset()

	service.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeVpc, nil, vpcStore)
	assert.Empty(t, fatalErrors)
	assert.Equal(t, sets.New[string](), vpcStore.ListIndexFuncValues(common.TagScopeNamespaceUID))
}

func TestVPCStore_CRUDResource(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &VPCStore{ResourceStore: resourceStore}
	type args struct {
		i interface{}
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: &model.Vpc{Id: common.String("1")}}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, vpcStore.Apply(tt.args.i), fmt.Sprintf("CRUDResource(%v)", tt.args.i))
		})
	}
}

func TestVPCStore_CRUDResource_List(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{
		common.TagScopeNamespaceUID: vpcIndexNamespaceIDFunc,
		common.TagScopeNamespace:    vpcIndexNamespaceNameFunc,
	})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &VPCStore{ResourceStore: resourceStore}
	ns1 := "test-ns-1"
	ns1UID := "fakeNamespace1UID"
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
			Scope: &tagScopeNamespaceUID,
			Tag:   &ns1UID,
		},
	}
	ns2 := "test-ns-2"
	ns2UID := "fakeNamespace2UID"
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
			Scope: &tagScopeNamespaceUID,
			Tag:   &ns2UID,
		},
	}
	vpc1 := model.Vpc{
		DisplayName:        &vpcName1,
		Id:                 &vpcID1,
		Tags:               tag1,
		IpAddressType:      &IPv4Type,
		PrivateIps:         []string{"1.1.1.0/24"},
		ExternalIpv4Blocks: []string{"2.2.2.0/24"},
	}
	vpc2 := model.Vpc{
		DisplayName:        &vpcName2,
		Id:                 &vpcID2,
		Tags:               tag2,
		IpAddressType:      &IPv4Type,
		PrivateIps:         []string{"3.3.3.0/24"},
		ExternalIpv4Blocks: []string{"4.4.4.0/24"},
	}
	testCases := []struct {
		name             string
		existingVPC      []*model.Vpc
		expectVPCInStore int
		searchNameKey    string
		searchIDKey      string
		expectResVPC     []string
		expectErrStr     string
	}{
		{
			name:             "GetVPCsByNamespace with invalid name",
			existingVPC:      []*model.Vpc{&vpc1, &vpc2},
			expectVPCInStore: 2,
			searchNameKey:    "invalid",
			expectResVPC:     nil,
		},
		{
			name:             "GetVPCsByNamespace with valid name",
			existingVPC:      []*model.Vpc{&vpc1, &vpc2},
			expectVPCInStore: 2,
			searchNameKey:    ns2,
			expectResVPC:     []string{vpcName2},
		},
		{
			name:             "GetVPCsByNamespaceID with invalid ID",
			existingVPC:      []*model.Vpc{&vpc1, &vpc2},
			expectVPCInStore: 2,
			searchIDKey:      "invalid",
			expectResVPC:     nil,
		},
		{
			name:             "GetVPCsByNamespaceID with valid ID",
			existingVPC:      []*model.Vpc{&vpc1, &vpc2},
			expectVPCInStore: 2,
			searchIDKey:      ns2UID,
			expectResVPC:     []string{vpcName2},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, vpc := range tc.existingVPC {
				err := vpcStore.Apply(vpc)
				assert.NoError(t, err)
			}
			got := vpcStore.List()

			assert.Equal(t, tc.expectVPCInStore, len(got))
			if tc.searchNameKey != "" {
				vpcRes := vpcStore.GetVPCsByNamespaceFromStore(tc.searchNameKey)
				assert.Equal(t, len(tc.expectResVPC), len(vpcRes))
				for _, vpc := range vpcRes {
					assert.Contains(t, tc.expectResVPC, *vpc.DisplayName)
				}
			}
			if tc.searchIDKey != "" {
				vpcRes := vpcStore.GetVPCsByNamespaceIDFromStore(tc.searchIDKey)
				assert.Equal(t, len(tc.expectResVPC), len(vpcRes))
				for _, vpc := range vpcRes {
					assert.Contains(t, tc.expectResVPC, *vpc.DisplayName)
				}
			}
		})
	}
}

func Test_keyFunc(t *testing.T) {
	id := "test_id"
	vpcPath := fmt.Sprintf(common.VPCKey, "fake-org", "fake-project", "fake-vpc-id")
	type args struct {
		obj interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name:    "vpc",
			args:    args{obj: &model.Vpc{Id: &id}},
			want:    id,
			wantErr: assert.NoError,
		},
		{
			name:    "lbs",
			args:    args{obj: &model.LBService{Id: &defaultLBSName, ConnectivityPath: &vpcPath}},
			want:    "fake-vpc-id_default",
			wantErr: assert.NoError,
		},
		{
			name:    "invalid",
			args:    args{obj: &model.AntreaTraceflowConfig{Id: &id}},
			want:    "",
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := keyFunc(tt.args.obj)
			if !tt.wantErr(t, err, fmt.Sprintf("keyFunc(%v)", tt.args.obj)) {
				return
			}
			assert.Equalf(t, tt.want, got, "keyFunc(%v)", tt.args.obj)
		})
	}
}

func TestLBSStore_CRUD(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.LBServiceBindingType(),
	}
	ls := &LBSStore{
		ResourceStore: resourceStore,
	}
	lbs1 := &model.LBService{Id: &defaultLBSName, ConnectivityPath: &vpcID1}
	lbs2 := &model.LBService{Id: &defaultLBSName, ConnectivityPath: &vpcID2}
	require.NoError(t, ls.Apply(lbs1))
	require.Equal(t, 1, len(ls.List()))
	require.True(t, reflect.DeepEqual(lbs1, ls.GetByKey(vpcID1)))
	require.NoError(t, ls.Apply(lbs2))
	require.Equal(t, 2, len(ls.List()))
	lbs2.MarkedForDelete = common.Bool(true)
	require.NoError(t, ls.Apply(lbs2))
	require.Equal(t, 1, len(ls.List()))
	require.Nil(t, ls.GetByKey(vpcID2))
	defer func() {
		require.NotNil(t, recover())
	}()
	ls.Apply(&model.AntreaTraceflowConfig{Id: common.String("invalid")})
}
