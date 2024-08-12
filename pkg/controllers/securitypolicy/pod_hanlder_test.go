package securitypolicy

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func Test_getAllPodPortNames(t *testing.T) {
	pod := v1.Pod{
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
	}
	type args struct {
		pods []v1.Pod
	}
	tests := []struct {
		name string
		args args
		want sets.Set[string]
	}{
		{"1", args{[]v1.Pod{pod}}, sets.New[string]("test-port", "test-port-2")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, getAllPodPortNames(tt.args.pods), "getAllPodPortNames(%v)", tt.args.pods)
		})
	}
}

func TestEnqueueRequestForPod_Raw(t *testing.T) {
	evt := event.CreateEvent{
		Object: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod",
			},
		},
	}
	type fields struct {
		Client client.Client
	}
	type args struct {
		evt interface{}
		q   workqueue.RateLimitingInterface
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{}, args{evt, nil}},
	}
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(r *SecurityPolicyReconciler, client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface,
	) error {
		return nil
	})
	defer patches.Reset()

	patches2 := gomonkey.ApplyFunc(util.IsSystemNamespace, func(client client.Client, ns string, obj *v1.Namespace,
	) (bool, error) {
		return false, nil
	})
	defer patches2.Reset()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForPod{}
			e.Raw(tt.args.evt, tt.args.q)
		})
	}
}

func TestEnqueueRequestForPod_Create(t *testing.T) {
	evt := event.CreateEvent{
		Object: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod",
			},
		},
	}
	type fields struct {
		Client client.Client
	}
	type args struct {
		evt event.CreateEvent
		q   workqueue.RateLimitingInterface
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{}, args{evt, nil}},
	}
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(r *SecurityPolicyReconciler, client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface,
	) error {
		return nil
	})
	defer patches.Reset()

	patches2 := gomonkey.ApplyFunc(util.IsSystemNamespace, func(client client.Client, ns string, obj *v1.Namespace,
	) (bool, error) {
		return false, nil
	})
	defer patches2.Reset()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForPod{}
			e.Create(context.TODO(), tt.args.evt, tt.args.q)
		})
	}
}

func TestEnqueueRequestForPod_Update(t *testing.T) {
	evt := event.UpdateEvent{
		ObjectOld: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod",
			},
		},
		ObjectNew: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-new",
			},
		},
	}
	type fields struct {
		Client client.Client
	}
	type args struct {
		evt event.UpdateEvent
		q   workqueue.RateLimitingInterface
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{}, args{evt, nil}},
	}
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(r *SecurityPolicyReconciler, client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface,
	) error {
		return nil
	})
	defer patches.Reset()

	patches2 := gomonkey.ApplyFunc(util.IsSystemNamespace, func(client client.Client, ns string, obj *v1.Namespace,
	) (bool, error) {
		return false, nil
	})
	defer patches2.Reset()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForPod{}
			e.Update(context.TODO(), tt.args.evt, tt.args.q)
		})
	}
}

func TestEnqueueRequestForPod_Delete(t *testing.T) {
	evt := event.DeleteEvent{
		Object: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod",
			},
		},
	}
	type fields struct {
		Client client.Client
	}
	type args struct {
		evt event.DeleteEvent
		q   workqueue.RateLimitingInterface
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{}, args{evt, nil}},
	}
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(r *SecurityPolicyReconciler, client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface,
	) error {
		return nil
	})
	defer patches.Reset()

	patches2 := gomonkey.ApplyFunc(util.IsSystemNamespace, func(client client.Client, ns string, obj *v1.Namespace,
	) (bool, error) {
		return false, nil
	})
	defer patches2.Reset()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForPod{}
			e.Delete(context.TODO(), tt.args.evt, tt.args.q)
		})
	}
}

func TestEnqueueRequestForPod_Generic(t *testing.T) {
	evt := event.GenericEvent{
		Object: &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod",
			},
		},
	}
	type fields struct {
		Client client.Client
	}
	type args struct {
		evt event.GenericEvent
		q   workqueue.RateLimitingInterface
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"1", fields{}, args{evt, nil}},
	}
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(r *SecurityPolicyReconciler, client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface,
	) error {
		return nil
	})
	defer patches.Reset()

	patches2 := gomonkey.ApplyFunc(util.IsSystemNamespace, func(client client.Client, ns string, obj *v1.Namespace,
	) (bool, error) {
		return false, nil
	})
	defer patches2.Reset()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForPod{}
			e.Generic(context.TODO(), tt.args.evt, tt.args.q)
		})
	}
}
