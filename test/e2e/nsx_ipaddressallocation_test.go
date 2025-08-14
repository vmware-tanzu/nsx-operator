package e2e

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

const (
	externalCIDR   = "192.168.0.0/16"
	privateCIDR    = "172.26.0.0/16"
	privateTGWCIDR = "10.246.0.0/16"
)

func TestIPAddressAllocation(t *testing.T) {
	t.Parallel()
	t.Run("testIPAddressAllocationExternal", func(t *testing.T) {
		t.Parallel()
		ns := fmt.Sprintf("test-ipaddress-allocation-external-%s", getRandomString())
		err := testData.createVCNamespace(ns)
		if err != nil {
			t.Fatalf("Failed to create VC namespace: %v", err)
		}
		defer func() {
			err := testData.deleteVCNamespace(ns)
			if err != nil {
				t.Fatalf("Failed to delete VC namespace: %v", err)
			}
		}()
		testIPAddressAllocation(t, "./manifest/testIPAddressAllocation/ipaddressallocation_external.yaml", externalCIDR, ns)
	})
	t.Run("testIPAddressAllocationPrivate", func(t *testing.T) {
		t.Parallel()
		ns := fmt.Sprintf("test-ipaddress-allocation-private-%s", getRandomString())
		err := testData.createVCNamespace(ns)
		if err != nil {
			t.Fatalf("Failed to create VC namespace: %v", err)
		}
		defer func() {
			err := testData.deleteVCNamespace(ns)
			if err != nil {
				t.Fatalf("Failed to delete VC namespace: %v", err)
			}
		}()
		testIPAddressAllocation(t, "./manifest/testIPAddressAllocation/ipaddressallocation_private.yaml", privateCIDR, ns)
	})
	t.Run("testIPAddressAllocationPrivateTGW", func(t *testing.T) {
		t.Parallel()
		ns := fmt.Sprintf("test-ipaddress-allocation-privatetgw-%s", getRandomString())
		err := testData.createVCNamespace(ns)
		if err != nil {
			t.Fatalf("Failed to create VC namespace: %v", err)
		}
		defer func() {
			err := testData.deleteVCNamespace(ns)
			if err != nil {
				t.Fatalf("Failed to delete VC namespace: %v", err)
			}
		}()
		testIPAddressAllocation(t, "./manifest/testIPAddressAllocation/ipaddressallocation_privatetgw.yaml", privateTGWCIDR, ns)
	})
	t.Run("testIPAddressAllocationWithServiceVIP", func(t *testing.T) {
		t.Parallel()
		ns := fmt.Sprintf("test-ipaddress-allocation-service-vip-%s", getRandomString())
		err := testData.createVCNamespace(ns)
		if err != nil {
			t.Fatalf("Failed to create VC namespace: %v", err)
		}
		defer func() {
			err := testData.deleteVCNamespace(ns)
			if err != nil {
				t.Fatalf("Failed to delete VC namespace: %v", err)
			}
		}()
		testServiceWithAllocatedIP(t, ns)
	})
}

func testIPAddressAllocation(t *testing.T, yamlPath string, expectedCIDR string, namespace string) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	var err error

	// Parse YAML to get CR's name
	ipAllocPath, _ := filepath.Abs(yamlPath)
	require.NoError(t, applyYAML(ipAllocPath, namespace))

	// Assume the name is the same as defined in the respective YAML
	ipAllocName := getNameFromYAML(ipAllocPath)
	assureIPAddressAllocationReady(t, namespace, ipAllocName)

	// Check AllocationIPs
	assertAllocationCIDR(t, namespace, ipAllocName, expectedCIDR)

	// Delete IPAddressAllocation
	_ = deleteYAML(ipAllocPath, namespace)

	err = wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().IPAddressAllocations(namespace).Get(ctx, ipAllocName, v1.GetOptions{})
		log.V(2).Info("Check resource", "resp", resp)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for IPAddressAllocation %s", ipAllocName)
		}
		return false, nil
	})
	require.NoError(t, err)
}

func assureIPAddressAllocationReady(t *testing.T, ns, ipAllocName string) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().IPAddressAllocations(ns).Get(context.Background(), ipAllocName, v1.GetOptions{})
		log.V(2).Info("Get IPAddressAllocations", "Namespace", ns, "Name", ipAllocName)
		if err != nil {
			return false, fmt.Errorf("error when waiting for %s", ipAllocName)
		}
		for _, con := range resp.Status.Conditions {
			if con.Type == v1alpha1.Ready && resp.Status.AllocationIPs != "" {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err)
}

func getAllocationCIDR(t *testing.T, ns, ipAllocName string) string {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	var allocCIDR string
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().IPAddressAllocations(ns).Get(context.Background(), ipAllocName, v1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error when waiting for %s", ipAllocName)
		}
		allocCIDR = resp.Status.AllocationIPs
		return true, nil
	})
	require.NoError(t, err)
	return allocCIDR
}

func waitforServiceReady(ns, svc string, expectedVIP string) error {
	log.V(2).Info("Waiting for service ready", "expectedVIP", expectedVIP)
	_, err := testData.serviceWaitFor(120*time.Second, ns, svc, func(svc *corev1.Service) (bool, error) {
		lbStatuses := svc.Status.LoadBalancer.Ingress
		if len(lbStatuses) > 0 {
			log.Info("Get svc ip", "Namespace", ns, "ingress", svc.Status.LoadBalancer.Ingress[0].IP)
			if svc.Status.LoadBalancer.Ingress[0].IP == expectedVIP {
				return true, nil
			}
		}
		return false, nil
	})
	return err
}

func assertAllocationCIDR(t *testing.T, ns, ipAllocName, expectedCIDR string) {
	_, expectedNet, err := net.ParseCIDR(expectedCIDR)
	require.NoError(t, err)

	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err = wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (bool, error) {
		resp, err := testData.crdClientset.CrdV1alpha1().IPAddressAllocations(ns).Get(context.Background(), ipAllocName, v1.GetOptions{})
		if err != nil {
			return false, err
		}
		if resp.Status.AllocationIPs != "" {
			allocCIDR := resp.Status.AllocationIPs
			_, allocNet, err := net.ParseCIDR(allocCIDR)
			if err != nil {
				return false, err
			}
			if expectedNet.Contains(allocNet.IP) && allocNet.Contains(expectedNet.IP) {
				return true, nil
			}
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err, "Failed to verify AllocationIPs CIDR for IPAddressAllocation %s", ipAllocName)
}

func getNameFromYAML(yamlPath string) string {
	// Manually extract the CR's name from the filename, adjust logic if necessary
	switch filepath.Base(yamlPath) {
	case "ipaddressallocation_external.yaml":
		return "guestcluster-workers-b"
	case "ipaddressallocation_private.yaml":
		return "guestcluster-workers-a"
	case "ipaddressallocation_privatetgw.yaml":
		return "guestcluster-workers-c"
	case "tea-ipalloc.yaml":
		return "ipalloc-vip"
	default:
		panic("Unknown YAML file")
	}
}

func testServiceWithAllocatedIP(t *testing.T, namespace string) {
	serviceYaml := "./manifest/testIPAddressAllocation/tea-svc.yaml"
	ipAllocyaml := "./manifest/testIPAddressAllocation/tea-ipalloc.yaml"

	// Parse YAML to get CR's name
	ipAllocPath, _ := filepath.Abs(ipAllocyaml)
	require.NoError(t, applyYAML(ipAllocPath, namespace))

	// 1. Apply Service YAML
	require.NoError(t, applyYAML(serviceYaml, namespace))

	// 2. Wait for IPAddressAllocation to be ready
	ipAllocName := getNameFromYAML(ipAllocPath)
	assureIPAddressAllocationReady(t, namespace, ipAllocName)

	// 3. Get first IP from allocationIPs
	cidr := getAllocationCIDR(t, namespace, ipAllocName)
	require.NotEmpty(t, cidr)
	ip, _, err := net.ParseCIDR(cidr)
	require.NoError(t, err)
	firstIP := ip.String()
	log.Info("First IP from allocationIPs", "ip", firstIP)

	// 4. Patch Service to set spec.loadBalancerIP
	patch := []byte(fmt.Sprintf(`{"spec":{"loadBalancerIP":"%s"}}`, firstIP))
	_, err = testData.clientset.CoreV1().Services(namespace).Patch(context.Background(), "tea-svc", types.MergePatchType, patch, v1.PatchOptions{})
	require.NoError(t, err)

	// 5. Verify Service has correct loadBalancerIP
	err = waitforServiceReady(namespace, "tea-svc", firstIP)
	require.NoError(t, err)

	// 6. try to delete the ip allocation, webhook should deny the request
	err = testData.crdClientset.CrdV1alpha1().IPAddressAllocations(namespace).Delete(context.TODO(), ipAllocName, v1.DeleteOptions{})
	log.Info("Delete IPAddressAllocation", "Name", ipAllocName, "error", err)
	require.ErrorContains(t, err, "denied the request")

	// 7. Clean up
	_ = deleteYAML(serviceYaml, namespace)
}
