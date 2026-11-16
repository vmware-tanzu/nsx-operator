package subnetport

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/mock"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func externalDHCPSubnet() *model.VpcSubnet {
	return &model.VpcSubnet{
		Id: ptr.To("vlan"), Path: ptr.To("/orgs/default/projects/default/vpcs/vpc/subnets/vlan"),
		RealizationId: ptr.To("logical-switch"), AccessMode: ptr.To(model.VpcSubnet_ACCESS_MODE_PUBLIC),
		VlanConnection:   ptr.To("/infra/distributed-vlan-connections/vlan"),
		AdvancedConfig:   &model.SubnetAdvancedConfig{StaticIpAllocation: &model.StaticIpAllocation{Enabled: ptr.To(false)}},
		SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: ptr.To(model.SubnetDhcpConfig_MODE_DEACTIVATED)},
	}
}

func defaultPodPort() *v1alpha1.SubnetPort {
	return &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: "ns", UID: "12345678-1234-1234-1234-123456789abc",
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "pod", UID: "pod-uid"}}},
		Spec: v1alpha1.SubnetPortSpec{SubnetSet: "default-pod", InterfaceIPType: v1alpha1.IPAddressTypeIPv4,
			StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
			AddressBindings:        []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10", MACAddress: "02:00:00:00:00:10"}}},
	}
}

type dhcpPodSubnetService struct {
	mock.MockSubnetServiceProvider
	subnet     *model.VpcSubnet
	gatewayErr error
}

func (s *dhcpPodSubnetService) GetSubnetByPath(string, bool) (*model.VpcSubnet, error) {
	return s.subnet, nil
}

func (s *dhcpPodSubnetService) GetAllGatewayPrefixesOfSubnet(*model.VpcSubnet) ([]servicecommon.GatewayPrefixInfo, error) {
	return nil, s.gatewayErr
}

func TestExternalDHCPDefaultPodNetworkScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*v1alpha1.SubnetPort, *model.VpcSubnet)
		want   bool
	}{
		{"external DHCP", func(_ *v1alpha1.SubnetPort, _ *model.VpcSubnet) {}, true},
		{"direct default Subnet", func(s *v1alpha1.SubnetPort, _ *model.VpcSubnet) { s.Spec.SubnetSet = ""; s.Spec.Subnet = "vlan" }, true},
		{"VM", func(s *v1alpha1.SubnetPort, _ *model.VpcSubnet) { s.OwnerReferences = nil }, false},
		{"secondary network", func(s *v1alpha1.SubnetPort, _ *model.VpcSubnet) { s.Spec.SubnetSet = "secondary" }, false},
		{"unrelated Subnet", func(s *v1alpha1.SubnetPort, _ *model.VpcSubnet) { s.Spec.SubnetSet = ""; s.Spec.Subnet = "other" }, false},
		{"overlay", func(_ *v1alpha1.SubnetPort, s *model.VpcSubnet) { s.VlanConnection = nil }, false},
		{"omitted advanced config", func(_ *v1alpha1.SubnetPort, s *model.VpcSubnet) { s.AdvancedConfig = nil }, false},
		{"omitted enabled", func(_ *v1alpha1.SubnetPort, s *model.VpcSubnet) { s.AdvancedConfig.StaticIpAllocation.Enabled = nil }, false},
		{"explicit true", func(_ *v1alpha1.SubnetPort, s *model.VpcSubnet) {
			s.AdvancedConfig.StaticIpAllocation.Enabled = ptr.To(true)
		}, false},
		{"static port", func(s *v1alpha1.SubnetPort, _ *model.VpcSubnet) {
			s.Spec.StaticIPAllocationType = v1alpha1.StaticIPAllocationTypeIPv4
		}, false},
		{"IPv6 only", func(s *v1alpha1.SubnetPort, _ *model.VpcSubnet) { s.Spec.InterfaceIPType = v1alpha1.IPAddressTypeIPv6 }, false},
		{"NSX DHCP", func(_ *v1alpha1.SubnetPort, s *model.VpcSubnet) {
			s.SubnetDhcpConfig.Mode = ptr.To(model.SubnetDhcpConfig_MODE_SERVER)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			ss := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "default-pod", Namespace: "ns", Labels: map[string]string{servicecommon.LabelDefaultNetwork: servicecommon.DefaultPodNetwork}}, Spec: v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"vlan"}}}
			secondary := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "secondary", Namespace: "ns"}}
			r := &SubnetPortReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(ss, secondary).Build()}
			sp, subnet := defaultPodPort(), externalDHCPSubnet()
			tc.change(sp, subnet)
			got, err := r.isExternalDHCPDefaultPodNetwork(sp, subnet)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// Exercise real CR spec/status persistence around the NSX service boundary.
// Neither desired bindings nor a merely-created port are sufficient for Ready.
func TestDHCPDefaultPodReconcileReadinessAndRetry(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	sp, subnet := defaultPodPort(), externalDHCPSubnet()
	sp.Spec.StaticIPAllocationType = ""
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "ns", UID: "pod-uid"}}
	ss := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "default-pod", Namespace: "ns", Labels: map[string]string{servicecommon.LabelDefaultNetwork: servicecommon.DefaultPodNetwork}}, Spec: v1alpha1.SubnetSetSpec{IPAddressType: v1alpha1.IPAddressTypeIPv4}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(sp).WithObjects(sp, pod, ss).
		WithIndex(&v1alpha1.Subnet{}, util.SubnetAssociatedResource, func(client.Object) []string { return nil }).Build()
	cfg := &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}, K8sConfig: &config.K8sConfig{}}
	svc := &subnetport.SubnetPortService{Service: servicecommon.Service{Client: c, NSXClient: &nsx.Client{}, NSXConfig: cfg}, SubnetPortStore: &subnetport.SubnetPortStore{}}
	subnets, vpcs, allocations := &dhcpPodSubnetService{subnet: subnet}, &mock.MockVPCServiceProvider{}, &mock.MockIPAddressAllocationProvider{}
	vpcs.On("IsRADeactivatedByVPCPath", *subnet.Path).Return(true, nil)
	r := &SubnetPortReconciler{Client: c, APIReader: c, Scheme: scheme, SubnetPortService: svc, SubnetService: subnets, VPCService: vpcs, IpAddressAllocationService: allocations}
	r.StatusUpdater = common.NewStatusUpdater(c, cfg, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	patches := gomonkey.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID, func(*subnetport.SubnetPortStore, types.UID) (*model.VpcSubnetPort, error) { return nil, nil })
	defer patches.Reset()
	patches.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort, func(*SubnetPortReconciler, context.Context, *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
		return true, false, *subnet.Path, nil, nil, v1alpha1.IPAddressTypeIPv4, "", nil
	})
	state := &model.SegmentPortState{}
	var realizationErr error = nsxutil.NewRealizeStateError("target realization failed", 500000)
	patches.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSubnetPort, func(_ *subnetport.SubnetPortService, obj interface{}, _ *model.VpcSubnet, _ string, _ *map[string]string, vm, restore bool, _ v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
		actual := obj.(*v1alpha1.SubnetPort)
		require.False(t, vm)
		require.False(t, restore)
		require.Equal(t, v1alpha1.StaticIPAllocationTypeNone, actual.Spec.StaticIPAllocationType)
		require.Equal(t, sp.Spec.AddressBindings, actual.Spec.AddressBindings)
		return state, realizationErr
	})
	read := func() *v1alpha1.SubnetPort {
		actual := &v1alpha1.SubnetPort{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(sp), actual))
		return actual
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(sp)}
	res, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, common.ResultRequeueAfter60sec, res)
	require.Empty(t, read().Status.Attachment.ID)
	require.Equal(t, v1.ConditionFalse, read().Status.Conditions[0].Status)
	require.Contains(t, read().Status.Conditions[0].Message, "target realization failed")

	realizationErr = nil
	for _, attachment := range []*model.SegmentPortAttachmentState{nil, {}, {Id: ptr.To("")}} {
		state.Attachment = attachment
		_, err = r.Reconcile(ctx, req)
		require.ErrorContains(t, err, "realized attachment is missing")
		require.Empty(t, read().Status.Attachment.ID)
		require.Equal(t, v1.ConditionFalse, read().Status.Conditions[0].Status)
	}

	state.Attachment = &model.SegmentPortAttachmentState{Id: ptr.To("ready-attachment")}
	state.RealizedBindings = []model.AddressBindingEntry{{Binding: &model.PacketAddressClassifier{IpAddress: ptr.To("192.0.2.10"), MacAddress: ptr.To("02:00:00:00:00:10")}}}
	subnets.gatewayErr = errors.New("gateway lookup failed")
	_, err = r.Reconcile(ctx, req)
	require.ErrorContains(t, err, "gateway lookup failed")
	require.Empty(t, read().Status.Attachment.ID)
	require.Equal(t, v1.ConditionFalse, read().Status.Conditions[0].Status)

	// An L2-only VLAN has no NSX gateway; guest DHCP supplies it.
	subnets.gatewayErr = nil
	for i := 0; i < 2; i++ {
		_, err = r.Reconcile(ctx, req)
		require.NoError(t, err)
		actual := read()
		require.Equal(t, v1.ConditionTrue, actual.Status.Conditions[0].Status)
		require.Equal(t, "ready-attachment", actual.Status.Attachment.ID)
		require.Equal(t, "logical-switch", actual.Status.NetworkInterfaceConfig.LogicalSwitchUUID)
		require.False(t, actual.Status.NetworkInterfaceConfig.DHCPDeactivatedOnSubnet)
		require.True(t, actual.Status.NetworkInterfaceConfig.DHCPv6DeactivatedOnSubnet)
		require.Equal(t, "192.0.2.10", actual.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress)
		require.Equal(t, sp.Spec.AddressBindings, actual.Spec.AddressBindings)
	}
	// A cached ready port still needs its DHCP configuration refreshed when
	// default-network membership changes, even if its NSX payload is unchanged.
	state = nil
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.False(t, read().Status.NetworkInterfaceConfig.DHCPDeactivatedOnSubnet)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(ss), ss))
	ss.Labels = nil
	require.NoError(t, c.Update(ctx, ss))
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.True(t, read().Status.NetworkInterfaceConfig.DHCPDeactivatedOnSubnet)

	// A failed lookup must remain visible, rather than silently disabling DHCP.
	require.NoError(t, c.Delete(ctx, ss))
	_, err = r.Reconcile(ctx, req)
	require.Error(t, err)
	require.Equal(t, v1.ConditionFalse, read().Status.Conditions[0].Status)
}
