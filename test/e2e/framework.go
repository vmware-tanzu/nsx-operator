package e2e

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/vmware-tanzu/nsx-operator/pkg/client/clientset/versioned"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/test/e2e/providers"
)

const (
	defaultTimeout = 200 * time.Second
	PolicyAPI      = "policy/api/v1"
)

type Status int

const (
	Ready Status = iota
	Deleted
)

type ClusterNode struct {
	idx  int // 0 for master Node
	name string
	uid  string
}

type ClusterInfo struct {
	numWorkerNodes   int
	numNodes         int
	podV4NetworkCIDR string
	podV6NetworkCIDR string
	masterNodeName   string
	nodes            map[int]ClusterNode
	k8sServerVersion string
}

var clusterInfo ClusterInfo

type TestOptions struct {
	providerName        string
	providerConfigPath  string
	logsExportDir       string
	operatorConfigPath  string
	logsExportOnSuccess bool
	withIPPool          bool
}

var testOptions TestOptions

var provider providers.ProviderInterface

// TestData stores the state required for each test case.
type TestData struct {
	kubeConfig   *restclient.Config
	clientset    clientset.Interface
	crdClientset versioned.Interface
	nsxClient    *NSXClient
}

var testData *TestData

//Temporarily disable traffic check
/*
type PodIPs struct {
	ipv4      *net.IP
	ipv6      *net.IP
	ipStrings []string
}
*/

func initProvider() error {
	providerFactory := map[string]func(string) (providers.ProviderInterface, error){
		"remote": providers.NewRemoteProvider,
	}
	if fn, ok := providerFactory[testOptions.providerName]; ok {
		if newProvider, err := fn(testOptions.providerConfigPath); err != nil {
			return err
		} else {
			provider = newProvider
		}
	} else {
		return fmt.Errorf("unknown provider '%s'", testOptions.providerName)
	}
	return nil
}

func NewTestData(nsxConfig string) error {
	testData = &TestData{}
	err := testData.createClients()
	if err != nil {
		return err
	}
	err = testData.createNSXClients(nsxConfig)
	if err != nil {
		return err
	}
	return nil
}

func (data *TestData) createNSXClients(nsxConfig string) error {
	nsxClient, err := NewNSXClient(nsxConfig)
	if err != nil {
		return err
	}
	data.nsxClient = nsxClient
	return nil
}

// createClients initializes the clientSets in the TestData structure.
func (data *TestData) createClients() error {
	kubeconfigPath, err := provider.GetKubeconfigPath()
	if err != nil {
		return fmt.Errorf("error when getting Kubeconfig path: %v", err)
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = kubeconfigPath
	configOverrides := &clientcmd.ConfigOverrides{}

	kubeConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides).ClientConfig()
	if err != nil {
		return fmt.Errorf("error when building kube config: %v", err)
	}
	clientSet, err := clientset.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("error when creating kubernetes client: %v", err)
	}
	crdClientset, err := versioned.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("error when creating nsx-operator CRD client: %v", err)
	}
	data.kubeConfig = kubeConfig
	data.clientset = clientSet
	data.crdClientset = crdClientset
	return nil
}

// RunCommandOnNode is a convenience wrapper around the Provider interface RunCommandOnNode method.
func RunCommandOnNode(nodeName string, cmd string) (code int, stdout string, stderr string, err error) {
	return provider.RunCommandOnNode(nodeName, cmd)
}

func collectClusterInfo() error {
	serverVersion, err := testData.clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("error when detecting K8s server version %v", err)
	}
	clusterInfo.k8sServerVersion = serverVersion.String()

	// retrieve Node information
	nodes, err := testData.clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error when listing cluster Nodes: %v", err)
	}
	log.Printf("Found %d Nodes in the cluster", len(nodes.Items))
	workerIdx := 1
	clusterInfo.nodes = make(map[int]ClusterNode)
	for _, node := range nodes.Items {
		isMaster := func() bool {
			_, ok := node.Labels["node-role.kubernetes.io/control-plane"]
			if !ok {
				// openshift has label node-role.kubernetes.io/master, but not node-role.kubernetes.io/control-plane
				_, ok = node.Labels["node-role.kubernetes.io/master"]
			}
			return ok
		}()

		var nodeIdx int
		// If multiple master Nodes (HA), we will select the last one in the list
		if isMaster {
			nodeIdx = 0
			clusterInfo.masterNodeName = node.Name
		} else {
			nodeIdx = workerIdx
			workerIdx++
		}

		clusterInfo.nodes[nodeIdx] = ClusterNode{
			idx:  nodeIdx,
			name: node.Name,
			uid:  string(node.UID),
		}
	}
	if clusterInfo.masterNodeName == "" {
		return fmt.Errorf("error when listing cluster Nodes: master Node not found")
	}
	clusterInfo.numNodes = workerIdx
	clusterInfo.numWorkerNodes = clusterInfo.numNodes - 1

	retrieveCIDRs := func(cmd string, reg string) ([]string, error) {
		res := make([]string, 2)
		rc, stdout, _, err := RunCommandOnNode(clusterInfo.masterNodeName, cmd)
		if err != nil || rc != 0 {
			return res, fmt.Errorf("error when running the following command `%s` on master Node: %v, %s", cmd, err, stdout)
		}
		re := regexp.MustCompile(reg)
		if matches := re.FindStringSubmatch(stdout); len(matches) == 0 {
			return res, fmt.Errorf("cannot retrieve CIDR, unexpected kubectl output: %s", stdout)
		} else {
			cidrs := strings.Split(matches[1], ",")
			if len(cidrs) == 1 {
				_, cidr, err := net.ParseCIDR(cidrs[0])
				if err != nil {
					return res, fmt.Errorf("CIDR cannot be parsed: %s", cidrs[0])
				}
				if cidr.IP.To4() != nil {
					res[0] = cidrs[0]
				} else {
					res[1] = cidrs[0]
				}
			} else if len(cidrs) == 2 {
				_, cidr, err := net.ParseCIDR(cidrs[0])
				if err != nil {
					return res, fmt.Errorf("CIDR cannot be parsed: %s", cidrs[0])
				}
				if cidr.IP.To4() != nil {
					res[0] = cidrs[0]
					res[1] = cidrs[1]
				} else {
					res[0] = cidrs[1]
					res[1] = cidrs[0]
				}
			} else {
				return res, fmt.Errorf("unexpected cluster CIDR: %s", matches[1])
			}
		}
		return res, nil
	}

	// retrieve cluster CIDRs
	podCIDRs, err := retrieveCIDRs("kubectl cluster-info dump | grep cluster-cidr", `cluster-cidr=([^"]+)`)
	if err != nil {
		log.Printf("Failed to detect IPv4 or IPv6 Pod CIDR. Ignore.")
	} else {
		clusterInfo.podV4NetworkCIDR = podCIDRs[0]
		clusterInfo.podV6NetworkCIDR = podCIDRs[1]
	}

	return nil
}

// createNamespace creates the provided namespace.
func (data *TestData) createNamespace(namespace string) error {
	ns := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	if ns, err := data.clientset.CoreV1().Namespaces().Create(context.TODO(), &ns, metav1.CreateOptions{}); err != nil {
		// Ignore error if the namespace already exists
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("error when creating '%s' Namespace: %v", namespace, err)
		}
		// When namespace already exists, check phase
		if ns.Status.Phase == corev1.NamespaceTerminating {
			return fmt.Errorf("error when creating '%s' Namespace: namespace exists but is in 'Terminating' phase", namespace)
		}
	}
	return nil
}

// deleteNamespace deletes the provided namespace and waits for deletion to actually complete.
func (data *TestData) deleteNamespace(namespace string, timeout time.Duration) error {
	var gracePeriodSeconds int64
	propagationPolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
		PropagationPolicy:  &propagationPolicy,
	}
	if err := data.clientset.CoreV1().Namespaces().Delete(context.TODO(), namespace, deleteOptions); err != nil {
		if errors.IsNotFound(err) {
			// namespace does not exist, we return right away
			return nil
		}
		return fmt.Errorf("error when deleting '%s' Namespace: %v", namespace, err)
	}
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		if ns, err := data.clientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{}); err != nil {
			if errors.IsNotFound(err) {
				// Success
				return true, nil
			}
			return false, fmt.Errorf("error when getting Namespace '%s' after delete: %v", namespace, err)
		} else if ns.Status.Phase != corev1.NamespaceTerminating {
			return false, fmt.Errorf("deleted Namespace '%s' should be in 'Terminating' phase", namespace)
		}
		// Keep trying
		return false, nil
	})
	return err
}

type PodCondition func(*corev1.Pod) (bool, error)

// waitForSecurityPolicyReady polls the K8s apiServer until the specified CR is in the "True" state (or until
// the provided timeout expires).
func (data *TestData) waitForCRReadyOrDeleted(timeout time.Duration, cr string, namespace string, name string, status Status) error {
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		cmd := fmt.Sprintf("kubectl get %s %s -n %s -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}'", cr, name, namespace)
		log.Printf("%s", cmd)
		rc, stdout, _, err := RunCommandOnNode(clusterInfo.masterNodeName, cmd)
		if err != nil || rc != 0 {
			if status == Deleted {
				return true, nil
			}
			return false, fmt.Errorf("error when running the following command `%s` on master Node: %v, %s", cmd, err, stdout)
		} else {
			if status == Ready {
				if stdout == "True" {
					return true, nil
				}
				return false, nil
			}
			return false, nil
		}
	})
	if err != nil {
		return err
	}
	return nil
}

func (data *TestData) getCRPropertiesByJson(timeout time.Duration, crType, crName, namespace, key string) (string, error) {
	value := ""
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		cmd := fmt.Sprintf("kubectl get %s %s -n %s -o json | jq '%s'", crType, crName, namespace, key)
		log.Printf("%s", cmd)
		rc, stdout, _, err := RunCommandOnNode(clusterInfo.masterNodeName, cmd)
		if err != nil || rc != 0 {
			return false, fmt.Errorf("error when running the following command `%s` on master Node: %v, %s", cmd, err, stdout)
		} else {
			// check if 'null' in stdout
			if strings.Contains(stdout, "null") {
				return false, nil
			}
			value = stdout
			return true, nil
		}
	})
	if err != nil {
		return value, err
	}
	return value, nil
}

// Check if CR is created under NS, for resources like VPC, we do not know CR name
// return map structure, key is CR name, value is CR UID
func (data *TestData) getCRResource(timeout time.Duration, cr string, namespace string) (map[string]string, error) {
	crs := map[string]string{}
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		cmd := fmt.Sprintf("kubectl get %s -n %s", cr, namespace)
		// check if name is nil
		if cr == "namespaces" {
			cmd = fmt.Sprintf("kubectl get %s %s", cr, namespace)
		}
		log.Printf("%s", cmd)
		rc, stdout, _, err := RunCommandOnNode(clusterInfo.masterNodeName, cmd)
		if err != nil || rc != 0 {
			return false, fmt.Errorf("error when running the following command `%s` on master Node: %v, %s", cmd, err, stdout)
		} else {
			crs_raw := strings.Split(stdout, "\n")
			// for each resource, get json structure as value
			for i, c := range crs_raw {
				if i == 0 {
					// first line is table header
					continue
				}
				r := regexp.MustCompile("[^\\s]+")
				parts := r.FindAllString(c, -1)
				if len(parts) < 1 { // to avoid empty lines
					continue
				}
				uid_cmd := fmt.Sprintf("kubectl get %s %s -n %s -o yaml | grep uid", cr, parts[0], namespace)
				if cr == "namespaces" {
					uid_cmd = fmt.Sprintf("kubectl get %s %s -o yaml | grep uid", cr, parts[0])
				}
				log.Printf("trying to get uid for cr: %s", uid_cmd)
				rc, stdout, _, err := RunCommandOnNode(clusterInfo.masterNodeName, uid_cmd)
				if err != nil || rc != 0 {
					return false, fmt.Errorf("error when running the following command `%s` on master Node: %v, %s", uid_cmd, err, stdout)
				}
				uid := strings.Split(stdout, ":")[1]
				crs[parts[0]] = uid
			}
			return true, nil
		}
	})
	if err != nil {
		return crs, err
	}
	return crs, nil
}

// deploymentWaitForNames polls the K8s apiServer once the specific pods are created, no matter they are running or not.
func (data *TestData) deploymentWaitForNames(timeout time.Duration, namespace, deployment string) ([]string, error) {
	var podNames []string
	opt := metav1.ListOptions{
		LabelSelector: "deployment=" + deployment,
	}
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		if pods, err := data.clientset.CoreV1().Pods(namespace).List(context.TODO(), opt); err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when getting Pod  %v", err)
		} else {
			for _, p := range pods.Items {
				podNames = append(podNames, p.Name)
			}
			return true, nil
		}
	})
	if err != nil {
		return nil, err
	}
	return podNames, nil
}

//Temporarily disable traffic check
/*
// podWaitFor polls the K8s apiServer until the specified Pod is found (in the test Namespace) and
// the condition predicate is met (or until the provided timeout expires).
func (data *TestData) podWaitFor(timeout time.Duration, name, namespace string, condition PodCondition) (*corev1.Pod, error) {
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		if pod, err := data.clientset.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{}); err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when getting Pod '%s': %v", name, err)
		} else {
			return condition(pod)
		}
	})
	if err != nil {
		return nil, err
	}
	return data.clientset.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

// podWaitForIPs polls the K8s apiServer until the specified Pod is in the "running" state (or until
// the provided timeout expires). The function then returns the IP addresses assigned to the Pod. If the
// Pod is not using "hostNetwork", the function also checks that an IP address exists in each required
// Address Family in the cluster.
func (data *TestData) podWaitForIPs(timeout time.Duration, name, namespace string) (*PodIPs, error) {
	pod, err := data.podWaitFor(timeout, name, namespace, func(pod *corev1.Pod) (bool, error) {
		return pod.Status.Phase == corev1.PodRunning, nil
	})
	if err != nil {
		return nil, err
	}
	// According to the K8s API documentation (https://godoc.org/k8s.io/api/core/v1#PodStatus),
	// the PodIP field should only be empty if the Pod has not yet been scheduled, and "running"
	// implies scheduled.
	if pod.Status.PodIP == "" {
		return nil, fmt.Errorf("pod is running but has no assigned IP, which should never happen")
	}
	podIPStrings := sets.New[string](pod.Status.PodIP)
	for _, podIP := range pod.Status.PodIPs {
		ipStr := strings.TrimSpace(podIP.IP)
		if ipStr != "" {
			podIPStrings.Insert(ipStr)
		}
	}
	ips, err := parsePodIPs(podIPStrings)
	if err != nil {
		return nil, err
	}

	if !pod.Spec.HostNetwork {
		if clusterInfo.podV4NetworkCIDR != "" && ips.ipv4 == nil {
			return nil, fmt.Errorf("no IPv4 address is assigned while cluster was configured with IPv4 Pod CIDR %s", clusterInfo.podV4NetworkCIDR)
		}
		if clusterInfo.podV6NetworkCIDR != "" && ips.ipv6 == nil {
			return nil, fmt.Errorf("no IPv6 address is assigned while cluster was configured with IPv6 Pod CIDR %s", clusterInfo.podV6NetworkCIDR)
		}
		if ips.ipv4 == nil && ips.ipv6 == nil {
			// If e2e test runs in OpenShift, the cluster-cidr is not properly detected. For now just check Pod IPs.
			return nil, fmt.Errorf("no IPv4 or IPv6 address is assigned to Pod %s/%s", namespace, name)
		}
	}
	return ips, nil
}

// deploymentWaitForIPsOrNames polls the K8s apiServer until the specified Pod in deployment has an IP address
func (data *TestData) deploymentWaitForIPsOrNames(timeout time.Duration, namespace, deployment string) ([]string, []string, error) {
	podIPStrings := sets.NewString()
	var podNames []string
	opt := metav1.ListOptions{
		LabelSelector: "deployment=" + deployment,
	}
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		if pods, err := data.clientset.CoreV1().Pods(namespace).List(context.TODO(), opt); err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when getting Pod  %v", err)
		} else {
			for _, p := range pods.Items {
				if p.Status.Phase != corev1.PodRunning {
					return false, nil
				} else if p.Status.PodIP == "" {
					return false, nil
				} else {
					podIPStrings.Insert(p.Status.PodIP)
					podNames = append(podNames, p.Name)
				}
			}
			return true, nil
		}
	})
	if err != nil {
		return nil, nil, err
	}
	return podIPStrings.List(), podNames, nil
}

func parsePodIPs(podIPStrings sets.Set[string]) (*PodIPs, error) {
	ips := new(PodIPs)
	for podIP := range podIPStrings {
		ip := net.ParseIP(podIP)
		if ip.To4() != nil {
			if ips.ipv4 != nil && podIP != ips.ipv4.String() {
				return nil, fmt.Errorf("pod is assigned multiple IPv4 addresses: %s and %s", ips.ipv4.String(), podIP)
			}
			if ips.ipv4 == nil {
				ips.ipv4 = &ip
				ips.ipStrings = append(ips.ipStrings, podIP)
			}
		} else {
			if ips.ipv6 != nil && podIP != ips.ipv6.String() {
				return nil, fmt.Errorf("pod is assigned multiple IPv6 addresses: %s and %s", ips.ipv6.String(), podIP)
			}
			if ips.ipv6 == nil {
				ips.ipv6 = &ip
				ips.ipStrings = append(ips.ipStrings, podIP)
			}
		}
	}
	if len(ips.ipStrings) == 0 {
		return nil, fmt.Errorf("pod is running but has no assigned IP, which should never happen")
	}
	return ips, nil
}

// Run the provided command in the specified Container for the give Pod and returns the contents of
// stdout and stderr as strings. An error either indicates that the command couldn't be run or that
// the command returned a non-zero error code.
func (data *TestData) runCommandFromPod(namespace string, podName string, containerName string, cmd []string) (stdout string, stderr string, err error) {
	log.Printf("Running '%s' in Pod '%s/%s' container '%s'", strings.Join(cmd, " "), namespace, podName, containerName)
	request := data.clientset.CoreV1().RESTClient().Post().
		Namespace(namespace).
		Resource("pods").
		Name(podName).
		SubResource("exec").
		Param("container", containerName).
		VersionedParams(&corev1.PodExecOptions{
			Command: cmd,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		}, scheme.ParameterCodec)
	exec2, err := remotecommand.NewSPDYExecutor(data.kubeConfig, "POST", request.URL())
	if err != nil {
		return "", "", err
	}
	var stdoutB, stderrB bytes.Buffer
	if err := exec2.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdout: &stdoutB,
		Stderr: &stderrB,
	}); err != nil {
		log.Printf("Error when running command '%s' in Pod '%s/%s' container '%s': %v", strings.Join(cmd, " "), namespace, podName, containerName, err)
		return stdoutB.String(), stderrB.String(), err
	}
	outStr, errStr := stdoutB.String(), stderrB.String()
	log.Printf("Command '%s' in Pod '%s/%s' container '%s' returned with output: '%s' and error: '%s'", strings.Join(cmd, " "), namespace, podName, containerName, outStr, errStr)
	return stdoutB.String(), stderrB.String(), nil
}

func (data *TestData) runPingCommandFromPod(namespace string, podName string, targetPodIPs *PodIPs, count int) error {
	var cmd []string
	if targetPodIPs.ipv4 != nil {
		cmd = []string{"ping", "-c", strconv.Itoa(count), targetPodIPs.ipv4.String()}
		if _, _, err := data.runCommandFromPod(namespace, podName, podName, cmd); err != nil {
			return err
		}
	}
	if targetPodIPs.ipv6 != nil {
		cmd = []string{"ping", "-6", "-c", strconv.Itoa(count), targetPodIPs.ipv6.String()}
		if _, _, err := data.runCommandFromPod(namespace, podName, podName, cmd); err != nil {
			return err
		}
	}
	return nil
}

func (data *TestData) runNetcatCommandFromPod(namespace string, podName string, containerName string, server string, port int) error {
	cmd := []string{
		"/bin/sh",
		"-c",
		fmt.Sprintf("for i in $(seq 1 5); do nc -w 4 %s %d && exit 0 || sleep 1; done; exit 1",
			server, port),
	}
	_, _, err := data.runCommandFromPod(namespace, podName, containerName, cmd)
	if err != nil {
		return err
	}
	return nil
}
*/

func applyYAML(filename string, ns string) error {
	cmd := fmt.Sprintf("kubectl apply -f %s -n %s", filename, ns)
	if ns == "" {
		cmd = fmt.Sprintf("kubectl apply -f %s", filename)
	}
	var stdout, stderr bytes.Buffer
	command := exec.Command("bash", "-c", cmd)
	log.Printf("Applying YAML file %s", filename)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		log.Printf("Error when applying YAML file %s: %v", filename, err)
		return err
	}
	outStr, errStr := string(stdout.Bytes()), string(stderr.Bytes())
	log.Printf("YAML file %s applied with output: '%s' and error: '%s'", cmd, outStr, errStr)
	return nil
}

//Temporarily disable traffic check
/*
func runCommand(cmd string) (string, error) {
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, defaultTimeout, false, func(ctx context.Context) (bool, error) {
		var stdout, stderr bytes.Buffer
		command := exec.Command("bash", "-c", cmd)
		log.Printf("Running command %s", cmd)
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		if err != nil {
			log.Printf("Error when running command %s: %v", cmd, err)
			return false, nil
		}
		outStr, errStr := string(stdout.Bytes()), string(stderr.Bytes())
		log.Printf("Command %s returned with output: '%s' and error: '%s'", cmd, outStr, errStr)
		if errStr != "" {
			return false, nil
		}
		return true, nil
	})
	return "", err
}
*/

func deleteYAML(filename string, ns string) error {
	cmd := fmt.Sprintf("kubectl delete -f %s -n %s", filename, ns)
	if ns == "" {
		cmd = fmt.Sprintf("kubectl delete -f %s", filename)
	}
	var stdout, stderr bytes.Buffer
	command := exec.Command("bash", "-c", cmd)
	log.Printf("Deleting YAML file (%s)", filename)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		log.Printf("Error when deleting YAML file %s: %v", filename, err)
		return nil
	}
	outStr, errStr := string(stdout.Bytes()), string(stderr.Bytes())
	log.Printf("YAML file %s deleted with output: '%s' and error: '%s'", cmd, outStr, errStr)
	return nil
}

// queryResource is used to query resource by tags, not handling pagination
// tags should be present in pairs, the first tag is the scope, the second tag is the value
// caller should transform the response to the expected resource type
func (data *TestData) queryResource(resourceType string, tags []string) (model.SearchResponse, error) {
	tagScopeClusterKey := strings.Replace(common.TagScopeNamespace, "/", "\\/", -1)
	tagScopeClusterValue := strings.Replace(tags[0], ":", "\\:", -1)
	tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagScopeClusterKey, tagScopeClusterValue)
	resourceParam := fmt.Sprintf("%s:%s", common.ResourceType, resourceType)
	queryParam := resourceParam + " AND " + tagParam
	if len(tags) >= 2 {
		tagscope := strings.Replace(tags[0], "/", "\\/", -1)
		tagtag := strings.Replace(tags[1], ":", "\\:", -1)
		tagParam = fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagscope, tagtag)
		queryParam = resourceParam + " AND " + tagParam
	}
	queryParam += " AND marked_for_delete:false"
	var cursor *string
	var pageSize int64 = 500
	response, err := data.nsxClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
	if err != nil {
		log.Printf("Error when querying resource %s: %v", resourceType, err)
		return model.SearchResponse{}, err
	}
	return response, nil
}

func (data *TestData) waitForResourceExist(namespace string, resourceType string, key string, value string, shouldExist bool) error {
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, defaultTimeout, false, func(ctx context.Context) (bool, error) {
		exist := true
		tagScopeClusterKey := strings.Replace(common.TagScopeNamespace, "/", "\\/", -1)
		tagScopeClusterValue := strings.Replace(namespace, ":", "\\:", -1)
		tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagScopeClusterKey, tagScopeClusterValue)
		resourceParam := fmt.Sprintf("%s:%s AND %s:*%s*", common.ResourceType, resourceType, key, value)
		queryParam := resourceParam + " AND " + tagParam
		queryParam += " AND marked_for_delete:false"

		var cursor *string
		var pageSize int64 = 500
		response, err := testData.nsxClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		if err != nil {
			log.Printf("Error when querying resource %s/%s: %s,%v", resourceType, key, value, err)
			return false, err
		}
		if len(response.Results) == 0 {
			exist = false
		}
		log.Printf("QueryParam: %s exist: %t", queryParam, exist)
		if exist != shouldExist {
			return false, nil
		}
		return true, nil
	})
	return err
}

func (data *TestData) waitForResourceExistById(namespace string, resourceType string, id string, shouldExist bool) error {
	return data.waitForResourceExist(namespace, resourceType, "id", id, shouldExist)
}

func (data *TestData) waitForResourceExistOrNot(namespace string, resourceType string, resourceName string, shouldExist bool) error {
	return data.waitForResourceExist(namespace, resourceType, "display_name", resourceName, shouldExist)
}

func (data *TestData) waitForResourceExistByPath(pathPolicy string, shouldExist bool) error {
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, defaultTimeout, false, func(ctx context.Context) (bool, error) {
		exist := true

		fullURL := PolicyAPI + pathPolicy
		fullURL = strings.ReplaceAll(fullURL, "\"", "")
		fullURL = strings.ReplaceAll(fullURL, "\n", "")
		fullURL = strings.ReplaceAll(fullURL, "\r", "")
		_, err := url.Parse(fullURL)
		if err != nil {
			fmt.Println("Invalid URL:", err)
			return false, err
		}

		resp, err := testData.nsxClient.Client.Cluster.HttpGet(fullURL)
		if err != nil {
			if !shouldExist {
				return true, nil
			}
			return false, err
		}
		id, ok := resp["id"].(string)
		if !ok || id == "" {
			exist = false
		}
		if exist != shouldExist {
			return false, nil
		}
		return true, nil
	})
	return err
}
