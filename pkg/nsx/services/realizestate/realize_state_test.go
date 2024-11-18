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
)

type fakeRealizedEntitiesClient struct{}

func (c *fakeRealizedEntitiesClient) List(orgIdParam string, projectIdParam string, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
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
	patches := gomonkey.ApplyFunc((*fakeRealizedEntitiesClient).List, func(c *fakeRealizedEntitiesClient, orgIdParam string, projectIdParam string, intentPathParam string, sitePathParam *string) (model.GenericPolicyRealizedResourceListResult, error) {
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
	defer patches.Reset()

	backoff := wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   2.0,
		Jitter:   0,
		Steps:    6,
	}
	err := s.CheckRealizeState(backoff, "/orgs/default/projects/default/vpcs/vpc/subnets/subnet/ports/port", "RealizedLogicalPort")

	realizeStateError, ok := err.(*RealizeStateError)
	assert.True(t, ok)
	assert.Equal(t, realizeStateError.Error(), "RealizedLogicalPort realized with errors: [mocked error]")
}
