// Package readiness checks the operator resources required before running e2e tests.
package readiness

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
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

// Check verifies that the current operator template serves the webhook.
// A ready active replica is sufficient; an unschedulable standby must not block e2e.
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
		status.UpdatedReplicas < 1 || status.ReadyReplicas < 1 || status.AvailableReplicas < 1 {
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
	endpointCount, terminatingCount, unreadyCount, addresslessCount, readyCount := 0, 0, 0, 0, 0
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
			hasAddress := false
			for _, address := range endpoint.Addresses {
				if strings.TrimSpace(address) != "" {
					hasAddress = true
					break
				}
			}
			if !hasAddress {
				addresslessCount++
				continue
			}
			// Check every routable endpoint, so a ready old master cannot be
			// accepted alongside a replica from the current template.
			if err := checkEndpointPod(ctx, kube, deployment, serviceNamespace, endpoint); err != nil {
				return fmt.Errorf("webhook Service %s/%s endpoint is not served by the current operator: %w", serviceNamespace, serviceName, err)
			}
			readyCount++
		}
	}
	if readyCount > 0 {
		return nil
	}

	return fmt.Errorf("webhook Service %s/%s has no ready non-terminating endpoint with an address: slices=%d endpoints=%d terminating=%d unready=%d addressless=%d",
		serviceNamespace, serviceName, len(slices.Items), endpointCount, terminatingCount, unreadyCount, addresslessCount)
}

// checkEndpointPod follows controller UIDs rather than trusting deployment-wide
// ready counts, which can describe an old replica during a rolling update.
func checkEndpointPod(ctx context.Context, kube kubernetes.Interface, deployment *appsv1.Deployment, namespace string, endpoint discoveryv1.Endpoint) error {
	ref := endpoint.TargetRef
	if ref == nil || ref.Kind != "Pod" || ref.Name == "" || ref.UID == "" || (ref.Namespace != "" && ref.Namespace != namespace) {
		return fmt.Errorf("endpoint has no complete local Pod reference: %+v", ref)
	}
	if namespace != deployment.Namespace {
		return fmt.Errorf("endpoint namespace %s differs from Deployment namespace %s", namespace, deployment.Namespace)
	}
	pod, err := kube.CoreV1().Pods(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get endpoint Pod %s/%s: %w", namespace, ref.Name, err)
	}
	if pod.UID != ref.UID || pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
		return fmt.Errorf("endpoint Pod %s/%s is stale, terminating or not running", namespace, ref.Name)
	}
	ready := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			ready = true
			break
		}
	}
	if !ready {
		return fmt.Errorf("endpoint Pod %s/%s is not Ready", namespace, ref.Name)
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.Kind != "ReplicaSet" || owner.Name == "" || owner.UID == "" {
		return fmt.Errorf("endpoint Pod %s/%s has no ReplicaSet controller", namespace, ref.Name)
	}
	replicaSet, err := kube.AppsV1().ReplicaSets(namespace).Get(ctx, owner.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get endpoint ReplicaSet %s/%s: %w", namespace, owner.Name, err)
	}
	deploymentOwner := metav1.GetControllerOf(replicaSet)
	if replicaSet.UID != owner.UID || replicaSet.DeletionTimestamp != nil || deploymentOwner == nil ||
		deploymentOwner.Kind != "Deployment" || deploymentOwner.UID != deployment.UID || deploymentOwner.Name != deployment.Name {
		return fmt.Errorf("endpoint ReplicaSet %s/%s is not controlled by Deployment %s/%s", namespace, owner.Name, deployment.Namespace, deployment.Name)
	}
	const revisionAnnotation = "deployment.kubernetes.io/revision"
	revision := deployment.Annotations[revisionAnnotation]
	if revision == "" || replicaSet.Annotations[revisionAnnotation] != revision {
		return fmt.Errorf("endpoint ReplicaSet %s/%s revision=%q differs from current Deployment revision=%q", namespace, owner.Name, replicaSet.Annotations[revisionAnnotation], revision)
	}
	// The Deployment controller adds this label when creating a ReplicaSet.
	// Compare everything else, including containers and template annotations.
	template := replicaSet.Spec.Template.DeepCopy()
	delete(template.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	currentTemplate := deployment.Spec.Template.DeepCopy()
	delete(currentTemplate.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	if !equality.Semantic.DeepEqual(template, currentTemplate) {
		return fmt.Errorf("endpoint ReplicaSet %s/%s has an older Pod template", namespace, owner.Name)
	}
	return nil
}
