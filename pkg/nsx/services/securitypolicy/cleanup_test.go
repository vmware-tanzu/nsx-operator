package securitypolicy

import (
	"context"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_org_root "github.com/vmware-tanzu/nsx-operator/pkg/mock/orgrootclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	projectPath         = "/orgs/default/projects/project-1"
	infraShareId        = "infra-share"
	projectShareId      = "proj-share"
	infraGroupId        = "infra-group"
	projectGroupId      = "proj-group"
	vpcPath             = fmt.Sprintf("%s/vpcs/vpc-1", projectPath)
	vpcRuleId           = "rule0"
	vpcGroupId          = "vpc-group"
	vpcSecurityPolicyId = "security-policy"

	infraResourceTags = []model.Tag{
		{
			Scope: String(common.TagScopeSecurityPolicyCRUID),
			Tag:   String("test-security-policy-cr-id"),
		}, {
			Scope: String(common.TagScopeNetworkPolicyUID),
			Tag:   String("test-network-policy-id"),
		},
	}
)

func TestCleanupInfraResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	orgRootClient := mock_org_root.NewMockOrgRootClient(ctrl)
	svc := prepareServiceForCleanup(orgRootClient)

	infraShare := &model.Share{
		Id:         String(infraShareId),
		Path:       String(fmt.Sprintf("/infra/shares/%s", infraShareId)),
		ParentPath: String("/infra"),
		Tags:       infraResourceTags,
	}
	svc.infraShareStore.Add(infraShare)

	projectShare := &model.Share{
		Id:         String(projectShareId),
		Path:       String(fmt.Sprintf("%s/shares/%s", projectPath, infraShareId)),
		ParentPath: String(projectPath),
		Tags:       infraResourceTags,
	}
	svc.projectShareStore.Add(projectShare)

	infraGroup := &model.Group{
		Id:         String(infraGroupId),
		Path:       String(fmt.Sprintf("/infra/domains/default/groups/%s", infraGroupId)),
		ParentPath: String("/infra/domains/default"),
		Tags:       infraResourceTags,
	}
	svc.infraGroupStore.Add(infraGroup)

	projectGroup := &model.Group{
		Id:         String(projectGroupId),
		Path:       String(fmt.Sprintf("%s/infra/domains/default/groups/%s", projectPath, projectGroupId)),
		ParentPath: String(fmt.Sprintf("%s/infra/domains/default", projectPath)),
		Tags:       infraResourceTags,
	}
	svc.projectGroupStore.Add(projectGroup)

	assert.Equal(t, 1, len(svc.infraShareStore.List()))
	assert.Equal(t, 1, len(svc.infraGroupStore.List()))
	assert.Equal(t, 1, len(svc.projectGroupStore.List()))
	assert.Equal(t, 1, len(svc.projectShareStore.List()))

	patches := gomonkey.ApplyMethodSeq(svc.NSXClient.InfraClient, "Patch", []gomonkey.OutputCell{{
		Values: gomonkey.Params{nil},
		Times:  2,
	}})
	defer patches.Reset()
	orgRootClient.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	ctx := context.Background()
	err := svc.CleanupInfraResources(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(svc.infraShareStore.List()))
	assert.Equal(t, 0, len(svc.infraGroupStore.List()))
	assert.Equal(t, 0, len(svc.projectGroupStore.List()))
	assert.Equal(t, 0, len(svc.projectShareStore.List()))
}

func TestCleanupVPCChildResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	securityPolicyPath := fmt.Sprintf("%s/security-policies/%s", vpcPath, vpcSecurityPolicyId)
	securityPolicy := &model.SecurityPolicy{
		Id:         String(vpcSecurityPolicyId),
		Path:       String(securityPolicyPath),
		ParentPath: String(vpcPath),
		Tags:       infraResourceTags,
	}
	vpcRule := &model.Rule{
		Id:         String(vpcRuleId),
		Path:       String(fmt.Sprintf("%s/rules/%s", securityPolicyPath, vpcRuleId)),
		ParentPath: String(securityPolicyPath),
		Tags:       infraResourceTags,
	}
	vpcGroup := &model.Group{
		Id:         String(vpcGroupId),
		Path:       String(fmt.Sprintf("%s/security-policies/%s", vpcPath, vpcGroupId)),
		ParentPath: String(vpcPath),
		Tags:       infraResourceTags,
	}

	for _, tc := range []struct {
		name    string
		mockFn  func() *mock_org_root.MockOrgRootClient
		vpcPath string
	}{
		{
			name: "clean up with a given VPC path",
			mockFn: func() *mock_org_root.MockOrgRootClient {
				return mock_org_root.NewMockOrgRootClient(ctrl)
			},
			vpcPath: vpcPath,
		}, {
			name: "clean up with all resources",
			mockFn: func() *mock_org_root.MockOrgRootClient {
				client := mock_org_root.NewMockOrgRootClient(ctrl)
				client.EXPECT().Patch(gomock.Any(), gomock.Any()).Return(nil).Times(3)
				return client
			},
			vpcPath: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orgRootClient := tc.mockFn()
			svc := prepareServiceForCleanup(orgRootClient)
			svc.ruleStore.Add(vpcRule)
			rulesBeforeCleanup := svc.ruleStore.GetByIndex(common.IndexByVPCPathFuncKey, vpcPath)
			assert.Equal(t, 1, len(rulesBeforeCleanup))

			svc.securityPolicyStore.Add(securityPolicy)
			securityPoliciesBeforeCleanup := svc.securityPolicyStore.GetByIndex(common.IndexByVPCPathFuncKey, vpcPath)
			assert.Equal(t, 1, len(securityPoliciesBeforeCleanup))

			svc.groupStore.Add(vpcGroup)
			groupsBeforeCleanup := svc.groupStore.GetByIndex(common.IndexByVPCPathFuncKey, vpcPath)
			assert.Equal(t, 1, len(groupsBeforeCleanup))

			ctx := context.Background()
			err := svc.CleanupVPCChildResources(ctx, tc.vpcPath)
			require.NoError(t, err)
			if tc.vpcPath != "" {
				rulesAfterCleanup := svc.ruleStore.GetByIndex(common.IndexByVPCPathFuncKey, vpcPath)
				assert.Equal(t, 0, len(rulesAfterCleanup))

				securityPoliciesAfterCleanup := svc.securityPolicyStore.GetByIndex(common.IndexByVPCPathFuncKey, vpcPath)
				assert.Equal(t, 0, len(securityPoliciesAfterCleanup))

				groupsAfterCleanup := svc.groupStore.GetByIndex(common.IndexByVPCPathFuncKey, vpcPath)
				assert.Equal(t, 0, len(groupsAfterCleanup))
			} else {
				assert.Equal(t, 0, len(svc.ruleStore.List()))
				assert.Equal(t, 0, len(svc.securityPolicyStore.List()))
				assert.Equal(t, 0, len(svc.groupStore.List()))
			}
		})
	}
}

func prepareServiceForCleanup(orgRootClient *mock_org_root.MockOrgRootClient) *SecurityPolicyService {
	svc := &SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				OrgRootClient: orgRootClient,
				InfraClient:   &fakeInfraClient{},
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: "k8scl-one:test",
					},
				},
			},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster:          "k8scl-one:test",
					EnableVPCNetwork: true,
				},
			},
		},
	}
	svc.setUpStore(common.TagScopeSecurityPolicyUID, true)
	svc.securityPolicyBuilder, _ = common.PolicyPathVpcSecurityPolicy.NewPolicyTreeBuilder()
	svc.ruleBuilder, _ = common.PolicyPathVpcSecurityPolicyRule.NewPolicyTreeBuilder()
	svc.groupBuilder, _ = common.PolicyPathVpcGroup.NewPolicyTreeBuilder()
	svc.infraShareBuilder, _ = common.PolicyPathInfraShare.NewPolicyTreeBuilder()
	svc.projectShareBuilder, _ = common.PolicyPathProjectShare.NewPolicyTreeBuilder()
	svc.projectGroupBuilder, _ = common.PolicyPathProjectGroup.NewPolicyTreeBuilder()
	svc.infraGroupBuilder, _ = common.PolicyPathInfraGroup.NewPolicyTreeBuilder()
	return svc
}
