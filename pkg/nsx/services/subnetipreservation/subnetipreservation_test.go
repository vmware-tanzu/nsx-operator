package subnetipreservation

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type fakeQueryClient struct{}

func (c *fakeQueryClient) List(queryParam string, cursorParam *string, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.SearchResponse, error) {
	return model.SearchResponse{}, nil
}

type fakeDynamicIPReservationsClient struct{}

func (c *fakeDynamicIPReservationsClient) Delete(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string) error {
	return nil
}

func (c *fakeDynamicIPReservationsClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, anyIpReservationIdParam string) (model.DynamicIpAddressReservation, error) {
	return model.DynamicIpAddressReservation{}, nil
}

func (c *fakeDynamicIPReservationsClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.DynamicIpAddressReservationListResult, error) {
	return model.DynamicIpAddressReservationListResult{}, nil
}

func (c *fakeDynamicIPReservationsClient) Patch(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string, dynamicIpAddressReservationParam model.DynamicIpAddressReservation) (model.DynamicIpAddressReservation, error) {
	return model.DynamicIpAddressReservation{}, nil
}

func (c *fakeDynamicIPReservationsClient) Update(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string, dynamicIpAddressReservationParam model.DynamicIpAddressReservation) (model.DynamicIpAddressReservation, error) {
	return model.DynamicIpAddressReservation{}, nil
}

func TestInitializeService(t *testing.T) {
	commonService := common.Service{
		NSXClient: &nsx.Client{
			QueryClient: &fakeQueryClient{},
			NsxConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "k8scl-one:test",
				},
			},
		},
	}
	patches := gomonkey.ApplyMethodSeq(commonService.NSXClient.QueryClient, "List", []gomonkey.OutputCell{{
		Values: gomonkey.Params{model.SearchResponse{}, nil},
		Times:  1,
	}})
	defer patches.Reset()
	ipReservationService, err := InitializeService(commonService)
	require.Nil(t, err)
	if !reflect.DeepEqual(ipReservationService.Service, commonService) {
		t.Errorf("InitializeService() got = %v, want %v", ipReservationService.Service, commonService)
	}

	patches = gomonkey.ApplyMethodSeq(commonService.NSXClient.QueryClient, "List", []gomonkey.OutputCell{{
		Values: gomonkey.Params{nil, fmt.Errorf("mocked error")},
		Times:  1,
	}})
	defer patches.Reset()
	_, err = InitializeService(commonService)
	require.Contains(t, err.Error(), "mocked error")
}

func TestDeleteIPReservation(t *testing.T) {
	service := createFakeService()
	service.IPReservationStore.Apply(ipr1)
	service.IPReservationStore.Apply(ipr2)
	service.IPReservationStore.Apply(ipr3)

	patches := gomonkey.ApplyFunc(service.NSXClient.DynamicIPReservationsClient.Delete, func(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string) error {
		return nil
	})
	defer patches.Reset()
	err := service.DeleteIPReservationByCRId("ipr1-uuid")
	require.Nil(t, err)
	err = service.DeleteIPReservationByCRName("ns-2", "ipr1")
	require.Nil(t, err)
	iprs := service.IPReservationStore.List()
	require.Equal(t, 1, len(iprs))
	require.Equal(t, ipr2, iprs[0].(*model.DynamicIpAddressReservation))
}

func TestListSubnetIPReservationCRUIDsInStore(t *testing.T) {
	service := createFakeService()
	service.IPReservationStore.Apply(ipr1)
	service.IPReservationStore.Apply(ipr2)
	service.IPReservationStore.Apply(ipr3)

	ids := service.ListSubnetIPReservationCRUIDsInStore()
	require.Equal(t, 3, len(ids))
	require.True(t, ids.Has("ipr1-uuid"))
	require.True(t, ids.Has("ipr2-uuid"))
	require.True(t, ids.Has("ipr3-uuid"))
}

func TestGetOrCreateSubnetIPReservation(t *testing.T) {
	service := createFakeService()
	service.IPReservationStore.Apply(ipr1)
	tests := []struct {
		name           string
		prepareFunc    func() *gomonkey.Patches
		expectedErr    string
		expectedResult *model.DynamicIpAddressReservation
		ipReservation  *v1alpha1.SubnetIPReservation
		subnetPath     string
	}{
		{
			name: "Existed",
			ipReservation: &v1alpha1.SubnetIPReservation{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns-1",
					Name:      "ipr1",
					UID:       "ipr1-uuid",
				},
				Spec: v1alpha1.SubnetIPReservationSpec{
					NumberOfIPs: 10,
					Subnet:      "subnet-1",
				},
			},
			subnetPath:     *ipr1.ParentPath,
			expectedResult: ipr1,
		},
		{
			name: "PatchFailure",
			prepareFunc: func() *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.DynamicIPReservationsClient), "Patch", func(c *fakeDynamicIPReservationsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string, dynamicIpAddressReservationParam model.DynamicIpAddressReservation) (model.DynamicIpAddressReservation, error) {
					return model.DynamicIpAddressReservation{}, fmt.Errorf("mocked patch error")
				})
			},
			ipReservation: &v1alpha1.SubnetIPReservation{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns-1",
					Name:      "ipr2",
					UID:       "ipr2-uuid",
				},
				Spec: v1alpha1.SubnetIPReservationSpec{
					NumberOfIPs: 10,
					Subnet:      "subnet-1",
				},
			},
			subnetPath:  *ipr2.ParentPath,
			expectedErr: "mocked patch error",
		},
		{
			name: "GetFailure",
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.DynamicIPReservationsClient), "Patch", func(c *fakeDynamicIPReservationsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string, dynamicIpAddressReservationParam model.DynamicIpAddressReservation) (model.DynamicIpAddressReservation, error) {
					return model.DynamicIpAddressReservation{}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(service.NSXClient.DynamicIPReservationsClient), "Get", func(c *fakeDynamicIPReservationsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, anyIpReservationIdParam string) (model.DynamicIpAddressReservation, error) {
					return model.DynamicIpAddressReservation{}, fmt.Errorf("mocked get error")
				})
				return patches
			},
			ipReservation: &v1alpha1.SubnetIPReservation{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns-1",
					Name:      "ipr2",
					UID:       "ipr2-uuid",
				},
				Spec: v1alpha1.SubnetIPReservationSpec{
					NumberOfIPs: 10,
					Subnet:      "subnet-1",
				},
			},
			subnetPath:  *ipr2.ParentPath,
			expectedErr: "mocked get error",
		},
		{
			name: "Success",
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.DynamicIPReservationsClient), "Patch", func(c *fakeDynamicIPReservationsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string, dynamicIpAddressReservationParam model.DynamicIpAddressReservation) (model.DynamicIpAddressReservation, error) {
					return model.DynamicIpAddressReservation{}, nil
				})
				patches.ApplyMethod(reflect.TypeOf(service.NSXClient.DynamicIPReservationsClient), "Get", func(c *fakeDynamicIPReservationsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, anyIpReservationIdParam string) (model.DynamicIpAddressReservation, error) {
					return *ipr2, nil
				})
				return patches
			},
			ipReservation: &v1alpha1.SubnetIPReservation{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns-1",
					Name:      "ipr2",
					UID:       "ipr2-uuid",
				},
				Spec: v1alpha1.SubnetIPReservationSpec{
					NumberOfIPs: 10,
					Subnet:      "subnet-1",
				},
			},
			subnetPath:     *ipr2.ParentPath,
			expectedResult: ipr2,
		},
	}
	for _, tt := range tests {
		if tt.prepareFunc != nil {
			patches := tt.prepareFunc()
			defer patches.Reset()
		}
		ipr, err := service.GetOrCreateSubnetIPReservation(tt.ipReservation, tt.subnetPath)
		if tt.expectedErr != "" {
			require.Contains(t, err.Error(), tt.expectedErr)
		} else {
			require.Nil(t, err)
			require.Equal(t, tt.expectedResult, ipr)
		}
	}
}

func createFakeService() *IPReservationService {
	return &IPReservationService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				DynamicIPReservationsClient: &fakeDynamicIPReservationsClient{},
				QueryClient:                 &fakeQueryClient{},
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
		IPReservationStore: SetupStore(),
	}
}
