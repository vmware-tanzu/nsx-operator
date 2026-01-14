package e2e

import (
	"context"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	port          = int32(80)
	sourcePodName = fmt.Sprintf("test-source-pod-%s", getRandomString())
	host          = "coffee.example.com"
)

type backendSvc struct {
	name       string
	namespace  string
	port       int32
	podName    string
	targetPort int32
}
type ingress struct {
	name           string
	namespace      string
	host           string
	rules          []IngressRule
	defaultSvcName string
	defaultSvcPort int32
	annotations    map[string]string
	secretName     string
}

func TestLoadBalancer(t *testing.T) {
	// Use pre-created namespaces
	lbNs := NsLoadBalancerLB
	podNs := NsLoadBalancerPod

	createPodAndWaitingRunning(t, sourcePodName, podNs)
	t.Run("testIngress", func(t *testing.T) {
		testIngress(t, lbNs, podNs)
	})
	t.Run("testUpdateServiceVIP", func(t *testing.T) {
		testUpdateSvcIP(t, lbNs, podNs)
	})
}

func waitForIngressReady(t *testing.T, ingressName string, ns string) string {
	var ip string
	_, err := testData.ingressWaitFor(120*time.Second, ns, ingressName, func(ingress *networkingv1.Ingress) (bool, error) {
		if len(ingress.Status.LoadBalancer.Ingress) > 0 {
			ip = ingress.Status.LoadBalancer.Ingress[0].IP
			log.Info("Ingress is ready", "ingress", ingressName, "namespace", ns, "ip", ip)
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)
	return ip
}

func createDedicateIPService(t *testing.T, backSvc backendSvc, ip string) *corev1.Service {
	selector := map[string]string{"app": backSvc.podName}
	service, err := testData.createService(backSvc.namespace, backSvc.name, backSvc.port, backSvc.targetPort, corev1.ProtocolTCP, selector, corev1.ServiceTypeLoadBalancer, func(service *corev1.Service) {
		service.Spec.LoadBalancerIP = ip
	})
	require.NoError(t, err)
	return service
}

func createMultipleProtocolService(t *testing.T, backSvc backendSvc) *corev1.Service {
	// Create a service
	selector := map[string]string{"app": backSvc.podName}
	addUDPPort := func(service *corev1.Service) {
		service.Spec.Ports = append(service.Spec.Ports, corev1.ServicePort{
			Name:       strings.ToLower(string(corev1.ProtocolUDP)),
			Port:       backSvc.port,
			TargetPort: intstr.FromInt(int(backSvc.targetPort)),
			Protocol:   corev1.ProtocolUDP,
		})
	}
	svc, err := testData.createService(backSvc.namespace, backSvc.name, backSvc.port, backSvc.targetPort, corev1.ProtocolTCP, selector, corev1.ServiceTypeLoadBalancer, addUDPPort)
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	return svc
}

// waitForService will wait for svc when its vip meet the condition
// if expectIP is "", that means svc vip should have value
// if expectIP not "", the vip of svc should equal to expectIP
func waitForService(ns, svc string, expectIP string) (string, error) {
	var ip string
	log.Trace("Waiting for service ready", "svc", svc)
	_, err := testData.serviceWaitFor(120*time.Second, ns, svc, func(svc *corev1.Service) (bool, error) {
		lbStatuses := svc.Status.LoadBalancer.Ingress
		if len(lbStatuses) > 0 {
			log.Info("Get svc ip", "service", svc.Name, "Namespace", ns, "ingress", svc.Status.LoadBalancer.Ingress[0].IP, "expectIP", expectIP)
			if svc.Status.LoadBalancer.Ingress[0].IP != "" {
				ip = svc.Status.LoadBalancer.Ingress[0].IP
				if expectIP != "" && ip != expectIP {
					return false, nil
				} else {
					return true, nil
				}
			} else {
				return false, nil
			}
		}
		return false, nil
	})
	return ip, err
}

func createPodAndWaitingRunning(t *testing.T, podName string, ns string) {
	_, err := testData.createPod(ns, podName, containerName, podImage, corev1.ProtocolTCP, port)
	if err != nil {
		t.Fatalf("Failed to create source pod: %v", err)
	}
	_, err = testData.podWaitFor(resourceReadyTime, podName, ns, func(pod *corev1.Pod) (bool, error) {
		return pod.Status.Phase == corev1.PodRunning, nil
	})
	if err != nil {
		log.Error(err, "create pod", "pod", podName, "namespace", ns)
		t.Fatalf("Failed to create pod: %v", err)
	}
}

// GetNthIPFromCIDR parses an IPv4 CIDR (e.g., "192.168.0.32/27") and returns the nth IP address (0-based index).
// n=0 returns the network address, n=1 returns the first usable IP, etc.
func getNthIPFromCIDR(cidr string, n int) (string, error) {
	// Parse CIDR
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("failed to parse CIDR %s: %v", cidr, err)
	}

	// Validate IPv4
	if ipNet.IP.To4() == nil {
		return "", fmt.Errorf("CIDR %s is not an IPv4 address", cidr)
	}

	// Calculate number of available IPs in the CIDR
	ones, bits := ipNet.Mask.Size()
	numIPs := 1 << uint(bits-ones) // 2^(32-ones) for IPv4
	if n < 0 || n >= numIPs {
		return "", fmt.Errorf("n=%d is out of range for CIDR %s (0 to %d)", n, cidr, numIPs-1)
	}

	// Convert base IP to big.Int for arithmetic
	ipInt := new(big.Int)
	ipInt.SetBytes(ipNet.IP.To4())

	// Add n to the base IP
	ipInt.Add(ipInt, big.NewInt(int64(n)))

	// Convert back to net.IP
	resultIP := make(net.IP, 4)
	ipIntBytes := ipInt.Bytes()
	// Pad with zeros if necessary (big.Int may return fewer bytes)
	for i, b := range ipIntBytes {
		resultIP[4-len(ipIntBytes)+i] = b
	}

	// Validate the resulting IP is within the CIDR
	if !ipNet.Contains(resultIP) {
		return "", fmt.Errorf("resulting IP %s is not within CIDR %s", resultIP.String(), cidr)
	}

	return resultIP.String(), nil
}

func testUpdateSvcIP(t *testing.T, lbNs, podNs string) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	coffeeSvc := backendSvc{
		port:       port,
		name:       "coffee-svc",
		namespace:  lbNs,
		podName:    "coffee",
		targetPort: port,
	}

	createPodAndWaitingRunning(t, coffeeSvc.podName, coffeeSvc.namespace)
	// create svc allocated ip by NCP
	svc := createDedicateIPService(t, coffeeSvc, "")
	coffeeSvcIP, err := waitForService(coffeeSvc.namespace, coffeeSvc.name, "")
	require.NoError(t, err)
	trafficErr := checkTrafficByCurl(podNs, sourcePodName, containerName, coffeeSvcIP, coffeeSvc.port, 5*time.Second, 30*time.Second)
	require.NoError(t, trafficErr, "Service traffic should work")

	ipAllocationName := "coffee-ip"
	err = testData.createIpAddressAllocation(lbNs, "coffee-ip", "External", 32, "")
	require.NoError(t, err)
	// create ipaddressallocation and assign loadBalancerIP of svc
	ips := assureIPAddressAllocationReady(t, lbNs, ipAllocationName)
	ip, err := getNthIPFromCIDR(ips, 1)
	require.NoError(t, err)
	svc.Spec.LoadBalancerIP = ip
	err = testData.updateService(svc)
	require.NoError(t, err)
	coffeeSvcIP, err = waitForService(coffeeSvc.namespace, coffeeSvc.name, ip)
	require.NoError(t, err)

	trafficErr = checkTrafficByCurl(podNs, sourcePodName, containerName, coffeeSvcIP, coffeeSvc.port, 5*time.Second, 30*time.Second)
	require.NoError(t, trafficErr, "Service traffic should work")

	// update the svc ip
	ip, err = getNthIPFromCIDR(ips, 2)
	require.NoError(t, err)
	log.Debug("Updating service IP", "service", svc.Name, "namespace", svc.Namespace, "new IP", ip)
	svc.Spec.LoadBalancerIP = ip
	err = testData.updateService(svc)
	require.NoError(t, err)
	_, err = waitForService(coffeeSvc.namespace, coffeeSvc.name, ip)
	require.NoError(t, err)
	trafficErr = checkTrafficByCurl(podNs, sourcePodName, containerName, ip, coffeeSvc.port, 5*time.Second, 30*time.Second)
	require.NoError(t, trafficErr, "Service traffic should work")
}

func testIngress(t *testing.T, lbNs, podNs string) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Create the multiple service
	teaSvc := backendSvc{
		port:       port,
		name:       "tea-svc",
		namespace:  lbNs,
		podName:    "tea",
		targetPort: port,
	}

	createMultipleProtocolService(t, teaSvc)
	teaSvcIP, err := waitForService(teaSvc.namespace, teaSvc.name, "")
	require.NoError(t, err)

	defaultSvc := backendSvc{
		port:       port,
		name:       "default-svc",
		namespace:  lbNs,
		podName:    "default",
		targetPort: port,
	}

	createMultipleProtocolService(t, defaultSvc)
	defaultSvcIP, err := waitForService(defaultSvc.namespace, defaultSvc.name, "")
	require.NoError(t, err)

	// Create backened pod
	createPodAndWaitingRunning(t, teaSvc.podName, teaSvc.namespace)
	createPodAndWaitingRunning(t, defaultSvc.podName, defaultSvc.namespace)

	trafficErr := checkTrafficByCurl(podNs, sourcePodName, containerName, teaSvcIP, teaSvc.port, 5*time.Second, 30*time.Second)
	require.NoError(t, trafficErr, "Service traffic should work")

	trafficErr = checkTrafficByCurl(podNs, sourcePodName, containerName, defaultSvcIP, defaultSvc.port, 5*time.Second, 30*time.Second)
	require.NoError(t, trafficErr, "Service traffic should work")

	// Create Ingress
	coffeeIngress := ingress{
		name:      "coffee-ingress",
		namespace: lbNs,
		host:      host,
		rules: []IngressRule{
			{
				ServiceName: defaultSvc.name,
				Path:        "/fo{2}/[abc]+(r|R)/(.*)",
				ServicePort: defaultSvc.port,
				PathType:    networkingv1.PathTypeImplementationSpecific,
			},
			{
				ServiceName: teaSvc.name,
				Path:        "/tea",
				ServicePort: teaSvc.port,
				PathType:    networkingv1.PathTypeImplementationSpecific,
			},
		},
		defaultSvcName: defaultSvc.name,
		defaultSvcPort: defaultSvc.port,
		annotations: map[string]string{
			"kubernetes.io/ingress.class":      "nsx",
			"kubernetes.io/ingress.allow-http": "True",
			"ncp/ssl-mode":                     "offload",
			"ncp/http-redirect":                "true",
		},
		secretName: "coffee-secret",
	}
	log.Debug("Creating ingress", "name", coffeeIngress.name, "namespace", coffeeIngress.namespace)
	_, err = testData.createIngress(coffeeIngress.namespace, coffeeIngress.name, coffeeIngress.host, coffeeIngress.secretName, coffeeIngress.defaultSvcName, coffeeIngress.defaultSvcPort, coffeeIngress.rules, coffeeIngress.annotations)
	require.NoError(t, err)
	ingressIP := waitForIngressReady(t, coffeeIngress.name, coffeeIngress.namespace)

	trafficErr = checkTrafficByCurl(podNs, sourcePodName, containerName, ingressIP, defaultSvc.port, 5*time.Second, 30*time.Second, WithPath("/foo/bar/case"),
		WithHeaders(map[string]string{"host": host}))
	require.NoError(t, trafficErr, "Ingress traffic should work")
}
