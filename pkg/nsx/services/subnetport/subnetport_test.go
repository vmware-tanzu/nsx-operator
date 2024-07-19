package subnetport

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestListNSXSubnetPortIDForCR(t *testing.T) {
	subnetPortService := createSubnetPortService()
	crName := "fake_subnetport"
	crUUID := "2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"
	subnetPortByCR := &model.VpcSubnetPort{
		DisplayName: common.String(crName),
		Id:          common.String(fmt.Sprintf("%s-%s", crName, crUUID)),
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
				Tag:   common.String(crName),
			},
			{
				Scope: common.String("nsx-op/subnetport_uid"),
				Tag:   common.String(crUUID),
			},
		},
		Path:       common.String("/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1/ports/ports/fake_subnetport-2ccec3b9-7546-4fd2-812a-1e3a4afd7acc"),
		ParentPath: common.String("/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1"),
		Attachment: &model.PortAttachment{
			AllocateAddresses: common.String("DHCP"),
			Type_:             common.String("STATIC"),
			Id:                common.String("66616b65-5f73-4562-ae65-74706f72742d"),
			TrafficTag:        common.Int64(0),
		},
	}
	subnetPortService.SubnetPortStore.Add(subnetPortByCR)
	subnetPortIDs := subnetPortService.ListNSXSubnetPortIDForCR()
	assert.Equal(t, 1, len(subnetPortIDs))
	assert.Equal(t, *subnetPortByCR.Id, subnetPortIDs.UnsortedList()[0])
}

func TestListNSXSubnetPortIDForPod(t *testing.T) {
	subnetPortService := createSubnetPortService()
	podName := "fake_pod"
	podUUID := "c5db1800-ce4c-11de-a935-8105ba7ace78"
	subnetPortByPod := &model.VpcSubnetPort{
		DisplayName: common.String(podName),
		Id:          common.String(fmt.Sprintf("fake_pod-%s", podUUID)),
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
				Tag:   common.String(podName),
			},
			{
				Scope: common.String("nsx-op/pod_uid"),
				Tag:   common.String(podUUID),
			},
		},
		Path:       common.String("/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1/ports/fake_pod-c5db1800-ce4c-11de-a935-8105ba7ace78"),
		ParentPath: common.String("/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1"),
		Attachment: &model.PortAttachment{
			AllocateAddresses: common.String("DHCP"),
			Type_:             common.String("STATIC"),
			Id:                common.String("66616b65-5f70-4f64-ad63-356462313830"),
			TrafficTag:        common.Int64(0),
			AppId:             common.String(podUUID),
			ContextId:         common.String("fake_context_id"),
		},
	}
	subnetPortService.SubnetPortStore.Add(subnetPortByPod)
	subnetPortIDs := subnetPortService.ListNSXSubnetPortIDForPod()
	assert.Equal(t, 1, len(subnetPortIDs))
	assert.Equal(t, *subnetPortByPod.Id, subnetPortIDs.UnsortedList()[0])
}

func createSubnetPortService() *SubnetPortService {
	return &SubnetPortService{
		SubnetPortStore: &SubnetPortStore{ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc,
				cache.Indexers{
					common.TagScopeSubnetPortCRUID: subnetPortIndexByCRUID,
					common.TagScopePodUID:          subnetPortIndexByPodUID,
					common.IndexKeySubnetID:        subnetPortIndexBySubnetID,
				}),
			BindingType: model.VpcSubnetPortBindingType(),
		}},
	}
}
