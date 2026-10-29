/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package infrastructure

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	netapi "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func readyObjects() (*netapi.WorkloadNetworkConfiguration, *corev1.Pod) {
	config := &netapi.WorkloadNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "default", UID: "wlnc", Generation: 2}, Spec: netapi.WorkloadNetworkConfigurationSpec{ActiveSystemProvider: netapi.NetworkProviderVSphereDistributed, Providers: []netapi.NetworkProviderEntry{{Type: netapi.NetworkProviderVSphereDistributed}, {Type: netapi.NetworkProviderVPC, SystemConfiguration: &netapi.NamespaceNetworkConfig{VPCConfig: netapi.VPCConfig{AutoCreateConfig: netapi.AutoCreateVPCConfig{NSXProject: "/orgs/default/projects/default"}}}}}}, Status: &netapi.WorkloadNetworkConfigurationStatus{Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 2}}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "nsx-ncp-0", Namespace: ncpNamespace, UID: "ncp", Labels: map[string]string{"nsx-operator-role": "master"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "nsx-operator", Ready: true, ContainerID: "containerd://current", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
	return config, pod
}
func TestKubernetesReadiness(t *testing.T) {
	cases := []struct {
		name     string
		change   func(*netapi.WorkloadNetworkConfiguration, *corev1.Pod)
		allowVPC bool
		reason   string
	}{
		{name: "ready"},
		{name: "provider missing", change: func(c *netapi.WorkloadNetworkConfiguration, p *corev1.Pod) { c.Spec.Providers = c.Spec.Providers[:1] }, reason: "ProviderNotRegistered"},
		{name: "wrong project", change: func(c *netapi.WorkloadNetworkConfiguration, p *corev1.Pod) {
			c.Spec.Providers[1].SystemConfiguration.VPCConfig.AutoCreateConfig.NSXProject = "/orgs/default/projects/other"
		}, reason: "ProviderProjectMismatch"},
		{name: "stale provider readiness", change: func(c *netapi.WorkloadNetworkConfiguration, p *corev1.Pod) {
			c.Status.Conditions[0].ObservedGeneration = 1
		}, reason: "ProviderNotReady"},
		{name: "migration container alone", change: func(c *netapi.WorkloadNetworkConfiguration, p *corev1.Pod) {
			p.Status.ContainerStatuses[0].Name = "supervisor-network-migration-operator"
		}, reason: "NCPNotReady"},
		{name: "operator unready", change: func(c *netapi.WorkloadNetworkConfiguration, p *corev1.Pod) {
			p.Status.ContainerStatuses[0].Ready = false
		}, reason: "NCPNotReady"},
		{name: "standby only", change: func(c *netapi.WorkloadNetworkConfiguration, p *corev1.Pod) { p.Labels["nsx-operator-role"] = "standby" }, reason: "NCPNotReady"},
		{name: "new request on vpc", change: func(c *netapi.WorkloadNetworkConfiguration, p *corev1.Pod) {
			c.Spec.ActiveSystemProvider = netapi.NetworkProviderVPC
		}, reason: "UnexpectedActiveProvider"},
		{name: "resume on vpc", change: func(c *netapi.WorkloadNetworkConfiguration, p *corev1.Pod) {
			c.Spec.ActiveSystemProvider = netapi.NetworkProviderVPC
		}, allowVPC: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, netapi.AddToScheme(scheme))
			config, pod := readyObjects()
			if tc.change != nil {
				tc.change(config, pod)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(config, pod).Build()
			r := KubernetesReadiness{Reader: c}
			err := r.Check(context.Background(), tc.allowVPC, "/orgs/default/projects/default")
			if tc.reason == "" {
				require.NoError(t, err)
			} else {
				var issue *Issue
				require.ErrorAs(t, err, &issue)
				require.Equal(t, tc.reason, issue.Reason)
			}
		})
	}
}

func TestReadFailureIsNotUnregisteredProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, netapi.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
		return apierrors.NewForbidden(schema.GroupResource{Group: netapi.GroupName, Resource: "workloadnetworkconfigurations"}, "default", nil)
	}}).Build()
	err := (&KubernetesReadiness{Reader: c}).Check(context.Background(), false, "/orgs/default/projects/default")
	var problem *Issue
	require.ErrorAs(t, err, &problem)
	require.True(t, problem.Unknown)
	require.Equal(t, "PrerequisiteReadFailed", problem.Reason)
}
