package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderror "errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"golang.org/x/crypto/ssh"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/client/clientset/versioned"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/third_party/retry"
	"github.com/vmware-tanzu/nsx-operator/test/e2e/providers"
)

var log = logger.Log

const (
	createVCNamespaceEndpoint = "/api/vcenter/namespaces/instances/v2"
	defaultTimeout            = 60 * time.Second
	podStartupTimeout         = 2 * time.Minute
	vcNamespaceDeleteTimeout  = 3 * time.Minute
	k8sNamespaceDeleteTimeout = 2 * time.Minute
	vpcNetworkConfigTimeout   = 2 * time.Minute
	PolicyAPI                 = "policy/api/v1"
	e2eCRDClientQPS           = 20
	e2eCRDClientBurst         = 40
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
	logLevel            int
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

func StartParallel(t *testing.T) {
	if parallel := flag.Lookup("test.parallel"); parallel != nil {
		if p, _ := strconv.Atoi(parallel.Value.String()); p > 1 {
			t.Parallel()
		}
	}
}

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
	k8sClientConfig := restclient.CopyConfig(kubeConfig)
	k8sClientConfig.QPS = e2eCRDClientQPS
	k8sClientConfig.Burst = e2eCRDClientBurst
	clientSet, err := clientset.NewForConfig(k8sClientConfig)
	if err != nil {
		return fmt.Errorf("error when creating kubernetes client: %v", err)
	}
	crdClientConfig := restclient.CopyConfig(kubeConfig)
	crdClientConfig.QPS = e2eCRDClientQPS
	crdClientConfig.Burst = e2eCRDClientBurst
	crdClientset, err := versioned.NewForConfig(crdClientConfig)
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
	return data.createK8sNamespace(&ns)
}

func (data *TestData) createK8sNamespace(ns *corev1.Namespace) error {
	namespace := ns.Name
	// Record the name before sending the request so a lost response after a
	// successful API-side create still leaves it eligible for suite cleanup.
	createCtx, createCancel, accepted, wasTracked := beginNamespaceCreate(namespace, false)
	if !accepted {
		return fmt.Errorf("cannot create Kubernetes namespace %q after suite cleanup has started", namespace)
	}
	defer endNamespaceCreate(namespace, false, createCancel)
	if err := createCtx.Err(); err != nil {
		return fmt.Errorf("cannot create Kubernetes namespace %q after suite cleanup has started: %w", namespace, err)
	}
	// This admitted create request has its own deadline. Final cleanup waits for
	// it to settle before sending a delete request.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !trackNamespaceForCleanup(namespace, false) {
		return fmt.Errorf("cannot create Kubernetes namespace %q after suite cleanup has started", namespace)
	}
	ns = ns.DeepCopy()
	if ns.Annotations == nil {
		ns.Annotations = make(map[string]string)
	}
	cleanupToken := getRandomString() + getRandomString()
	ns.Annotations[namespaceCleanupAnnotation] = cleanupToken
	namespaceRegistry.Lock()
	namespaceRegistry.k8sTokens[namespace] = cleanupToken
	namespaceRegistry.Unlock()
	created, err := data.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("error when creating '%s' Namespace: %w", namespace, err)
		}
		if !wasTracked {
			untrackK8sNamespace(namespace)
		}
		existing, getErr := data.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("error when checking existing '%s' Namespace: %w", namespace, getErr)
		}
		if existing.Status.Phase == corev1.NamespaceTerminating || existing.DeletionTimestamp != nil {
			return fmt.Errorf("error when creating '%s' Namespace: namespace exists but is terminating", namespace)
		}
		return fmt.Errorf("error when creating '%s' Namespace: namespace already exists", namespace)
	}
	namespaceRegistry.Lock()
	namespaceRegistry.k8s[namespace] = created.UID
	namespaceRegistry.Unlock()
	return nil
}

// createVCNamespace creates a VC namespace with the provided namespace.
func (data *TestData) createVCNamespace(namespace string) error {
	// Track before the first request so cleanup covers a create whose response
	// is lost, and keep cleanup from snapshotting while creation is in flight.
	createCtx, createCancel, accepted, wasTracked := beginNamespaceCreate(namespace, true)
	if !accepted {
		return fmt.Errorf("cannot create VC namespace %q after suite cleanup has started", namespace)
	}
	defer endNamespaceCreate(namespace, true, createCancel)

	// the supervisor may in status CONFIGURING, waiting for it to be RUNNING
	var svID = ""
	err := wait.PollUntilContextTimeout(createCtx, 10*time.Second, 120*time.Second, false, func(ctx context.Context) (done bool, err error) {
		superID, err := data.vcClient.getSupervisorIDContext(ctx)
		if err != nil {
			log.Error(err, "Failed to get supervisor ID")
			return false, nil
		}
		svID = superID
		return true, nil
	})

	if err != nil {
		log.Error(err, "Failed to get supervisor ID")
		return fmt.Errorf("failed to get supervisor ID: %v", err)
	}
	_, storagePolicyID, _ := data.vcClient.getStoragePolicyIDContext(createCtx)
	log.Debug("Get storage policy", "storagePolicyID", storagePolicyID)
	contentLibraryID, _ := data.vcClient.getContentLibraryIDContext(createCtx)
	log.Debug("Get content library", "contentLibraryID", contentLibraryID)
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

	// Session will be created automatically by ensureSessionContext() in vcClient methods
	// No need to explicitly start/close sessions - they are reused globally

	dataJson, err := json.Marshal(vcNamespace)
	log.Debug("Data json", "dataJson", vcNamespace)
	if err != nil {
		log.Error(err, "Unable convert vcNamespace object to json bytes", "namespace", namespace)
		return fmt.Errorf("unable convert vcNamespace object to json bytes: %v", err)
	}
	request, err := data.vcClient.prepareRequest(http.MethodPost, createVCNamespaceEndpoint, dataJson)
	if err != nil {
		log.Error(err, "Failed to prepare http request with vcNamespace data", "namespace", namespace)
		return fmt.Errorf("failed to parepare http request with vcNamespace data: %v", err)
	}
	if !trackNamespaceForCleanup(namespace, true) {
		return fmt.Errorf("cannot create VC namespace %q after suite cleanup has started", namespace)
	}
	if err := createCtx.Err(); err != nil {
		if !wasTracked {
			untrackVCNamespace(namespace)
		}
		return fmt.Errorf("cannot create VC namespace %q after suite cleanup has started: %w", namespace, err)
	}
	requestCtx, requestCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer requestCancel()
	request = request.WithContext(requestCtx)
	statusCode, requestErr := data.vcClient.handleRequest(request, nil)
	if requestErr != nil {
		if statusCode == http.StatusConflict && !wasTracked {
			untrackVCNamespace(namespace)
		}
		log.Error(requestErr, "Failed to create VC namespace", "namespace", namespace)
		return requestErr
	}
	namespaceRegistry.Lock()
	namespaceRegistry.vc[namespace] = true
	namespaceRegistry.Unlock()
	// wait for the namespace on k8s running
	err = wait.PollUntilContextTimeout(createCtx, 1*time.Second, 2*defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		ns, err := data.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
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
		if !stderror.Is(err, context.Canceled) {
			deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = data.vcClient.deleteNamespaceContext(deleteCtx, namespace)
			deleteCancel()
		}
	}
	return err
}

// deleteVCNamespace requests deletion through the VC API and waits for Kubernetes to observe it.
func (data *TestData) deleteVCNamespace(namespace string) error {
	log.Info("Deleting VC namespace", "namespace", namespace)
	ctx, cancel := context.WithTimeout(context.Background(), vcNamespaceDeleteTimeout)
	defer cancel()
	if err := data.vcClient.deleteNamespaceContext(ctx, namespace); err != nil {
		return fmt.Errorf("failed to delete VC namespace %q: %w", namespace, err)
	}

	if err := data.waitForNamespaceDeletionContext(ctx, namespace, vcNamespaceDeleteTimeout); err != nil {
		return err
	}
	untrackVCNamespace(namespace)
	return nil
}

// deleteNamespace deletes the provided Kubernetes namespace and waits for deletion to complete.
func (data *TestData) deleteNamespace(namespace string) error {
	return data.deleteNamespaceWithTimeout(namespace, k8sNamespaceDeleteTimeout)
}

func (data *TestData) deleteNamespaceWithTimeout(namespace string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var gracePeriodSeconds int64
	propagationPolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodSeconds,
		PropagationPolicy:  &propagationPolicy,
	}
	namespaceRegistry.Lock()
	uid, tracked := namespaceRegistry.k8s[namespace]
	token := namespaceRegistry.k8sTokens[namespace]
	namespaceRegistry.Unlock()
	if tracked {
		if uid == "" {
			observed, err := data.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					untrackK8sNamespace(namespace)
					return nil
				}
				return fmt.Errorf("cannot resolve uncertain creation of namespace %q: %w", namespace, err)
			}
			if token == "" || observed.Annotations[namespaceCleanupAnnotation] != token {
				return fmt.Errorf("cannot establish ownership of namespace %q", namespace)
			}
			uid = observed.UID
		}
		deleteOptions.Preconditions = &metav1.Preconditions{UID: &uid}
	}
	if err := data.clientset.CoreV1().Namespaces().Delete(ctx, namespace, deleteOptions); err != nil {
		if errors.IsNotFound(err) {
			// namespace does not exist, we return right away
			untrackK8sNamespace(namespace)
			return nil
		}
		return fmt.Errorf("error when deleting '%s' Namespace: %v", namespace, err)
	}
	if err := data.waitForNamespaceDeletionContext(ctx, namespace, timeout); err != nil {
		return err
	}
	untrackK8sNamespace(namespace)
	return nil
}

func (data *TestData) waitForNamespaceDeletionContext(ctx context.Context, namespace string, timeout time.Duration) error {
	var lastErr error
	var lastNamespace *corev1.Namespace
	err := wait.PollUntilContextTimeout(ctx, 3*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		observed, err := data.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			log.Info("Namespace successfully deleted", "namespace", namespace)
			return true, nil
		}
		if err != nil {
			lastErr = err
			if isRetryableAPIError(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when checking Namespace %q deletion: %w", namespace, err)
		}
		lastErr = nil
		lastNamespace = observed
		return false, nil
	})
	if err != nil {
		if lastNamespace != nil {
			log.Info("Last observed Namespace before deletion wait failed", "namespace", namespace,
				"phase", lastNamespace.Status.Phase, "deletionTimestamp", lastNamespace.DeletionTimestamp,
				"finalizers", lastNamespace.Spec.Finalizers, "conditions", lastNamespace.Status.Conditions)
		}
		data.logNamespaceDeletionDiagnostics(namespace)
		return fmt.Errorf("timed out or failed waiting for Namespace %q deletion after %s: %w; last GET error: %v", namespace, timeout, err, lastErr)
	}
	return nil
}

func (data *TestData) logNamespaceDeletionDiagnostics(namespace string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ns, err := data.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		log.Error(err, "Failed to inspect Namespace after deletion wait", "namespace", namespace)
		return
	}
	log.Error(nil, "Namespace deletion is still pending", "namespace", namespace, "phase", ns.Status.Phase,
		"deletionTimestamp", ns.DeletionTimestamp, "finalizers", ns.Spec.Finalizers, "conditions", ns.Status.Conditions)

	events, err := data.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Error(err, "Failed to list Namespace events after deletion wait", "namespace", namespace)
	} else {
		for _, event := range events.Items {
			log.Info("Namespace cleanup event", "namespace", namespace, "type", event.Type, "reason", event.Reason,
				"object", event.InvolvedObject.Kind+"/"+event.InvolvedObject.Name, "message", event.Message,
				"count", event.Count, "lastTimestamp", event.LastTimestamp)
		}
	}

	cmdCtx, cmdCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cmdCancel()
	output, cmdErr := exec.CommandContext(cmdCtx, "kubectl", "get", "all", "--ignore-not-found", "-n", namespace, "-o", "wide").CombinedOutput()
	if cmdErr != nil {
		log.Error(cmdErr, "Failed to list remaining standard resources in Namespace", "namespace", namespace, "output", string(output), "contextError", cmdCtx.Err())
	} else if len(output) > 0 {
		log.Info("Remaining standard resources in Namespace", "namespace", namespace, "resources", string(output))
	}
}

func isRetryableAPIError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	return errors.IsTooManyRequests(err) || strings.Contains(err.Error(), "client rate limiter Wait returned an error") ||
		strings.Contains(err.Error(), "rate: Wait(") || errors.IsTimeout(err) || errors.IsServerTimeout(err) ||
		errors.IsServiceUnavailable(err) || (stderror.As(err, &netErr) && netErr.Timeout()) || strings.Contains(err.Error(), "http2: client connection lost")
}

var webhookDiagnosticsOnce sync.Once

func collectWebhookDiagnostics() {
	webhookDiagnosticsOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, ns := range []string{"vmware-system-nsx", "vmware-system-vmop"} {
			output, err := exec.CommandContext(ctx, "kubectl", "get", "pods,services,endpointslices,events", "-n", ns, "-o", "wide", "--request-timeout=5s").CombinedOutput()
			log.Info("Webhook diagnostic snapshot", "namespace", ns, "output", string(output), "error", err)
		}
	})
}

type PodCondition func(*corev1.Pod) (bool, error)

// deploymentWaitForNames polls the K8s apiServer once the specific pods are created, no matter they are running or not.
func (data *TestData) deploymentWaitForNames(timeout time.Duration, namespace, deployment string) ([]string, error) {
	var podNames []string
	opt := metav1.ListOptions{
		LabelSelector: "deployment=" + deployment,
	}
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		if pods, err := data.clientset.CoreV1().Pods(namespace).List(ctx, opt); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var readyPod *corev1.Pod
	err := wait.PollUntilContextTimeout(ctx, time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		pod, err := data.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when getting Pod '%s': %v", name, err)
		}
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			return false, fmt.Errorf("pod %s/%s reached terminal phase %s: %s %s", namespace, name, pod.Status.Phase, pod.Status.Reason, pod.Status.Message)
		}
		ready, err := condition(pod)
		if ready {
			readyPod = pod
		}
		return ready, err
	})
	if err != nil {
		return nil, err
	}
	return readyPod, nil
}

func (data *TestData) vmWaitFor(timeout time.Duration, namespace, vmName string) (string, error) {
	var primaryIP4 string
	pollCount := 0
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		pollCount++
		cmd := exec.Command("kubectl", "get", "vm", vmName, "-n", namespace, "-o", "jsonpath={.status.network.primaryIP4}")
		output, err := cmd.Output()
		if err != nil {
			var exitError *exec.ExitError
			if stderror.As(err, &exitError) {
				if exitError.ExitCode() == 1 {
					log.V(1).Info("VM not found yet", "vmName", vmName, "namespace", namespace, "pollCount", pollCount)
					return false, nil
				}
			}
			return false, fmt.Errorf("error when getting VirtualMachine '%s': %v", vmName, err)
		}

		primaryIP4 = strings.TrimSpace(string(output))
		if primaryIP4 == "" {
			// Log VM phase periodically (every 10 polls) for debugging
			if pollCount%10 == 0 {
				phaseCmd := exec.Command("kubectl", "get", "vm", vmName, "-n", namespace, "-o", "jsonpath={.status.phase}")
				phaseOutput, _ := phaseCmd.Output()
				powerStateCmd := exec.Command("kubectl", "get", "vm", vmName, "-n", namespace, "-o", "jsonpath={.status.powerState}")
				powerStateOutput, _ := powerStateCmd.Output()
				log.Info("Waiting for VM IP", "vmName", vmName, "namespace", namespace, "pollCount", pollCount,
					"phase", string(phaseOutput), "powerState", string(powerStateOutput))
			}
			return false, nil
		}

		return true, nil
	})
	log.Info("vmWaitFor completed", "vmName", vmName, "namespace", namespace, "pollCount", pollCount, "ip", primaryIP4, "error", err)
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
		data.logPodReadinessDiagnostics(namespace, name)
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

func (data *TestData) logPodReadinessDiagnostics(namespace, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pod, err := data.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		log.Error(err, "Failed to inspect Pod after readiness wait", "namespace", namespace, "pod", name)
	} else {
		log.Error(nil, "Pod did not become ready before timeout", "namespace", namespace, "pod", name,
			"phase", pod.Status.Phase, "node", pod.Spec.NodeName, "podIP", pod.Status.PodIP,
			"reason", pod.Status.Reason, "message", pod.Status.Message, "conditions", pod.Status.Conditions,
			"containerStatuses", pod.Status.ContainerStatuses, "initContainerStatuses", pod.Status.InitContainerStatuses)
	}

	events, err := data.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + name,
	})
	if err != nil {
		log.Error(err, "Failed to list Pod events after readiness wait", "namespace", namespace, "pod", name)
		return
	}
	for _, event := range events.Items {
		log.Info("Pod readiness event", "namespace", namespace, "pod", name, "type", event.Type,
			"reason", event.Reason, "message", event.Message, "count", event.Count, "lastTimestamp", event.LastTimestamp)
	}
}

// deploymentWaitForIPsOrNames polls the K8s apiServer until the specified Pod in deployment has an IP address
func (data *TestData) deploymentWaitForIPsOrNames(timeout time.Duration, namespace, deployment string, replica int) ([]string, []string, error) {
	var podIPStrings []string
	var podNames []string
	opt := metav1.ListOptions{
		LabelSelector: "deployment=" + deployment,
	}

	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		podIPStrings = podIPStrings[:0]
		podNames = podNames[:0]
		if pods, err := data.clientset.CoreV1().Pods(namespace).List(context.TODO(), opt); err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when getting Pod %v", err)
		} else {
			for _, p := range pods.Items {
				if p.Status.Phase != corev1.PodRunning || p.Status.PodIP == "" {
					continue
				}
				podIPStrings = append(podIPStrings, p.Status.PodIP)
				podNames = append(podNames, p.Name)
			}
			if len(podNames) < replica {
				return false, nil
			}
			return true, nil
		}
	})
	if err != nil {
		return nil, nil, err
	}
	return podIPStrings, podNames, nil
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
	_, errorString := stdout.String(), stderr.String()

	if err != nil {
		log.Info("Failed to execute", "cmd error", err, "stdout", stdout.String(), "stderr", errorString)
		if strings.Contains(errorString, "failed calling webhook") {
			collectWebhookDiagnostics()
		}
		return fmt.Errorf("failed to apply YAML %q in namespace %q: %w (stdout: %s; stderr: %s)", filename, ns, err, stdout.String(), errorString)
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
		log.Error(err, "Error when deleting YAML file")
		return nil
	}
	_, _ = stdout.String(), stderr.String()
	return nil
}

// queryResource is used to query resource by tags, not handling pagination
// tags should be present in pairs, the first tag is the scope, the second tag is the value
// caller should transform the response to the expected resource type
func (data *TestData) queryResource(resourceType string, tags []string) (model.SearchResponse, error) {
	resourceParam := fmt.Sprintf("%s:%s", common.ResourceType, resourceType)
	queryParam := resourceParam
	if len(tags) >= 2 {
		tagscope := strings.ReplaceAll(tags[0], "/", "\\/")
		tagtag := strings.ReplaceAll(tags[1], ":", "\\:")
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
		resourceParam := fmt.Sprintf("%s:%s AND %s:*%s*", common.ResourceType, resourceType, key, value)
		queryParam := resourceParam

		// Only add the tag query if namespace is not empty and not for inventory resources
		if namespace != "" && !strings.HasPrefix(resourceType, "Container") {
			tagScopeClusterKey := strings.ReplaceAll(common.TagScopeNamespace, "/", "\\/")
			tagScopeClusterValue := strings.ReplaceAll(namespace, ":", "\\:")
			tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagScopeClusterKey, tagScopeClusterValue)
			queryParam = resourceParam + " AND " + tagParam
			queryParam += " AND marked_for_delete:false"
		}

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
		if exist != shouldExist {
			return false, nil
		}
		return true, nil
	})
	return err
}

func (data *TestData) waitForContainerApplicationInstanceToContainAppId(podName string, appId string, shouldContain bool) error {
	return wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, defaultTimeout, false, func(ctx context.Context) (bool, error) {
		queryParam := fmt.Sprintf("resource_type:ContainerApplicationInstance AND display_name:*%s* AND container_application_ids:*%s*", podName, appId)
		var cursor *string
		var pageSize int64 = 500
		response, err := data.nsxClient.QueryClient.List(queryParam, cursor, nil, &pageSize, nil, nil)
		if err != nil {
			return false, err
		}

		exist := len(response.Results) > 0
		if exist != shouldContain {
			return false, nil
		}
		return true, nil
	})
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

func (data *TestData) updateService(service *corev1.Service) error {
	svc, err := data.clientset.CoreV1().Services(service.Namespace).Get(context.TODO(), service.Name, metav1.GetOptions{})
	if err != nil {
		log.Debug("Failed to get service", "service", service.Name, "namespace", service.Namespace, "error", err)
		return err
	}
	svc.Spec = service.Spec
	_, err = data.clientset.CoreV1().Services(service.Namespace).Update(context.TODO(), svc, metav1.UpdateOptions{})
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
				Name:       strings.ToLower(string(protocol)),
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

func (data *TestData) createDeployment(namespace, deploymentName, containerName, image string,
	protocol corev1.Protocol, containerPort int32, replicas int32,
	mutators ...func(deployment *v1.Deployment),
) (*v1.Deployment, error) {
	deployment := v1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        deploymentName,
			Namespace:   namespace,
			Annotations: map[string]string{},
		},
		Spec: v1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"nsx-op-e2e": deploymentName,
					"deployment": deploymentName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"nsx-op-e2e": deploymentName,
						"deployment": deploymentName,
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
					HostNetwork: false,
					// Set it to 1s for immediate shutdown to reduce test run time and to avoid affecting subsequent tests.
					TerminationGracePeriodSeconds: ptr.To[int64](1),
				},
			},
		},
	}

	for _, mutator := range mutators {
		mutator(&deployment)
	}
	return data.clientset.AppsV1().Deployments(namespace).Create(context.TODO(), &deployment, metav1.CreateOptions{})
}

func (data *TestData) createIpAddressAllocation(namespace, name string, visibility string, size int, ip string) error {

	allocation := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			IPAddressBlockVisibility: v1alpha1.IPAddressVisibility(visibility),
		},
	}
	if ip != "" {
		allocation.Spec.AllocationIPs = ip
	} else {
		if size > 0 {
			allocation.Spec.AllocationSize = size
		} else {
			allocation.Spec.AllocationSize = 32
		}
	}
	_, err := data.crdClientset.CrdV1alpha1().IPAddressAllocations(namespace).Create(context.TODO(), allocation, metav1.CreateOptions{})
	return err
}

type IngressRule struct {
	Path        string
	ServiceName string
	ServicePort int32
	PathType    networkingv1.PathType
}

func (data *TestData) createIngress(
	namespace, ingressName string,
	host string,
	tlsSecretName string,
	defaultBackendService string,
	defaultBackendPort int32,
	rules []IngressRule,
	annotations map[string]string,
	mutators ...func(ingress *networkingv1.Ingress),
) (*networkingv1.Ingress, error) {

	// Build Ingress object
	ingress := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ingressName,
			Namespace: namespace,
			Labels: map[string]string{
				"nsx-op-e2e": ingressName,
				"app":        ingressName,
			},
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{
			TLS: []networkingv1.IngressTLS{{
				Hosts:      []string{host},
				SecretName: tlsSecretName,
			}},
			DefaultBackend: &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: defaultBackendService,
					Port: networkingv1.ServiceBackendPort{
						Number: defaultBackendPort,
					},
				},
			},
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{},
					},
				},
			}},
		},
	}

	// Add rules to paths
	for _, rule := range rules {
		ingress.Spec.Rules[0].HTTP.Paths = append(ingress.Spec.Rules[0].HTTP.Paths, networkingv1.HTTPIngressPath{
			Path:     rule.Path,
			PathType: &rule.PathType,
			Backend: networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: rule.ServiceName,
					Port: networkingv1.ServiceBackendPort{
						Number: rule.ServicePort,
					},
				},
			},
		})
	}

	// Apply mutators
	for _, mutator := range mutators {
		mutator(&ingress)
	}

	// Create Ingress
	return data.clientset.NetworkingV1().Ingresses(namespace).Create(context.TODO(), &ingress, metav1.CreateOptions{})
}

func (data *TestData) serviceWaitFor(readyTime time.Duration, namespace string, name string, conditionFunc func(svc *corev1.Service) (bool, error)) (*corev1.Service, error) {
	err := wait.PollUntilContextTimeout(context.TODO(), 3*time.Second, readyTime, false, func(ctx context.Context) (bool, error) {
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

func (data *TestData) ingressWaitFor(readyTime time.Duration, namespace string, name string, conditionFunc func(ingress *networkingv1.Ingress) (bool, error)) (*networkingv1.Ingress, error) {
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, readyTime, false, func(ctx context.Context) (bool, error) {
		if ingress, err := data.clientset.NetworkingV1().Ingresses(namespace).Get(context.TODO(), name, metav1.GetOptions{}); err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("error when getting Ingress '%s/%s': %v", namespace, name, err)
		} else {
			return conditionFunc(ingress)
		}
	})
	if err != nil {
		return nil, err
	}
	return data.clientset.NetworkingV1().Ingresses(namespace).Get(context.TODO(), name, metav1.GetOptions{})
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

type CurlOptions struct {
	Scheme  string            // "http" or "https", defaults to "http"
	Path    string            // e.g., "/coffee", defaults to ""
	Headers map[string]string // e.g., {"host": "cafe.example.com"}, defaults to nil
}
type CurlOption func(*CurlOptions)

// WithScheme sets the HTTP scheme (http or https)
func WithScheme(scheme string) CurlOption {
	return func(o *CurlOptions) {
		o.Scheme = scheme
	}
}

// WithPath sets the URL path
func WithPath(path string) CurlOption {
	return func(o *CurlOptions) {
		o.Path = path
	}

}

// WithHeaders sets the HTTP headers
func WithHeaders(headers map[string]string) CurlOption {
	return func(o *CurlOptions) {
		o.Headers = headers
	}
}

func checkTrafficByCurl(ns, podname, containername, ip string, port int32, shouldPass bool, opts ...CurlOption) bool {
	// Default options
	options := &CurlOptions{
		Scheme:  "http",
		Path:    "",
		Headers: nil,
	}

	// Apply provided options
	for _, option := range opts {
		option(options)
	}

	cmd := []string{"/bin/sh", "-c"}
	url := fmt.Sprintf("%s://%s:%d%s", options.Scheme, ip, port, options.Path)
	curlArgs := []string{"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--connect-timeout", "2", "--max-time", "5"}
	if options.Scheme == "https" {
		curlArgs = append(curlArgs, "-k") // Skip SSL verification for HTTPS
	}
	for key, value := range options.Headers {
		curlArgs = append(curlArgs, "-H", fmt.Sprintf("'%s: %s'", key, value))
	}
	curlArgs = append(curlArgs, url)
	cmd = append(cmd, strings.Join(curlArgs, " "))
	log.Debug("Curl command", "cmd", cmd, "shouldPass", shouldPass)

	// NSX Load Balancer may take time to fully configure and health check backends
	// Use more retries with longer interval to account for LB propagation delay
	const maxAttempts = 10
	const retryInterval = 3 * time.Second

	for i := 0; i < maxAttempts; i++ {
		stdOut, _, err := testData.runCommandFromPod(ns, podname, containername, cmd)
		statusCode := strings.Trim(stdOut, `"`)
		isSuccess := err == nil && statusCode == "200"

		if isSuccess == shouldPass {
			return true
		}
		// Log with more context for debugging
		log.Info("Traffic check attempt", "attempt", i+1, "maxAttempts", maxAttempts, "ip", ip, "statusCode", statusCode, "shouldPass", shouldPass, "isSuccess", isSuccess)
		if i < maxAttempts-1 {
			time.Sleep(retryInterval)
		}
	}
	return false
}

// waitForHTTPEndpointReady waits until a Pod can successfully curl an HTTP endpoint and get 200.
// This is used to avoid flakiness when Pods are "Running" but the container process hasn't started listening yet.
func waitForHTTPEndpointReady(ns, podName, containerName, ip string, port int32, timeout time.Duration, opts ...CurlOption) error {
	deadlineCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Reuse the same curl construction as checkTrafficByCurl, but gate on "200" and stop on timeout.
	options := &CurlOptions{Scheme: "http", Path: "", Headers: nil}
	for _, option := range opts {
		option(options)
	}
	url := fmt.Sprintf("%s://%s:%d%s", options.Scheme, ip, port, options.Path)
	curlArgs := []string{"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--connect-timeout", "2", "--max-time", "5"}
	if options.Scheme == "https" {
		curlArgs = append(curlArgs, "-k")
	}
	for key, value := range options.Headers {
		curlArgs = append(curlArgs, "-H", fmt.Sprintf("'%s: %s'", key, value))
	}
	curlArgs = append(curlArgs, url)
	cmd := []string{"/bin/sh", "-c", strings.Join(curlArgs, " ")}

	return wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, timeout, false, func(ctx context.Context) (done bool, err error) {
		stdOut, _, execErr := testData.runCommandFromPod(ns, podName, containerName, cmd)
		statusCode := strings.Trim(stdOut, `"`)
		if execErr == nil && statusCode == "200" {
			return true, nil
		}
		// Keep retrying until timeout; include last observed code for troubleshooting.
		log.Info("Waiting for HTTP endpoint ready", "pod", podName, "container", containerName, "url", url, "statusCode", statusCode, "execErr", execErr)
		return false, nil
	})
}

func testSSHConnection(host, username, password string, port int, timeout time.Duration, attempts uint, delay time.Duration) error {
	if host == "" || username == "" {
		return fmt.Errorf("host and username are required")
	}

	cfg := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106
		Timeout:         timeout,
	}

	address := net.JoinHostPort(host, strconv.Itoa(port))

	return retry.Do(
		func() error {
			conn, err := ssh.Dial("tcp", address, cfg)
			if err != nil {
				return fmt.Errorf("failed to establish SSH connection to %s: %w", address, err)
			}
			defer func() {
				if closeErr := conn.Close(); closeErr != nil {
					log.Error(closeErr, "Failed to close SSH connection")
				}
			}()

			session, err := conn.NewSession()
			if err != nil {
				return fmt.Errorf("failed to create SSH session: %w", err)
			}
			defer func() {
				if closeErr := session.Close(); closeErr != nil {
					log.Error(closeErr, "Failed to close SSH session")
				}
			}()

			return nil
		},
		retry.Attempts(attempts),
		retry.Delay(delay),
		retry.OnRetry(func(n uint, err error) {
			log.Info("Retrying SSH connection", "attempt", n+1, "total_attempts", attempts, "error", err)
		}),
		retry.LastErrorOnly(true),
	)
}

// getRandomString generates a random string by hashing the current timestamp
// and taking the first 8 characters of the hex-encoded hash.
func getRandomString() string {
	timestamp := time.Now().UnixNano()
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d", timestamp)))
	return hex.EncodeToString(hash[:])[:8]
}
