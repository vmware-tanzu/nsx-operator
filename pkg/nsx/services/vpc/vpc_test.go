package vpc

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	vpcName1          = "ns1-vpc-1"
	vpcName2          = "ns1-vpc-2"
	vpcID1            = "ns-vpc-uid-1"
	vpcID2            = "ns-vpc-uid-2"
	IPv4Type          = "IPv4"
	cluster           = "k8scl-one"
	tagValueNS        = "ns1"
	tagScopeVPCCRName = common.TagScopeVPCCRName
	tagScopeVPCCRUID  = common.TagScopeVPCCRUID
	tagValueVPCCRName = "vpcA"
	tagValueVPCCRUID  = "uidA"
	tagScopeCluster   = common.TagScopeCluster
	tagScopeNamespace = common.TagScopeNamespace

	basicTags = []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &tagValueNS,
		},
		{
			Scope: &tagScopeVPCCRName,
			Tag:   &tagValueVPCCRName,
		},
		{
			Scope: &tagScopeVPCCRUID,
			Tag:   &tagValueVPCCRUID,
		},
	}
)

func TestVPC_GetVPCsByNamespace(t *testing.T) {
	vpcCacheIndexer := cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeVPCCRUID: indexFunc})
	resourceStore := common.ResourceStore{
		Indexer:     vpcCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	vpcStore := &VPCStore{ResourceStore: resourceStore}
	service := &VPCService{
		Service: common.Service{NSXClient: nil},
	}
	service.VpcStore = vpcStore
	type args struct {
		i interface{}
		j interface{}
	}
	ns1 := "test-ns-1"
	tag1 := []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &ns1,
		},
		{
			Scope: &tagScopeVPCCRName,
			Tag:   &tagValueVPCCRName,
		},
		{
			Scope: &tagScopeVPCCRUID,
			Tag:   &tagValueVPCCRUID,
		},
	}
	ns2 := "test-ns-2"
	tag2 := []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &ns2,
		},
		{
			Scope: &tagScopeVPCCRName,
			Tag:   &tagValueVPCCRName,
		},
		{
			Scope: &tagScopeVPCCRUID,
			Tag:   &tagValueVPCCRUID,
		},
	}
	vpc1 := model.Vpc{

		DisplayName:        &vpcName1,
		Id:                 &vpcID1,
		Tags:               tag1,
		IpAddressType:      &IPv4Type,
		PrivateIpv4Blocks:  []string{"1.1.1.0/24"},
		ExternalIpv4Blocks: []string{"2.2.2.0/24"},
	}
	vpc2 := model.Vpc{

		DisplayName:        &vpcName2,
		Id:                 &vpcID2,
		Tags:               tag2,
		IpAddressType:      &IPv4Type,
		PrivateIpv4Blocks:  []string{"3.3.3.0/24"},
		ExternalIpv4Blocks: []string{"4.4.4.0/24"},
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: vpc1, j: vpc2}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vpcStore.Apply(&vpc1)
			vpcStore.GetByKey(vpcID1)
			vpcStore.Apply(&vpc2)
			got := vpcStore.List()
			if len(got) != 2 {
				t.Errorf("size = %v, want %v", len(got), 2)
			}
			vpc_list_1 := service.GetVPCsByNamespace("invalid")
			if len(vpc_list_1) != 0 {
				t.Errorf("size = %v, want %v", len(vpc_list_1), 0)
			}
			vpc_list_2 := service.GetVPCsByNamespace(ns2)
			if len(vpc_list_2) != 1 && *vpc_list_2[0].DisplayName != vpcName2 {
				t.Errorf("size = %v, want %v, display = %s, want %s", len(vpc_list_2), 1, *vpc_list_2[0].DisplayName, vpcName2)
			}
		})
	}
}

type MockSecurityPoliciesClient struct {
	SP  model.SecurityPolicy
	Err error
}

func (spClient *MockSecurityPoliciesClient) Delete(orgIdParam string, projectIdParam string, vpcIdParam string, groupIdParam string) error {
	return spClient.Err
}

func (spClient *MockSecurityPoliciesClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, securityPolicyIdParam string) (model.SecurityPolicy, error) {
	return spClient.SP, spClient.Err
}

func (spClient *MockSecurityPoliciesClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includeRuleCountParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.SecurityPolicyListResult, error) {
	return model.SecurityPolicyListResult{}, spClient.Err
}

func (spClient *MockSecurityPoliciesClient) Patch(orgIdParam string, projectIdParam string, vpcIdParam string, securityPolicyIdParam string, securityPolicyParam model.SecurityPolicy) error {
	return spClient.Err
}
func (spClient *MockSecurityPoliciesClient) Update(orgIdParam string, projectIdParam string, vpcIdParam string, securityPolicyIdParam string, securityPolicyParam model.SecurityPolicy) (model.SecurityPolicy, error) {
	return spClient.SP, spClient.Err
}
func (spClient *MockSecurityPoliciesClient) Revise(orgIdParam string, projectIdParam string, vpcIdParam string, securityPolicyIdParam string, securityPolicyParam model.SecurityPolicy, anchorPathParam *string, operationParam *string) (model.SecurityPolicy, error) {
	return model.SecurityPolicy{}, spClient.Err
}

type MockGroupClient struct {
	Group model.Group
	Err   error
}

func (groupClient *MockGroupClient) Delete(orgIdParam string, projectIdParam string, vpcIdParam string, groupIdParam string) error {
	return groupClient.Err
}

func (groupClient *MockGroupClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, groupIdParam string) (model.Group, error) {
	return groupClient.Group, groupClient.Err
}

func (groupClient *MockGroupClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, memberTypesParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.GroupListResult, error) {
	return model.GroupListResult{}, groupClient.Err
}

func (groupClient *MockGroupClient) Patch(orgIdParam string, projectIdParam string, vpcIdParam string, groupIdParam string, groupParam model.Group) error {
	return groupClient.Err
}
func (groupClient *MockGroupClient) Update(orgIdParam string, projectIdParam string, vpcIdParam string, groupIdParam string, groupParam model.Group) (model.Group, error) {
	return groupClient.Group, groupClient.Err
}

type MockRuleClient struct {
	Rule model.Rule
	Err  error
}

func (ruleClient *MockRuleClient) Delete(orgIdParam string, projectIdParam string, vpcIdParam string, securityPolicyIdParam string, ruleIdParam string) error {
	return ruleClient.Err
}

func (ruleClient *MockRuleClient) Get(orgIdParam string, projectIdParam string, vpcIdParam string, securityPolicyIdParam string, ruleIdParam string) (model.Rule, error) {
	return ruleClient.Rule, ruleClient.Err
}
func (ruleClient *MockRuleClient) List(orgIdParam string, projectIdParam string, vpcIdParam string, securityPolicyIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string) (model.RuleListResult, error) {
	return model.RuleListResult{}, ruleClient.Err
}

func (ruleClient *MockRuleClient) Patch(orgIdParam string, projectIdParam string, vpcIdParam string, securityPolicyIdParam string, ruleIdParam string, ruleParam model.Rule) error {
	return ruleClient.Err
}

func (ruleClient *MockRuleClient) Update(orgIdParam string, projectIdParam string, vpcIdParam string, securityPolicyIdParam string, ruleIdParam string, ruleParam model.Rule) (model.Rule, error) {
	return ruleClient.Rule, ruleClient.Err
}

func (ruleClient *MockRuleClient) Revise(orgIdParam string, projectIdParam string, vpcIdParam string, securityPolicyIdParam string, ruleIdParam string, ruleParam model.Rule, anchorPathParam *string, operationParam *string) (model.Rule, error) {
	return model.Rule{}, ruleClient.Err
}

func TestVPC_CreateOrUpdateAVIRule(t *testing.T) {
	aviRuleCacheIndexer := cache.NewIndexer(keyFuncAVI, nil)
	resourceStore := common.ResourceStore{
		Indexer:     aviRuleCacheIndexer,
		BindingType: model.VpcBindingType(),
	}
	ruleStore := &AviRuleStore{ResourceStore: resourceStore}
	resourceStore1 := common.ResourceStore{
		Indexer:     aviRuleCacheIndexer,
		BindingType: model.GroupBindingType(),
	}
	groupStore := &AviGroupStore{ResourceStore: resourceStore1}
	resourceStore2 := common.ResourceStore{
		Indexer:     aviRuleCacheIndexer,
		BindingType: model.SecurityPolicyBindingType(),
	}
	spStore := &AviSecurityPolicyStore{ResourceStore: resourceStore2}

	service := &VPCService{
		Service: common.Service{NSXClient: nil},
	}

	service.RuleStore = ruleStore
	service.GroupStore = groupStore
	service.SecurityPolicyStore = spStore

	ns1 := "test-ns-1"
	tag1 := []model.Tag{
		{
			Scope: &tagScopeCluster,
			Tag:   &cluster,
		},
		{
			Scope: &tagScopeNamespace,
			Tag:   &ns1,
		},
		{
			Scope: &tagScopeVPCCRName,
			Tag:   &tagValueVPCCRName,
		},
		{
			Scope: &tagScopeVPCCRUID,
			Tag:   &tagValueVPCCRUID,
		},
	}
	path1 := "/orgs/default/projects/project_1/vpcs/vpc1"
	vpc1 := model.Vpc{
		Path:               &path1,
		DisplayName:        &vpcName1,
		Id:                 &vpcID1,
		Tags:               tag1,
		IpAddressType:      &IPv4Type,
		PrivateIpv4Blocks:  []string{"1.1.1.0/24"},
		ExternalIpv4Blocks: []string{"2.2.2.0/24"},
	}

	// feature not supported
	enableAviAllowRule = false
	err := service.CreateOrUpdateAVIRule(&vpc1, ns1)
	assert.Equal(t, err, nil)

	// enable feature
	enableAviAllowRule = true
	spClient := MockSecurityPoliciesClient{}

	service.NSXClient = &nsx.Client{}
	service.NSXClient.VPCSecurityClient = &spClient
	service.NSXConfig = &config.NSXOperatorConfig{}
	service.NSXConfig.CoeConfig = &config.CoeConfig{}
	service.NSXConfig.Cluster = "k8scl_one"
	sppath1 := "/orgs/default/projects/project_1/vpcs/vpc1/security-policies/sp1"
	sp := model.SecurityPolicy{
		Path: &sppath1,
	}

	// security policy not found
	spClient.SP = sp
	notFound := errors.New("avi security policy not found")
	spClient.Err = notFound
	err = service.CreateOrUpdateAVIRule(&vpc1, ns1)
	assert.Equal(t, err, notFound)

	// security policy found, get rule, failed to get external CIDR
	rulepath1 := fmt.Sprintf("/orgs/default/projects/project_1/vpcs/ns-vpc-uid-1/security-policies/default-layer3-section/rules/%s", AviSEIngressAllowRuleId)
	rule := model.Rule{
		Path:              &rulepath1,
		DestinationGroups: []string{"2.2.2.0/24"},
	}
	ruleStore.Add(rule)
	spClient.Err = nil
	resulterr := errors.New("get external ipblock failed")
	patch := gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "getIpblockCidr", func(_ *VPCService, cidr []string) ([]string, error) {
		return []string{}, resulterr
	})
	err = service.CreateOrUpdateAVIRule(&vpc1, ns1)
	patch.Reset()
	assert.Equal(t, err, resulterr)

	// security policy found, get rule, get external CIDR which matched
	spClient.Err = nil
	resulterr = errors.New("get external ipblock failed")
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "getIpblockCidr", func(_ *VPCService, cidr []string) ([]string, error) {
		return []string{"2.2.2.0/24"}, nil
	})
	err = service.CreateOrUpdateAVIRule(&vpc1, ns1)
	patch.Reset()
	assert.Equal(t, err, nil)

	// security policy found, get external CIDR, create group failed
	patch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(service), "getIpblockCidr", func(_ *VPCService, cidr []string) ([]string, error) {
		return []string{"192.168.0.0/16"}, nil
	})
	defer patch.Reset()
	groupClient := MockGroupClient{Err: nil}
	service.NSXClient.VpcGroupClient = &groupClient
	grouppath1 := "/orgs/default/projects/project_1/vpcs/vpc1/groups/group1"
	group := model.Group{
		Path: &grouppath1,
	}
	groupClient.Group = group
	groupClient.Err = errors.New("create avi group error")
	service.NSXConfig = &config.NSXOperatorConfig{}
	service.NSXConfig.CoeConfig = &config.CoeConfig{}
	service.NSXConfig.Cluster = "k8scl-one"
	err = service.CreateOrUpdateAVIRule(&vpc1, ns1)
	assert.Equal(t, err, groupClient.Err)

	// security policy found, get external CIDR, create group, create rule failed
	groupClient.Err = nil
	ruleClient := MockRuleClient{}
	service.NSXClient.VPCRuleClient = &ruleClient

	ruleClient.Rule = rule
	ruleClient.Err = errors.New("create avi rule error")
	err = service.CreateOrUpdateAVIRule(&vpc1, ns1)
	assert.Equal(t, err, ruleClient.Err)

	// security policy found, get external CIDR, create group, create rule
	ruleClient.Err = nil
	err = service.CreateOrUpdateAVIRule(&vpc1, ns1)
	assert.Equal(t, err, nil)
}
