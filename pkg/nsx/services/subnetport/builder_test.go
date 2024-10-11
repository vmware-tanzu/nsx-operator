package subnetport

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestBuildSubnetPort(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &SubnetPortService{
		Service: common.Service{
			Client:    k8sClient,
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
				CoeConfig: &config.CoeConfig{
					Cluster: "fake_cluster",
				},
			},
		},
	}
	ctx := context.Background()
	namespace := &corev1.Namespace{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), namespace).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			return nil
		}).AnyTimes()

	tests := []struct {
		name          string
		obj           interface{}
		nsxSubnet     *model.VpcSubnet
		contextID     string
		labelTags     *map[string]string
		expectedPort  *model.VpcSubnetPort
		expectedError error
	}{
		{
			name: "build-NSX-port-for-subnetport",
			obj: &v1alpha1.SubnetPort{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1alpha1",
					Kind:       "SubnetPort",
				},
				ObjectMeta: metav1.ObjectMeta{
					UID:       "2ccec3b9-7546-4fd2-812a-1e3a4afd7acc",
					Name:      "fake_subnetport",
					Namespace: "fake_ns",
				},
			},
			nsxSubnet: &model.VpcSubnet{
				DhcpConfig: &model.VpcSubnetDhcpConfig{
					EnableDhcp: common.Bool(true),
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_subnetport"),
				Id:          common.String("fake_subnetport_2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
				Tags: []model.Tag{
					{
						Scope: common.String("nsx-op/cluster"),
						Tag:   common.String("fake_cluster"),
					},
					{
						Scope: common.String("nsx-op/version"),
						Tag:   common.String("1.0.0"),
					},
					{
						Scope: common.String("nsx-op/vm_namespace"),
						Tag:   common.String("fake_ns"),
					},
					{
						Scope: common.String("nsx-op/subnetport_name"),
						Tag:   common.String("fake_subnetport"),
					},
					{
						Scope: common.String("nsx-op/subnetport_uid"),
						Tag:   common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
					},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("DHCP"),
					Type_:             common.String("STATIC"),
					Id:                common.String("32636365-6333-4239-ad37-3534362d3466"),
					TrafficTag:        common.Int64(0),
				},
			},
			expectedError: nil,
		},
		{
			name: "build-NSX-port-for-pod",
			obj: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					UID:       "c5db1800-ce4c-11de-a935-8105ba7ace78",
					Name:      "fake_pod",
					Namespace: "fake_ns",
				},
			},
			nsxSubnet: &model.VpcSubnet{
				DhcpConfig: &model.VpcSubnetDhcpConfig{
					EnableDhcp: common.Bool(true),
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_pod"),
				Id:          common.String("fake_pod_c5db1800-ce4c-11de-a935-8105ba7ace78"),
				Tags: []model.Tag{
					{
						Scope: common.String("nsx-op/cluster"),
						Tag:   common.String("fake_cluster"),
					},
					{
						Scope: common.String("nsx-op/version"),
						Tag:   common.String("1.0.0"),
					},
					{
						Scope: common.String("nsx-op/namespace"),
						Tag:   common.String("fake_ns"),
					},
					{
						Scope: common.String("nsx-op/pod_name"),
						Tag:   common.String("fake_pod"),
					},
					{
						Scope: common.String("nsx-op/pod_uid"),
						Tag:   common.String("c5db1800-ce4c-11de-a935-8105ba7ace78"),
					},
				},
				Path:       common.String("fake_path/ports/fake_pod_c5db1800-ce4c-11de-a935-8105ba7ace78"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("DHCP"),
					Type_:             common.String("STATIC"),
					Id:                common.String("63356462-3138-4030-ad63-6534632d3131"),
					TrafficTag:        common.Int64(0),
					AppId:             common.String("c5db1800-ce4c-11de-a935-8105ba7ace78"),
					ContextId:         common.String("fake_context_id"),
				},
			},
			expectedError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedPort, err := service.buildSubnetPort(tt.obj, tt.nsxSubnet, tt.contextID, tt.labelTags)
			assert.Equal(t, tt.expectedPort, observedPort)
			assert.Equal(t, common.CompareResource(SubnetPortToComparable(tt.expectedPort), SubnetPortToComparable(observedPort)), false)
			assert.Equal(t, tt.expectedError, err)
		})
	}

}

func TestGetAddressBindingBySubnetPort(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	commonService := common.Service{
		Client: k8sClient,
	}
	service := &SubnetPortService{
		Service: commonService,
	}

	tests := []struct {
		name    string
		sp      *v1alpha1.SubnetPort
		ab      *v1alpha1.AddressBinding
		preFunc func()
	}{
		{
			name: "Succuss",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/vm/port"},
				},
			},
			preFunc: func() {
				abList := &v1alpha1.AddressBindingList{}
				k8sClient.EXPECT().List(gomock.Any(), abList, gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.AddressBindingList)
					a.Items = append(a.Items, v1alpha1.AddressBinding{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ns",
							Name:      "AddressBinding-1",
						},
					})
					return nil
				})
				subnetPortList := &v1alpha1.SubnetPortList{}
				k8sClient.EXPECT().List(gomock.Any(), subnetPortList, gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetPortList)
					a.Items = append(a.Items, v1alpha1.SubnetPort{})
					return nil
				})
			},
			ab: &v1alpha1.AddressBinding{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      "AddressBinding-1",
				},
			},
		},
		{
			name: "InvalidAnnotation",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/non-existed"},
				},
			},
			preFunc: func() {
			},
		},
		{
			name: "NoAnnotation",
			sp:   &v1alpha1.SubnetPort{},
			preFunc: func() {
			},
		},
		{
			name: "ListAddressBindingFailure",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/vm/port"},
				},
			},
			preFunc: func() {
				abList := &v1alpha1.AddressBindingList{}
				k8sClient.EXPECT().List(gomock.Any(), abList, gomock.Any()).Return(fmt.Errorf("mock error"))
			},
		},
		{
			name: "ListSubnetPortFailure",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/vm/port"},
				},
			},
			preFunc: func() {
				abList := &v1alpha1.AddressBindingList{}
				k8sClient.EXPECT().List(gomock.Any(), abList, gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.AddressBindingList)
					a.Items = append(a.Items, v1alpha1.AddressBinding{})
					return nil
				})
				subnetPortList := &v1alpha1.SubnetPortList{}
				k8sClient.EXPECT().List(gomock.Any(), subnetPortList, gomock.Any()).Return(fmt.Errorf("mock error"))
			},
		},
		{
			name: "MultipleSubnetPort",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/vm/port"},
				},
			},
			preFunc: func() {
				abList := &v1alpha1.AddressBindingList{}
				k8sClient.EXPECT().List(gomock.Any(), abList, gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.AddressBindingList)
					a.Items = append(a.Items, v1alpha1.AddressBinding{})
					return nil
				})
				subnetPortList := &v1alpha1.SubnetPortList{}
				k8sClient.EXPECT().List(gomock.Any(), subnetPortList, gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.SubnetPortList)
					a.Items = append(a.Items, v1alpha1.SubnetPort{}, v1alpha1.SubnetPort{})
					return nil
				})
			},
		},
		{
			name: "PortInInterfaceName",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/vm/port"},
				},
			},
			preFunc: func() {
				abList := &v1alpha1.AddressBindingList{}
				k8sClient.EXPECT().List(gomock.Any(), abList, gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					a := list.(*v1alpha1.AddressBindingList)
					a.Items = append(a.Items, v1alpha1.AddressBinding{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ns",
							Name:      "AddressBinding-1",
						},
						Spec: v1alpha1.AddressBindingSpec{
							InterfaceName: "port",
						},
					})
					return nil
				})
			},
			ab: &v1alpha1.AddressBinding{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      "AddressBinding-1",
				},
				Spec: v1alpha1.AddressBindingSpec{
					InterfaceName: "port",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.preFunc()
			actualAb := service.GetAddressBindingBySubnetPort(tt.sp)
			assert.Equal(t, tt.ab, actualAb)
		})
	}

}
