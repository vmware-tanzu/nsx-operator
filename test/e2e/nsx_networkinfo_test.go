package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	NetworkInfoCRType     = "networkinfos.crd.nsx.vmware.com"
	NSCRType              = "namespaces"
	PrivateIPBlockNSXType = "IpAddressBlock"

	InfraVPCNamespace       = "kube-system"
	SharedInfraVPCNamespace = "kube-public"

	CustomizedPrivateCIDR1 = "172.29.0.0"
	CustomizedPrivateCIDR2 = "172.39.0.0"
	CustomizedPrivateCIDR3 = "172.39.0.0"
)

func verifyCRCreated(t *testing.T, crtype string, ns string, expect int) (string, string) {
	// there should be one networkinfo created
	resources, err := testData.getCRResource(defaultTimeout, crtype, ns)
	// only one networkinfo should be created under ns using default network config
	assert.Equal(t, expect, len(resources), "NetworkInfo CR creation verify failed")
	assertNil(t, err)

	cr_name, cr_uid := "", ""
	// waiting for CR to be ready
	for k, v := range resources {
		cr_name = k
		cr_uid = strings.TrimSpace(v)
	}

	return cr_name, cr_uid
}

func verifyCRDeleted(t *testing.T, crtype string, ns string) {
	res, _ := testData.getCRResource(defaultTimeout, crtype, ns)
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

	ns := "customized-ns"

	networkinfo_name, _ := verifyCRCreated(t, NetworkInfoCRType, ns, 1)
	verifyCRCreated(t, NSCRType, ns, 1)

	vpcPath, _ := testData.getCRPropertiesByJson(defaultTimeout, NetworkInfoCRType, networkinfo_name, ns, ".vpcs[0].vpcPath")
	err := testData.waitForResourceExistByPath(vpcPath, true)
	assertNil(t, err)
}

// Test Infra NetworkInfo
func TestInfraNetworkInfo(t *testing.T) {
	// Check namespace cr existence
	verifyCRCreated(t, NSCRType, InfraVPCNamespace, 1)
	// Check networkinfo cr existence
	networkinfo_name, _ := verifyCRCreated(t, NetworkInfoCRType, InfraVPCNamespace, 1)

	vpcPath, _ := testData.getCRPropertiesByJson(defaultTimeout, NetworkInfoCRType, networkinfo_name, InfraVPCNamespace, ".vpcs[0].vpcPath")
	err := testData.waitForResourceExistByPath(vpcPath, true)
	assertNil(t, err)

	// kube-public vpcpath should be the same as kube-system vpcpath
	networkinfo_name = SharedInfraVPCNamespace
	vpcPath2, err := testData.getCRPropertiesByJson(defaultTimeout, NetworkInfoCRType, networkinfo_name, SharedInfraVPCNamespace,
		".vpcs[0].vpcPath")
	assertNil(t, err)
	assertTrue(t, vpcPath == vpcPath2, "vpcPath %s should be the same as vpcPath2 %s", vpcPath, vpcPath2)
}

// Test Default NetworkInfo
func TestDefaultNetworkInfo(t *testing.T) {
	// If no annotation on namespace, then NetworkInfo will use default network config to create vpc under each ns
	ns := "networkinfo-default-1"
	setupTest(t, ns)
	defer teardownTest(t, ns, defaultTimeout)

	// Check namespace cr existence
	verifyCRCreated(t, NSCRType, ns, 1)
	// Check networkinfo cr existence
	networkinfo_name, _ := verifyCRCreated(t, NetworkInfoCRType, ns, 1)

	vpcPath, _ := testData.getCRPropertiesByJson(defaultTimeout, NetworkInfoCRType, networkinfo_name, ns, ".vpcs[0].vpcPath")
	err := testData.waitForResourceExistByPath(vpcPath, true)
	assertNil(t, err)

	// delete namespace and check all resources are deleted
	err = testData.deleteNamespace(ns, defaultTimeout)
	assertNil(t, err)
	verifyCRDeleted(t, NetworkInfoCRType, ns)
	verifyCRDeleted(t, NSCRType, ns)
	err = testData.waitForResourceExistByPath(vpcPath, false)
	assertNil(t, err)
}

// ns1 share vpc with ns, delete ns1, vpc should not be deleted
func TestSharedNetworkInfo(t *testing.T) {
	ns := "shared-vpc-ns-0"
	ns1 := "shared-vpc-ns-1"

	nsPath, _ := filepath.Abs("./manifest/testVPC/shared_ns.yaml")
	_ = applyYAML(nsPath, "")
	defer deleteYAML(nsPath, "")

	// Check namespace cr existence
	verifyCRCreated(t, NSCRType, ns, 1)
	_, _ = verifyCRCreated(t, NSCRType, ns1, 1)
	// Check networkinfo cr existence
	networkinfo_name, _ := verifyCRCreated(t, NetworkInfoCRType, ns, 1)
	networkinfo_name_1, _ := verifyCRCreated(t, NetworkInfoCRType, ns1, 1)

	vpcPath, _ := testData.getCRPropertiesByJson(defaultTimeout, NetworkInfoCRType, networkinfo_name, ns, ".vpcs[0].vpcPath")
	err := testData.waitForResourceExistByPath(vpcPath, true)
	assertNil(t, err)
	vpcPath1, _ := testData.getCRPropertiesByJson(defaultTimeout, NetworkInfoCRType, networkinfo_name_1, ns1, ".vpcs[0].vpcPath")
	err = testData.waitForResourceExistByPath(vpcPath1, true)
	assertNil(t, err)

	assertTrue(t, vpcPath == vpcPath1, "vpcPath %s should be the same as vpcPath2 %s", vpcPath, vpcPath1)

	// delete ns1 and check vpc not deleted
	err = testData.deleteNamespace(ns1, defaultTimeout)
	assertNil(t, err)
	verifyCRDeleted(t, NetworkInfoCRType, ns1)
	verifyCRDeleted(t, NSCRType, ns1)
	err = testData.waitForResourceExistByPath(vpcPath, true)
	assertNil(t, err)
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
	verifyCRCreated(t, NSCRType, ns, 1)
	// Check networkinfo cr existence
	networkinfo_name, _ := verifyCRCreated(t, NetworkInfoCRType, ns, 1)

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
