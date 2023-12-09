// This file is for e2e StaticRoute tests.

package e2e

import (
	"path/filepath"
	"testing"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	StaticRoute = "StaticRoute"
)

// TestStaticRouteBasic verifies that it could successfully realize StaticRoute.
func TestStaticRouteBasic(t *testing.T) {
	ns := "sc-a"
	name := "guestcluster-staticroute-2"
	setupTest(t, ns)
	defer teardownTest(t, ns)

	// Create StaticRoute
	StaticRoutePath, _ := filepath.Abs("./manifest/testStaticRoute/staticroute.yaml")
	err := applyYAML(StaticRoutePath, ns)
	if err != nil {
		t.Fatalf("Failed to apply StaticRoute YAML file: %v", err)
		assert_nil(t, err)
	}

	// Check StaticRoute status
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, StaticRoute, ns, name, Ready)
	assert_nil(t, err, "Error when waiting for Static Route %s", name)

	// Check nsx-t resource existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeStaticRoute, name, true)
	assert_nil(t, err)

	// Delete StaticRoute
	_ = deleteYAML(StaticRoutePath, ns)

	// Check nsx-t resource not existing
	err = testData.waitForResourceExistOrNot(ns, common.ResourceTypeStaticRoute, name, false)
	assert_nil(t, err)
}
