package vpc

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_org_root "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/searchclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	projectPath = fmt.Sprintf("/orgs/default/projects/project-1")
	vpcPath     = fmt.Sprintf("%s/vpcs/vpc-1", projectPath)
	infraVSId   = "infra-lb-vs"
	vpcVSId     = "vpc-lb-vs"
	infraPoolId = "infra-lb-pool"
	vpcPoolId   = "vpc-lb-pool"
	autoVpcID   = "auto-vpc1"
	autoVpcPath = fmt.Sprintf("%s/vpcs/%s", projectPath, autoVpcID)
)

func TestListAutoCreatedVPCPaths(t *testing.T) {
	vpcService := &VPCService{
		VpcStore: &VPCStore{ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
				common.TagScopeNamespaceUID: vpcIndexNamespaceIDFunc,
				common.TagScopeNamespace:    vpcIndexNamespaceNameFunc,
			}),
			BindingType: model.VpcBindingType(),
		}},
	}

	vpc1 := &model.Vpc{
		Id:   common.String(autoVpcID),
		Path: common.String(autoVpcPath),
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeNamespaceUID),
				Tag:   common.String("namespace1-uid"),
			},
			{
				Scope: common.String(common.TagScopeNamespace),
				Tag:   common.String("namespace1"),
			},
			{
				Scope: common.String(common.TagScopeVPCManagedBy),
				Tag:   common.String(common.AutoCreatedVPCTagValue),
			},
		},
	}
	err := vpcService.VpcStore.Add(vpc1)
	require.NoError(t, err)
	vpcPathSet := vpcService.ListAutoCreatedVPCPaths()
	require.Equal(t, 1, vpcPathSet.Len())
	assert.Equal(t, *vpc1.Path, vpcPathSet.UnsortedList()[0])
}

func TestGetStaleSLBVirtualServers(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueryClient := mocks.NewMockQueryClient(ctrl)
	vpcService := &VPCService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient: mockQueryClient,
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: "k8scl-one:test",
					},
				},
			},
		},
		VpcStore: &VPCStore{},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "ListAutoCreatedVPCPaths", func(*VPCService) sets.Set[string] {
		return sets.New[string](autoVpcPath)
	})
	defer patches.Reset()

	results := make([]*data.StructValue, 0)
	for _, vs := range prepareLBVirtualServers() {
		vsData, errs := NewConverter().ConvertToVapi(vs, model.LBVirtualServerBindingType())
		require.Equal(t, 0, len(errs))
		results = append(results, vsData.(*data.StructValue))
	}

	// Test with happy path.
	expQueryParam := "resource_type:LBVirtualServer AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:SLB"
	resultCount := int64(len(results))
	cursor := fmt.Sprintf("%d", resultCount)
	expResponse := model.SearchResponse{
		Results:     results,
		Cursor:      &cursor,
		ResultCount: &resultCount,
	}
	for _, tc := range []struct {
		name      string
		nsxErr    error
		expErrStr string
	}{
		{
			name:   "success to search on NSX",
			nsxErr: nil,
		}, {
			name:      "failed to query on NSX",
			nsxErr:    fmt.Errorf("connection issue"),
			expErrStr: "connection issue",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockQueryClient.EXPECT().List(expQueryParam, gomock.Any(), nil, gomock.Any(), nil, nil).Return(expResponse, tc.nsxErr)
			vss, err := vpcService.getStaleSLBVirtualServers()
			if tc.nsxErr != nil {
				require.EqualError(t, err, tc.expErrStr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, len(vss))
				vs := vss[0]
				assert.Equal(t, vpcVSId, *vs.Id)
			}
		})
	}

}

func TestCleanupBeforeVPCDeletion(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockOrgClient := mock_org_root.NewMockOrgRootClient(ctrl)
	vpcService := &VPCService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				OrgRootClient: mockOrgClient,
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: "k8scl-one:test",
					},
				},
			},
		},
		VpcStore: &VPCStore{},
	}

	validCtx := context.Background()
	canceledCtx, cancelFn := context.WithCancel(validCtx)
	cancelFn()

	for _, tc := range []struct {
		name      string
		ctx       context.Context
		mockFn    func(s *VPCService) *gomonkey.Patches
		expErrStr string
	}{
		{
			name: "success with no SLB found on NSX",
			ctx:  validCtx,
			mockFn: func(s *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService), "getStaleSLBVirtualServers", func(_ *VPCService) ([]*model.LBVirtualServer, error) {
					return nil, nil
				})
				return patches
			},
		}, {
			name: "success to cleanup SLB found on NSX",
			ctx:  validCtx,
			mockFn: func(s *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService), "getStaleSLBVirtualServers", func(_ *VPCService) ([]*model.LBVirtualServer, error) {
					return []*model.LBVirtualServer{
						{
							Id:         common.String(vpcVSId),
							Path:       common.String(fmt.Sprintf("%s/vpc-lb-virtual-servers/%s", vpcPath, vpcVSId)),
							ParentPath: common.String(vpcPath),
						},
					}, nil
				})
				mockOrgClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil)
				return patches
			},
		}, {
			name: "failed to query SLB on NSX",
			ctx:  validCtx,
			mockFn: func(s *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService), "getStaleSLBVirtualServers", func(_ *VPCService) ([]*model.LBVirtualServer, error) {
					return nil, fmt.Errorf("failed to query SLB")
				})
				return patches
			},
			expErrStr: "failed to query SLB",
		}, {
			name: "failed to clean up SLB found on NSX",
			ctx:  validCtx,
			mockFn: func(s *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService), "getStaleSLBVirtualServers", func(_ *VPCService) ([]*model.LBVirtualServer, error) {
					return []*model.LBVirtualServer{
						{
							Id:         common.String(vpcVSId),
							Path:       common.String(fmt.Sprintf("%s/vpc-lb-virtual-servers/%s", vpcPath, vpcVSId)),
							ParentPath: common.String(vpcPath),
						},
					}, nil
				})
				mockOrgClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(fmt.Errorf("issues to clean up resources"))
				return patches
			},
			expErrStr: "issues to clean up resources",
		}, {
			name: "failed to clean up SLB with canceled context",
			ctx:  canceledCtx,
			mockFn: func(s *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService), "getStaleSLBVirtualServers", func(_ *VPCService) ([]*model.LBVirtualServer, error) {
					return []*model.LBVirtualServer{
						{
							Id:         common.String(vpcVSId),
							Path:       common.String(fmt.Sprintf("%s/vpc-lb-virtual-servers/%s", vpcPath, vpcVSId)),
							ParentPath: common.String(vpcPath),
						},
					}, nil
				})
				return patches
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patches := tc.mockFn(vpcService)
			defer patches.Reset()

			err := vpcService.CleanupBeforeVPCDeletion(tc.ctx)
			if tc.expErrStr == "" {
				require.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expErrStr)
			}
		})
	}

}

func TestGetStaleSLBPools(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueryClient := mocks.NewMockQueryClient(ctrl)
	vpcService := &VPCService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient: mockQueryClient,
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: "k8scl-one:test",
					},
				},
			},
		},
		VpcStore: &VPCStore{},
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(vpcService), "ListAutoCreatedVPCPaths", func(*VPCService) sets.Set[string] {
		return sets.New[string](autoVpcPath)
	})
	defer patches.Reset()

	results := make([]*data.StructValue, 0)
	for _, vs := range prepareLBPools() {
		lbData, errs := NewConverter().ConvertToVapi(vs, model.LBPoolBindingType())
		require.Equal(t, 0, len(errs))
		results = append(results, lbData.(*data.StructValue))
	}

	// Test with happy path.
	expQueryParam := "resource_type:LBPool AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:SLB"
	resultCount := int64(len(results))
	cursor := fmt.Sprintf("%d", resultCount)
	expResponse := model.SearchResponse{
		Results:     results,
		Cursor:      &cursor,
		ResultCount: &resultCount,
	}
	for _, tc := range []struct {
		name      string
		nsxErr    error
		expErrStr string
	}{
		{
			name:   "success to search on NSX",
			nsxErr: nil,
		}, {
			name:      "failed to query on NSX",
			nsxErr:    fmt.Errorf("connection issue"),
			expErrStr: "connection issue",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockQueryClient.EXPECT().List(expQueryParam, gomock.Any(), nil, gomock.Any(), nil, nil).Return(expResponse, tc.nsxErr)
			pools, err := vpcService.getStaleSLBPools()
			if tc.nsxErr != nil {
				require.EqualError(t, err, tc.expErrStr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, len(pools))
				pool := pools[0]
				assert.Equal(t, vpcPoolId, *pool.Id)
			}
		})
	}

}

func TestCleanupVPCChildResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockOrgClient := mock_org_root.NewMockOrgRootClient(ctrl)
	vpcService := &VPCService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				OrgRootClient: mockOrgClient,
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: "k8scl-one:test",
					},
				},
			},
		},
		VpcStore: &VPCStore{},
	}

	validCtx := context.Background()
	canceledCtx, cancelFn := context.WithCancel(validCtx)
	cancelFn()

	for _, tc := range []struct {
		name      string
		ctx       context.Context
		mockFn    func(s *VPCService) *gomonkey.Patches
		vpcPath   string
		expErrStr string
	}{
		{
			name:    "success with auto-created VPC",
			ctx:     validCtx,
			vpcPath: autoVpcPath,
			mockFn: func(s *VPCService) *gomonkey.Patches {
				return nil
			},
		},
		{
			name: "success with no SLB found on NSX",
			ctx:  validCtx,
			mockFn: func(s *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService), "getStaleSLBPools", func(_ *VPCService) ([]*model.LBPool, error) {
					return nil, nil
				})
				return patches
			},
		}, {
			name: "success to cleanup SLB found on NSX",
			ctx:  validCtx,
			mockFn: func(s *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService), "getStaleSLBPools", func(_ *VPCService) ([]*model.LBPool, error) {
					return []*model.LBPool{
						{
							Id:         common.String(vpcPoolId),
							Path:       common.String(fmt.Sprintf("%s/vpc-lb-pools/%s", vpcPath, vpcPoolId)),
							ParentPath: common.String(vpcPath),
						},
					}, nil
				})
				mockOrgClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil)
				return patches
			},
		}, {
			name: "failed to query SLB on NSX",
			ctx:  validCtx,
			mockFn: func(s *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService), "getStaleSLBPools", func(_ *VPCService) ([]*model.LBPool, error) {
					return nil, fmt.Errorf("failed to query SLB")
				})
				return patches
			},
			expErrStr: "failed to query SLB",
		}, {
			name: "failed to clean up SLB found on NSX",
			ctx:  validCtx,
			mockFn: func(s *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService), "getStaleSLBPools", func(_ *VPCService) ([]*model.LBPool, error) {
					return []*model.LBPool{
						{
							Id:         common.String(vpcPoolId),
							Path:       common.String(fmt.Sprintf("%s/vpc-lb-pools/%s", vpcPath, vpcPoolId)),
							ParentPath: common.String(vpcPath),
						},
					}, nil
				})
				mockOrgClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(fmt.Errorf("issues to clean up resources"))
				return patches
			},
			expErrStr: "issues to clean up resources",
		}, {
			name: "failed to clean up SLB with canceled context",
			ctx:  canceledCtx,
			mockFn: func(s *VPCService) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(vpcService), "getStaleSLBPools", func(_ *VPCService) ([]*model.LBPool, error) {
					return []*model.LBPool{
						{
							Id:         common.String(vpcPoolId),
							Path:       common.String(fmt.Sprintf("%s/vpc-lb-pools/%s", vpcPath, vpcPoolId)),
							ParentPath: common.String(vpcPath),
						},
					}, nil
				})
				return patches
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patches := tc.mockFn(vpcService)
			if patches != nil {
				defer patches.Reset()
			}

			err := vpcService.CleanupVPCChildResources(tc.ctx, tc.vpcPath)
			if tc.expErrStr == "" {
				require.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expErrStr)
			}
		})
	}

}

func prepareLBVirtualServers() []*model.LBVirtualServer {
	return []*model.LBVirtualServer{
		{
			Id:         common.String(infraVSId),
			Path:       common.String(fmt.Sprintf("/infra/lb-virtual-servers/%s", infraVSId)),
			ParentPath: common.String("/infra"),
		},
		{
			Id:         common.String(vpcVSId),
			Path:       common.String(fmt.Sprintf("%s/vpc-lb-virtual-servers/%s", vpcPath, vpcVSId)),
			ParentPath: common.String(vpcPath),
		},
		{
			Id:         common.String("autovpc-vs"),
			Path:       common.String(fmt.Sprintf("%s/vpc-lb-virtual-servers/autovpc-vs", autoVpcPath)),
			ParentPath: common.String(autoVpcPath),
		},
	}
}

func prepareLBPools() []*model.LBPool {
	return []*model.LBPool{
		{
			Id:         common.String(infraPoolId),
			Path:       common.String(fmt.Sprintf("/infra/lb-pools/%s", infraPoolId)),
			ParentPath: common.String("/infra"),
		},
		{
			Id:         common.String(vpcPoolId),
			Path:       common.String(fmt.Sprintf("%s/vpc-lb-pools/%s", vpcPath, vpcPoolId)),
			ParentPath: common.String(vpcPath),
		},
		{
			Id:         common.String("autovpc-pool"),
			Path:       common.String(fmt.Sprintf("%s/vpc-lb-pools/autovpc-pool", autoVpcPath)),
			ParentPath: common.String(autoVpcPath),
		},
	}
}
