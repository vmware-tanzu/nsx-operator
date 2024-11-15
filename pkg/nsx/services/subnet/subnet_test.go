package subnet

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestGenerateSubnetNSTags(t *testing.T) {
	scheme := clientgoscheme.Scheme
	v1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	service := &SubnetService{
		Service: common.Service{
			Client:    fakeClient,
			NSXClient: &nsx.Client{},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint:   "vmc-enforcementpoint",
					UseAVILoadBalancer: false,
				},
			},
		},
	}

	// Create a test namespace
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns",
			UID:  "namespace-uid",
			Labels: map[string]string{
				"env": "test",
			},
		},
	}

	assert.NoError(t, fakeClient.Create(context.TODO(), namespace))

	// Define the Subnet object
	subnet := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnet",
			Namespace: "test-ns",
		},
	}

	// Generate tags for the Subnet
	tags := service.GenerateSubnetNSTags(subnet)

	// Validate the tags
	assert.NotNil(t, tags)
	assert.Equal(t, 3, len(tags)) // 3 tags should be generated

	// Check specific tags
	assert.Equal(t, "namespace-uid", *tags[0].Tag)
	assert.Equal(t, "test-ns", *tags[1].Tag)
	assert.Equal(t, "test", *tags[2].Tag)

	// Define the SubnetSet object
	subnetSet := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnet-set",
			Namespace: "test-ns",
			Labels: map[string]string{
				common.LabelDefaultSubnetSet: common.LabelDefaultPodSubnetSet,
			},
		},
	}

	// Generate tags for the SubnetSet
	tagsSet := service.GenerateSubnetNSTags(subnetSet)

	// Validate the tags for SubnetSet
	assert.NotNil(t, tagsSet)
	assert.Equal(t, 3, len(tagsSet)) // 3 tags should be generated
	assert.Equal(t, "namespace-uid", *tagsSet[0].Tag)
	assert.Equal(t, "test-ns", *tagsSet[1].Tag)
}

type fakeOrgRootClient struct {
}

func (f fakeOrgRootClient) Get(basePathParam *string, filterParam *string, typeFilterParam *string) (model.OrgRoot, error) {
	return model.OrgRoot{}, nil
}

func (f fakeOrgRootClient) Patch(orgRootParam model.OrgRoot, enforceRevisionCheckParam *bool) error {
	return nil
}

type fakeSubnetsClient struct {
}

func (f fakeSubnetsClient) Delete(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) error {
	return nil
}

var fakeSubnetPath = "/orgs/default/projects/nsx_operator_e2e_test/vpcs/subnet-e2e_8f36f7fc-90cd-4e65-a816-daf3ecd6a0f9/subnets/subnet-1"
var fakeSubnetID = "fakeSubnetID"
var fakeVpcSubnet = model.VpcSubnet{Path: &fakeSubnetPath, Id: &fakeSubnetID}

func (f fakeSubnetsClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string) (model.VpcSubnet, error) {
	return fakeVpcSubnet, nil
}

func (f fakeSubnetsClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.VpcSubnetListResult, error) {
	return model.VpcSubnetListResult{}, nil
}

func (f fakeSubnetsClient) Patch(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, vpcSubnetParam model.VpcSubnet) error {
	return nil
}

func (f fakeSubnetsClient) Update(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, vpcSubnetParam model.VpcSubnet) (model.VpcSubnet, error) {
	return model.VpcSubnet{}, nil
}

type fakeRealizedEntitiesClient struct {
}

func (f fakeRealizedEntitiesClient) List(orgIdParam string, projectIdParam string, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
	// GenericPolicyRealizedResource
	state := model.GenericPolicyRealizedResource_STATE_REALIZED
	return model.GenericPolicyRealizedResourceListResult{
		Results: []model.GenericPolicyRealizedResource{
			{
				State: &state,
			},
		},
	}, nil
}

func TestInitializeSubnetService(t *testing.T) {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).Build()
	// SubnetsClient
	commonService := common.Service{
		Client: fakeClient,
		NSXClient: &nsx.Client{
			OrgRootClient:          &fakeOrgRootClient{},
			SubnetsClient:          &fakeSubnetsClient{},
			QueryClient:            &fakeQueryClient{},
			RealizedEntitiesClient: &fakeRealizedEntitiesClient{},
			// VPCClient:     mockVpcclient,
			// RestConnector: rc,
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
	}
	service, err := InitializeSubnetService(commonService)
	assert.NoError(t, err)
	res := service.ListAllSubnet()
	assert.Equal(t, 0, len(res))

	subnetCR := &v1alpha1.Subnet{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{},
		Spec:       v1alpha1.SubnetSpec{},
		Status:     v1alpha1.SubnetStatus{},
	}
	vpcInfo := common.VPCResourceInfo{
		OrgID:             "",
		ProjectID:         "",
		VPCID:             "",
		ID:                "",
		ParentID:          "",
		PrivateIpv4Blocks: nil,
	}
	tags := []model.Tag{{}}
	nsxSubnet, err := service.CreateOrUpdateSubnet(subnetCR, vpcInfo, tags)
	assert.NoError(t, err)
	assert.Equal(t, nsxSubnet, nsxSubnet)

	err = service.DeleteSubnet(fakeVpcSubnet)
	assert.NoError(t, err)

	err = service.Cleanup(context.TODO())
	assert.NoError(t, err)
}

func TestSubnetService_UpdateSubnetSet(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
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
			Scope: common.String("nsx-op/subnet_uid"),
			Tag:   common.String("subnet-1"),
		},
		{
			Scope: common.String("nsx-op/subnetset_uid"),
			Tag:   common.String("subnetset-1"),
		},
		{
			Scope: common.String("nsx-op/namespace"),
			Tag:   common.String("ns-1"),
		},
	}
	vpcSubnets := []*model.VpcSubnet{
		{
			Path: &fakeSubnetPath,
			Id:   common.String("subnet-1"),
			Tags: tags,
			SubnetDhcpConfig: &model.SubnetDhcpConfig{
				Mode: common.String("DHCP_SERVER"),
			},
			AdvancedConfig: &model.SubnetAdvancedConfig{
				StaticIpAllocation: &model.StaticIpAllocation{
					Enabled: common.Bool(false),
				},
			},
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		subnetSet := obj.(*v1alpha1.SubnetSet)
		subnetSet.Namespace = "ns-1"
		subnetSet.Name = "subnetset-1"
		return nil
	})

	patchesCreateOrUpdateSubnet := gomonkey.ApplyFunc((*SubnetService).createOrUpdateSubnet,
		func(r *SubnetService, obj client.Object, nsxSubnet *model.VpcSubnet, vpcInfo *common.VPCResourceInfo) (string, error) {
			return fakeSubnetPath, nil
		})
	defer patchesCreateOrUpdateSubnet.Reset()

	err := service.UpdateSubnetSet("ns-1", vpcSubnets, tags, "")
	assert.Nil(t, err)
}
