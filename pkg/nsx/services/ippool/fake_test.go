package ippool

import (
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

type fakeProjectQueryClient struct {
}

func (f fakeProjectQueryClient) List(orgIdParam string, projectIdParam string, queryParam string, cursorParam *string, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.SearchResponse, error) {
	return model.SearchResponse{}, nil
}

func (qIface *fakeQueryClient) List(queryParam string, cursorParam *string, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.SearchResponse, error) {
	cursor := "2"
	resultCount := int64(2)
	return model.SearchResponse{
		Results: []*data.StructValue{&data.StructValue{}},
		Cursor:  &cursor, ResultCount: &resultCount,
	}, nil
}

type fakeProjectInfraClient struct {
}

func (f fakeProjectInfraClient) Get(orgIdParam string, projectIdParam string, basePathParam *string, filterParam *string, typeFilterParam *string) (model.Infra, error) {
	return model.Infra{}, nil
}

func (f fakeProjectInfraClient) Patch(orgIdParam string, projectIdParam string, infraParam model.Infra, enforceRevisionCheckParam *bool) error {
	return nil
}

type fakeRealizedEntitiesClient struct {
}

func (f fakeRealizedEntitiesClient) List(_ string, _ *string) (model.GenericPolicyRealizedResourceListResult, error) {
	a := model.GenericPolicyRealizedResourceListResult{
		Results: []model.GenericPolicyRealizedResource{
			{
				EntityType: String("IpBlockSubnet"),
				ExtendedAttributes: []model.AttributeVal{
					{Key: String("cidr"), Values: []string{"1.1.1.1/24"}},
				},
			},
		},
	}
	return a, nil
}

func fakeService() *IPPoolService {
	c := nsx.NewConfig("localhost", "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(c)
	rc, _ := cluster.NewRestConnector()
	ipPoolStore := &IPPoolStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPPoolCRUID: indexFunc}),
		BindingType: model.IpAddressPoolBindingType(),
	}}
	ipPoolBlockSubnetStore := &IPPoolBlockSubnetStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeIPPoolCRUID: indexFunc}),
		BindingType: model.IpAddressPoolBlockSubnetBindingType(),
	}}

	service := &IPPoolService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient:            &fakeQueryClient{},
				RestConnector:          rc,
				RealizedEntitiesClient: &fakeRealizedEntitiesClient{},
				ProjectInfraClient:     &fakeProjectInfraClient{},
				ProjectQueryClient:     &fakeProjectQueryClient{},
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
		ipPoolStore:            ipPoolStore,
		ipPoolBlockSubnetStore: ipPoolBlockSubnetStore,
	}
	return service
}
