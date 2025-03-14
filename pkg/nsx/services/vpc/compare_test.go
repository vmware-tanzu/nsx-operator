package vpc

import (
	"testing"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestIsVPCChanged(t *testing.T) {
	type args struct {
		nc  v1alpha1.VPCNetworkConfiguration
		vpc *model.Vpc
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "no change",
			args: args{
				nc:  v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{PrivateIPs: []string{"1.1.1.1"}}},
				vpc: &model.Vpc{PrivateIps: []string{"1.1.1.1"}},
			},
			want: false,
		},
		{
			name: "changed",
			args: args{
				nc:  v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{PrivateIPs: []string{"1.1.1.1", "2.2.2.2"}}},
				vpc: &model.Vpc{PrivateIps: []string{"1.1.1.1"}},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsVPCChanged(tt.args.nc, tt.args.vpc); got != tt.want {
				t.Errorf("IsVPCChanged() = %v, want %v", got, tt.want)
			}
		})
	}
}
