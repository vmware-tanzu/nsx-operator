package subnetport

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/mock"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestBuildSubnetPort(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	nsxClient := &nsx.Client{}
	service := &SubnetPortService{
		Service: common.Service{
			Client:    k8sClient,
			NSXClient: nsxClient,
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

	patches := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion",
		func(_ *nsx.Client, _ int) bool {
			return true
		})
	defer patches.Reset()

	ctx := context.Background()
	namespace := &corev1.Namespace{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), namespace).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			return nil
		}).AnyTimes()

	tests := []struct {
		name            string
		obj             interface{}
		nsxSubnet       *model.VpcSubnet
		contextID       string
		labelTags       *map[string]string
		restore         bool
		interfaceIPType v1alpha1.IPAddressType
		expectedPort    *model.VpcSubnetPort
		expectedError   error
	}{
		{
			name:            "build-NSX-port-for-subnetport",
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
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
				Spec: v1alpha1.SubnetPortSpec{
					StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
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
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/subnetport_name"), Tag: common.String("fake_subnetport")},
					{Scope: common.String("nsx-op/subnetport_uid"), Tag: common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc")},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("NONE"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					Id:                common.String("32636365-6333-4239-ad37-3534362d3466"),
					TrafficTag:        common.Int64(0),
				},
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeNone),
			},
			expectedError: nil,
		},
		{
			name:            "build-NSX-port-in-subnet-dhcp-with-binding-in-nsx-mac-pool",
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
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
							IPAddress:  "192.168.1.100",
							MACAddress: "04:50:56:00:fa:00",
						},
					},
					StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
				},
			},
			nsxSubnet: &model.VpcSubnet{
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_DEACTIVATED"),
				},
				AdvancedConfig: &model.SubnetAdvancedConfig{
					StaticIpAllocation: &model.StaticIpAllocation{
						Enabled: common.Bool(false),
					},
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_subnetport"),
				Id:          common.String("fake_subnetport_phoia"),
				Tags: []model.Tag{
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/subnetport_name"), Tag: common.String("fake_subnetport")},
					{Scope: common.String("nsx-op/subnetport_uid"), Tag: common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc")},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("NONE"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					Id:                common.String("32636365-6333-4239-ad37-3534362d3466"),
					TrafficTag:        common.Int64(0),
				},
				AddressBindings: []model.PortAddressBindingEntry{
					{
						IpAddress:  common.String("192.168.1.100"),
						MacAddress: common.String("04:50:56:00:fa:00"),
					},
				},
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeNone),
			},
			expectedError: nil,
		},
		{
			name:            "build-NSX-port-for-restore-subnetport-dhcp",
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
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
				Spec: v1alpha1.SubnetPortSpec{
					StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
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
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/subnetport_name"), Tag: common.String("fake_subnetport")},
					{Scope: common.String("nsx-op/subnetport_uid"), Tag: common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc")},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("NONE"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					TrafficTag:        common.Int64(0),
				},
				AddressBindings: []model.PortAddressBindingEntry{
					{
						IpAddress:  common.String("10.0.0.3"),
						MacAddress: common.String("aa:bb:cc:dd:ee:ff"),
					},
				},
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeNone),
			},
			expectedError: nil,
		},
		{
			name:            "build-NSX-port-for-restore-subnetport-ipam",
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
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
				Spec: v1alpha1.SubnetPortSpec{
					StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
				},
			},
			nsxSubnet: &model.VpcSubnet{
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_DEACTIVATED"),
				},
				AdvancedConfig: &model.SubnetAdvancedConfig{
					StaticIpAllocation: &model.StaticIpAllocation{
						Enabled: common.Bool(true),
					},
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
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/subnetport_name"), Tag: common.String("fake_subnetport")},
					{Scope: common.String("nsx-op/subnetport_uid"), Tag: common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc")},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("BOTH"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					TrafficTag:        common.Int64(0),
				},
				AddressBindings: []model.PortAddressBindingEntry{
					{
						IpAddress:  common.String("10.0.0.3"),
						MacAddress: common.String("aa:bb:cc:dd:ee:ff"),
					},
				},
				// Updated: o.Spec.StaticIPAllocationType is empty string -> converted to "" (or whatever ConvertCRStaticIPAddressTypeToNSX handles for empty strings)
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeIPv4),
			},
			expectedError: nil,
		},
		{
			name:            "build-NSX-port-for-restore-subnetport-ipam-with-mac",
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
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
				Spec: v1alpha1.SubnetPortSpec{
					AddressBindings: []v1alpha1.PortAddressBinding{
						{
							IPAddress:  "10.0.0.3",
							MACAddress: "aa:bb:cc:dd:ee:ff",
						},
					},
					StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
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
					Mode: common.String("DHCP_DEACTIVATED"),
				},
				AdvancedConfig: &model.SubnetAdvancedConfig{
					StaticIpAllocation: &model.StaticIpAllocation{
						Enabled: common.Bool(true),
					},
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
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/subnetport_name"), Tag: common.String("fake_subnetport")},
					{Scope: common.String("nsx-op/subnetport_uid"), Tag: common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc")},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("IP_POOL"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					TrafficTag:        common.Int64(0),
				},
				AddressBindings: []model.PortAddressBindingEntry{
					{
						IpAddress:  common.String("10.0.0.3"),
						MacAddress: common.String("aa:bb:cc:dd:ee:ff"),
					},
				},
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeIPv4),
			},
			expectedError: nil,
		},
		{
			name:            "build-NSX-port-for-subnetport-specified-mac",
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
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
					StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
				},
			},
			nsxSubnet: &model.VpcSubnet{
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_DEACTIVATED"),
				},
				AdvancedConfig: &model.SubnetAdvancedConfig{
					StaticIpAllocation: &model.StaticIpAllocation{
						Enabled: common.Bool(true),
					},
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_subnetport"),
				Id:          common.String("fake_subnetport_phoia"),
				Tags: []model.Tag{
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/subnetport_name"), Tag: common.String("fake_subnetport")},
					{Scope: common.String("nsx-op/subnetport_uid"), Tag: common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc")},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("IP_POOL"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					Id:                common.String("32636365-6333-4239-ad37-3534362d3466"),
					TrafficTag:        common.Int64(0),
				},
				AddressBindings: []model.PortAddressBindingEntry{
					{
						IpAddress:  common.String("10.0.0.1"),
						MacAddress: common.String("aa:bb:cc:dd:ee:ff"),
					},
				},
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeIPv4),
			},
			expectedError: nil,
		},
		{
			name:            "build-NSX-port-for-pod-ipv4",
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
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
				AdvancedConfig: &model.SubnetAdvancedConfig{
					StaticIpAllocation: &model.StaticIpAllocation{
						Enabled: common.Bool(true),
					},
				},
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_DEACTIVATED"),
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
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/pod_name"), Tag: common.String("fake_pod")},
					{Scope: common.String("nsx-op/pod_uid"), Tag: common.String("c5db1800-ce4c-11de-a935-8105ba7ace78")},
					{Scope: common.String("kubernetes.io/metadata.name"), Tag: common.String("fake_ns")},
					{Scope: common.String("vSphereClusterID"), Tag: common.String("domain-c11")},
				},
				Path:       common.String("fake_path/ports/fake_pod_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("BOTH"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					TrafficTag:        common.Int64(0),
					Id:                common.String("63356462-3138-4030-ad63-6534632d3131"),
					AppId:             common.String("c5db1800-ce4c-11de-a935-8105ba7ace78"),
					ContextId:         common.String("fake_context_id"),
				},
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeIPv4),
			},
			expectedError: nil,
		},
		{
			name:            "build-NSX-port-for-pod-ipv6",
			interfaceIPType: v1alpha1.IPAddressTypeIPv6,
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
				AdvancedConfig: &model.SubnetAdvancedConfig{
					StaticIpAllocation: &model.StaticIpAllocation{
						Enabled: common.Bool(true),
					},
				},
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_DEACTIVATED"),
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
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/pod_name"), Tag: common.String("fake_pod")},
					{Scope: common.String("nsx-op/pod_uid"), Tag: common.String("c5db1800-ce4c-11de-a935-8105ba7ace78")},
					{Scope: common.String("kubernetes.io/metadata.name"), Tag: common.String("fake_ns")},
					{Scope: common.String("vSphereClusterID"), Tag: common.String("domain-c11")},
				},
				Path:       common.String("fake_path/ports/fake_pod_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("BOTH"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					TrafficTag:        common.Int64(0),
					Id:                common.String("63356462-3138-4030-ad63-6534632d3131"),
					AppId:             common.String("c5db1800-ce4c-11de-a935-8105ba7ace78"),
					ContextId:         common.String("fake_context_id"),
				},
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeIPv6),
			},
			expectedError: nil,
		},
		{
			name:            "build-NSX-port-for-restore-pod",
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
			obj: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					UID:         "c5db1800-ce4c-11de-a935-8105ba7ace78",
					Name:        "fake_pod",
					Namespace:   "fake_ns",
					Annotations: map[string]string{common.AnnotationPodMAC: "04:50:56:00:fa:00"},
				},
				Status: corev1.PodStatus{
					PodIPs: []corev1.PodIP{
						{IP: "10.0.0.1"},
					},
				},
			},
			nsxSubnet: &model.VpcSubnet{
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_DEACTIVATED"),
				},
				AdvancedConfig: &model.SubnetAdvancedConfig{
					StaticIpAllocation: &model.StaticIpAllocation{
						Enabled: common.Bool(true),
					},
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
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/pod_name"), Tag: common.String("fake_pod")},
					{Scope: common.String("nsx-op/pod_uid"), Tag: common.String("c5db1800-ce4c-11de-a935-8105ba7ace78")},
					{Scope: common.String("kubernetes.io/metadata.name"), Tag: common.String("fake_ns")},
					{Scope: common.String("vSphereClusterID"), Tag: common.String("domain-c11")},
				},
				Path:       common.String("fake_path/ports/fake_pod_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("BOTH"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					TrafficTag:        common.Int64(0),
					AppId:             common.String("c5db1800-ce4c-11de-a935-8105ba7ace78"),
					ContextId:         common.String("fake_context_id"),
				},
				AddressBindings: []model.PortAddressBindingEntry{
					{
						IpAddress:  common.String("10.0.0.1"),
						MacAddress: common.String("04:50:56:00:fa:00"),
					},
				},
				// Updated: Uses ConvertCRIPAddressTypeToNSX(interfaceIPType)
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeIPv4),
			},
			expectedError: nil,
			restore:       true,
		},
		{
			name:            "build-NSX-port-for-subnetport-no-ip",
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
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
				Spec: v1alpha1.SubnetPortSpec{
					StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
				},
			},
			nsxSubnet: &model.VpcSubnet{
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_DEACTIVATED"),
				},
				AdvancedConfig: &model.SubnetAdvancedConfig{
					StaticIpAllocation: &model.StaticIpAllocation{
						Enabled: common.Bool(false),
					},
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_subnetport"),
				Id:          common.String("fake_subnetport_phoia"),
				Tags: []model.Tag{
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/subnetport_name"), Tag: common.String("fake_subnetport")},
					{Scope: common.String("nsx-op/subnetport_uid"), Tag: common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc")},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("NONE"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					Id:                common.String("32636365-6333-4239-ad37-3534362d3466"),
					TrafficTag:        common.Int64(0),
				},
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeNone),
			},
			expectedError: nil,
		},
		{
			name:            "build-NSX-port-for-subnetport-no-ip-2",
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
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
				Spec: v1alpha1.SubnetPortSpec{
					StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
				},
			},
			nsxSubnet: &model.VpcSubnet{
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_DEACTIVATED"),
				},
				AdvancedConfig: &model.SubnetAdvancedConfig{
					ConnectivityState: common.String("CONNECTED"),
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_subnetport"),
				Id:          common.String("fake_subnetport_phoia"),
				Tags: []model.Tag{
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/subnetport_name"), Tag: common.String("fake_subnetport")},
					{Scope: common.String("nsx-op/subnetport_uid"), Tag: common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc")},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("NONE"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					Id:                common.String("32636365-6333-4239-ad37-3534362d3466"),
					TrafficTag:        common.Int64(0),
				},
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeNone),
			},
			expectedError: nil,
		},
		{
			name:            "build-NSX-port-in-subnet-no-ip-but-binding-in-nsx-mac-pool",
			interfaceIPType: v1alpha1.IPAddressTypeIPv4,
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
							IPAddress:  "192.168.1.100",
							MACAddress: "04:50:56:00:fa:00",
						},
					},
					StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
				},
			},
			nsxSubnet: &model.VpcSubnet{
				SubnetDhcpConfig: &model.SubnetDhcpConfig{
					Mode: common.String("DHCP_DEACTIVATED"),
				},
				AdvancedConfig: &model.SubnetAdvancedConfig{
					StaticIpAllocation: &model.StaticIpAllocation{
						Enabled: common.Bool(false),
					},
				},
				Path: common.String("fake_path"),
			},
			contextID: "fake_context_id",
			labelTags: nil,
			expectedPort: &model.VpcSubnetPort{
				DisplayName: common.String("fake_subnetport"),
				Id:          common.String("fake_subnetport_phoia"),
				Tags: []model.Tag{
					{Scope: common.String("nsx-op/cluster"), Tag: common.String("fake_cluster")},
					{Scope: common.String("nsx-op/version"), Tag: common.String("1.0.0")},
					{Scope: common.String("nsx-op/namespace"), Tag: common.String("fake_ns")},
					{Scope: common.String("nsx-op/subnetport_name"), Tag: common.String("fake_subnetport")},
					{Scope: common.String("nsx-op/subnetport_uid"), Tag: common.String("2ccec3b9-7546-4fd2-812a-1e3a4afd7acc")},
				},
				Path:       common.String("fake_path/ports/fake_subnetport_phoia"),
				ParentPath: common.String("fake_path"),
				Attachment: &model.PortAttachment{
					AllocateAddresses: common.String("NONE"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					Id:                common.String("32636365-6333-4239-ad37-3534362d3466"),
					TrafficTag:        common.Int64(0),
				},
				AddressBindings: []model.PortAddressBindingEntry{
					{
						IpAddress:  common.String("192.168.1.100"),
						MacAddress: common.String("04:50:56:00:fa:00"),
					},
				},
				StaticIpAllocationType: common.String(controllercommon.NSXIPAddressTypeNone),
			},
			expectedError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Passed tt.interfaceIPType explicitly into the updated method signature
			observedPort, err := service.buildSubnetPort(tt.obj, tt.nsxSubnet, tt.contextID, tt.labelTags, false, tt.restore, tt.interfaceIPType)
			if tt.expectedError != nil {
				assert.Equal(t, tt.expectedError, err)
			} else {
				assert.Nil(t, err)
				if tt.restore && observedPort != nil && observedPort.Attachment != nil {
					tt.expectedPort.Attachment.Id = observedPort.Attachment.Id
				}
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
		{
			name: "non-restore-with-restored-ipaddressallocation",
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
							Status: v1alpha1.AddressBindingStatus{
								IPAddress: "1.2.3.4",
							},
						}
					},
				)
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

func TestGetStatefulSetInfo(t *testing.T) {
	tests := []struct {
		name         string
		obj          interface{}
		expectedName string
		expectedUID  string
	}{
		{
			name: "StatefulSet pod with controller reference",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "StatefulSet",
							Name:       "web",
							UID:        "sts-uid-123",
							Controller: ptr.To(true),
						},
					},
				},
			},
			expectedName: "web",
			expectedUID:  "sts-uid-123",
		},
		{
			name: "StatefulSet owner but not controller (should ignore)",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "StatefulSet",
							Name:       "web",
							UID:        "sts-uid-123",
							Controller: ptr.To(false),
						},
					},
				},
			},
			expectedName: "",
			expectedUID:  "",
		},
		{
			name: "Deployment pod with owner reference",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "ReplicaSet",
							Name:       "nginx-7d4c7b8b5",
						},
					},
				},
			},
			expectedName: "",
			expectedUID:  "",
		},
		{
			name: "Standalone pod without owner",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{},
				},
			},
			expectedName: "",
			expectedUID:  "",
		},
		{
			name:         "SubnetPort CR (not a pod)",
			obj:          &v1alpha1.SubnetPort{},
			expectedName: "",
			expectedUID:  "",
		},
		{
			name:         "Nil object",
			obj:          nil,
			expectedName: "",
			expectedUID:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stsName, stsUID := getStatefulSetInfo(tt.obj)
			assert.Equal(t, tt.expectedName, stsName)
			assert.Equal(t, tt.expectedUID, stsUID)
		})
	}
}

func TestBuildSubnetPortIdAndName_existingPortByUID(t *testing.T) {
	nsxClient := &nsx.Client{}
	store := &SubnetPortStore{}
	service := &SubnetPortService{
		Service: common.Service{
			NSXClient: nsxClient,
		},
		SubnetPortStore: store,
	}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(store), "GetVpcSubnetPortByUID",
		func(s *SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			portID := "existing-port-id"
			portName := "existing-port-name"
			return &model.VpcSubnetPort{
				Id:          &portID,
				DisplayName: &portName,
			}, nil
		})
	patches.ApplyMethod(reflect.TypeOf(store), "GetByKey",
		func(s *SubnetPortStore, key string) *model.VpcSubnetPort {
			return nil
		})
	defer patches.Reset()
	patchesStsFeat := gomonkey.ApplyFunc(nsx.StatefulSetPodSubnetPortFeatureEnabled,
		func(_ *nsx.Client, _ *config.NSXOperatorConfig) bool {
			return false
		})
	defer patchesStsFeat.Reset()

	objMeta := &metav1.ObjectMeta{Name: "test-pod", UID: "pod-uid-123"}
	id, name := service.BuildSubnetPortIdAndName(objMeta, types.UID("ns-uid-456"), "")
	assert.Equal(t, "existing-port-id", id)
	assert.Equal(t, "existing-port-name", name)
}

func TestBuildSubnetPortIdAndName_reuseSTSPortByUIDAndPodName(t *testing.T) {
	nsxClient := &nsx.Client{}
	store := &SubnetPortStore{}
	service := &SubnetPortService{
		Service: common.Service{
			NSXClient: nsxClient,
		},
		SubnetPortStore: store,
	}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(store), "GetVpcSubnetPortByUID",
		func(s *SubnetPortStore, uid types.UID) (*model.VpcSubnetPort, error) {
			return nil, nil
		})
	defer patches.Reset()
	patches.ApplyMethod(reflect.TypeOf(store), "GetByIndex",
		func(s *SubnetPortStore, indexKey string, indexValue string) []*model.VpcSubnetPort {
			if indexKey == common.TagScopeStatefulSetUID && indexValue == "sts-uid-123" {
				podNameScope := "nsx-op/pod_name"
				stsPortID := "sts-port-id"
				stsPortName := "test-pod"
				return []*model.VpcSubnetPort{
					{
						Id:          &stsPortID,
						DisplayName: &stsPortName,
						Tags: []model.Tag{
							{Scope: &podNameScope, Tag: common.String("test-pod")},
						},
					},
				}
			}
			return []*model.VpcSubnetPort{}
		})
	patches.ApplyMethod(reflect.TypeOf(store), "GetByKey",
		func(s *SubnetPortStore, key string) *model.VpcSubnetPort {
			return nil
		})
	patchesStsFeat := gomonkey.ApplyFunc(nsx.StatefulSetPodSubnetPortFeatureEnabled,
		func(_ *nsx.Client, _ *config.NSXOperatorConfig) bool {
			return true
		})
	defer patchesStsFeat.Reset()

	objMeta := &metav1.ObjectMeta{Name: "test-pod", UID: "pod-uid-123"}
	id, name := service.BuildSubnetPortIdAndName(objMeta, types.UID("ns-uid-456"), "sts-uid-123")
	assert.Equal(t, "sts-port-id", id)
	assert.Equal(t, "test-pod", name)
}

// TestBuildSubnetPortMACPool verifies that the MAC_POOL migration guard works correctly:
// - Normal reconcile (non-restore): existing status MAC must NOT trigger NONE→MAC_POOL migration
// - Restore mode: existing status MAC IS included so NSX re-uses the same MAC
func TestBuildSubnetPortMACPool(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	nsxClient := &nsx.Client{}
	tr := true
	service := &SubnetPortService{
		Service: common.Service{
			Client:    k8sClient,
			NSXClient: nsxClient,
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
					VpcWcpEnhance:    &tr,
				},
				CoeConfig: &config.CoeConfig{
					Cluster: "fake_cluster",
				},
			},
		},
		SubnetPortStore: setupStore(),
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion",
		func(_ *nsx.Client, _ int) bool {
			return true
		})
	defer patches.Reset()

	ctx := context.Background()
	namespace := &corev1.Namespace{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), namespace).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			return nil
		}).AnyTimes()

	dhcpSubnet := &model.VpcSubnet{
		SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: common.String("DHCP_SERVER")},
		Path:             common.String("fake_path"),
	}
	spWithStatusMAC := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "2ccec3b9-7546-4fd2-812a-1e3a4afd7acc",
			Name:      "fake_subnetport",
			Namespace: "fake_ns",
		},
		Spec: v1alpha1.SubnetPortSpec{StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone},
		Status: v1alpha1.SubnetPortStatus{
			NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
				MACAddress: "aa:bb:cc:dd:ee:ff",
			},
		},
	}

	// Non-restore: existing MAC in status must NOT cause MAC_POOL migration.
	port, err := service.buildSubnetPort(spWithStatusMAC, dhcpSubnet, "ctx", nil, false, false, v1alpha1.IPAddressTypeIPv4)
	assert.Nil(t, err)
	assert.Equal(t, "NONE", *port.Attachment.AllocateAddresses)
	assert.Empty(t, port.AddressBindings)

	// Restore mode: existing MAC in status IS preserved via MAC_POOL so NSX reuses it.
	port, err = service.buildSubnetPort(spWithStatusMAC, dhcpSubnet, "ctx", nil, false, true, v1alpha1.IPAddressTypeIPv4)
	assert.Nil(t, err)
	assert.Equal(t, "MAC_POOL", *port.Attachment.AllocateAddresses)
	if assert.Len(t, port.AddressBindings, 1) {
		assert.Equal(t, "aa:bb:cc:dd:ee:ff", *port.AddressBindings[0].MacAddress)
	}
}

// TestBuildSubnetPortIPOnlyAllocateAddresses verifies allocateAddresses when the
// user specifies an IP only (no MAC) on NSX 9.2+:
//   - staticIPAllocationType set (IP falls in the subnet's static IP pool) -> BOTH
//   - staticIPAllocationType None (IP outside any static pool)             -> MAC_POOL
func TestBuildSubnetPortIPOnlyAllocateAddresses(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	nsxClient := &nsx.Client{}
	tr := true
	service := &SubnetPortService{
		Service: common.Service{
			Client:    k8sClient,
			NSXClient: nsxClient,
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
					VpcWcpEnhance:    &tr,
				},
				CoeConfig: &config.CoeConfig{Cluster: "fake_cluster"},
			},
		},
		SubnetPortStore: setupStore(),
	}

	// NSX 9.2+ so MAC_POOL is available.
	patches := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion",
		func(_ *nsx.Client, _ int) bool { return true })
	defer patches.Reset()

	ctx := context.Background()
	namespace := &corev1.Namespace{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), namespace).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			return nil
		}).AnyTimes()

	staticEnabled := &tr
	staticSubnet := &model.VpcSubnet{
		AdvancedConfig: &model.SubnetAdvancedConfig{
			StaticIpAllocation: &model.StaticIpAllocation{Enabled: staticEnabled},
		},
		Path: common.String("fake_path"),
	}

	newSP := func(name string, allocType v1alpha1.StaticIPAllocationType) *v1alpha1.SubnetPort {
		return &v1alpha1.SubnetPort{
			ObjectMeta: metav1.ObjectMeta{
				UID:       types.UID("2ccec3b9-7546-4fd2-812a-1e3a4afd7ac" + name),
				Name:      "fake_subnetport_" + name,
				Namespace: "fake_ns",
			},
			Spec: v1alpha1.SubnetPortSpec{
				StaticIPAllocationType: allocType,
				AddressBindings: []v1alpha1.PortAddressBinding{
					{IPAddress: "10.0.0.5"},
				},
			},
		}
	}

	// IP falls within the static IP pool -> BOTH.
	spInPool := newSP("a", v1alpha1.StaticIPAllocationTypeIPv4)
	port, err := service.buildSubnetPort(spInPool, staticSubnet, "ctx", nil, false, false, v1alpha1.IPAddressTypeIPv4)
	assert.Nil(t, err)
	assert.Equal(t, "BOTH", *port.Attachment.AllocateAddresses)
	if assert.Len(t, port.AddressBindings, 1) {
		assert.Equal(t, "10.0.0.5", *port.AddressBindings[0].IpAddress)
	}

	// IP is not in any static IP pool (staticIPAllocationType None) -> MAC_POOL.
	spOutOfPool := newSP("b", v1alpha1.StaticIPAllocationTypeNone)
	port, err = service.buildSubnetPort(spOutOfPool, staticSubnet, "ctx", nil, false, false, v1alpha1.IPAddressTypeIPv4)
	assert.Nil(t, err)
	assert.Equal(t, "MAC_POOL", *port.Attachment.AllocateAddresses)
	if assert.Len(t, port.AddressBindings, 1) {
		assert.Equal(t, "10.0.0.5", *port.AddressBindings[0].IpAddress)
	}
}

// TestBuildSubnetPortMACVersionGating verifies that on NSX < 9.2 (where the
// staticIPAllocationType matrix is unavailable) a MAC-only binding is only sent
// when static IP allocation is enabled or an explicit IP is provided, while on
// NSX 9.2+ the MAC is always bound.
func TestBuildSubnetPortMACVersionGating(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	nsxClient := &nsx.Client{}
	service := &SubnetPortService{
		Service: common.Service{
			Client:    k8sClient,
			NSXClient: nsxClient,
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{EnforcementPoint: "vmc-enforcementpoint"},
				CoeConfig: &config.CoeConfig{Cluster: "fake_cluster"},
			},
		},
		SubnetPortStore: setupStore(),
	}

	ctx := context.Background()
	namespace := &corev1.Namespace{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), namespace).Return(nil).Do(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
			return nil
		}).AnyTimes()

	// DHCP subnet => static IP allocation disabled.
	dhcpSubnet := &model.VpcSubnet{
		SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: common.String("DHCP_SERVER")},
		Path:             common.String("fake_path"),
	}
	spMACOnly := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "2ccec3b9-7546-4fd2-812a-1e3a4afd7acc",
			Name:      "fake_subnetport",
			Namespace: "fake_ns",
		},
		Spec: v1alpha1.SubnetPortSpec{
			StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			AddressBindings: []v1alpha1.PortAddressBinding{
				{MACAddress: "aa:bb:cc:dd:ee:ff"},
			},
		},
	}

	// NSX < 9.2: MAC-only binding on a static-disabled subnet is dropped (legacy behavior).
	patchesOld := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion",
		func(_ *nsx.Client, _ int) bool { return false })
	port, err := service.buildSubnetPort(spMACOnly, dhcpSubnet, "ctx", nil, false, false, v1alpha1.IPAddressTypeIPv4)
	assert.Nil(t, err)
	assert.Empty(t, port.AddressBindings)
	patchesOld.Reset()

	// NSX 9.2+: MAC is always bound.
	patchesNew := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion",
		func(_ *nsx.Client, _ int) bool { return true })
	defer patchesNew.Reset()
	port, err = service.buildSubnetPort(spMACOnly, dhcpSubnet, "ctx", nil, false, false, v1alpha1.IPAddressTypeIPv4)
	assert.Nil(t, err)
	if assert.Len(t, port.AddressBindings, 1) {
		assert.Equal(t, "aa:bb:cc:dd:ee:ff", *port.AddressBindings[0].MacAddress)
	}
}
