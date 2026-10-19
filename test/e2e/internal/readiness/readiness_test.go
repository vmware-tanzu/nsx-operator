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
}

func readyFixture(cfg Config) fixture {
	return fixture{
		deployment: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: cfg.Namespace, Name: cfg.DeploymentName, Generation: 3},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))},
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
			}},
		}},
	}
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
	return objects
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
			name: "old replica is still present during rollout",
			change: func(f *fixture) {
				f.deployment.Status.Replicas = 2
			},
			wantErr: "desired=1 replicas=2",
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
			client := fake.NewSimpleClientset(f.objects()...)
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
