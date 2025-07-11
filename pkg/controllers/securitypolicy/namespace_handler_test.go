/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func TestEnqueueRequestForNamespace_Create(t *testing.T) {
	type fields struct {
		Client client.Client
	}
	type args struct {
		createEvent event.CreateEvent
		l           workqueue.TypedRateLimitingInterface[reconcile.Request]
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{}, args{event.CreateEvent{}, nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForNamespace{
				Client: tt.fields.Client,
			}
			e.Create(context.TODO(), tt.args.createEvent, tt.args.l)
		})
	}
}

func TestEnqueueRequestForNamespace_Delete(t *testing.T) {
	type fields struct {
		Client client.Client
	}
	type args struct {
		deleteEvent event.DeleteEvent
		l           workqueue.TypedRateLimitingInterface[reconcile.Request]
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{}, args{event.DeleteEvent{}, nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForNamespace{
				Client: tt.fields.Client,
			}
			e.Delete(context.TODO(), tt.args.deleteEvent, tt.args.l)
		})
	}
}

func TestEnqueueRequestForNamespace_Generic(t *testing.T) {
	type fields struct {
		Client client.Client
	}
	type args struct {
		genericEvent event.GenericEvent
		l            workqueue.TypedRateLimitingInterface[reconcile.Request]
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{}, args{event.GenericEvent{}, nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForNamespace{
				Client: tt.fields.Client,
			}
			e.Generic(context.TODO(), tt.args.genericEvent, tt.args.l)
		})
	}
}

func TestEnqueueRequestForNamespace_Update(t *testing.T) {
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
	patches := gomonkey.ApplyFuncSeq(util.IsSystemNamespace, []gomonkey.OutputCell{
		{Values: gomonkey.Params{false, nil}},
	})
	patches.ApplyFunc(reconcileSecurityPolicy, func(r *SecurityPolicyReconciler, client client.Client, pods []v1.Pod,
		q workqueue.TypedRateLimitingInterface[reconcile.Request],
	) error {
		return nil
	})
	patches.ApplyFunc(securitypolicy.IsVPCEnabled, func(_ interface{}) bool {
		return false
	})
	patches.ApplyFunc(util.CheckPodHasNamedPort, func(pod v1.Pod, reason string) bool {
		return true
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
				SecurityPolicyReconciler: &SecurityPolicyReconciler{
					Service: fakeService(),
				},
			}
			e.Update(context.TODO(), tt.args.updateEvent, tt.args.l)
		})
	}
}

func TestPredicateFuncsNs(t *testing.T) {
	oldNamespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-namespace",
			Labels: map[string]string{"env": "test"},
		},
	}

	newNamespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-namespace",
			Labels: map[string]string{"env": "prod"},
		},
	}

	updateEvent := event.UpdateEvent{
		ObjectOld: oldNamespace,
		ObjectNew: newNamespace,
	}

	// Test the update function logic in PredicateFuncsNs
	result := PredicateFuncsNs.UpdateFunc(updateEvent)
	assert.True(t, result, "Expected update event to trigger requeue")

	// Test with no label change
	noChangeEvent := event.UpdateEvent{
		ObjectOld: oldNamespace,
		ObjectNew: oldNamespace,
	}

	result = PredicateFuncsNs.UpdateFunc(noChangeEvent)
	assert.False(t, result, "Expected no action when labels have not changed")

	res := PredicateFuncsNs.CreateFunc(event.CreateEvent{Object: newNamespace})
	assert.False(t, res, "Expected no action when labels have not changed")

	res = PredicateFuncsNs.DeleteFunc(event.DeleteEvent{Object: newNamespace})
	assert.False(t, res, "Expected no action when labels have not changed")
}
