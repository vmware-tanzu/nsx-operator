package subnetport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func Test_subnetPortIndexPodNamespace(t *testing.T) {
	type args struct {
		obj interface{}
	}
	namespaceScope := "nsx-op/namespace"
	ns := "ns-1"
	tests := []struct {
		name           string
		expectedResult string
		expectedErr    string
		args           args
	}{
		{
			name:           "Success",
			expectedResult: ns,
			args: args{obj: &model.VpcSubnetPort{
				Tags: []model.Tag{
					{
						Scope: &namespaceScope,
						Tag:   &ns,
					},
				},
			}},
		},
		{
			name:        "Failure",
			expectedErr: "subnetPortIndexPodNamespace doesn't support unknown type",
			args:        args{obj: &ns},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := subnetPortIndexPodNamespace(tt.args.obj)
			if tt.expectedErr != "" {
				assert.EqualError(t, err, tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, 1, len(result))
				assert.Equal(t, tt.expectedResult, result[0])
			}
		})
	}
}

func Test_subnetPortIndexNamespace(t *testing.T) {
	type args struct {
		obj interface{}
	}
	namespacedNameScope := "nsx-op/vm_namespace"
	ns := "ns-1"
	tests := []struct {
		name           string
		expectedResult string
		expectedErr    string
		args           args
	}{
		{
			name:           "Success",
			expectedResult: ns,
			args: args{obj: &model.VpcSubnetPort{
				Tags: []model.Tag{
					{
						Scope: &namespacedNameScope,
						Tag:   &ns,
					},
				},
			}},
		},
		{
			name:        "Failure",
			expectedErr: "subnetPortIndexNamespace doesn't support unknown type",
			args:        args{obj: &ns},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := subnetPortIndexNamespace(tt.args.obj)
			if tt.expectedErr != "" {
				assert.EqualError(t, err, tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, 1, len(result))
				assert.Equal(t, tt.expectedResult, result[0])
			}
		})
	}
}

func Test_subnetPortIndexBySubnetID(t *testing.T) {
	type args struct {
		obj interface{}
	}
	path := "/orgs/org/projects/project/vpcs/vpc/subnets/subnet-1/ports/subnetport-1"
	tests := []struct {
		name           string
		expectedResult string
		expectedErr    string
		args           args
	}{
		{
			name:           "Success",
			expectedResult: "subnet-1",
			args: args{obj: &model.VpcSubnetPort{
				Path: &path,
			}},
		},
		{
			name:        "Failure",
			expectedErr: "subnetPortIndexBySubnetID doesn't support unknown type",
			args:        args{obj: &path},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := subnetPortIndexBySubnetID(tt.args.obj)
			if tt.expectedErr != "" {
				assert.EqualError(t, err, tt.expectedErr)
			} else {
				assert.Equal(t, 1, len(result))
				assert.Nil(t, err)
				assert.Equal(t, tt.expectedResult, result[0])
			}
		})
	}
}

func Test_subnetPortIndexByPodUID(t *testing.T) {
	type args struct {
		obj interface{}
	}
	podUIDScope := "nsx-op/pod_uid"
	podUID := "pod-1"
	tests := []struct {
		name           string
		expectedResult string
		expectedErr    string
		args           args
	}{
		{
			name:           "Success",
			expectedResult: podUID,
			args: args{obj: &model.VpcSubnetPort{
				Tags: []model.Tag{
					{
						Scope: &podUIDScope,
						Tag:   &podUID,
					},
				},
			}},
		},
		{
			name:        "Failure",
			expectedErr: "subnetPortIndexByPodUID doesn't support unknown type",
			args:        args{obj: &podUID},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := subnetPortIndexByPodUID(tt.args.obj)
			if tt.expectedErr != "" {
				assert.EqualError(t, err, tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, 1, len(result))
				assert.Equal(t, tt.expectedResult, result[0])
			}
		})
	}
}

func Test_subnetPortIndexByCRUID(t *testing.T) {
	type args struct {
		obj interface{}
	}
	portUIDScope := "nsx-op/subnetport_uid"
	portUID := "subnetport-1"
	tests := []struct {
		name           string
		expectedResult string
		expectedErr    string
		args           args
	}{
		{
			name:           "Success",
			expectedResult: portUID,
			args: args{obj: &model.VpcSubnetPort{
				Tags: []model.Tag{
					{
						Scope: &portUIDScope,
						Tag:   &portUID,
					},
				},
			}},
		},
		{
			name:        "Failure",
			expectedErr: "subnetPortIndexByCRUID doesn't support unknown type",
			args:        args{obj: &portUID},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := subnetPortIndexByCRUID(tt.args.obj)
			if tt.expectedErr != "" {
				assert.EqualError(t, err, tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, 1, len(result))
				assert.Equal(t, tt.expectedResult, result[0])
			}
		})
	}
}
