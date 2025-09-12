package subnetipreservation

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestBuildIPReservation(t *testing.T) {
	ipReservationCR := &v1alpha1.SubnetIPReservation{
		ObjectMeta: v1.ObjectMeta{
			Name:      "ipr-1",
			Namespace: "ns-1",
			UID:       "ipr-uid-1",
		},
		Spec: v1alpha1.SubnetIPReservationSpec{
			Subnet:      "subnet-1",
			NumberOfIPs: 10,
		},
	}

	expectedNSXIPReservation := &model.DynamicIpAddressReservation{
		Id:          common.String("ipr-1_3yw4m"),
		DisplayName: common.String("ipr-1"),
		NumberOfIps: common.Int64(10),
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeCluster),
				Tag:   common.String("fake_cluster"),
			},
			{
				Scope: common.String(common.TagScopeVersion),
				Tag:   common.String("1.0.0"),
			},
			{
				Scope: common.String(common.TagScopeNamespace),
				Tag:   common.String("ns-1"),
			},
			{
				Scope: common.String(common.TagScopeSubnetIPReservationCRName),
				Tag:   common.String("ipr-1"),
			},
			{
				Scope: common.String(common.TagScopeSubnetIPReservationCRUID),
				Tag:   common.String("ipr-uid-1"),
			},
		},
	}

	service := &IPReservationService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "fake_cluster",
				},
			},
		},
		IPReservationStore: SetupStore(),
	}
	nsxIPReservation := service.buildIPReservation(ipReservationCR, "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1")
	require.Equal(t, expectedNSXIPReservation, nsxIPReservation)
}
