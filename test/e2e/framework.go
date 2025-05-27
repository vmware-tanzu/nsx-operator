package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderror "errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/client/clientset/versioned"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/test/e2e/providers"
)

var log = &logger.Log

const (
	createVCNamespaceEndpoint = "/api/vcenter/namespaces/instances/v2"
	defaultTimeout            = 300 * time.Second
	PolicyAPI                 = "policy/api/v1"
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
	vcUser              string
	vcPassword          string
	logsExportOnSuccess bool
	debugLog            bool
}

var testOptions TestOptions

var provider providers.ProviderInterface

// TestData stores the state required for each test case.
type TestData struct {
	kubeConfig   *restclient.Config
	clientset    clientset.Interface
	crdClientset versioned.Interface
	nsxClient    *NSXClient
	vcClient     *vcClient
}

var testData *TestData

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

func NewTestData(nsxConfig string, vcUser string, vcPassword string) error {
	testData = &TestData{}
	err := testData.createClients()
	if err != nil {
		return err
	}
	config.UpdateConfigFilePath(nsxConfig)
	cf, err := config.NewNSXOperatorConfigFromFile()
	if err != nil {
		return err
	}
	err = testData.createNSXClients(cf)
	if err != nil {
		return err
	}
	if vcUser != "" && vcPassword != "" {
		testData.vcClient = newVcClient(cf.VCEndPoint, cf.HttpsPort, vcUser, vcPassword)
	}
	return nil
}

func (data *TestData) createNSXClients(cf *config.NSXOperatorConfig) error {
	nsxClient, err := NewNSXClient(cf)
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
	log.Info("Found Nodes in the cluster", "nodes count", len(nodes.Items))
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
		log.Info("Failed to detect IPv4 or IPv6 Pod CIDR. Ignore.")
	} else {
		clusterInfo.podV4NetworkCIDR = podCIDRs[0]
		clusterInfo.podV6NetworkCIDR = podCIDRs[1]
	}

	return nil
}

// createNamespace creates the provided namespace.
func (data *TestData) createNamespace(namespace string, mutators ...func(ns *corev1.Namespace)) error {
	ns := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	for _, mutator := range mutators {
		mutator(&ns)
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

// createVCNamespace creates a VC namespace with the provided namespace.
func (data *TestData) createVCNamespace(namespace string) error {
	svID, _ := data.vcClient.getSupervisorID()
	_, storagePolicyID, _ := data.vcClient.getStoragePolicyID()
	log.V(1).Info("Get storage policy", "storagePolicyID", storagePolicyID)
	contentLibraryID, _ := data.vcClient.getContentLibraryID()
	log.V(1).Info("Get content library", "contentLibraryID", contentLibraryID)
	vcNamespace := &VCNamespaceCreateSpec{
		Supervisor: svID,
		Namespace:  namespace,
		StorageSpecs: []InstancesStorageSpec{
			{
				Policy: storagePolicyID,
			},
		},
		ContentLibraries: []InstancesContentLibrarySpec{
			{
				ContentLibrary: contentLibraryID,
			},
		},
		NetworkSpec: InstancesNetworkConfigInfo{
			NetworkProvider: "NSX_VPC",
			VpcNetwork: InstancesVpcNetworkInfo{
				DefaultSubnetSize: 16,
			},
		},
		VmServiceSpec: &InstancesVMServiceSpec{
			ContentLibraries: []string{contentLibraryID},
			VmClasses:        []string{"best-effort-xsmall"},
		},
	}

	err := testData.vcClient.startSession()
	if err != nil {
		return err
	}
	defer func() {
		testData.vcClient.closeSession()
	}()

	dataJson, err := json.Marshal(vcNamespace)
	log.V(1).Info("Data json", "dataJson", string(dataJson))
	if err != nil {
		log.Error(err, "Unable convert vcNamespace object to json bytes", "namespace", namespace)
		return fmt.Errorf("unable convert vcNamespace object to json bytes: %v", err)
	}
	request, err := data.vcClient.prepareRequest(http.MethodPost, createVCNamespaceEndpoint, dataJson)
	if err != nil {
		log.Error(err, "Failed to prepare http request with vcNamespace data", "namespace", namespace)
		return fmt.Errorf("failed to parepare http request with vcNamespace data: %v", err)
	}
	if _, err = data.vcClient.handleRequest(request, nil); err != nil {
		log.Error(err, "Failed to create VC namespace", "namespace", namespace)
		return err
	}
	// wait for the namespace on k8s running
	err = wait.PollUntilContextTimeout(context.TODO(), 10*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		ns, err := data.clientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
		if err != nil {
			log.Error(err, "Check namespace existence", "namespace", namespace)
			return false, err
		}

		for _, condition := range ns.Status.Conditions {
			if condition.Type == "NamespaceNetworkReady" && condition.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		testData.deleteVCNamespace(namespace)
	}
	return err
}

// deleteVCNamespace deletes the provided VC namespace and waits for deletion to actually complete.
func (data *TestData) deleteVCNamespace(namespace string) error {
	err := testData.vcClient.startSession()
	if err != nil {
		return err
	}
	defer func() {
		testData.vcClient.closeSession()
	}()

	_ = testData.vcClient.deleteNamespace(namespace)
	// wait for the namespace on k8s terminating
	err = wait.PollUntilContextTimeout(context.TODO(), 10*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		ns, err := data.clientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				log.Info("Namespace not found, it has been deleted", "namespace", namespace)
				return true, nil
			}
			log.Error(err, "Check namespace existence", "namespace", namespace)
			return false, err
		}
		log.Info("Waiting for namespace to be deleted", "namespace", namespace, "status phase", ns.Status.Phase)
		return false, nil
	})
	return err
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

type PodIPs struct {
	ipv4      *net.IP
	ipv6      *net.IP
	ipStrings []string
}

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

func (data *TestData) vmWaitFor(timeout time.Duration, namespace, vmName string) (string, error) {
	var primaryIP4 string
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		cmd := exec.Command("kubectl", "get", "vm", vmName, "-n", namespace, "-o", "jsonpath={.status.network.primaryIP4}")
		output, err := cmd.Output()
		if err != nil {
			var exitError *exec.ExitError
			if stderror.As(err, &exitError) {
				if exitError.ExitCode() == 1 {
					return false, nil
				}
			}
			return false, fmt.Errorf("error when getting VirtualMachine '%s': %v", vmName, err)
		}

		primaryIP4 = strings.TrimSpace(string(output))
		if primaryIP4 == "" {
			return false, nil
		}

		return true, nil
	})
	return primaryIP4, err
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
		log.Error(err, "Failed to wait for Pod becoming RUNNING phase", "Pod", name)
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
		log.Error(err, "Failed to parse Pod's IP", "Pod", name)
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
	log.Info("Running command in Pod's container", "Namespace", namespace, "Pod", podName, "Container", containerName, "Command", cmd)
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
		log.Error(err, "Failed to run command in Pod's container", "Namespace", namespace, "Pod", podName, "Container", containerName, "Command", cmd)
		return stdoutB.String(), stderrB.String(), err
	}
	outStr, errStr := stdoutB.String(), stderrB.String()
	log.Info("Successfully run command in Pod's container", "Namespace", namespace, "Pod", podName, "Container", containerName, "Command", cmd, "stdOut", outStr, "stdErr", errStr)
	return stdoutB.String(), stderrB.String(), nil
}

func applyYAML(filename string, ns string) error {
	cmd := fmt.Sprintf("kubectl apply -f %s -n %s", filename, ns)
	if ns == "" {
		cmd = fmt.Sprintf("kubectl apply -f %s", filename)
	}
	var stdout, stderr bytes.Buffer
	command := exec.Command("bash", "-c", cmd)
	command.Stdout = &stdout
	command.Stderr = &stderr

	log.Info("Executing", "cmd", cmd)

	err := command.Run()
	_, _ = stdout.String(), stderr.String()

	if err != nil {
		log.Info("Failed to execute", "cmd error", err)
		return fmt.Errorf("failed to apply YAML: %w", err)
	}
	return nil
}

func deleteYAML(filename string, ns string) error {
	cmd := fmt.Sprintf("kubectl delete -f %s -n %s", filename, ns)
	if ns == "" {
		cmd = fmt.Sprintf("kubectl delete -f %s", filename)
	}
	var stdout, stderr bytes.Buffer
	command := exec.Command("bash", "-c", cmd)
	log.Info("Executing", "cmd", cmd)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		// Ignore error info
		// very short watch: k8s.io/client-go/tools/watch/informerwatcher.
		// go:146: Unexpected watch close - watch lasted less than a second and no items received
		// log.Error(err, "Error when deleting YAML file")
		return nil
	}
	_, _ = string(stdout.Bytes()), string(stderr.Bytes())
	return nil
}

// queryResource is used to query resource by tags, not handling pagination
// tags should be present in pairs, the first tag is the scope, the second tag is the value
// caller should transform the response to the expected resource type
func (data *TestData) queryResource(resourceType string, tags []string) (model.SearchResponse, error) {
	resourceParam := fmt.Sprintf("%s:%s", common.ResourceType, resourceType)
	queryParam := resourceParam
	if len(tags) >= 2 {
		tagscope := strings.Replace(tags[0], "/", "\\/", -1)
		tagtag := strings.Replace(tags[1], ":", "\\:", -1)
		tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagscope, tagtag)
		queryParam = resourceParam + " AND " + tagParam
	}
	queryParam += " AND marked_for_delete:false"
	var cursor *string
	var pageSize int64 = 500
	response, err := data.nsxClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
	if err != nil {
		log.Info("Error when querying resource ", "resourceType", resourceType, "error", err)
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
			log.Info("Error when querying resource ", "resourceType", resourceType, "key", key, "value", value, "error", err)
			return false, err
		}
		if len(response.Results) == 0 {
			exist = false
		}
		log.V(2).Info("", "QueryParam", queryParam, "exist", exist)
		if exist != shouldExist {
			return false, nil
		}
		return true, nil
	})
	return err
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
			if err == util.HttpNotFoundError && shouldExist {
				return false, nil
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

func (data *TestData) createService(namespace, serviceName string, port, targetPort int32, protocol corev1.Protocol, selector map[string]string,
	serviceType corev1.ServiceType, mutators ...func(service *corev1.Service),
) (*corev1.Service, error) {
	ipFamilies := []corev1.IPFamily{corev1.IPv4Protocol}

	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels: map[string]string{
				"nsx-op-e2e": serviceName,
				"app":        serviceName,
			},
		},
		Spec: corev1.ServiceSpec{
			SessionAffinity: corev1.ServiceAffinityNone,
			Ports: []corev1.ServicePort{{
				Port:       port,
				TargetPort: intstr.FromInt32(targetPort),
				Protocol:   protocol,
			}},
			Type:       serviceType,
			Selector:   selector,
			IPFamilies: ipFamilies,
		},
	}
	for _, mutator := range mutators {
		mutator(&service)
	}
	return data.clientset.CoreV1().Services(namespace).Create(context.TODO(), &service, metav1.CreateOptions{})
}

func (data *TestData) createPod(namespace, podName, containerName, image string, protocol corev1.Protocol, containerPort int32,
	mutators ...func(pod *corev1.Pod),
) (*corev1.Pod, error) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   namespace,
			Annotations: map[string]string{},
			Labels: map[string]string{
				"nsx-op-e2e": podName,
				"app":        podName,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:            containerName,
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Ports: []corev1.ContainerPort{
						{
							Protocol:      protocol,
							ContainerPort: containerPort,
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
			HostNetwork:   false,
			// Set it to 1s for immediate shutdown to reduce test run time and to avoid affecting subsequent tests.
			TerminationGracePeriodSeconds: ptr.To[int64](1),
		},
	}
	for _, mutator := range mutators {
		mutator(&pod)
	}
	return data.clientset.CoreV1().Pods(namespace).Create(context.TODO(), &pod, metav1.CreateOptions{})
}

func (data *TestData) serviceWaitFor(readyTime time.Duration, namespace string, name string, conditionFunc func(svc *corev1.Service) (bool, error)) (*corev1.Service, error) {
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, readyTime, false, func(ctx context.Context) (bool, error) {
		if svc, err := data.clientset.CoreV1().Services(namespace).Get(context.TODO(), name, metav1.GetOptions{}); err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when getting Service '%s/%s': %v", namespace, name, err)
		} else {
			return conditionFunc(svc)
		}
	})
	if err != nil {
		return nil, err
	}
	return data.clientset.CoreV1().Services(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

func (data *TestData) deleteService(nsName string, svcName string) error {
	ctx := context.TODO()
	err := data.clientset.CoreV1().Services(nsName).Delete(ctx, svcName, metav1.DeleteOptions{})
	if err != nil {
		log.Error(err, "Failed to delete Service", "namespace", nsName, "name", svcName)
	}
	return err
}

func (data *TestData) useWCPSetup() bool {
	return data.vcClient != nil
}

func checkTrafficByCurl(ns, podname, containername, ip string, port int32, interval, timeout time.Duration) error {
	// Test traffic from client Pod to server Pod
	url := fmt.Sprintf("http://%s:%d", ip, port)
	cmd := []string{
		`/bin/sh`, "-c", fmt.Sprintf(`curl -s -o /dev/null -w %%{http_code} %s`, url),
	}
	trafficErr := wait.PollUntilContextTimeout(context.TODO(), interval, timeout, true, func(ctx context.Context) (bool, error) {
		stdOut, _, err := testData.runCommandFromPod(ns, podname, containername, cmd)
		if err != nil {
			return false, nil
		}
		statusCode := strings.Trim(stdOut, `"`)
		if statusCode != "200" {
			log.Info("Failed to access ip", "ip", ip, "statusCode", statusCode)
			return false, nil
		}
		return true, nil
	})
	return trafficErr
}

func testSSHConnection(host, username, password string, port int) error {
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106
		Timeout:         5 * time.Second,
	}

	address := fmt.Sprintf("%s:%d", host, port)
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return fmt.Errorf("failed to dial: %v", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	return nil
}

// getRandomString generates a random string by hashing the current timestamp
// and taking the first 8 characters of the hex-encoded hash.
func getRandomString() string {
	timestamp := time.Now().UnixNano()
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d", timestamp)))
	return hex.EncodeToString(hash[:])[:8]
}
