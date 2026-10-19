// Package readiness checks the operator resources required before running e2e tests.
package readiness

import (
	"context"
	"fmt"
	"strings"

	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type Config struct {
	Namespace         string
	DeploymentName    string
	WebhookConfigName string
	WebhookName       string
}

// Check performs one read-only check of the current rollout and webhook endpoints.
// The caller can poll Check, retaining the final error for timeout diagnostics.
func Check(ctx context.Context, kube kubernetes.Interface, cfg Config) error {
	deployment, err := kube.AppsV1().Deployments(cfg.Namespace).Get(ctx, cfg.DeploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get operator Deployment %s/%s: %w", cfg.Namespace, cfg.DeploymentName, err)
	}
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	status := deployment.Status
	if status.ObservedGeneration < deployment.Generation || desired <= 0 ||
		status.Replicas != desired || status.UpdatedReplicas != desired ||
		status.ReadyReplicas != desired || status.AvailableReplicas != desired {
		return fmt.Errorf("Deployment %s/%s rollout incomplete: generation=%d observedGeneration=%d desired=%d replicas=%d updated=%d ready=%d available=%d conditions=%+v",
			cfg.Namespace, cfg.DeploymentName, deployment.Generation, status.ObservedGeneration,
			desired, status.Replicas, status.UpdatedReplicas, status.ReadyReplicas, status.AvailableReplicas, status.Conditions)
	}

	configuration, err := kube.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(ctx, cfg.WebhookConfigName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get ValidatingWebhookConfiguration %s: %w", cfg.WebhookConfigName, err)
	}
	var serviceNamespace, serviceName string
	found := false
	for _, webhook := range configuration.Webhooks {
		if webhook.Name != cfg.WebhookName {
			continue
		}
		found = true
		service := webhook.ClientConfig.Service
		if service == nil || service.Namespace == "" || service.Name == "" {
			return fmt.Errorf("webhook %s in configuration %s has no complete Service reference", cfg.WebhookName, cfg.WebhookConfigName)
		}
		serviceNamespace, serviceName = service.Namespace, service.Name
		break
	}
	if !found {
		return fmt.Errorf("webhook %s not found in configuration %s", cfg.WebhookName, cfg.WebhookConfigName)
	}
	if _, err := kube.CoreV1().Services(serviceNamespace).Get(ctx, serviceName, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("get webhook Service %s/%s: %w", serviceNamespace, serviceName, err)
	}

	selector := labels.Set{discoveryv1.LabelServiceName: serviceName}.AsSelector().String()
	slices, err := kube.DiscoveryV1().EndpointSlices(serviceNamespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("list webhook EndpointSlices for Service %s/%s: %w", serviceNamespace, serviceName, err)
	}
	endpointCount, terminatingCount, unreadyCount, addresslessCount := 0, 0, 0, 0
	for _, slice := range slices.Items {
		for _, endpoint := range slice.Endpoints {
			endpointCount++
			if endpoint.Conditions.Terminating != nil && *endpoint.Conditions.Terminating {
				terminatingCount++
				continue
			}
			if endpoint.Conditions.Ready == nil || !*endpoint.Conditions.Ready {
				unreadyCount++
				continue
			}
			for _, address := range endpoint.Addresses {
				if strings.TrimSpace(address) != "" {
					return nil
				}
			}
			addresslessCount++
		}
	}
	return fmt.Errorf("webhook Service %s/%s has no ready non-terminating endpoint with an address: slices=%d endpoints=%d terminating=%d unready=%d addressless=%d",
		serviceNamespace, serviceName, len(slices.Items), endpointCount, terminatingCount, unreadyCount, addresslessCount)
}
