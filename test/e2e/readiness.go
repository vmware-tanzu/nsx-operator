package e2e

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	testframework "github.com/vmware-tanzu/nsx-operator/test/framework"
)

// Verify a current WCP master and its webhook backend before creating test
// resources. Extra replicas may be unschedulable on a single control-plane node.
func (data *TestData) waitForOperatorReady() error {
	if !data.useWCPSetup() {
		return nil
	}
	const namespace = "vmware-system-nsx"
	var lastLog time.Time
	return pollForCondition(podStartupTimeout, func(ctx context.Context) (bool, error) {
		deployment, err := data.clientset.AppsV1().Deployments(namespace).Get(ctx, "nsx-ncp", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
		if err != nil {
			return false, err
		}
		options := metav1.ListOptions{LabelSelector: selector.String()}
		replicaSets, err := data.clientset.AppsV1().ReplicaSets(namespace).List(ctx, options)
		if err != nil {
			return false, err
		}
		pods, err := data.clientset.CoreV1().Pods(namespace).List(ctx, options)
		if err != nil {
			return false, err
		}
		slices, err := data.clientset.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "kubernetes.io/service-name=vmware-system-nsx-operator-webhook-service",
		})
		if err != nil {
			return false, err
		}
		if testframework.OperatorReady(deployment, replicaSets.Items, pods.Items, slices.Items) {
			return true, nil
		}
		if time.Since(lastLog) >= 15*time.Second {
			log.Info("Waiting for current operator master and webhook backend", "generation", deployment.Generation,
				"observedGeneration", deployment.Status.ObservedGeneration, "readyReplicas", deployment.Status.ReadyReplicas)
			lastLog = time.Now()
		}
		return false, nil
	})
}
