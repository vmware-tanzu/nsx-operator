package subnetport

import (
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestDHCPDefaultPodPayloadAndRetry(t *testing.T) {
	for _, ipv6Supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "pre-9.2", true: "9.2"}[ipv6Supported], func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, v1.AddToScheme(scheme))
			namespace := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", UID: "namespace-uid"}}
			pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "ns", UID: "pod-uid"}}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespace, pod).Build()
			ports := &fakePortClient{}
			realization := &fakeRealizedEntitiesClient{}
			nsxClient := &nsx.Client{PortClient: ports, PortStateClient: &fakePortStateClient{}, RealizedEntitiesClient: realization}
			svc := &SubnetPortService{Service: common.Service{Client: c, NSXClient: nsxClient, NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster"}}}, SubnetPortStore: setupStore()}
			patches := gomonkey.ApplyFunc((*nsx.Client).NSXCheckVersion, func(*nsx.Client, int) bool { return ipv6Supported })
			defer patches.Reset()
			subnet := &model.VpcSubnet{
				Id: ptr.To("vlan"), Path: ptr.To("/orgs/default/projects/default/vpcs/vpc/subnets/vlan"),
				AccessMode:       ptr.To(model.VpcSubnet_ACCESS_MODE_PUBLIC),
				VlanConnection:   ptr.To("/infra/distributed-vlan-connections/vlan"),
				AdvancedConfig:   &model.SubnetAdvancedConfig{StaticIpAllocation: &model.StaticIpAllocation{Enabled: ptr.To(false)}},
				SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: ptr.To(model.SubnetDhcpConfig_MODE_DEACTIVATED)},
			}
			sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: "ns", UID: "12345678-1234-1234-1234-123456789abc",
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "pod", UID: pod.UID}}},
				Spec: v1alpha1.SubnetPortSpec{InterfaceIPType: v1alpha1.IPAddressTypeIPv4, StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
					AddressBindings: []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10", MACAddress: "02:00:00:00:00:10"}}}}
			// No pool clients are installed: external DHCP must never query NSX IPAM.
			ok, err := svc.AllocatePortFromSubnet(subnet, true, sp.Spec.InterfaceIPType, sp.Spec.StaticIPAllocationType, sp.Spec.AddressBindings)
			require.NoError(t, err)
			require.True(t, ok)
			svc.ReleasePortInSubnet(*subnet.Path, sp.Spec.InterfaceIPType, sp.Spec.StaticIPAllocationType, sp.Spec.AddressBindings)
			realization.listFn = func(string, *string) (model.GenericPolicyRealizedResourceListResult, error) {
				return model.GenericPolicyRealizedResourceListResult{}, nsxutil.NewRealizeStateError("target realization failed", 500000)
			}
			state, err := svc.CreateOrUpdateSubnetPort(sp, subnet, "host", nil, false, false, sp.Spec.InterfaceIPType)
			require.Error(t, err)
			require.True(t, nsxutil.IsRealizeStateError(err))
			require.Nil(t, state)
			first := *ports.patchedPort
			require.Equal(t, "NONE", *first.Attachment.AllocateAddresses)
			require.Equal(t, "192.0.2.10", *first.AddressBindings[0].IpAddress)
			require.Equal(t, "02:00:00:00:00:10", *first.AddressBindings[0].MacAddress)
			realization.listFn = nil
			for i := 0; i < 2; i++ {
				_, err = svc.CreateOrUpdateSubnetPort(sp, subnet, "host", nil, false, false, sp.Spec.InterfaceIPType)
				require.NoError(t, err)
				actual, err := svc.SubnetPortStore.GetVpcSubnetPortByUID(sp.UID)
				require.NoError(t, err)
				require.Equal(t, first.Id, actual.Id)
				require.Equal(t, first.Attachment, actual.Attachment)
				require.Equal(t, first.AddressBindings, actual.AddressBindings)
			}
			sp.Status = v1alpha1.SubnetPortStatus{
				Attachment:             v1alpha1.PortAttachment{ID: "old-attachment"},
				Conditions:             []v1alpha1.Condition{{Type: v1alpha1.Ready, Reason: "SubnetPortReady", Status: v1.ConditionTrue}},
				NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{{IPAddress: "192.0.2.10", Gateway: "192.0.2.1"}}},
			}
			state, err = svc.CreateOrUpdateSubnetPort(sp, subnet, "host", nil, false, false, sp.Spec.InterfaceIPType)
			require.NoError(t, err)
			require.NotNil(t, state, "a Ready condition for another attachment cannot bypass realization")
		})
	}
}
