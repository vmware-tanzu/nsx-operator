// This file contains end-to-end tests for IPv6 support in SecurityPolicy and NetworkPolicy.
// It verifies that IPv6 CIDR blocks (including ipBlock.except) are correctly translated to
// NSX-T resources. These tests validate the resource creation path; traffic verification
// requires a dual-stack cluster and is left to targeted integration tests.

package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func testIPv6SecurityPolicy(t *testing.T) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	ns := NsIPv6PolicyVC

	securityPolicyName := "ipv6-ipblock-policy"

	// Create security policy with IPv6 ipBlocks
	yamlPath, _ := filepath.Abs("./manifest/testSecurityPolicy/ipv6-ipblock-policy.yaml")
	require.NoError(t, applyYAML(yamlPath, ns))
	defer deleteYAML(yamlPath, ns)

	assureSecurityPolicyReady(t, ns, securityPolicyName)

	// Verify NSX-T SecurityPolicy resource was created
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, true))
	log.Info("Verified IPv6 SecurityPolicy NSX resource exists", "name", securityPolicyName)

	// Cleanup
	_ = deleteYAML(yamlPath, ns)
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		_, err = testData.crdClientset.CrdV1alpha1().SecurityPolicies(ns).Get(ctx, securityPolicyName, v1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error waiting for SecurityPolicy %s deletion", securityPolicyName)
		}
		return false, nil
	})
	require.NoError(t, err)

	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, securityPolicyName, false))
	log.Info("Verified IPv6 SecurityPolicy NSX resource cleaned up", "name", securityPolicyName)
}

func testIPv6NetworkPolicy(t *testing.T) {
	ns := NsIPv6PolicyVC

	npName := "np-ipv6-ipblock"

	// Create NetworkPolicy with IPv6 ipBlock and except
	yamlPath, _ := filepath.Abs("./manifest/testNetworkPolicy/np_ipv6_ipblock.yaml")
	require.NoError(t, applyYAML(yamlPath, ns))
	defer deleteYAML(yamlPath, ns)

	// The nsx-operator converts NetworkPolicy to SecurityPolicy internally.
	// Verify that the corresponding NSX SecurityPolicy is created.
	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, npName, true))
	log.Info("Verified IPv6 NetworkPolicy -> NSX SecurityPolicy exists", "name", npName)
}

func testDualStackNetworkPolicy(t *testing.T) {
	ns := NsIPv6PolicyVC

	npName := "np-dualstack-ipblock"

	// Create NetworkPolicy with both IPv4 and IPv6 ipBlocks
	yamlPath, _ := filepath.Abs("./manifest/testNetworkPolicy/np_dualstack_ipblock.yaml")
	require.NoError(t, applyYAML(yamlPath, ns))
	defer deleteYAML(yamlPath, ns)

	assert.NoError(t, testData.waitForResourceExistOrNot(ns, common.ResourceTypeSecurityPolicy, npName, true))
	log.Info("Verified dual-stack NetworkPolicy -> NSX SecurityPolicy exists", "name", npName)
}
