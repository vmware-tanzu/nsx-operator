package e2e

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSecurityPolicyBasicTraffic verifies that the new created pod appears in inventory.
func TestSecurityPolicyBasicTraffic(t *testing.T) {
	ns := "test-security-policy-1"
	setupTest(t, ns)
	defer teardownTest(t, ns)

	busyboxPath, _ := filepath.Abs("./manifest/testSecurityPolicy/busybox.yaml")
	_ = applyYAML(busyboxPath, ns)
	netcatPath, _ := filepath.Abs("./manifest/testSecurityPolicy/netcat-pod.yaml")
	_ = applyYAML(netcatPath, ns)

	ps, err := testData.podWaitForIPs(defaultTimeout, "busybox", ns)
	t.Logf("Pods are %v", ps)
	assert.Nil(t, err, "Error when waiting for IP for Pod 'busybox'")
	iPs, err := testData.podWaitForIPs(defaultTimeout, "nc-pod", ns)
	t.Logf("Pods are %v", iPs)
	assert.Nil(t, err, "Error when waiting for IP for Pod 'nc-pod'")

	err = testData.runPingCommandFromTestPod(ns, "busybox", iPs, 4)
	assert.Nil(t, err, "Error when running ping command from test Pod 'busybox'")

	nsIsolationPath, _ := filepath.Abs("./manifest/testSecurityPolicy/ns-isolation-policy.yaml")
	_ = applyYAML(nsIsolationPath, ns)
	err = testData.securityPolicyWaitFor(defaultTimeout, "isolate-policy-1", ns)
	assert.Nil(t, err, "Error when waiting for Security Policy 'isolate-policy-1'")

	err = testData.runPingCommandFromTestPod(ns, "busybox", iPs, 4)
	assert.NotNilf(t, err, "Error when running ping command from test Pod 'busybox'")
}
