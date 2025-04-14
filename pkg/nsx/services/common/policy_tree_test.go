package common

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	orgroot_mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

type mockInfraClient struct{}

func (c *mockInfraClient) Get(*string, *string, *string) (model.Infra, error) {
	return model.Infra{}, nil
}

func (c *mockInfraClient) Patch(model.Infra, *bool) error {
	return nil
}

func (c *mockInfraClient) Update(model.Infra) (model.Infra, error) {
	return model.Infra{}, nil
}

func TestBuilder(t *testing.T) {
	builder, err := PolicyPathVpcSubnetConnectionBindingMap.NewPolicyTreeBuilder()
	require.NoError(t, err)

	assert.Equal(t, ResourceTypeOrgRoot, builder.rootType)
}

func TestPolicyPathBuilder_DeleteMultipleResourcesOnNSX(t *testing.T) {
	testVPCResources(t)
	testInfraResources(t)
	testProjectInfraResources(t)
}

func TestPagingDeleteResources(t *testing.T) {
	count := 10
	targetSubnets := make([]*model.VpcSubnet, count)
	for i := 0; i < count; i++ {
		idString := fmt.Sprintf("id-%d", i)
		targetSubnets[i] = &model.VpcSubnet{
			Id:   String(idString),
			Path: String(fmt.Sprintf("/orgs/default/projects/p1/vpcs/vpc1/subnets/%s", idString)),
		}
	}

	// Verify the happy path.
	t.Run("testPagingDeleteResourcesSucceeded", func(t *testing.T) {
		testPagingDeleteResourcesSucceeded(t, targetSubnets)
	})

	// Verify the case tha a TimeoutFailed error is returned if context is done.
	t.Run("testPagingDeleteResourcesWithContextDone", func(t *testing.T) {
		testPagingDeleteResourcesWithContextDone(t, targetSubnets)
	})

	// Verify the case that NSX error is hit when calling HAPI.
	t.Run("testPagingDeleteResourcesWithNSXFailure", func(t *testing.T) {
		testPagingDeleteResourcesWithNSXFailure(t, targetSubnets)
	})
}

func testPagingDeleteResourcesSucceeded(t *testing.T, targetSubnets []*model.VpcSubnet) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	builder, err := PolicyPathVpcSubnet.NewPolicyTreeBuilder()
	require.NoError(t, err)

	mockRootClient := orgroot_mocks.NewMockOrgRootClient(ctrl)
	mockRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil)
	nsxClient := &nsx.Client{
		OrgRootClient: mockRootClient,
	}

	ctx := context.Background()
	err = builder.PagingUpdateResources(ctx, targetSubnets, 500, nsxClient, nil)
	require.NoError(t, err)
}

func TestPagingNSXResources(t *testing.T) {
	for _, tc := range []struct {
		name     string
		count    int
		expPages int
	}{
		{
			name:     "no paging on the requests",
			count:    50,
			expPages: 1,
		}, {
			name:     "paging on the requests",
			count:    1501,
			expPages: 4,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targetSubnets := make([]*model.VpcSubnet, tc.count)
			for i := 0; i < tc.count; i++ {
				idString := fmt.Sprintf("id-%d", i)
				targetSubnets[i] = &model.VpcSubnet{
					Id:   String(idString),
					Path: String(fmt.Sprintf("/orgs/default/projects/p1/vpcs/vpc1/subnets/%s", idString)),
				}
			}

			pagedResources := PagingNSXResources(targetSubnets, 500)
			assert.Equal(t, tc.expPages, len(pagedResources))

			var totalResources []*model.VpcSubnet
			for _, pagedSlice := range pagedResources {
				totalResources = append(totalResources, pagedSlice...)
			}
			assert.ElementsMatch(t, targetSubnets, totalResources)
		})
	}
}

func testPagingDeleteResourcesWithContextDone(t *testing.T, targetSubnets []*model.VpcSubnet) {
	ctx, cancelFn := context.WithCancel(context.TODO())
	cancelFn()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	nsxClient := &nsx.Client{
		OrgRootClient: orgroot_mocks.NewMockOrgRootClient(ctrl),
	}

	builder, err := PolicyPathVpcSubnet.NewPolicyTreeBuilder()
	require.NoError(t, err)

	err = builder.PagingUpdateResources(ctx, targetSubnets, 500, nsxClient, nil)
	assert.ErrorContains(t, err, "failed because of timeout")
}

func testPagingDeleteResourcesWithNSXFailure(t *testing.T, targetSubnets []*model.VpcSubnet) {
	ctx := context.Background()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRootClient := orgroot_mocks.NewMockOrgRootClient(ctrl)
	mockRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(fmt.Errorf("NSX returned an error"))
	nsxClient := &nsx.Client{
		OrgRootClient: mockRootClient,
	}

	builder, err := PolicyPathVpcSubnet.NewPolicyTreeBuilder()
	require.NoError(t, err)
	err = builder.PagingUpdateResources(ctx, targetSubnets, 500, nsxClient, nil)
	assert.EqualError(t, err, "NSX returned an error")
}

func testVPCResources(t *testing.T) {
	cases := []struct {
		name    string
		objects any
	}{
		{
			name: "delete VpcSubnet",
			objects: []*model.VpcSubnet{{
				Id:           String("subnet1"),
				Path:         String("/orgs/default/projects/p1/vpcs/vpc1/subnets/subnet1"),
				ResourceType: String(ResourceTypeSubnet),
			}},
		}, {
			name: "delete VpcSubnetPort",
			objects: []*model.VpcSubnetPort{{
				Id:           String("port1"),
				Path:         String("/orgs/default/projects/p1/vpcs/vpc1/subnets/subnet1/ports/port"),
				ResourceType: String(ResourceTypeSubnetPort),
			}},
		}, {
			name: "delete SubnetConnectionBindingMap",
			objects: []*model.SubnetConnectionBindingMap{{
				Id:           String("bm1"),
				Path:         String("/orgs/default/projects/p1/vpcs/vpc1/subnets/subnet1/subnet-connection-binding-maps/bm1"),
				ResourceType: String(ResourceTypeSubnetConnectionBindingMap),
			}},
		}, {
			name: "delete SecurityPolicy",
			objects: []*model.SecurityPolicy{{
				Id:           String("sp1"),
				Path:         String("/orgs/default/projects/p1/vpcs/vpc1/security-policies/sg1"),
				ResourceType: String(ResourceTypeSecurityPolicy),
			}},
		}, {
			name: "delete SecurityPolicy rule",
			objects: []*model.Rule{{
				Id:           String("rule1"),
				Path:         String("/orgs/default/projects/p1/vpcs/vpc1/security-policies/sg1/rules/rule1"),
				Action:       String("ALLOW"),
				ResourceType: String(ResourceTypeRule),
			}},
		}, {
			name: "delete VPC group",
			objects: []*model.Group{{
				Id:           String("group1"),
				Path:         String("/orgs/default/projects/p1/vpcs/vpc1/groups/group1"),
				ResourceType: String(ResourceTypeGroup),
			}},
		}, {
			name: "delete VPC IP address allocation",
			objects: []*model.VpcIpAddressAllocation{{
				Id:           String("allocation1"),
				Path:         String("/orgs/default/projects/p1/vpcs/vpc1/ip-address-allocations/allocation1"),
				ResourceType: String(ResourceTypeIPAddressAllocation),
			}},
		}, {
			name: "delete static routes",
			objects: []*model.StaticRoutes{{
				Id:           String("route1"),
				Path:         String("/orgs/default/projects/p1/vpcs/vpc1/static-routes/route1"),
				ResourceType: String(ResourceTypeStaticRoutes),
			}},
		}, {
			name: "delete VPC LB service",
			objects: []*model.LBService{{
				Id:           String("lbs1"),
				Path:         String("/orgs/default/projects/p1/vpcs/vpc1/vpc-lbs/lbs1"),
				ResourceType: String(ResourceTypeLBService),
			}},
		}, {
			name: "delete VPC LB pool",
			objects: []*model.LBPool{{
				Id:           String("pool1"),
				Path:         String("/orgs/default/projects/p1/vpcs/vpc1/vpc-lb-pools/pool1"),
				ResourceType: String(ResourceTypeLBPool),
			}},
		}, {
			name: "delete VPC LB virtual server",
			objects: []*model.LBVirtualServer{{
				Id:           String("vs1"),
				Path:         String("/orgs/default/projects/p1/vpcs/vpc1/vpc-lb-virtual-servers/vs1"),
				ResourceType: String(ResourceTypeLBVirtualServer),
			}},
		},
	}

	nsxMockFn := func(ctrl *gomock.Controller, expErr error) (string, *nsx.Client) {
		orgRootClient := orgroot_mocks.NewMockOrgRootClient(ctrl)
		orgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(expErr)
		expErrStr := ""
		if expErr != nil {
			expErrStr = expErr.Error()
		}
		return expErrStr, &nsx.Client{
			OrgRootClient: orgRootClient,
		}
	}

	testVPCResourceDeletion := func(nsxErr error) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				errStr, nsxClient := nsxMockFn(ctrl, nsxErr)

				var err error
				switch tc.objects.(type) {
				case []*model.VpcSubnet:
					res := tc.objects.([]*model.VpcSubnet)
					err = testPolicyPathBuilderDeletion(t, PolicyPathVpcSubnet, res, nsxClient)
				case []*model.VpcSubnetPort:
					res := tc.objects.([]*model.VpcSubnetPort)
					err = testPolicyPathBuilderDeletion(t, PolicyPathVpcSubnetPort, res, nsxClient)
				case []*model.SubnetConnectionBindingMap:
					res := tc.objects.([]*model.SubnetConnectionBindingMap)
					err = testPolicyPathBuilderDeletion(t, PolicyPathVpcSubnetConnectionBindingMap, res, nsxClient)
				case []*model.SecurityPolicy:
					res := tc.objects.([]*model.SecurityPolicy)
					err = testPolicyPathBuilderDeletion(t, PolicyPathVpcSecurityPolicy, res, nsxClient)
				case []*model.Rule:
					res := tc.objects.([]*model.Rule)
					err = testPolicyPathBuilderDeletion(t, PolicyPathVpcSecurityPolicyRule, res, nsxClient)
				case []*model.Group:
					res := tc.objects.([]*model.Group)
					err = testPolicyPathBuilderDeletion(t, PolicyPathVpcGroup, res, nsxClient)
				case []*model.VpcIpAddressAllocation:
					res := tc.objects.([]*model.VpcIpAddressAllocation)
					err = testPolicyPathBuilderDeletion(t, PolicyPathVpcIPAddressAllocation, res, nsxClient)
				case []*model.StaticRoutes:
					res := tc.objects.([]*model.StaticRoutes)
					err = testPolicyPathBuilderDeletion(t, PolicyPathVpcStaticRoutes, res, nsxClient)
				case []*model.LBService:
					res := tc.objects.([]*model.LBService)
					err = testPolicyPathBuilderDeletion(t, PolicyPathVpcLBService, res, nsxClient)
				case []*model.LBPool:
					res := tc.objects.([]*model.LBPool)
					err = testPolicyPathBuilderDeletion(t, PolicyPathVpcLBPool, res, nsxClient)
				case []*model.LBVirtualServer:
					res := tc.objects.([]*model.LBVirtualServer)
					err = testPolicyPathBuilderDeletion(t, PolicyPathVpcLBVirtualServer, res, nsxClient)
				}

				if nsxErr != nil {
					assert.EqualError(t, err, errStr)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	}

	t.Run("testVPCResourceDeletionSucceed", func(t *testing.T) {
		testVPCResourceDeletion(nil)
	})
	t.Run("testVPCResourceDeletionFailed", func(t *testing.T) {
		testVPCResourceDeletion(fmt.Errorf("NSX returns 503"))
	})

}

func testInfraResources(t *testing.T) {
	cases := []struct {
		name    string
		objects any
	}{
		{
			name: "delete infra group",
			objects: []*model.Group{{
				Id:           String("group1"),
				Path:         String("/infra/domains/default/groups/group1"),
				ResourceType: String(ResourceTypeGroup),
			}},
		}, {
			name: "delete infra share",
			objects: []*model.Share{{
				Id:           String("share1"),
				Path:         String("/infra/shares/share1"),
				ResourceType: String(ResourceTypeShare),
			}},
		}, {
			name: "delete infra shared resource",
			objects: []*model.SharedResource{{
				Id:           String("res1"),
				Path:         String("/infra/shares/share1/resources/res1"),
				ResourceType: String(ResourceTypeSharedResource),
			}},
		},
		{
			name: "delete infra LB service",
			objects: []*model.LBService{{
				Id:           String("lbs1"),
				Path:         String("/infra/lb-services/lbs1"),
				ResourceType: String(ResourceTypeLBService),
			}},
		}, {
			name: "delete infra LB pool",
			objects: []*model.LBPool{{
				Id:           String("pool1"),
				Path:         String("/infra/lb-pools/pool1"),
				ResourceType: String(ResourceTypeLBPool),
			}},
		}, {
			name: "delete infra LB virtual server",
			objects: []*model.LBVirtualServer{{
				Id:           String("vs1"),
				Path:         String("/infra/lb-virtual-servers/vs1"),
				ResourceType: String(ResourceTypeLBVirtualServer),
			}},
		}, {
			name: "delete infra LB tls certificate",
			objects: []*model.TlsCertificate{{
				Id:           String("cert1"),
				Path:         String("/infra/certificates/cert1"),
				ResourceType: String(ResourceTypeTlsCertificate),
			}},
		},
	}

	nsxMockFn := func(nsxClient *nsx.Client, nsxErr error) (*gomonkey.Patches, string) {
		patches := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient.InfraClient), "Patch", func(_ *mockInfraClient, infraParam model.Infra, enforceRevisionCheckParam *bool) error {
			return nsxErr
		})
		errStr := ""
		if nsxErr != nil {
			errStr = nsxErr.Error()
		}
		return patches, errStr
	}

	testInfraResourceDeletion := func(nsxErr error) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				nsxClient := &nsx.Client{
					InfraClient: &mockInfraClient{},
				}
				patches, expErr := nsxMockFn(nsxClient, nsxErr)
				defer patches.Reset()

				var err error
				switch tc.objects.(type) {
				case []*model.Group:
					res := tc.objects.([]*model.Group)
					err = testPolicyPathBuilderDeletion(t, PolicyPathInfraGroup, res, nsxClient)
				case []*model.Share:
					res := tc.objects.([]*model.Share)
					err = testPolicyPathBuilderDeletion(t, PolicyPathInfraShare, res, nsxClient)
				case []*model.SharedResource:
					res := tc.objects.([]*model.SharedResource)
					err = testPolicyPathBuilderDeletion(t, PolicyPathInfraSharedResource, res, nsxClient)
				case []*model.LBService:
					res := tc.objects.([]*model.LBService)
					err = testPolicyPathBuilderDeletion(t, PolicyPathInfraLBService, res, nsxClient)
				case []*model.LBPool:
					res := tc.objects.([]*model.LBPool)
					err = testPolicyPathBuilderDeletion(t, PolicyPathInfraLBPool, res, nsxClient)
				case []*model.LBVirtualServer:
					res := tc.objects.([]*model.LBVirtualServer)
					err = testPolicyPathBuilderDeletion(t, PolicyPathInfraLBVirtualServer, res, nsxClient)
				case []*model.TlsCertificate:
					res := tc.objects.([]*model.TlsCertificate)
					err = testPolicyPathBuilderDeletion(t, PolicyPathInfraCert, res, nsxClient)
				}

				if nsxErr != nil {
					assert.EqualError(t, err, expErr)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	}

	t.Run("testInfraResourceDeletionSucceeded", func(t *testing.T) {
		testInfraResourceDeletion(nil)
	})
	t.Run("testInfraResourceDeletionFailed", func(t *testing.T) {
		testInfraResourceDeletion(fmt.Errorf("NSX returns 503"))
	})
}

func testProjectInfraResources(t *testing.T) {
	cases := []struct {
		name    string
		objects any
	}{
		{
			name: "delete project group",
			objects: []*model.Group{{
				Id:           String("group1"),
				Path:         String("/orgs/default/projects/p1/infra/domains/default/groups/group1"),
				ResourceType: String(ResourceTypeGroup),
			}},
		}, {
			name: "delete project share",
			objects: []*model.Share{{
				Id:           String("share1"),
				Path:         String("/orgs/default/projects/p1/infra/shares/share1"),
				ResourceType: String(ResourceTypeShare),
			}},
		},
	}

	nsxMockFn := func(ctrl *gomock.Controller, expErr error) (string, *nsx.Client) {
		orgRootClient := orgroot_mocks.NewMockOrgRootClient(ctrl)
		orgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(expErr)
		expErrStr := ""
		if expErr != nil {
			expErrStr = expErr.Error()
		}
		return expErrStr, &nsx.Client{
			OrgRootClient: orgRootClient,
		}
	}

	testProjectResourceDeletion := func(nsxErr error) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				errStr, nsxClient := nsxMockFn(ctrl, nsxErr)

				var err error
				switch tc.objects.(type) {
				case []*model.Group:
					res := tc.objects.([]*model.Group)
					err = testPolicyPathBuilderDeletion(t, PolicyPathProjectGroup, res, nsxClient)
				case []*model.Share:
					res := tc.objects.([]*model.Share)
					err = testPolicyPathBuilderDeletion(t, PolicyPathProjectShare, res, nsxClient)
				}

				if nsxErr != nil {
					assert.EqualError(t, err, errStr)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	}
	t.Run("testProjectResourceDeletionSucceeded", func(t *testing.T) {
		testProjectResourceDeletion(nil)
	})
	t.Run("testProjectResourceDeletionFailed", func(t *testing.T) {
		testProjectResourceDeletion(fmt.Errorf("NSX returns 503"))
	})
}

func testPolicyPathBuilderDeletion[T any](t *testing.T, resourcePath PolicyResourcePath[T], objects []T, nsxClient *nsx.Client) error {
	builder, err := resourcePath.NewPolicyTreeBuilder()
	require.Nil(t, err)
	return builder.UpdateMultipleResourcesOnNSX(objects, nsxClient)
}

func TestBuildRootNodePerformance(t *testing.T) {
	orgPrefix, orgCount := "org", 1
	projectPrefix, projectCount := "proj", 10
	vpcPrefix, vpcCount := "vpc", 20
	subnetPrefix, subnetCount := "subnet", 10
	bindingPrefix, bindingCount := "binding", 5

	bindings := make([]*model.SubnetConnectionBindingMap, 0)
	for i := 1; i <= orgCount; i++ {
		orgID := fmt.Sprintf("%s%d", orgPrefix, i)
		for j := 1; j <= projectCount; j++ {
			projID := fmt.Sprintf("%s%d", projectPrefix, j)
			for k := 1; k <= vpcCount; k++ {
				vpcID := fmt.Sprintf("%s%d", vpcPrefix, k)
				for l := 1; l <= subnetCount; l++ {
					subnetID := fmt.Sprintf("%s%d", subnetPrefix, l)
					subnetPath := fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s/subnets/%s", orgID, projID, vpcID, subnetID)
					for m := 0; m <= bindingCount; m++ {
						bindingID := fmt.Sprintf("%s%d", bindingPrefix, m)
						bindingPath := fmt.Sprintf("%s/subnet-connection-binding-maps/%s", subnetPath, bindingID)
						binding := &model.SubnetConnectionBindingMap{
							Id:           String(bindingID),
							Path:         String(bindingPath),
							ParentPath:   String(subnetPath),
							ResourceType: String(ResourceTypeSubnetConnectionBindingMap),
						}
						bindings = append(bindings, binding)
					}
				}
			}
		}
	}

	// The total time to build OrgRoot with 10W SubnetConnectionBindngMaps is supposed to less than 10s.
	builder, err := PolicyPathVpcSubnetConnectionBindingMap.NewPolicyTreeBuilder()
	require.NoError(t, err)
	start := time.Now()
	builder.BuildRootNode(bindings, "")
	cost := time.Now().Sub(start)
	assert.Truef(t, cost.Seconds() < 3, "It takes %s to build Org root with 10K resources", cost.String())
}

func TestParsePathSegments(t *testing.T) {
	builder1, err := PolicyPathInfraShare.NewPolicyTreeBuilder()
	require.NoError(t, err)
	segments, err := builder1.parsePathSegments("/infra/shares/infra-test-share")
	require.NoError(t, err)
	require.Len(t, segments, 2)
	assert.Equal(t, "infra-test-share", segments[len(segments)-1])

	builder2, err := PolicyPathProjectShare.NewPolicyTreeBuilder()
	require.NoError(t, err)
	segments2, err := builder2.parsePathSegments("/orgs/default/projects/project1/infra/shares/project-test-share")
	require.NoError(t, err)
	require.Len(t, segments2, 7)
	assert.Equal(t, "project-test-share", segments2[len(segments2)-1])
}
