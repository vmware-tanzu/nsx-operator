package e2e

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v12 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	testInfraNetworkConfigName = "system"

	infraVPCNamespace       = "kube-system"
	sharedInfraVPCNamespace = "kube-public"

	customizedPrivateCIDR1 = "172.29.0.0/16"
	customizedPrivateCIDR2 = "172.39.0.0/16"
	customizedPrivateCIDR3 = "172.49.0.0/16"
)

func TestNetworkInfo(t *testing.T) {
	TrackTest(t)
	StartParallel(t)

	// ParallelTests: These tests use independent resources and can run concurrently
	RunSubtest(t, "ParallelTests", func(t *testing.T) {
		RunSubtest(t, "testInfraNetworkInfo", func(t *testing.T) {
			StartParallel(t)
			testInfraNetworkInfo(t)
		})
		RunSubtest(t, "testDefaultNetworkInfo", func(t *testing.T) {
			StartParallel(t)
			testDefaultNetworkInfo(t)
		})
		RunSubtest(t, "testLBBackendIPsPopulated", func(t *testing.T) {
			StartParallel(t)
			testLBBackendIPsPopulated(t)
		})
		RunSubtest(t, "testDualStackLBBackendIPs", func(t *testing.T) {
			StartParallel(t)
			testDualStackLBBackendIPs(t)
		})
		RunSubtest(t, "testLBBackendIPsIdempotent", func(t *testing.T) {
			StartParallel(t)
			testLBBackendIPsIdempotent(t)
		})
	})

	// These tests share one configuration within this run and must run sequentially.
	RunSubtest(t, "SequentialTests", func(t *testing.T) {
		networkConfigName := fmt.Sprintf("selfdefinedconfig-%s", getRandomString())
		customizeNetworkconfig := &v1alpha1.VPCNetworkConfiguration{
			ObjectMeta: v1.ObjectMeta{Name: networkConfigName},
			Spec: v1alpha1.VPCNetworkConfigurationSpec{
				DefaultSubnetSize:      32,
				NSXProject:             "/orgs/default/projects/project-quality",
				PrivateIPs:             []string{customizedPrivateCIDR1, customizedPrivateCIDR2},
				VPCConnectivityProfile: fmt.Sprintf("/orgs/default/projects/project-quality/vpc-connectivity-profiles/%s", defaultVPCProfile),
			},
		}
		_, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Create(context.TODO(), customizeNetworkconfig, v1.CreateOptions{})
		require.NoError(t, err)
		t.Cleanup(func() { deleteVPCNetworkConfiguration(t, networkConfigName) })

		RunSubtest(t, "testCustomizedNetworkInfo", func(t *testing.T) {
			testCustomizedNetworkInfo(t, networkConfigName)
		})
		RunSubtest(t, "testSharedNSXVPC", func(t *testing.T) {
			testSharedNSXVPC(t, networkConfigName)
		})
		RunSubtest(t, "testUpdateVPCNetworkconfigNetworkInfo", func(t *testing.T) {
			testUpdateVPCNetworkconfigNetworkInfo(t, networkConfigName)
		})
	})
}

// Test Customized NetworkInfo
func testCustomizedNetworkInfo(t *testing.T, networkConfigName string) {
	ns := fmt.Sprintf("customized-ns-%s", getRandomString())
	namespace := &v12.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name: ns,
			Annotations: map[string]string{
				common.AnnotationVPCNetworkConfig: networkConfigName,
			},
		},
	}
	_, err := testData.clientset.CoreV1().Namespaces().Create(context.TODO(), namespace, v1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { teardownTest(t, ns, defaultTimeout) })

	// For networkinfo CR, its CR name is the same as namespace
	networkInfo := assureNetworkInfo(t, ns, ns)
	assureNamespace(t, ns)

	vpcPath := getVPCPathFromVPCNetworkConfiguration(t, networkConfigName, networkInfo.VPCs[0].Name)
	require.NoError(t, testData.waitForResourceExistByPath(vpcPath, true))

	// Delete namespace and wait for VPC to be cleaned up from VPCNetworkConfiguration status
	// This is important because the next test (testSharedNSXVPC) uses the same VPCNetworkConfiguration
	require.NoError(t, testData.deleteNamespace(ns, defaultTimeout))
	assureNetworkInfoDeleted(t, ns)
	assureNamespaceDeleted(t, ns)
	// Wait for VPC to be deleted from NSX and VPCNetworkConfiguration status to be cleared
	require.NoError(t, testData.waitForResourceExistByPath(vpcPath, false))
	assureVPCNetworkConfigurationStatusEmpty(t, networkConfigName)
}

// Test Infra NetworkInfo
func testInfraNetworkInfo(t *testing.T) {
	// Check namespace cr existence
	assureNamespace(t, infraVPCNamespace)

	// Check networkinfo cr existence
	networkInfo := assureNetworkInfo(t, infraVPCNamespace, infraVPCNamespace)
	require.NotNil(t, networkInfo)

	vpcPath := getVPCPathFromVPCNetworkConfiguration(t, testInfraNetworkConfigName, networkInfo.VPCs[0].Name)
	require.NoError(t, testData.waitForResourceExistByPath(vpcPath, true))

	// kube-public namespace should have its own NetworkInfo CR
	// Check networkinfo cr existence
	assureNetworkInfo(t, sharedInfraVPCNamespace, sharedInfraVPCNamespace)
}

// Test Default NetworkInfo
func testDefaultNetworkInfo(t *testing.T) {
	// If no annotation on namespace, then NetworkInfo will use default network config to create vpc under each ns
	ns := fmt.Sprintf("networkinfo-default-%s", getRandomString())
	setupTest(t, ns)
	defer teardownTest(t, ns, defaultTimeout)

	// Check namespace cr existence
	assureNamespace(t, ns)

	// Check networkinfo cr existence
	assureNetworkInfo(t, ns, ns)

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
	require.NoError(t, testData.deleteNamespace(ns, defaultTimeout))
	assureNetworkInfoDeleted(t, ns)
	assureNamespaceDeleted(t, ns)
}

// ns1 share vpc with ns, each ns should have its own NetworkInfo
// delete ns1, vpc should not be deleted
func testSharedNSXVPC(t *testing.T, networkConfigName string) {
	ns := fmt.Sprintf("shared-vpc-ns-0-%s", getRandomString())
	ns1 := fmt.Sprintf("shared-vpc-ns-1-%s", getRandomString())

	namespace := &v12.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name: ns,
			Annotations: map[string]string{
				common.AnnotationVPCNetworkConfig: networkConfigName,
			},
		},
	}
	_, err := testData.clientset.CoreV1().Namespaces().Create(context.TODO(), namespace, v1.CreateOptions{})
	require.NoError(t, err)
	var vpcPath string
	t.Cleanup(func() {
		teardownTest(t, ns, defaultTimeout)
		if vpcPath != "" {
			require.NoError(t, testData.waitForResourceExistByPath(vpcPath, false))
		}
		assureVPCNetworkConfigurationStatusEmpty(t, networkConfigName)
	})

	namespace1 := &v12.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name: ns1,
			Annotations: map[string]string{
				common.AnnotationVPCNetworkConfig:   networkConfigName,
				common.AnnotationSharedVPCNamespace: ns,
			},
		},
	}
	_, err = testData.clientset.CoreV1().Namespaces().Create(context.TODO(), namespace1, v1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { teardownTest(t, ns1, defaultTimeout) })

	// Check namespace cr existence
	assureNamespace(t, ns)
	assureNamespace(t, ns1)

	// Check networkinfo cr existence
	networkInfo := assureNetworkInfo(t, ns, ns)
	sharedNetworkInfo := assureNetworkInfo(t, ns1, ns1)
	require.Equal(t, networkInfo.VPCs[0].Name, sharedNetworkInfo.VPCs[0].Name,
		"the shared namespace must use the owner's VPC")

	vpcPath = getVPCPathFromVPCNetworkConfiguration(t, networkConfigName, networkInfo.VPCs[0].Name)

	// delete ns1 and check vpc not deleted
	require.NoError(t, testData.deleteNamespace(ns1, defaultTimeout))
	assureNetworkInfoDeleted(t, ns1)
	assureNamespaceDeleted(t, ns1)
	require.NoError(t, testData.waitForResourceExistByPath(vpcPath, true))
}

// update vpcnetworkconfig, and check vpc is updated
func testUpdateVPCNetworkconfigNetworkInfo(t *testing.T, networkConfigName string) {
	ns := fmt.Sprintf("update-ns-%s", getRandomString())

	// Create the namespace with the required annotation using the clientset
	namespace := &v12.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name: ns,
			Annotations: map[string]string{
				common.AnnotationVPCNetworkConfig: networkConfigName,
			},
		},
	}
	_, err := testData.clientset.CoreV1().Namespaces().Create(context.TODO(), namespace, v1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { teardownTest(t, ns, defaultTimeout) })

	// Check namespace cr existence
	assureNamespace(t, ns)

	// Check networkinfo cr existence
	networkInfo := assureNetworkInfo(t, ns, ns)
	require.NotNil(t, networkInfo)

	networkInfoNew := getNetworkInfoWithCondition(t, ns, networkInfo.Name, func(networkInfo *v1alpha1.NetworkInfo) (bool, error) {
		return len(networkInfo.VPCs) > 0 &&
			slices.Contains(networkInfo.VPCs[0].PrivateIPs, customizedPrivateCIDR1) &&
			slices.Contains(networkInfo.VPCs[0].PrivateIPs, customizedPrivateCIDR2), nil
	})
	privateIPs := networkInfoNew.VPCs[0].PrivateIPs
	assert.Contains(t, privateIPs, customizedPrivateCIDR1, "privateIPs %s should contain %s", privateIPs, customizedPrivateCIDR1)
	assert.Contains(t, privateIPs, customizedPrivateCIDR2, "privateIPs %s should contain %s", privateIPs, customizedPrivateCIDR2)

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		customizeNetworkconfig, getErr := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(context.Background(), networkConfigName, v1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		if !slices.Contains(customizeNetworkconfig.Spec.PrivateIPs, customizedPrivateCIDR3) {
			customizeNetworkconfig.Spec.PrivateIPs = append(customizeNetworkconfig.Spec.PrivateIPs, customizedPrivateCIDR3)
		}
		_, updateErr := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Update(context.Background(), customizeNetworkconfig, v1.UpdateOptions{})
		return updateErr
	})
	require.NoError(t, err)

	networkInfoNew = getNetworkInfoWithCondition(t, ns, networkInfo.Name, func(networkInfo *v1alpha1.NetworkInfo) (bool, error) {
		return len(networkInfo.VPCs) > 0 && slices.Contains(networkInfo.VPCs[0].PrivateIPs, customizedPrivateCIDR3), nil
	})
	privateIPs = networkInfoNew.VPCs[0].PrivateIPs
	assert.Contains(t, privateIPs, customizedPrivateCIDR3, "privateIPs %s should contain %s", privateIPs, customizedPrivateCIDR3)
}

func assureNetworkInfo(t *testing.T, ns, networkInfoName string) (res *v1alpha1.NetworkInfo) {
	t.Helper()
	return getNetworkInfoWithCondition(t, ns, networkInfoName, func(networkInfo *v1alpha1.NetworkInfo) (bool, error) {
		return len(networkInfo.VPCs) > 0 && networkInfo.VPCs[0].Name != "", nil
	})
}

func assureNetworkInfoDeleted(t *testing.T, ns string) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		err = testData.crdClientset.CrdV1alpha1().NetworkInfos(ns).Delete(context.Background(), ns, v1.DeleteOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when deleting Namespace %s", ns)
		}
		_, err = testData.crdClientset.CrdV1alpha1().NetworkInfos(ns).Get(context.Background(), ns, v1.GetOptions{})
		log.Trace("Deleting NetworkInfos", "Namespace", ns, "Name", ns)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for %s", ns)
		}
		return false, nil
	})
	require.NoError(t, err)
}

func assureNamespace(t *testing.T, ns string) (res *v12.Namespace) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		res, err = testData.clientset.CoreV1().Namespaces().Get(context.Background(), ns, v1.GetOptions{})
		log.Trace("Get Namespaces", "Name", ns)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when waiting for Namespaces %s", ns)
		}
		return true, nil
	})
	require.NoError(t, err)
	return res
}

func assureNamespaceDeleted(t *testing.T, ns string) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		err = testData.clientset.CoreV1().Namespaces().Delete(context.Background(), ns, v1.DeleteOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when deleting Namespace %s", ns)
		}
		_, err = testData.clientset.CoreV1().Namespaces().Get(context.Background(), ns, v1.GetOptions{})
		log.Trace("Deleting Namespace", "Name", ns)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for %s", ns)
		}
		return false, nil
	})
	require.NoError(t, err)
}

func getNetworkInfoWithCondition(t *testing.T, ns, networkInfoName string, condition func(networkInfo *v1alpha1.NetworkInfo) (bool, error)) (networkInfo *v1alpha1.NetworkInfo) {
	t.Helper()
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		networkInfo, err = testData.crdClientset.CrdV1alpha1().NetworkInfos(ns).Get(ctx, networkInfoName, v1.GetOptions{})
		if err != nil {
			log.Trace("Check networkinfo status", "error", err)
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when waiting for NetworkInfo %s/%s: %w", ns, networkInfoName, err)
		}
		return condition(networkInfo)
	})
	if err != nil {
		testData.logNamespaceWaitDiagnostics(ns, nil)
	}
	require.NoError(t, err, "waiting for NetworkInfo %s/%s, last object: %+v", ns, networkInfoName, networkInfo)
	return networkInfo
}

func getVPCPathFromVPCNetworkConfiguration(t *testing.T, ncName, vpcName string) (vpcPath string) {
	t.Helper()
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(ctx, ncName, v1.GetOptions{})
		if err != nil {
			log.Trace("Check VPC path of vpcnetworkconfigurations", "error", err)
			return false, fmt.Errorf("error when waiting for vpcnetworkconfigurations VPC path: %s: %v", ncName, err)
		}
		for _, vpc := range resp.Status.VPCs {
			if vpc.Name == vpcName && vpc.VPCPath != "" {
				vpcPath = vpc.VPCPath
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err, "waiting for VPC %s in VPCNetworkConfiguration %s", vpcName, ncName)
	return vpcPath
}

func assureVPCNetworkConfigurationStatusEmpty(t *testing.T, ncName string) {
	t.Helper()
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	var lastVPCs []v1alpha1.VPCInfo
	var lastGetErr error
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(ctx, ncName, v1.GetOptions{})
		lastGetErr = err
		if err != nil {
			log.Trace("Check VPCNetworkConfiguration status", "error", err)
			return false, fmt.Errorf("error when getting VPCNetworkConfiguration %s: %v", ncName, err)
		}
		lastVPCs = resp.Status.VPCs
		if len(lastVPCs) == 0 {
			return true, nil
		}
		log.Trace("Waiting for VPCNetworkConfiguration status to be empty", "ncName", ncName, "currentVPCs", resp.Status.VPCs)
		return false, nil
	})
	require.NoError(t, err, "waiting for VPCNetworkConfiguration %s status to be empty; last VPCs: %+v; last GET error: %v", ncName, lastVPCs, lastGetErr)
}

func deleteVPCNetworkConfiguration(t *testing.T, ncName string) {
	t.Helper()
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		err = testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Delete(ctx, ncName, v1.DeleteOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("error when deleting VPCNetworkConfiguration %s: %w", ncName, err)
		}
		log.Trace("Delete VPCNetworkConfigurations", "name", ncName)

		_, err = testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(ctx, ncName, v1.GetOptions{})
		if err != nil {
			log.Trace("Check stale vpcnetworkconfigurations", "error", err)
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for VPCNetworkConfiguration %s deletion: %w", ncName, err)
		}
		return false, nil
	})
	require.NoError(t, err)
}

// testLBBackendIPsPopulated verifies that LoadBalancerBackendIPs is populated for a
// VPC with LB configured, and that every entry is a valid IP or CIDR. It also confirms
// that LoadBalancerIPAddresses (the primary field) appears in the backend list.
func testLBBackendIPsPopulated(t *testing.T) {
	assureNamespace(t, infraVPCNamespace)

	networkInfo := getNetworkInfoWithCondition(t, infraVPCNamespace, infraVPCNamespace,
		func(ni *v1alpha1.NetworkInfo) (bool, error) {
			if len(ni.VPCs) == 0 {
				return false, nil
			}
			return len(ni.VPCs[0].LoadBalancerBackendIPs) > 0, nil
		})

	require.NotNil(t, networkInfo)
	require.NotEmpty(t, networkInfo.VPCs)
	vpc := networkInfo.VPCs[0]

	if vpc.LoadBalancerIPAddresses != "" {
		assert.Contains(t, vpc.LoadBalancerBackendIPs, vpc.LoadBalancerIPAddresses,
			"LoadBalancerIPAddresses must appear in LoadBalancerBackendIPs")
	}

	for _, entry := range vpc.LoadBalancerBackendIPs {
		raw := entry
		if i := strings.Index(entry, "/"); i >= 0 {
			raw = entry[:i]
		}
		assert.NotNil(t, net.ParseIP(raw),
			"LoadBalancerBackendIPs entry %q is not a valid IP or CIDR", entry)
	}
}

// testDualStackLBBackendIPs verifies that on a dual-stack cluster the backend list
// contains at least one IPv4 and one IPv6 entry, and that LoadBalancerIPAddresses
// (the primary field) is the IPv4 entry per the k8s convention.
func testDualStackLBBackendIPs(t *testing.T) {
	if clusterInfo.podV6NetworkCIDR == "" {
		t.Skip("cluster does not support IPv6; skipping dual-stack LB backend IPs test")
	}

	assureNamespace(t, infraVPCNamespace)

	networkInfo := getNetworkInfoWithCondition(t, infraVPCNamespace, infraVPCNamespace,
		func(ni *v1alpha1.NetworkInfo) (bool, error) {
			if len(ni.VPCs) == 0 {
				return false, nil
			}
			return len(ni.VPCs[0].LoadBalancerBackendIPs) >= 2, nil
		})

	require.NotNil(t, networkInfo)
	require.NotEmpty(t, networkInfo.VPCs)
	vpc := networkInfo.VPCs[0]

	var hasIPv4, hasIPv6 bool
	for _, entry := range vpc.LoadBalancerBackendIPs {
		raw := entry
		if i := strings.Index(entry, "/"); i >= 0 {
			raw = entry[:i]
		}
		ip := net.ParseIP(raw)
		require.NotNil(t, ip, "invalid IP/CIDR in LoadBalancerBackendIPs: %q", entry)
		if ip.To4() != nil {
			hasIPv4 = true
		} else {
			hasIPv6 = true
		}
	}
	assert.True(t, hasIPv4, "expected at least one IPv4 entry in LoadBalancerBackendIPs")
	assert.True(t, hasIPv6, "expected at least one IPv6 entry in LoadBalancerBackendIPs")

	if vpc.LoadBalancerIPAddresses != "" {
		raw := vpc.LoadBalancerIPAddresses
		if i := strings.Index(raw, "/"); i >= 0 {
			raw = raw[:i]
		}
		ip := net.ParseIP(raw)
		require.NotNil(t, ip, "LoadBalancerIPAddresses is not a valid IP/CIDR: %q", vpc.LoadBalancerIPAddresses)
		assert.NotNil(t, ip.To4(),
			"LoadBalancerIPAddresses should be IPv4 on a dual-stack cluster (k8s convention)")
	}
}

// testLBBackendIPsIdempotent verifies that successive reconcile loops do not produce
// spurious CR updates once LoadBalancerBackendIPs is already correctly populated.
func testLBBackendIPsIdempotent(t *testing.T) {
	assureNamespace(t, infraVPCNamespace)

	networkInfo := getNetworkInfoWithCondition(t, infraVPCNamespace, infraVPCNamespace,
		func(ni *v1alpha1.NetworkInfo) (bool, error) {
			if len(ni.VPCs) == 0 {
				return false, nil
			}
			return len(ni.VPCs[0].LoadBalancerBackendIPs) > 0, nil
		})

	require.NotNil(t, networkInfo)
	baseRV := networkInfo.ResourceVersion
	require.NotEmpty(t, baseRV, "expected non-empty ResourceVersion after initial reconcile")

	// Allow time for additional reconcile loops to run
	time.Sleep(30 * time.Second)

	updated, err := testData.crdClientset.CrdV1alpha1().NetworkInfos(infraVPCNamespace).Get(
		context.Background(), infraVPCNamespace, v1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, baseRV, updated.ResourceVersion,
		"ResourceVersion changed after idle period — spurious update detected in LoadBalancerBackendIPs reconcile")
}
