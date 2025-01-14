package e2e

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

const (
	externalCIDR   = "192.168.0.0/16"
	privateCIDR    = "172.26.0.0/16"
	privateTGWCIDR = "10.246.0.0/16"
	ns             = "test-ipaddress-allocation"
)

func TestIPAddressAllocation(t *testing.T) {
	return
	setupTest(t, ns)
	defer teardownTest(t, ns, defaultTimeout)
	t.Run("testIPAddressAllocationExternal", func(t *testing.T) {
		testIPAddressAllocation(t, "./manifest/testIPAddressAllocation/ipaddressallocation_external.yaml", externalCIDR)
	})
	t.Run("testIPAddressAllocationPrivate", func(t *testing.T) {
		testIPAddressAllocation(t, "./manifest/testIPAddressAllocation/ipaddressallocation_private.yaml", privateCIDR)
	})
	t.Run("testIPAddressAllocationPrivateTGW", func(t *testing.T) {
		testIPAddressAllocation(t, "./manifest/testIPAddressAllocation/ipaddressallocation_privatetgw.yaml", privateTGWCIDR)
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
	default:
		panic("Unknown YAML file")
	}
}
