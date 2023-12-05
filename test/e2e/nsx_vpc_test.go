package e2e

import (
	"log"
	"path/filepath"
	"strings"
	"testing"
)

const (
	VPCCRType             = "vpcs"
	VPCNSXType            = "Vpc"
	PrivateIPBlockNSXType = "IpAddressBlock"

	InfraVPCNamespace       = "kube-system"
	SharedInfraVPCNamespace = "kube-public"

	DefaultPrivateCIDR1    = "172.28.0.0"
	DefaultPrivateCIDR2    = "172.38.0.0"
	InfraPrivateCIDR1      = "172.27.0.0"
	InfraPrivateCIDR2      = "172.37.0.0"
	CustomizedPrivateCIDR1 = "172.29.0.0"
	CustomizedPrivateCIDR2 = "172.39.0.0"
)

var (
	verify_keys = []string{"defaultSNATIP", "lbSubnetCIDR", "lbSubnetPath", "nsxResourcePath"}
)

func verifyVPCCRCreated(t *testing.T, ns string, expect int) (string, string) {
	// there should be one vpc created
	resources, err := testData.getCRResource(defaultTimeout, VPCCRType, ns)
	// only one vpc should be created under ns using default network config
	if len(resources) != expect {
		log.Printf("VPC list %s size not the same as expected %d", resources, expect)
		panic("VPC CR creation verify failed")
	}
	assert_nil(t, err)

	var vpc_name, vpc_uid string = "", ""
	// waiting for CR to be ready
	for k, v := range resources {
		vpc_name = k
		vpc_uid = strings.TrimSpace(v)
	}

	return vpc_name, vpc_uid
}

func verifyPrivateIPBlockCreated(t *testing.T, ns, id string) {
	err := testData.waitForResourceExistById(ns, PrivateIPBlockNSXType, id, true)
	assert_nil(t, err)
}

func verifyVPCCRProperties(t *testing.T, ns, vpc_name string) {
	for _, key := range verify_keys {
		value, err := testData.getCRProperties(defaultTimeout, VPCCRType, vpc_name, ns, key)
		assert_nil(t, err)
		if strings.TrimSpace(value) == "" {
			log.Printf("failed to read key %s for VPC %s", key, vpc_name)
			panic("failed to read attribute from VPC CR")
		}
	}
}

// Test Customized VPC
func TestCustomizedVPC(t *testing.T) {
	// Create customized networkconfig
	ncPath, _ := filepath.Abs("./manifest/testVPC/customize_networkconfig.yaml")
	_ = applyYAML(ncPath, "")
	nsPath, _ := filepath.Abs("./manifest/testVPC/customize_ns.yaml")
	_ = applyYAML(nsPath, "")

	defer deleteYAML(nsPath, "")
	defer deleteYAML(ncPath, "")

	ns := "customized-ns"

	vpc_name, vpc_uid := verifyVPCCRCreated(t, ns, 1)

	err := testData.waitForCRReadyOrDeleted(defaultTimeout, VPCCRType, ns, vpc_name, Ready)
	assert_nil(t, err, "Error when waiting for VPC %s", vpc_name)

	verifyVPCCRProperties(t, ns, vpc_name)

	// Check nsx-t resource existing, nsx vpc is using vpc cr uid as id
	err = testData.waitForResourceExistById(ns, VPCNSXType, vpc_uid, true)
	assert_nil(t, err)

	//verify private ipblocks created for vpc
	p_ipb_id1 := vpc_uid + "_" + CustomizedPrivateCIDR1
	p_ipb_id2 := vpc_uid + "_" + CustomizedPrivateCIDR2

	verifyPrivateIPBlockCreated(t, ns, p_ipb_id1)
	verifyPrivateIPBlockCreated(t, ns, p_ipb_id2)
}

// Test Infra VPC
func TestInfraVPC(t *testing.T) {
	// there should be one shared vpc created under namespace kube-system
	vpc_name, vpc_uid := verifyVPCCRCreated(t, InfraVPCNamespace, 1)

	err := testData.waitForCRReadyOrDeleted(defaultTimeout, VPCCRType, InfraVPCNamespace, vpc_name, Ready)
	assert_nil(t, err, "Error when waiting for VPC %s", vpc_name)

	verifyVPCCRProperties(t, InfraVPCNamespace, vpc_name)

	// Check nsx-t resource existing, nsx vpc is using vpc cr uid as id
	err = testData.waitForResourceExistById(InfraVPCNamespace, VPCNSXType, vpc_uid, true)
	assert_nil(t, err)

	//verify private ipblocks created for vpc
	p_ipb_id1 := vpc_uid + "_" + InfraPrivateCIDR1
	p_ipb_id2 := vpc_uid + "_" + InfraPrivateCIDR2

	verifyPrivateIPBlockCreated(t, InfraVPCNamespace, p_ipb_id1)
	verifyPrivateIPBlockCreated(t, InfraVPCNamespace, p_ipb_id2)

	// there should be no VPC exist under namespace kube-public
	_, _ = verifyVPCCRCreated(t, SharedInfraVPCNamespace, 0)
}

// Test Default VPC
func TestDefaultVPC(t *testing.T) {
	// If no annotation on namespace, then VPC will use default network config to create
	// VPC under each ns
	ns := "vpc-default-1"
	setupTest(t, ns)
	defer teardownTest(t, ns, defaultTimeout)

	// Check vpc cr existence
	vpc_name, vpc_uid := verifyVPCCRCreated(t, ns, 1)

	err := testData.waitForCRReadyOrDeleted(defaultTimeout, VPCCRType, ns, vpc_name, Ready)
	assert_nil(t, err, "Error when waiting for VPC %s", vpc_name)

	verifyVPCCRProperties(t, ns, vpc_name)

	// Check nsx-t resource existing, nsx vpc is using vpc cr uid as id
	err = testData.waitForResourceExistById(ns, VPCNSXType, vpc_uid, true)
	assert_nil(t, err)

	//verify private ipblocks created for vpc
	p_ipb_id1 := vpc_uid + "_" + DefaultPrivateCIDR1
	p_ipb_id2 := vpc_uid + "_" + DefaultPrivateCIDR2

	verifyPrivateIPBlockCreated(t, ns, p_ipb_id1)
	verifyPrivateIPBlockCreated(t, ns, p_ipb_id2)
}
