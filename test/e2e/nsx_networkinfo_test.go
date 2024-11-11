package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	networkInfoCRType   = "networkinfos.crd.nsx.vmware.com"
	networkConfigCRType = "vpcnetworkconfigurations.crd.nsx.vmware.com"
	nsCRType            = "namespaces"

	testCustomizedNetworkConfigName = "selfdefinedconfig"
	testInfraNetworkConfigName      = "system"

	infraVPCNamespace       = "kube-system"
	sharedInfraVPCNamespace = "kube-public"

	e2eNetworkInfoNamespace       = "customized-ns"
	e2eNetworkInfoNamespaceShare0 = "shared-vpc-ns-0"
	e2eNetworkInfoNamespaceShare1 = "shared-vpc-ns-1"

	customizedPrivateCIDR1 = "172.29.0.0"
	customizedPrivateCIDR2 = "172.39.0.0"
	customizedPrivateCIDR3 = "172.39.0.0"
)

func verifyCRCreated(t *testing.T, crtype string, ns string, crname string, expect int) (string, string) {
	// For CRs that do not know the name, get all resources and compare with expected
	if crname == "" {
		// get CR list with CR type
		resources, err := testData.getCRResources(defaultTimeout, crtype, ns)
		// CR list lengh should match expected length
		assert.NoError(t, err)
		assert.Equal(t, expect, len(resources), "CR creation verify failed, %s list %s size not the same as expected %d", crtype, resources, expect)

		// TODO: if mutiple resources are required, here we need to return multiple elements, but for now, we only need one.
		cr_name, cr_uid := "", ""
		for k, v := range resources {
			cr_name = k
			cr_uid = strings.TrimSpace(v)
		}
		return cr_name, cr_uid
	} else {
		// there should be one networkinfo created
		uid, err := testData.getCRResource(defaultTimeout, crtype, crname, ns)
		assert.NoError(t, err)

		return crname, strings.TrimSpace(uid)
	}
}

func verifyCRDeleted(t *testing.T, crtype string, ns string) {
	res, _ := testData.getCRResources(defaultTimeout, crtype, ns)
	assert.True(t, len(res) == 0, "NetworkInfo CR %s should be deleted", crtype)
}

func TestNetworkInfo(t *testing.T) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		deleteErr := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Delete(context.Background(), testCustomizedNetworkConfigName, v1.DeleteOptions{})
		t.Logf("Delete VPCNetworkConfigurations %s: %v", testCustomizedNetworkConfigName, deleteErr)

		resp, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(ctx, testCustomizedNetworkConfigName, v1.GetOptions{})
		t.Logf("Check stale vpcnetworkconfigurations: %v", resp)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for vpcnetworkconfigurations %s", testCustomizedNetworkConfigName)
		}
		return false, nil
	})
	assert.NoError(t, err)

	defer t.Cleanup(
		func() {
			err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Delete(context.Background(), testCustomizedNetworkConfigName, v1.DeleteOptions{})
			t.Logf("Delete VPCNetworkConfigurations %s: %v", testCustomizedNetworkConfigName, err)
			teardownTest(t, e2eNetworkInfoNamespace, defaultTimeout)
			teardownTest(t, e2eNetworkInfoNamespaceShare0, defaultTimeout)
			teardownTest(t, e2eNetworkInfoNamespaceShare1, defaultTimeout)
		})

	t.Run("testCustomizedNetworkInfo", func(t *testing.T) { testCustomizedNetworkInfo(t) })
	t.Run("testInfraNetworkInfo", func(t *testing.T) { testInfraNetworkInfo(t) })
	t.Run("testDefaultNetworkInfo", func(t *testing.T) { testDefaultNetworkInfo(t) })
	t.Run("testSharedNSXVPC", func(t *testing.T) { testSharedNSXVPC(t) })
	t.Run("testUpdateVPCNetworkconfigNetworkInfo", func(t *testing.T) { testUpdateVPCNetworkconfigNetworkInfo(t) })
}

// Test Customized NetworkInfo
func testCustomizedNetworkInfo(t *testing.T) {
	// Create customized networkconfig
	ncPath, _ := filepath.Abs("./manifest/testVPC/customize_networkconfig.yaml")
	_ = applyYAML(ncPath, "")
	nsPath, _ := filepath.Abs("./manifest/testVPC/customize_ns.yaml")
	_ = applyYAML(nsPath, "")

	defer deleteYAML(nsPath, "")

	ns := "customized-ns"

	// For networkinfo CR, its CR name is the same as namespace
	verifyCRCreated(t, networkInfoCRType, ns, ns, 1)
	verifyCRCreated(t, nsCRType, ns, ns, 1)

	vpcPath, _ := testData.getCRPropertiesByJson(
		defaultTimeout, networkConfigCRType, testCustomizedNetworkConfigName, "", ".status.vpcs[0].vpcPath")
	err := testData.waitForResourceExistByPath(vpcPath, true)
	assert.NoError(t, err)
}

// Test Infra NetworkInfo
func testInfraNetworkInfo(t *testing.T) {
	// Check namespace cr existence
	verifyCRCreated(t, nsCRType, infraVPCNamespace, infraVPCNamespace, 1)

	// Check networkinfo cr existence
	verifyCRCreated(t, networkInfoCRType, infraVPCNamespace, infraVPCNamespace, 1)

	vpcPath, _ := testData.getCRPropertiesByJson(
		defaultTimeout, networkConfigCRType, testInfraNetworkConfigName, "", ".status.vpcs[0].vpcPath")
	err := testData.waitForResourceExistByPath(vpcPath, true)
	assert.NoError(t, err)

	// kube-public namespace should have its own NetworkInfo CR
	// Check networkinfo cr existence
	verifyCRCreated(t, networkInfoCRType, sharedInfraVPCNamespace, sharedInfraVPCNamespace, 1)
}

// Test Default NetworkInfo
func testDefaultNetworkInfo(t *testing.T) {
	// If no annotation on namespace, then NetworkInfo will use default network config to create vpc under each ns
	ns := "networkinfo-default-1"
	setupTest(t, ns)
	defer teardownTest(t, ns, defaultTimeout)

	// Check namespace cr existence
	verifyCRCreated(t, nsCRType, ns, ns, 1)

	// Check networkinfo cr existence
	verifyCRCreated(t, networkInfoCRType, ns, ns, 1)

	// TODO: for default network config, it maybe shared by multiple ns, and the vpc[0] may not be its VPC
	// in WCP scenario, there will be no shared network config, skip this part, leave for future if we need to
	// support non-WCP scenarios.
	// vpcPath, _ := testData.getCRPropertiesByJson(
	//      defaultTimeout, networkConfigCRType, TestDefaultNetworkConfigName, "", ".status.vpcs[0].vpcPath")
	// err := testData.waitForResourceExistByPath(vpcPath, true)
	// assert.NoError(t, err)

	// delete namespace and check all resources are deleted
	// normally, when deleting ns, if using shared vpc, then no vpc will be deleted
	// if using normal vpc, in ns deletion, the networkconfig CR will also be deleted, so there is no
	// need to check vpcPath deletion on network config CR anymore
	err := testData.deleteNamespace(ns, defaultTimeout)
	assert.NoError(t, err)
	verifyCRDeleted(t, networkInfoCRType, ns)
	verifyCRDeleted(t, nsCRType, ns)
	// err = testData.waitForResourceExistByPath(vpcPath, false)
	assert.NoError(t, err)
}

// ns1 share vpc with ns, each ns should have its own NetworkInfo
// delete ns1, vpc should not be deleted
func testSharedNSXVPC(t *testing.T) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	ns := "shared-vpc-ns-0"
	ns1 := "shared-vpc-ns-1"

	nsPath, _ := filepath.Abs("./manifest/testVPC/shared_ns.yaml")
	_ = applyYAML(nsPath, "")
	defer deleteYAML(nsPath, "")

	// Check namespace cr existence
	verifyCRCreated(t, nsCRType, ns, ns, 1)
	verifyCRCreated(t, nsCRType, ns1, ns1, 1)

	// Check networkinfo cr existence
	verifyCRCreated(t, networkInfoCRType, ns, ns, 1)
	verifyCRCreated(t, networkInfoCRType, ns1, ns1, 1)

	vpcPath := ""
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(ctx, testCustomizedNetworkConfigName, v1.GetOptions{})
		t.Logf("Check VPC path of vpcnetworkconfigurations: %v", resp)
		if err != nil {
			return false, fmt.Errorf("error when waiting for vpcnetworkconfigurations VPC path: %s", testCustomizedNetworkConfigName)
		}
		if len(resp.Status.VPCs) > 0 && resp.Status.VPCs[0].VPCPath != "" {
			vpcPath = resp.Status.VPCs[0].VPCPath
			return true, nil
		}
		return false, nil
	})
	assert.NoError(t, err)

	t.Logf("................................%s", vpcPath)

	// delete ns1 and check vpc not deleted
	err = testData.deleteNamespace(ns1, defaultTimeout)
	assert.NoError(t, err)
	verifyCRDeleted(t, networkInfoCRType, ns1)
	verifyCRDeleted(t, nsCRType, ns1)
	err = testData.waitForResourceExistByPath(vpcPath, true)
	assert.NoError(t, err)
}

// update vpcnetworkconfig, and check vpc is updated
func testUpdateVPCNetworkconfigNetworkInfo(t *testing.T) {
	ns := "update-ns"

	nsPath, _ := filepath.Abs("./manifest/testVPC/update_ns.yaml")
	_ = applyYAML(nsPath, "")
	defer deleteYAML(nsPath, "")

	vncPathOriginal, _ := filepath.Abs("./manifest/testVPC/customize_networkconfig.yaml")
	defer applyYAML(vncPathOriginal, "")

	// Check namespace cr existence
	verifyCRCreated(t, nsCRType, ns, ns, 1)

	// Check networkinfo cr existence
	networkinfo_name, _ := verifyCRCreated(t, networkInfoCRType, ns, ns, 1)

	privateIPs, err := testData.getCRPropertiesByJson(defaultTimeout, networkInfoCRType, networkinfo_name, ns, ".vpcs[0].privateIPs")
	assert.True(t, strings.Contains(privateIPs, customizedPrivateCIDR1), "privateIPs %s should contain %s", privateIPs, customizedPrivateCIDR1)
	assert.True(t, strings.Contains(privateIPs, customizedPrivateCIDR2), "privateIPs %s should contain %s", privateIPs, customizedPrivateCIDR1)
	assert.NoError(t, err)

	vncPath, _ := filepath.Abs("./manifest/testVPC/customize_networkconfig_updated.yaml")
	_ = applyYAML(vncPath, "")

	privateIPs, err = testData.getCRPropertiesByJson(defaultTimeout, networkInfoCRType, networkinfo_name, ns, ".vpcs[0].privateIPs")
	assert.True(t, strings.Contains(privateIPs, customizedPrivateCIDR3), "privateIPs %s should contain %s", privateIPs, customizedPrivateCIDR3)
	assert.NoError(t, err)
}
