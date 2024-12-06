package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v12 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

const (
	testCustomizedNetworkConfigName = "selfdefinedconfig"
	testInfraNetworkConfigName      = "system"

	infraVPCNamespace       = "kube-system"
	sharedInfraVPCNamespace = "kube-public"

	e2eNetworkInfoNamespace       = "customized-ns"
	e2eNetworkInfoNamespaceShare0 = "shared-vpc-ns-0"
	e2eNetworkInfoNamespaceShare1 = "shared-vpc-ns-1"

	customizedPrivateCIDR1 = "172.29.0.0/16"
	customizedPrivateCIDR2 = "172.39.0.0/16"
	customizedPrivateCIDR3 = "172.39.0.0/16"
)

func TestNetworkInfo(t *testing.T) {
	deleteVPCNetworkConfiguration(t, testCustomizedNetworkConfigName)
	defer t.Cleanup(
		func() {
			err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Delete(context.Background(), testCustomizedNetworkConfigName, v1.DeleteOptions{})
			if err != nil {
				log.Error(err, "Delete VPCNetworkConfigurations", "name", testCustomizedNetworkConfigName)
			}
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
	require.NoError(t, applyYAML(ncPath, ""))
	nsPath, _ := filepath.Abs("./manifest/testVPC/customize_ns.yaml")
	require.NoError(t, applyYAML(nsPath, ""))

	defer deleteYAML(nsPath, "")

	ns := "customized-ns"

	// For networkinfo CR, its CR name is the same as namespace
	assureNetworkInfo(t, ns, ns)
	assureNamespace(t, ns)

	vpcPath := getVPCPathFromVPCNetworkConfiguration(t, testCustomizedNetworkConfigName)
	require.NoError(t, testData.waitForResourceExistByPath(vpcPath, true))
}

// Test Infra NetworkInfo
func testInfraNetworkInfo(t *testing.T) {
	// Check namespace cr existence
	assureNamespace(t, infraVPCNamespace)

	// Check networkinfo cr existence
	networkInfo := assureNetworkInfo(t, infraVPCNamespace, infraVPCNamespace)
	require.NotNil(t, networkInfo)

	vpcPath := getVPCPathFromVPCNetworkConfiguration(t, testInfraNetworkConfigName)
	require.NoError(t, testData.waitForResourceExistByPath(vpcPath, true))

	// kube-public namespace should have its own NetworkInfo CR
	// Check networkinfo cr existence
	assureNetworkInfo(t, sharedInfraVPCNamespace, sharedInfraVPCNamespace)
}

// Test Default NetworkInfo
func testDefaultNetworkInfo(t *testing.T) {
	// If no annotation on namespace, then NetworkInfo will use default network config to create vpc under each ns
	ns := "networkinfo-default-1"
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
func testSharedNSXVPC(t *testing.T) {
	ns := "shared-vpc-ns-0"
	ns1 := "shared-vpc-ns-1"

	nsPath, _ := filepath.Abs("./manifest/testVPC/shared_ns.yaml")
	require.NoError(t, applyYAML(nsPath, ""))
	defer deleteYAML(nsPath, "")

	// Check namespace cr existence
	assureNamespace(t, ns)
	assureNamespace(t, ns1)

	// Check networkinfo cr existence
	assureNetworkInfo(t, ns, ns)
	assureNetworkInfo(t, ns1, ns1)

	vpcPath := getVPCPathFromVPCNetworkConfiguration(t, testCustomizedNetworkConfigName)

	// delete ns1 and check vpc not deleted
	require.NoError(t, testData.deleteNamespace(ns1, defaultTimeout))
	assureNetworkInfoDeleted(t, ns1)
	assureNamespaceDeleted(t, ns1)
	require.NoError(t, testData.waitForResourceExistByPath(vpcPath, true))
}

// update vpcnetworkconfig, and check vpc is updated
func testUpdateVPCNetworkconfigNetworkInfo(t *testing.T) {
	ns := "update-ns"

	nsPath, _ := filepath.Abs("./manifest/testVPC/update_ns.yaml")
	require.NoError(t, applyYAML(nsPath, ""))
	defer deleteYAML(nsPath, "")

	vncPathOriginal, _ := filepath.Abs("./manifest/testVPC/customize_networkconfig.yaml")
	defer applyYAML(vncPathOriginal, "")

	// Check namespace cr existence
	assureNamespace(t, ns)

	// Check networkinfo cr existence
	networkInfo := assureNetworkInfo(t, ns, ns)
	require.NotNil(t, networkInfo)

	networkInfoNew := getNetworkInfoWithPrivateIPs(t, ns, networkInfo.Name)
	privateIPs := networkInfoNew.VPCs[0].PrivateIPs
	assert.Contains(t, privateIPs, customizedPrivateCIDR1, "privateIPs %s should contain %s", privateIPs, customizedPrivateCIDR1)
	assert.Contains(t, privateIPs, customizedPrivateCIDR2, "privateIPs %s should contain %s", privateIPs, customizedPrivateCIDR1)

	vncPath, _ := filepath.Abs("./manifest/testVPC/customize_networkconfig_updated.yaml")
	require.NoError(t, applyYAML(vncPath, ""))

	networkInfoNew = getNetworkInfoWithPrivateIPs(t, ns, networkInfo.Name)
	privateIPs = networkInfoNew.VPCs[0].PrivateIPs
	assert.Contains(t, privateIPs, customizedPrivateCIDR3, "privateIPs %s should contain %s", privateIPs, customizedPrivateCIDR3)
}

func assureNetworkInfo(t *testing.T, ns, networkInfoName string) (res *v1alpha1.NetworkInfo) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		res, err = testData.crdClientset.CrdV1alpha1().NetworkInfos(ns).Get(context.Background(), networkInfoName, v1.GetOptions{})
		log.V(2).Info("Get NetworkInfos", "res", res, "Namespace", ns, "Name", networkInfoName)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when waiting for %s", networkInfoName)
		}
		return true, nil
	})
	require.NoError(t, err)
	return
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
		res, err := testData.crdClientset.CrdV1alpha1().NetworkInfos(ns).Get(context.Background(), ns, v1.GetOptions{})
		log.V(2).Info("Deleting NetworkInfos", "res", res, "Namespace", ns, "Name", ns)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for %s", ns)
		}
		return false, nil
	})
	require.NoError(t, err)
	return
}

func assureNamespace(t *testing.T, ns string) (res *v12.Namespace) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		res, err = testData.clientset.CoreV1().Namespaces().Get(context.Background(), ns, v1.GetOptions{})
		log.V(2).Info("Get Namespaces", "res", res, "Name", ns)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when waiting for Namespaces %s", ns)
		}
		return true, nil
	})
	require.NoError(t, err)
	return
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
		res, err := testData.clientset.CoreV1().Namespaces().Get(context.Background(), ns, v1.GetOptions{})
		log.V(2).Info("Deleting Namespace", "res", res, "Name", ns)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for %s", ns)
		}
		return false, nil
	})
	require.NoError(t, err)
	return
}

func getNetworkInfoWithPrivateIPs(t *testing.T, ns, networkInfoName string) (networkInfo *v1alpha1.NetworkInfo) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		networkInfo, err = testData.crdClientset.CrdV1alpha1().NetworkInfos(ns).Get(ctx, networkInfoName, v1.GetOptions{})
		if err != nil {
			log.V(2).Info("Check private ips of networkinfo", "networkInfo", networkInfo, "error", err)
			return false, fmt.Errorf("error when waiting for vpcnetworkinfo private ips: %s", networkInfoName)
		}
		if len(networkInfo.VPCs) > 0 && len(networkInfo.VPCs[0].PrivateIPs) > 0 {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)
	return
}

func getVPCPathFromVPCNetworkConfiguration(t *testing.T, ncName string) (vpcPath string) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(ctx, ncName, v1.GetOptions{})
		if err != nil {
			log.V(2).Info("Check VPC path of vpcnetworkconfigurations", "resp", resp)
			return false, fmt.Errorf("error when waiting for vpcnetworkconfigurations VPC path: %s: %v", ncName, err)
		}
		if len(resp.Status.VPCs) > 0 && resp.Status.VPCs[0].VPCPath != "" {
			vpcPath = resp.Status.VPCs[0].VPCPath
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)
	return
}

func deleteVPCNetworkConfiguration(t *testing.T, ncName string) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		_ = testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Delete(context.Background(), ncName, v1.DeleteOptions{})
		log.V(2).Info("Delete VPCNetworkConfigurations", "name", testCustomizedNetworkConfigName)

		resp, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(ctx, ncName, v1.GetOptions{})
		if err != nil {
			log.V(2).Info("Check stale vpcnetworkconfigurations", "resp", resp)
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when deleting vpcnetworkconfiguration %s", ncName)
		}
		return false, nil
	})
	require.NoError(t, err)
}
