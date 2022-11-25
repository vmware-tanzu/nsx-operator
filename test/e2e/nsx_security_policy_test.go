package e2e

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// TestSecurityPolicyBasicTraffic verifies that the basic traffic of security policy.
func TestSecurityPolicyBasicTraffic(t *testing.T) {
	ns := "test-security-policy-1"
	setupTest(t, ns)
	defer teardownTest(t, ns)

	// Create pods
	busyboxPath, _ := filepath.Abs("./manifest/testSecurityPolicy/busybox.yaml")
	_ = applyYAML(busyboxPath, ns)
	netcatPath, _ := filepath.Abs("./manifest/testSecurityPolicy/netcat-pod.yaml")
	_ = applyYAML(netcatPath, ns)

	// Wait for pods
	ps, err := testData.podWaitForIPs(defaultTimeout, "busybox", ns)
	t.Logf("Pods are %v", ps)
	assert.Nil(t, err, "Error when waiting for IP for Pod 'busybox'")
	iPs, err := testData.podWaitForIPs(defaultTimeout, "nc-pod", ns)
	t.Logf("Pods are %v", iPs)
	assert.Nil(t, err, "Error when waiting for IP for Pod 'nc-pod'")

	// Ping from pod
	err = testData.runPingCommandFromPod(ns, "busybox", iPs, 4)
	assert.Nil(t, err, "Error when running ping command from test Pod 'busybox'")

	// Create security policy
	nsIsolationPath, _ := filepath.Abs("./manifest/testSecurityPolicy/ns-isolation-policy.yaml")
	_ = applyYAML(nsIsolationPath, ns)
	err = testData.waitForSecurityPolicyReadyOrDeleted(defaultTimeout, ns, "isolate-policy-1", Ready)
	assert.Nil(t, err, "Error when waiting for Security Policy 'isolate-policy-1'")

	// Check nsx-t resource existing
	exist := testData.checkResourceExist(ns, common.ResourceTypeSecurityPolicy, "isolate-policy-1")
	assert.True(t, exist)
	exist = testData.checkResourceExist(ns, common.ResourceTypeRule, "isolate-policy-1")
	assert.True(t, exist)

	// Ping from pod
	err = testData.runPingCommandFromPod(ns, "busybox", iPs, 4)
	assert.NotNilf(t, err, "Error when running ping command from test Pod 'busybox'")

	// Delete security policy
	_ = deleteYAML(nsIsolationPath, ns)
	err = testData.waitForSecurityPolicyReadyOrDeleted(defaultTimeout, ns, "isolate-policy-1", Deleted)
	assert.Nil(t, err, "Error when waiting for Security Policy 'isolate-policy-1'")

	// Check nsx-t resource not existing
	exist = testData.checkResourceExist(ns, common.ResourceTypeSecurityPolicy, "isolate-policy-1")
	assert.False(t, exist)
	exist = testData.checkResourceExist(ns, common.ResourceTypeRule, "isolate-policy-1")
	assert.False(t, exist)

	// Ping from pod
	err = testData.runPingCommandFromPod(ns, "busybox", iPs, 4)
	assert.Nil(t, err, "Error when running ping command from test Pod 'busybox'")
}
