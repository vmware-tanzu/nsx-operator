package subnetport

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	mp_model "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/mock"
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
		SubnetPortStore: setupStore(),
	}
	service.macPool = &mp_model.MacPool{
		Ranges: []mp_model.MacRange{
			{
				Start: common.String("04:50:56:00:00:00"),
				End:   common.String("04:50:56:00:ff:ff"),
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
		restore       bool
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
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_SERVER"),
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_subnetport"),
				Id:          common.String("fake_subnetport_phoia"),
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
						Scope: common.String("nsx-op/subnetport_name"),
						Tag:   common.String("fake_subnetport"),
					},
					{
						Scope: common.String("nsx-op/subnetport_uid"),
						Tag:   common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
					},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("DHCP"),
					Type_:             common.String("STATIC"),
					TrafficTag:        common.Int64(0),
				},
			},
			expectedError: nil,
		},
		{
			name: "build-NSX-port-for-restore-subnetport",
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
				Status: v1alpha1.SubnetPortStatus{
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{
							{Gateway: "10.0.0.1", IPAddress: "10.0.0.3/28"},
						},
						MACAddress: "aa:bb:cc:dd:ee:ff",
					},
				},
			},
			nsxSubnet: &model.VpcSubnet{
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_SERVER"),
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			restore:   true,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_subnetport"),
				Id:          common.String("fake_subnetport_phoia"),
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
						Scope: common.String("nsx-op/subnetport_name"),
						Tag:   common.String("fake_subnetport"),
					},
					{
						Scope: common.String("nsx-op/subnetport_uid"),
						Tag:   common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
					},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("DHCP"),
					Type_:             common.String("STATIC"),
					TrafficTag:        common.Int64(0),
				},
				AddressBindings: []model.PortAddressBindingEntry{
					{
						IpAddress:  common.String("10.0.0.3"),
						MacAddress: common.String("aa:bb:cc:dd:ee:ff"),
					},
				},
			},
			expectedError: nil,
		},
		{
			name: "build-NSX-port-for-subnetport-outside-nsx-mac-pool",
			obj: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "2ccec3b9-7546-4fd2-812a-1e3a4afd7acc",
					Name:      "fake_subnetport",
					Namespace: "fake_ns",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
					AddressBindings: []v1alpha1.PortAddressBinding{
						{
							IPAddress:  "10.0.0.1",
							MACAddress: "aa:bb:cc:dd:ee:ff",
						},
					},
				},
			},
			nsxSubnet: &model.VpcSubnet{
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_DEACTIVATED"),
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_subnetport"),
				Id:          common.String("fake_subnetport_phoia"),
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
						Scope: common.String("nsx-op/subnetport_name"),
						Tag:   common.String("fake_subnetport"),
					},
					{
						Scope: common.String("nsx-op/subnetport_uid"),
						Tag:   common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
					},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("IP_POOL"),
					Type_:             common.String("STATIC"),
					TrafficTag:        common.Int64(0),
				},
				AddressBindings: []model.PortAddressBindingEntry{
					{
						IpAddress:  common.String("10.0.0.1"),
						MacAddress: common.String("aa:bb:cc:dd:ee:ff"),
					},
				},
			},
			expectedError: nil,
		},
		{
			name: "build-NSX-port-for-subnetport-inside-nsx-mac-pool",
			obj: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "2ccec3b9-7546-4fd2-812a-1e3a4afd7acc",
					Name:      "fake_subnetport",
					Namespace: "fake_ns",
				},
				Spec: v1alpha1.SubnetPortSpec{
					Subnet: "subnet-1",
					AddressBindings: []v1alpha1.PortAddressBinding{
						{
							IPAddress:  "10.0.0.1",
							MACAddress: "04:50:56:00:fa:00",
						},
					},
				},
			},
			nsxSubnet: &model.VpcSubnet{
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_DEACTIVATED"),
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_subnetport"),
				Id:          common.String("fake_subnetport_phoia"),
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
						Scope: common.String("nsx-op/subnetport_name"),
						Tag:   common.String("fake_subnetport"),
					},
					{
						Scope: common.String("nsx-op/subnetport_uid"),
						Tag:   common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
					},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("BOTH"),
					Type_:             common.String("STATIC"),
					TrafficTag:        common.Int64(0),
				},
				AddressBindings: []model.PortAddressBindingEntry{
					{
						IpAddress:  common.String("10.0.0.1"),
						MacAddress: common.String("04:50:56:00:fa:00"),
					},
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
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_SERVER"),
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: &map[string]string{
				"kubernetes.io/metadata.name": "fake_ns",
				"vSphereClusterID":            "domain-c11",
			},
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_pod"),
				Id:          common.String("fake_pod_phoia"),
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
					{
						Scope: common.String("kubernetes.io/metadata.name"),
						Tag:   common.String("fake_ns"),
					},
					{
						Scope: common.String("vSphereClusterID"),
						Tag:   common.String("domain-c11"),
					},
				},
				Path:       common.String("fake_path/ports/fake_pod_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("DHCP"),
					Type_:             common.String("STATIC"),
					TrafficTag:        common.Int64(0),
					AppId:             common.String("c5db1800-ce4c-11de-a935-8105ba7ace78"),
					ContextId:         common.String("fake_context_id"),
				},
			},
			expectedError: nil,
		},
		{
			name: "build-NSX-port-for-restore-pod",
			obj: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					UID:         "c5db1800-ce4c-11de-a935-8105ba7ace78",
					Name:        "fake_pod",
					Namespace:   "fake_ns",
					Annotations: map[string]string{common.AnnotationPodMAC: "11:22:33:44:55:66"},
				},
				Status: corev1.PodStatus{
					PodIP: "10.0.0.1",
				},
			},
			nsxSubnet: &model.VpcSubnet{
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_SERVER"),
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: &map[string]string{
				"kubernetes.io/metadata.name": "fake_ns",
				"vSphereClusterID":            "domain-c11",
			},
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_pod"),
				Id:          common.String("fake_pod_phoia"),
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
					{
						Scope: common.String("kubernetes.io/metadata.name"),
						Tag:   common.String("fake_ns"),
					},
					{
						Scope: common.String("vSphereClusterID"),
						Tag:   common.String("domain-c11"),
					},
				},
				Path:       common.String("fake_path/ports/fake_pod_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("DHCP"),
					Type_:             common.String("STATIC"),
					TrafficTag:        common.Int64(0),
					AppId:             common.String("c5db1800-ce4c-11de-a935-8105ba7ace78"),
					ContextId:         common.String("fake_context_id"),
				},
				AddressBindings: []model.PortAddressBindingEntry{
					{
						IpAddress:  common.String("10.0.0.1"),
						MacAddress: common.String("11:22:33:44:55:66"),
					},
				},
			},
			expectedError: nil,
			restore:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedPort, err := service.buildSubnetPort(tt.obj, tt.nsxSubnet, tt.contextID, tt.labelTags, false, tt.restore)
			// Ignore attachment id as it is random
			if tt.expectedError != nil {
				assert.Equal(t, tt.expectedError, err)
			} else {
				assert.Nil(t, err)
				tt.expectedPort.Attachment.Id = observedPort.Attachment.Id
				assert.Equal(t, tt.expectedPort, observedPort)
				assert.Equal(t, common.CompareResource(SubnetPortToComparable(tt.expectedPort), SubnetPortToComparable(observedPort)), false)
			}
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
		{
			name: "MultipleAddressBinding",
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
							Namespace:         "ns",
							Name:              "AddressBinding-1",
							CreationTimestamp: metav1.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
						},
						Spec: v1alpha1.AddressBindingSpec{
							VMName:        "vm",
							InterfaceName: "port",
						},
					}, v1alpha1.AddressBinding{
						ObjectMeta: metav1.ObjectMeta{
							Namespace:         "ns",
							Name:              "AddressBinding-2",
							CreationTimestamp: metav1.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),
						},
						Spec: v1alpha1.AddressBindingSpec{
							VMName:        "vm",
							InterfaceName: "port",
						},
					})
					return nil
				})
			},
			ab: &v1alpha1.AddressBinding{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         "ns",
					Name:              "AddressBinding-2",
					CreationTimestamp: metav1.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				Spec: v1alpha1.AddressBindingSpec{
					VMName:        "vm",
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

func TestBuildExternalAddressBinding(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	commonService := common.Service{
		Client: k8sClient,
	}
	mockVPCService := mock.MockVPCServiceProvider{}
	mockIPAddressAllocationService := mock.MockIPAddressAllocationProvider{}
	service := &SubnetPortService{
		Service:                    commonService,
		VPCService:                 &mockVPCService,
		IpAddressAllocationService: &mockIPAddressAllocationService,
	}
	vpcInfo := &common.VPCResourceInfo{
		OrgID:     "default",
		ProjectID: "project-quality",
		VPCID:     "vpc-id",
	}

	tests := []struct {
		name          string
		sp            *v1alpha1.SubnetPort
		restoreMode   bool
		preFunc       func(service *SubnetPortService, mockVPC *mock.MockVPCServiceProvider, mockIPAlloc *mock.MockIPAddressAllocationProvider) *gomonkey.Patches
		expectedAb    *model.ExternalAddressBinding
		expectedError error
	}{
		{
			name: "non-restore-with-address-binding",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/vm/port"},
				},
			},
			restoreMode: false,
			preFunc: func(service *SubnetPortService, mockVPC *mock.MockVPCServiceProvider, mockIPAlloc *mock.MockIPAddressAllocationProvider) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(service), "GetAddressBindingBySubnetPort",
					func(_ *SubnetPortService, _ *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
						return &v1alpha1.AddressBinding{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "ns",
								Name:      "AddressBinding-1",
							},
							Spec: v1alpha1.AddressBindingSpec{
								InterfaceName: "port",
							},
						}
					},
				)
			},
			expectedAb:    &model.ExternalAddressBinding{},
			expectedError: nil,
		},
		{
			name: "non-restore-without-address-binding",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/vm/port"},
				},
			},
			restoreMode: false,
			preFunc: func(service *SubnetPortService, mockVPC *mock.MockVPCServiceProvider, mockIPAlloc *mock.MockIPAddressAllocationProvider) *gomonkey.Patches {
				return gomonkey.ApplyMethod(reflect.TypeOf(service), "GetAddressBindingBySubnetPort",
					func(_ *SubnetPortService, _ *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
						return nil
					})
			},
			expectedAb:    nil,
			expectedError: nil,
		},
		{
			name: "restore-with-address-binding-status",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/vm/port"},
				},
			},
			restoreMode: true,
			preFunc: func(service *SubnetPortService, mockVPC *mock.MockVPCServiceProvider, mockIPAlloc *mock.MockIPAddressAllocationProvider) *gomonkey.Patches {
				// Mock GetAddressBindingBySubnetPort
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service), "GetAddressBindingBySubnetPort",
					func(_ *SubnetPortService, _ *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
						return &v1alpha1.AddressBinding{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "ns",
								Name:      "AddressBinding-1",
							},
							Spec: v1alpha1.AddressBindingSpec{
								InterfaceName: "port",
							},
							Status: v1alpha1.AddressBindingStatus{
								IPAddress: "1.2.3.4",
							},
						}
					},
				)
				patches.ApplyMethod(reflect.TypeOf(service.VPCService), "ListVPCInfo",
					func(_ *mock.MockVPCServiceProvider, ns string) []common.VPCResourceInfo {
						return []common.VPCResourceInfo{
							*vpcInfo,
						}
					})
				patches.ApplyMethod(reflect.TypeOf(vpcInfo), "GetVPCPath",
					func(_ *common.VPCResourceInfo) string {
						return "/orgs/default/projects/project-quality/vpcs/vpc-id"
					})
				patches.ApplyMethod(reflect.TypeOf(service.IpAddressAllocationService), "GetIPAddressAllocationByOwner",
					func(_ *mock.MockIPAddressAllocationProvider, owner metav1.Object) (*model.VpcIpAddressAllocation, error) {
						return &model.VpcIpAddressAllocation{
							Id: common.String("alloc-id-123"),
						}, nil
					})
				return patches
			},
			expectedAb: &model.ExternalAddressBinding{
				AllocatedExternalIpPath: String("/orgs/default/projects/project-quality/vpcs/vpc-id/ip-address-allocations/alloc-id-123"),
			},
			expectedError: nil,
		},
		{
			name: "non-restore-with-allocated-external-ip-name",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/vm/port"},
				},
			},
			restoreMode: false,
			preFunc: func(service *SubnetPortService, mockVPC *mock.MockVPCServiceProvider, mockIPAlloc *mock.MockIPAddressAllocationProvider) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service), "GetAddressBindingBySubnetPort",
					func(_ *SubnetPortService, _ *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
						return &v1alpha1.AddressBinding{
							ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
							Spec: v1alpha1.AddressBindingSpec{
								IPAddressAllocationName: "ip1",
							},
						}
					},
				)
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Eq(types.NamespacedName{
					Namespace: "ns1",
					Name:      "ip1",
				}), gomock.AssignableToTypeOf(&v1alpha1.IPAddressAllocation{})).Return(nil)
				patches.ApplyMethod(reflect.TypeOf(service.IpAddressAllocationService), "GetIPAddressAllocationByOwner",
					func(_ *mock.MockIPAddressAllocationProvider, obj metav1.Object) (*model.VpcIpAddressAllocation, error) {
						return &model.VpcIpAddressAllocation{Path: String("/orgs/default/projects/project-quality/vpcs/vpc-id/ip-address-allocations/alloc-id-123")}, nil
					})
				return patches
			},
			expectedAb: &model.ExternalAddressBinding{
				AllocatedExternalIpPath: String("/orgs/default/projects/project-quality/vpcs/vpc-id/ip-address-allocations/alloc-id-123"),
			},
			expectedError: nil,
		},
		{
			name: "non-restore-with-allocated-external-ip-name-get-k8s-error",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/vm/port"},
				},
			},
			restoreMode: false,
			preFunc: func(service *SubnetPortService, mockVPC *mock.MockVPCServiceProvider, mockIPAlloc *mock.MockIPAddressAllocationProvider) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service), "GetAddressBindingBySubnetPort",
					func(_ *SubnetPortService, _ *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
						return &v1alpha1.AddressBinding{
							ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
							Spec: v1alpha1.AddressBindingSpec{
								IPAddressAllocationName: "ip1",
							},
						}
					},
				)
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Eq(types.NamespacedName{
					Namespace: "ns1",
					Name:      "ip1",
				}), gomock.AssignableToTypeOf(&v1alpha1.IPAddressAllocation{})).Return(fmt.Errorf("mock error"))
				return patches
			},
			expectedAb:    nil,
			expectedError: fmt.Errorf("mock error"),
		},
		{
			name: "non-restore-with-allocated-external-ip-name-get-nsx-error",
			sp: &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"nsx.vmware.com/attachment_ref": "VirtualMachine/vm/port"},
				},
			},
			restoreMode: false,
			preFunc: func(service *SubnetPortService, mockVPC *mock.MockVPCServiceProvider, mockIPAlloc *mock.MockIPAddressAllocationProvider) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(service), "GetAddressBindingBySubnetPort",
					func(_ *SubnetPortService, _ *v1alpha1.SubnetPort) *v1alpha1.AddressBinding {
						return &v1alpha1.AddressBinding{
							ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
							Spec: v1alpha1.AddressBindingSpec{
								IPAddressAllocationName: "ip1",
							},
						}
					},
				)
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Eq(types.NamespacedName{
					Namespace: "ns1",
					Name:      "ip1",
				}), gomock.AssignableToTypeOf(&v1alpha1.IPAddressAllocation{})).Return(nil)
				patches.ApplyMethod(reflect.TypeOf(service.IpAddressAllocationService), "GetIPAddressAllocationByOwner",
					func(_ *mock.MockIPAddressAllocationProvider, obj metav1.Object) (*model.VpcIpAddressAllocation, error) {
						return nil, fmt.Errorf("mock error")
					})
				return patches
			},
			expectedAb:    nil,
			expectedError: fmt.Errorf("mock error"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.preFunc(service, &mockVPCService, &mockIPAddressAllocationService)
			if patches != nil {
				defer patches.Reset()
			}
			actualAb, _ := service.buildExternalAddressBinding(tt.sp, tt.restoreMode)
			assert.Equal(t, tt.expectedAb, actualAb)
		})
	}
}
