/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestSetVPCNetworkConfigurationStatusWithGatewayConnection(t *testing.T) {
	tests := []struct {
		name                    string
		prepareFunc             func(*testing.T, context.Context, client.Client, string, bool, string, *v1alpha1.VPCNetworkConfiguration) *gomonkey.Patches
		gatewayConnectionReady  bool
		reason                  string
		expectedConditionType   v1alpha1.ConditionType
		expectedConditionStatus corev1.ConditionStatus
		expectedConditionReason string
	}{
		{
			name: "GatewayConnectionReady",
			prepareFunc: func(_ *testing.T, ctx context.Context, client client.Client, _ string, _ bool, _ string, nc *v1alpha1.VPCNetworkConfiguration) (patches *gomonkey.Patches) {
				assert.NoError(t, client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ncName",
					},
				}))
				patches = &gomonkey.Patches{}
				return patches
			},
			gatewayConnectionReady:  true,
			reason:                  "",
			expectedConditionType:   v1alpha1.GatewayConnectionReady,
			expectedConditionStatus: corev1.ConditionTrue,
			expectedConditionReason: "",
		},
		{
			name: "GatewayConnectionNotReady",
			prepareFunc: func(_ *testing.T, ctx context.Context, client client.Client, _ string, _ bool, _ string, nc *v1alpha1.VPCNetworkConfiguration) (patches *gomonkey.Patches) {
				assert.NoError(t, client.Create(ctx, &v1alpha1.VPCNetworkConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ncName",
					},
				}))
				patches = &gomonkey.Patches{}
				return patches
			},
			gatewayConnectionReady:  false,
			reason:                  "EdgeMissingInProject",
			expectedConditionType:   v1alpha1.GatewayConnectionReady,
			expectedConditionStatus: corev1.ConditionFalse,
			expectedConditionReason: "EdgeMissingInProject",
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
				patches := tt.prepareFunc(t, ctx, client, "ncName", tt.gatewayConnectionReady, tt.reason, actualCR)
				defer patches.Reset()
			}
			setVPCNetworkConfigurationStatusWithGatewayConnection(ctx, client, actualCR, tt.gatewayConnectionReady, tt.reason)
			assert.Equal(t, tt.expectedConditionReason, actualCR.Status.Conditions[0].Reason)
			assert.Equal(t, tt.expectedConditionType, actualCR.Status.Conditions[0].Type)
			assert.Equal(t, tt.expectedConditionStatus, actualCR.Status.Conditions[0].Status)
		})
	}
}

func TestSetVPCNetworkConfigurationStatusWithSnatEnabled(t *testing.T) {
	tests := []struct {
		name                    string
		prepareFunc             func(*testing.T, context.Context, client.Client, string, bool, *v1alpha1.VPCNetworkConfiguration) *gomonkey.Patches
		autoSnatEnabled         bool
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
		},
		{
			name: "AutoSnatDisabled",
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
			setVPCNetworkConfigurationStatusWithSnatEnabled(ctx, client, actualCR, tt.autoSnatEnabled)
			assert.Equal(t, tt.expectedConditionType, actualCR.Status.Conditions[0].Type)
			assert.Equal(t, tt.expectedConditionStatus, actualCR.Status.Conditions[0].Status)
		})
	}
}

func TestGetGatewayConnectionStatus(t *testing.T) {
	tests := []struct {
		name           string
		prepareFunc    func(*testing.T, context.Context, client.Client) *gomonkey.Patches
		conditions     []v1alpha1.Condition
		expectedStatus bool
		expectedReason string
	}{
		{
			name:           "EmptyCondition",
			prepareFunc:    nil,
			conditions:     []v1alpha1.Condition{},
			expectedStatus: false,
			expectedReason: "",
		},
		{
			name:        "GetGatewayConnectionStatusReady",
			prepareFunc: nil,
			conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.AutoSnatEnabled,
					Status: corev1.ConditionFalse,
				},
				{
					Type:   v1alpha1.GatewayConnectionReady,
					Status: corev1.ConditionTrue,
					Reason: "reason",
				},
			},
			expectedStatus: true,
			expectedReason: "reason",
		},
	}
	for _, tt := range tests {
		ctx := context.TODO()
		t.Run(tt.name, func(t *testing.T) {
			vpcNetworkConfiguration := v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ncName",
				},
				Status: v1alpha1.VPCNetworkConfigurationStatus{
					Conditions: tt.conditions,
				},
			}
			gatewayConnectionReady, reason, _ := getGatewayConnectionStatus(ctx, &vpcNetworkConfiguration)
			assert.Equal(t, tt.expectedReason, reason)
			assert.Equal(t, tt.expectedStatus, gatewayConnectionReady)
		})
	}
}
