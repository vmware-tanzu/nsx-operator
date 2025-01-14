// This file is for e2e security policy tests, these tests abide by consistent flow.
// Create namespace -> Create pods -> Create security policy -> Check nsx-t resource existing
// -> Ping or nc from pod -> Delete security policy -> Check nsx-t resource not existing -> Ping or nc from pod -> Delete pods
// -> Delete security policy(defer function to avoid garbage residue) -> Delete namespace(all pods in namespace will be deleted)
// Note that there are some points to consider:
// 1. Each test run in a separate namespace, and the namespace should be deleted after the test.
// 2. Deleting a namespace will delete all pods in the namespace, but the security policy will reside,
//    so basically `defer deleteYAML` should follow `applyYAML`.
// 3. Self-defined assert function is in order to panic when error occurs, and the remained tests will be skipped.
// 4. Each test case maintains idempotency, regardless of whether the case ends successfully or quits halfway,
//    it does not affect the next execution. So we should carefully consider the garbage residue problem.
// 5. It is designed to run in sequence, parallel execution has not been tested.
// 6. There is time delay at nsx-t side, so we need to wait for the resource to be ready or deleted.

package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	timeInterval = 1 * time.Second
	timeOut10    = 10 * time.Second
	timeOut5     = 5 * time.Second
)

func TestSecurityPolicy(t *testing.T) {
	t.Run("testSecurityPolicyBasicTraffic", func(t *testing.T) { testSecurityPolicyBasicTraffic(t) })
	return
	t.Run("testSecurityPolicyAddDeleteRule", func(t *testing.T) { testSecurityPolicyAddDeleteRule(t) })
	t.Run("testSecurityPolicyMatchExpression", func(t *testing.T) { testSecurityPolicyMatchExpression(t) })
	t.Run("testSecurityPolicyNamedPortWithoutPod", func(t *testing.T) { testSecurityPolicyNamedPortWithoutPod(t) })
}

// TestSecurityPolicyBasicTraffic verifies that the basic traffic of security policy.
// This is the very basic, blocking all in and out traffic between pods should take effect.
func testSecurityPolicyBasicTraffic(t *testing.T) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	ns := "test-security-policy-1"
	securityPolicyName := "isolate-policy-1"
	ruleName0 := "all_ingress_isolation"
	ruleName1 := "all_egress_isolation"

	err := testData.createVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}
	defer func() {
		err := testData.deleteVCNamespace(ns)
		if err != nil {
			t.Fatalf("Failed to delete VC namespace: %v", err)
		}
	}()

	// Create pods
	clientPodName := "client-pod"
	serverPodName := "server-pod"
	_, err = testData.createPod(ns, clientPodName, containerName, podImage, corev1.ProtocolTCP, podPort)
	require.NoErrorf(t, err, "Client Pod '%s/%s' should be created", ns, clientPodName)
	_, err = testData.createPod(ns, serverPodName, containerName, podImage, corev1.ProtocolTCP, podPort)
	require.NoErrorf(t, err, "Server Pod '%s/%s' should be created", ns, serverPodName)
	_, err = testData.podWaitForIPs(resourceReadyTime, clientPodName, ns)
	require.NoErrorf(t, err, "Client Pod '%s/%s' is not ready within time %s", ns, clientPodName, resourceReadyTime.String())
	iPs, err := testData.podWaitForIPs(resourceReadyTime, serverPodName, ns)
	require.NoErrorf(t, err, "Server Pod '%s/%s' is not ready within time %s", ns, serverPodName, resourceReadyTime.String())
	log.Info("Server Pod in the Namespace is ready", "Namespace", ns, "Pod", serverPodName)

	// Test traffic from client Pod to server Pod
	trafficErr := checkTrafficByCurl(ns, clientPodName, containerName, iPs.ipv4.String(), podPort, timeInterval, timeOut10)
	require.NoError(t, trafficErr, "Basic traffic should work")
	log.Info("Verified traffic from client Pod to the server Pod")

	// Create security policy
	nsIsolationPath, _ := filepath.Abs("./manifest/testSecurityPolicy/ns-isolation-policy.yaml")
	require.NoError(t, applyYAML(nsIsolationPath, ns))
	defer deleteYAML(nsIsolationPath, ns)
	assureSecurityPolicyReady(t, ns, securityPolicyName)

	// Check nsx-t resource existing
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, true))
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, true))
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, true))

	// Test traffic from client Pod to server Pod
	trafficErr = checkTrafficByCurl(ns, clientPodName, containerName, iPs.ipv4.String(), podPort, timeInterval, timeOut5)
	require.Error(t, trafficErr, "Basic traffic should not work")
	log.Info("Verified traffic from client Pod to the server Pod")

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)
	err = wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(ctx, securityPolicyName, v1.GetOptions{})
		log.V(2).Info("Check resource", "resp", resp)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for Security Policy %s", securityPolicyName)
		}
		return false, nil
	})
	require.NoError(t, err)

	// Check nsx-t resource not existing
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, false))
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, false))
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, false))

	// Test traffic from client Pod to server Pod
	trafficErr = checkTrafficByCurl(ns, clientPodName, containerName, iPs.ipv4.String(), podPort, timeInterval, timeOut10)
	require.NoError(t, trafficErr, "Basic traffic should work")
	log.Info("Verified traffic from client Pod to the server Pod")
}

// TestSecurityPolicyAddDeleteRule verifies that when adding or deleting rule, the security policy will be updated.
// This is once a bug which is fixed later. When adding or deleting rule of one security policy repeatedly,
// the nsx-t side should keep consistent.
func testSecurityPolicyAddDeleteRule(t *testing.T) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	ns := "test-security-policy-2"
	securityPolicyName := "isolate-policy-1"
	ruleName0 := "all_ingress_isolation"
	ruleName1 := "all_egress_isolation"
	setupTest(t, ns)
	defer teardownTest(t, ns, defaultTimeout)

	// Create security policy
	nsIsolationPath, _ := filepath.Abs("./manifest/testSecurityPolicy/ns-isolation-policy.yaml")
	require.NoError(t, applyYAML(nsIsolationPath, ns))
	defer deleteYAML(nsIsolationPath, ns)
	assureSecurityPolicyReady(t, ns, securityPolicyName)

	// Check nsx-t resource existing
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, true))
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, true))
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, true))

	// Update security policy
	nsIsolationPath, _ = filepath.Abs("./manifest/testSecurityPolicy/ns-isolation-policy-1.yaml")
	require.NoError(t, applyYAML(nsIsolationPath, ns))
	defer deleteYAML(nsIsolationPath, ns)
	assureSecurityPolicyReady(t, ns, securityPolicyName)

	// Check nsx-t resource existing
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, true))
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, false))

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)

	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(ctx, securityPolicyName, v1.GetOptions{})
		log.V(2).Info("Check resource", "resp", resp)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for Security Policy %s", securityPolicyName)
		}
		return false, nil
	})
	require.NoError(t, err)

	// Check nsx-t resource not existing
	require.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, false))
}

// TestSecurityPolicyMatchExpression verifies that the traffic of security policy when match expression applied.
// This test is to verify the match expression, NotIn/In operator feature of security policy. It should apply
// to the specified pod.
func testSecurityPolicyMatchExpression(t *testing.T) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	ns := "test-security-policy-match-expression"
	securityPolicyName := "expression-policy-1"
	ruleName := "expression-policy-1-rule"

	err := testData.createVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}
	defer func() {
		err := testData.deleteVCNamespace(ns)
		if err != nil {
			t.Fatalf("Failed to delete VC namespace: %v", err)
		}
	}()

	// Create pods
	podPath, _ := filepath.Abs("./manifest/testSecurityPolicy/allow-client-a-via-pod-selector-with-match-expressions.yaml")
	require.NoError(t, applyYAML(podPath, ns))
	defer deleteYAML(podPath, "")

	clientA := "client-a"
	clientB := "client-b"
	podA := "pod-a"
	// Wait for pods
	_, err = testData.podWaitForIPs(defaultTimeout, clientA, ns)
	assert.NoError(t, err, "Error when waiting for IP for Pod %s", clientA)
	_, err = testData.podWaitForIPs(defaultTimeout, clientB, ns)
	assert.NoError(t, err, "Error when waiting for IP for Pod %s", clientB)
	iPs, err := testData.podWaitForIPs(defaultTimeout, podA, ns)
	assert.NoError(t, err, "Error when waiting for IP for Pod %s", podA)

	// Test traffic from clientA to podA
	trafficErr := checkTrafficByCurl(ns, clientA, clientA, iPs.ipv4.String(), podPort, timeInterval, timeOut10)
	require.NoError(t, trafficErr, "TestSecurityPolicyMatchExpression traffic should work")
	log.Info("Verified traffic from client Pod to PodA")
	// Test traffic from clientB to podA
	trafficErr = checkTrafficByCurl(ns, clientB, clientB, iPs.ipv4.String(), podPort, timeInterval, timeOut10)
	require.NoError(t, trafficErr, "TestSecurityPolicyMatchExpression traffic should work")
	log.Info("Verified traffic from client Pod to PodB")

	// Create security policy
	nsIsolationPath, _ := filepath.Abs("./manifest/testSecurityPolicy/match-expression.yaml")
	require.NoError(t, applyYAML(nsIsolationPath, ns))
	defer deleteYAML(nsIsolationPath, ns)
	assureSecurityPolicyReady(t, ns, securityPolicyName)

	// Check nsx-t resource existing
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, true))
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName, true))

	// Test traffic from clientA to podA
	trafficErr = checkTrafficByCurl(ns, clientA, clientA, iPs.ipv4.String(), podPort, timeInterval, timeOut10)
	require.NoError(t, trafficErr, "TestSecurityPolicyMatchExpression traffic should work")
	log.Info("Verified traffic from client Pod to PodA")
	// Test traffic from clientB to podA
	trafficErr = checkTrafficByCurl(ns, clientB, clientB, iPs.ipv4.String(), podPort, timeInterval, timeOut5)
	require.Error(t, trafficErr, "TestSecurityPolicyMatchExpression traffic should not work")
	log.Info("Verified traffic from client Pod to PodB")

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)

	err = wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(ctx, securityPolicyName, v1.GetOptions{})
		log.V(2).Info("Check resource", "resp", resp)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for Security Policy %s", securityPolicyName)
		}
		return false, nil
	})
	require.NoError(t, err)

	// Check nsx-t resource not existing
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, false))
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName, false))

	// Test traffic from clientA to podA
	trafficErr = checkTrafficByCurl(ns, clientA, clientA, iPs.ipv4.String(), podPort, timeInterval, timeOut10)
	require.NoError(t, trafficErr, "TestSecurityPolicyMatchExpression traffic should work")
	log.Info("Verified traffic from client Pod to PodA")
	// Test traffic from clientB to podA
	trafficErr = checkTrafficByCurl(ns, clientB, clientB, iPs.ipv4.String(), podPort, timeInterval, timeOut10)
	require.NoError(t, trafficErr, "TestSecurityPolicyMatchExpression traffic should work")
	log.Info("Verified traffic from client Pod to PodB")
}

// TestSecurityPolicyNamedPortWithoutPod verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in policy level and there's no pod holding the related named ports.
func testSecurityPolicyNamedPortWithoutPod(t *testing.T) {
	nsClient := "client"
	nsWeb := "web"
	securityPolicyCRName := "named-port-policy-without-pod"
	webA := "web"
	labelWeb := "tcp-deployment"
	ruleName0 := "all_ingress_isolation"
	ruleName1 := "all_egress_isolation"

	testData.deleteNamespace(nsClient, defaultTimeout)
	testData.deleteNamespace(nsWeb, defaultTimeout)
	_ = testData.createNamespace(nsClient)
	_ = testData.createNamespace(nsWeb)
	defer testData.deleteNamespace(nsClient, defaultTimeout)
	defer testData.deleteNamespace(nsWeb, defaultTimeout)

	// Create all
	yamlPath, _ := filepath.Abs("./manifest/testSecurityPolicy/named-port-without-pod.yaml")
	require.NoError(t, applyYAML(yamlPath, ""))
	defer deleteYAML(yamlPath, "")

	psb, err := testData.deploymentWaitForNames(defaultTimeout, nsWeb, labelWeb)
	log.V(2).Info("Pods", "pods", psb)
	assert.NoError(t, err, "Error when waiting for IP for Pod %s", webA)
	assureSecurityPolicyReady(t, nsWeb, securityPolicyCRName)

	// Check NSX resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyCRName, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName0, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName1, true)
	assert.NoError(t, err)
}

func assureSecurityPolicyReady(t *testing.T, ns, spName string) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, timeInterval, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(context.Background(), spName, v1.GetOptions{})
		log.V(2).Info("Get resources", "SecurityPolicies", resp, "Namespace", ns, "Name", spName)
		if err != nil {
			return false, fmt.Errorf("error when waiting for  %s", spName)
		}
		for _, con := range resp.Status.Conditions {
			if con.Type == v1alpha1.Ready {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err)
}
