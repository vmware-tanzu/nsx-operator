package common

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
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

func TestAllocateSubnetFromSubnetSet(t *testing.T) {
	expectedSubnetPath := "subnet-path-1"
	subnetSize := int64(32)
	tests := []struct {
		name           string
		prepareFunc    func(*testing.T, servicecommon.VPCServiceProvider, servicecommon.SubnetServiceProvider, servicecommon.SubnetPortServiceProvider)
		expectedErr    string
		expectedResult string
	}{
		{
			name: "AvailableSubnet",
			prepareFunc: func(t *testing.T, vsp servicecommon.VPCServiceProvider, ssp servicecommon.SubnetServiceProvider, spsp servicecommon.SubnetPortServiceProvider) {
				ssp.(*servicecommon.MockSubnetServiceProvider).On("GetSubnetsByIndex", mock.Anything, mock.Anything).
					Return([]*model.VpcSubnet{
						{
							Id:             servicecommon.String("id-1"),
							Path:           &expectedSubnetPath,
							Ipv4SubnetSize: &subnetSize,
							IpAddresses:    []string{"10.0.0.1/28"},
						},
					})
				spsp.(*servicecommon.MockSubnetPortServiceProvider).On("GetPortsOfSubnet", mock.Anything).Return([]*model.VpcSubnetPort{})
			},
			expectedResult: expectedSubnetPath,
		},
		{
			name: "ListVPCFailure",
			prepareFunc: func(t *testing.T, vsp servicecommon.VPCServiceProvider, ssp servicecommon.SubnetServiceProvider, spsp servicecommon.SubnetPortServiceProvider) {
				ssp.(*servicecommon.MockSubnetServiceProvider).On("GetSubnetsByIndex", mock.Anything, mock.Anything).
					Return([]*model.VpcSubnet{})
				ssp.(*servicecommon.MockSubnetServiceProvider).On("GenerateSubnetNSTags", mock.Anything)
				vsp.(*servicecommon.MockVPCServiceProvider).On("ListVPCInfo", mock.Anything).Return([]servicecommon.VPCResourceInfo{})
			},
			expectedErr: "no VPC found",
		},
		{
			name: "CreateSubnet",
			prepareFunc: func(t *testing.T, vsp servicecommon.VPCServiceProvider, ssp servicecommon.SubnetServiceProvider, spsp servicecommon.SubnetPortServiceProvider) {
				ssp.(*servicecommon.MockSubnetServiceProvider).On("GetSubnetsByIndex", mock.Anything, mock.Anything).
					Return([]*model.VpcSubnet{})
				ssp.(*servicecommon.MockSubnetServiceProvider).On("GenerateSubnetNSTags", mock.Anything)
				vsp.(*servicecommon.MockVPCServiceProvider).On("ListVPCInfo", mock.Anything).Return([]servicecommon.VPCResourceInfo{{}})
				ssp.(*servicecommon.MockSubnetServiceProvider).On("CreateOrUpdateSubnet", mock.Anything, mock.Anything, mock.Anything).Return(expectedSubnetPath, nil)
			},
			expectedResult: expectedSubnetPath,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vps := &servicecommon.MockVPCServiceProvider{}
			ssp := &servicecommon.MockSubnetServiceProvider{}
			spsp := &servicecommon.MockSubnetPortServiceProvider{}
			tt.prepareFunc(t, vps, ssp, spsp)
			subnetPath, err := AllocateSubnetFromSubnetSet(&v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnetset-1",
					Namespace: "ns-1",
				},
			}, vps, ssp, spsp)
			if tt.expectedErr != "" {
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, expectedSubnetPath, subnetPath)
			}
		})
	}
}

func TestGetDefaultSubnetSet(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	subnetSet := v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			UID:  "uuid-1",
			Name: "subnetset-1",
		},
	}
	tests := []struct {
		name           string
		prepareFunc    func(*testing.T)
		expectedErr    string
		expectedResult *v1alpha1.SubnetSet
	}{
		{
			name: "NamespaceNotFound",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("failed to get Namespace"))
			},
			expectedErr: "failed to get Namespace",
		},
		{
			name: "SharedNamespace",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
					namespaceCR := obj.(*v1.Namespace)
					namespaceCR.Annotations = make(map[string]string)
					namespaceCR.Annotations[servicecommon.AnnotationSharedVPCNamespace] = "sharedNamespace"
					return nil
				})
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetSetList)
					a.Items = append(a.Items, subnetSet)
					return nil
				})
			},
			expectedResult: &subnetSet,
		},
		{
			name: "ListSubnetSetFailure",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("failed to list SubnetSet"))
			},
			expectedErr: "failed to list SubnetSet",
		},
		{
			name: "NoDefaultSubnetSet",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			},
			expectedErr: "default subnetset not found",
		},
		{
			name: "MultipleDefaultSubnetSet",
			prepareFunc: func(t *testing.T) {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetSetList)
					a.Items = append(a.Items, subnetSet)
					a.Items = append(a.Items, subnetSet)
					return nil
				})
			},
			expectedErr: "default subnetset multiple default subnetsets found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.prepareFunc(t)
			GetDefaultSubnetSet(k8sClient, context.TODO(), "ns-1", "")
		})
	}

}
