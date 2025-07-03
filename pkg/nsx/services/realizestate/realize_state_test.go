package realizestate

import (
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
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

	// for subnetport, realized with IPAllocationError
	patches = gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
		return model.GenericPolicyRealizedResourceListResult{
			Results: []model.GenericPolicyRealizedResource{
				{
					State: common.String(model.GenericPolicyRealizedResource_STATE_ERROR),
					Alarms: []model.PolicyAlarmResource{
						{
							Message: common.String("Realization failure"),
							ErrorDetails: &model.PolicyApiError{
								ErrorCode:    common.Int64(nsxutil.IPAllocationErrorCode),
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
	realizedError, ok := err.(*nsxutil.RealizeStateError)
	assert.Equal(t, ok, true)
	assert.Equal(t, nsxutil.IPAllocationErrorCode, realizedError.GetCode())
	patches.Reset()
}
