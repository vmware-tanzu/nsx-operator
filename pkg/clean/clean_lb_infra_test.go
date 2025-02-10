package clean

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/searchclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	lbVS = &model.LBVirtualServer{
		Id:         common.String("infra-vs"),
		Path:       common.String("/infra/lb-virtual-servers/infra-vs"),
		ParentPath: common.String("/infra"),
	}
	lbPool = &model.LBPool{
		Id:         common.String("infra-pool"),
		Path:       common.String("/infra/lb-pools/infra-pool"),
		ParentPath: common.String("/infra"),
	}
	lbService = &model.LBService{
		Id:         common.String("infra-lbs"),
		Path:       common.String("/infra/lb-services/infra-lbs"),
		ParentPath: common.String("/infra"),
	}
	group = &model.Group{
		Id:         common.String("infra-group"),
		Path:       common.String("/infra/domains/default/groups/infra-group"),
		ParentPath: common.String("/infra/domains/default"),
	}
	share = &model.Share{
		Id:         common.String("infra-share"),
		Path:       common.String("/infra/shares/infra-share"),
		ParentPath: common.String("/infra"),
	}
	sharedResource = &model.SharedResource{
		Id:         common.String("infra-sr"),
		Path:       common.String("/infra/shares/infra-share/resources/infra-sr"),
		ParentPath: common.String("/infra/shares/infra-share"),
	}
	cert = &model.TlsCertificate{
		Id:         common.String("cert"),
		Path:       common.String("/infra/certificates/cert"),
		ParentPath: common.String("/infra"),
	}
	appProfile = &model.LBHttpProfile{
		Id:           common.String("default-http-lb-app-profile"),
		Path:         common.String("/infra/lb-app-profiles/default-http-lb-app-profile"),
		ParentPath:   common.String("/infra"),
		ResourceType: common.ResourceTypeLBHttpProfile,
	}
	monitorProfile = &model.LBTcpMonitorProfile{
		Id:           common.String("default-tcp-lb-monitor"),
		Path:         common.String("/infra/lb-monitor-profiles/default-tcp-lb-monitor"),
		ParentPath:   common.String("/infra"),
		ResourceType: common.ResourceTypeLBTcpMonitorProfile,
	}
	persistenceProfile = &model.LBSourceIpPersistenceProfile{
		Id:           common.String("default-source-ip-lb-persistence-profile"),
		Path:         common.String("/infra/lb-persistence-profiles/default-source-ip-lb-persistence-profile"),
		ParentPath:   common.String("/infra"),
		ResourceType: common.ResourceTypeLBSourceIpPersistenceProfile,
	}
)

type fakeInfraClient struct{}

func (f fakeInfraClient) Get(basePathParam *string, filterParam *string, typeFilterParam *string) (model.Infra, error) {
	return model.Infra{}, nil
}

func (f fakeInfraClient) Update(infraParam model.Infra) (model.Infra, error) {
	return model.Infra{}, nil
}

func (f fakeInfraClient) Patch(infraParam model.Infra, enforceRevisionCheckParam *bool) error {
	return nil
}

type fakeLBAppProfileClient struct{}

func (f fakeLBAppProfileClient) Delete(string, *bool) error {
	return nil
}

func (f fakeLBAppProfileClient) Get(string) (*data.StructValue, error) {
	return nil, nil
}

func (f fakeLBAppProfileClient) List(*string, *bool, *string, *int64, *bool, *string) (model.LBAppProfileListResult, error) {
	return model.LBAppProfileListResult{}, nil
}

func (f fakeLBAppProfileClient) Patch(string, *data.StructValue) error {
	return nil
}

func (f fakeLBAppProfileClient) Update(string, *data.StructValue) (*data.StructValue, error) {
	return nil, nil
}

type fakeLBMonitorProfileClient struct{}

func (f fakeLBMonitorProfileClient) Delete(string, *bool) error {
	return nil
}

func (f fakeLBMonitorProfileClient) Get(string) (*data.StructValue, error) {
	return nil, nil
}

func (f fakeLBMonitorProfileClient) List(*string, *bool, *string, *int64, *bool, *string) (model.LBMonitorProfileListResult, error) {
	return model.LBMonitorProfileListResult{}, nil
}

func (f fakeLBMonitorProfileClient) Patch(string, *data.StructValue) error {
	return nil
}

func (f fakeLBMonitorProfileClient) Update(string, *data.StructValue) (*data.StructValue, error) {
	return nil, nil
}

type fakeLBPersistenceProfileClient struct{}

func (f fakeLBPersistenceProfileClient) Delete(string, *bool) error {
	return nil
}

func (f fakeLBPersistenceProfileClient) Get(string) (*data.StructValue, error) {
	return nil, nil
}

func (f fakeLBPersistenceProfileClient) List(*string, *bool, *string, *int64, *bool, *string) (model.LBPersistenceProfileListResult, error) {
	return model.LBPersistenceProfileListResult{}, nil
}

func (f fakeLBPersistenceProfileClient) Patch(string, *data.StructValue) error {
	return nil
}

func (f fakeLBPersistenceProfileClient) Update(string, *data.StructValue) (*data.StructValue, error) {
	return nil, nil
}

func TestCleanupInfraDLBResources(t *testing.T) {
	validCtx := context.Background()
	invalidCtx, cancelFn := context.WithCancel(validCtx)
	cancelFn()

	for _, tc := range []struct {
		name           string
		ctx            context.Context
		cleanupFn      func(svc *LBInfraCleaner) func(ctx context.Context) error
		queriedObjects interface{}
		mockFn         func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches
		queryErr       error
		expErrStr      string
	}{
		{
			name: "success with cleanupInfraDLBVirtualServers",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBVirtualServers
			},
			queriedObjects: []*model.LBVirtualServer{lbVS},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:LBVirtualServer AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
		},
		{
			name: "timed out with cleanupInfraDLBVirtualServers",
			ctx:  invalidCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBVirtualServers
			},
			queriedObjects: []*model.LBVirtualServer{lbVS},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:LBVirtualServer AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				return nil
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
		{
			name: "failed with cleanupInfraDLBVirtualServers by NSX error",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBVirtualServers
			},
			queriedObjects: []*model.LBVirtualServer{lbVS},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:LBVirtualServer AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("server error")},
					Times:  1,
				}})
				return patches
			},
			expErrStr: "server error",
		},
		{
			name: "success with cleanupInfraDLBPools",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBPools
			},
			queriedObjects: []*model.LBPool{lbPool},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:LBPool AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
		},
		{
			name: "timed out with cleanupInfraDLBPools",
			ctx:  invalidCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBPools
			},
			queriedObjects: []*model.LBPool{lbPool},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:LBPool AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				return nil
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
		{
			name: "failed with cleanupInfraDLBPools by NSX error",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBPools
			},
			queriedObjects: []*model.LBPool{lbPool},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:LBPool AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("server error")},
					Times:  1,
				}})
				return patches
			},
			expErrStr: "server error",
		},
		{
			name: "success with cleanupInfraDLBServices",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBServices
			},
			queriedObjects: []*model.LBService{lbService},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:LBService AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
		},
		{
			name: "timed out with cleanupInfraDLBServices",
			ctx:  invalidCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBServices
			},
			queriedObjects: []*model.LBService{lbService},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:LBService AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				return nil
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
		{
			name: "failed with cleanupInfraDLBServices by NSX error",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBServices
			},
			queriedObjects: []*model.LBService{lbService},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:LBService AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("server error")},
					Times:  1,
				}})
				return patches
			},
			expErrStr: "server error",
		},
		{
			name: "success with cleanupInfraDLBGroups",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBGroups
			},
			queriedObjects: []*model.Group{group},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:Group AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
		},
		{
			name: "timed out with cleanupInfraDLBGroups",
			ctx:  invalidCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBGroups
			},
			queriedObjects: []*model.Group{group},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:Group AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				return nil
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
		{
			name: "failed with cleanupInfraDLBGroups by NSX error",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraDLBGroups
			},
			queriedObjects: []*model.Group{group},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:Group AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test AND tags.scope:ncp\\/created_for AND tags.tag:DLB"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("server error")},
					Times:  1,
				}})
				return patches
			},
			expErrStr: "server error",
		},
		{
			name: "success with cleanupInfraSharedResources",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraSharedResources
			},
			queriedObjects: []*model.SharedResource{sharedResource},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:SharedResource AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
		},
		{
			name: "timed out with cleanupInfraSharedResources",
			ctx:  invalidCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraSharedResources
			},
			queriedObjects: []*model.SharedResource{sharedResource},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:SharedResource AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				return nil
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
		{
			name: "failed with cleanupInfraSharedResources by NSX error",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraSharedResources
			},
			queriedObjects: []*model.SharedResource{sharedResource},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:SharedResource AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("server error")},
					Times:  1,
				}})
				return patches
			},
			expErrStr: "server error",
		},
		{
			name: "success with cleanupInfraShares",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraShares
			},
			queriedObjects: []*model.Share{share},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:Share AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
		},
		{
			name: "timed out with cleanupInfraShares",
			ctx:  invalidCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraShares
			},
			queriedObjects: []*model.Share{share},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:Share AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				return nil
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
		{
			name: "failed with cleanupInfraShares by NSX error",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraShares
			},
			queriedObjects: []*model.Share{share},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:Share AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("server error")},
					Times:  1,
				}})
				return patches
			},
			expErrStr: "server error",
		},
		{
			name: "success with cleanupInfraCerts",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraCerts
			},
			queriedObjects: []*model.TlsCertificate{cert},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:TlsCertificate AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
		},
		{
			name: "timed out with cleanupInfraCerts",
			ctx:  invalidCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraCerts
			},
			queriedObjects: []*model.TlsCertificate{cert},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:TlsCertificate AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				return nil
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
		{
			name: "failed with cleanupInfraCerts by NSX error",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupInfraCerts
			},
			queriedObjects: []*model.TlsCertificate{cert},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "resource_type:TlsCertificate AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("server error")},
					Times:  1,
				}})
				return patches
			},
			expErrStr: "server error",
		},
		{
			name: "success with cleanupLBAppProfiles",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBAppProfiles
			},
			queriedObjects: []*model.LBHttpProfile{appProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBHttpProfile OR resource_type:LBFastTcpProfile OR resource_type:LBFastUdpProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.LbAppProfileClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
		},
		{
			name: "timed out with cleanupLBAppProfiles",
			ctx:  invalidCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBAppProfiles
			},
			queriedObjects: []*model.LBHttpProfile{appProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBHttpProfile OR resource_type:LBFastTcpProfile OR resource_type:LBFastUdpProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				return nil
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
		{
			name: "failed with cleanupLBAppProfiles by NSX error",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBAppProfiles
			},
			queriedObjects: []*model.LBHttpProfile{appProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBHttpProfile OR resource_type:LBFastTcpProfile OR resource_type:LBFastUdpProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.LbAppProfileClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("server error")},
					Times:  1,
				}})
				return patches
			},
			expErrStr: "server error",
		},
		{
			name: "success with cleanupLBMonitorProfiles",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBMonitorProfiles
			},
			queriedObjects: []*model.LBTcpMonitorProfile{monitorProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBHttpMonitorProfile OR resource_type:LBTcpMonitorProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.LbMonitorProfilesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
		},
		{
			name: "timed out with cleanupLBMonitorProfiles",
			ctx:  invalidCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBMonitorProfiles
			},
			queriedObjects: []*model.LBTcpMonitorProfile{monitorProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBHttpMonitorProfile OR resource_type:LBTcpMonitorProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				return nil
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
		{
			name: "failed with cleanupLBMonitorProfiles by NSX error",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBMonitorProfiles
			},
			queriedObjects: []*model.LBTcpMonitorProfile{monitorProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBHttpMonitorProfile OR resource_type:LBTcpMonitorProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.LbMonitorProfilesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("server error")},
					Times:  1,
				}})
				return patches
			},
			expErrStr: "server error",
		},
		{
			name: "success with cleanupLBMonitorProfiles",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBMonitorProfiles
			},
			queriedObjects: []*model.LBTcpMonitorProfile{monitorProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBHttpMonitorProfile OR resource_type:LBTcpMonitorProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.LbMonitorProfilesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
		},
		{
			name: "timed out with cleanupLBMonitorProfiles",
			ctx:  invalidCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBMonitorProfiles
			},
			queriedObjects: []*model.LBTcpMonitorProfile{monitorProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBHttpMonitorProfile OR resource_type:LBTcpMonitorProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				return nil
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
		{
			name: "failed with cleanupLBMonitorProfiles by NSX error",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBMonitorProfiles
			},
			queriedObjects: []*model.LBTcpMonitorProfile{monitorProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBHttpMonitorProfile OR resource_type:LBTcpMonitorProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.LbMonitorProfilesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("server error")},
					Times:  1,
				}})
				return patches
			},
			expErrStr: "server error",
		},

		{
			name: "success with cleanupLBPersistenceProfiles",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBPersistenceProfiles
			},
			queriedObjects: []*model.LBSourceIpPersistenceProfile{persistenceProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBCookiePersistenceProfile OR resource_type:LBSourceIpPersistenceProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.LbPersistenceProfilesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{nil},
					Times:  1,
				}})
				return patches
			},
		},
		{
			name: "timed out with cleanupLBPersistenceProfiles",
			ctx:  invalidCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBPersistenceProfiles
			},
			queriedObjects: []*model.LBSourceIpPersistenceProfile{persistenceProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBCookiePersistenceProfile OR resource_type:LBSourceIpPersistenceProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				return nil
			},
			expErrStr: "failed because of timeout\ncontext canceled",
		},
		{
			name: "failed with cleanupLBPersistenceProfiles by NSX error",
			ctx:  validCtx,
			cleanupFn: func(svc *LBInfraCleaner) func(ctx context.Context) error {
				return svc.cleanupLBPersistenceProfiles
			},
			queriedObjects: []*model.LBSourceIpPersistenceProfile{persistenceProfile},
			mockFn: func(svc *LBInfraCleaner, queryResponse model.SearchResponse, mockQueryClient *mocks.MockQueryClient) *gomonkey.Patches {
				query := "(resource_type:LBCookiePersistenceProfile OR resource_type:LBSourceIpPersistenceProfile) AND tags.scope:ncp\\/cluster AND tags.tag:k8scl-one\\:test"
				mockQueryClient.EXPECT().List(query, gomock.Any(), nil, gomock.Any(), nil, nil).Return(queryResponse, nil)
				patches := gomonkey.ApplyMethodSeq(svc.NSXClient.LbPersistenceProfilesClient, "Delete", []gomonkey.OutputCell{{
					Values: gomonkey.Params{fmt.Errorf("server error")},
					Times:  1,
				}})
				return patches
			},
			expErrStr: "server error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockQueryClient := mocks.NewMockQueryClient(ctrl)
			cleaner := prepareCleaner()
			cleaner.NSXClient.QueryClient = mockQueryClient

			patches := tc.mockFn(cleaner, generateQueryResponse(t, tc.queriedObjects), mockQueryClient)
			if patches != nil {
				defer patches.Reset()
			}

			err := tc.cleanupFn(cleaner)(tc.ctx)
			if tc.expErrStr != "" {
				require.EqualError(t, err, tc.expErrStr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCleanupInfraResources(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mockFn    func(cleaner *LBInfraCleaner) *gomonkey.Patches
		expErrStr string
	}{
		{
			name: "success to clean up infra resources",
			mockFn: func(cleaner *LBInfraCleaner) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraDLBVirtualServers", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraSharedResources", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraShares", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraDLBPools", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraDLBServices", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraDLBGroups", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraCerts", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupLBAppProfiles", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupLBPersistenceProfiles", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupLBMonitorProfiles", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				return patches
			},
		}, {
			name: "failed with LB virtual server clean up",
			mockFn: func(cleaner *LBInfraCleaner) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraDLBVirtualServers", func(_ *LBInfraCleaner, ctx context.Context) error {
					return fmt.Errorf("failure in cleanupInfraDLBVirtualServers")
				})
				return patches
			},
			expErrStr: "failure in cleanupInfraDLBVirtualServers",
		},
		{
			name: "failed with parallel clean up",
			mockFn: func(cleaner *LBInfraCleaner) *gomonkey.Patches {
				patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraDLBVirtualServers", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraSharedResources", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraShares", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraDLBPools", func(_ *LBInfraCleaner, ctx context.Context) error {
					return fmt.Errorf("failure in cleanupInfraDLBPools")
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraDLBServices", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraDLBGroups", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupInfraCerts", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupLBAppProfiles", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupLBPersistenceProfiles", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(cleaner), "cleanupLBMonitorProfiles", func(_ *LBInfraCleaner, ctx context.Context) error {
					return nil
				})
				return patches
			},
			expErrStr: "failure in cleanupInfraDLBPools",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleaner := prepareCleaner()
			patches := tc.mockFn(cleaner)
			if patches != nil {
				defer patches.Reset()
			}

			ctx := context.Background()
			err := cleaner.CleanupInfraResources(ctx)
			if tc.expErrStr != "" {
				require.EqualError(t, err, tc.expErrStr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func generateQueryResponse(t *testing.T, resources interface{}) model.SearchResponse {
	objects := make([]interface{}, 0)
	var bindingType bindings.BindingType
	switch resources.(type) {
	case []*model.LBVirtualServer:
		for _, vs := range resources.([]*model.LBVirtualServer) {
			objects = append(objects, vs)
		}
		bindingType = model.LBVirtualServerBindingType()
	case []*model.LBPool:
		for _, pool := range resources.([]*model.LBPool) {
			objects = append(objects, pool)
		}
		bindingType = model.LBPoolBindingType()
	case []*model.LBService:
		for _, lbs := range resources.([]*model.LBService) {
			objects = append(objects, lbs)
		}
		bindingType = model.LBServiceBindingType()
	case []*model.TlsCertificate:
		for _, cert := range resources.([]*model.TlsCertificate) {
			objects = append(objects, cert)
		}
		bindingType = model.TlsCertificateBindingType()
	case []*model.Group:
		for _, g := range resources.([]*model.Group) {
			objects = append(objects, g)
		}
		bindingType = model.GroupBindingType()
	case []*model.SharedResource:
		for _, sharedResource := range resources.([]*model.SharedResource) {
			objects = append(objects, sharedResource)
		}
		bindingType = model.SharedResourceBindingType()
	case []*model.Share:
		for _, share := range resources.([]*model.Share) {
			objects = append(objects, share)
		}
		bindingType = model.ShareBindingType()
	case []*model.LBHttpProfile:
		for _, profile := range resources.([]*model.LBHttpProfile) {
			objects = append(objects, profile)
		}
		bindingType = model.LBHttpProfileBindingType()
	case []*model.LBTcpMonitorProfile:
		for _, profile := range resources.([]*model.LBTcpMonitorProfile) {
			objects = append(objects, profile)
		}
		bindingType = model.LBTcpMonitorProfileBindingType()
	case []*model.LBSourceIpPersistenceProfile:
		for _, profile := range resources.([]*model.LBSourceIpPersistenceProfile) {
			objects = append(objects, profile)
		}
		bindingType = model.LBSourceIpPersistenceProfileBindingType()
	}
	return convertToResponse(t, objects, bindingType)
}

func convertToResponse(t *testing.T, resources []interface{}, bindingType bindings.BindingType) model.SearchResponse {
	var results []*data.StructValue
	for _, obj := range resources {
		vsData, errs := common.NewConverter().ConvertToVapi(obj, bindingType)
		require.Equal(t, 0, len(errs))
		results = append(results, vsData.(*data.StructValue))
	}
	resultCount := int64(len(results))
	cursor := fmt.Sprintf("%d", resultCount)
	return model.SearchResponse{
		Results:     results,
		Cursor:      &cursor,
		ResultCount: &resultCount,
	}
}

func prepareCleaner() *LBInfraCleaner {
	log := logger.ZapLogger(false, 0)
	return &LBInfraCleaner{
		Service: common.Service{
			NSXClient: &nsx.Client{
				InfraClient:                 &fakeInfraClient{},
				LbAppProfileClient:          &fakeLBAppProfileClient{},
				LbMonitorProfilesClient:     &fakeLBMonitorProfileClient{},
				LbPersistenceProfilesClient: &fakeLBPersistenceProfileClient{},
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: "k8scl-one:test",
					},
				},
			},
		},
		log: &log,
	}
}
