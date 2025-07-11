/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkpolicy

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
)

// TestEnqueueRequestForNamespace_Create tests the Create method of EnqueueRequestForNamespace
func TestEnqueueRequestForNamespace_Create(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()
	e := &EnqueueRequestForNamespace{
		Client:                  fakeClient,
		NetworkPolicyReconciler: &NetworkPolicyReconciler{},
	}

	mockQueue := &mockWorkQueue{}
	createEvent := event.CreateEvent{
		Object: &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-namespace",
			},
		},
	}

	// The Create method should just log and do nothing, so no need to mock anything
	e.Create(context.TODO(), createEvent, mockQueue)
	// Test passes if no panic occurs
}

// TestEnqueueRequestForNamespace_Delete tests the Delete method of EnqueueRequestForNamespace
func TestEnqueueRequestForNamespace_Delete(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()
	e := &EnqueueRequestForNamespace{
		Client:                  fakeClient,
		NetworkPolicyReconciler: &NetworkPolicyReconciler{},
	}

	mockQueue := &mockWorkQueue{}
	deleteEvent := event.DeleteEvent{
		Object: &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-namespace",
			},
		},
	}

	// The Delete method should just log and do nothing, so no need to mock anything
	e.Delete(context.TODO(), deleteEvent, mockQueue)
	// Test passes if no panic occurs
}

// TestEnqueueRequestForNamespace_Generic tests the Generic method of EnqueueRequestForNamespace
func TestEnqueueRequestForNamespace_Generic(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()
	e := &EnqueueRequestForNamespace{
		Client:                  fakeClient,
		NetworkPolicyReconciler: &NetworkPolicyReconciler{},
	}

	mockQueue := &mockWorkQueue{}
	genericEvent := event.GenericEvent{
		Object: &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-namespace",
			},
		},
	}

	// The Generic method should just log and do nothing, so no need to mock anything
	e.Generic(context.TODO(), genericEvent, mockQueue)
	// Test passes if no panic occurs
}

// TestEnqueueRequestForNamespace_Update tests the Update method of EnqueueRequestForNamespace
func TestEnqueueRequestForNamespace_Update(t *testing.T) {
	// Mock client.List for pods
	podList := v1.PodList{
		Items: []v1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Ports: []v1.ContainerPort{
								{Name: "test-port", ContainerPort: 8080},
								{Name: "test-port-2", ContainerPort: 80},
							},
						},
					},
				},
			},
		},
	}
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	ctx := context.Background()
	pList := &v1.PodList{}
	ops := client.ListOption(client.InNamespace("test-ns-new"))
	k8sClient.EXPECT().List(ctx, pList, ops).Return(nil).Do(func(_ context.Context, list client.ObjectList,
		o ...client.ListOption,
	) error {
		log.Info("listing pods", "options", o)
		a := list.(*v1.PodList)
		a.Items = podList.Items
		return nil
	})
	patches := gomonkey.ApplyFunc(reconcileNetworkPolicy, func(client client.Client,
		q workqueue.TypedRateLimitingInterface[reconcile.Request],
	) error {
		return nil
	})

	defer patches.Reset()

	type fields struct {
		Client client.Client
	}
	type args struct {
		updateEvent event.UpdateEvent
		l           workqueue.TypedRateLimitingInterface[reconcile.Request]
	}
	evt := event.UpdateEvent{
		ObjectOld: &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-ns",
			},
		},
		ObjectNew: &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-ns-new",
			},
		},
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", fields{k8sClient}, args{updateEvent: evt}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForNamespace{
				Client: tt.fields.Client,
				NetworkPolicyReconciler: &NetworkPolicyReconciler{
					Service: fakeService(),
				},
			}
			e.Update(context.TODO(), tt.args.updateEvent, tt.args.l)
		})
	}
}

// TestPredicateFuncsNs tests the PredicateFuncsNs variable and its functions
func TestPredicateFuncsNs(t *testing.T) {
	testCases := []struct {
		name       string
		event      interface{}
		expectPass bool
	}{
		{
			name: "Create event",
			event: event.CreateEvent{
				Object: &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-namespace",
					},
				},
			},
			expectPass: false,
		},
		{
			name: "Update event with label change",
			event: event.UpdateEvent{
				ObjectOld: &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-namespace",
						Labels: map[string]string{"app": "web"},
					},
				},
				ObjectNew: &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-namespace",
						Labels: map[string]string{"app": "api"},
					},
				},
			},
			expectPass: true,
		},
		{
			name: "Update event without label change",
			event: event.UpdateEvent{
				ObjectOld: &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-namespace",
						Labels: map[string]string{"app": "web"},
					},
				},
				ObjectNew: &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-namespace",
						Labels: map[string]string{"app": "web"},
					},
				},
			},
			expectPass: false,
		},
		{
			name: "Delete event",
			event: event.DeleteEvent{
				Object: &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-namespace",
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
				pass = PredicateFuncsNs.CreateFunc(e)
			case event.UpdateEvent:
				pass = PredicateFuncsNs.UpdateFunc(e)
			case event.DeleteEvent:
				pass = PredicateFuncsNs.DeleteFunc(e)
			}
			assert.Equal(t, tc.expectPass, pass)
		})
	}
}
