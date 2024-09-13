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
	"path/filepath"
	"testing"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	SP = "securitypolicies.crd.nsx.vmware.com"
)

// TestSecurityPolicyBasicTraffic verifies that the basic traffic of security policy.
// This is the very basic, blocking all in and out traffic between pods should take effect.
func TestSecurityPolicyBasicTraffic(t *testing.T) {
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
		assertNil(t, err, "Error when waiting for IP for Pod %s", busybox)
		iPs, err := testData.podWaitForIPs(defaultTimeout, ncPod, ns)
		t.Logf("Pods are %v", iPs)
		assertNil(t, err, "Error when waiting for IP for Pod %s", ncPod)

		// Ping from pod
		err = testData.runPingCommandFromPod(ns, busybox, iPs, 4)
		assertNil(t, err, "Error when running ping command from test Pod %s", busybox)
	*/
	// Create security policy
	nsIsolationPath, _ := filepath.Abs("./manifest/testSecurityPolicy/ns-isolation-policy.yaml")
	_ = applyYAML(nsIsolationPath, ns)
	defer deleteYAML(nsIsolationPath, ns)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, ns, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, true)
	assertNil(t, err)

	//Temporarily disable traffic check
	/*
		// Ping from pod
		err = testData.runPingCommandFromPod(ns, busybox, iPs, 4)
		assertNotNil(t, err, "Error when running ping command from test Pod %s", busybox)
	*/

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, ns, securityPolicyName, Deleted)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, false)
	assertNil(t, err)

	//Temporarily disable traffic check
	/*
		// Ping from pod
		err = testData.runPingCommandFromPod(ns, busybox, iPs, 4)
		assertNil(t, err, "Error when running ping command from test Pod %s", busybox)
	*/
}

// TestSecurityPolicyAddDeleteRule verifies that when adding or deleting rule, the security policy will be updated.
// This is once a bug which is fixed later. When adding or deleting rule of one security policy repeatedly,
// the nsx-t side should keep consistent.
func TestSecurityPolicyAddDeleteRule(t *testing.T) {
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
	err := testData.waitForCRReadyOrDeleted(defaultTimeout, SP, ns, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, true)
	assertNil(t, err)

	// Update security policy
	nsIsolationPath, _ = filepath.Abs("./manifest/testSecurityPolicy/ns-isolation-policy-1.yaml")
	_ = applyYAML(nsIsolationPath, ns)
	defer deleteYAML(nsIsolationPath, ns)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, ns, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName0, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName1, false)
	assertNil(t, err)

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, ns, securityPolicyName, Deleted)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assertNil(t, err)
}

// TestSecurityPolicyMatchExpression verifies that the traffic of security policy when match expression applied.
// This test is to verify the match expression, NotIn/In operator feature of security policy. It should apply
// to the specified pod.
func TestSecurityPolicyMatchExpression(t *testing.T) {
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
	//clientA := "client-a"
	//clientB := "client-b"
	//podA := "pod-a"
	/*
		// Wait for pods
		ps, err := testData.podWaitForIPs(defaultTimeout, clientA, ns)
		t.Logf("Pods are %v", ps)
		assertNil(t, err, "Error when waiting for IP for Pod %s", clientA)
		psb, err := testData.podWaitForIPs(defaultTimeout, clientB, ns)
		t.Logf("Pods are %v", psb)
		assertNil(t, err, "Error when waiting for IP for Pod %s", clientB)
		iPs, err := testData.podWaitForIPs(defaultTimeout, podA, ns)
		t.Logf("Pods are %v", iPs)
		assertNil(t, err, "Error when waiting for IP for Pod %s", podA)

		// Ping from pod
		err = testData.runPingCommandFromPod(ns, clientA, iPs, 4)
		assertNil(t, err, "Error when running ping command from Pod %s", clientA)
		err = testData.runPingCommandFromPod(ns, clientB, iPs, 4)
		assertNil(t, err, "Error when running ping command from Pod %s", clientB)
	*/

	// Create security policy
	nsIsolationPath, _ := filepath.Abs("./manifest/testSecurityPolicy/match-expression.yaml")
	_ = applyYAML(nsIsolationPath, ns)
	defer deleteYAML(nsIsolationPath, ns)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, ns, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName, true)
	assertNil(t, err)

	// Temporarily disable traffic check
	/*
		// Ping from pod
		err = testData.runPingCommandFromPod(ns, clientA, iPs, 4)
		assertNil(t, err, "Error when running ping command from Pod %s", clientA)
		err = testData.runPingCommandFromPod(ns, clientB, iPs, 4)
		assert.NotNilf(t, err, "Error when running ping command from Pod %s", clientB)
	*/

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, ns, securityPolicyName, Deleted)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeRule, ruleName, false)
	assertNil(t, err)

	// Temporarily disable traffic check
	/*
		// Ping from pod
		err = testData.runPingCommandFromPod(ns, clientA, iPs, 4)
		assertNil(t, err, "Error when running ping command from Pod %s", clientA)
		err = testData.runPingCommandFromPod(ns, clientB, iPs, 4)
		assertNil(t, err, "Error when running ping command from Pod %s", clientB)
	*/
}

// TestSecurityPolicyNamedPortWithoutPod verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in policy level and there's no pod holding the related named ports.
func TestSecurityPolicyNamedPortWithoutPod(t *testing.T) {
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
	assertNil(t, err, "Error when waiting for IP for Pod %s", webA)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyCRName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyCRName)

	// Check NSX resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyCRName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName0, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName1, true)
	assertNil(t, err)
}

/*
// TestSecurityPolicyNamedPort0 verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in policy level.
func TestSecurityPolicyNamedPort0(t *testing.T) {
	nsClient := "client"
	nsWeb := "web"
	securityPolicyName := "named-port-policy"
	clientA := "client"
	webA := "web"
	labelWeb := "tcp-deployment"

	testData.deleteNamespace(nsClient, defaultTimeout)
	testData.deleteNamespace(nsWeb, defaultTimeout)
	_ = testData.createNamespace(nsClient)
	_ = testData.createNamespace(nsWeb)
	defer testData.deleteNamespace(nsClient, defaultTimeout)
	defer testData.deleteNamespace(nsWeb, defaultTimeout)

	// Create all
	podPath, _ := filepath.Abs("./manifest/testSecurityPolicy/rule-in-policy-applied-to.yaml")
	_ = applyYAML(podPath, "")
	defer deleteYAML(podPath, "")

	// Wait for pods
	ps, err := testData.podWaitForIPs(defaultTimeout, clientA, nsClient)
	t.Logf("Pods are %v", ps)
	assertNil(t, err, "Error when waiting for IP for Pod %s", clientA)
	psb, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsWeb, labelWeb)
	t.Logf("Pods are %v", psb)
	assertNil(t, err, "Error when waiting for IP for Pod %s", webA)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, securityPolicyName, true)
	assertNil(t, err)

	// Nc from pod
	err = testData.runNetcatCommandFromPod(nsClient, clientA, clientA, psb[0], 80)
	assertNil(t, err, "Error when running nc command from Pod %s", clientA)
	err = testData.runNetcatCommandFromPod(nsClient, clientA, clientA, psb[1], 80)
	assertNil(t, err, "Error when running nc command from Pod %s", clientA)

	// Delete all
	_ = deleteYAML(podPath, "")
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Deleted)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, securityPolicyName, false)
	assertNil(t, err)
}

// TestSecurityPolicyNamedPort1 verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in rule level.
func TestSecurityPolicyNamedPort1(t *testing.T) {
	nsClient := "client"
	nsWeb := "web"
	securityPolicyName := "named-port-policy"
	clientA := "client"
	webA := "web"
	labelWeb := "tcp-deployment"

	testData.deleteNamespace(nsClient, defaultTimeout)
	testData.deleteNamespace(nsWeb, defaultTimeout)
	_ = testData.createNamespace(nsClient)
	_ = testData.createNamespace(nsWeb)
	defer testData.deleteNamespace(nsClient, defaultTimeout)
	defer testData.deleteNamespace(nsWeb, defaultTimeout)

	// Create all
	podPath, _ := filepath.Abs("./manifest/testSecurityPolicy/rule-in-rule-applied-to.yaml")
	_ = applyYAML(podPath, "")
	defer deleteYAML(podPath, "")

	// Wait for pods
	ps, err := testData.podWaitForIPs(defaultTimeout, clientA, nsClient)
	t.Logf("Pods are %v", ps)
	assertNil(t, err, "Error when waiting for IP for Pod %s", clientA)
	psb, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsWeb, labelWeb)
	t.Logf("Pods are %v", psb)
	assertNil(t, err, "Error when waiting for IP for Pod %s", webA)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, securityPolicyName, true)
	assertNil(t, err)

	// Nc from pod
	err = testData.runNetcatCommandFromPod(nsClient, clientA, clientA, psb[0], 80)
	assertNil(t, err, "Error when running nc command from Pod %s", clientA)
	err = testData.runNetcatCommandFromPod(nsClient, clientA, clientA, psb[1], 80)
	assertNil(t, err, "Error when running nc command from Pod %s", clientA)

	// Delete all
	_ = deleteYAML(podPath, "")
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Deleted)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, securityPolicyName, false)
	assertNil(t, err)
}

// TestSecurityPolicyNamedPort2 verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in rule level and there is source selector in rule.
func TestSecurityPolicyNamedPort2(t *testing.T) {
	nsClient := "client"
	nsWeb := "web"
	securityPolicyName := "named-port-policy"
	clientA := "client"
	webA := "web"
	labelWeb := "tcp-deployment"

	testData.deleteNamespace(nsClient, defaultTimeout)
	testData.deleteNamespace(nsWeb, defaultTimeout)
	_ = testData.createNamespace(nsClient)
	_ = testData.createNamespace(nsWeb)
	defer testData.deleteNamespace(nsClient, defaultTimeout)
	defer testData.deleteNamespace(nsWeb, defaultTimeout)

	// Create all
	podPath, _ := filepath.Abs("./manifest/testSecurityPolicy/rule-in-rule-applied-to-with-src.yaml")
	_ = applyYAML(podPath, "")
	defer deleteYAML(podPath, "")

	// Wait for pods
	ps, err := testData.podWaitForIPs(defaultTimeout, clientA, nsClient)
	t.Logf("Pods are %v", ps)
	assertNil(t, err, "Error when waiting for IP for Pod %s", clientA)
	psb, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsWeb, labelWeb)
	t.Logf("Pods are %v", psb)
	assertNil(t, err, "Error when waiting for IP for Pod %s", webA)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, securityPolicyName, true)
	assertNil(t, err)

	// Label ns
	cmd := fmt.Sprintf("kubectl label ns %s %s=%s --overwrite", clientA, "role", "client")
	_, err = runCommand(cmd)
	assertNil(t, err, "Error when running command %s", cmd)

	// Nc from pod
	err = testData.runNetcatCommandFromPod(nsClient, clientA, clientA, psb[0], 80)
	assertNil(t, err, "Error when running nc command from Pod %s", clientA)
	err = testData.runNetcatCommandFromPod(nsClient, clientA, clientA, psb[1], 80)
	assertNil(t, err, "Error when running nc command from Pod %s", clientA)

	// Delete all
	_ = deleteYAML(podPath, "")
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Deleted)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, securityPolicyName, false)
	assertNil(t, err)
}

// TestSecurityPolicyNamedPort3 verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in policy level and there is destination selector in rule.
func TestSecurityPolicyNamedPort3(t *testing.T) {
	nsDB := "db"
	nsWeb := "web"
	containerName := "web"
	securityPolicyName := "named-port-policy"
	labelWeb := "tcp-deployment"
	labelDB := "mysql"

	testData.deleteNamespace(nsDB, defaultTimeout)
	testData.deleteNamespace(nsWeb, defaultTimeout)
	_ = testData.createNamespace(nsDB)
	_ = testData.createNamespace(nsWeb)
	defer testData.deleteNamespace(nsDB, defaultTimeout)
	defer testData.deleteNamespace(nsWeb, defaultTimeout)

	// Label ns
	cmd := fmt.Sprintf("kubectl label ns %s %s=%s --overwrite", nsDB, "role", "db")
	_, err := runCommand(cmd)
	assertNil(t, err, "Error when running command %s", cmd)

	// Create all
	podPath, _ := filepath.Abs("./manifest/testSecurityPolicy/rule-out-policy-applied-to-with-dst.yaml")
	_ = applyYAML(podPath, "")
	defer deleteYAML(podPath, "")

	// Wait for pods
	ps, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsDB, labelDB)
	t.Logf("Pods are %v", ps)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsDB)

	_, psb, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsWeb, labelWeb)
	t.Logf("Pods are %v", psb)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsWeb)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, securityPolicyName, true)
	assertNil(t, err)

	// Nc from pod
	err = testData.runNetcatCommandFromPod(nsWeb, psb[0], containerName, ps[0], 3306)
	assertNil(t, err, "Error when running nc command from Pod %s", "web")

	// Delete all
	_ = deleteYAML(podPath, "")
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Deleted)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, securityPolicyName, false)
	assertNil(t, err)
}

// TestSecurityPolicyNamedPort4 verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in policy level and there is destination selector in rule.
func TestSecurityPolicyNamedPort4(t *testing.T) {
	nsDB := "db"
	nsWeb := "web"
	containerName := "web"
	securityPolicyName := "named-port-policy"
	labelWeb := "tcp-deployment"
	labelDB := "mysql"

	testData.deleteNamespace(nsDB, defaultTimeout)
	testData.deleteNamespace(nsWeb, defaultTimeout)
	_ = testData.createNamespace(nsDB)
	_ = testData.createNamespace(nsWeb)
	defer testData.deleteNamespace(nsDB, defaultTimeout)
	defer testData.deleteNamespace(nsWeb, defaultTimeout)

	// Label ns
	cmd := fmt.Sprintf("kubectl label ns %s %s=%s --overwrite", nsDB, "role", "db")
	_, err := runCommand(cmd)
	assertNil(t, err, "Error when running command %s", cmd)

	// Create all
	podPath, _ := filepath.Abs("./manifest/testSecurityPolicy/rule-out-rule-applied-to-with-dst.yaml")
	_ = applyYAML(podPath, "")
	defer deleteYAML(podPath, "")

	// Wait for pods
	ps, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsDB, labelDB)
	t.Logf("Pods are %v", ps)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsDB)

	_, psb, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsWeb, labelWeb)
	t.Logf("Pods are %v", psb)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsWeb)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, securityPolicyName, true)
	assertNil(t, err)

	// Nc from pod
	err = testData.runNetcatCommandFromPod(nsWeb, psb[0], containerName, ps[0], 3306)
	assertNil(t, err, "Error when running nc command from Pod %s", "web")

	// Delete all
	_ = deleteYAML(podPath, "")
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Deleted)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, securityPolicyName, false)
	assertNil(t, err)
}

// TestSecurityPolicyNamedPort5 verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in rule level and there is destination selector which consists of match expression in rule.
func TestSecurityPolicyNamedPort5(t *testing.T) {
	nsDB := "db"
	nsDB2 := "db2"
	nsWeb := "web"
	containerName := "web"
	securityPolicyName := "named-port-policy"
	ruleName := "named-port-policy-0-0-0"
	ruleName1 := "named-port-policy-0-0-1"
	labelWeb := "tcp-deployment"
	labelDB := "mysql"
	labelDB2 := "mysql2"

	testData.deleteNamespace(nsDB, defaultTimeout)
	testData.deleteNamespace(nsDB2, defaultTimeout)
	testData.deleteNamespace(nsWeb, defaultTimeout)
	_ = testData.createNamespace(nsDB)
	_ = testData.createNamespace(nsDB2)
	_ = testData.createNamespace(nsWeb)
	defer testData.deleteNamespace(nsDB, defaultTimeout)
	defer testData.deleteNamespace(nsDB2, defaultTimeout)
	defer testData.deleteNamespace(nsWeb, defaultTimeout)

	// Label ns
	cmd := fmt.Sprintf("kubectl label ns %s %s=%s --overwrite", nsDB, "role", "db")
	_, err := runCommand(cmd)
	assertNil(t, err, "Error when running command %s", cmd)

	// Create all
	podPath, _ := filepath.Abs("./manifest/testSecurityPolicy/rule-out-rule-applied-to-with-expression-selector.yaml")
	_ = applyYAML(podPath, "")
	defer deleteYAML(podPath, "")

	// Wait for pods
	ps, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsDB, labelDB)
	t.Logf("Pods are %v", ps)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsDB)

	ps2, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsDB2, labelDB2)
	t.Logf("Pods are %v", ps2)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsDB2)

	_, psb, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsWeb, labelWeb)
	t.Logf("Pods are %v", psb)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsWeb)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName, true)
	assertNil(t, err)

	// Nc from pod
	err = testData.runNetcatCommandFromPod(nsWeb, psb[0], containerName, ps2[0], 1234)
	assertNotNil(t, err, "Error when running nc command from Pod %s", "web")
	err = testData.runNetcatCommandFromPod(nsWeb, psb[0], containerName, ps[0], 3306)
	assertNil(t, err, "Error when running nc command from Pod %s", "web")

	// Label ns
	cmd = fmt.Sprintf("kubectl label ns %s %s=%s --overwrite", nsDB2, "role", "db")
	_, err = runCommand(cmd)
	assertNil(t, err, "Error when running command %s", cmd)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName1, true)
	assertNil(t, err)

	// Nc from pod
	err = testData.runNetcatCommandFromPod(nsWeb, psb[0], containerName, ps2[0], 1234)
	assertNil(t, err, "Error when running nc command from Pod %s", "web")
	err = testData.runNetcatCommandFromPod(nsWeb, psb[0], containerName, ps[0], 3306)
	assertNil(t, err, "Error when running nc command from Pod %s", "web")

	// Delete all
	_ = deleteYAML(podPath, "")
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Deleted)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName1, false)
	assertNil(t, err)
}

// TestSecurityPolicyNamedPort6 verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in rule level and there is destination selector in rule.
// If the port number is the same in multiple pods, then there should be only one rule created,
// and the ip set group consists of multiple ips and the port number is only one.
func TestSecurityPolicyNamedPort6(t *testing.T) {
	nsDB := "db"
	nsDB2 := "db2"
	nsWeb := "web"
	containerName := "web"
	securityPolicyName := "named-port-policy"
	ruleName := "named-port-policy-0-0-0"
	ruleName1 := "named-port-policy-0-0-1"
	labelWeb := "tcp-deployment"
	labelDB := "mysql"
	labelDB2 := "mysql2"

	testData.deleteNamespace(nsDB, defaultTimeout)
	testData.deleteNamespace(nsDB2, defaultTimeout)
	testData.deleteNamespace(nsWeb, defaultTimeout)
	_ = testData.createNamespace(nsDB)
	_ = testData.createNamespace(nsDB2)
	_ = testData.createNamespace(nsWeb)
	defer testData.deleteNamespace(nsDB, defaultTimeout)
	defer testData.deleteNamespace(nsDB2, defaultTimeout)
	defer testData.deleteNamespace(nsWeb, defaultTimeout)

	// Label ns
	cmd := fmt.Sprintf("kubectl label ns %s %s=%s --overwrite", nsDB2, "role", "db")
	_, err := runCommand(cmd)
	assertNil(t, err, "Error when running command %s", cmd)
	cmd = fmt.Sprintf("kubectl label ns %s %s=%s --overwrite", nsDB, "role", "db")
	_, err = runCommand(cmd)
	assertNil(t, err, "Error when running command %s", cmd)

	// Create all
	podPath, _ := filepath.Abs("./manifest/testSecurityPolicy/rule-out-rule-applied-to-with-dst-with-dup-port.yaml")
	_ = applyYAML(podPath, "")
	defer deleteYAML(podPath, "")

	// Wait for pods
	ps, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsDB, labelDB)
	t.Logf("Pods are %v", ps)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsDB)

	ps2, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsDB2, labelDB2)
	t.Logf("Pods are %v", ps2)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsDB2)

	_, psb, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsWeb, labelWeb)
	t.Logf("Pods are %v", psb)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsWeb)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName1, false)
	assertNil(t, err)

	// Nc from pod
	err = testData.runNetcatCommandFromPod(nsWeb, psb[0], containerName, ps2[0], 3306)
	assertNil(t, err, "Error when running nc command from Pod %s", "web")
	err = testData.runNetcatCommandFromPod(nsWeb, psb[0], containerName, ps[0], 3306)
	assertNil(t, err, "Error when running nc command from Pod %s", "web")

	// Delete all
	_ = deleteYAML(podPath, "")
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Deleted)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName1, false)
	assertNil(t, err)
}

// TestSecurityPolicyNamedPort7 verifies that the traffic of security policy when named port applied.
// This test is to verify the named port feature of security policy.
// When appliedTo is in rule level and there is destination selector in rule.
// If the port number is not the same in multiple pods, then there should be multiple rules created,
// and each rule has an ip set group, and the port number is also different.
func TestSecurityPolicyNamedPort7(t *testing.T) {
	nsDB := "db"
	nsDB2 := "db2"
	nsWeb := "web"
	containerName := "web"
	securityPolicyName := "named-port-policy"
	ruleName := "named-port-policy-0-0-0"
	ruleName1 := "named-port-policy-0-0-1"
	labelWeb := "tcp-deployment"
	labelDB := "mysql"
	labelDB2 := "mysql2"

	testData.deleteNamespace(nsDB, defaultTimeout)
	testData.deleteNamespace(nsDB2, defaultTimeout)
	testData.deleteNamespace(nsWeb, defaultTimeout)
	_ = testData.createNamespace(nsDB)
	_ = testData.createNamespace(nsDB2)
	_ = testData.createNamespace(nsWeb)
	defer testData.deleteNamespace(nsDB, defaultTimeout)
	defer testData.deleteNamespace(nsDB2, defaultTimeout)
	defer testData.deleteNamespace(nsWeb, defaultTimeout)

	// Label ns
	cmd := fmt.Sprintf("kubectl label ns %s %s=%s --overwrite", nsDB2, "role", "db")
	_, err := runCommand(cmd)
	assertNil(t, err, "Error when running command %s", cmd)
	cmd = fmt.Sprintf("kubectl label ns %s %s=%s --overwrite", nsDB, "role", "db")
	_, err = runCommand(cmd)
	assertNil(t, err, "Error when running command %s", cmd)

	// Create all
	podPath, _ := filepath.Abs("./manifest/testSecurityPolicy/rule-out-rule-applied-to-with-dst-with-dup-port-multi.yaml")
	_ = applyYAML(podPath, "")
	defer deleteYAML(podPath, "")

	// Wait for pods
	ps, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsDB, labelDB)
	t.Logf("Pods are %v", ps)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsDB)

	ps2, _, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsDB2, labelDB2)
	t.Logf("Pods are %v", ps2)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsDB2)

	_, psb, err := testData.deploymentWaitForIPsOrNames(defaultTimeout, nsWeb, labelWeb)
	t.Logf("Pods are %v", psb)
	assertNil(t, err, "Error when waiting for IP for Pod ns %s", nsWeb)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Ready)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName, true)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName1, true)
	assertNil(t, err)

	// Nc from pod
	err = testData.runNetcatCommandFromPod(nsWeb, psb[0], containerName, ps2[0], 1234)
	assertNil(t, err, "Error when running nc command from Pod %s", "web")
	err = testData.runNetcatCommandFromPod(nsWeb, psb[0], containerName, ps[0], 3306)
	assertNil(t, err, "Error when running nc command from Pod %s", "web")

	// Delete all
	_ = deleteYAML(podPath, "")
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SP, nsWeb, securityPolicyName, Deleted)
	assertNil(t, err, "Error when waiting for Security Policy %s", securityPolicyName)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeSecurityPolicy, securityPolicyName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName, false)
	assertNil(t, err)
	err = testData.waitForResourceExistOrNot(nsWeb, common.ResourceTypeRule, ruleName1, false)
	assertNil(t, err)
}
*/
