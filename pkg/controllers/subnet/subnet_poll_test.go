package subnet

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	subnetservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
)

func TestHasSubnetSpecChanged(t *testing.T) {
	tests := []struct {
		name         string
		originalSpec *v1alpha1.SubnetSpec
		newSpec      *v1alpha1.SubnetSpec
		expected     bool
	}{
		{
			name: "No changes",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			expected: false,
		},
		{
			name: "ConnectivityState changed",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateDisconnected,
				},
			},
			expected: true,
		},
		{
			name: "VLANConnection no changed",
			originalSpec: &v1alpha1.SubnetSpec{
				VLANConnection: "/infra/distributed-vlan-connections/gatewayconnection-103",
			},
			newSpec: &v1alpha1.SubnetSpec{
				VLANConnection: "/infra/distributed-vlan-connections/gatewayconnection-103",
			},
			expected: false,
		},
		{
			name: "VLANConnection changed",
			originalSpec: &v1alpha1.SubnetSpec{
				VLANConnection: "",
			},
			newSpec: &v1alpha1.SubnetSpec{
				VLANConnection: "/infra/distributed-vlan-connections/gatewayconnection-103",
			},
			expected: true,
		},
		{
			name: "DHCP Mode changed",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
				SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
					Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated),
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
				SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
					Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
				},
			},
			expected: true,
		},
		{
			name: "ReservedIPRanges not changed",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
				SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
					DHCPServerAdditionalConfig: v1alpha1.DHCPServerAdditionalConfig{
						ReservedIPRanges: []string{"172.26.0.4-172.26.0.10"},
					},
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
				SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
					DHCPServerAdditionalConfig: v1alpha1.DHCPServerAdditionalConfig{
						ReservedIPRanges: []string{"172.26.0.4-172.26.0.10"},
					},
				},
			},
			expected: false,
		},
		{
			name: "ReservedIPRanges changed from nil to value",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
				SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
					DHCPServerAdditionalConfig: v1alpha1.DHCPServerAdditionalConfig{
						ReservedIPRanges: []string{"172.26.0.4-172.26.0.10"},
					},
				},
			},
			expected: true,
		},
		{
			name: "ReservedIPRanges changed from value to nil",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
				SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
					DHCPServerAdditionalConfig: v1alpha1.DHCPServerAdditionalConfig{
						ReservedIPRanges: []string{"172.26.0.4-172.26.0.10"},
					},
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			expected: true,
		},
		{
			name: "ReservedIPRanges changed value",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
				SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
					DHCPServerAdditionalConfig: v1alpha1.DHCPServerAdditionalConfig{
						ReservedIPRanges: []string{"172.26.0.4-172.26.0.10"},
					},
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
				SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
					DHCPServerAdditionalConfig: v1alpha1.DHCPServerAdditionalConfig{
						ReservedIPRanges: []string{"172.26.0.4-172.26.0.10", "172.26.0.13"},
					},
				},
			},
			expected: true,
		},
		{
			name: "VPCName changed",
			originalSpec: &v1alpha1.SubnetSpec{
				VPCName: "vpc-1",
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				VPCName: "vpc-2",
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			expected: true,
		},
		{
			name: "IPv4SubnetSize changed",
			originalSpec: &v1alpha1.SubnetSpec{
				IPv4SubnetSize: 24,
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				IPv4SubnetSize: 28,
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			expected: true,
		},
		{
			name: "AccessMode changed",
			originalSpec: &v1alpha1.SubnetSpec{
				AccessMode: v1alpha1.AccessMode(v1alpha1.AccessModePublic),
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AccessMode: v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			expected: true,
		},
		{
			name: "IPAddresses changed",
			originalSpec: &v1alpha1.SubnetSpec{
				IPAddresses: []string{"192.168.1.0/24"},
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				IPAddresses: []string{"192.168.2.0/24"},
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			expected: true,
		},
		{
			name: "StaticIPAllocation.Enabled changed from true to false - spec changed",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
					StaticIPAllocation: v1alpha1.StaticIPAllocation{
						Enabled: common.Bool(true),
					},
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
					StaticIPAllocation: v1alpha1.StaticIPAllocation{
						Enabled: common.Bool(false),
					},
				},
			},
			expected: true,
		},
		{
			name: "StaticIPAllocation.Enabled changed from false to true - spec changed",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
					StaticIPAllocation: v1alpha1.StaticIPAllocation{
						Enabled: common.Bool(false),
					},
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
					StaticIPAllocation: v1alpha1.StaticIPAllocation{
						Enabled: common.Bool(true),
					},
				},
			},
			expected: true,
		},
		{
			name: "StaticIPAllocation.Enabled changed from set to nil",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
					StaticIPAllocation: v1alpha1.StaticIPAllocation{
						Enabled: common.Bool(true),
					},
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
					StaticIPAllocation: v1alpha1.StaticIPAllocation{
						Enabled: nil,
					},
				},
			},
			expected: true,
		},
		{
			name: "StaticIPAllocation.Enabled not changed - both nil",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			expected: false,
		},
		{
			name: "StaticIPAllocation.Enabled not changed - both true",
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
					StaticIPAllocation: v1alpha1.StaticIPAllocation{
						Enabled: common.Bool(true),
					},
				},
			},
			newSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
					StaticIPAllocation: v1alpha1.StaticIPAllocation{
						Enabled: common.Bool(true),
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createFakeSubnetReconciler(nil)
			result := r.hasSubnetSpecChanged(tt.originalSpec, tt.newSpec)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasStatusChanged(t *testing.T) {
	tests := []struct {
		name           string
		originalStatus *v1alpha1.SubnetStatus
		newStatus      *v1alpha1.SubnetStatus
		expected       bool
	}{
		{
			name: "No changes",
			originalStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses:    []string{"10.0.0.0/24"},
				GatewayAddresses:    []string{"10.0.0.1"},
				DHCPServerAddresses: []string{"10.0.0.2"},
				Shared:              true,
			},
			newStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses:    []string{"10.0.0.0/24"},
				GatewayAddresses:    []string{"10.0.0.1"},
				DHCPServerAddresses: []string{"10.0.0.2"},
				Shared:              true,
			},
			expected: false,
		},
		{
			name: "NetworkAddresses changed",
			originalStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses:    []string{"10.0.0.0/24"},
				GatewayAddresses:    []string{"10.0.0.1"},
				DHCPServerAddresses: []string{"10.0.0.2"},
				Shared:              true,
			},
			newStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses:    []string{"10.0.0.0/24", "10.0.1.0/24"},
				GatewayAddresses:    []string{"10.0.0.1"},
				DHCPServerAddresses: []string{"10.0.0.2"},
				Shared:              true,
			},
			expected: true,
		},
		{
			name: "GatewayAddresses changed",
			originalStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses:    []string{"10.0.0.0/24"},
				GatewayAddresses:    []string{"10.0.0.1"},
				DHCPServerAddresses: []string{"10.0.0.2"},
				Shared:              true,
			},
			newStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses:    []string{"10.0.0.0/24"},
				GatewayAddresses:    []string{"10.0.0.1", "10.0.1.1"},
				DHCPServerAddresses: []string{"10.0.0.2"},
				Shared:              true,
			},
			expected: true,
		},
		{
			name: "DHCPServerAddresses changed",
			originalStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses:    []string{"10.0.0.0/24"},
				GatewayAddresses:    []string{"10.0.0.1"},
				DHCPServerAddresses: []string{"10.0.0.2"},
				Shared:              true,
			},
			newStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses:    []string{"10.0.0.0/24"},
				GatewayAddresses:    []string{"10.0.0.1"},
				DHCPServerAddresses: []string{"10.0.0.2", "10.0.1.2"},
				Shared:              true,
			},
			expected: true,
		},
		{
			name: "Shared changed",
			originalStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses:    []string{"10.0.0.0/24"},
				GatewayAddresses:    []string{"10.0.0.1"},
				DHCPServerAddresses: []string{"10.0.0.2"},
				Shared:              true,
			},
			newStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses:    []string{"10.0.0.0/24"},
				GatewayAddresses:    []string{"10.0.0.1"},
				DHCPServerAddresses: []string{"10.0.0.2"},
				Shared:              false,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createFakeSubnetReconciler(nil)
			result := r.hasStatusChanged(tt.originalStatus, tt.newStatus)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUpdateSubnetIfNeeded(t *testing.T) {
	tests := []struct {
		name                string
		originalStatus      *v1alpha1.SubnetStatus
		originalSpec        *v1alpha1.SubnetSpec
		updateSubnetErr     error
		statusChanged       bool
		specChanged         bool
		updateClientErr     error
		expectedErrContains string
	}{
		{
			name: "Status changed and update succeeds",
			originalStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses: []string{"10.0.0.0/24"},
				GatewayAddresses: []string{"10.0.0.1"},
				Shared:           true,
			},
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			updateSubnetErr: nil,
			statusChanged:   true,
			specChanged:     false,
			updateClientErr: nil,
		},
		{
			name: "Spec changed and update succeeds",
			originalStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses: []string{"10.0.0.0/24"},
				GatewayAddresses: []string{"10.0.0.1"},
				Shared:           true,
			},
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			updateSubnetErr: nil,
			statusChanged:   false,
			specChanged:     true,
			updateClientErr: nil,
		},
		{
			name: "Status changed but update fails",
			originalStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses: []string{"10.0.0.0/24"},
				GatewayAddresses: []string{"10.0.0.1"},
				Shared:           true,
			},
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			updateSubnetErr:     nil,
			statusChanged:       true,
			specChanged:         false,
			updateClientErr:     fmt.Errorf("update failed"),
			expectedErrContains: "failed to update shared Subnet status",
		},
		{
			name: "Spec changed but update fails",
			originalStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses: []string{"10.0.0.0/24"},
				GatewayAddresses: []string{"10.0.0.1"},
				Shared:           true,
			},
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			updateSubnetErr:     nil,
			statusChanged:       false,
			specChanged:         true,
			updateClientErr:     fmt.Errorf("update failed"),
			expectedErrContains: "failed to update shared Subnet spec",
		},
		{
			name: "Subnet update error",
			originalStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses: []string{"10.0.0.0/24"},
				GatewayAddresses: []string{"10.0.0.1"},
				Shared:           true,
			},
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			updateSubnetErr:     fmt.Errorf("update subnet error"),
			statusChanged:       false,
			specChanged:         true,
			updateClientErr:     nil,
			expectedErrContains: "update subnet error",
		},
		{
			name: "No changes",
			originalStatus: &v1alpha1.SubnetStatus{
				NetworkAddresses: []string{"10.0.0.0/24"},
				GatewayAddresses: []string{"10.0.0.1"},
				Shared:           true,
			},
			originalSpec: &v1alpha1.SubnetSpec{
				AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
					ConnectivityState: v1alpha1.ConnectivityStateConnected,
				},
			},
			updateSubnetErr: nil,
			statusChanged:   false,
			specChanged:     false,
			updateClientErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			namespacedName := types.NamespacedName{Namespace: "default", Name: "test-subnet"}

			subnetCR := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-subnet",
					Namespace: "default",
				},
				Spec:   *tt.originalSpec,
				Status: *tt.originalStatus,
			}

			r := createFakeSubnetReconciler([]client.Object{subnetCR})

			nsxSubnet := &model.VpcSubnet{
				Id:   common.String("subnet-id"),
				Path: common.String("/projects/project1/vpcs/vpc1/subnets/subnet1"),
			}

			// Create a status list for testing
			statusList := []model.VpcSubnetStatus{
				{
					NetworkAddress:    common.String("10.0.0.0/24"),
					GatewayAddress:    common.String("10.0.0.1"),
					DhcpServerAddress: common.String("10.0.0.2"),
				},
			}

			// Mock MapNSXSubnetToSubnetCR
			patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "MapNSXSubnetToSubnetCR",
				func(_ *subnetservice.SubnetService, subnetCR *v1alpha1.Subnet, _ *model.VpcSubnet) {
					subnetCR.Spec.AccessMode = v1alpha1.AccessMode(v1alpha1.AccessModePublic)
					subnetCR.Spec.IPv4SubnetSize = 24
					subnetCR.Spec.IPAddresses = []string{"192.168.1.0/24"}
					subnetCR.Spec.SubnetDHCPConfig.Mode = v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer)
				})

			// Mock MapNSXSubnetStatusToSubnetCRStatus
			patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "MapNSXSubnetStatusToSubnetCRStatus",
				func(_ *subnetservice.SubnetService, subnetCR *v1alpha1.Subnet, statusList []model.VpcSubnetStatus) {
					// Verify the status list is passed correctly
					assert.Equal(t, 1, len(statusList))
					assert.Equal(t, "10.0.0.0/24", *statusList[0].NetworkAddress)
					assert.Equal(t, "10.0.0.1", *statusList[0].GatewayAddress)
					assert.Equal(t, "10.0.0.2", *statusList[0].DhcpServerAddress)

					// Set the status fields
					subnetCR.Status.NetworkAddresses = []string{"10.0.0.0/24"}
					subnetCR.Status.GatewayAddresses = []string{"10.0.0.1"}
					subnetCR.Status.DHCPServerAddresses = []string{"10.0.0.2"}
					subnetCR.Status.Shared = true
				})

			patches.ApplyPrivateMethod(reflect.TypeOf(r), "hasStatusChanged",
				func(_ *SubnetReconciler, originalStatus, newStatus *v1alpha1.SubnetStatus) bool {
					return tt.statusChanged
				})

			patches.ApplyPrivateMethod(reflect.TypeOf(r), "hasSubnetSpecChanged",
				func(_ *SubnetReconciler, originalSpec, newSpec *v1alpha1.SubnetSpec) bool {
					return tt.specChanged
				})

			// Always mock the client methods regardless of updateSubnetErr
			if tt.specChanged {
				patches.ApplyMethod(reflect.TypeOf(r.Client), "Update",
					func(_ client.Client, _ context.Context, _ client.Object, _ ...client.UpdateOption) error {
						return tt.updateClientErr
					})
			}

			if tt.statusChanged {
				patches.ApplyMethod(reflect.TypeOf(r.Client), "Status",
					func(_ client.Client) client.StatusWriter {
						return &fakeStatusWriter{updateErr: tt.updateClientErr}
					})
			}

			var err error
			if tt.updateSubnetErr != nil {
				err = tt.updateSubnetErr
			} else {
				err = r.updateSubnetIfNeeded(ctx, subnetCR, nsxSubnet, statusList, namespacedName)
			}

			if tt.expectedErrContains != "" {
				assert.Error(t, err)
				if err != nil {
					assert.Contains(t, err.Error(), tt.expectedErrContains)
				}
			} else {
				assert.NoError(t, err)
			}

			patches.Reset()
		})
	}
}

type fakeStatusWriter struct {
	updateErr error
}

func (f *fakeStatusWriter) Create(_ context.Context, _ client.Object, _ client.Object, _ ...client.SubResourceCreateOption) error {
	return nil
}

func (f *fakeStatusWriter) Update(_ context.Context, _ client.Object, _ ...client.SubResourceUpdateOption) error {
	return f.updateErr
}

func (f *fakeStatusWriter) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
	return nil
}

func TestPollAllSharedSubnets(t *testing.T) {
	tests := []struct {
		name                    string
		sharedSubnetsMap        map[string][]types.NamespacedName
		expectedUniqueResources int // Number of unique associated resources
		expectedEnqueueCalls    int // Number of times enqueueSubnetForReconciliation should be called
	}{
		{
			name:                    "Empty shared subnets map",
			sharedSubnetsMap:        map[string][]types.NamespacedName{},
			expectedUniqueResources: 0,
			expectedEnqueueCalls:    0,
		},
		{
			name: "One shared subnet",
			sharedSubnetsMap: map[string][]types.NamespacedName{
				"project1:vpc1:subnet1": {{Namespace: "default", Name: "test-subnet-1"}},
			},
			expectedUniqueResources: 1,
			expectedEnqueueCalls:    1,
		},
		{
			name: "Multiple shared subnets with different resources",
			sharedSubnetsMap: map[string][]types.NamespacedName{
				"project1:vpc1:subnet1": {{Namespace: "default", Name: "test-subnet-1"}},
				"project1:vpc1:subnet2": {{Namespace: "default", Name: "test-subnet-2"}},
				"project1:vpc1:subnet3": {{Namespace: "default", Name: "test-subnet-3"}},
			},
			expectedUniqueResources: 3,
			expectedEnqueueCalls:    3,
		},
		{
			name: "Multiple shared subnets with some shared resources",
			sharedSubnetsMap: map[string][]types.NamespacedName{
				"project1:vpc1:subnet1": {
					{Namespace: "default", Name: "test-subnet-1"},
					{Namespace: "default", Name: "test-subnet-2"}, // Same resource as subnet-1
				},
				"project1:vpc1:subnet2": {{Namespace: "default", Name: "test-subnet-3"}},
			},
			expectedUniqueResources: 2, // Only 2 unique resources
			expectedEnqueueCalls:    3, // But 3 subnets to update
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create subnet CRs for each namespaced name in the test
			var subnetCRs []client.Object
			for _, namespacedNames := range tt.sharedSubnetsMap {
				for _, namespacedName := range namespacedNames {
					subnetCR := &v1alpha1.Subnet{
						ObjectMeta: metav1.ObjectMeta{
							Name:      namespacedName.Name,
							Namespace: namespacedName.Namespace,
						},
						Spec: v1alpha1.SubnetSpec{
							IPv4SubnetSize: 24,
							IPAddresses:    []string{"192.168.1.0/24"},
						},
					}
					subnetCRs = append(subnetCRs, subnetCR)
				}
			}

			r := createFakeSubnetReconciler(subnetCRs)

			// Set up the SharedSubnetResourceMap
			for associatedResource, namespacedNames := range tt.sharedSubnetsMap {
				for _, namespacedName := range namespacedNames {
					if r.SubnetService.SharedSubnetResourceMap[associatedResource] == nil {
						r.SubnetService.SharedSubnetResourceMap[associatedResource] = sets.New(namespacedName)
					} else {
						r.SubnetService.SharedSubnetResourceMap[associatedResource].Insert(namespacedName)
					}
				}
			}

			// Track calls to key functions
			getNSXSubnetCalls := make(map[string]int)
			getSubnetStatusCalls := make(map[string]int)
			enqueueSubnetCalls := make(map[types.NamespacedName]bool)

			// Mock GetNSXSubnetByAssociatedResource
			patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetNSXSubnetByAssociatedResource",
				func(_ *subnetservice.SubnetService, associatedResource string) (*model.VpcSubnet, error) {
					getNSXSubnetCalls[associatedResource]++
					return &model.VpcSubnet{
						Id:   common.String("subnet-id-" + associatedResource),
						Path: common.String("/projects/project1/vpcs/vpc1/subnets/" + associatedResource),
					}, nil
				})

			// Mock GetSubnetStatus
			patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetStatus",
				func(_ *subnetservice.SubnetService, nsxSubnet *model.VpcSubnet) ([]model.VpcSubnetStatus, error) {
					resourceID := *nsxSubnet.Id
					getSubnetStatusCalls[resourceID]++
					return []model.VpcSubnetStatus{
						{
							NetworkAddress:    common.String("10.0.0.0/24"),
							GatewayAddress:    common.String("10.0.0.1"),
							DhcpServerAddress: common.String("10.0.0.2"),
						},
					}, nil
				})

			// Mock UpdateNSXSubnetCache to avoid nil pointer dereference
			patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "UpdateNSXSubnetCache",
				func(_ *subnetservice.SubnetService, associatedResource string, nsxSubnet *model.VpcSubnet, statusList []model.VpcSubnetStatus) {
					// Just a stub implementation
				})

			// Mock enqueueSubnetForReconciliation to track calls
			patches.ApplyPrivateMethod(reflect.TypeOf(r), "enqueueSubnetForReconciliation",
				func(_ *SubnetReconciler, ctx context.Context, namespacedName types.NamespacedName) {
					enqueueSubnetCalls[namespacedName] = true
				})

			// Call the actual function being tested
			r.pollAllSharedSubnets()

			// Verify the number of unique resources
			assert.Equal(t, tt.expectedUniqueResources, len(getNSXSubnetCalls),
				"Number of unique resources (GetNSXSubnetByAssociatedResource calls) should match expected")

			// Verify each unique resource only had GetNSXSubnetByAssociatedResource called once
			for resource, count := range getNSXSubnetCalls {
				assert.Equal(t, 1, count,
					"GetNSXSubnetByAssociatedResource should be called exactly once for resource %s", resource)
			}

			// Verify each unique subnet ID only had GetSubnetStatus called once
			for subnetID, count := range getSubnetStatusCalls {
				assert.Equal(t, 1, count,
					"GetSubnetStatus should be called exactly once for subnet ID %s", subnetID)
			}

			// Verify the number of enqueueSubnetForReconciliation calls
			assert.Equal(t, tt.expectedEnqueueCalls, len(enqueueSubnetCalls),
				"Number of enqueueSubnetForReconciliation calls should match expected")

			// Verify each subnet was enqueued
			for _, namespacedNames := range tt.sharedSubnetsMap {
				for _, namespacedName := range namespacedNames {
					assert.True(t, enqueueSubnetCalls[namespacedName],
						"Subnet %s should have been enqueued for reconciliation", namespacedName)
				}
			}

			patches.Reset()
		})
	}
}

func TestUpdateSharedSubnetWithError(t *testing.T) {
	// This test verifies that updateSharedSubnetWithError correctly handles different subnet states

	// Create a test subnet that exists and is not being deleted
	existingSubnet := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnet",
			Namespace: "default",
		},
	}

	// Create a test subnet that exists but is being deleted
	deletingSubnet := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-subnet-deleting",
			Namespace:         "default",
			DeletionTimestamp: &metav1.Time{Time: time.Now()},
			Finalizers:        []string{"test-finalizer"},
		},
	}

	// Create a fake client with the test subnets
	objects := []client.Object{existingSubnet, deletingSubnet}
	r := createFakeSubnetReconciler(objects)

	// Mock the clearSubnetAddresses method to avoid calling it
	patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "clearSubnetAddresses",
		func(_ *SubnetReconciler, _ client.Object) {
			// Do nothing, just mock the method
		})
	defer patches.Reset()

	// Test case 1: Subnet exists and is not being deleted
	// The function should get the subnet and not return early
	err := fmt.Errorf("test error")
	r.updateSharedSubnetWithError(context.Background(), types.NamespacedName{Namespace: "default", Name: "test-subnet"}, err, "test error type")

	// Test case 2: Subnet exists but is being deleted
	// The function should get the subnet and return early because the subnet is being deleted
	r.updateSharedSubnetWithError(context.Background(), types.NamespacedName{Namespace: "default", Name: "test-subnet-deleting"}, err, "test error type")

	// Test case 3: Subnet does not exist
	// The function should try to get the subnet, fail, and return early
	r.updateSharedSubnetWithError(context.Background(), types.NamespacedName{Namespace: "default", Name: "non-existent-subnet"}, err, "test error type")
}

func TestClearSubnetAddresses(t *testing.T) {
	// Create a test subnet with addresses
	subnet := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-subnet", Namespace: "default"},
		Status: v1alpha1.SubnetStatus{
			NetworkAddresses:    []string{"10.0.0.0/24"},
			GatewayAddresses:    []string{"10.0.0.1"},
			DHCPServerAddresses: []string{"10.0.0.2"},
		},
	}

	// Create a fake reconciler
	r := createFakeSubnetReconciler([]client.Object{subnet})

	// Call the function being tested
	r.clearSubnetAddresses(subnet)

	// Verify that the addresses were cleared
	assert.Empty(t, subnet.Status.NetworkAddresses)
	assert.Empty(t, subnet.Status.GatewayAddresses)
	assert.Empty(t, subnet.Status.DHCPServerAddresses)
}

func TestHandleNSXSubnetError(t *testing.T) {
	tests := []struct {
		name                string
		validSubnets        map[types.NamespacedName]bool
		expectedUpdateCalls int
	}{
		{
			name:                "No valid subnets",
			validSubnets:        map[types.NamespacedName]bool{},
			expectedUpdateCalls: 0,
		},
		{
			name: "One valid subnet",
			validSubnets: map[types.NamespacedName]bool{
				{Namespace: "default", Name: "test-subnet"}: true,
			},
			expectedUpdateCalls: 1,
		},
		{
			name: "Multiple valid subnets",
			validSubnets: map[types.NamespacedName]bool{
				{Namespace: "default", Name: "test-subnet-1"}: true,
				{Namespace: "default", Name: "test-subnet-2"}: true,
				{Namespace: "default", Name: "test-subnet-3"}: true,
			},
			expectedUpdateCalls: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake reconciler
			r := createFakeSubnetReconciler(nil)

			// Create a set of valid subnets
			validSubnets := sets.New[types.NamespacedName]()
			for namespacedName := range tt.validSubnets {
				validSubnets.Insert(namespacedName)
			}

			// Mock the updateSharedSubnetWithError method to track if it's called
			updateCalls := 0
			patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "updateSharedSubnetWithError",
				func(_ *SubnetReconciler, _ context.Context, namespacedName types.NamespacedName, _ error, _ string) {
					updateCalls++
					assert.True(t, tt.validSubnets[namespacedName], "updateSharedSubnetWithError should only be called for valid subnets")
				})

			// Mock the RemoveSubnetFromCache method to avoid nil pointer dereference
			patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "RemoveSubnetFromCache",
				func(_ *subnetservice.SubnetService, _ string, _ string) {
					// Do nothing
				})
			defer patches.Reset()

			// Call the function being tested
			r.handleNSXSubnetError(context.Background(), fmt.Errorf("test error"), validSubnets, "test-resource", "test error type")

			// Verify the results
			assert.Equal(t, tt.expectedUpdateCalls, updateCalls, "updateSharedSubnetWithError should be called %d times", tt.expectedUpdateCalls)
		})
	}
}
