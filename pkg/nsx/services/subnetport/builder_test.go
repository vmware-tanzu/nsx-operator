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
			return false
		})
	defer patches.Reset()

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
			// DHCP/DHCPRelay - Y; StaticIPAllocation Enabled - N;
			// AddressBinding exists - N
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
					AllocateAddresses: common.String("NONE"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					Id:                common.String("32636365-6333-4239-ad37-3534362d3466"),
					TrafficTag:        common.Int64(0),
				},
			},
			expectedError: nil,
		},
		{
			// DHCP/DHCPRelay - Y; StaticIPAllocation Enabled - N;
			// AddressBinding exists - Y;
			name: "build-NSX-port-in-subnet-dhcp-with-binding-in-nsx-mac-pool",
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
			},
			expectedError: nil,
		},
		{
			// DHCP/DHCPRelay - Y; StaticIPAllocation Enabled - N;
			// AddressBinding exists - Y (restore);
			name: "build-NSX-port-for-restore-subnetport-dhcp",
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
			},
			expectedError: nil,
		},
		{
			// DHCP/DHCPRelay - N; StaticIPAllocation Enabled - Y;
			// AddressBinding exists - N (restore);
			name: "build-NSX-port-for-restore-subnetport-ipam",
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
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
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
			// DHCP/DHCPRelay - N; StaticIPAllocation Enabled - Y;
			// AddressBinding exists - Y (restore);
			name: "build-NSX-port-for-restore-subnetport-ipam-with-mac",
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
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
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
			// DHCP/DHCPRelay - N; StaticIPAllocation Enabled - Y;
			// AddressBinding exists - Y
			name: "build-NSX-port-for-subnetport-specified-mac",
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
			},
			expectedError: nil,
		},
		{
			// DHCP/DHCPRelay - N; StaticIPAllocation Enabled - Y;
			// AddressBinding exists - N (pod)
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
					AllocateAddresses: common.String("BOTH"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					TrafficTag:        common.Int64(0),
					Id:                common.String("63356462-3138-4030-ad63-6534632d3131"),
					AppId:             common.String("c5db1800-ce4c-11de-a935-8105ba7ace78"),
					ContextId:         common.String("fake_context_id"),
				},
			},
			expectedError: nil,
		},
		{
			// DHCP/DHCPRelay - N; StaticIPAllocation Enabled - Y;
			// AddressBinding exists - N (restore pod)
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
					Annotations: map[string]string{common.AnnotationPodMAC: "04:50:56:00:fa:00"},
				},
				Status: corev1.PodStatus{
					PodIP: "10.0.0.1",
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
			},
			expectedError: nil,
			restore:       true,
		},
		{
			// DHCP/DHCPRelay - N; StaticIPAllocation Enabled - N;
			// AddressBinding exists - N
			name: "build-NSX-port-for-subnetport-no-ip",
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
					AllocateAddresses: common.String("NONE"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					Id:                common.String("32636365-6333-4239-ad37-3534362d3466"),
					TrafficTag:        common.Int64(0),
				},
			},
			expectedError: nil,
		},
		{
			// DHCP/DHCPRelay - N; StaticIPAllocation Enabled - N;
			// AddressBinding exists - N
			name: "build-NSX-port-for-subnetport-no-ip-2",
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
					AllocateAddresses: common.String("NONE"),
					Type_:             common.String(model.PortAttachment_TYPE_INDEPENDENT),
					Id:                common.String("32636365-6333-4239-ad37-3534362d3466"),
					TrafficTag:        common.Int64(0),
				},
			},
			expectedError: nil,
		},
		{
			// DHCP/DHCPRelay - N; StaticIPAllocation Enabled - N;
			// AddressBinding exists - Y;
			name: "build-NSX-port-in-subnet-no-ip-but-binding-in-nsx-mac-pool",
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
			},
			expectedError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedPort, err := service.buildSubnetPort(tt.obj, tt.nsxSubnet, tt.contextID, tt.labelTags, false, tt.restore)
			if tt.expectedError != nil {
				assert.Equal(t, tt.expectedError, err)
			} else {
				assert.Nil(t, err)
				// Ignore attachment id for restore mode as it is random
				if tt.restore {
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
