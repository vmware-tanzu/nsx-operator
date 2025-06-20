package subnet

import (
	"context"
	"fmt"
	"reflect"
	"testing"

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

			// Create status list for testing
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

func (f *fakeStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}

func (f *fakeStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return f.updateErr
}

func (f *fakeStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
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
