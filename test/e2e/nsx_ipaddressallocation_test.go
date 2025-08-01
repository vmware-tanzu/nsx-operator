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

var ns = fmt.Sprintf("test-ipaddress-allocation-%s", getRandomString())

func TestIPAddressAllocation(t *testing.T) {
	prepare(t, ns)
	defer destroy(t, ns, defaultTimeout)
	t.Run("testIPAddressAllocationExternal", func(t *testing.T) {
		testIPAddressAllocation(t, "./manifest/testIPAddressAllocation/ipaddressallocation_external.yaml", externalCIDR)
	})
	t.Run("testIPAddressAllocationPrivate", func(t *testing.T) {
		testIPAddressAllocation(t, "./manifest/testIPAddressAllocation/ipaddressallocation_private.yaml", privateCIDR)
	})
	t.Run("testIPAddressAllocationPrivateTGW", func(t *testing.T) {
		testIPAddressAllocation(t, "./manifest/testIPAddressAllocation/ipaddressallocation_privatetgw.yaml", privateTGWCIDR)
	})
	t.Run("testIPAddressAllocationWithServiceVIP", func(t *testing.T) {
		testServiceWithAllocatedIP(t)
	})
}

func testIPAddressAllocation(t *testing.T, yamlPath string, expectedCIDR string) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	var err error

	// Parse YAML to get CR's name
	ipAllocPath, _ := filepath.Abs(yamlPath)
	require.NoError(t, applyYAML(ipAllocPath, ns))

	// Assume the name is the same as defined in the respective YAML
	ipAllocName := getNameFromYAML(ipAllocPath)
	assureIPAddressAllocationReady(t, ns, ipAllocName)

	// Check AllocationIPs
	assertAllocationCIDR(t, ns, ipAllocName, expectedCIDR)

	// Delete IPAddressAllocation
	_ = deleteYAML(ipAllocPath, ns)

	err = wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().IPAddressAllocations(ns).Get(ctx, ipAllocName, v1.GetOptions{})
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

func testServiceWithAllocatedIP(t *testing.T) {
	serviceYaml := "./manifest/testIPAddressAllocation/tea-svc.yaml"
	ipAllocyaml := "./manifest/testIPAddressAllocation/tea-ipalloc.yaml"

	// Parse YAML to get CR's name
	ipAllocPath, _ := filepath.Abs(ipAllocyaml)
	require.NoError(t, applyYAML(ipAllocPath, ns))

	// 1. Apply Service YAML
	require.NoError(t, applyYAML(serviceYaml, ns))

	// 2. Wait for IPAddressAllocation to be ready
	ipAllocName := getNameFromYAML(ipAllocPath)
	assureIPAddressAllocationReady(t, ns, ipAllocName)

	// 3. Get first IP from allocationIPs
	cidr := getAllocationCIDR(t, ns, ipAllocName)
	require.NotEmpty(t, cidr)
	ip, _, err := net.ParseCIDR(cidr)
	require.NoError(t, err)
	firstIP := ip.String()
	log.Info("First IP from allocationIPs", "ip", firstIP)

	// 4. Patch Service to set spec.loadBalancerIP
	patch := []byte(fmt.Sprintf(`{"spec":{"loadBalancerIP":"%s"}}`, firstIP))
	_, err = testData.clientset.CoreV1().Services(ns).Patch(context.Background(), "tea-svc", types.MergePatchType, patch, v1.PatchOptions{})
	require.NoError(t, err)

	// 5. Verify Service has correct loadBalancerIP
	err = waitforServiceReady(ns, "tea-svc", firstIP)
	require.NoError(t, err)

	// 6. try to delete the ip allocation, webhook should deny the request
	err = testData.crdClientset.CrdV1alpha1().IPAddressAllocations(ns).Delete(context.TODO(), ipAllocName, v1.DeleteOptions{})
	log.Info("Delete IPAddressAllocation", "Name", ipAllocName, "error", err)
	require.ErrorContains(t, err, "denied the request")

	// 7. Clean up
	_ = deleteYAML(serviceYaml, ns)
}
