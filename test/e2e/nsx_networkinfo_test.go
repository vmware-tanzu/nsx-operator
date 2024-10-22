package e2e

import (
	"log"
	"path/filepath"
	"strings"
	"testing"
)

const (
	NetworkInfoCRType     = "networkinfos.crd.nsx.vmware.com"
	NetworkConfigCRType   = "vpcnetworkconfigurations.crd.nsx.vmware.com"
	NSCRType              = "namespaces"
	PrivateIPBlockNSXType = "IpAddressBlock"

	TestCustomizedNetworkConfigName = "selfdefinedconfig"
	TestInfraNetworkConfigName      = "system"
	TestDefaultNetworkConfigName    = "default"

	InfraVPCNamespace       = "kube-system"
	SharedInfraVPCNamespace = "kube-public"

	CustomizedPrivateCIDR1 = "172.29.0.0"
	CustomizedPrivateCIDR2 = "172.39.0.0"
	CustomizedPrivateCIDR3 = "172.39.0.0"
)

func verifyCRCreated(t *testing.T, crtype string, ns string, crname string, expect int) (string, string) {
	// For CRs that do not know the name, get all resources and compare with expected
	if crname == "" {
		// get CR list with CR type
		resources, err := testData.getCRResources(defaultTimeout, crtype, ns)
		// CR list lengh should match expected length
		assertNil(t, err)
		if len(resources) != expect {
			log.Printf("%s list %s size not the same as expected %d", crtype, resources, expect)
			panic("CR creation verify failed")
		}

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
		assertNil(t, err)

		return crname, strings.TrimSpace(uid)
	}
}

func verifyCRDeleted(t *testing.T, crtype string, ns string) {
	res, _ := testData.getCRResources(defaultTimeout, crtype, ns)
	assertTrue(t, len(res) == 0, "NetworkInfo CR %s should be deleted", crtype)
}

// Test Customized NetworkInfo
func TestCustomizedNetworkInfo(t *testing.T) {
	// Create customized networkconfig
	ncPath, _ := filepath.Abs("./manifest/testVPC/customize_networkconfig.yaml")
	_ = applyYAML(ncPath, "")
	nsPath, _ := filepath.Abs("./manifest/testVPC/customize_ns.yaml")
	_ = applyYAML(nsPath, "")

	defer deleteYAML(nsPath, "")
	defer deleteYAML(ncPath, "")

	ns := "customized-ns"

	// For networkinfo CR, its CR name is the same as namespace
	verifyCRCreated(t, NetworkInfoCRType, ns, ns, 1)
	verifyCRCreated(t, NSCRType, ns, ns, 1)

	vpcPath, _ := testData.getCRPropertiesByJson(
		defaultTimeout, NetworkConfigCRType, TestCustomizedNetworkConfigName, "", ".status.vpcs[0].vpcPath")
	err := testData.waitForResourceExistByPath(vpcPath, true)
	assertNil(t, err)
}

// Test Infra NetworkInfo
func TestInfraNetworkInfo(t *testing.T) {
	// Check namespace cr existence
	verifyCRCreated(t, NSCRType, InfraVPCNamespace, InfraVPCNamespace, 1)

	// Check networkinfo cr existence
	verifyCRCreated(t, NetworkInfoCRType, InfraVPCNamespace, InfraVPCNamespace, 1)

	vpcPath, _ := testData.getCRPropertiesByJson(
		defaultTimeout, NetworkConfigCRType, TestInfraNetworkConfigName, "", ".status.vpcs[0].vpcPath")
	err := testData.waitForResourceExistByPath(vpcPath, true)
	assertNil(t, err)

	// kube-public namespace should have its own NetworkInfo CR
	// Check networkinfo cr existence
	verifyCRCreated(t, NetworkInfoCRType, SharedInfraVPCNamespace, SharedInfraVPCNamespace, 1)
}

// Test Default NetworkInfo
func TestDefaultNetworkInfo(t *testing.T) {
	// If no annotation on namespace, then NetworkInfo will use default network config to create vpc under each ns
	ns := "networkinfo-default-1"
	setupTest(t, ns)
	defer teardownTest(t, ns, defaultTimeout)

	// Check namespace cr existence
	verifyCRCreated(t, NSCRType, ns, ns, 1)

	// Check networkinfo cr existence
	verifyCRCreated(t, NetworkInfoCRType, ns, ns, 1)

	// TODO: for default network config, it maybe shared by multiple ns, and the vpc[0] may not be its VPC
	// in WCP scenario, there will be no shared network config, skip this part, leave for future if we need to
	// support non-WCP scenarios.
	// vpcPath, _ := testData.getCRPropertiesByJson(
	// 	defaultTimeout, NetworkConfigCRType, TestDefaultNetworkConfigName, "", ".status.vpcs[0].vpcPath")
	// err := testData.waitForResourceExistByPath(vpcPath, true)
	// assertNil(t, err)

	// delete namespace and check all resources are deleted
	// normally, when deleting ns, if using shared vpc, then no vpc will be deleted
	// if using normal vpc, in ns deletion, the networkconfig CR will also be deleted, so there is no
	// need to check vpcPath deletion on network config CR anymore
	err := testData.deleteNamespace(ns, defaultTimeout)
	assertNil(t, err)
	verifyCRDeleted(t, NetworkInfoCRType, ns)
	verifyCRDeleted(t, NSCRType, ns)
	// err = testData.waitForResourceExistByPath(vpcPath, false)
	assertNil(t, err)
}

// ns1 share vpc with ns, each ns should have its own NetworkInfo
// delete ns1, vpc should not be deleted
func TestSharedNSXVPC(t *testing.T) {
	ns := "shared-vpc-ns-0"
	ns1 := "shared-vpc-ns-1"

	nsPath, _ := filepath.Abs("./manifest/testVPC/shared_ns.yaml")
	_ = applyYAML(nsPath, "")
	defer deleteYAML(nsPath, "")

	// Check namespace cr existence
	verifyCRCreated(t, NSCRType, ns, ns, 1)
	verifyCRCreated(t, NSCRType, ns1, ns1, 1)

	// Check networkinfo cr existence
	verifyCRCreated(t, NetworkInfoCRType, ns, ns, 1)
	verifyCRCreated(t, NetworkInfoCRType, ns1, ns1, 1)

	vpcPath, err := testData.getCRPropertiesByJson(
		defaultTimeout, NetworkConfigCRType, TestCustomizedNetworkConfigName, "", ".status.vpcs[0].vpcPath")
	assertNil(t, err)

	t.Logf("................................%s", vpcPath)

	// delete ns1 and check vpc not deleted
	err = testData.deleteNamespace(ns1, defaultTimeout)
	assertNil(t, err)
	verifyCRDeleted(t, NetworkInfoCRType, ns1)
	verifyCRDeleted(t, NSCRType, ns1)
	// err = testData.waitForResourceExistByPath(vpcPath, true)
	// assertNil(t, err)
}

// update vpcnetworkconfig, and check vpc is updated
func TestUpdateVPCNetworkconfigNetworkInfo(t *testing.T) {
	ns := "update-ns"

	nsPath, _ := filepath.Abs("./manifest/testVPC/update_ns.yaml")
	_ = applyYAML(nsPath, "")
	defer deleteYAML(nsPath, "")

	vncPathOriginal, _ := filepath.Abs("./manifest/testVPC/customize_networkconfig.yaml")
	defer applyYAML(vncPathOriginal, "")

	// Check namespace cr existence
	verifyCRCreated(t, NSCRType, ns, ns, 1)

	// Check networkinfo cr existence
	networkinfo_name, _ := verifyCRCreated(t, NetworkInfoCRType, ns, ns, 1)

	privateIPs, err := testData.getCRPropertiesByJson(defaultTimeout, NetworkInfoCRType, networkinfo_name, ns, ".vpcs[0].privateIPs")
	assertTrue(t, strings.Contains(privateIPs, CustomizedPrivateCIDR1), "privateIPs %s should contain %s", privateIPs, CustomizedPrivateCIDR1)
	assertTrue(t, strings.Contains(privateIPs, CustomizedPrivateCIDR2), "privateIPs %s should contain %s", privateIPs, CustomizedPrivateCIDR1)
	assertNil(t, err)

	vncPath, _ := filepath.Abs("./manifest/testVPC/customize_networkconfig_updated.yaml")
	_ = applyYAML(vncPath, "")

	privateIPs, err = testData.getCRPropertiesByJson(defaultTimeout, NetworkInfoCRType, networkinfo_name, ns, ".vpcs[0].privateIPs")
	assertTrue(t, strings.Contains(privateIPs, CustomizedPrivateCIDR3), "privateIPs %s should contain %s", privateIPs, CustomizedPrivateCIDR3)
	assertNil(t, err)
}
