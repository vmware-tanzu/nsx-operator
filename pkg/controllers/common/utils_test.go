package common

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestGetVirtualMachineNameForSubnetPort(t *testing.T) {
	type args struct {
		subnetPort *v1alpha1.SubnetPort
	}
	type want struct {
		vm   string
		port string
		err  error
	}
	tests := []struct {
		name string
		args args
		want want
	}{
		{
			"port_with_annotation",
			args{&v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"nsx.vmware.com/attachment_ref": "virtualmachine/abc/port1",
					},
				}}},
			want{vm: "abc", port: "port1", err: nil},
		},
		{
			"port_without_annotation",
			args{&v1alpha1.SubnetPort{}},
			want{vm: "", port: "", err: nil},
		},
		{
			"port_with_invalid_annotation",
			args{&v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"nsx.vmware.com/attachment_ref": "invalid/abc",
					},
				}}},
			want{vm: "", port: "", err: fmt.Errorf("invalid annotation value of 'nsx.vmware.com/attachment_ref': invalid/abc")},
		},
	}
	for _, tt := range tests {
		got1, got2, err := GetVirtualMachineNameForSubnetPort(tt.args.subnetPort)
		assert.Equal(t, err, tt.want.err)
		if got1 != tt.want.vm {
			t.Errorf("%s failed: got %s, want %s", tt.name, got1, tt.want.vm)
		}
		if got2 != tt.want.port {
			t.Errorf("%s failed: got %s, want %s", tt.name, got2, tt.want.port)
		}
	}
}
