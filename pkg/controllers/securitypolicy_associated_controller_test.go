package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
)

func Test_checkPod(t *testing.T) {
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
					},
				},
			},
		},
	}
	pod2 := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-2",
			Namespace: "kube-system",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Ports: []v1.ContainerPort{
						{Name: "test-port", ContainerPort: 8080},
					},
				},
			},
		},
	}
	pod3 := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-3",
			Namespace: "test",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Ports: []v1.ContainerPort{},
				},
			},
		},
	}
	type args struct {
		pod *v1.Pod
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{"1", args{&pod}, true},
		{"2", args{&pod2}, false},
		{"3", args{&pod3}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, checkPod(tt.args.pod, ""), "checkPod(%v)", tt.args.pod)
		})
	}
}

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
		want sets.String
	}{
		{"1", args{[]v1.Pod{pod}}, sets.NewString("test-port", "test-port-2")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, getAllPodPortNames(tt.args.pods), "getAllPodPortNames(%v)", tt.args.pods)
		})
	}
}

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

func TestEnqueueRequestForPod_Raw1(t *testing.T) {
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
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface) error {
		return nil
	})
	defer patches.Reset()
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
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface) error {
		return nil
	})
	defer patches.Reset()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForPod{}
			e.Create(tt.args.evt, tt.args.q)
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
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface) error {
		return nil
	})
	defer patches.Reset()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForPod{}
			e.Update(tt.args.evt, tt.args.q)
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
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface) error {
		return nil
	})
	defer patches.Reset()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForPod{}
			e.Delete(tt.args.evt, tt.args.q)
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
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface) error {
		return nil
	})
	defer patches.Reset()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &EnqueueRequestForPod{}
			e.Generic(tt.args.evt, tt.args.q)
		})
	}
}

func TestEnqueueRequestForNamespace_Create1(t *testing.T) {
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

func TestReconcileSecurityPolicy(t *testing.T) {
	rule := v1alpha1.SecurityPolicyRule{
		Name: "rule-with-pod-selector",
		AppliedTo: []v1alpha1.SecurityPolicyTarget{
			{},
		},
		Sources: []v1alpha1.SecurityPolicyPeer{
			{
				PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"pod_selector_1": "pod_value_1"},
				},
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"ns1": "spA"},
				},
			},
		},
		Ports: []v1alpha1.SecurityPolicyPort{
			{
				Protocol: v1.ProtocolUDP,
				Port:     intstr.IntOrString{Type: intstr.String, StrVal: "named-port"},
			},
		},
	}
	spList := &v1alpha1.SecurityPolicyList{
		Items: []v1alpha1.SecurityPolicy{
			{
				Spec: v1alpha1.SecurityPolicySpec{
					Rules: []v1alpha1.SecurityPolicyRule{
						rule,
					},
				},
			},
		},
	}
	pods := []v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-1",
				Namespace: "spA",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name: "test-container-1",
						Ports: []v1.ContainerPort{
							{
								Name: "named-port",
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
	policyList := &v1alpha1.SecurityPolicyList{}
	k8sClient.EXPECT().List(ctx, policyList).Return(nil).Do(func(_ context.Context, list client.ObjectList,
		_ ...client.ListOption) error {
		a := list.(*v1alpha1.SecurityPolicyList)
		a.Items = spList.Items
		return nil
	})

	mockQueue := mock_client.NewMockInterface(mockCtl)

	type args struct {
		client client.Client
		pods   []v1.Pod
		q      workqueue.RateLimitingInterface
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{k8sClient, pods, mockQueue}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, reconcileSecurityPolicy(tt.args.client, tt.args.pods, tt.args.q),
				fmt.Sprintf("reconcileSecurityPolicy(%v, %v, %v)", tt.args.client, tt.args.pods, tt.args.q))
		})
	}
}

func TestEnqueueRequestForNamespace_Update(t *testing.T) {
	podList := v1.PodList{
		Items: []v1.Pod{
			v1.Pod{
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
		o ...client.ListOption) error {
		log.V(1).Info("Listing pods", "options", o)
		a := list.(*v1.PodList)
		a.Items = podList.Items
		return nil
	})
	patches := gomonkey.ApplyFunc(reconcileSecurityPolicy, func(client client.Client, pods []v1.Pod,
		q workqueue.RateLimitingInterface) error {
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
