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

func fakeService() *SecurityPolicyService {
	c := nsx.NewConfig("1.1.1.1", "1", "1", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(c)
	rc, _ := cluster.NewRestConnector()
	service = &SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient:   &fakeQueryClient{},
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
	}
	return service
}

func TestSecurityPolicyService_wrapSecurityPolicy(t *testing.T) {
	Converter := bindings.NewTypeConverter()
	Converter.SetMode(bindings.REST)
	service := fakeService()
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
	Converter.SetMode(bindings.REST)
	service := fakeService()
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
	Converter.SetMode(bindings.REST)
	service := fakeService()
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
	Converter.SetMode(bindings.REST)
	service := fakeService()
	type args struct {
		children []*data.StructValue
	}
	var children []*data.StructValue
	serviceEntry := data.NewStructValue(
		"",
		map[string]data.DataValue{
			"l4_protocol":   data.NewStringValue("TCP"),
			"resource_type": data.NewStringValue("L4PortSetServiceEntry"),
			// adding the following default values to make it easy when compare the existing object from store and the new built object
			"marked_for_delete": data.NewBooleanValue(false),
			"overridden":        data.NewBooleanValue(false),
		},
	)
	children = append(children, serviceEntry)
	tests := []struct {
		name    string
		args    args
		want    []*data.StructValue
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{[]*data.StructValue{}}, nil, assert.NoError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := service.wrapResourceReference(tt.args.children)
			for _, v := range got {
				r, _ := Converter.ConvertToGolang(v, model.ChildResourceReferenceBindingType())
				rc := r.(model.ChildResourceReference)
				assert.Equal(t, "k8scl-one:test", *rc.Id)
				assert.Equal(t, "ChildResourceReference", rc.ResourceType)
				assert.NotNil(t, "Domain", rc.TargetType)
			}
		})
	}
}
