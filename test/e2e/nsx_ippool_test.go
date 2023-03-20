// This file is for e2e ippool tests.

package e2e

import (
	"path/filepath"
	"testing"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	IPPool = "ippool"
)

// TestIPPoolBasic verifies that it could successfully realize ippool subnet from ipblock.
func TestIPPoolBasic(t *testing.T) {
	ns := "sc-a"
	name := "guestcluster-ippool-2"
	subnet_name_1 := "guestcluster1-workers-a"
	subnet_name_2 := "guestcluster1-workers-b"
	subnet_name_3 := "guestcluster1-workers-c"
	setupTest(t, ns)
	defer teardownTest(t, ns)

	// Create ippool
	ippoolPath, _ := filepath.Abs("./manifest/testIPPool/ippool.yaml")
	_ = applyYAML(ippoolPath, ns)
	defer deleteYAML(ippoolPath, ns)

	// Check ippool status
	err := testData.waitForCRReadyOrDeleted(defaultTimeout, IPPool, ns, name, Ready)
	assert_nil(t, err, "Error when waiting for Security Policy %s", name)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPool, name, true)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_1, true)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_2, true)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_3, true)
	assert_nil(t, err)

	// Delete ippool
	_ = deleteYAML(ippoolPath, ns)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPool, name, false)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_1, false)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_2, false)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_3, false)
	assert_nil(t, err)
}

// TestIPPoolAddDeleteSubnet verifies that it is as expected when adding or deleting some subnets.
func TestIPPoolAddDeleteSubnet(t *testing.T) {
	ns := "sc-a"
	name := "guestcluster-ippool-2"
	subnet_name_1 := "guestcluster1-workers-a"
	subnet_name_2 := "guestcluster1-workers-b"
	subnet_name_3 := "guestcluster1-workers-c"
	setupTest(t, ns)
	defer teardownTest(t, ns)

	// Create ippool
	ippoolPath, _ := filepath.Abs("./manifest/testIPPool/ippool.yaml")
	_ = applyYAML(ippoolPath, ns)
	defer deleteYAML(ippoolPath, ns)

	// Check ippool status
	err := testData.waitForCRReadyOrDeleted(defaultTimeout, IPPool, ns, name, Ready)
	assert_nil(t, err, "Error when waiting for Security Policy %s", name)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPool, name, true)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_1, true)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_2, true)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_3, true)
	assert_nil(t, err)

	// Delete subnet_name_2 and subnet_name_3
	ippoolDeletePath, _ := filepath.Abs("./manifest/testIPPool/ippool_delete.yaml")
	_ = applyYAML(ippoolDeletePath, ns)

	// Check ippool status
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, IPPool, ns, name, Ready)
	assert_nil(t, err, "Error when waiting for Security Policy %s", name)

	// Check nsx-t resource existing and not existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPool, name, true)
	assert_nil(t, err)
	// Still existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_1, true)
	assert_nil(t, err)
	// Deleted
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_2, false)
	assert_nil(t, err)
	// Deleted
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_3, false)
	assert_nil(t, err)

	// Add back subnet_name_2 and subnet_name_3
	_ = applyYAML(ippoolPath, ns)
	// Check ippool status
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, IPPool, ns, name, Ready)
	assert_nil(t, err, "Error when waiting for Security Policy %s", name)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPool, name, true)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_1, true)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_2, true)
	assert_nil(t, err)
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPoolBlockSubnet, subnet_name_3, true)
	assert_nil(t, err)
}

// TestIPPoolBasic verifies that it could support when subnets are nil
func TestIPPoolSubnetsNil(t *testing.T) {
	ns := "sc-a"
	name := "guestcluster-ippool-2"
	setupTest(t, ns)
	defer teardownTest(t, ns)

	// Create ippool
	ippoolPath, _ := filepath.Abs("./manifest/testIPPool/ippool.yaml")
	_ = applyYAML(ippoolPath, ns)
	defer deleteYAML(ippoolPath, ns)

	// Check ippool status
	err := testData.waitForCRReadyOrDeleted(defaultTimeout, IPPool, ns, name, Ready)
	assert_nil(t, err, "Error when waiting for Security Policy %s", name)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPool, name, true)
	assert_nil(t, err)

	// Delete ippool
	_ = deleteYAML(ippoolPath, ns)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeIPPool, name, false)
	assert_nil(t, err)
}
