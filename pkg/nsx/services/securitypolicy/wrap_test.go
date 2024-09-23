package securitypolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type (
	fakeQueryClient       struct{}
	fakeInfraClient       struct{}
	fakeOrgClient         struct{}
	fakeSecurityClient    struct{}
	fakeVPCSecurityClient struct{}
)

func (_ *fakeQueryClient) List(_ string, _ *string, _ *string, _ *int64, _ *bool, _ *string) (model.SearchResponse, error) {
	cursor := "2"
	resultCount := int64(2)
	return model.SearchResponse{
		Results: []*data.StructValue{{}},
		Cursor:  &cursor, ResultCount: &resultCount,
	}, nil
}

func (f fakeInfraClient) Get(basePathParam *string, filterParam *string, typeFilterParam *string) (model.Infra, error) {
	return model.Infra{}, nil
}

func (f fakeInfraClient) Update(infraParam model.Infra) (model.Infra, error) {
	return model.Infra{}, nil
}

func (f fakeInfraClient) Patch(infraParam model.Infra, enforceRevisionCheckParam *bool) error {
	return nil
}

func (f fakeSecurityClient) Delete(domainIdParam string, securityPolicyIdParam string) error {
	return nil
}

func (f fakeSecurityClient) Get(domainIdParam string, securityPolicyIdParam string) (model.SecurityPolicy, error) {
	return model.SecurityPolicy{}, nil
}

func (f fakeSecurityClient) List(domainIdParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool, includeRuleCountParam *bool, includedFieldsParam *string,
	pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string,
) (model.SecurityPolicyListResult, error) {
	return model.SecurityPolicyListResult{}, nil
}

func (f fakeSecurityClient) Patch(domainIdParam string, securityPolicyIdParam string, securityPolicyParam model.SecurityPolicy) error {
	return nil
}

func (f fakeSecurityClient) Revise(domainIdParam string, securityPolicyIdParam string, securityPolicyParam model.SecurityPolicy,
	anchorPathParam *string, operationParam *string,
) (model.SecurityPolicy, error) {
	return model.SecurityPolicy{}, nil
}

func (f fakeSecurityClient) Update(domainIdParam string, securityPolicyIdParam string, securityPolicyParam model.SecurityPolicy) (model.SecurityPolicy, error) {
	return model.SecurityPolicy{}, nil
}

func (f fakeOrgClient) Get(basePathParam *string, filterParam *string, typeFilterParam *string) (model.OrgRoot, error) {
	return model.OrgRoot{}, nil
}

func (f fakeOrgClient) Patch(orgRootParam model.OrgRoot, enforceRevisionCheckParam *bool) error {
	return nil
}

func (f fakeVPCSecurityClient) Delete(orgIDParam string, projectIDParam string, vpcIDParam string, securityPolicyIdParam string) error {
	return nil
}

func (f fakeVPCSecurityClient) Get(orgIDParam string, projectIDParam string, vpcIDParam string, securityPolicyIdParam string) (model.SecurityPolicy, error) {
	return model.SecurityPolicy{}, nil
}

func (f fakeVPCSecurityClient) List(orgIDParam string, projectIDParam string, vpcIDParam string, cursorParam *string, includeMarkForDeleteObjectsParam *bool,
	includeRuleCountParam *bool, includedFieldsParam *string, pageSizeParam *int64, sortAscendingParam *bool, sortByParam *string,
) (model.SecurityPolicyListResult, error) {
	return model.SecurityPolicyListResult{}, nil
}

func (f fakeVPCSecurityClient) Patch(orgIDParam string, projectIDParam string, vpcIDParam string, securityPolicyIdParam string, securityPolicyParam model.SecurityPolicy) error {
	return nil
}

func (f fakeVPCSecurityClient) Revise(orgIDParam string, projectIDParam string, vpcIDParam string, securityPolicyIdParam string, securityPolicyParam model.SecurityPolicy,
	anchorPathParam *string, operationParam *string,
) (model.SecurityPolicy, error) {
	return model.SecurityPolicy{}, nil
}

func (f fakeVPCSecurityClient) Update(orgIDParam string, projectIDParam string, vpcIDParam string, securityPolicyIdParam string, securityPolicyParam model.SecurityPolicy) (model.SecurityPolicy, error) {
	return model.SecurityPolicy{}, nil
}

func fakeSecurityPolicyService() *SecurityPolicyService {
	c := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(c)
	rc, _ := cluster.NewRestConnector()
	fakeService := &SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient:       &fakeQueryClient{},
				InfraClient:       &fakeInfraClient{},
				SecurityClient:    &fakeSecurityClient{},
				OrgRootClient:     &fakeOrgClient{},
				VPCSecurityClient: &fakeVPCSecurityClient{},
				RestConnector:     rc,
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: clusterName,
					},
				},
			},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: clusterName,
				},
			},
		},
	}
	return fakeService
}

func TestSecurityPolicyService_wrapSecurityPolicy(t *testing.T) {
	Converter := bindings.NewTypeConverter()
	service := fakeSecurityPolicyService()
	mId, mTag, mScope := "11111", "11111", "nsx-op/security_policy_cr_uid"
	markDelete := true
	s := model.SecurityPolicy{
		Id:              &mId,
		Tags:            []model.Tag{{Tag: &mTag, Scope: &mScope}},
		MarkedForDelete: &markDelete,
	}
	type args struct {
		sp *model.SecurityPolicy
	}
	tests := []struct {
		name    string
		args    args
		want    []*data.StructValue
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{&s}, nil, assert.Error},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := service.wrapSecurityPolicy(tt.args.sp)
			for _, v := range got {
				sp, _ := Converter.ConvertToGolang(v, model.ChildSecurityPolicyBindingType())
				spc := sp.(model.ChildSecurityPolicy)
				assert.Equal(t, mId, *spc.Id)
				assert.Equal(t, MarkedForDelete, *spc.MarkedForDelete)
				assert.NotNil(t, spc.SecurityPolicy)
			}
		})
	}
}

func TestSecurityPolicyService_wrapGroups(t *testing.T) {
	Converter := bindings.NewTypeConverter()
	service := fakeSecurityPolicyService()
	mId, mTag, mScope := "11111", "11111", "nsx-op/security_policy_cr_uid"
	markDelete := true
	m := model.Group{
		Id:              &mId,
		Tags:            []model.Tag{{Tag: &mTag, Scope: &mScope}},
		MarkedForDelete: &markDelete,
	}
	ms := []model.Group{m}
	type args struct {
		groups []model.Group
	}
	tests := []struct {
		name    string
		args    args
		want    []*data.StructValue
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{ms}, nil, assert.Error},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := service.wrapGroups(tt.args.groups)
			for _, v := range got {
				g, _ := Converter.ConvertToGolang(v, model.ChildGroupBindingType())
				gc := g.(model.ChildGroup)
				assert.Equal(t, mId, *gc.Id)
				assert.Equal(t, MarkedForDelete, *gc.MarkedForDelete)
				assert.NotNil(t, gc.Group)
			}
		})
	}
}

func TestSecurityPolicyService_wrapRules(t *testing.T) {
	Converter := bindings.NewTypeConverter()
	service := fakeSecurityPolicyService()
	mId, mTag, mScope := "11111", "11111", "nsx-op/security_policy_cr_uid"
	markDelete := true
	r := model.Rule{
		Id:              &mId,
		Tags:            []model.Tag{{Tag: &mTag, Scope: &mScope}},
		MarkedForDelete: &markDelete,
	}
	rs := []model.Rule{r}
	type args struct {
		rules []model.Rule
	}
	tests := []struct {
		name    string
		args    args
		want    []*data.StructValue
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{rs}, nil, assert.Error},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := service.wrapRules(tt.args.rules)
			for _, v := range got {
				r, _ := Converter.ConvertToGolang(v, model.ChildRuleBindingType())
				rc := r.(model.ChildRule)
				assert.Equal(t, mId, *rc.Id)
				assert.Equal(t, MarkedForDelete, *rc.MarkedForDelete)
				assert.NotNil(t, rc.Rule)
			}
		})
	}
}

func TestSecurityPolicyService_wrapResourceReference(t *testing.T) {
	Converter := bindings.NewTypeConverter()
	service := fakeSecurityPolicyService()
	type args struct {
		children []*data.StructValue
	}
	tests := []struct {
		name    string
		args    args
		want    []*data.StructValue
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{[]*data.StructValue{}}, nil, assert.NoError},
	}

	domainId := getDomain(service)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := service.wrapDomainResource(tt.args.children, domainId)
			for _, v := range got {
				r, _ := Converter.ConvertToGolang(v, model.ChildResourceReferenceBindingType())
				rc := r.(model.ChildResourceReference)
				assert.Equal(t, "k8scl-one", *rc.Id)
				assert.Equal(t, "ChildResourceReference", rc.ResourceType)
				assert.NotNil(t, "Domain", rc.TargetType)
			}
		})
	}
}
