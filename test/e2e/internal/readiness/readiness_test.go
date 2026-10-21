package readiness

import (
	"context"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
)

type fixture struct {
	deployment *appsv1.Deployment
	webhooks   *admissionv1.ValidatingWebhookConfiguration
	service    *corev1.Service
	slices     []*discoveryv1.EndpointSlice
	replicaSet *appsv1.ReplicaSet
	pod        *corev1.Pod
	extra      []runtime.Object
}

func readyFixture(cfg Config) fixture {
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"component": "operator"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "operator", Image: "operator:current"}}},
	}
	f := fixture{
		deployment: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cfg.Namespace, Name: cfg.DeploymentName, UID: "deployment-uid", Generation: 3,
				Annotations: map[string]string{"deployment.kubernetes.io/revision": "4"},
			},
			Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(1)), Template: *template.DeepCopy()},
			Status: appsv1.DeploymentStatus{
				ObservedGeneration: 3,
				Replicas:           1,
				UpdatedReplicas:    1,
				ReadyReplicas:      1,
				AvailableReplicas:  1,
			},
		},
		webhooks: &admissionv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: cfg.WebhookConfigName},
			Webhooks: []admissionv1.ValidatingWebhook{{
				Name: cfg.WebhookName,
				ClientConfig: admissionv1.WebhookClientConfig{Service: &admissionv1.ServiceReference{
					Namespace: cfg.Namespace,
					Name:      "webhook-service",
				}},
			}},
		},
		service: &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: cfg.Namespace, Name: "webhook-service"}},
		slices: []*discoveryv1.EndpointSlice{{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cfg.Namespace, Name: "webhook-endpoints",
				Labels: map[string]string{discoveryv1.LabelServiceName: "webhook-service"},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
			Endpoints: []discoveryv1.Endpoint{{
				Addresses:  []string{"192.0.2.1"},
				Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true)},
				TargetRef:  &corev1.ObjectReference{Kind: "Pod", Namespace: cfg.Namespace, Name: "operator-current", UID: "pod-uid"},
			}},
		}},
	}
	f.replicaSet = &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cfg.Namespace, Name: "operator-current", UID: "replica-set-uid",
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": "4"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: cfg.DeploymentName, UID: f.deployment.UID, Controller: ptr.To(true)}},
		},
		Spec: appsv1.ReplicaSetSpec{Template: *template.DeepCopy()},
	}
	f.replicaSet.Spec.Template.Labels[appsv1.DefaultDeploymentUniqueLabelKey] = "current"
	f.pod = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cfg.Namespace, Name: "operator-current", UID: "pod-uid",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: f.replicaSet.Name, UID: f.replicaSet.UID, Controller: ptr.To(true)}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
	}
	return f
}

func (f fixture) objects() []runtime.Object {
	var objects []runtime.Object
	if f.deployment != nil {
		objects = append(objects, f.deployment)
	}
	if f.webhooks != nil {
		objects = append(objects, f.webhooks)
	}
	if f.service != nil {
		objects = append(objects, f.service)
	}
	for _, slice := range f.slices {
		objects = append(objects, slice)
	}
	if f.replicaSet != nil {
		objects = append(objects, f.replicaSet)
	}
	if f.pod != nil {
		objects = append(objects, f.pod)
	}
	return append(objects, f.extra...)
}

func TestCheck(t *testing.T) {
	cfg := Config{
		Namespace: "operator-system", DeploymentName: "operator",
		WebhookConfigName: "operator-webhooks", WebhookName: "validate.example.com",
	}
	tests := []struct {
		name    string
		change  func(*fixture)
		wantErr string
	}{
		{name: "ready"},
		{
			name: "old replica is ready while replacement has not started",
			change: func(f *fixture) {
				f.deployment.Status.UpdatedReplicas = 0
			},
			wantErr: "updated=0 ready=1 available=1",
		},
		{
			name: "controller has not observed current generation",
			change: func(f *fixture) {
				f.deployment.Status.ObservedGeneration = 2
			},
			wantErr: "generation=3 observedGeneration=2",
		},
		{
			name: "scaled down deployment",
			change: func(f *fixture) {
				f.deployment.Spec.Replicas = ptr.To(int32(0))
				f.deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: 3}
			},
			wantErr: "desired=0",
		},
		{
			name: "current webhook replica is ready during rollout",
			change: func(f *fixture) {
				f.deployment.Status.Replicas = 2
			},
		},
		{
			name: "current master ready with unschedulable standby",
			change: func(f *fixture) {
				f.deployment.Spec.Replicas = ptr.To(int32(2))
				f.deployment.Status.Replicas = 2
				f.deployment.Status.UpdatedReplicas = 2
				standby := f.pod.DeepCopy()
				standby.Name, standby.UID = "operator-standby", "standby-uid"
				standby.Status = corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{
					Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "no free host ports",
				}}}
				f.extra = append(f.extra, standby)
			},
		},
		{
			name: "ready endpoint from old replica template",
			change: func(f *fixture) {
				f.deployment.Spec.Template.Spec.Containers[0].Image = "operator:new"
			},
			wantErr: "older Pod template",
		},
		{
			name: "ready endpoint from older deployment revision",
			change: func(f *fixture) {
				f.replicaSet.Annotations["deployment.kubernetes.io/revision"] = "3"
			},
			wantErr: `revision="3" differs from current Deployment revision="4"`,
		},
		{
			name: "ready old replica cannot hide behind ready current replica",
			change: func(f *fixture) {
				oldReplicaSet := f.replicaSet.DeepCopy()
				oldReplicaSet.Name, oldReplicaSet.UID = "operator-old", "old-replica-set-uid"
				oldReplicaSet.Spec.Template.Spec.Containers[0].Image = "operator:old"
				oldPod := f.pod.DeepCopy()
				oldPod.Name, oldPod.UID = "operator-old", "old-pod-uid"
				oldPod.OwnerReferences[0].Name, oldPod.OwnerReferences[0].UID = oldReplicaSet.Name, oldReplicaSet.UID
				f.extra = append(f.extra, oldReplicaSet, oldPod)
				oldEndpoint := f.slices[0].Endpoints[0].DeepCopy()
				oldEndpoint.Addresses = []string{"192.0.2.2"}
				oldEndpoint.TargetRef.Name, oldEndpoint.TargetRef.UID = oldPod.Name, oldPod.UID
				f.slices[0].Endpoints = append(f.slices[0].Endpoints, *oldEndpoint)
			},
			wantErr: "older Pod template",
		},
		{
			name:    "endpoint Pod is no longer Ready",
			change:  func(f *fixture) { f.pod.Status.Conditions[0].Status = corev1.ConditionFalse },
			wantErr: "is not Ready",
		},
		{
			name:    "endpoint references previous Pod with same name",
			change:  func(f *fixture) { f.pod.UID = "replacement-pod-uid" },
			wantErr: "is stale, terminating or not running",
		},
		{
			name:    "endpoint Pod is terminating",
			change:  func(f *fixture) { now := metav1.Now(); f.pod.DeletionTimestamp = &now },
			wantErr: "is stale, terminating or not running",
		},
		{
			name:    "ready endpoint lacks Pod reference",
			change:  func(f *fixture) { f.slices[0].Endpoints[0].TargetRef = nil },
			wantErr: "no complete local Pod reference",
		},
		{
			name:    "ready endpoint belongs to another Deployment",
			change:  func(f *fixture) { f.replicaSet.OwnerReferences[0].UID = "other-deployment-uid" },
			wantErr: "is not controlled by Deployment",
		},
		{
			name:    "endpoint Pod references previous ReplicaSet with same name",
			change:  func(f *fixture) { f.replicaSet.UID = "replacement-replica-set-uid" },
			wantErr: "is not controlled by Deployment",
		},
		{
			name: "missing deployment",
			change: func(f *fixture) {
				f.deployment = nil
			},
			wantErr: "get operator Deployment",
		},
		{
			name: "missing webhook configuration",
			change: func(f *fixture) {
				f.webhooks = nil
			},
			wantErr: "get ValidatingWebhookConfiguration",
		},
		{
			name: "configuration does not contain target webhook",
			change: func(f *fixture) {
				f.webhooks.Webhooks[0].Name = "other.example.com"
			},
			wantErr: "webhook validate.example.com not found",
		},
		{
			name: "URL webhook has no service reference",
			change: func(f *fixture) {
				f.webhooks.Webhooks[0].ClientConfig.Service = nil
				f.webhooks.Webhooks[0].ClientConfig.URL = ptr.To("https://example.com/validate")
			},
			wantErr: "has no complete Service reference",
		},
		{
			name: "missing webhook service",
			change: func(f *fixture) {
				f.service = nil
			},
			wantErr: "get webhook Service",
		},
		{
			name: "missing endpoint slices",
			change: func(f *fixture) {
				f.slices = nil
			},
			wantErr: "slices=0 endpoints=0",
		},
		{
			name: "unready endpoint",
			change: func(f *fixture) {
				f.slices[0].Endpoints[0].Conditions.Ready = ptr.To(false)
			},
			wantErr: "unready=1",
		},
		{
			name: "unknown readiness is not ready",
			change: func(f *fixture) {
				f.slices[0].Endpoints[0].Conditions.Ready = nil
			},
			wantErr: "unready=1",
		},
		{
			name: "terminating endpoint is not ready",
			change: func(f *fixture) {
				f.slices[0].Endpoints[0].Conditions.Terminating = ptr.To(true)
			},
			wantErr: "terminating=1",
		},
		{
			name: "endpoint has no address",
			change: func(f *fixture) {
				f.slices[0].Endpoints[0].Addresses = nil
			},
			wantErr: "addressless=1",
		},
		{
			name: "empty endpoint address",
			change: func(f *fixture) {
				f.slices[0].Endpoints[0].Addresses = []string{""}
			},
			wantErr: "addressless=1",
		},
		{
			name: "ready endpoint belongs to another service",
			change: func(f *fixture) {
				f.slices[0].Labels[discoveryv1.LabelServiceName] = "other-service"
			},
			wantErr: "slices=0 endpoints=0",
		},
		{
			name: "one ready endpoint is enough",
			change: func(f *fixture) {
				f.slices[0].Endpoints = append([]discoveryv1.Endpoint{{
					Addresses:  []string{"192.0.2.2"},
					Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(false)},
				}}, f.slices[0].Endpoints...)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := readyFixture(cfg)
			if tt.change != nil {
				tt.change(&f)
			}
			client := fake.NewClientset(f.objects()...)
			err := Check(context.Background(), client, cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected ready resources, got %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
			for _, action := range client.Actions() {
				if action.GetVerb() != "get" && action.GetVerb() != "list" {
					t.Fatalf("readiness check mutated resources: %v", action)
				}
			}
		})
	}
}
