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

func TestSecurityPolicy(t *testing.T) {
	TrackTest(t)

	// Clean up shared namespaces when all subtests complete
	t.Cleanup(func() {
		CleanupVCNamespaces(NsSecurityPolicy, NsSecurityPolicyNamedPortClient, NsSecurityPolicyNamedPortWeb)
	})
	StartParallel(t)

	// SequentialTests: All subtests share NsSecurityPolicy namespace and some share "isolate-policy-1"
	// so they MUST run sequentially to avoid conflicts
	RunSubtest(t, "SequentialTests", func(t *testing.T) {
		RunSubtest(t, "testSecurityPolicyBasicTraffic", func(t *testing.T) { testSecurityPolicyBasicTraffic(t) })
		RunSubtest(t, "testSecurityPolicyAddDeleteRule", func(t *testing.T) { testSecurityPolicyAddDeleteRule(t) })
		RunSubtest(t, "testSecurityPolicyMatchExpression", func(t *testing.T) { testSecurityPolicyMatchExpression(t) })
		RunSubtest(t, "testSecurityPolicyNamedPortWithoutPod", func(t *testing.T) { testSecurityPolicyNamedPortWithoutPod(t) })
		RunSubtest(t, "testSecurityPolicyNamedPorWithPod", func(t *testing.T) { testSecurityPolicyNamedPorWithPod(t) })
	})
}

// TestSecurityPolicyBasicTraffic verifies that the basic traffic of security policy.
// This is the very basic, blocking all in and out traffic between pods should take effect.
// NOTE: This test must NOT run in parallel with testSecurityPolicyAddDeleteRule - both use isolate-policy-1
func testSecurityPolicyBasicTraffic(t *testing.T) {
	// Do NOT run in parallel - conflicts with testSecurityPolicyAddDeleteRule (same policy name)
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Use pre-created namespace (shared with other security policy tests)
	ns := NsSecurityPolicy
	securityPolicyName := "isolate-policy-1"
	ruleName0 := "all_ingress_isolation"
	ruleName1 := "all_egress_isolation"

	// Create pods
	deploymentName := "server-client"
	_, err := testData.createDeployment(ns, deploymentName, containerName, podImage, corev1.ProtocolTCP, podPort, 2)
	require.NoErrorf(t, err, "Deloyment '%s/%s' should be created", ns, deploymentName)
	serverClientIPs, serverClientNames, err := testData.deploymentWaitForIPsOrNames(2*defaultTimeout, ns, deploymentName, 2)
	require.NoError(t, err, "Error when waiting for IP for Deployment '%s/%s'", ns, deploymentName)

	serverPodName := serverClientNames[0]
	clientPodName := serverClientNames[1]
	serverPodIP := serverClientIPs[0]

	log.Info("Server and Client Pod in the Namespace is ready", "Namespace", ns, "ServerPod", serverPodName, "ClientPod", clientPodName)

	// Test traffic from client Pod to server Pod
	require.True(t, checkTrafficByCurl(ns, clientPodName, containerName, serverPodIP, podPort, true), "Basic traffic should work")
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
	require.True(t, checkTrafficByCurl(ns, clientPodName, containerName, serverPodIP, podPort, false), "Basic traffic should not work")
	log.Info("Verified traffic from client Pod to the server Pod")

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)
	err = wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(ctx, securityPolicyName, v1.GetOptions{})
		log.Trace("Check resource", "resp", resp)
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
	require.True(t, checkTrafficByCurl(ns, clientPodName, containerName, serverPodIP, podPort, true), "Basic traffic should work")
	log.Info("Verified traffic from client Pod to the server Pod")
}

// TestSecurityPolicyAddDeleteRule verifies that when adding or deleting rule, the security policy will be updated.
// This is once a bug which is fixed later. When adding or deleting rule of one security policy repeatedly,
// the nsx-t side should keep consistent.
// NOTE: This test must NOT run in parallel with testSecurityPolicyBasicTraffic - both use isolate-policy-1
func testSecurityPolicyAddDeleteRule(t *testing.T) {
	// Do NOT run in parallel - conflicts with testSecurityPolicyBasicTraffic (same policy name)
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Use pre-created namespace (shared with other security policy tests)
	ns := NsSecurityPolicy
	securityPolicyName := "isolate-policy-1"
	ruleName0 := "all_ingress_isolation"
	ruleName1 := "all_egress_isolation"

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
		log.Trace("Check resource", "resp", resp)
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
// NOTE: This test must NOT run in parallel with testSecurityPolicyBasicTraffic/AddDeleteRule - all use NsSecurityPolicy
func testSecurityPolicyMatchExpression(t *testing.T) {
	// Do NOT run in parallel - shares NsSecurityPolicy namespace where other tests create isolation policies
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Use pre-created namespace (shared with other security policy tests)
	ns := NsSecurityPolicy
	securityPolicyName := "expression-policy-1"
	ruleName := "expression-policy-1-rule"

	// Create pods
	podPath, _ := filepath.Abs("./manifest/testSecurityPolicy/allow-client-a-via-pod-selector-with-match-expressions.yaml")
	require.NoError(t, applyYAML(podPath, ns))
	defer deleteYAML(podPath, "")

	clientA := "client-a"
	clientB := "client-b"
	podA := "pod-a"
	// Wait for pods
	_, err := testData.podWaitForIPs(defaultTimeout, clientA, ns)
	assert.NoError(t, err, "Error when waiting for IP for Pod %s", clientA)
	_, err = testData.podWaitForIPs(defaultTimeout, clientB, ns)
	assert.NoError(t, err, "Error when waiting for IP for Pod %s", clientB)
	iPs, err := testData.podWaitForIPs(defaultTimeout, podA, ns)
	assert.NoError(t, err, "Error when waiting for IP for Pod %s", podA)

	// Test traffic from clientA to podA
	require.True(t, checkTrafficByCurl(ns, clientA, clientA, iPs.ipv4.String(), podPort, true), "TestSecurityPolicyMatchExpression traffic should work")
	log.Info("Verified traffic from client Pod to PodA")
	// Test traffic from clientB to podA
	require.True(t, checkTrafficByCurl(ns, clientB, clientB, iPs.ipv4.String(), podPort, true), "TestSecurityPolicyMatchExpression traffic should work")
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
	require.True(t, checkTrafficByCurl(ns, clientA, clientA, iPs.ipv4.String(), podPort, true), "TestSecurityPolicyMatchExpression traffic should work")
	log.Info("Verified traffic from client Pod to PodA")
	// Test traffic from clientB to podA
	require.True(t, checkTrafficByCurl(ns, clientB, clientB, iPs.ipv4.String(), podPort, false), "TestSecurityPolicyMatchExpression traffic should not work")
	log.Info("Verified traffic from client Pod to PodB")

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)

	err = wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(ctx, securityPolicyName, v1.GetOptions{})
		log.Trace("Check resource", "resp", resp)
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
	require.True(t, checkTrafficByCurl(ns, clientA, clientA, iPs.ipv4.String(), podPort, true), "TestSecurityPolicyMatchExpression traffic should work")
	log.Info("Verified traffic from client Pod to PodA")
	// Test traffic from clientB to podA
	require.True(t, checkTrafficByCurl(ns, clientB, clientB, iPs.ipv4.String(), podPort, true), "TestSecurityPolicyMatchExpression traffic should work")
	log.Info("Verified traffic from client Pod to PodB")
}

// TestSecurityPolicyNamedPortWithoutPod verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in policy level and there's no pod holding the related named ports.
// NOTE: This test must NOT run in parallel with testSecurityPolicyNamedPorWithPod - both use same namespace and deployment label
func testSecurityPolicyNamedPortWithoutPod(t *testing.T) {
	// Do NOT run in parallel - conflicts with testSecurityPolicyNamedPorWithPod (same namespace and deployment)
	// Use pre-created namespaces (shared with testSecurityPolicyNamedPorWithPod)
	nsClient := NsSecurityPolicyNamedPortClient
	nsWeb := NsSecurityPolicyNamedPortWeb
	securityPolicyCRName := "named-port-policy-without-pod"
	webA := "web"
	labelWeb := "tcp-deployment"
	ruleName0 := "named-port-rule"
	ruleName1 := "all_ingress_isolation"
	ruleName2 := "all_egress_isolation"

	// Note: using pre-created namespaces, no need to create/delete here
	_ = nsClient // suppress unused warning, used for documentation

	// Create all
	yamlPath, _ := filepath.Abs("./manifest/testSecurityPolicy/named-port-without-pod.yaml")
	require.NoError(t, applyYAML(yamlPath, nsWeb))
	defer deleteYAML(yamlPath, nsWeb)

	psb, err := testData.deploymentWaitForNames(defaultTimeout, nsWeb, labelWeb)
	log.Trace("Pods", "pods", psb)
	assert.NoError(t, err, "Error when waiting for IP for Pod %s", webA)
	assureSecurityPolicyReady(t, nsWeb, securityPolicyCRName)

	// Check NSX resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyCRName, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName0, false)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName1, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName2, true)
	assert.NoError(t, err)
}

// testSecurityPolicyNamedPorWithPod verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in policy level and there's running pods holding the related named ports.
// NOTE: This test must NOT run in parallel with testSecurityPolicyNamedPortWithoutPod - both use same namespace and deployment label
func testSecurityPolicyNamedPorWithPod(t *testing.T) {
	// Do NOT run in parallel - conflicts with testSecurityPolicyNamedPortWithoutPod (same namespace and deployment)
	// Use pre-created namespaces
	nsClient := NsSecurityPolicyNamedPortClient
	nsWeb := NsSecurityPolicyNamedPortWeb
	securityPolicyCRName := "named-port-policy-with-pod"
	ruleName0 := "named-port-rule"
	ruleName1 := "all_ingress_isolation"
	ruleName2 := "all_egress_isolation"

	// Create all
	yamlPath, _ := filepath.Abs("./manifest/testSecurityPolicy/named-port-with-pod-client.yaml")
	_ = applyYAML(yamlPath, nsClient)
	defer deleteYAML(yamlPath, nsClient)

	yamlPath, _ = filepath.Abs("./manifest/testSecurityPolicy/named-port-with-pod-web.yaml")
	_ = applyYAML(yamlPath, nsWeb)
	defer deleteYAML(yamlPath, nsWeb)

	clientA := "client"
	webA := "web"
	labelWeb := "tcp-deployment"
	// Wait for pods
	clientPodIPs, err := testData.podWaitForIPs(defaultTimeout, clientA, nsClient)
	t.Logf("client Pods are %v", clientPodIPs)
	require.NoError(t, err, "Error when waiting for IP for Pod %s", clientA)
	namedPortPodIPs, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsWeb, labelWeb, 2)
	t.Logf("NamedPort Pods are %v", namedPortPodIPs)
	require.NoError(t, err, "Error when waiting for IP for Pod %s", webA)
	assureSecurityPolicyReady(t, nsWeb, securityPolicyCRName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyCRName, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName0, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName1, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName2, true)
	assert.NoError(t, err)

	// Test traffic from client to pod0
	require.True(t, checkTrafficByCurl(nsClient, clientA, clientA, namedPortPodIPs[0], podPort, true), "testSecurityPolicyNamedPort traffic should work")
	log.Info("Verified traffic from client Pod to Pod0")
	// Test traffic from clientA to pod1
	require.True(t, checkTrafficByCurl(nsClient, clientA, clientA, namedPortPodIPs[1], podPort, true), "testSecurityPolicyNamedPort traffic should work")
	log.Info("Verified traffic from client Pod to Pod1")
}

func assureSecurityPolicyReady(t *testing.T, ns, spName string) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 10, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(context.Background(), spName, v1.GetOptions{})
		log.Trace("Get resources", "SecurityPolicies", resp, "Namespace", ns, "Name", spName)
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
