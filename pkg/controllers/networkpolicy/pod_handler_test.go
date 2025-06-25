/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
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
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

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
				NetworkPolicyReconciler: &NetworkPolicyReconciler{
					Service: fakeService(),
				},
			}

			// Mock util.IsSystemNamespace
			patches := gomonkey.ApplyFunc(securitypolicy.IsVPCEnabled, func(_ interface{}) bool {
				return false
			})
			patches.ApplyFunc(util.IsSystemNamespace, func(client.Client, string, *v1.Namespace) (bool, error) {
				return tc.isSystemNs, tc.isSystemNsErr
			})
			// Mock reconcileNetworkPolicy to avoid actual reconciliation
			patches.ApplyFunc(reconcileNetworkPolicy, func(client.Client, workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
				if tc.expectError {
					return assert.AnError
				}
				return nil
			})

			// Call the Raw method
			e.Raw(tc.event, &mockWorkQueue{})
			patches.Reset()

		})
	}
}

func TestEnqueueRequestForPod_EventMethods(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()

	e := &EnqueueRequestForPod{
		Client: fakeClient,
		NetworkPolicyReconciler: &NetworkPolicyReconciler{
			Service: fakeService(),
		},
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
	patches.ApplyFunc(reconcileNetworkPolicy, func(client.Client, workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
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

			err := reconcileNetworkPolicy(fakeClient, mockQueue)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedReconcileRequests, reconcileCount)
		})
	}
}
