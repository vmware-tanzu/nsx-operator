package subnet

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	subnetservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
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
				VLANConnectionName: "gatewayconnection-103",
			},
			newSpec: &v1alpha1.SubnetSpec{
				VLANConnectionName: "gatewayconnection-103",
			},
			expected: false,
		},
		{
			name: "VLANConnection changed",
			originalSpec: &v1alpha1.SubnetSpec{
				VLANConnectionName: "",
			},
			newSpec: &v1alpha1.SubnetSpec{
				VLANConnectionName: "gatewayconnection-103",
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

func (f *fakeStatusWriter) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...client.SubResourceApplyOption) error {
	return nil
}

func TestPollAllSharedSubnets(t *testing.T) {
	tests := []struct {
		name                    string
		sharedSubnetsMap        map[string][]types.NamespacedName
		expectedUniqueResources int // Number of unique associated resources
		expectedEnqueueCalls    int // Number of times enqueueSubnetForReconciliation should be called
		initialCache            map[string]struct {
			Subnet     *model.VpcSubnet
			StatusList []model.VpcSubnetStatus
		}
	}{
		{
			name:                    "Empty shared subnets map",
			sharedSubnetsMap:        map[string][]types.NamespacedName{},
			expectedUniqueResources: 0,
			expectedEnqueueCalls:    0,
			initialCache: map[string]struct {
				Subnet     *model.VpcSubnet
				StatusList []model.VpcSubnetStatus
			}{
				"project1:vpc1:subnet1": {Subnet: &model.VpcSubnet{}},
			},
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
			r.SubnetService.NSXSubnetCache = tt.initialCache

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

			var subnetportService *subnetport.SubnetPortService
			patches.ApplyMethod(reflect.TypeOf(subnetportService), "DeletePortCount", func(_ common.SubnetPortServiceProvider, subnetPath string) {})

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
	r.updateSharedSubnetWithError(context.Background(), types.NamespacedName{Namespace: "default", Name: "test-subnet"}, "", err, "test error type")

	// Test case 2: Subnet exists but is being deleted
	// The function should get the subnet and return early because the subnet is being deleted
	r.updateSharedSubnetWithError(context.Background(), types.NamespacedName{Namespace: "default", Name: "test-subnet-deleting"}, "", err, "test error type")

	// Test case 3: Subnet does not exist
	// The function should try to get the subnet, fail, and return early
	r.updateSharedSubnetWithError(context.Background(), types.NamespacedName{Namespace: "default", Name: "non-existent-subnet"}, "", err, "test error type")
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
				func(_ *SubnetReconciler, _ context.Context, namespacedName types.NamespacedName, _ string, _ error, _ string) {
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

func TestSyncPortConfigsForNamespaceSubnet(t *testing.T) {
	ctx := context.Background()
	ps1ID := "port-config-1"
	ps2ID := "port-config-2"
	psOldID := "port-config-orphan"
	assocRes := "proj-1:vpc-1:subnet-1"

	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared-subnet-1",
			Namespace: "ns-test",
			Annotations: map[string]string{
				common.AnnotationAssociatedResource: assocRes,
			},
		},
	}

	orphanCR := &v1alpha1.SubnetPortSetting{
		ObjectMeta: metav1.ObjectMeta{
			Name:      psOldID,
			Namespace: "ns-test",
			Annotations: map[string]string{
				common.AnnotationAssociatedResource: fmt.Sprintf("%s:%s", assocRes, psOldID),
			},
		},
		Spec: v1alpha1.SubnetPortSettingSpec{
			SubnetName: "shared-subnet-1",
		},
		Status: v1alpha1.SubnetPortSettingStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	existingCR := &v1alpha1.SubnetPortSetting{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ps1ID,
			Namespace: "ns-test",
			Annotations: map[string]string{
				common.AnnotationAssociatedResource: fmt.Sprintf("%s:%s", assocRes, ps1ID),
			},
		},
		Spec: v1alpha1.SubnetPortSettingSpec{
			SubnetName: "shared-subnet-1",
		},
	}

	r := createFakeSubnetReconciler([]client.Object{subnetCR, orphanCR, existingCR})

	portSettings := []model.VpcSubnetPortSetting{
		{Id: &ps1ID},
		{Id: &ps2ID},
	}
	nsxIDs := sets.New[string](ps1ID, ps2ID)

	err := r.syncPortConfigsForNamespaceSubnet(ctx, subnetCR, portSettings, nsxIDs, assocRes)
	assert.NoError(t, err)

	// Verify ps-1 exists and condition updated to Ready
	ps1CR := &v1alpha1.SubnetPortSetting{}
	err = r.Client.Get(ctx, types.NamespacedName{Namespace: "ns-test", Name: ps1ID}, ps1CR)
	assert.NoError(t, err)
	assert.Equal(t, "shared-subnet-1", ps1CR.Spec.SubnetName)
	assert.NotEmpty(t, ps1CR.Status.Conditions)
	assert.Equal(t, v1alpha1.Ready, ps1CR.Status.Conditions[0].Type)
	assert.Equal(t, corev1.ConditionTrue, ps1CR.Status.Conditions[0].Status)

	// Verify ps-2 is newly created with Ready condition and AnnotationAssociatedResource
	ps2CR := &v1alpha1.SubnetPortSetting{}
	err = r.Client.Get(ctx, types.NamespacedName{Namespace: "ns-test", Name: ps2ID}, ps2CR)
	assert.NoError(t, err)
	assert.Equal(t, "shared-subnet-1", ps2CR.Spec.SubnetName)
	assert.Equal(t, fmt.Sprintf("%s:%s", assocRes, ps2ID), ps2CR.Annotations[common.AnnotationAssociatedResource])
	assert.NotEmpty(t, ps2CR.Status.Conditions)
	assert.Equal(t, v1alpha1.Ready, ps2CR.Status.Conditions[0].Type)
	assert.Equal(t, corev1.ConditionTrue, ps2CR.Status.Conditions[0].Status)

	// Verify orphan CR (psOldID) is deleted
	orphanCheck := &v1alpha1.SubnetPortSetting{}
	err = r.Client.Get(ctx, types.NamespacedName{Namespace: "ns-test", Name: psOldID}, orphanCheck)
	assert.Error(t, err)
}

func TestSyncPortConfigs_NameConflict_FallbackGenerateName(t *testing.T) {
	ctx := context.Background()
	psID := "default"
	assocRes := "proj-1:vpc-1:subnet-2"

	// Subnet-2 being synced
	subnetCR2 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared-subnet-2",
			Namespace: "ns-test",
			Annotations: map[string]string{
				common.AnnotationAssociatedResource: assocRes,
			},
		},
	}

	// Another existing CR already occupies name "default" for subnet-1
	existingCRFromSubnet1 := &v1alpha1.SubnetPortSetting{
		ObjectMeta: metav1.ObjectMeta{
			Name:      psID,
			Namespace: "ns-test",
			Annotations: map[string]string{
				common.AnnotationAssociatedResource: "proj-1:vpc-1:subnet-1:default",
			},
		},
		Spec: v1alpha1.SubnetPortSettingSpec{
			SubnetName: "shared-subnet-1",
		},
	}

	r := createFakeSubnetReconciler([]client.Object{subnetCR2, existingCRFromSubnet1})

	portSettings := []model.VpcSubnetPortSetting{
		{Id: &psID},
	}
	nsxIDs := sets.New[string](psID)

	err := r.syncPortConfigsForNamespaceSubnet(ctx, subnetCR2, portSettings, nsxIDs, assocRes)
	assert.NoError(t, err)

	// Verify list of SubnetPortSettings in ns-test
	list := &v1alpha1.SubnetPortSettingList{}
	err = r.Client.List(ctx, list, client.InNamespace("ns-test"))
	assert.NoError(t, err)
	assert.Equal(t, 2, len(list.Items))

	var subnet2CR *v1alpha1.SubnetPortSetting
	for i := range list.Items {
		if list.Items[i].Spec.SubnetName == "shared-subnet-2" {
			subnet2CR = &list.Items[i]
			break
		}
	}
	assert.NotNil(t, subnet2CR)
	assert.True(t, strings.HasPrefix(subnet2CR.Name, "default-"))
	assert.Equal(t, fmt.Sprintf("%s:%s", assocRes, psID), subnet2CR.Annotations[common.AnnotationAssociatedResource])
}

func TestGenerateValidPortSettingName(t *testing.T) {
	// Valid DNS-1123 name
	validName := "valid-port-setting-1"
	assert.Equal(t, validName, generateValidPortSettingName(validName))

	// Invalid name with underscore or uppercase
	invalidName := "Invalid_Port_Setting_1"
	generated := generateValidPortSettingName(invalidName)
	assert.NotEqual(t, invalidName, generated)
	assert.True(t, strings.HasPrefix(generated, "port-setting-"))
}

func TestPollAndSyncPortConfigs(t *testing.T) {
	ctx := context.Background()
	psID := "port-config-test"
	assocRes := "proj-1:vpc-1:subnet-1"

	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "shared-subnet-1",
			Namespace:   "ns-test",
			Annotations: map[string]string{common.AnnotationAssociatedResource: assocRes},
		},
	}

	r := createFakeSubnetReconciler([]client.Object{subnetCR})

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListPortSettingsByAssociatedResource",
		func(_ *subnetservice.SubnetService, _ string) ([]model.VpcSubnetPortSetting, error) {
			return []model.VpcSubnetPortSetting{
				{Id: &psID},
			}, nil
		})
	defer patches.Reset()

	namespacedNames := sets.New[types.NamespacedName](
		types.NamespacedName{Namespace: "ns-test", Name: "shared-subnet-1"},
	)

	r.pollAndSyncPortConfigs(ctx, assocRes, namespacedNames)

	// Verify SubnetPortSetting was created
	createdCR := &v1alpha1.SubnetPortSetting{}
	err := r.Client.Get(ctx, types.NamespacedName{Namespace: "ns-test", Name: psID}, createdCR)
	assert.NoError(t, err)
	assert.Equal(t, "shared-subnet-1", createdCR.Spec.SubnetName)
	assert.NotEmpty(t, createdCR.Status.Conditions)
	assert.Equal(t, v1alpha1.Ready, createdCR.Status.Conditions[0].Type)
	assert.Equal(t, corev1.ConditionTrue, createdCR.Status.Conditions[0].Status)
}

// stalePortConfigClient simulates the informer not seeing recently created mirrors.
type stalePortConfigClient struct {
	client.Client
}

func (c *stalePortConfigClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if settings, ok := list.(*v1alpha1.SubnetPortSettingList); ok {
		settings.Items = nil
		return nil
	}
	return c.Client.List(ctx, list, opts...)
}

func TestSyncPortConfigs_StaleCache(t *testing.T) {
	ctx := context.Background()
	subnetCR := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns-test", UID: "subnet-uid",
			Annotations: map[string]string{common.AnnotationAssociatedResource: "proj:vpc:subnet"}},
	}
	foreign := &v1alpha1.SubnetPortSetting{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: subnetCR.Namespace,
			Annotations: map[string]string{common.AnnotationAssociatedResource: "proj:vpc:other:default"}},
		Spec: v1alpha1.SubnetPortSettingSpec{SubnetName: "other"},
	}
	r := createFakeSubnetReconciler([]client.Object{subnetCR, foreign})
	r.Client = &stalePortConfigClient{Client: r.Client}
	settings := []model.VpcSubnetPortSetting{{Id: common.String("default")}}
	for range 3 {
		require.NoError(t, r.syncPortConfigsForNamespaceSubnet(ctx, subnetCR, settings, sets.New("default"), "proj:vpc:subnet"))
	}
	list := &v1alpha1.SubnetPortSettingList{}
	require.NoError(t, r.APIReader.List(ctx, list, client.InNamespace(subnetCR.Namespace)))
	require.Len(t, list.Items, 2, "a stale informer must not create more generated-name mirrors")
	for _, setting := range list.Items {
		if setting.Spec.SubnetName == subnetCR.Name {
			require.Len(t, setting.OwnerReferences, 1)
			assert.Equal(t, subnetCR.UID, setting.OwnerReferences[0].UID)
			assert.Equal(t, "proj:vpc:subnet:default", setting.Annotations[common.AnnotationAssociatedResource])
			assert.Equal(t, corev1.ConditionTrue, setting.Status.Conditions[0].Status)
		}
	}
}

func TestCreatePortConfig_AlreadyExistsSameSource(t *testing.T) {
	ctx := context.Background()
	subnetCR := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns-test", UID: "subnet-uid"}}
	r := createFakeSubnetReconciler([]client.Object{subnetCR})
	first, err := r.createPortConfig(ctx, subnetCR, "default", "proj:vpc:subnet")
	require.NoError(t, err)
	second, err := r.createPortConfig(ctx, subnetCR, "default", "proj:vpc:subnet")
	require.NoError(t, err)
	assert.Equal(t, first.UID, second.UID)
	assert.Equal(t, first.Name, second.Name)
	list := &v1alpha1.SubnetPortSettingList{}
	require.NoError(t, r.APIReader.List(ctx, list))
	assert.Len(t, list.Items, 1)
}

func TestSyncPortConfigs_AllMirrorsAndSourceIdentity(t *testing.T) {
	ctx := context.Background()
	assoc := "proj:vpc:subnet"
	subnetCR := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns-test",
		Annotations: map[string]string{common.AnnotationAssociatedResource: assoc}}}
	var objects = []client.Object{subnetCR}
	for _, name := range []string{"first", "duplicate", "foreign"} {
		source := assoc + ":default"
		if name == "foreign" {
			source = "proj:vpc:other:default"
		}
		objects = append(objects, &v1alpha1.SubnetPortSetting{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: subnetCR.Namespace,
				Annotations: map[string]string{common.AnnotationAssociatedResource: source}},
			Spec: v1alpha1.SubnetPortSettingSpec{SubnetName: subnetCR.Name},
		})
	}
	r := createFakeSubnetReconciler(objects)
	settings := []model.VpcSubnetPortSetting{{Id: common.String("default")}}
	require.NoError(t, r.syncPortConfigsForNamespaceSubnet(ctx, subnetCR, settings, sets.New("default"), assoc))
	for _, name := range []string{"first", "duplicate"} {
		cr := &v1alpha1.SubnetPortSetting{}
		require.NoError(t, r.Client.Get(ctx, client.ObjectKey{Namespace: subnetCR.Namespace, Name: name}, cr))
		require.Len(t, cr.Status.Conditions, 1)
		assert.Equal(t, corev1.ConditionTrue, cr.Status.Conditions[0].Status)
	}
	require.NoError(t, r.syncPortConfigsForNamespaceSubnet(ctx, subnetCR, nil, sets.New[string](), assoc))
	list := &v1alpha1.SubnetPortSettingList{}
	require.NoError(t, r.APIReader.List(ctx, list))
	require.Len(t, list.Items, 1, "all orphan mirrors must be removed without touching a foreign source")
	assert.Equal(t, "foreign", list.Items[0].Name)
	assert.Empty(t, list.Items[0].Status.Conditions)
}

func TestSharedSubnetFailureInvalidatesPortConfigs(t *testing.T) {
	for _, poll := range []bool{true, false} {
		for _, sourceErr := range []error{errors.New("NSX returned 404 NOT_FOUND"), context.DeadlineExceeded} {
			t.Run(fmt.Sprintf("poll=%t/error=%s", poll, sourceErr), func(t *testing.T) {
				ctx := context.Background()
				assoc := "proj:vpc:subnet"
				subnetCR := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns-test",
					Annotations: map[string]string{common.AnnotationAssociatedResource: assoc}}}
				setting := &v1alpha1.SubnetPortSetting{
					ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: subnetCR.Namespace,
						Annotations: map[string]string{common.AnnotationAssociatedResource: assoc + ":default"}},
					Spec: v1alpha1.SubnetPortSettingSpec{SubnetName: subnetCR.Name},
					Status: v1alpha1.SubnetPortSettingStatus{Conditions: []v1alpha1.Condition{
						{Type: v1alpha1.Ready, Status: corev1.ConditionTrue},
						{Type: v1alpha1.UpdateFailure, Status: corev1.ConditionFalse, Reason: "KeepOtherConditions"},
					}},
				}
				r := createFakeSubnetReconciler([]client.Object{subnetCR, setting})
				r.SubnetPortService.(*subnetport.SubnetPortService).SubnetPortStore = &subnetport.SubnetPortStore{}
				key := client.ObjectKeyFromObject(subnetCR)
				r.SubnetService.AddSharedSubnetToResourceMap(assoc, key)
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetNSXSubnetByAssociatedResource",
					func(_ *subnetservice.SubnetService, _ string) (*model.VpcSubnet, error) { return nil, sourceErr })
				defer patches.Reset()
				if poll {
					r.pollAllSharedSubnets()
				} else {
					for range 2 {
						_, err := r.handleSharedSubnet(ctx, subnetCR, key, assoc)
						require.ErrorIs(t, err, sourceErr, "failed synchronization must release the source for retry")
					}
				}
				retained := &v1alpha1.SubnetPortSetting{}
				require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(setting), retained))
				require.Len(t, retained.Status.Conditions, 2)
				assert.Equal(t, corev1.ConditionFalse, retained.Status.Conditions[0].Status)
				assert.Contains(t, retained.Status.Conditions[0].Message, sourceErr.Error())
				assert.Equal(t, setting.Status.Conditions[1], retained.Status.Conditions[1])
				settings := []model.VpcSubnetPortSetting{{Id: common.String("default")}}
				require.NoError(t, r.syncPortConfigsForNamespaceSubnet(ctx, subnetCR, settings, sets.New("default"), assoc))
				require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(setting), retained))
				assert.Equal(t, corev1.ConditionTrue, retained.Status.Conditions[0].Status)
				assert.Equal(t, "PortSettingRealized", retained.Status.Conditions[0].Reason)
				require.NoError(t, r.Client.Get(ctx, key, &v1alpha1.Subnet{}), "the parent CR must also be retained")
			})
		}
	}
}

func TestPollingAndOnDemandSerializePortConfigs(t *testing.T) {
	for _, pollFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("poll-first=%t", pollFirst), func(t *testing.T) {
			ctx := context.Background()
			assoc := "proj:vpc:subnet"
			subnetCR := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns-test", UID: "subnet-uid",
				Annotations: map[string]string{common.AnnotationAssociatedResource: assoc}}}
			otherSubnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "ns-other", UID: "other-uid",
				Annotations: map[string]string{common.AnnotationAssociatedResource: "proj:vpc:other"}}}
			r := createFakeSubnetReconciler([]client.Object{subnetCR, otherSubnet})
			key := client.ObjectKeyFromObject(subnetCR)
			r.SubnetService.AddSharedSubnetToResourceMap(assoc, key)
			r.getQueue("subnet", nil)
			var parentFetches, statusFetches atomic.Int32
			patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetNSXSubnetByAssociatedResource",
				func(_ *subnetservice.SubnetService, source string) (*model.VpcSubnet, error) {
					if source == assoc {
						parentFetches.Add(1)
					}
					id := strings.Split(source, ":")[2]
					return &model.VpcSubnet{Id: common.String(id), Path: common.String("/orgs/default/projects/proj/vpcs/vpc/subnets/" + id)}, nil
				})
			defer patches.Reset()
			patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "GetSubnetStatus",
				func(_ *subnetservice.SubnetService, nsxSubnet *model.VpcSubnet) ([]model.VpcSubnetStatus, error) {
					if *nsxSubnet.Id == "subnet" {
						statusFetches.Add(1)
					}
					return []model.VpcSubnetStatus{{NetworkAddress: common.String("10.0.0.0/24")}}, nil
				})
			patches.ApplyPrivateMethod(reflect.TypeOf(r), "updateSubnetIfNeeded",
				func(_ *SubnetReconciler, _ context.Context, _ *v1alpha1.Subnet, _ *model.VpcSubnet, _ []model.VpcSubnetStatus, _ types.NamespacedName) error {
					return nil
				})
			firstSnapshot := make(chan struct{})
			releaseSnapshot := make(chan struct{})
			release := sync.OnceFunc(func() { close(releaseSnapshot) })
			var calls atomic.Int32
			patches.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListPortSettingsByAssociatedResource",
				func(_ *subnetservice.SubnetService, source string) ([]model.VpcSubnetPortSetting, error) {
					if source == assoc && calls.Add(1) == 1 {
						close(firstSnapshot)
						<-releaseSnapshot
					}
					return []model.VpcSubnetPortSetting{{Id: common.String("default")}}, nil
				})
			ownerDone := make(chan error, 1)
			go func() {
				defer close(ownerDone)
				if pollFirst {
					r.pollAllSharedSubnets()
					ownerDone <- nil
				} else {
					_, err := r.handleSharedSubnet(ctx, subnetCR, key, assoc)
					ownerDone <- err
				}
			}()
			defer func() {
				release()
				select {
				case <-ownerDone:
				case <-time.After(5 * time.Second):
					t.Error("initial source synchronization did not finish")
				}
			}()
			select {
			case <-firstSnapshot:
			case <-time.After(5 * time.Second):
				t.Fatal("initial source synchronization did not start")
			}

			r.SubnetService.AddSharedSubnetToResourceMap("proj:vpc:other", client.ObjectKeyFromObject(otherSubnet))

			// A busy source requeues promptly, while a different source can finish its NSX I/O.
			workersDone := make(chan error, 1)
			go func() {
				result, err := r.handleSharedSubnet(ctx, subnetCR.DeepCopy(), key, assoc)
				if err == nil && result != ResultRequeueAfter10sec {
					err = fmt.Errorf("busy source returned %v instead of a delayed requeue", result)
				}
				if err == nil {
					_, err = r.handleSharedSubnet(ctx, otherSubnet, client.ObjectKeyFromObject(otherSubnet), "proj:vpc:other")
				}
				workersDone <- err
			}()
			select {
			case err := <-workersDone:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("reconciliation workers blocked behind an unrelated source")
			}
			assert.EqualValues(t, 1, calls.Load())

			pollDone := make(chan struct{})
			if !pollFirst {
				go func() {
					defer close(pollDone)
					r.pollAllSharedSubnets()
				}()
			}
			release()
			require.NoError(t, <-ownerDone)
			if pollFirst {
				_, err := r.handleSharedSubnet(ctx, subnetCR, key, assoc)
				require.NoError(t, err)
			} else {
				select {
				case <-pollDone:
				case <-time.After(5 * time.Second):
					t.Fatal("polling did not refresh the busy source")
				}
				assert.EqualValues(t, 2, parentFetches.Load(), "polling must refresh the parent after on-demand synchronization")
				assert.EqualValues(t, 2, statusFetches.Load(), "polling must refresh parent status too")
			}
			assert.EqualValues(t, 2, calls.Load())
			list := &v1alpha1.SubnetPortSettingList{}
			require.NoError(t, r.APIReader.List(ctx, list, client.InNamespace(subnetCR.Namespace)))
			require.Len(t, list.Items, 1)
			assert.Equal(t, corev1.ConditionTrue, list.Items[0].Status.Conditions[0].Status)
		})
	}
}

func TestSyncPortConfigsPreservesUnrelatedCRs(t *testing.T) {
	assoc := "proj:vpc:subnet"
	for _, annotation := range []map[string]string{
		nil,
		{common.AnnotationAssociatedResource: ""},
		{common.AnnotationAssociatedResource: "proj:vpc:other:default"},
		{common.AnnotationAssociatedResource: assoc + ":"},
	} {
		t.Run(fmt.Sprint(annotation), func(t *testing.T) {
			ctx := context.Background()
			subnetCR := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns-test",
				Annotations: map[string]string{common.AnnotationAssociatedResource: assoc}}}
			unrelated := &v1alpha1.SubnetPortSetting{
				ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: subnetCR.Namespace, Annotations: annotation},
				Spec:       v1alpha1.SubnetPortSettingSpec{SubnetName: subnetCR.Name},
				Status: v1alpha1.SubnetPortSettingStatus{Conditions: []v1alpha1.Condition{
					{Type: v1alpha1.Ready, Status: corev1.ConditionTrue, Reason: "UserManaged"},
				}},
			}
			r := createFakeSubnetReconciler([]client.Object{subnetCR, unrelated})
			key := client.ObjectKeyFromObject(unrelated)
			before := &v1alpha1.SubnetPortSetting{}
			require.NoError(t, r.Client.Get(ctx, key, before))
			settings := []model.VpcSubnetPortSetting{{Id: common.String("default")}}
			require.NoError(t, r.syncPortConfigsForNamespaceSubnet(ctx, subnetCR, settings, sets.New("default"), assoc))
			list := &v1alpha1.SubnetPortSettingList{}
			require.NoError(t, r.APIReader.List(ctx, list))
			require.Len(t, list.Items, 2, "an unrelated name conflict must get a separate mirror")
			for _, item := range list.Items {
				if item.Name != unrelated.Name {
					assert.Equal(t, assoc+":default", item.Annotations[common.AnnotationAssociatedResource])
					require.Len(t, item.Status.Conditions, 1)
					assert.Equal(t, corev1.ConditionTrue, item.Status.Conditions[0].Status)
				}
			}
			require.NoError(t, r.markPortConfigsNotReady(ctx, subnetCR, context.DeadlineExceeded))
			require.NoError(t, r.syncPortConfigsForNamespaceSubnet(ctx, subnetCR, nil, sets.New[string](), assoc))
			require.NoError(t, r.APIReader.List(ctx, list))
			require.Len(t, list.Items, 1, "orphan cleanup must retain the unrelated CR")
			after := &v1alpha1.SubnetPortSetting{}
			require.NoError(t, r.Client.Get(ctx, key, after))
			assert.Equal(t, before, after, "synchronization, parent failure and cleanup must leave unrelated CRs unchanged")
		})
	}
}

func TestSharedSubnetPollIgnoresChangedSource(t *testing.T) {
	ctx := context.Background()
	oldSource, newSource := "proj:vpc:old", "proj:vpc:new"
	subnetCR := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns-test",
		Annotations: map[string]string{common.AnnotationAssociatedResource: newSource}},
		Status: v1alpha1.SubnetStatus{NetworkAddresses: []string{"10.0.0.0/24"}}}
	var objects = []client.Object{subnetCR}
	for _, source := range []string{oldSource, newSource} {
		objects = append(objects, &v1alpha1.SubnetPortSetting{
			ObjectMeta: metav1.ObjectMeta{Name: strings.Split(source, ":")[2], Namespace: subnetCR.Namespace,
				Annotations: map[string]string{common.AnnotationAssociatedResource: source + ":default"}},
			Spec:   v1alpha1.SubnetPortSettingSpec{SubnetName: subnetCR.Name},
			Status: v1alpha1.SubnetPortSettingStatus{Conditions: []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: corev1.ConditionTrue}}},
		})
	}
	r := createFakeSubnetReconciler(objects)
	before := &v1alpha1.SubnetPortSettingList{}
	require.NoError(t, r.APIReader.List(ctx, before))
	parentBefore := &v1alpha1.Subnet{}
	key := client.ObjectKeyFromObject(subnetCR)
	require.NoError(t, r.APIReader.Get(ctx, key, parentBefore))
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.SubnetService), "ListPortSettingsByAssociatedResource",
		func(_ *subnetservice.SubnetService, _ string) ([]model.VpcSubnetPortSetting, error) { return nil, nil })
	defer patches.Reset()
	r.pollAndSyncPortConfigs(ctx, oldSource, sets.New(key))
	r.updateSharedSubnetWithError(ctx, key, oldSource, context.DeadlineExceeded, "NSX subnet")
	after := &v1alpha1.SubnetPortSettingList{}
	require.NoError(t, r.APIReader.List(ctx, after))
	assert.Equal(t, before, after)
	parentAfter := &v1alpha1.Subnet{}
	require.NoError(t, r.APIReader.Get(ctx, key, parentAfter))
	assert.Equal(t, parentBefore, parentAfter)
}
