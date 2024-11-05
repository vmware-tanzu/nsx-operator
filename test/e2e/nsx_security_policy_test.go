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
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSecurityPolicy(t *testing.T) {
	t.Run("testSecurityPolicyBasicTraffic", func(t *testing.T) { testSecurityPolicyBasicTraffic(t) })
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
	var err error
	setupTest(t, ns)
	defer teardownTest(t, ns, defaultTimeout)

	// Temporarily disable traffic check
	/*
		// Create pods
		busyboxPath, _ := filepath.Abs("./manifest/testSecurityPolicy/busybox.yaml")
		_ = applyYAML(busyboxPath, ns)
		netcatPath, _ := filepath.Abs("./manifest/testSecurityPolicy/netcat-pod.yaml")
		_ = applyYAML(netcatPath, ns)

		busybox := "busybox"
		ncPod := "nc-pod
		// Wait for pods
		ps, err := testData.podWaitForIPs(defaultTimeout, busybox, ns)
		t.Logf("Pods are %v", ps)
		assert.NoError(t, err, "Error when waiting for IP for Pod %s", busybox)
		iPs, err := testData.podWaitForIPs(defaultTimeout, ncPod, ns)
		t.Logf("Pods are %v", iPs)
		assert.NoError(t, err, "Error when waiting for IP for Pod %s", ncPod)

		// Ping from pod
		err = testData.runPingCommandFromPod(ns, busybox, iPs, 4)
		assert.NoError(t, err, "Error when running ping command from test Pod %s", busybox)
	*/
	// Create security policy
	nsIsolationPath, _ := filepath.Abs("./manifest/testSecurityPolicy/ns-isolation-policy.yaml")
	_ = applyYAML(nsIsolationPath, ns)
	defer deleteYAML(nsIsolationPath, ns)
	assureSecurityPolicyReady(t, ns, securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, true)
	assert.NoError(t, err)

	// Temporarily disable traffic check
	/*
		// Ping from pod
		err = testData.runPingCommandFromPod(ns, busybox, iPs, 4)
		assertNotNil(t, err, "Error when running ping command from test Pod %s", busybox)
	*/

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)
	err = wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(ctx, securityPolicyName, v1.GetOptions{})
		t.Logf("Check resource: %v", resp)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for Security Policy %s", securityPolicyName)
		}
		return false, nil
	})
	assert.NoError(t, err)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, false)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, false)
	assert.NoError(t, err)

	// Temporarily disable traffic check
	/*
		// Ping from pod
		err = testData.runPingCommandFromPod(ns, busybox, iPs, 4)
		assert.NoError(t, err, "Error when running ping command from test Pod %s", busybox)
	*/
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
	_ = applyYAML(nsIsolationPath, ns)
	defer deleteYAML(nsIsolationPath, ns)
	assureSecurityPolicyReady(t, ns, securityPolicyName)

	// Check nsx-t resource existing
	err := testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, true)
	assert.NoError(t, err)

	// Update security policy
	nsIsolationPath, _ = filepath.Abs("./manifest/testSecurityPolicy/ns-isolation-policy-1.yaml")
	_ = applyYAML(nsIsolationPath, ns)
	defer deleteYAML(nsIsolationPath, ns)
	assureSecurityPolicyReady(t, ns, securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, false)
	assert.NoError(t, err)

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)

	err = wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(ctx, securityPolicyName, v1.GetOptions{})
		t.Logf("Check resource: %v", resp)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for Security Policy %s", securityPolicyName)
		}
		return false, nil
	})
	assert.NoError(t, err)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assert.NoError(t, err)
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

	var err error
	setupTest(t, ns)
	defer teardownTest(t, ns, defaultTimeout)

	// Create pods
	podPath, _ := filepath.Abs("./manifest/testSecurityPolicy/allow-client-a-via-pod-selector-with-match-expressions.yaml")
	_ = applyYAML(podPath, ns)
	defer deleteYAML(podPath, "")

	// Temporarily disable traffic check
	// clientA := "client-a"
	// clientB := "client-b"
	// podA := "pod-a"
	/*
		// Wait for pods
		ps, err := testData.podWaitForIPs(defaultTimeout, clientA, ns)
		t.Logf("Pods are %v", ps)
		assert.NoError(t, err, "Error when waiting for IP for Pod %s", clientA)
		psb, err := testData.podWaitForIPs(defaultTimeout, clientB, ns)
		t.Logf("Pods are %v", psb)
		assert.NoError(t, err, "Error when waiting for IP for Pod %s", clientB)
		iPs, err := testData.podWaitForIPs(defaultTimeout, podA, ns)
		t.Logf("Pods are %v", iPs)
		assert.NoError(t, err, "Error when waiting for IP for Pod %s", podA)

		// Ping from pod
		err = testData.runPingCommandFromPod(ns, clientA, iPs, 4)
		assert.NoError(t, err, "Error when running ping command from Pod %s", clientA)
		err = testData.runPingCommandFromPod(ns, clientB, iPs, 4)
		assert.NoError(t, err, "Error when running ping command from Pod %s", clientB)
	*/

	// Create security policy
	nsIsolationPath, _ := filepath.Abs("./manifest/testSecurityPolicy/match-expression.yaml")
	_ = applyYAML(nsIsolationPath, ns)
	defer deleteYAML(nsIsolationPath, ns)
	assureSecurityPolicyReady(t, ns, securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName, true)
	assert.NoError(t, err)

	// Temporarily disable traffic check
	/*
		// Ping from pod
		err = testData.runPingCommandFromPod(ns, clientA, iPs, 4)
		assert.NoError(t, err, "Error when running ping command from Pod %s", clientA)
		err = testData.runPingCommandFromPod(ns, clientB, iPs, 4)
		assert.NotNilf(t, err, "Error when running ping command from Pod %s", clientB)
	*/

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)

	err = wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(ctx, securityPolicyName, v1.GetOptions{})
		t.Logf("Check resource: %v", resp)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for Security Policy %s", securityPolicyName)
		}
		return false, nil
	})
	assert.NoError(t, err)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assert.NoError(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName, false)
	assert.NoError(t, err)

	// Temporarily disable traffic check
	/*
		// Ping from pod
		err = testData.runPingCommandFromPod(ns, clientA, iPs, 4)
		assert.NoError(t, err, "Error when running ping command from Pod %s", clientA)
		err = testData.runPingCommandFromPod(ns, clientB, iPs, 4)
		assert.NoError(t, err, "Error when running ping command from Pod %s", clientB)
	*/
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
	_ = applyYAML(yamlPath, "")
	defer deleteYAML(yamlPath, "")

	psb, err := testData.deploymentWaitForNames(defaultTimeout, nsWeb, labelWeb)
	t.Logf("Pods are %v", psb)
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
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(context.Background(), spName, v1.GetOptions{})
		t.Logf("Get SecurityPolicies: %v, Namespace: %s, Name: %s", resp, ns, spName)
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
	assert.NoError(t, err)
}
