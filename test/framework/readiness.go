package framework

import appsv1 "k8s.io/api/apps/v1"

// DeploymentReady requires the current generation and all requested replicas.
// A ready Pod from the previous rollout cannot satisfy this check.
func DeploymentReady(deployment *appsv1.Deployment) bool {
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas == 0 || deployment.DeletionTimestamp != nil {
		return false
	}
	desired := *deployment.Spec.Replicas
	status := deployment.Status
	return status.ObservedGeneration >= deployment.Generation &&
		status.UpdatedReplicas == desired && status.Replicas == desired &&
		status.ReadyReplicas == desired && status.AvailableReplicas == desired
}
