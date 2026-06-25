package subnetipreservation

import (
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	controllerscommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func newTestService() *IPReservationService {
	return &IPReservationService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "fake_cluster",
				},
			},
		},
		DynamicIPReservationStore: SetupDynamicIPReservationStore(),
		StaticIPReservationStore:  SetupStaticIPReservationStore(),
	}
}

func baseTags(namespace, name, uid string) []model.Tag {
	return []model.Tag{
		{Scope: common.String(common.TagScopeCluster), Tag: common.String("fake_cluster")},
		{Scope: common.String(common.TagScopeVersion), Tag: common.String("1.0.0")},
		{Scope: common.String(common.TagScopeNamespace), Tag: common.String(namespace)},
		{Scope: common.String(common.TagScopeSubnetIPReservationCRName), Tag: common.String(name)},
		{Scope: common.String(common.TagScopeSubnetIPReservationCRUID), Tag: common.String(uid)},
	}
}

func TestBuildDynamicIPReservation(t *testing.T) {
	subnetPath := "/orgs/default/projects/default/vpcs/ns-1/subnets/subnet-1"
	// Covers both the current mixed-case CRD enum values ("IPv4"/"IPv6"/"IPv4IPv6") and
	// legacy all-caps values ("IPV4"/"IPV6"/"IPV4IPV6") that may exist in older Subnet CRs,
	// as well as the empty / unset case.
	strPtr := func(s string) *string { return &s }
	tests := []struct {
		name              string
		ipAddressType     v1alpha1.IPAddressType
		expectedNSXFamily *string
	}{
		// Feature supported: IpAddressType is set in the NSX request.
		{
			name:              "Supported_DefaultIPv4WhenUnset",
			ipAddressType:     "",
			expectedNSXFamily: nil,
		},
		{
			name:              "Supported_MixedCase_IPv4",
			ipAddressType:     "IPv4",
			expectedNSXFamily: strPtr(controllerscommon.ConvertCRIPAddressTypeToNSX(v1alpha1.IPAddressTypeIPv4)),
		},
		{
			name:              "Supported_MixedCase_IPv6",
			ipAddressType:     "IPv6",
			expectedNSXFamily: strPtr(controllerscommon.ConvertCRIPAddressTypeToNSX(v1alpha1.IPAddressTypeIPv6)),
		},
		{
			name:              "Supported_MixedCase_DualStack",
			ipAddressType:     "IPv4IPv6",
			expectedNSXFamily: strPtr(controllerscommon.ConvertCRIPAddressTypeToNSX(v1alpha1.IPAddressTypeIPv4IPv6)),
		},
		{
			name:              "Supported_AllCaps_IPV4",
			ipAddressType:     "IPV4",
			expectedNSXFamily: strPtr(controllerscommon.ConvertCRIPAddressTypeToNSX(v1alpha1.IPAddressType("IPV4"))),
		},
		{
			name:              "Supported_AllCaps_IPV6",
			ipAddressType:     "IPV6",
			expectedNSXFamily: strPtr(controllerscommon.ConvertCRIPAddressTypeToNSX(v1alpha1.IPAddressType("IPV6"))),
		},
		{
			name:              "Supported_AllCaps_DualStack",
			ipAddressType:     "IPV4IPV6",
			expectedNSXFamily: strPtr(controllerscommon.ConvertCRIPAddressTypeToNSX(v1alpha1.IPAddressType("IPV4IPV6"))),
		},
	}

	service := newTestService()
	patches := gomonkey.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool {
		return true
	})
	defer patches.Reset()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ipReservationCR := &v1alpha1.SubnetIPReservation{
				ObjectMeta: v1.ObjectMeta{
					Name:      "ipr-1",
					Namespace: "ns-1",
					UID:       "ipr-uid-1",
				},
				Spec: v1alpha1.SubnetIPReservationSpec{
					Subnet:        "subnet-1",
					NumberOfIPs:   10,
					IPAddressType: tt.ipAddressType,
				},
			}
			nsxIPReservation := service.buildDynamicIPReservation(ipReservationCR, subnetPath)
			require.Equal(t, tt.expectedNSXFamily, nsxIPReservation.IpAddressType)
			require.Equal(t, int64(10), *nsxIPReservation.NumberOfIps)
			require.Equal(t, "ipr-1", *nsxIPReservation.DisplayName)
			require.Equal(t, baseTags("ns-1", "ipr-1", "ipr-uid-1"), nsxIPReservation.Tags)
		})
	}
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
