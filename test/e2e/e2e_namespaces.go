package e2e

import "time"

// Pre-defined namespace names for e2e tests
// These namespaces are created once at the start of all tests and cleaned up at the end
var (
	// Security Policy test namespaces - need VC namespace for pod creation
	// NsSecurityPolicy is shared by testSecurityPolicyBasicTraffic, testSecurityPolicyMatchExpression, and testSecurityPolicyAddDeleteRule
	NsSecurityPolicy                = "e2e-sp-" + getRandomString()
	NsSecurityPolicyNamedPortClient = "e2e-sp-np-client-" + getRandomString()
	NsSecurityPolicyNamedPortWeb    = "e2e-sp-np-web-" + getRandomString()

	// Inventory sync test namespaces - need VC namespace for pod creation
	NsInventorySync = "e2e-inventory-" + getRandomString()

	// IPAddressAllocation test namespace - only CRD operations, no pod/vm
	NsIPAddressAllocation = "e2e-ipalloc-" + getRandomString()

	// LoadBalancer test namespaces - need VC namespace for pod creation
	NsLoadBalancerLB  = "e2e-lb-" + getRandomString()
	NsLoadBalancerPod = "e2e-lb-pod-" + getRandomString()

	// VM test namespace - need VC namespace for VM creation
	NsCreateVM = "e2e-vm-" + getRandomString()

	// Subnet precreated test namespaces - only CRD operations, no pod/vm
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
	NsInventorySync,
	NsLoadBalancerLB,
	NsLoadBalancerPod,
	NsCreateVM,
}

// allK8sNamespaces is the list of namespaces that can be created via K8s API
// These namespaces only perform CRD operations without creating pods or VMs
// Using K8s API is faster than VC API
var allK8sNamespaces = []string{
	NsIPAddressAllocation,
	NsSubnetPrecreated1,
	NsSubnetPrecreated2,
	NsSubnetPrecreatedTarget,
}

// InitAllNamespaces creates all required namespaces at the start of tests
// VC namespaces are created for tests that need to run pods/VMs
// K8s namespaces are created for tests that only operate on CRDs
func InitAllNamespaces() error {
	if testData == nil {
		log.Info("Skipping batch namespace creation - testData is nil")
		return nil
	}

	// Create K8s namespaces first (faster)
	log.Info("Creating K8s namespaces", "count", len(allK8sNamespaces))
	for _, ns := range allK8sNamespaces {
		if err := testData.createNamespace(ns); err != nil {
			log.Error(err, "Failed to create K8s namespace", "namespace", ns)
			return err
		}
		log.Info("Created K8s namespace", "namespace", ns)
	}

	// Create VC namespaces (slower, required for pod/VM creation)
	if !testData.useWCPSetup() {
		log.Info("Skipping VC namespace creation - not using WCP setup")
		return nil
	}

	log.Info("Creating VC namespaces", "count", len(allVCNamespaces))
	// Create namespaces sequentially to avoid session race conditions
	for _, ns := range allVCNamespaces {
		if err := testData.createVCNamespace(ns); err != nil {
			log.Error(err, "Failed to create VC namespace", "namespace", ns)
			return err
		}
		log.Info("Created VC namespace", "namespace", ns)
	}

	log.Info("All e2e namespaces created successfully")
	return nil
}

// CleanupAllNamespaces deletes all namespaces at the end of tests
func CleanupAllNamespaces() {
	if testData == nil {
		log.Info("Skipping batch namespace cleanup - testData is nil")
		return
	}

	// Delete VC namespaces first
	if testData.useWCPSetup() {
		log.Info("Deleting VC namespaces", "count", len(allVCNamespaces))
		for _, ns := range allVCNamespaces {
			if err := testData.deleteVCNamespace(ns); err != nil {
				log.Error(err, "Failed to delete VC namespace", "namespace", ns)
			} else {
				log.Info("Deleted VC namespace", "namespace", ns)
			}
		}
	}

	// Delete K8s namespaces
	log.Info("Deleting K8s namespaces", "count", len(allK8sNamespaces))
	for _, ns := range allK8sNamespaces {
		if err := testData.deleteNamespace(ns, 60*time.Second); err != nil {
			log.Error(err, "Failed to delete K8s namespace", "namespace", ns)
		} else {
			log.Info("Deleted K8s namespace", "namespace", ns)
		}
	}

	log.Info("All e2e namespaces cleanup completed")
}
