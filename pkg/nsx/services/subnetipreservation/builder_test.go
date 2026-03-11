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

func TestBuildDynamicIPReservation(t *testing.T) {
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
		DynamicIPReservationStore: SetupDynamicIPReservationStore(),
		StaticIPReservationStore:  SetupStaticIPReservationStore(),
	}
	nsxIPReservation := service.buildDynamicIPReservation(ipReservationCR, "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1")
	require.Equal(t, expectedNSXIPReservation, nsxIPReservation)
}

func TestBuildStaticIPReservation(t *testing.T) {
	tests := []struct {
		name                string
		ipReservationCR     *v1alpha1.SubnetIPReservation
		expectedReservedIPs []string
		expectedDisplayName string
	}{
		{
			name: "WithReservedIPs",
			ipReservationCR: &v1alpha1.SubnetIPReservation{
				ObjectMeta: v1.ObjectMeta{
					Name:      "sipr-1",
					Namespace: "ns-1",
					UID:       "sipr-uid-1",
				},
				Spec: v1alpha1.SubnetIPReservationSpec{
					Subnet:      "subnet-1",
					ReservedIPs: []string{"192.168.1.1", "192.168.1.2-192.168.1.5", "192.168.2.0/28"},
				},
			},
			expectedReservedIPs: []string{"192.168.1.1", "192.168.1.2-192.168.1.5", "192.168.2.0/28"},
			expectedDisplayName: "sipr-1",
		},
		{
			name: "RestoreMode_UsesStatusIPs",
			ipReservationCR: &v1alpha1.SubnetIPReservation{
				ObjectMeta: v1.ObjectMeta{
					Name:      "sipr-restore",
					Namespace: "ns-1",
					UID:       "sipr-restore-uid",
				},
				Spec: v1alpha1.SubnetIPReservationSpec{
					Subnet: "subnet-1",
					// ReservedIPs empty - restore scenario
				},
				Status: v1alpha1.SubnetIPReservationStatus{
					IPs: []string{"10.0.0.1", "10.0.0.2"},
				},
			},
			expectedReservedIPs: []string{"10.0.0.1", "10.0.0.2"},
			expectedDisplayName: "sipr-restore",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &IPReservationService{
				Service: common.Service{
					NSXConfig: &config.NSXOperatorConfig{
						CoeConfig: &config.CoeConfig{
							Cluster: "fake_cluster",
						},
					},
				},
				DynamicIPReservationStore: SetupDynamicIPReservationStore(),
				StaticIPReservationStore:  SetupStaticIPReservationStore(),
			}
			nsxStatic := service.buildStaticIPReservation(tt.ipReservationCR, "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1")
			require.Equal(t, tt.expectedReservedIPs, nsxStatic.ReservedIps)
			require.Equal(t, tt.expectedDisplayName, *nsxStatic.DisplayName)
			require.NotEmpty(t, *nsxStatic.Id)
		})
	}
}
