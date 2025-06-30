/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkpolicy

import (
	"context"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func TestGetAllPodPortNames(t *testing.T) {
	testCases := []struct {
		name          string
		pods          []v1.Pod
		expectedNames sets.Set[string]
	}{
		{
			name:          "Empty pods",
			pods:          []v1.Pod{},
			expectedNames: sets.New[string](),
		},
		{
			name: "Single pod with named ports",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "http", ContainerPort: 80},
									{Name: "db", ContainerPort: 5432},
								},
							},
						},
					},
				},
			},
			expectedNames: sets.New[string]("http", "db"),
		},
		{
			name: "Multiple pods with mixed named and unnamed ports",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "http", ContainerPort: 80},
									{ContainerPort: 22}, // Unnamed port
								},
							},
						},
					},
				},
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "db", ContainerPort: 5432},
								},
							},
						},
					},
				},
			},
			expectedNames: sets.New[string]("http", "db"),
		},
		{
			name: "Pod with multiple containers having named ports",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "web", ContainerPort: 8080},
								},
							},
							{
								Ports: []v1.ContainerPort{
									{Name: "metrics", ContainerPort: 9090},
									{Name: "health", ContainerPort: 8081},
								},
							},
						},
					},
				},
			},
			expectedNames: sets.New[string]("web", "metrics", "health"),
		},
		{
			name: "Pod with no containers",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{},
					},
				},
			},
			expectedNames: sets.New[string](),
		},
		{
			name: "Pod with containers but no ports",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{},
							},
						},
					},
				},
			},
			expectedNames: sets.New[string](),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualNames := getAllPodPortNames(tc.pods)
			assert.Equal(t, tc.expectedNames, actualNames)
		})
	}
}

func TestReconcileNetworkPolicy(t *testing.T) {
	// Create test NetworkPolicies with named ports
	npWithIngressNamedPort := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "np-with-ingress-named-port",
			Namespace: "default",
		},
		Spec: networkingv1.NetworkPolicySpec{
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Port: &intstr.IntOrString{
								Type:   intstr.String,
								StrVal: "http",
							},
						},
					},
				},
			},
		},
	}

	npWithEgressNamedPort := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "np-with-egress-named-port",
			Namespace: "default",
		},
		Spec: networkingv1.NetworkPolicySpec{
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Port: &intstr.IntOrString{
								Type:   intstr.String,
								StrVal: "db",
							},
						},
					},
				},
			},
		},
	}

	npWithNumericPort := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "np-with-numeric-port",
			Namespace: "default",
		},
		Spec: networkingv1.NetworkPolicySpec{
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Port: &intstr.IntOrString{
								Type:   intstr.Int,
								IntVal: 80,
							},
						},
					},
				},
			},
		},
	}

	npWithoutPorts := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "np-without-ports",
			Namespace: "default",
		},
		Spec: networkingv1.NetworkPolicySpec{
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{},
				},
			},
		},
	}

	testCases := []struct {
		name                      string
		pods                      []v1.Pod
		networkPolicies           []client.Object
		expectedReconcileRequests int
		listNetworkPoliciesError  error
	}{
		{
			name: "Pod with named port matching NetworkPolicy ingress",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "http", ContainerPort: 8080},
								},
							},
						},
					},
				},
			},
			networkPolicies:           []client.Object{npWithIngressNamedPort, npWithNumericPort},
			expectedReconcileRequests: 1, // Only npWithIngressNamedPort should be reconciled
		},
		{
			name: "Pod with named port matching NetworkPolicy egress",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "db", ContainerPort: 5432},
								},
							},
						},
					},
				},
			},
			networkPolicies:           []client.Object{npWithEgressNamedPort, npWithNumericPort},
			expectedReconcileRequests: 1, // Only npWithEgressNamedPort should be reconciled
		},
		{
			name: "Pod with multiple named ports matching multiple NetworkPolicies",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "http", ContainerPort: 8080},
									{Name: "db", ContainerPort: 5432},
								},
							},
						},
					},
				},
			},
			networkPolicies:           []client.Object{npWithIngressNamedPort, npWithEgressNamedPort},
			expectedReconcileRequests: 2, // Both NetworkPolicies should be reconciled
		},
		{
			name: "Pod with named port not matching any NetworkPolicy",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "redis", ContainerPort: 6379},
								},
							},
						},
					},
				},
			},
			networkPolicies:           []client.Object{npWithIngressNamedPort, npWithEgressNamedPort},
			expectedReconcileRequests: 0, // No NetworkPolicies should be reconciled
		},
		{
			name: "Pod without named ports",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{ContainerPort: 8080}, // Unnamed port
								},
							},
						},
					},
				},
			},
			networkPolicies:           []client.Object{npWithIngressNamedPort},
			expectedReconcileRequests: 0, // No NetworkPolicies should be reconciled
		},
		{
			name: "NetworkPolicy without named ports",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "http", ContainerPort: 8080},
								},
							},
						},
					},
				},
			},
			networkPolicies:           []client.Object{npWithoutPorts, npWithNumericPort},
			expectedReconcileRequests: 0, // No NetworkPolicies should be reconciled
		},
		{
			name:                      "Empty pods list",
			pods:                      []v1.Pod{},
			networkPolicies:           []client.Object{npWithIngressNamedPort},
			expectedReconcileRequests: 0, // No NetworkPolicies should be reconciled
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithObjects(tc.networkPolicies...).Build()

			// Mock workqueue to count reconcile requests
			reconcileCount := 0
			mockQueue := &mockWorkQueue{
				addFunc: func(item reconcile.Request) {
					reconcileCount++
				},
			}

			err := reconcileNetworkPolicy(fakeClient, tc.pods, mockQueue)

			if tc.listNetworkPoliciesError != nil {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.listNetworkPoliciesError.Error())
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedReconcileRequests, reconcileCount)
			}
		})
	}
}

// Mock workqueue for testing
type mockWorkQueue struct {
	addFunc func(item reconcile.Request)
}

func (m *mockWorkQueue) Add(item reconcile.Request) {
	if m.addFunc != nil {
		m.addFunc(item)
	}
}

func (m *mockWorkQueue) Len() int { return 0 }
func (m *mockWorkQueue) Get() (item reconcile.Request, shutdown bool) {
	return reconcile.Request{}, false
}
func (m *mockWorkQueue) Done(_ reconcile.Request)                      {}
func (m *mockWorkQueue) ShutDown()                                     {}
func (m *mockWorkQueue) ShutDownWithDrain()                            {}
func (m *mockWorkQueue) ShuttingDown() bool                            { return false }
func (m *mockWorkQueue) AddAfter(_ reconcile.Request, _ time.Duration) {}
func (m *mockWorkQueue) AddRateLimited(_ reconcile.Request)            {}
func (m *mockWorkQueue) Forget(_ reconcile.Request)                    {}
func (m *mockWorkQueue) NumRequeues(_ reconcile.Request) int           { return 0 }

func TestEnqueueRequestForPod_Raw(t *testing.T) {
	testCases := []struct {
		name          string
		event         interface{}
		isSystemNs    bool
		isSystemNsErr error
		expectError   bool
	}{
		{
			name: "CreateEvent with regular namespace",
			event: event.CreateEvent{
				Object: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
				},
			},
			isSystemNs:  false,
			expectError: false,
		},
		{
			name: "UpdateEvent with regular namespace",
			event: event.UpdateEvent{
				ObjectNew: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
				},
			},
			isSystemNs:  false,
			expectError: false,
		},
		{
			name: "DeleteEvent with regular namespace",
			event: event.DeleteEvent{
				Object: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
				},
			},
			isSystemNs:  false,
			expectError: false,
		},
		{
			name: "GenericEvent with regular namespace",
			event: event.GenericEvent{
				Object: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
				},
			},
			isSystemNs:  false,
			expectError: false,
		},
		{
			name: "CreateEvent with system namespace",
			event: event.CreateEvent{
				Object: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "system-pod",
						Namespace: "kube-system",
					},
				},
			},
			isSystemNs:  true,
			expectError: false,
		},
		{
			name: "CreateEvent with IsSystemNamespace error",
			event: event.CreateEvent{
				Object: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
				},
			},
			isSystemNsErr: assert.AnError,
			expectError:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().Build()

			e := &EnqueueRequestForPod{
				Client: fakeClient,
			}

			// Mock util.IsSystemNamespace
			patches := gomonkey.ApplyFunc(util.IsSystemNamespace, func(client.Client, string, *v1.Namespace) (bool, error) {
				return tc.isSystemNs, tc.isSystemNsErr
			})
			defer patches.Reset()

			// Mock reconcileNetworkPolicy to avoid actual reconciliation
			patches.ApplyFunc(reconcileNetworkPolicy, func(client.Client, []v1.Pod, workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
				if tc.expectError {
					return assert.AnError
				}
				return nil
			})

			// Call the Raw method
			e.Raw(tc.event, &mockWorkQueue{})

			// The test passes if no panic occurs and the function completes
		})
	}
}

func TestEnqueueRequestForPod_EventMethods(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()

	e := &EnqueueRequestForPod{
		Client: fakeClient,
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}

	// Mock util.IsSystemNamespace
	patches := gomonkey.ApplyFunc(util.IsSystemNamespace, func(client.Client, string, *v1.Namespace) (bool, error) {
		return false, nil
	})
	defer patches.Reset()

	// Mock reconcileNetworkPolicy
	patches.ApplyFunc(reconcileNetworkPolicy, func(client.Client, []v1.Pod, workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
		return nil
	})

	mockQueue := &mockWorkQueue{}

	// Test all event methods
	t.Run("Create", func(t *testing.T) {
		createEvent := event.CreateEvent{Object: pod}
		e.Create(context.TODO(), createEvent, mockQueue)
	})

	t.Run("Update", func(t *testing.T) {
		updateEvent := event.UpdateEvent{ObjectNew: pod}
		e.Update(context.TODO(), updateEvent, mockQueue)
	})

	t.Run("Delete", func(t *testing.T) {
		deleteEvent := event.DeleteEvent{Object: pod}
		e.Delete(context.TODO(), deleteEvent, mockQueue)
	})

	t.Run("Generic", func(t *testing.T) {
		genericEvent := event.GenericEvent{Object: pod}
		e.Generic(context.TODO(), genericEvent, mockQueue)
	})
}

func TestPredicateFuncsPod(t *testing.T) {
	testCases := []struct {
		name       string
		event      interface{}
		expectPass bool
	}{
		{
			name: "Create event with named port",
			event: event.CreateEvent{
				Object: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{
										Name:          "http",
										Protocol:      "TCP",
										ContainerPort: 8080,
									},
								},
							},
						},
					},
				},
			},
			expectPass: true,
		},
		{
			name: "Create event without named port",
			event: event.CreateEvent{
				Object: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{
										Protocol:      "TCP",
										ContainerPort: 8080,
									},
								},
							},
						},
					},
				},
			},
			expectPass: false,
		},
		{
			name: "Update event with phase change and named port",
			event: event.UpdateEvent{
				ObjectOld: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels:    map[string]string{"app": "web"},
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{
										Name:          "http",
										Protocol:      "TCP",
										ContainerPort: 8080,
									},
								},
							},
						},
					},
					Status: v1.PodStatus{Phase: v1.PodPending},
				},
				ObjectNew: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels:    map[string]string{"app": "web"},
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{
										Name:          "http",
										Protocol:      "TCP",
										ContainerPort: 8080,
									},
								},
							},
						},
					},
					Status: v1.PodStatus{Phase: v1.PodRunning},
				},
			},
			expectPass: true,
		},
		{
			name: "Update event with no label or phase change",
			event: event.UpdateEvent{
				ObjectOld: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels:    map[string]string{"app": "web"},
					},
					Status: v1.PodStatus{Phase: v1.PodRunning},
				},
				ObjectNew: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels:    map[string]string{"app": "web"},
					},
					Status: v1.PodStatus{Phase: v1.PodRunning},
				},
			},
			expectPass: false,
		},
		{
			name: "Update event with label change and named port",
			event: event.UpdateEvent{
				ObjectOld: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels:    map[string]string{"app": "web"},
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{
										Name:          "http",
										Protocol:      "TCP",
										ContainerPort: 8080,
									},
								},
							},
						},
					},
					Status: v1.PodStatus{Phase: v1.PodRunning},
				},
				ObjectNew: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels:    map[string]string{"app": "web", "version": "v1"},
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{
										Name:          "http",
										Protocol:      "TCP",
										ContainerPort: 8080,
									},
								},
							},
						},
					},
					Status: v1.PodStatus{Phase: v1.PodRunning},
				},
			},
			expectPass: true,
		},
		{
			name: "Delete event with named port",
			event: event.DeleteEvent{
				Object: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{
										Name:          "http",
										Protocol:      "TCP",
										ContainerPort: 8080,
									},
								},
							},
						},
					},
				},
			},
			expectPass: true,
		},
		{
			name: "Delete event without named port",
			event: event.DeleteEvent{
				Object: &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{
										Protocol:      "TCP",
										ContainerPort: 8080,
									},
								},
							},
						},
					},
				},
			},
			expectPass: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var pass bool
			switch e := tc.event.(type) {
			case event.CreateEvent:
				pass = PredicateFuncsPod.CreateFunc(e)
			case event.UpdateEvent:
				pass = PredicateFuncsPod.UpdateFunc(e)
			case event.DeleteEvent:
				pass = PredicateFuncsPod.DeleteFunc(e)
			}
			assert.Equal(t, tc.expectPass, pass)
		})
	}
}

func TestReconcileNetworkPolicyWithComplexScenarios(t *testing.T) {
	// Test with NetworkPolicy having both ingress and egress rules with named ports
	npWithBothRules := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "np-with-both-rules",
			Namespace: "default",
		},
		Spec: networkingv1.NetworkPolicySpec{
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Port: &intstr.IntOrString{
								Type:   intstr.String,
								StrVal: "web",
							},
						},
					},
				},
			},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Port: &intstr.IntOrString{
								Type:   intstr.String,
								StrVal: "db",
							},
						},
					},
				},
			},
		},
	}

	// Test with NetworkPolicy having multiple ports in the same rule
	npWithMultiplePorts := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "np-with-multiple-ports",
			Namespace: "default",
		},
		Spec: networkingv1.NetworkPolicySpec{
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Port: &intstr.IntOrString{
								Type:   intstr.String,
								StrVal: "http",
							},
						},
						{
							Port: &intstr.IntOrString{
								Type:   intstr.Int,
								IntVal: 8080,
							},
						},
						{
							Port: &intstr.IntOrString{
								Type:   intstr.String,
								StrVal: "metrics",
							},
						},
					},
				},
			},
		},
	}

	testCases := []struct {
		name                      string
		pods                      []v1.Pod
		networkPolicies           []client.Object
		expectedReconcileRequests int
	}{
		{
			name: "Pod matching both ingress and egress named ports",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "web", ContainerPort: 8080},
									{Name: "db", ContainerPort: 5432},
								},
							},
						},
					},
				},
			},
			networkPolicies:           []client.Object{npWithBothRules},
			expectedReconcileRequests: 1, // Should reconcile once for the NetworkPolicy
		},
		{
			name: "Pod matching only one of multiple named ports in rule",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "metrics", ContainerPort: 9090},
								},
							},
						},
					},
				},
			},
			networkPolicies:           []client.Object{npWithMultiplePorts},
			expectedReconcileRequests: 1, // Should reconcile for the matching named port
		},
		{
			name: "Pod with port matching numeric port in rule",
			pods: []v1.Pod{
				{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Ports: []v1.ContainerPort{
									{Name: "api", ContainerPort: 8080}, // Named but not matching string ports
								},
							},
						},
					},
				},
			},
			networkPolicies:           []client.Object{npWithMultiplePorts},
			expectedReconcileRequests: 0, // Should not reconcile as "api" doesn't match any string port
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithObjects(tc.networkPolicies...).Build()

			reconcileCount := 0
			mockQueue := &mockWorkQueue{
				addFunc: func(item reconcile.Request) {
					reconcileCount++
				},
			}

			err := reconcileNetworkPolicy(fakeClient, tc.pods, mockQueue)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedReconcileRequests, reconcileCount)
		})
	}
}
