package framework

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OperatorReady requires a serving master from the current Deployment template.
// Unscheduled extra replicas do not block functional tests, but an old ready
// replica or an endpoint belonging to a different Pod cannot satisfy the check.
func OperatorReady(deployment *appsv1.Deployment, replicaSets []appsv1.ReplicaSet, pods []corev1.Pod, slices []discoveryv1.EndpointSlice) bool {
	if deployment == nil || deployment.UID == "" || deployment.DeletionTimestamp != nil ||
		deployment.Spec.Replicas == nil || *deployment.Spec.Replicas <= 0 ||
		deployment.Status.ObservedGeneration < deployment.Generation {
		return false
	}
	for _, rs := range replicaSets {
		if rs.Namespace != deployment.Namespace || rs.UID == "" || rs.DeletionTimestamp != nil ||
			rs.Spec.Replicas == nil || *rs.Spec.Replicas <= 0 || !controlledBy(&rs, deployment, "Deployment") ||
			!sameTemplate(&rs.Spec.Template, &deployment.Spec.Template) {
			continue
		}
		for _, pod := range pods {
			if pod.Namespace != deployment.Namespace || pod.UID == "" || !controlledBy(&pod, &rs, "ReplicaSet") || !readyMaster(&pod) {
				continue
			}
			for _, slice := range slices {
				if slice.Namespace != pod.Namespace {
					continue
				}
				for _, endpoint := range slice.Endpoints {
					ref := endpoint.TargetRef
					if ref != nil && ref.Kind == "Pod" && ref.UID == pod.UID && ref.Name == pod.Name &&
						(ref.Namespace == "" || ref.Namespace == pod.Namespace) && len(endpoint.Addresses) > 0 &&
						endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready &&
						(endpoint.Conditions.Serving == nil || *endpoint.Conditions.Serving) &&
						(endpoint.Conditions.Terminating == nil || !*endpoint.Conditions.Terminating) {
						return true
					}
				}
			}
		}
	}
	return false
}

func controlledBy(object, owner metav1.Object, kind string) bool {
	ref := metav1.GetControllerOf(object)
	return ref != nil && ref.Kind == kind && ref.Name == owner.GetName() && ref.UID == owner.GetUID()
}

func sameTemplate(a, b *corev1.PodTemplateSpec) bool {
	left, right := a.DeepCopy(), b.DeepCopy()
	delete(left.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	delete(right.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	return equality.Semantic.DeepEqual(left, right)
}

func readyMaster(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning ||
		pod.Labels["nsx-operator-role"] != "master" || len(pod.Spec.Containers) == 0 {
		return false
	}
	ready := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			ready = true
		}
	}
	if !ready {
		return false
	}
	for _, container := range pod.Spec.Containers {
		containerReady := false
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == container.Name && status.Ready && status.State.Running != nil {
				containerReady = true
				break
			}
		}
		if !containerReady {
			return false
		}
	}
	return true
}
