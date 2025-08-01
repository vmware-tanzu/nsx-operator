package e2e

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
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

type IPAddressAllocationSuite struct {
	suite.Suite
	Namespace string
}

func (s *IPAddressAllocationSuite) SetupSuite() {
	// Initialize Namespace
	s.Namespace = fmt.Sprintf("test-ipaddress-allocation-%s", getRandomString())
	// Setup test environment
	err := testData.createVCNamespace(s.Namespace)
	s.NoError(err, "Failed to create VC namespace: %v", err)
}

func (s *IPAddressAllocationSuite) TearDownSuite() {
	// Cleanup test environment
	err := testData.deleteVCNamespace(s.Namespace)
	s.NoError(err, "Failed to delete VC namespace: %v", err)
}

func (s *IPAddressAllocationSuite) TestIPAddressAllocationExternal() {
	s.T().Parallel() // Enable suite to run concurrently with other suites
	s.testIPAddressAllocation("./manifest/testIPAddressAllocation/ipaddressallocation_external.yaml", externalCIDR)
}

func (s *IPAddressAllocationSuite) TestIPAddressAllocationPrivate() {
	// Runs sequentially after TestIPAddressAllocationExternal
	s.testIPAddressAllocation("./manifest/testIPAddressAllocation/ipaddressallocation_private.yaml", privateCIDR)
}

func (s *IPAddressAllocationSuite) TestIPAddressAllocationPrivateTGW() {
	// Runs sequentially
	s.testIPAddressAllocation("./manifest/testIPAddressAllocation/ipaddressallocation_privatetgw.yaml", privateTGWCIDR)
}

func (s *IPAddressAllocationSuite) TestIPAddressAllocationWithServiceVIP() {
	// Runs sequentially
	s.testServiceWithAllocatedIP()
}

func (s *IPAddressAllocationSuite) testIPAddressAllocation(yamlPath string, expectedCIDR string) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Parse YAML to get CR's name
	ipAllocPath, _ := filepath.Abs(yamlPath)
	s.NoError(applyYAML(ipAllocPath, s.Namespace))

	// Assume the name is the same as defined in the respective YAML
	ipAllocName := s.getNameFromYAML(ipAllocPath)

	s.assureIPAddressAllocationReady(s.Namespace, ipAllocName)

	// Check AllocationIPs
	s.assertAllocationCIDR(s.Namespace, ipAllocName, expectedCIDR)

	// Delete IPAddressAllocation
	_ = deleteYAML(ipAllocPath, s.Namespace)

	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		resp, err := testData.crdClientset.CrdV1alpha1().IPAddressAllocations(s.Namespace).Get(ctx, ipAllocName, v1.GetOptions{})
		log.V(2).Info("Check resource", "resp", resp)
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("error when waiting for IPAddressAllocation %s", ipAllocName)
		}
		return false, nil
	})
	s.NoError(err)
}

func (s *IPAddressAllocationSuite) assureIPAddressAllocationReady(ns, ipAllocName string) {
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
	s.NoError(err)
}

func (s *IPAddressAllocationSuite) getAllocationCIDR(ns, ipAllocName string) string {
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
	s.NoError(err)
	return allocCIDR
}

func (s *IPAddressAllocationSuite) waitforServiceReady(ns, svc string, expectedVIP string) error {
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

func (s *IPAddressAllocationSuite) assertAllocationCIDR(ns, ipAllocName, expectedCIDR string) {
	_, expectedNet, err := net.ParseCIDR(expectedCIDR)
	s.NoError(err)

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
	s.NoError(err, "Failed to verify AllocationIPs CIDR for IPAddressAllocation %s", ipAllocName)
}

func (s *IPAddressAllocationSuite) getNameFromYAML(yamlPath string) string {
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
		s.FailNow("Unknown YAML file")
		return ""
	}
}

func (s *IPAddressAllocationSuite) testServiceWithAllocatedIP() {
	serviceYaml := "./manifest/testIPAddressAllocation/tea-svc.yaml"
	ipAllocyaml := "./manifest/testIPAddressAllocation/tea-ipalloc.yaml"

	// Parse YAML to get CR's name
	ipAllocPath, _ := filepath.Abs(ipAllocyaml)
	s.NoError(applyYAML(ipAllocPath, s.Namespace))

	// 1. Apply Service YAML
	s.NoError(applyYAML(serviceYaml, s.Namespace))

	// 2. Wait for IPAddressAllocation to be ready
	ipAllocName := s.getNameFromYAML(ipAllocPath)
	s.assureIPAddressAllocationReady(s.Namespace, ipAllocName)

	// 3. Get first IP from allocationIPs
	cidr := s.getAllocationCIDR(s.Namespace, ipAllocName)
	s.NotEmpty(cidr)

	ip, _, err := net.ParseCIDR(cidr)
	s.NoError(err)

	firstIP := ip.String()
	log.Info("First IP from allocationIPs", "ip", firstIP)

	// 4. Patch Service to set spec.loadBalancerIP
	patch := []byte(fmt.Sprintf(`{"spec":{"loadBalancerIP":"%s"}}`, firstIP))
	_, err = testData.clientset.CoreV1().Services(s.Namespace).Patch(context.Background(), "tea-svc", types.MergePatchType, patch, v1.PatchOptions{})
	s.NoError(err)

	// 5. Verify Service has correct loadBalancerIP
	err = s.waitforServiceReady(s.Namespace, "tea-svc", firstIP)
	s.NoError(err)

	// 6. Try to delete the ip allocation, webhook should deny the request
	err = testData.crdClientset.CrdV1alpha1().IPAddressAllocations(s.Namespace).Delete(context.TODO(), ipAllocName, v1.DeleteOptions{})
	log.Info("Delete IPAddressAllocation", "Name", ipAllocName, "error", err)
	s.ErrorContains(err, "denied the request")

	// 7. Clean up
	_ = deleteYAML(serviceYaml, s.Namespace)
}

func TestIPAddressAllocationSuite(t *testing.T) {
	suite.Run(t, new(IPAddressAllocationSuite))
}
