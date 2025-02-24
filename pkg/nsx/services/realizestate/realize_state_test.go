package realizestate

import (
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

type fakeRealizedEntitiesClient struct{}

func (c *fakeRealizedEntitiesClient) List(intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
	return model.GenericPolicyRealizedResourceListResult{
		Results: []model.GenericPolicyRealizedResource{},
	}, nil
}

func TestRealizeStateService_CheckRealizeState(t *testing.T) {
	commonService := common.Service{
		NSXClient: &nsx.Client{
			RealizedEntitiesClient: &fakeRealizedEntitiesClient{},
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
	s := &RealizeStateService{
		Service: commonService,
	}

	patches := gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
		return model.GenericPolicyRealizedResourceListResult{
			Results: []model.GenericPolicyRealizedResource{
				{
					State: common.String(model.GenericPolicyRealizedResource_STATE_ERROR),
					Alarms: []model.PolicyAlarmResource{
						{Message: common.String("mocked error")},
					},
					EntityType: common.String("RealizedLogicalPort"),
				},
			},
		}, nil
	})

	backoff := wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   2.0,
		Jitter:   0,
		Steps:    6,
	}
	// default project
	err := s.CheckRealizeState(backoff, "/orgs/default/projects/default/vpcs/vpc/subnets/subnet/ports/port", []string{})

	realizeStateError, ok := err.(*nsxutil.RealizeStateError)
	assert.True(t, ok)
	assert.Equal(t, realizeStateError.Error(), "/orgs/default/projects/default/vpcs/vpc/subnets/subnet/ports/port realized with errors: [mocked error]")

	// non default project
	err = s.CheckRealizeState(backoff, "/orgs/default/projects/project-quality/vpcs/vpc/subnets/subnet/ports/port", []string{})

	realizeStateError, ok = err.(*nsxutil.RealizeStateError)
	assert.True(t, ok)
	assert.Equal(t, realizeStateError.Error(), "/orgs/default/projects/project-quality/vpcs/vpc/subnets/subnet/ports/port realized with errors: [mocked error]")

	// check with extra ids
	patches.Reset()
	patches = gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
		return model.GenericPolicyRealizedResourceListResult{
			Results: []model.GenericPolicyRealizedResource{
				{
					State:      common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
					Alarms:     []model.PolicyAlarmResource{},
					EntityType: common.String("RealizedLogicalRouterPort"),
					Id:         common.String(common.GatewayInterfaceId),
				},
				{
					State:      common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
					Alarms:     []model.PolicyAlarmResource{},
					EntityType: common.String("RealizedLogicalRouter"),
					Id:         common.String("vpc"),
				},
			},
		}, nil
	})
	err = s.CheckRealizeState(backoff, "/orgs/default/projects/project-quality/vpcs/vpc", []string{common.GatewayInterfaceId})
	assert.Equal(t, err, nil)

	// for lbs, realized with ProviderNotReady and need retry
	patches.Reset()
	patches = gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
		return model.GenericPolicyRealizedResourceListResult{
			Results: []model.GenericPolicyRealizedResource{
				{
					State: common.String(model.GenericPolicyRealizedResource_STATE_ERROR),
					Alarms: []model.PolicyAlarmResource{
						{
							Message: common.String("Realization failure"),
							ErrorDetails: &model.PolicyApiError{
								ErrorCode:    common.Int64(nsxutil.ProviderNotReadyErrorCode),
								ErrorMessage: common.String("Realization failure"),
							},
						},
					},
					EntityType: common.String("GenericPolicyRealizedResource"),
				},
			},
		}, nil
	})

	backoff = wait.Backoff{
		Duration: 10 * time.Millisecond,
		Factor:   1,
		Jitter:   0,
		Steps:    1,
	}
	err = s.CheckRealizeState(backoff, "/orgs/default/projects/default/vpcs/vpc/vpc-lbs/default", []string{})
	assert.NotEqual(t, err, nil)
	_, ok = err.(*nsxutil.RetryRealizeError)
	assert.Equal(t, ok, true)

	// for subnet, RealizedLogicalPort realized with errors
	patches.Reset()

	patches = gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
		return model.GenericPolicyRealizedResourceListResult{
			Results: []model.GenericPolicyRealizedResource{
				{
					State: common.String(model.GenericPolicyRealizedResource_STATE_ERROR),
					Alarms: []model.PolicyAlarmResource{
						{Message: common.String("mocked error")},
					},
					EntityType: common.String("RealizedLogicalPort"),
				},
				{
					State: common.String(model.GenericPolicyRealizedResource_STATE_UNREALIZED),
					Alarms: []model.PolicyAlarmResource{
						{Message: common.String("mocked error")},
					},
					EntityType: common.String("RealizedLogicalSwitch"),
				},
				{
					State:      common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
					Alarms:     []model.PolicyAlarmResource{},
					EntityType: common.String("RealizedLogicalRouterPort"),
				},
			},
		}, nil
	})
	err = s.CheckRealizeState(backoff, "/orgs/default/projects/project-quality/vpcs/vpc/subnets/subnet/", []string{})

	realizeStateError, ok = err.(*nsxutil.RealizeStateError)
	assert.True(t, ok)
	assert.Equal(t, realizeStateError.Error(), "/orgs/default/projects/project-quality/vpcs/vpc/subnets/subnet/ realized with errors: [mocked error]")

	// for subnet, realized successfully
	patches.Reset()

	patches = gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
		return model.GenericPolicyRealizedResourceListResult{
			Results: []model.GenericPolicyRealizedResource{
				{
					State:      common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
					Alarms:     []model.PolicyAlarmResource{},
					EntityType: common.String("RealizedLogicalPort"),
				},
				{
					State:      common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
					Alarms:     []model.PolicyAlarmResource{},
					EntityType: common.String("RealizedLogicalSwitch"),
				},
				{
					State:      common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
					Alarms:     []model.PolicyAlarmResource{},
					EntityType: common.String("RealizedLogicalRouterPort"),
				},
			},
		}, nil
	})
	err = s.CheckRealizeState(backoff, "/orgs/default/projects/project-quality/vpcs/vpc/subnets/subnet/", []string{})
	assert.Equal(t, err, nil)

	// for subnet, need retry
	patches.Reset()

	patches = gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
		return model.GenericPolicyRealizedResourceListResult{
			Results: []model.GenericPolicyRealizedResource{
				{
					State:      common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
					Alarms:     []model.PolicyAlarmResource{},
					EntityType: common.String("RealizedLogicalPort"),
				},
				{
					State:      common.String(model.GenericPolicyRealizedResource_STATE_UNREALIZED),
					Alarms:     []model.PolicyAlarmResource{},
					EntityType: common.String("RealizedLogicalSwitch"),
				},
				{
					State:      common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
					Alarms:     []model.PolicyAlarmResource{},
					EntityType: common.String("RealizedLogicalRouterPort"),
				},
			},
		}, nil
	})
	backoff = wait.Backoff{
		Duration: 10 * time.Millisecond,
		Factor:   1,
		Jitter:   0,
		Steps:    1,
	}
	err = s.CheckRealizeState(backoff, "/orgs/default/projects/project-quality/vpcs/vpc/subnets/subnet/", []string{})
	assert.NotEqual(t, err, nil)
	_, ok = err.(*nsxutil.RealizeStateError)
	assert.Equal(t, ok, false)
	patches.Reset()
}

func TestRealizeStateService_GetPolicyTier1UplinkPortIP(t *testing.T) {
	commonService := common.Service{
		NSXClient: &nsx.Client{
			RealizedEntitiesClient: &fakeRealizedEntitiesClient{},
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
	s := &RealizeStateService{
		Service: commonService,
	}

	testCases := []struct {
		name         string
		intentPath   string
		prepareFuncs func() *gomonkey.Patches
		wantObj      string
		wantErr      string
	}{
		{
			name:       "Test normal case",
			intentPath: "/orgs/default/projects/project-quality/vpcs/ns-vpc-uid-1",
			wantObj:    "100.64.0.3",
			prepareFuncs: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
					return model.GenericPolicyRealizedResourceListResult{
						Results: []model.GenericPolicyRealizedResource{
							{
								State:      common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
								EntityType: common.String("RealizedLogicalRouterPort"),
								IntentPaths: []string{
									"/orgs/default/projects/project-quality/vpcs/ns-vpc-uid-1",
								},
							},
							{
								State: common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
								ExtendedAttributes: []model.AttributeVal{
									{
										DataType: common.String("STRING"),
										Key:      common.String("IpAddresses"),
										Values:   []string{"100.64.0.3/31"},
									},
									{
										DataType: common.String("STRING"),
										Key:      common.String("MacAddress"),
										Values:   []string{"02:50:56:56:44:52"},
									},
								},
								EntityType: common.String("RealizedLogicalRouterPort"),
								IntentPaths: []string{
									"/orgs/default/projects/project-quality/vpcs/ns-vpc-uid-1",
								},
							},
						},
					}, nil
				})
				return patches
			},
		},
		{
			name:       "Empty list result",
			intentPath: "/orgs/default/projects/project-quality/vpcs/ns-vpc-uid-1",
			wantErr:    "tier1 uplink port IP not found",
			prepareFuncs: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
					return model.GenericPolicyRealizedResourceListResult{
						Results: []model.GenericPolicyRealizedResource{
							{},
						},
					}, nil
				})
				return patches
			},
		},
		{
			name:       "Invalid tier1 uplink port IP",
			intentPath: "/orgs/default/projects/project-quality/vpcs/ns-vpc-uid-1",
			wantErr:    "tier1 uplink port IP not found",
			prepareFuncs: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
					return model.GenericPolicyRealizedResourceListResult{
						Results: []model.GenericPolicyRealizedResource{
							{
								State: common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
								ExtendedAttributes: []model.AttributeVal{
									{
										DataType: common.String("STRING"),
										Key:      common.String("IpAddresses"),
										Values:   []string{"100.64.0.3/31/33"},
									},
								},
								EntityType: common.String("RealizedLogicalRouterPort"),
								IntentPaths: []string{
									"/orgs/default/projects/project-quality/vpcs/ns-vpc-uid-1",
								},
							},
						},
					}, nil
				})
				return patches
			},
		},
		{
			name:       "Realized error",
			intentPath: "/orgs/default/projects/project-quality/vpcs/ns-vpc-uid-1",
			wantErr:    "com.vmware.vapi.std.errors.service_unavailable",
			prepareFuncs: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
					return model.GenericPolicyRealizedResourceListResult{
						Results: []model.GenericPolicyRealizedResource{
							{
								State:      common.String(model.GenericPolicyRealizedResource_STATE_ERROR),
								EntityType: common.String("RealizedLogicalRouterPort"),
							},
						},
					}, apierrors.NewServiceUnavailable()
				})
				return patches
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.prepareFuncs != nil {
				patches := testCase.prepareFuncs()
				defer patches.Reset()
			}

			got, err := s.GetPolicyTier1UplinkPortIP(testCase.intentPath)
			if testCase.wantErr != "" {
				assert.ErrorContains(t, err, testCase.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.wantObj, got)
			}
		})
	}
}
