// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package inventory

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
	"github.com/vmware-tanzu/nsx-operator/test/e2e/framework"
)

var (
	// f is the framework data instance
	f = framework.Data
	// defaultTimeout is the default timeout for operations
	defaultTimeout = framework.DefaultTimeout
	// resourceReadyTime is the time to wait for resources to be ready
	resourceReadyTime = framework.DefaultTimeout
	// containerName is the name of the container to use in tests
	containerName = "nginx"
	// podImage is the image to use for pods in tests
	podImage = "nginx:latest"
)

// TestInventorySync runs all inventory sync tests in parallel.
// Each subtest creates its own resources with unique names to avoid conflicts.
func TestInventorySync(t *testing.T) {
	t.Run("testContainerCluster", func(t *testing.T) {
		t.Parallel()
		testContainerCluster(t)
	})
	t.Run("testNamespaceSync", func(t *testing.T) {
		t.Parallel()
		testNamespaceSync(t)
	})
	t.Run("testPodSync", func(t *testing.T) {
		t.Parallel()
		testPodSync(t)
	})
	t.Run("testServiceSync", func(t *testing.T) {
		t.Parallel()
		testServiceSync(t)
	})
	t.Run("testNodeSync", func(t *testing.T) {
		t.Parallel()
		testNodeSync(t)
	})
	t.Run("testNetworkPolicySync", func(t *testing.T) {
		t.Parallel()
		testNetworkPolicySync(t)
	})
	t.Run("testIngressSync", func(t *testing.T) {
		t.Parallel()
		testIngressSync(t)
	})
}

// testNamespaceSync tests that a Kubernetes namespace is synced to the NSX inventory as a ContainerProject
func testNamespaceSync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Create a namespace with a unique name
	ns := fmt.Sprintf("test-namespace-sync-%s", framework.GetRandomString())

	// Create a VC namespace
	err := f.CreateVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}

	// Wait for the namespace to be synced to the NSX inventory as a ContainerProject
	err = f.WaitForResourceExistOrNot(ns, "ContainerProject", ns, true)
	assert.NoError(t, err, "Namespace was not synced to NSX inventory as ContainerProject")

	// Delete the namespace and verify it's removed from the NSX inventory
	err = f.DeleteVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to delete VC namespace: %v", err)
	}

	// Wait for the namespace to be removed from the NSX inventory
	err = f.WaitForResourceExistOrNot(ns, "ContainerProject", ns, false)
	assert.NoError(t, err, "Namespace was not removed from NSX inventory")
}

// testPodSync tests that a Kubernetes pod is synced to the NSX inventory as a ContainerApplicationInstance
func testPodSync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Create a namespace with a unique name
	ns := fmt.Sprintf("test-pod-sync-%s", framework.GetRandomString())

	// Create a VC namespace
	err := f.CreateVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}
	defer func() {
		err := f.DeleteVCNamespace(ns)
		if err != nil {
			t.Fatalf("Failed to delete VC namespace: %v", err)
		}
	}()

	// Create a pod
	podName := "test-pod"
	_, err = f.CreatePod(ns, podName, containerName, podImage, corev1.ProtocolTCP, 80)
	if err != nil {
		t.Fatalf("Failed to create pod: %v", err)
	}

	// Wait for the pod to be ready
	_, err = f.PodWaitFor(resourceReadyTime, podName, ns, func(pod *corev1.Pod) (bool, error) {
		return pod.Status.Phase == corev1.PodRunning, nil
	})
	if err != nil {
		t.Fatalf("Pod did not become ready: %v", err)
	}

	// Wait for the pod to be synced to the NSX inventory as a ContainerApplicationInstance
	err = f.WaitForResourceExistOrNot(ns, "ContainerApplicationInstance", podName, true)
	assert.NoError(t, err, "Pod was not synced to NSX inventory as ContainerApplicationInstance")

	// Delete the pod
	err = f.ClientSet.CoreV1().Pods(ns).Delete(context.TODO(), podName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete pod: %v", err)
	}

	// Wait for the pod to be removed from the NSX inventory
	err = f.WaitForResourceExistOrNot(ns, "ContainerApplicationInstance", podName, false)
	assert.NoError(t, err, "Pod was not removed from NSX inventory")
}

// testServiceSync tests that a Kubernetes service is synced to the NSX inventory as a ContainerApplication
func testServiceSync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Create a namespace with a unique name
	ns := fmt.Sprintf("test-service-sync-%s", framework.GetRandomString())

	// Create a VC namespace
	err := f.CreateVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}
	defer func() {
		err := f.DeleteVCNamespace(ns)
		if err != nil {
			t.Fatalf("Failed to delete VC namespace: %v", err)
		}
	}()

	// Create a service
	serviceName := "test-service"
	port := int32(80)
	targetPort := int32(8080)
	selector := map[string]string{"app": "test"}

	_, err = f.CreateService(ns, serviceName, port, targetPort, corev1.ProtocolTCP, selector, corev1.ServiceTypeClusterIP)
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	// Wait for the service to be synced to the NSX inventory as a ContainerApplication
	err = f.WaitForResourceExistOrNot(ns, "ContainerApplication", serviceName, true)
	assert.NoError(t, err, "Service was not synced to NSX inventory as ContainerApplication")

	// Delete the service
	err = f.ClientSet.CoreV1().Services(ns).Delete(context.TODO(), serviceName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete service: %v", err)
	}

	// Wait for the service to be removed from the NSX inventory
	err = f.WaitForResourceExistOrNot(ns, "ContainerApplication", serviceName, false)
	assert.NoError(t, err, "Service was not removed from NSX inventory")
}

// testNodeSync tests that a Kubernetes node is synced to the NSX inventory as a ContainerClusterNode
func testNodeSync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Get a list of nodes
	nodes, err := f.ClientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed to list nodes: %v", err)
	}
	if len(nodes.Items) == 0 {
		t.Fatalf("No nodes found in the cluster")
	}

	// Pick the first node
	node := nodes.Items[0]
	nodeName := node.Name

	// Verify the node is synced to the NSX inventory as a ContainerClusterNode
	// Note: We don't need to create a node as they already exist in the cluster
	err = f.WaitForResourceExistOrNot("", "ContainerClusterNode", nodeName, true)
	assert.NoError(t, err, "Node was not synced to NSX inventory as ContainerClusterNode")

	// We don't delete the node as it's part of the cluster
}

// testNetworkPolicySync tests that a Kubernetes NetworkPolicy is synced to the NSX inventory as a ContainerNetworkPolicy
func testNetworkPolicySync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Create a namespace with a unique name
	ns := fmt.Sprintf("test-netpol-sync-%s", framework.GetRandomString())

	// Create a VC namespace
	err := f.CreateVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}
	defer func() {
		err := f.DeleteVCNamespace(ns)
		if err != nil {
			t.Fatalf("Failed to delete VC namespace: %v", err)
		}
	}()

	// Create a NetworkPolicy
	policyName := "test-network-policy"

	// Define a simple NetworkPolicy that denies all ingress traffic
	networkPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policyName,
			Namespace: ns,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{},
		},
	}

	_, err = f.ClientSet.NetworkingV1().NetworkPolicies(ns).Create(context.TODO(), networkPolicy, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create NetworkPolicy: %v", err)
	}

	// Wait for the NetworkPolicy to be synced to the NSX inventory as a ContainerNetworkPolicy
	err = f.WaitForResourceExistOrNot(ns, "ContainerNetworkPolicy", policyName, true)
	assert.NoError(t, err, "NetworkPolicy was not synced to NSX inventory as ContainerNetworkPolicy")

	// Delete the NetworkPolicy
	err = f.ClientSet.NetworkingV1().NetworkPolicies(ns).Delete(context.TODO(), policyName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete NetworkPolicy: %v", err)
	}

	// Wait for the NetworkPolicy to be removed from the NSX inventory
	err = f.WaitForResourceExistOrNot(ns, "ContainerNetworkPolicy", policyName, false)
	assert.NoError(t, err, "NetworkPolicy was not removed from NSX inventory")
}

// testIngressSync tests that a Kubernetes Ingress is synced to the NSX inventory as a ContainerIngressPolicy
func testIngressSync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Create a namespace with a unique name
	ns := fmt.Sprintf("test-ingress-sync-%s", framework.GetRandomString())

	// Create a VC namespace
	err := f.CreateVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}
	defer func() {
		err := f.DeleteVCNamespace(ns)
		if err != nil {
			t.Fatalf("Failed to delete VC namespace: %v", err)
		}
	}()

	// Create a service first (ingress needs a backend service)
	serviceName := "test-service"
	port := int32(80)
	targetPort := int32(8080)
	selector := map[string]string{"app": "test"}

	_, err = f.CreateService(ns, serviceName, port, targetPort, corev1.ProtocolTCP, selector, corev1.ServiceTypeClusterIP)
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	// Create an Ingress
	ingressName := "test-ingress"
	pathType := networkingv1.PathTypePrefix

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ingressName,
			Namespace: ns,
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "test.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingv1.ServiceBackendPort{
												Number: port,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err = f.ClientSet.NetworkingV1().Ingresses(ns).Create(context.TODO(), ingress, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create Ingress: %v", err)
	}

	// Wait for the Ingress to be synced to the NSX inventory as a ContainerIngressPolicy
	err = f.WaitForResourceExistOrNot(ns, "ContainerIngressPolicy", ingressName, true)
	assert.NoError(t, err, "Ingress was not synced to NSX inventory as ContainerIngressPolicy")

	// Delete the Ingress
	err = f.ClientSet.NetworkingV1().Ingresses(ns).Delete(context.TODO(), ingressName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete Ingress: %v", err)
	}

	// Wait for the Ingress to be removed from the NSX inventory
	err = f.WaitForResourceExistOrNot(ns, "ContainerIngressPolicy", ingressName, false)
	assert.NoError(t, err, "Ingress was not removed from NSX inventory")

	// Clean up the service
	err = f.ClientSet.CoreV1().Services(ns).Delete(context.TODO(), serviceName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete service: %v", err)
	}
}

// testContainerCluster tests that the Kubernetes cluster is synced to the NSX inventory as a ContainerCluster
func testContainerCluster(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Get the NSX operator config to access the cluster name
	cf, err := config.NewNSXOperatorConfigFromFile()
	if err != nil {
		t.Fatalf("Failed to get NSX operator config: %v", err)
	}

	// Get the cluster name from the config
	clusterName := cf.CoeConfig.Cluster

	// Get the cluster UUID which is used as the external_id in NSX
	clusterUUID := util.GetClusterUUID(clusterName).String()

	// Verify the cluster is synced to the NSX inventory as a ContainerCluster
	// Use external_id instead of display_name for the query
	err = f.WaitForResourceExist("", "ContainerCluster", "external_id", clusterUUID, true)
	assert.NoError(t, err, "Cluster was not synced to NSX inventory as ContainerCluster")

	// We don't delete the cluster as it's the environment we're testing in
}
