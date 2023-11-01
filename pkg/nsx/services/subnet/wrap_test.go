package subnet

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

func fakeService() *SubnetService {
	c := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(c)
	rc, _ := cluster.NewRestConnector()
	service := &SubnetService{
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

func TestSubnetService_wrapSubnet(t *testing.T) {
	Converter := bindings.NewTypeConverter()
	Converter.SetMode(bindings.REST)
	service := fakeService()
	mId, mTag, mScope := "11111", "11111", "nsx-op/subnet_cr_uid"
	markDelete := true
	s := model.VpcSubnet{
		Id:              &mId,
		Tags:            []model.Tag{{Tag: &mTag, Scope: &mScope}},
		MarkedForDelete: &markDelete,
	}
	type args struct {
		subnet *model.VpcSubnet
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
			got, _ := service.wrapSubnet(tt.args.subnet)
			for _, v := range got {
				subnet, _ := Converter.ConvertToGolang(v, model.ChildVpcSubnetBindingType())
				child := subnet.(model.ChildVpcSubnet)
				assert.Equal(t, mId, *child.Id)
				assert.Equal(t, MarkedForDelete, *child.MarkedForDelete)
				assert.NotNil(t, child.VpcSubnet)
			}
		})
	}
}
