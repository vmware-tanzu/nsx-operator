package subnetport

import (
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type commitThenTimeoutClient struct {
	fakePortClient
	committed []model.VpcSubnetPort
}

func (c *commitThenTimeoutClient) Patch(_, _, _, _, _ string, p model.VpcSubnetPort) error {
	for i := range c.committed {
		if *c.committed[i].Id == *p.Id {
			c.committed[i] = p
			return nil
		}
	}
	c.committed = append(c.committed, p)
	return fmt.Errorf("request timed out after backend commit")
}

func (c *commitThenTimeoutClient) List(_, _, _, _ string, _ *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcSubnetPortListResult, error) {
	return model.VpcSubnetPortListResult{Results: c.committed}, nil
}

func (c *commitThenTimeoutClient) Get(_, _, _, _, id string) (model.VpcSubnetPort, error) {
	for _, port := range c.committed {
		if *port.Id == id {
			return port, nil
		}
	}
	return model.VpcSubnetPort{}, fmt.Errorf("port %s not found", id)
}

func TestSharedSubnetRetryAfterCommittedTimeout(t *testing.T) {
	s := createSubnetPortService(t)
	s.Client = fake.NewClientBuilder().WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "review", UID: "namespace-id"}}).Build()
	backend := &commitThenTimeoutClient{}
	s.NSXClient.PortClient = backend
	s.NSXClient.IPPoolClient = &mockFuncIPPoolClient{getFunc: func(_, _, _, _, _ string) (model.IpAddressPool, error) {
		return model.IpAddressPool{PoolUsage: &model.PolicyPoolUsage{TotalIps: common.Int64(1), RequestedIpAllocations: common.Int64(int64(len(backend.committed)))}}, nil
	}}
	patches := gomonkey.ApplyMethod(s.NSXClient, "NSXCheckVersion", func(_ *nsx.Client, _ int) bool { return false })
	defer patches.Reset()
	subnet := &model.VpcSubnet{
		Id: common.String("subnet"), Path: common.String("/orgs/default/projects/default/vpcs/vpc/subnets/subnet"),
		AccessMode: common.String(model.VpcSubnet_ACCESS_MODE_PUBLIC), IpAddresses: []string{"192.0.2.0/24"},
		AdvancedConfig: &model.SubnetAdvancedConfig{StaticIpAllocation: &model.StaticIpAllocation{Enabled: common.Bool(true)}},
	}
	port := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: "review", UID: "00000000-0000-0000-0000-000000000001"}, Spec: v1alpha1.SubnetPortSpec{
		InterfaceIPType: v1alpha1.IPAddressTypeIPv4, StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4,
		AddressBindings: []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10", MACAddress: "02:00:00:00:00:10"}},
	}}
	ok, err := s.AllocatePortFromSubnet(subnet, true, port.Spec.InterfaceIPType, port.Spec.StaticIPAllocationType, port.Spec.AddressBindings, port.UID)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = s.CreateOrUpdateSubnetPort(port, subnet, "", nil, true, false, port.Spec.InterfaceIPType)
	require.ErrorContains(t, err, "request timed out after backend commit")
	require.Len(t, backend.committed, 1)
	s.ReleasePortInSubnet(*subnet.Path, port.Spec.InterfaceIPType, port.Spec.StaticIPAllocationType, port.Spec.AddressBindings)
	cached, err := s.SubnetPortStore.GetVpcSubnetPortByUID(port.UID)
	require.NoError(t, err)
	require.Nil(t, cached, "failed Patch never populates the operator store")
	patches.ApplyMethod(s, "CheckSubnetPortState", func(_ *SubnetPortService, _ interface{}, _ string) (*model.SegmentPortState, error) {
		return &model.SegmentPortState{}, nil
	})
	originalID := *backend.committed[0].Id
	originalAttachment := *backend.committed[0].Attachment.Id
	for attempt := 0; attempt < 2; attempt++ {
		ok, err = s.AllocatePortFromSubnet(subnet, true, port.Spec.InterfaceIPType, port.Spec.StaticIPAllocationType, port.Spec.AddressBindings, port.UID)
		require.NoError(t, err)
		require.True(t, ok, "recover even when the backend pool is full")
		cached, err = s.SubnetPortStore.GetVpcSubnetPortByUID(port.UID)
		require.NoError(t, err)
		require.Equal(t, originalID, *cached.Id)
		// Recovery, unlike a new allocation, has no reservation to release.
		info, _ := s.SubnetPortStore.PortCountInfo.Load(*subnet.Path)
		require.Zero(t, info.(*CountInfo).dirtyStaticCount)
		require.Empty(t, info.(*CountInfo).requestedIPs)
		_, err = s.CreateOrUpdateSubnetPort(port, subnet, "", nil, true, false, port.Spec.InterfaceIPType)
		require.NoError(t, err)
		require.Len(t, backend.committed, 1)
		require.Equal(t, originalAttachment, *backend.committed[0].Attachment.Id)
	}
}

func TestRejectedRequestDoesNotPoisonRelayCapacity(t *testing.T) {
	s := createSubnetPortService(t)
	subnet := &model.VpcSubnet{
		Id: common.String("relay"), Path: common.String("/orgs/default/projects/default/vpcs/vpc/subnets/relay"),
		IpAddresses: []string{"192.0.2.0/28"}, SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: common.String(model.SubnetDhcpConfig_MODE_RELAY)},
	}
	require.NoError(t, s.SubnetPortStore.Apply(&model.VpcSubnetPort{Id: common.String("existing"), ParentPath: subnet.Path, AddressBindings: []model.PortAddressBindingEntry{{IpAddress: common.String("192.0.2.10")}}}))
	badBindings := []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10"}}
	ok, err := s.AllocatePortFromSubnet(subnet, false, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, badBindings)
	require.False(t, ok)
	require.ErrorContains(t, err, "already allocated")
	for attempt := 0; attempt < 2; attempt++ {
		ok, err = s.AllocatePortFromSubnet(subnet, false, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, nil)
		require.NoError(t, err)
		if !ok {
			t.Errorf("ordinary request %d falsely reports /28 subnet with only one used address exhausted", attempt+1)
		} else {
			s.ReleasePortInSubnet(*subnet.Path, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, nil)
		}
	}
}

func TestSharedSubnetRecoveryRejectsUnownedOrInvalidPorts(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*model.VpcSubnetPort)
	}{
		{"other CR", func(p *model.VpcSubnetPort) { p.Tags[0].Tag = common.String("other") }},
		{"other cluster", func(p *model.VpcSubnetPort) { p.Tags[1].Tag = common.String("other") }},
		{"untagged", func(p *model.VpcSubnetPort) { p.Tags = nil }},
		{"ambiguous owner", func(p *model.VpcSubnetPort) {
			p.Tags = append([]model.Tag{{Scope: common.String(common.TagScopeSubnetPortCRUID), Tag: common.String("other")}}, p.Tags...)
		}},
		{"missing tag scope", func(p *model.VpcSubnetPort) { p.Tags[0].Scope = nil }},
		{"missing tag value", func(p *model.VpcSubnetPort) { p.Tags[1].Tag = nil }},
		{"incomplete extra tag", func(p *model.VpcSubnetPort) { p.Tags = append(p.Tags, model.Tag{}) }},
		{"wrong path", func(p *model.VpcSubnetPort) { p.Path = common.String("/wrong/ports/port") }},
		{"missing ID", func(p *model.VpcSubnetPort) { p.Id = nil }},
		{"deleting", func(p *model.VpcSubnetPort) { p.MarkedForDelete = common.Bool(true) }},
		{"different address", func(p *model.VpcSubnetPort) { p.AddressBindings[0].IpAddress = common.String("192.0.2.11") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := createSubnetPortService(t)
			subnet := &model.VpcSubnet{Path: common.String("/orgs/default/projects/default/vpcs/vpc/subnets/subnet")}
			port := model.VpcSubnetPort{
				Id: common.String("port"), DisplayName: common.String("port"), ParentPath: subnet.Path,
				Path: common.String(*subnet.Path + "/ports/port"),
				Tags: []model.Tag{
					{Scope: common.String(common.TagScopeSubnetPortCRUID), Tag: common.String("owner")},
					{Scope: common.String(common.TagScopeCluster), Tag: common.String(getCluster(s))},
				},
				AddressBindings: []model.PortAddressBindingEntry{{IpAddress: common.String("192.0.2.10")}},
			}
			test.mutate(&port)
			s.NSXClient.PortClient = &commitThenTimeoutClient{committed: []model.VpcSubnetPort{port}}
			ok, err := s.AllocatePortFromSubnet(subnet, true, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeIPv4,
				[]v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10"}}, "owner")
			require.False(t, ok)
			require.Error(t, err)
			cached, err := s.SubnetPortStore.GetVpcSubnetPortByUID("owner")
			require.NoError(t, err)
			require.Nil(t, cached)
		})
	}
}

func TestRelayCapacityReinitializedAfterReset(t *testing.T) {
	s := createSubnetPortService(t)
	subnet := &model.VpcSubnet{Path: common.String("/orgs/default/projects/default/vpcs/vpc/subnets/relay"),
		Ipv4SubnetSize: common.Int64(4), SubnetDhcpConfig: &model.SubnetDhcpConfig{Mode: common.String(model.SubnetDhcpConfig_MODE_RELAY)}}
	ok, err := s.AllocatePortFromSubnet(subnet, false, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, nil)
	require.NoError(t, err)
	require.False(t, ok, "initialized zero-capacity pool must remain exhausted")
	subnet.Ipv4SubnetSize = common.Int64(16)
	s.ResetSubnetTotalIP(*subnet.Path)
	ok, err = s.AllocatePortFromSubnet(subnet, false, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, nil)
	require.NoError(t, err)
	require.True(t, ok)
	s.ReleasePortInSubnet(*subnet.Path, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeNone, nil)
}

type recoveryPageClient struct {
	fakePortClient
	own  model.VpcSubnetPort
	fail bool
}

func (c *recoveryPageClient) List(_, _, _, _ string, cursor *string, _, _ *bool, _ *string, _ *int64, _ *bool, _ *string) (model.VpcSubnetPortListResult, error) {
	if cursor == nil {
		return model.VpcSubnetPortListResult{Results: []model.VpcSubnetPort{c.own}, Cursor: common.String("next")}, nil
	}
	if c.fail {
		return model.VpcSubnetPortListResult{}, fmt.Errorf("later page failed")
	}
	return model.VpcSubnetPortListResult{Results: []model.VpcSubnetPort{{Id: common.String("foreign"), AddressBindings: c.own.AddressBindings}}}, nil
}

func TestSharedSubnetRecoveryChecksAllPagesBeforeCaching(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("listFailure=%t", fail), func(t *testing.T) {
			s := createSubnetPortService(t)
			subnet := &model.VpcSubnet{Path: common.String("/orgs/default/projects/default/vpcs/vpc/subnets/subnet")}
			s.NSXClient.PortClient = &recoveryPageClient{fail: fail, own: model.VpcSubnetPort{
				Id: common.String("port"), DisplayName: common.String("port"), ParentPath: subnet.Path,
				Path: common.String(*subnet.Path + "/ports/port"),
				Tags: []model.Tag{
					{Scope: common.String(common.TagScopeSubnetPortCRUID), Tag: common.String("owner")},
					{Scope: common.String(common.TagScopeCluster), Tag: common.String(getCluster(s))},
				},
				AddressBindings: []model.PortAddressBindingEntry{{IpAddress: common.String("192.0.2.10")}},
			}}
			ok, err := s.AllocatePortFromSubnet(subnet, true, v1alpha1.IPAddressTypeIPv4, v1alpha1.StaticIPAllocationTypeIPv4,
				[]v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10"}}, "owner")
			require.False(t, ok)
			if fail {
				require.ErrorContains(t, err, "later page failed")
			} else {
				require.ErrorContains(t, err, "already allocated")
			}
			cached, err := s.SubnetPortStore.GetVpcSubnetPortByUID("owner")
			require.NoError(t, err)
			require.Nil(t, cached, "failed checks must not turn a retry into an existing-port bypass")
		})
	}
}
