package securitypolicy

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
)

func TestEnqueueRequestForNamespace_Create(t *testing.T) {
	type fields struct {
		Client client.Client
	}
	type args struct {
		createEvent event.CreateEvent
		l           workqueue.RateLimitingInterface
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
			e.Create(tt.args.createEvent, tt.args.l)
		})
	}
}

func TestEnqueueRequestForNamespace_Delete(t *testing.T) {
	type fields struct {
		Client client.Client
	}
	type args struct {
		deleteEvent event.DeleteEvent
		l           workqueue.RateLimitingInterface
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
			e.Delete(tt.args.deleteEvent, tt.args.l)
		})
	}
}

func TestEnqueueRequestForNamespace_Generic(t *testing.T) {
	type fields struct {
		Client client.Client
	}
	type args struct {
		genericEvent event.GenericEvent
		l            workqueue.RateLimitingInterface
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
			e.Generic(tt.args.genericEvent, tt.args.l)
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
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface,
	) error {
		return nil
	})
	defer patches.Reset()

	type fields struct {
		Client client.Client
	}
	type args struct {
		updateEvent event.UpdateEvent
		l           workqueue.RateLimitingInterface
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
			}
			e.Update(tt.args.updateEvent, tt.args.l)
		})
	}
}
