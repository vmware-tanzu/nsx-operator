package ipaddressallocation

import (
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/vpcclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

type fakeQueryClient struct{}

func (qIface *fakeQueryClient) List(_ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
	cursor := "0"
	resultCount := int64(0)
	return model.SearchResponse{
		Results: []*data.StructValue{},
		Cursor:  &cursor, ResultCount: &resultCount,
	}, nil
}

func createService(t *testing.T) (*vpc.VPCService, *gomock.Controller, *mocks.MockVpcsClient) {
	config2 := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})

	cluster, _ := nsx.NewCluster(config2)
	rc := cluster.NewRestConnector()

	mockCtrl := gomock.NewController(t)
	mockVpcclient := mocks.NewMockVpcsClient(mockCtrl)
	k8sClient := mock_client.NewMockClient(mockCtrl)

	vpcStore := &vpc.VPCStore{ResourceStore: common.ResourceStore{
		BindingType: model.VpcBindingType(),
	}}

	service := &vpc.VPCService{
		Service: common.Service{
			Client: k8sClient,
			NSXClient: &nsx.Client{
				QueryClient:   &fakeQueryClient{},
				VPCClient:     mockVpcclient,
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
	return service, mockCtrl, mockVpcclient
}

func TestBuildIPAddressAllocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	vpcService, _, _ := createService(t)
	ipAllocService := &IPAddressAllocationService{
		VPCService: vpcService,
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
				CoeConfig: &config.CoeConfig{
					Cluster: "default",
				},
			},
		},
		ipAddressAllocationStore: buildIPAddressAllocationStore(),
	}

	t.Run("VPCInfo is empty", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{}
		ipAlloc.Namespace = "default"
		ipAlloc.Name = "test-ip-alloc"
		ipAlloc.UID = "uid1"

		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{}
		})
		defer patch.Reset()

		result, _, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, false)
		assert.Nil(t, result)
		assert.EqualError(t, err, "failed to find VPCInfo for IPAddressAllocation CR test-ip-alloc in Namespace default")
	})

	t.Run("Success case for IPAddressAllocation CR with AllocationSize", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ip-alloc",
				Namespace: "default",
				UID:       "uid1",
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal,
				AllocationSize:           10,
			},
		}
		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{
					OrgID:     "org1",
					ProjectID: "proj1",
					VPCID:     "vpc1",
				},
			}
		})
		patch.ApplyMethod(reflect.TypeOf(&ipAllocService.Service), "GetNamespaceUID",
			func(s *common.Service, ns string) types.UID {
				return "nsUUid"
			})
		defer patch.Reset()

		result, _, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, false)
		assert.Nil(t, err)
		assert.Equal(t, "test-ip-alloc_p26xv", *result.Id)
		assert.Equal(t, "test-ip-alloc", *result.DisplayName)
		assert.Equal(t, (*string)(nil), result.AllocationIps)
		assert.Equal(t, int64(10), *result.AllocationSize)
		assert.Equal(t, model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV4, *result.IpAddressType)
		assert.Equal(t, "EXTERNAL", *result.IpAddressBlockVisibility)
		assert.Equal(t, (*int64)(nil), result.Ipv6AllocationPrefixLength)
		assert.Equal(t, 6, len(result.Tags))
	})

	t.Run("Success case for IPAddressAllocation CR with allocationIPs", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ip-alloc",
				Namespace: "default",
				UID:       "uid1",
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal,
				AllocationIPs:            "10.0.0.0/28",
			},
		}
		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{
					OrgID:     "org1",
					ProjectID: "proj1",
					VPCID:     "vpc1",
				},
			}
		})
		patch.ApplyMethod(reflect.TypeOf(&ipAllocService.Service), "GetNamespaceUID",
			func(s *common.Service, ns string) types.UID {
				return "nsUUid"
			})
		defer patch.Reset()

		result, _, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, false)
		assert.Nil(t, err)
		assert.Equal(t, "test-ip-alloc_p26xv", *result.Id)
		assert.Equal(t, "test-ip-alloc", *result.DisplayName)
		assert.Equal(t, "10.0.0.0/28", *result.AllocationIps)
		assert.Equal(t, model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV4, *result.IpAddressType)
		assert.Equal(t, "EXTERNAL", *result.IpAddressBlockVisibility)
		assert.Equal(t, (*int64)(nil), result.Ipv6AllocationPrefixLength)
		assert.Equal(t, 6, len(result.Tags))
	})

	t.Run("Success case for IPv6 IPAddressAllocation CR", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ip-alloc-ipv6",
				Namespace: "default",
				UID:       "uid1",
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressType:              v1alpha1.IPAllocationIPAddressTypeIPv6,
				IPv6AllocationPrefixLength: 64,
			},
		}
		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{
					OrgID:     "org1",
					ProjectID: "proj1",
					VPCID:     "vpc1",
				},
			}
		})
		patch.ApplyMethod(reflect.TypeOf(&ipAllocService.Service), "GetNamespaceUID",
			func(s *common.Service, ns string) types.UID {
				return "nsUUid"
			})
		defer patch.Reset()

		result, _, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, false)
		assert.Nil(t, err)
		assert.Equal(t, "test-ip-alloc-ipv6_p26xv", *result.Id)
		assert.Equal(t, "test-ip-alloc-ipv6", *result.DisplayName)
		assert.Equal(t, model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV6, *result.IpAddressType)
		assert.Equal(t, int64(64), *result.Ipv6AllocationPrefixLength)
		assert.Equal(t, (*int64)(nil), result.AllocationSize)
		assert.Equal(t, (*string)(nil), result.IpAddressBlockVisibility)
		assert.Equal(t, 6, len(result.Tags))
	})

	t.Run("Success case for IPv6 IPAddressAllocation CR with allocationIPs", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ip-alloc-ipv6-ips",
				Namespace: "default",
				UID:       "uid1",
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressType: v1alpha1.IPAllocationIPAddressTypeIPv6,
				AllocationIPs: "2001:db8::/128",
			},
		}
		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{
					OrgID:     "org1",
					ProjectID: "proj1",
					VPCID:     "vpc1",
				},
			}
		})
		patch.ApplyMethod(reflect.TypeOf(&ipAllocService.Service), "GetNamespaceUID",
			func(s *common.Service, ns string) types.UID {
				return "nsUUid"
			})
		defer patch.Reset()

		result, _, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, false)
		assert.Nil(t, err)
		assert.Equal(t, "2001:db8::/128", *result.AllocationIps)
		assert.Equal(t, model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV6, *result.IpAddressType)
		assert.Equal(t, (*int64)(nil), result.Ipv6AllocationPrefixLength)
		assert.Equal(t, (*int64)(nil), result.AllocationSize)
		assert.Equal(t, (*string)(nil), result.IpAddressBlockVisibility)
		assert.Equal(t, 6, len(result.Tags))
	})

	t.Run("Restore AllocationIPs for IPAddressAllocation CR", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ip-alloc",
				Namespace: "default",
				UID:       "uid1",
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityExternal,
				AllocationSize:           10,
			},
			Status: v1alpha1.IPAddressAllocationStatus{
				AllocationIPs: "1.2.3.4",
			},
		}
		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{
					OrgID:     "org1",
					ProjectID: "proj1",
					VPCID:     "vpc1",
				},
			}
		})
		patch.ApplyMethod(reflect.TypeOf(&ipAllocService.Service), "GetNamespaceUID",
			func(s *common.Service, ns string) types.UID {
				return "nsUUid"
			})
		defer patch.Reset()
		result, _, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, true)
		assert.Nil(t, err)
		assert.Equal(t, "test-ip-alloc_p26xv", *result.Id)
		assert.Equal(t, "test-ip-alloc", *result.DisplayName)
		assert.Equal(t, "1.2.3.4", *result.AllocationIps)
		assert.Equal(t, (*int64)(nil), result.AllocationSize)
		assert.Equal(t, model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV4, *result.IpAddressType)
		assert.Equal(t, "EXTERNAL", *result.IpAddressBlockVisibility)
		assert.Equal(t, (*int64)(nil), result.Ipv6AllocationPrefixLength)
		assert.Equal(t, 6, len(result.Tags))
	})

	t.Run("Handle AllocationIPs for AddressBinding CR in normal mode", func(t *testing.T) {
		ab := &v1alpha1.AddressBinding{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ab",
				Namespace: "default",
				UID:       "ab-uid1",
			},
			Spec: v1alpha1.AddressBindingSpec{
				VMName:        "vm",
				InterfaceName: "port",
			},
			Status: v1alpha1.AddressBindingStatus{
				IPAddress: "1.2.3.4",
			},
		}
		sp := &v1alpha1.SubnetPort{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-sp",
				Namespace: "default",
				UID:       "sp-uid1",
			},
		}
		result, _, err := ipAllocService.BuildIPAddressAllocation(ab, sp, false)
		assert.Nil(t, err)
		assert.Nil(t, result)
	})

	t.Run("Restore AllocationIPs for AddressBinding CR", func(t *testing.T) {
		ab := &v1alpha1.AddressBinding{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ab",
				Namespace: "default",
				UID:       "ab-uid1",
			},
			Spec: v1alpha1.AddressBindingSpec{
				VMName:        "vm",
				InterfaceName: "port",
			},
			Status: v1alpha1.AddressBindingStatus{
				IPAddress: "1.2.3.4",
			},
		}
		sp := &v1alpha1.SubnetPort{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-sp",
				Namespace: "default",
				UID:       "sp-uid1",
			},
		}
		patch := gomonkey.ApplyMethod(reflect.TypeOf(&ipAllocService.Service), "GetNamespaceUID",
			func(s *common.Service, ns string) types.UID {
				return "nsUUid"
			})
		defer patch.Reset()
		result, _, err := ipAllocService.BuildIPAddressAllocation(ab, sp, true)
		assert.Nil(t, err)
		assert.Equal(t, "test-ab_p26xv", *result.Id)
		assert.Equal(t, "test-ab", *result.DisplayName)
		assert.Equal(t, "1.2.3.4", *result.AllocationIps)
		assert.Equal(t, (*int64)(nil), result.AllocationSize)
		assert.Equal(t, model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV4, *result.IpAddressType)
		assert.Equal(t, "EXTERNAL", *result.IpAddressBlockVisibility)
		assert.Equal(t, (*int64)(nil), result.Ipv6AllocationPrefixLength)
		assert.Equal(t, 8, len(result.Tags))
	})

	t.Run("Restore IPv6 AllocationIPs for AddressBinding CR", func(t *testing.T) {
		ab := &v1alpha1.AddressBinding{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ab-ipv6",
				Namespace: "default",
				UID:       "ab-uid2",
			},
			Spec: v1alpha1.AddressBindingSpec{
				VMName:        "vm",
				InterfaceName: "port",
			},
			Status: v1alpha1.AddressBindingStatus{
				IPAddress: "2001:db8::1",
			},
		}
		sp := &v1alpha1.SubnetPort{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-sp",
				Namespace: "default",
				UID:       "sp-uid2",
			},
		}
		patch := gomonkey.ApplyMethod(reflect.TypeOf(&ipAllocService.Service), "GetNamespaceUID",
			func(s *common.Service, ns string) types.UID {
				return "nsUUid"
			})
		defer patch.Reset()
		result, _, err := ipAllocService.BuildIPAddressAllocation(ab, sp, true)
		assert.Nil(t, err)
		assert.Equal(t, "2001:db8::1", *result.AllocationIps)
		assert.Equal(t, model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV6, *result.IpAddressType)
		assert.Equal(t, (*string)(nil), result.IpAddressBlockVisibility)
		assert.Equal(t, (*int64)(nil), result.AllocationSize)
		assert.Equal(t, (*int64)(nil), result.Ipv6AllocationPrefixLength)
		assert.Equal(t, 8, len(result.Tags))
	})

	t.Run("Success case with IPBlockName and loadBalancerVPC selection", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ip-alloc-lb",
				Namespace: "default",
				UID:       "uid-lb",
				Annotations: map[string]string{
					"nsx.vmware.com/lb": "true",
				},
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityPrivate,
				AllocationSize:           4,
				IPBlockName:              "custom-block",
			},
		}

		nc := &v1alpha1.VPCNetworkConfiguration{
			Spec: v1alpha1.VPCNetworkConfigurationSpec{
				LoadBalancerVPC: "/orgs/default/projects/proj-1/vpcs/vpc-lb",
			},
		}

		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "GetVPCNetworkConfigByNamespace", func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, error) {
			return nc, nil
		})
		patch.ApplyMethod(reflect.TypeOf(&ipAllocService.Service), "GetNamespaceUID",
			func(s *common.Service, ns string) types.UID {
				return "nsUUid"
			})
		defer patch.Reset()

		result, vpcInfo, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, false)
		assert.Nil(t, err)
		assert.Equal(t, 1, len(vpcInfo))
		assert.Equal(t, "default", vpcInfo[0].OrgID)
		assert.Equal(t, "proj-1", vpcInfo[0].ProjectID)
		assert.Equal(t, "vpc-lb", vpcInfo[0].VPCID)
		assert.Equal(t, "test-ip-alloc-lb_p26xv", *result.Id)
		assert.Equal(t, "test-ip-alloc-lb", *result.DisplayName)
		assert.Equal(t, "/orgs/default/projects/proj-1/infra/ip-blocks/custom-block", *result.IpBlock)
		assert.Equal(t, int64(4), *result.AllocationSize)
		assert.Equal(t, "PRIVATE", *result.IpAddressBlockVisibility)

		// Check that the tag "nsx-op/lb" is appended
		hasLBTag := false
		for _, tag := range result.Tags {
			if *tag.Scope == "nsx-op/lb" && *tag.Tag == "true" {
				hasLBTag = true
				break
			}
		}
		assert.True(t, hasLBTag)
	})

	t.Run("Success case with IPBlockName under projects (no prefix)", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ip-alloc-infra",
				Namespace: "default",
				UID:       "uid-infra",
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityPrivate,
				AllocationSize:           4,
				IPBlockName:              "infra-block-id",
			},
		}

		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{
					OrgID:     "org1",
					ProjectID: "proj1",
					VPCID:     "vpc1",
				},
			}
		})
		patch.ApplyMethod(reflect.TypeOf(&ipAllocService.Service), "GetNamespaceUID",
			func(s *common.Service, ns string) types.UID {
				return "nsUUid"
			})
		defer patch.Reset()

		result, _, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, false)
		assert.Nil(t, err)
		assert.Equal(t, "/orgs/org1/projects/proj1/infra/ip-blocks/infra-block-id", *result.IpBlock)
	})

	t.Run("Success case with IPBlockName under infra (leading colon)", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ip-alloc-infra-colon",
				Namespace: "default",
				UID:       "uid-infra-colon",
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityPrivate,
				AllocationSize:           4,
				IPBlockName:              ":infra-block-id",
			},
		}

		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{
					OrgID:     "org1",
					ProjectID: "proj1",
					VPCID:     "vpc1",
				},
			}
		})
		patch.ApplyMethod(reflect.TypeOf(&ipAllocService.Service), "GetNamespaceUID",
			func(s *common.Service, ns string) types.UID {
				return "nsUUid"
			})
		defer patch.Reset()

		result, _, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, false)
		assert.Nil(t, err)
		assert.Equal(t, "/infra/ip-blocks/infra-block-id", *result.IpBlock)
	})

	t.Run("Failure case with IPBlockName starting with slash", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ip-alloc-slash",
				Namespace: "default",
				UID:       "uid-slash",
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityPrivate,
				AllocationSize:           4,
				IPBlockName:              "/infra/ip-blocks/custom-block",
			},
		}

		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{
					OrgID:     "org1",
					ProjectID: "proj1",
					VPCID:     "vpc1",
				},
			}
		})
		defer patch.Reset()

		result, _, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, false)
		assert.Nil(t, result)
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "only block ID or ':ipBlockID' is supported, full path is not supported")
	})

	t.Run("Failure case with only colon in IPBlockName", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ip-alloc-only-colon",
				Namespace: "default",
				UID:       "uid-only-colon",
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityPrivate,
				AllocationSize:           4,
				IPBlockName:              ":", // missing block ID
			},
		}

		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{
					OrgID:     "org1",
					ProjectID: "proj1",
					VPCID:     "vpc1",
				},
			}
		})
		defer patch.Reset()

		result, _, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, false)
		assert.Nil(t, result)
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "IP block ID cannot be empty")
	})

	t.Run("Success case with IPBlockName containing spaces", func(t *testing.T) {
		ipAlloc := &v1alpha1.IPAddressAllocation{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-ip-alloc-spaces",
				Namespace: "default",
				UID:       "uid-spaces",
			},
			Spec: v1alpha1.IPAddressAllocationSpec{
				IPAddressBlockVisibility: v1alpha1.IPAddressVisibilityPrivate,
				AllocationSize:           4,
				IPBlockName:              "  custom-block-id  ",
			},
		}

		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "ListVPCInfo", func(_ *vpc.VPCService, _ string) []common.VPCResourceInfo {
			return []common.VPCResourceInfo{
				{
					OrgID:     "org1",
					ProjectID: "proj1",
					VPCID:     "vpc1",
				},
			}
		})
		patch.ApplyMethod(reflect.TypeOf(&ipAllocService.Service), "GetNamespaceUID",
			func(s *common.Service, ns string) types.UID {
				return "nsUUid"
			})
		defer patch.Reset()

		result, _, err := ipAllocService.BuildIPAddressAllocation(ipAlloc, nil, false)
		assert.Nil(t, err)
		assert.Equal(t, "/orgs/org1/projects/proj1/infra/ip-blocks/custom-block-id", *result.IpBlock)
	})
}

func Test_convertIpAddressBlockVisibility(t *testing.T) {
	assert.Equal(t, v1alpha1.IPAddressVisibilityPrivate, convertIpAddressBlockVisibility(""))
	assert.Equal(t, v1alpha1.IPAddressVisibilityExternal, convertIpAddressBlockVisibility(v1alpha1.IPAddressVisibilityExternal))
	assert.Equal(t, v1alpha1.IPAddressVisibility("PRIVATE_TGW"), convertIpAddressBlockVisibility(v1alpha1.IPAddressVisibility("PRIVATE_TGW")))
}

func Test_ipAddressTypeToNSX(t *testing.T) {
	assert.Equal(t, model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV6, ipAddressTypeToNSX(v1alpha1.IPAllocationIPAddressTypeIPv6))
	assert.Equal(t, model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV4, ipAddressTypeToNSX(v1alpha1.IPAllocationIPAddressTypeIPv4))
	assert.Equal(t, model.VpcIpAddressAllocation_IP_ADDRESS_TYPE_IPV4, ipAddressTypeToNSX(v1alpha1.IPAllocationAddressType("UNKNOWN")))
}

func Test_getVPCInfo(t *testing.T) {
	vpcService, _, _ := createService(t)
	ipAllocService := &IPAddressAllocationService{
		VPCService: vpcService,
	}

	t.Run("isLB true, GetVPCNetworkConfigByNamespace error", func(t *testing.T) {
		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "GetVPCNetworkConfigByNamespace",
			func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, error) {
				return nil, assert.AnError
			})
		defer patch.Reset()

		res, err := ipAllocService.getVPCInfo("ns1", true)
		assert.ErrorIs(t, err, assert.AnError)
		assert.Nil(t, res)
	})

	t.Run("isLB true, invalid LoadBalancerVPC path error", func(t *testing.T) {
		nc := &v1alpha1.VPCNetworkConfiguration{
			Spec: v1alpha1.VPCNetworkConfigurationSpec{
				LoadBalancerVPC: "invalid-vpc-path",
			},
		}
		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "GetVPCNetworkConfigByNamespace",
			func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, error) {
				return nc, nil
			})
		defer patch.Reset()

		res, err := ipAllocService.getVPCInfo("ns1", true)
		assert.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("isLB true, nc is nil, returns error", func(t *testing.T) {
		patch := gomonkey.ApplyMethod(reflect.TypeOf(ipAllocService.VPCService), "GetVPCNetworkConfigByNamespace",
			func(_ *vpc.VPCService, _ string) (*v1alpha1.VPCNetworkConfiguration, error) {
				return nil, nil
			})
		defer patch.Reset()

		res, err := ipAllocService.getVPCInfo("ns1", true)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "LoadBalancerVPC is not configured on VPCNetworkConfiguration")
		assert.Nil(t, res)
	})
}
