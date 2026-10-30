package e2e

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	testframework "github.com/vmware-tanzu/nsx-operator/test/framework"
)

// Verify a WCP rollout before creating test resources. This only observes the
// deployment and webhook backends; it never restarts or reconfigures them.
func (data *TestData) waitForOperatorReady() error {
	if !data.useWCPSetup() {
		return nil
	}
	const namespace = "vmware-system-nsx"
	return pollForCondition(podStartupTimeout, func(ctx context.Context) (bool, error) {
		deployment, err := data.clientset.AppsV1().Deployments(namespace).Get(ctx, "nsx-ncp", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !testframework.DeploymentReady(deployment) {
			log.Info("Waiting for operator rollout", "generation", deployment.Generation, "status", deployment.Status)
			return false, nil
		}
		slices, err := data.clientset.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "kubernetes.io/service-name=vmware-system-nsx-operator-webhook-service",
		})
		if err != nil {
			return false, err
		}
		for _, slice := range slices.Items {
			for _, endpoint := range slice.Endpoints {
				if endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready && len(endpoint.Addresses) > 0 {
					return true, nil
				}
			}
		}
		log.Info("Waiting for ready operator webhook backend")
		return false, nil
	})
}
