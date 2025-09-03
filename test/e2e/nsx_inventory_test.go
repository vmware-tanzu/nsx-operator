package e2e

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
)

func TestInventorySync(t *testing.T) {
	t.Run("testContainerCluster", func(t *testing.T) { testContainerCluster(t) })
	t.Run("testNamespaceSync", func(t *testing.T) { testNamespaceSync(t) })
	t.Run("testPodSync", func(t *testing.T) { testPodSync(t) })
	t.Run("testServiceSync", func(t *testing.T) { testServiceSync(t) })
	t.Run("testNodeSync", func(t *testing.T) { testNodeSync(t) })
	t.Run("testNetworkPolicySync", func(t *testing.T) { testNetworkPolicySync(t) })
	t.Run("testIngressSync", func(t *testing.T) { testIngressSync(t) })
}

// testNamespaceSync tests that a Kubernetes namespace is synced to the NSX inventory as a ContainerProject
func testNamespaceSync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Create a namespace with a unique name
	ns := fmt.Sprintf("test-namespace-sync-%s", getRandomString())

	err := testData.createVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}

	// Wait for the namespace to be synced to the NSX inventory as a ContainerProject
	err = testData.waitForResourceExistOrNot(ns, "ContainerProject", ns, true)
	assert.NoError(t, err, "Namespace was not synced to NSX inventory as ContainerProject")

	// Delete the namespace and verify it's removed from the NSX inventory
	err = testData.deleteVCNamespace(ns, defaultTimeout)
	if err != nil {
		t.Fatalf("Failed to delete VC namespace: %v", err)
	}

	// Wait for the namespace to be removed from the NSX inventory
	err = testData.waitForResourceExistOrNot(ns, "ContainerProject", ns, false)
	assert.NoError(t, err, "Namespace was not removed from NSX inventory")
}

// testPodSync tests that a Kubernetes pod is synced to the NSX inventory as a ContainerApplicationInstance
func testPodSync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Create a namespace with a unique name
	ns := fmt.Sprintf("test-pod-sync-%s", getRandomString())

	err := testData.createVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}
	defer func() {
		err := testData.deleteVCNamespace(ns, defaultTimeout)
		if err != nil {
			t.Fatalf("Failed to delete VC namespace: %v", err)
		}
	}()

	// Create a pod
	podName := fmt.Sprintf("test-pod-%s", getRandomString())
	_, err = testData.createPod(ns, podName, containerName, podImage, corev1.ProtocolTCP, 80)
	if err != nil {
		t.Fatalf("Failed to create pod: %v", err)
	}

	// Wait for the pod to be ready
	_, err = testData.podWaitFor(resourceReadyTime, podName, ns, func(pod *corev1.Pod) (bool, error) {
		return pod.Status.Phase == corev1.PodRunning, nil
	})
	if err != nil {
		t.Fatalf("Pod did not become ready: %v", err)
	}

	// Wait for the pod to be synced to the NSX inventory as a ContainerApplicationInstance
	err = testData.waitForResourceExistOrNot(ns, "ContainerApplicationInstance", podName, true)
	assert.NoError(t, err, "Pod was not synced to NSX inventory as ContainerApplicationInstance")

	// Delete the pod
	err = testData.clientset.CoreV1().Pods(ns).Delete(context.TODO(), podName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete pod: %v", err)
	}

	// Wait for the pod to be removed from the NSX inventory
	err = testData.waitForResourceExistOrNot(ns, "ContainerApplicationInstance", podName, false)
	assert.NoError(t, err, "Pod was not removed from NSX inventory")
}

// testServiceSync tests that a Kubernetes service is synced to the NSX inventory as a ContainerApplication
func testServiceSync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Create a namespace with a unique name
	ns := fmt.Sprintf("test-service-sync-%s", getRandomString())

	err := testData.createVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}
	defer func() {
		err := testData.deleteVCNamespace(ns, defaultTimeout)
		if err != nil {
			t.Fatalf("Failed to delete VC namespace: %v", err)
		}
	}()

	// Create a service
	serviceName := fmt.Sprintf("test-service-%s", getRandomString())
	port := int32(80)
	targetPort := int32(8080)
	selector := map[string]string{"app": "test"}

	_, err = testData.createService(ns, serviceName, port, targetPort, corev1.ProtocolTCP, selector, corev1.ServiceTypeClusterIP)
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	// Wait for the service to be synced to the NSX inventory as a ContainerApplication
	err = testData.waitForResourceExistOrNot(ns, "ContainerApplication", serviceName, true)
	assert.NoError(t, err, "Service was not synced to NSX inventory as ContainerApplication")

	// Delete the service
	err = testData.clientset.CoreV1().Services(ns).Delete(context.TODO(), serviceName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete service: %v", err)
	}

	// Wait for the service to be removed from the NSX inventory
	err = testData.waitForResourceExistOrNot(ns, "ContainerApplication", serviceName, false)
	assert.NoError(t, err, "Service was not removed from NSX inventory")
}

// testNodeSync tests that a Kubernetes node is synced to the NSX inventory as a ContainerClusterNode
func testNodeSync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Get a list of nodes
	nodes, err := testData.clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
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
	err = testData.waitForResourceExistOrNot("", "ContainerClusterNode", nodeName, true)
	assert.NoError(t, err, "Node was not synced to NSX inventory as ContainerClusterNode")

	// We don't delete the node as it's part of the cluster
}

// testNetworkPolicySync tests that a Kubernetes NetworkPolicy is synced to the NSX inventory as a ContainerNetworkPolicy
func testNetworkPolicySync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Create a namespace with a unique name
	ns := fmt.Sprintf("test-netpol-sync-%s", getRandomString())

	err := testData.createVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}
	defer func() {
		err := testData.deleteVCNamespace(ns, defaultTimeout)
		if err != nil {
			t.Fatalf("Failed to delete VC namespace: %v", err)
		}
	}()

	// Create a NetworkPolicy
	policyName := fmt.Sprintf("test-network-policy-%s", getRandomString())

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

	_, err = testData.clientset.NetworkingV1().NetworkPolicies(ns).Create(context.TODO(), networkPolicy, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create NetworkPolicy: %v", err)
	}

	// Wait for the NetworkPolicy to be synced to the NSX inventory as a ContainerNetworkPolicy
	err = testData.waitForResourceExistOrNot(ns, "ContainerNetworkPolicy", policyName, true)
	assert.NoError(t, err, "NetworkPolicy was not synced to NSX inventory as ContainerNetworkPolicy")

	// Delete the NetworkPolicy
	err = testData.clientset.NetworkingV1().NetworkPolicies(ns).Delete(context.TODO(), policyName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete NetworkPolicy: %v", err)
	}

	// Wait for the NetworkPolicy to be removed from the NSX inventory
	err = testData.waitForResourceExistOrNot(ns, "ContainerNetworkPolicy", policyName, false)
	assert.NoError(t, err, "NetworkPolicy was not removed from NSX inventory")
}

// testIngressSync tests that a Kubernetes Ingress is synced to the NSX inventory as a ContainerIngressPolicy
func testIngressSync(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Create a namespace with a unique name
	ns := fmt.Sprintf("test-ingress-sync-%s", getRandomString())

	err := testData.createVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}
	defer func() {
		err := testData.deleteVCNamespace(ns, defaultTimeout)
		if err != nil {
			t.Fatalf("Failed to delete VC namespace: %v", err)
		}
	}()

	// Create a service first (ingress needs a backend service)
	serviceName := fmt.Sprintf("test-service-%s", getRandomString())
	port := int32(80)
	targetPort := int32(8080)
	selector := map[string]string{"app": "test"}

	_, err = testData.createService(ns, serviceName, port, targetPort, corev1.ProtocolTCP, selector, corev1.ServiceTypeClusterIP)
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	// Create an Ingress
	ingressName := fmt.Sprintf("test-ingress-%s", getRandomString())
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

	_, err = testData.clientset.NetworkingV1().Ingresses(ns).Create(context.TODO(), ingress, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create Ingress: %v", err)
	}

	// Wait for the Ingress to be synced to the NSX inventory as a ContainerIngressPolicy
	err = testData.waitForResourceExistOrNot(ns, "ContainerIngressPolicy", ingressName, true)
	assert.NoError(t, err, "Ingress was not synced to NSX inventory as ContainerIngressPolicy")

	// Delete the Ingress
	err = testData.clientset.NetworkingV1().Ingresses(ns).Delete(context.TODO(), ingressName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete Ingress: %v", err)
	}

	// Wait for the Ingress to be removed from the NSX inventory
	err = testData.waitForResourceExistOrNot(ns, "ContainerIngressPolicy", ingressName, false)
	assert.NoError(t, err, "Ingress was not removed from NSX inventory")

	// Clean up the service
	err = testData.clientset.CoreV1().Services(ns).Delete(context.TODO(), serviceName, metav1.DeleteOptions{})
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
	err = testData.waitForResourceExist("", "ContainerCluster", "external_id", clusterUUID, true)
	assert.NoError(t, err, "Cluster was not synced to NSX inventory as ContainerCluster")

	// We don't delete the cluster as it's the environment we're testing in
}
