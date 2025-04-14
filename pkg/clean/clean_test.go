package clean

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/go-logr/logr"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/inventory"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipaddressallocation"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	sr "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/staticroute"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetbinding"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

var (
	cf = &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{NsxApiManagers: []string{"10.0.0.1"}}}
)

func TestClean_ValidationFailed(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()
	debug := false
	logLevel := 0
	patches := gomonkey.ApplyMethod(reflect.TypeOf(cf.NsxConfig), "ValidateConfigFromCmd", func(_ *config.NsxConfig) error {
		return errors.New("validation failed")
	})

	defer patches.Reset()

	err := Clean(ctx, cf, &log, debug, logLevel)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestClean_GetClientFailed(t *testing.T) {
	ctx := context.Background()

	log := logr.Discard()
	debug := false
	logLevel := 0

	patches := gomonkey.ApplyMethod(reflect.TypeOf(cf.NsxConfig), "ValidateConfigFromCmd", func(_ *config.NsxConfig) error {
		return nil
	})
	defer patches.Reset()
	patches.ApplyFunc(nsx.GetClient, func(_ *config.NSXOperatorConfig) *nsx.Client {
		return nil
	})

	err := Clean(ctx, cf, &log, debug, logLevel)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get nsx client")
}

func TestClean_InitError(t *testing.T) {
	ctx := context.Background()

	log := logr.Discard()
	debug := false
	logLevel := 0

	patches := gomonkey.ApplyMethod(reflect.TypeOf(cf.NsxConfig), "ValidateConfigFromCmd", func(_ *config.NsxConfig) error {
		return nil
	})
	defer patches.Reset()
	patches.ApplyFunc(nsx.GetClient, func(_ *config.NSXOperatorConfig) *nsx.Client {
		return &nsx.Client{}
	})

	patches.ApplyFunc(InitializeCleanupService, func(_ *config.NSXOperatorConfig, _ *nsx.Client, _ *logr.Logger) (*CleanupService, error) {
		return nil, errors.New("init cleanup service failed")
	})

	err := Clean(ctx, cf, &log, debug, logLevel)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "init cleanup service failed")
}

func TestClean_Cleanup(t *testing.T) {
	ctx := context.Background()

	debug := false
	logLevel := 0

	patches := gomonkey.ApplyMethod(reflect.TypeOf(cf.NsxConfig), "ValidateConfigFromCmd", func(_ *config.NsxConfig) error {
		return nil
	})
	defer patches.Reset()
	patches.ApplyFunc(nsx.GetClient, func(_ *config.NSXOperatorConfig) *nsx.Client {
		return &nsx.Client{}
	})

	cleanupService := &CleanupService{
		vpcService: &vpc.VPCService{},
	}
	clean := &MockCleanup{}
	cleanupService.AddCleanupService(func() (interface{}, error) {
		return clean, nil
	})

	patches.ApplyFunc(InitializeCleanupService, func(_ *config.NSXOperatorConfig, _ *nsx.Client, _ *logr.Logger) (*CleanupService, error) {
		return cleanupService, nil
	})
	patches.ApplyMethod(reflect.TypeOf(cleanupService.vpcService), "ListAutoCreatedVPCPaths", func(_ *vpc.VPCService) sets.Set[string] {
		return sets.New[string]("/orgs/default/projects/p1/vpcs/vpc-1")
	})
	patches.ApplyMethod(reflect.TypeOf(cleanupService.vpcService), "DeleteVPC", func(_ *vpc.VPCService, path string) error {
		return nil
	})

	err := Clean(ctx, cf, nil, debug, logLevel)
	assert.Nil(t, err)
	assert.True(t, clean.vpcPreCleanupCalled)
	assert.True(t, clean.vpcChildrenCleanupCalled)
	assert.True(t, clean.infraCleanupCalled)
	assert.ElementsMatch(t, []string{"/orgs/default/projects/p1/vpcs/vpc-1", ""}, clean.cleanedVPCs)
}

type MockCleanup struct {
	CleanupFunc              func(ctx context.Context) error
	vpcPreCleanupCalled      bool
	vpcChildrenCleanupCalled bool
	infraCleanupCalled       bool

	cleanedVPCs []string
}

func (m *MockCleanup) Cleanup(ctx context.Context) error {
	return m.CleanupFunc(ctx)
}

func (m *MockCleanup) CleanupBeforeVPCDeletion(ctx context.Context) error {
	m.vpcPreCleanupCalled = true
	return nil
}

func (m *MockCleanup) CleanupVPCChildResources(ctx context.Context, vpcPath string) error {
	m.vpcChildrenCleanupCalled = true
	m.cleanedVPCs = append(m.cleanedVPCs, vpcPath)
	return nil
}

func (m *MockCleanup) CleanupInfraResources(ctx context.Context) error {
	m.infraCleanupCalled = true
	return nil
}

func TestInitializeCleanupService_Success(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	nsxClient := &nsx.Client{}
	cf := &config.NSXOperatorConfig{}

	patches := gomonkey.ApplyFunc(vpc.InitializeVPC, func(service common.Service) (*vpc.VPCService, error) {
		return &vpc.VPCService{}, nil
	})
	defer patches.Reset()

	patches.ApplyFunc(subnet.InitializeSubnetService, func(service common.Service) (*subnet.SubnetService, error) {
		return &subnet.SubnetService{}, nil
	})
	patches.ApplyFunc(securitypolicy.InitializeSecurityPolicy, func(service common.Service, vpcService common.VPCServiceProvider, forCleanup bool) (*securitypolicy.SecurityPolicyService, error) {
		return &securitypolicy.SecurityPolicyService{}, nil
	})
	patches.ApplyFunc(sr.InitializeStaticRoute, func(service common.Service, vpcService common.VPCServiceProvider) (*sr.StaticRouteService, error) {
		return &sr.StaticRouteService{}, nil
	})
	patches.ApplyFunc(subnetport.InitializeSubnetPort, func(service common.Service) (*subnetport.SubnetPortService, error) {
		return &subnetport.SubnetPortService{}, nil
	})
	patches.ApplyFunc(ipaddressallocation.InitializeIPAddressAllocation, func(service common.Service, vpcService common.VPCServiceProvider, flag bool) (*ipaddressallocation.IPAddressAllocationService, error) {
		return &ipaddressallocation.IPAddressAllocationService{}, nil
	})
	patches.ApplyFunc(subnetbinding.InitializeService, func(service common.Service) (*subnetbinding.BindingService, error) {
		return &subnetbinding.BindingService{}, nil
	})
	patches.ApplyFunc(inventory.InitializeService, func(service common.Service, _ bool) (*inventory.InventoryService, error) {
		return &inventory.InventoryService{}, nil
	})

	cleanupService, err := InitializeCleanupService(cf, nsxClient, nil)
	assert.NoError(t, err)
	assert.NotNil(t, cleanupService)
	assert.Len(t, cleanupService.vpcPreCleaners, 4)
	assert.Len(t, cleanupService.vpcChildrenCleaners, 5)
	assert.Len(t, cleanupService.infraCleaners, 2)
}

func TestInitializeCleanupService_VPCError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	nsxClient := &nsx.Client{}
	cf := &config.NSXOperatorConfig{}

	expectedError := errors.New("vpc init error")
	patches := gomonkey.ApplyFunc(vpc.InitializeVPC, func(service common.Service) (*vpc.VPCService, error) {
		return nil, expectedError
	})
	defer patches.Reset()
	patches.ApplyFunc(subnet.InitializeSubnetService, func(service common.Service) (*subnet.SubnetService, error) {
		return &subnet.SubnetService{}, nil
	})
	patches.ApplyFunc(securitypolicy.InitializeSecurityPolicy, func(service common.Service, vpcService common.VPCServiceProvider, forCleanup bool) (*securitypolicy.SecurityPolicyService, error) {
		return &securitypolicy.SecurityPolicyService{}, nil
	})
	patches.ApplyFunc(sr.InitializeStaticRoute, func(service common.Service, vpcService common.VPCServiceProvider) (*sr.StaticRouteService, error) {
		return &sr.StaticRouteService{}, nil
	})
	patches.ApplyFunc(subnetport.InitializeSubnetPort, func(service common.Service) (*subnetport.SubnetPortService, error) {
		return &subnetport.SubnetPortService{}, nil
	})
	patches.ApplyFunc(ipaddressallocation.InitializeIPAddressAllocation, func(service common.Service, vpcService common.VPCServiceProvider, flag bool) (*ipaddressallocation.IPAddressAllocationService, error) {
		return &ipaddressallocation.IPAddressAllocationService{}, nil
	})
	patches.ApplyFunc(subnetbinding.InitializeService, func(service common.Service) (*subnetbinding.BindingService, error) {
		return &subnetbinding.BindingService{}, nil
	})

	cleanupService, err := InitializeCleanupService(cf, nsxClient, nil)
	assert.NoError(t, err)
	assert.NotNil(t, cleanupService)
	// Note, the services added after VPCService should fail because of the error returned in `InitializeVPC`.
	assert.Len(t, cleanupService.vpcChildrenCleaners, 3)
	assert.Len(t, cleanupService.vpcPreCleaners, 2)
	assert.Len(t, cleanupService.infraCleaners, 1)
	assert.Equal(t, expectedError, cleanupService.svcErr)
}
