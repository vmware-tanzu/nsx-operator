package e2e

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

var cleanupOnce sync.Once

var (
	vcNamespaceConcurrency  = 3
	vcNamespaceReadyTimeout = 5 * time.Minute
)

// Pre-defined namespace names for e2e tests
// These namespaces are created once at the start of all tests and cleaned up at the end
var (
	// NsSecurityPolicy Security Policy test namespaces - need VC namespace for pod creation
	// NsSecurityPolicy is shared by testSecurityPolicyBasicTraffic, testSecurityPolicyMatchExpression, and testSecurityPolicyAddDeleteRule
	NsSecurityPolicy                = "e2e-sp-" + getRandomString()
	NsSecurityPolicyNamedPortClient = "e2e-sp-np-client-" + getRandomString()
	NsSecurityPolicyNamedPortWeb    = "e2e-sp-np-web-" + getRandomString()

	// NsIPv6PolicyVC IPv6 policy test namespace (VC-based, isolated from other security policy tests).
	NsIPv6PolicyVC = "e2e-ipv6-policy-vc-" + getRandomString()

	// NsInventorySync Inventory sync test namespaces - need VC namespace for pod creation
	NsInventorySync = "e2e-inventory-" + getRandomString()

	NsIPAddressAllocation = "e2e-ipalloc-" + getRandomString()

	// NsLoadBalancerLB LoadBalancer test namespaces - need VC namespace for pod creation
	NsLoadBalancerLB  = "e2e-lb-" + getRandomString()
	NsLoadBalancerPod = "e2e-lb-pod-" + getRandomString()

	// NsCreateVM VM test namespace - need VC namespace for VM creation
	NsCreateVM = "e2e-vm-" + getRandomString()

	// NsSubnetPrecreated1 Subnet precreated test namespaces
	NsSubnetPrecreated1      = "e2e-subnet-pre1-" + getRandomString()
	NsSubnetPrecreated2      = "e2e-subnet-pre2-" + getRandomString()
	NsSubnetPrecreatedTarget = "e2e-subnet-pre-target-" + getRandomString()
)

// allVCNamespaces is the list of namespaces that need to be created via VC API
// These namespaces require VC namespace because they need to create pods or VMs
var allVCNamespaces = []string{
	NsSecurityPolicy,
	NsSecurityPolicyNamedPortClient,
	NsSecurityPolicyNamedPortWeb,
	NsIPv6PolicyVC,
	NsInventorySync,
	NsLoadBalancerLB,
	NsLoadBalancerPod,
	NsCreateVM,
	NsIPAddressAllocation,
	NsSubnetPrecreated1,
	NsSubnetPrecreated2,
	NsSubnetPrecreatedTarget,
}

// InitAllNamespaces creates all required namespaces at the start of tests
// VC namespaces are created for tests that need to run pods/VMs
func InitAllNamespaces() error {
	if testData == nil {
		log.Info("Skipping batch namespace creation - testData is nil")
		return nil
	}

	// Create VC namespaces (slower, required for pod/VM creation)
	if !testData.useWCPSetup() {
		log.Info("Skipping VC namespace creation - not using WCP setup")
		return nil
	}
	if vcNamespaceConcurrency < 1 {
		return fmt.Errorf("VC namespace concurrency must be positive, got %d", vcNamespaceConcurrency)
	}
	if vcNamespaceReadyTimeout <= 0 {
		return fmt.Errorf("VC namespace readiness timeout must be positive, got %s", vcNamespaceReadyTimeout)
	}

	log.Info("Creating VC namespaces", "count", len(allVCNamespaces), "concurrency", vcNamespaceConcurrency)
	// Limit concurrent provisioning to avoid overwhelming the supervisor and network controller.
	g, ctx := errgroup.WithContext(context.Background())
	g.SetLimit(vcNamespaceConcurrency)
	for _, ns := range allVCNamespaces {
		if ctx.Err() != nil {
			break
		}
		ns := ns
		g.Go(func() error {
			if err := testData.createVCNamespace(ctx, ns); err != nil {
				log.Error(err, "Failed to create VC namespace", "namespace", ns)
				return err
			}
			log.Info("Created VC namespace", "namespace", ns)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	log.Info("All e2e namespaces created successfully")
	return nil
}

// CleanupVCNamespaces deletes the specified VC namespaces
// Use this in individual tests via deferring to clean up as soon as the test completes
// This improves concurrency by releasing resources early
func CleanupVCNamespaces(namespaces ...string) {
	if testData == nil || !testData.useWCPSetup() {
		return
	}

	g := new(errgroup.Group)
	g.SetLimit(max(1, vcNamespaceConcurrency))
	for _, ns := range namespaces {
		ns := ns
		g.Go(func() error {
			if err := testData.deleteVCNamespace(ns); err != nil {
				log.Error(err, "Failed to delete VC namespace", "namespace", ns)
			}
			return nil
		})
	}
	_ = g.Wait()
}

// CleanupAllNamespaces deletes all namespaces at the end of tests
// This is a safety net cleanup - most namespaces should be deleted by individual tests
func CleanupAllNamespaces() {
	cleanupOnce.Do(func() {
		// Safety net cleanup: most namespaces should be deleted by individual tests,
		// but we still try to clean up shared namespace here.
		log.Info("Running safety net cleanup for namespaces", "vcCount", len(allVCNamespaces))
		CleanupVCNamespaces(allVCNamespaces...)
		log.Info("Safety net cleanup completed")
	})
}
