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
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
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

func (c *fakeDynamicIPReservationsClient) Patch(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string, dynamicIpAddressReservationParam model.DynamicIpAddressReservation) error {
	return nil
}

func (c *fakeDynamicIPReservationsClient) Update(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string, dynamicIpAddressReservationParam model.DynamicIpAddressReservation) (model.DynamicIpAddressReservation, error) {
	return model.DynamicIpAddressReservation{}, nil
}

type fakeStaticIPReservationsClient struct{}

func (c *fakeStaticIPReservationsClient) Delete(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, staticIpReservationIdParam string) error {
	return nil
}

func (c *fakeStaticIPReservationsClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, staticIpReservationIdParam string) (model.StaticIpAddressReservation, error) {
	return model.StaticIpAddressReservation{}, nil
}

func (c *fakeStaticIPReservationsClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.StaticIpAddressReservationListResult, error) {
	return model.StaticIpAddressReservationListResult{}, nil
}

func (c *fakeStaticIPReservationsClient) Patch(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, staticIpReservationIdParam string, staticIpAddressReservationParam model.StaticIpAddressReservation) error {
	return nil
}

func (c *fakeStaticIPReservationsClient) Update(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, staticIpReservationIdParam string, staticIpAddressReservationParam model.StaticIpAddressReservation) (model.StaticIpAddressReservation, error) {
	return model.StaticIpAddressReservation{}, nil
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
	subnetPortService := &subnetport.SubnetPortService{}
	patches := gomonkey.ApplyMethodFunc(commonService.NSXClient.QueryClient, "List",
		func(query string, cursor *string, fields *string, size *int64, asc *bool, sort *string) (model.SearchResponse, error) {
			return model.SearchResponse{}, nil
		},
	)
	defer patches.Reset()
	ipReservationService, err := InitializeService(commonService, subnetPortService)
	require.Nil(t, err)
	if !reflect.DeepEqual(ipReservationService.Service, commonService) {
		t.Errorf("InitializeService() got = %v, want %v", ipReservationService.Service, commonService)
	}

	patches = gomonkey.ApplyMethodFunc(commonService.NSXClient.QueryClient, "List",
		func(query string, cursor *string, fields *string, size *int64, asc *bool, sort *string) (model.SearchResponse, error) {
			return model.SearchResponse{}, fmt.Errorf("mocked error")
		},
	)
	defer patches.Reset()
	_, err = InitializeService(commonService, subnetPortService)
	require.Contains(t, err.Error(), "mocked error")
}

func TestDeleteIPReservation(t *testing.T) {
	service := createFakeService()
	service.DynamicIPReservationStore.Apply(ipr1)
	service.DynamicIPReservationStore.Apply(ipr2)
	service.DynamicIPReservationStore.Apply(ipr3)

	patches := gomonkey.ApplyFunc(service.NSXClient.DynamicIPReservationsClient.Delete, func(orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string) error {
		return nil
	})
	defer patches.Reset()
	err := service.DeleteIPReservationByCRId("ipr1-uuid")
	require.Nil(t, err)
	err = service.DeleteIPReservationByCRName("ns-2", "ipr1")
	require.Nil(t, err)
	iprs := service.DynamicIPReservationStore.List()
	require.Equal(t, 1, len(iprs))
	require.Equal(t, ipr2, iprs[0].(*model.DynamicIpAddressReservation))

	service.StaticIPReservationStore.Apply(staticIpr1)
	err = service.DeleteIPReservationByCRName("ns-1", "sipr1")
	require.NoError(t, err)
	require.Equal(t, 0, len(service.StaticIPReservationStore.List()))

	service.StaticIPReservationStore.Apply(staticIpr1)
	err = service.DeleteIPReservationByCRId("sipr1-uuid")
	require.NoError(t, err)
	require.Equal(t, 0, len(service.StaticIPReservationStore.List()))
}

func TestListSubnetIPReservationCRUIDsInStore(t *testing.T) {
	service := createFakeService()
	service.DynamicIPReservationStore.Apply(ipr1)
	service.DynamicIPReservationStore.Apply(ipr2)
	service.DynamicIPReservationStore.Apply(ipr3)
	service.StaticIPReservationStore.Apply(staticIpr1)

	ids := service.ListSubnetIPReservationCRUIDsInStore()
	require.Equal(t, 4, len(ids))
	require.True(t, ids.Has("ipr1-uuid"))
	require.True(t, ids.Has("ipr2-uuid"))
	require.True(t, ids.Has("ipr3-uuid"))
	require.True(t, ids.Has("sipr1-uuid"))
}

func TestGetOrCreateDynamicIPReservation(t *testing.T) {
	service := createFakeService()
	service.DynamicIPReservationStore.Apply(ipr1)
	tests := []struct {
		name          string
		prepareFunc   func() *gomonkey.Patches
		expectedErr   string
		expectedIPs   []string
		ipReservation *v1alpha1.SubnetIPReservation
		subnetPath    string
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
			subnetPath:  *ipr1.ParentPath,
			expectedIPs: ipr1.Ips,
		},
		{
			name: "PatchFailure",
			prepareFunc: func() *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.DynamicIPReservationsClient), "Patch", func(c *fakeDynamicIPReservationsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string, dynamicIpAddressReservationParam model.DynamicIpAddressReservation) error {
					return fmt.Errorf("mocked patch error")
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
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.DynamicIPReservationsClient), "Patch", func(c *fakeDynamicIPReservationsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string, dynamicIpAddressReservationParam model.DynamicIpAddressReservation) error {
					return nil
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
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient.DynamicIPReservationsClient), "Patch", func(c *fakeDynamicIPReservationsClient, orgIdParam string, projectIdParam string, vpcIdParam string, subnetIdParam string, dynamicIpReservationIdParam string, dynamicIpAddressReservationParam model.DynamicIpAddressReservation) error {
					return nil
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
			subnetPath:  *ipr2.ParentPath,
			expectedIPs: ipr2.Ips,
		},
	}
	for _, tt := range tests {
		if tt.prepareFunc != nil {
			patches := tt.prepareFunc()
			defer patches.Reset()
		}
		ips, err := service.GetOrCreateDynamicIPReservation(tt.ipReservation, tt.subnetPath)
		if tt.expectedErr != "" {
			require.Contains(t, err.Error(), tt.expectedErr)
		} else {
			require.Nil(t, err)
			require.Equal(t, tt.expectedIPs, ips)
		}
	}
}
func TestCreateOrUpdateSubnetIPReservation(t *testing.T) {
	tests := []struct {
		name          string
		ipReservation *v1alpha1.SubnetIPReservation
		subnetPath    string
		restoreMode   bool
		prepareFunc   func() *gomonkey.Patches
		expectedIPs   []string
		expectedErr   string
	}{
		{
			name: "WithReservedIPs",
			ipReservation: &v1alpha1.SubnetIPReservation{
				ObjectMeta: v1.ObjectMeta{Name: "sipr", Namespace: "ns-1", UID: "sipr-uid"},
				Spec:       v1alpha1.SubnetIPReservationSpec{Subnet: "subnet-1", ReservedIPs: []string{"192.168.1.1"}},
			},
			subnetPath:  "/orgs/org1/projects/proj1/vpcs/vpc1/subnets/sub1",
			restoreMode: false,
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&fakeStaticIPReservationsClient{}), "Patch", func(_ *fakeStaticIPReservationsClient, _ string, _ string, _ string, _ string, _ string, _ model.StaticIpAddressReservation) error {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(&fakeStaticIPReservationsClient{}), "Get", func(_ *fakeStaticIPReservationsClient, _ string, _ string, _ string, _ string, _ string) (model.StaticIpAddressReservation, error) {
					return model.StaticIpAddressReservation{Id: common.String("sipr-1"), ReservedIps: []string{"192.168.1.1"}}, nil
				})
				return patches
			},
			expectedIPs: []string{"192.168.1.1"},
		},
		{
			name: "RestoreMode",
			ipReservation: &v1alpha1.SubnetIPReservation{
				ObjectMeta: v1.ObjectMeta{Name: "sipr", Namespace: "ns-1", UID: "sipr-uid"},
				Spec:       v1alpha1.SubnetIPReservationSpec{Subnet: "subnet-1", NumberOfIPs: 5},
				Status:     v1alpha1.SubnetIPReservationStatus{IPs: []string{"10.0.0.1", "10.0.0.2"}},
			},
			subnetPath:  "/orgs/org1/projects/proj1/vpcs/vpc1/subnets/sub1",
			restoreMode: true,
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&fakeStaticIPReservationsClient{}), "Patch", func(_ *fakeStaticIPReservationsClient, _ string, _ string, _ string, _ string, _ string, _ model.StaticIpAddressReservation) error {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(&fakeStaticIPReservationsClient{}), "Get", func(_ *fakeStaticIPReservationsClient, _ string, _ string, _ string, _ string, _ string) (model.StaticIpAddressReservation, error) {
					return model.StaticIpAddressReservation{Id: common.String("sipr-1"), ReservedIps: []string{"10.0.0.1", "10.0.0.2"}}, nil
				})
				return patches
			},
			expectedIPs: []string{"10.0.0.1", "10.0.0.2"},
		},
		{
			name: "NumberOfIPs_NormalMode",
			ipReservation: &v1alpha1.SubnetIPReservation{
				ObjectMeta: v1.ObjectMeta{Name: "ipr", Namespace: "ns-1", UID: "ipr-uid"},
				Spec:       v1alpha1.SubnetIPReservationSpec{Subnet: "subnet-1", NumberOfIPs: 10},
			},
			subnetPath:  *ipr1.ParentPath,
			restoreMode: false,
			prepareFunc: func() *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(&fakeDynamicIPReservationsClient{}), "Patch", func(_ *fakeDynamicIPReservationsClient, _ string, _ string, _ string, _ string, _ string, _ model.DynamicIpAddressReservation) error {
					return nil
				})
				patches.ApplyMethod(reflect.TypeOf(&fakeDynamicIPReservationsClient{}), "Get", func(_ *fakeDynamicIPReservationsClient, _ string, _ string, _ string, _ string, _ string) (model.DynamicIpAddressReservation, error) {
					return model.DynamicIpAddressReservation{Id: common.String("dipr-1"), Ips: []string{"10.0.0.1-10.0.0.10"}}, nil
				})
				return patches
			},
			expectedIPs: []string{"10.0.0.1-10.0.0.10"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createFakeService()
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc()
				defer patches.Reset()
			}
			ips, err := service.CreateOrUpdateSubnetIPReservation(tt.ipReservation, tt.subnetPath, tt.restoreMode)
			if tt.expectedErr != "" {
				require.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedIPs, ips)
			}
		})
	}
}

func createFakeService() *IPReservationService {
	mockPortSvc := new(pkgmock.MockSubnetPortServiceProvider)
	return &IPReservationService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				DynamicIPReservationsClient: &fakeDynamicIPReservationsClient{},
				StaticIPReservationsClient:  &fakeStaticIPReservationsClient{},
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
		DynamicIPReservationStore: SetupDynamicIPReservationStore(),
		StaticIPReservationStore:  SetupStaticIPReservationStore(),
		SubnetPortService:         mockPortSvc,
	}
}
