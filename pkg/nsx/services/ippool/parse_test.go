package ippool

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha2"
)

func TestIPPoolService_FullyRealized(t *testing.T) {
	ipPoolService := fakeService()
	ipPool := &v1alpha2.IPPool{
		Spec: v1alpha2.IPPoolSpec{
			Subnets: []v1alpha2.SubnetRequest{
				{
					Name: "subnet1",
				},
			},
		},
		Status: v1alpha2.IPPoolStatus{
			Subnets: []v1alpha2.SubnetResult{
				{
					Name: "subnet1",
					CIDR: "1.1.1/24",
				},
			},
		},
	}

	ipPool2 := &v1alpha2.IPPool{
		Spec: v1alpha2.IPPoolSpec{
			Subnets: []v1alpha2.SubnetRequest{
				{
					Name: "subnet1",
				},
			},
		},
		Status: v1alpha2.IPPoolStatus{
			Subnets: []v1alpha2.SubnetResult{
				{
					Name: "subnet1",
				},
			},
		},
	}
	type args struct {
		obj *v1alpha2.IPPool
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{"fully realized", args{ipPool}, true},
		{"not fully realized", args{ipPool2}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, ipPoolService.FullyRealized(tt.args.obj), "FullyRealized(%v)", tt.args.obj)
		})
	}
}

func TestIPPoolService_GetUnrealizedSubnetNames(t *testing.T) {
	ipPoolService := fakeService()

	ipPool2 := &v1alpha2.IPPool{
		Spec: v1alpha2.IPPoolSpec{
			Subnets: []v1alpha2.SubnetRequest{
				{
					Name: "subnet1",
				},
			},
		},
		Status: v1alpha2.IPPoolStatus{
			Subnets: []v1alpha2.SubnetResult{
				{
					Name: "subnet1",
				},
			},
		},
	}
	type args struct {
		obj *v1alpha2.IPPool
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{"1", args{ipPool2}, []string{"subnet1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, ipPoolService.GetUnrealizedSubnetNames(tt.args.obj), "GetUnrealizedSubnetNames(%v)", tt.args.obj)
		})
	}
}
