/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"fmt"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apitypes "k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSetVPCNetworkConfigurationStatusWithGatewayConnection(t *testing.T) {
	tests := []struct {
		name                    string
		prepareFunc             func(*testing.T, context.Context, client.Client)
		gatewayConnectionReady  bool
		status                  *common.VPCConnectionStatus
		expectedConditionType   v1alpha1.ConditionType
		expectedConditionStatus corev1.ConditionStatus
		expectedConditionReason string
	}{
		{
			name: "GatewayConnectionReady",
			prepareFunc: func(_ *testing.T, ctx context.Context, client client.Client) {
				assert.NoError(t, client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ncName",
					},
				}))
			},
			status: &common.VPCConnectionStatus{
				GatewayConnectionReady: true,
				ServiceClusterReady:    false,
				ServiceClusterReason:   common.ReasonServiceClusterNotSet,
			},
		},
		{
			name: "ServiceClusterReady",
			prepareFunc: func(_ *testing.T, ctx context.Context, client client.Client) {
				assert.NoError(t, client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ncName",
					},
				}))
			},
			gatewayConnectionReady: false,
			status: &common.VPCConnectionStatus{
				GatewayConnectionReady:  false,
				GatewayConnectionReason: common.ReasonGatewayConnectionNotSet,
				ServiceClusterReady:     true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			scheme := clientgoscheme.Scheme
			v1alpha1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.VPCNetworkConfiguration{}).Build()
			actualCR := &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ncName",
				},
			}
			if tt.prepareFunc != nil {
				tt.prepareFunc(t, ctx, client)
			}
			setVPCNetworkConfigurationStatusWithGatewayConnection(ctx, client, actualCR, tt.status)
			assert.Equal(t, v1alpha1.GatewayConnectionReady, actualCR.Status.Conditions[0].Type)
			assert.Equal(t, tt.status.GatewayConnectionReason, actualCR.Status.Conditions[0].Reason)
			if tt.status.GatewayConnectionReady {
				assert.Equal(t, corev1.ConditionTrue, actualCR.Status.Conditions[0].Status)
			} else {
				assert.Equal(t, corev1.ConditionFalse, actualCR.Status.Conditions[0].Status)
			}
			assert.Equal(t, v1alpha1.ServiceClusterReady, actualCR.Status.Conditions[1].Type)
			assert.Equal(t, tt.status.ServiceClusterReason, actualCR.Status.Conditions[1].Reason)
			if tt.status.ServiceClusterReady {
				assert.Equal(t, corev1.ConditionTrue, actualCR.Status.Conditions[1].Status)
			} else {
				assert.Equal(t, corev1.ConditionFalse, actualCR.Status.Conditions[1].Status)
			}
		})
	}
}

func TestSetVPCNetworkConfigurationStatusWithSnatEnabled(t *testing.T) {
	tests := []struct {
		name                    string
		prepareFunc             func(*testing.T, context.Context, client.Client, string, bool, *v1alpha1.VPCNetworkConfiguration) *gomonkey.Patches
		autoSnatEnabled         bool
		networkStack            v1alpha1.NetworkStackType
		expectedConditionType   v1alpha1.ConditionType
		expectedConditionStatus corev1.ConditionStatus
		expectedConditionReason string
	}{
		{
			name: "AutoSnatEnabled",
			prepareFunc: func(_ *testing.T, ctx context.Context, client client.Client, _ string, _ bool, nc *v1alpha1.VPCNetworkConfiguration) (patches *gomonkey.Patches) {
				assert.NoError(t, client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ncName",
					},
				}))
				patches = &gomonkey.Patches{}
				return patches
			},
			autoSnatEnabled:         true,
			expectedConditionType:   v1alpha1.AutoSnatEnabled,
			expectedConditionStatus: corev1.ConditionTrue,
			expectedConditionReason: "",
			networkStack:            v1alpha1.FullStackVPC,
		},
		{
			name: "AutoSnatDisabled for TEP mode",
			prepareFunc: func(_ *testing.T, ctx context.Context, client client.Client, _ string, _ bool, nc *v1alpha1.VPCNetworkConfiguration) (patches *gomonkey.Patches) {
				assert.NoError(t, client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ncName",
					},
				}))
				patches = &gomonkey.Patches{}
				return patches
			},
			autoSnatEnabled:         false,
			expectedConditionType:   v1alpha1.AutoSnatEnabled,
			expectedConditionStatus: corev1.ConditionFalse,
			networkStack:            v1alpha1.FullStackVPC,
		},
		{
			name: "AutoSnatDisabled for TEPLess mode",
			prepareFunc: func(_ *testing.T, ctx context.Context, client client.Client, _ string, _ bool, nc *v1alpha1.VPCNetworkConfiguration) (patches *gomonkey.Patches) {
				assert.NoError(t, client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ncName",
					},
				}))
				patches = &gomonkey.Patches{}
				return patches
			},
			autoSnatEnabled:         false,
			expectedConditionType:   v1alpha1.AutoSnatEnabled,
			expectedConditionStatus: corev1.ConditionFalse,
			networkStack:            v1alpha1.VLANBackedVPC,
			expectedConditionReason: common.ReasonSNATNotSupportedInTEPLess,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			scheme := clientgoscheme.Scheme
			v1alpha1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.VPCNetworkConfiguration{}).Build()
			actualCR := &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ncName",
				},
			}
			if tt.prepareFunc != nil {
				patches := tt.prepareFunc(t, ctx, client, "ncName", tt.autoSnatEnabled, actualCR)
				defer patches.Reset()
			}
			setVPCNetworkConfigurationStatusWithSnatEnabled(ctx, client, actualCR, tt.autoSnatEnabled, tt.networkStack)
			assert.Equal(t, tt.expectedConditionType, actualCR.Status.Conditions[0].Type)
			assert.Equal(t, tt.expectedConditionStatus, actualCR.Status.Conditions[0].Status)
			assert.Equal(t, tt.expectedConditionReason, actualCR.Status.Conditions[0].Reason)
		})
	}
}

func TestSetVPCNetworkConfigurationStatusWithLBCapability(t *testing.T) {
	tests := []struct {
		name                    string
		lbCapable               bool
		expectedConditionStatus corev1.ConditionStatus
		expectedConditionReason string
	}{
		{
			name:                    "VNA + NSXLB + IPv6 — LBCapability False",
			lbCapable:               false,
			expectedConditionStatus: corev1.ConditionFalse,
			expectedConditionReason: common.ReasonIPv6LBNotSupportedOnVNA,
		},
		{
			name:                    "VNA + NSXLB + DualStack — LBCapability False",
			lbCapable:               false,
			expectedConditionStatus: corev1.ConditionFalse,
			expectedConditionReason: common.ReasonIPv6LBNotSupportedOnVNA,
		},
		{
			name:                    "VNA + NSXLB + IPv4 — LBCapability True",
			lbCapable:               true,
			expectedConditionStatus: corev1.ConditionTrue,
			expectedConditionReason: "",
		},
		{
			name:                    "VNA + AVI LB + IPv6 — LBCapability True",
			lbCapable:               true,
			expectedConditionStatus: corev1.ConditionTrue,
			expectedConditionReason: "",
		},
		{
			name:                    "FullStack + NSXLB + IPv6 — LBCapability True",
			lbCapable:               true,
			expectedConditionStatus: corev1.ConditionTrue,
			expectedConditionReason: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			scheme := clientgoscheme.Scheme
			v1alpha1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.VPCNetworkConfiguration{}).Build()
			nc := &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "ncName"},
			}
			assert.NoError(t, fakeClient.Create(ctx, nc))

			setVPCNetworkConfigurationStatusWithLBCapability(ctx, fakeClient, nc, tt.lbCapable)

			assert.Equal(t, 1, len(nc.Status.Conditions))
			assert.Equal(t, v1alpha1.LBCapability, nc.Status.Conditions[0].Type)
			assert.Equal(t, tt.expectedConditionStatus, nc.Status.Conditions[0].Status)
			assert.Equal(t, tt.expectedConditionReason, nc.Status.Conditions[0].Reason)
		})
	}
}

func TestGetConnectionStatusFromCR(t *testing.T) {
	tests := []struct {
		name                            string
		prepareFunc                     func(*testing.T, context.Context, client.Client) *gomonkey.Patches
		conditions                      []v1alpha1.Condition
		expectedGatewayConnectionStatus bool
		expectedGatewayConnectionReason string
		expectedServiceClusterStatus    bool
		expectedServiceClusterReason    string
	}{
		{
			name:                            "EmptyCondition",
			prepareFunc:                     nil,
			conditions:                      []v1alpha1.Condition{},
			expectedGatewayConnectionStatus: false,
			expectedGatewayConnectionReason: "",
			expectedServiceClusterStatus:    false,
			expectedServiceClusterReason:    "",
		},
		{
			name:        "GetServiceClusterStatusReady",
			prepareFunc: nil,
			conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.AutoSnatEnabled,
					Status: corev1.ConditionFalse,
				},
				{
					Type:   v1alpha1.GatewayConnectionReady,
					Status: corev1.ConditionFalse,
					Reason: common.ReasonGatewayConnectionNotSet,
				},
				{
					Type:   v1alpha1.ServiceClusterReady,
					Status: corev1.ConditionTrue,
				},
			},
			expectedGatewayConnectionStatus: false,
			expectedGatewayConnectionReason: common.ReasonGatewayConnectionNotSet,
			expectedServiceClusterStatus:    true,
			expectedServiceClusterReason:    "",
		},
		{
			name:        "GetGatewayConnectionReady",
			prepareFunc: nil,
			conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.AutoSnatEnabled,
					Status: corev1.ConditionFalse,
				},
				{
					Type:   v1alpha1.GatewayConnectionReady,
					Status: corev1.ConditionTrue,
				},
				{
					Type:   v1alpha1.ServiceClusterReady,
					Status: corev1.ConditionFalse,
					Reason: common.ReasonServiceClusterNotSet,
				},
			},
			expectedGatewayConnectionStatus: true,
			expectedGatewayConnectionReason: "",
			expectedServiceClusterStatus:    false,
			expectedServiceClusterReason:    common.ReasonServiceClusterNotSet,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vpcNetworkConfiguration := v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ncName",
				},
				Status: v1alpha1.VPCNetworkConfigurationStatus{
					Conditions: tt.conditions,
				},
			}
			gatewayConnectionReady, gatewayConnectionReason := getGatewayConnectionStatus(&vpcNetworkConfiguration)
			serviceClusterReady, serviceClusterReason := getServiceClusterStatus(&vpcNetworkConfiguration)
			assert.Equal(t, tt.expectedGatewayConnectionStatus, gatewayConnectionReady)
			assert.Equal(t, tt.expectedGatewayConnectionReason, gatewayConnectionReason)
			assert.Equal(t, tt.expectedServiceClusterStatus, serviceClusterReady)
			assert.Equal(t, tt.expectedServiceClusterReason, serviceClusterReason)
		})
	}
}

func TestSetNSNetworkReadyCondition(t *testing.T) {
	nsName := "test-ns"
	msg := nsMsgVPCCreateUpdateError
	msgErr := fmt.Errorf("failed to connect to NSX")
	nsNotReadyCondition := corev1.NamespaceCondition{
		Type:    NamespaceNetworkReady,
		Status:  corev1.ConditionFalse,
		Reason:  NSReasonVPCNotReady,
		Message: "Error happened to create or update VPC: failed to connect to NSX",
	}

	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		testFn  func(k8sclient *mock_client.MockClient)
		addCond bool
	}{
		{
			name: "Failed to get K8s Namespace",
			testFn: func(k8sClient *mock_client.MockClient) {
				k8sClient.EXPECT().Get(ctx, apitypes.NamespacedName{Name: nsName}, gomock.Any()).Return(fmt.Errorf("failed"))
			},
			addCond: false,
		}, {
			name: "Add failed condition on K8s Namespace",
			testFn: func(k8sClient *mock_client.MockClient) {
				k8sClient.EXPECT().Get(ctx, apitypes.NamespacedName{Name: nsName}, gomock.Any()).Return(nil).Do(
					func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...*client.GetOption) error {
						ns := obj.(*corev1.Namespace)
						ns.ObjectMeta = metav1.ObjectMeta{Name: nsName}
						return nil
					})
			},
			addCond: true,
		}, {
			name: "Update condition on K8s Namespace",
			testFn: func(k8sClient *mock_client.MockClient) {
				k8sClient.EXPECT().Get(ctx, apitypes.NamespacedName{Name: nsName}, gomock.Any()).Return(nil).Do(
					func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...*client.GetOption) error {
						ns := obj.(*corev1.Namespace)
						ns.ObjectMeta = metav1.ObjectMeta{Name: nsName}
						ns.Status = corev1.NamespaceStatus{
							Conditions: []corev1.NamespaceCondition{
								{
									Type:    NamespaceNetworkReady,
									Status:  corev1.ConditionFalse,
									Reason:  NSReasonVPCNetConfigNotReady,
									Message: "Error happened to get system VPC network configuration: failed to connect to NSX",
								},
							},
						}
						return nil
					})
			},
			addCond: true,
		}, {
			name: "Not update condition on K8s Namespace if it already exists",
			testFn: func(k8sClient *mock_client.MockClient) {
				k8sClient.EXPECT().Get(ctx, apitypes.NamespacedName{Name: nsName}, gomock.Any()).Return(nil).Do(
					func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...*client.GetOption) error {
						ns := obj.(*corev1.Namespace)
						ns.ObjectMeta = metav1.ObjectMeta{Name: nsName}
						ns.Status = corev1.NamespaceStatus{
							Conditions: []corev1.NamespaceCondition{
								nsNotReadyCondition,
							},
						}
						return nil
					})
			},
			addCond: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			k8sClient := mock_client.NewMockClient(ctrl)
			if tc.testFn != nil {
				tc.testFn(k8sClient)
			}
			if tc.addCond {
				statusClient := fakeStatusWriter{t: t, conditionMatcher: conditionMatcher(nsNotReadyCondition)}
				k8sClient.EXPECT().Status().Return(statusClient).Times(1)
			}
			setNSNetworkReadyCondition(ctx, k8sClient, nsName, msg.getNSNetworkCondition(msgErr))
		})
	}
}

type fakeStatusWriter struct {
	t                *testing.T
	conditionMatcher gomock.Matcher
}

func (writer fakeStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}
func (writer fakeStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	require.True(writer.t, writer.conditionMatcher.Matches(obj))
	return nil
}
func (writer fakeStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return nil
}

func (writer fakeStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return nil
}

type hasCondition struct {
	condition corev1.NamespaceCondition
}

func (m hasCondition) Matches(arg interface{}) bool {
	ns := arg.(*corev1.Namespace)
	for _, extCond := range ns.Status.Conditions {
		if nsConditionEquals(extCond, m.condition) {
			return true
		}
	}
	return false
}

func (m hasCondition) String() string {
	return fmt.Sprintf("Condition: type=%s, status=%v, reason=%s, message=%s",
		m.condition.Type, m.condition.Status, m.condition.Reason, m.condition.Message)
}

func conditionMatcher(cond corev1.NamespaceCondition) gomock.Matcher {
	return &hasCondition{condition: cond}
}

func TestGetNSNetworkCondition(t *testing.T) {
	networkReadyCondition := corev1.NamespaceCondition{
		Type:   NamespaceNetworkReady,
		Status: corev1.ConditionTrue,
	}
	require.True(t, nsConditionEquals(networkReadyCondition, *nsMsgVPCIsReady.getNSNetworkCondition()))

	msgErr := fmt.Errorf("failed to connect to NSX")
	vpcNotReadyCondition := corev1.NamespaceCondition{
		Type:    NamespaceNetworkReady,
		Status:  corev1.ConditionFalse,
		Reason:  NSReasonVPCNotReady,
		Message: "Error happened to create or update VPC: failed to connect to NSX",
	}
	require.True(t, nsConditionEquals(vpcNotReadyCondition, *nsMsgVPCCreateUpdateError.getNSNetworkCondition(msgErr)))
}

// TestSetNetworkInfoVPCStatusIdempotency verifies that reordered LoadBalancerBackendIPs do
// not trigger a spurious CR update because setNetworkInfoVPCStatus sorts both slices before
// reflect.DeepEqual.
func TestSetNetworkInfoVPCStatusIdempotency(t *testing.T) {
	ctx := context.TODO()
	scheme := clientgoscheme.Scheme
	v1alpha1.AddToScheme(scheme)

	// Build a NetworkInfo CR that already has a VPC state with LoadBalancerBackendIPs.
	existingNetworkInfo := &v1alpha1.NetworkInfo{
		ObjectMeta: metav1.ObjectMeta{Name: "ni", Namespace: "default"},
		VPCs: []v1alpha1.VPCState{
			{
				Name:                    "vpc1",
				LoadBalancerIPAddresses: "100.64.0.1",
				LoadBalancerBackendIPs:  []string{"100.64.0.1", "2001:db8::1"},
			},
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingNetworkInfo).Build()

	// The reconcile produces the same state but with IPs in reverse order.
	reorderedVPC := &v1alpha1.VPCState{
		Name:                    "vpc1",
		LoadBalancerIPAddresses: "100.64.0.1",
		LoadBalancerBackendIPs:  []string{"2001:db8::1", "100.64.0.1"},
	}

	// setNetworkInfoVPCStatus must NOT call fakeClient.Update because the states are equal
	// after sorting. We capture any update by reading the CR back; ResourceVersion should
	// be unchanged (fake client does not increment it on no-op, but the Update itself should
	// not be called when the sorted slices are equal).
	originalRV := existingNetworkInfo.ResourceVersion

	setNetworkInfoVPCStatus(fakeClient, ctx, existingNetworkInfo, metav1.Time{}, reorderedVPC)

	// Reload the CR and confirm no actual write occurred.
	refreshed := &v1alpha1.NetworkInfo{}
	require.NoError(t, fakeClient.Get(ctx, apitypes.NamespacedName{Name: "ni", Namespace: "default"}, refreshed))
	assert.Equal(t, originalRV, refreshed.ResourceVersion, "CR should not be updated when only IP order differs")
}

func TestHasPodOrVMDefaultSubnets(t *testing.T) {
	subnets := []v1alpha1.SharedSubnet{
		{
			Path:      "subnet-path",
			VMDefault: true,
		},
	}
	assert.False(t, hasPodDefaultSubnets(subnets))
	assert.True(t, hasVMDefaultSubnets(subnets))

	subnets = []v1alpha1.SharedSubnet{
		{
			Path:       "subnet-path",
			PodDefault: true,
		},
	}
	assert.True(t, hasPodDefaultSubnets(subnets))
	assert.False(t, hasVMDefaultSubnets(subnets))

	subnets = []v1alpha1.SharedSubnet{
		{
			Path: "subnet-path",
		},
	}
	assert.False(t, hasPodDefaultSubnets(subnets))
	assert.False(t, hasVMDefaultSubnets(subnets))
}

func TestSetNetworkInfoVPCStatus_AllowedDNSDomains(t *testing.T) {
	ctx := context.TODO()
	scheme := clientgoscheme.Scheme
	v1alpha1.AddToScheme(scheme)
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&v1alpha1.NetworkInfo{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ni1"},
	}).Build()

	networkInfoCR := &v1alpha1.NetworkInfo{}
	require.NoError(t, kubeClient.Get(ctx, apitypes.NamespacedName{Namespace: "ns1", Name: "ni1"}, networkInfoCR))

	state := &v1alpha1.VPCState{Name: "vpc-a"}
	domains := []string{"zone1.example.com", "zone2.example.com"}
	setNetworkInfoVPCStatus(kubeClient, ctx, networkInfoCR, metav1.Now(), state, domains)

	require.NoError(t, kubeClient.Get(ctx, apitypes.NamespacedName{Namespace: "ns1", Name: "ni1"}, networkInfoCR))
	require.Len(t, networkInfoCR.VPCs, 1)
	assert.Equal(t, "vpc-a", networkInfoCR.VPCs[0].Name)
	assert.Equal(t, domains, networkInfoCR.AllowedDNSDomains)

	// Clearing domains while keeping VPC state requires explicit second arg (nil slice).
	state2 := &v1alpha1.VPCState{Name: "vpc-a"}
	setNetworkInfoVPCStatus(kubeClient, ctx, networkInfoCR, metav1.Now(), state2, []string(nil))
	require.NoError(t, kubeClient.Get(ctx, apitypes.NamespacedName{Namespace: "ns1", Name: "ni1"}, networkInfoCR))
	assert.Nil(t, networkInfoCR.AllowedDNSDomains)
}
