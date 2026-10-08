// Copyright © 2026 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	nsxerrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/client/clientset/versioned"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	common "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	restoreutil "github.com/vmware-tanzu/nsx-operator/pkg/util"
	p2 "github.com/vmware-tanzu/nsx-operator/test/e2e/podv2"
)

var (
	podV2Only        = flag.Bool("podv2-only", true, "Run only the Pod v2 suite (false restores the existing suite)")
	p2Namespace      = flag.String("podv2-namespace", "", "Existing empty, provisioned VPC namespace; never deleted by this suite")
	p2OperatorNS     = flag.String("podv2-operator-namespace", "", "Namespace of the operator Deployment")
	p2Deployment     = flag.String("podv2-operator-deployment", "", "Operator Deployment name")
	p2Container      = flag.String("podv2-operator-container", "", "Operator container (required for multi-container Deployments)")
	p2ConfigPath     = flag.String("podv2-config-path", "/etc/nsx-ujo/ncp.ini", "Configuration file read by the operator container")
	p2Image          = flag.String("podv2-image", "busybox:1.36", "Workload image with sh, sleep, httpd, wget and nslookup")
	p2Timeout        = flag.Duration("podv2-step-timeout", 5*time.Minute, "Deadline for each asynchronous state transition")
	p2CleanupTimeout = flag.Duration("podv2-cleanup-timeout", 15*time.Minute, "Independent rollback/cleanup deadline")
	p2CleanupOnly    = flag.Bool("podv2-cleanup", false, "Recover an interrupted run from its journal, without running tests")
	p2Artifacts      = flag.String("podv2-artifacts", "podv2-artifacts", "Directory for per-case diagnostics")
	p2RunBudget      = flag.Duration("podv2-budget", 90*time.Minute, "Suite work deadline; cleanup has a separate deadline")
)

var p2DHCPNamespace = flag.String("podv2-dhcp-namespace", "", "Second empty VPC namespace whose default Pod SubnetSet references pre-created DHCP Subnets (TB03)")

const p2Label = "e2e.nsx.vmware.com/podv2-run"
const p2Volume = "podv2-e2e-config"
const p2ConfigMountDir = "/etc/nsx-p2-config"
const p2ConfigMountFile = "/etc/nsx-p2-config/ncp.ini"

var p2NCP = schema.GroupVersionResource{Group: "nsx.vmware.com", Version: "v1", Resource: "ncpconfigs"}

// The Secret is both an exclusive lock and a durable recovery journal. It is
// created BEFORE any testbed mutation, and removed only after verified cleanup.
// Original config bytes never enter the test log or diagnostic artifacts.
type p2Journal struct {
	DHCPNamespace    string
	DHCPNamespaceUID types.UID
	Run              string
	Namespace        string
	NamespaceUID     types.UID
	Deployment       appsv1.Deployment
	Container        string
	ConfigPath       string
	DefaultSet       api.SubnetSet
	NCPExists        bool
	NCPUID           types.UID
	NCPAnnotations   map[string]string
	PodUIDs          []string
	PortPaths        []string
	SubnetUIDs       []string
}

type p2Suite struct {
	activeNamespace string
	operatorCRD     versioned.Interface
	ctx             context.Context
	cancel          context.CancelFunc
	dynamic         dynamic.Interface
	journal         p2Journal
	secret          *corev1.Secret
	mu              sync.Mutex
	cleanupMu       sync.Mutex
	stage           string
	started         time.Time
}

var podV2Suite *p2Suite

var autoCreatedNamespace string

func cleanupAutoCreatedNamespace() {
	if autoCreatedNamespace != "" && testData != nil && testData.useWCPSetup() {
		ns := autoCreatedNamespace
		autoCreatedNamespace = ""
		fmt.Printf("Cleaning up auto-created VC namespace: %s\n", ns)
		if err := testData.deleteVCNamespace(ns); err != nil {
			fmt.Printf("Warning: failed to delete auto-created VC namespace %s: %v\n", ns, err)
		} else {
			fmt.Printf("Successfully deleted auto-created VC namespace: %s\n", ns)
		}
	}
}

func autoDiscoverOperator(client kubernetes.Interface) (string, string, string, error) {
	ctx := context.Background()
	nsCandidates := []string{"vmware-system-nsx", "nsx-system", "kube-system", "default"}
	if *p2OperatorNS != "" {
		nsCandidates = []string{*p2OperatorNS}
	}
	depCandidates := []string{"nsx-ncp", "nsx-operator"}
	if *p2Deployment != "" {
		depCandidates = []string{*p2Deployment}
	}

	for _, ns := range nsCandidates {
		for _, depName := range depCandidates {
			dep, err := client.AppsV1().Deployments(ns).Get(ctx, depName, metav1.GetOptions{})
			if err == nil && dep != nil {
				container := *p2Container
				if container == "" {
					for _, c := range dep.Spec.Template.Spec.Containers {
						if c.Name == "nsx-operator" {
							container = c.Name
							break
						}
					}
					if container == "" && len(dep.Spec.Template.Spec.Containers) == 1 {
						container = dep.Spec.Template.Spec.Containers[0].Name
					}
					if container == "" {
						for _, c := range dep.Spec.Template.Spec.Containers {
							if strings.Contains(c.Name, "operator") {
								container = c.Name
								break
							}
						}
					}
				}
				return ns, depName, container, nil
			}
		}
	}

	for _, ns := range nsCandidates {
		deps, err := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err == nil {
			for _, dep := range deps.Items {
				if strings.Contains(dep.Name, "operator") || strings.Contains(dep.Name, "ncp") {
					container := *p2Container
					if container == "" {
						for _, c := range dep.Spec.Template.Spec.Containers {
							if strings.Contains(c.Name, "operator") {
								container = c.Name
								break
							}
						}
						if container == "" && len(dep.Spec.Template.Spec.Containers) > 0 {
							container = dep.Spec.Template.Spec.Containers[0].Name
						}
					}
					return ns, dep.Name, container, nil
				}
			}
		}
	}
	return "", "", "", fmt.Errorf("could not auto-discover operator deployment (tried namespaces %v, deployments %v)", nsCandidates, depCandidates)
}

func isSystemNamespace(ns string) bool {
	return strings.HasPrefix(ns, "kube-") ||
		strings.HasPrefix(ns, "vmware-system") ||
		strings.HasPrefix(ns, "svc-")
}

func autoDiscoverOrCreateNamespace(ctx context.Context, clientset kubernetes.Interface, crdClientset versioned.Interface) (string, error) {
	// 1. Search for an existing idle VPC namespace
	sets, err := crdClientset.CrdV1alpha1().SubnetSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		vpcCandidates := make(map[string]*api.SubnetSet)
		for i := range sets.Items {
			set := &sets.Items[i]
			if (set.Labels[common.LabelDefaultNetwork] == common.DefaultPodNetwork || set.Labels[common.LabelDefaultSubnetSet] == common.LabelDefaultPodSubnetSet) && set.Spec.IPAddressType != "" {
				vpcCandidates[set.Namespace] = set
			}
		}

		// Find operator deployment ServiceAccount for RBAC dryRun probe
		opSA := "default"
		if *p2OperatorNS != "" && *p2Deployment != "" {
			if dep, getErr := clientset.AppsV1().Deployments(*p2OperatorNS).Get(ctx, *p2Deployment, metav1.GetOptions{}); getErr == nil && dep != nil {
				if dep.Spec.Template.Spec.ServiceAccountName != "" {
					opSA = dep.Spec.Template.Spec.ServiceAccountName
				}
			}
		}

		for nsName, defaultSet := range vpcCandidates {
			if isSystemNamespace(nsName) || nsName == "default" {
				continue
			}
			ns, e := clientset.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})
			if e != nil || ns.DeletionTimestamp != nil {
				continue
			}
			pods, e := clientset.CoreV1().Pods(nsName).List(ctx, metav1.ListOptions{})
			if e != nil {
				continue
			}
			hasWorkload := false
			for _, p := range pods.Items {
				if !p.Spec.HostNetwork {
					hasWorkload = true
					break
				}
			}
			if hasWorkload {
				continue
			}
			ports, e := crdClientset.CrdV1alpha1().SubnetPorts(nsName).List(ctx, metav1.ListOptions{})
			if e != nil || len(ports.Items) > 0 {
				continue
			}
			stss, e := clientset.AppsV1().StatefulSets(nsName).List(ctx, metav1.ListOptions{})
			if e != nil || len(stss.Items) > 0 {
				continue
			}
			// Probe permission with impersonation
			probe := defaultSet.DeepCopy()
			delete(probe.Labels, common.LabelDefaultNetwork)
			impersonated := rest.CopyConfig(testData.kubeConfig)
			impersonated.Impersonate = rest.ImpersonationConfig{
				UserName: "system:serviceaccount:" + *p2OperatorNS + ":" + opSA,
			}
			opCRD, crdErr := versioned.NewForConfig(impersonated)
			if crdErr == nil {
				_, updateErr := opCRD.CrdV1alpha1().SubnetSets(nsName).Update(ctx, probe, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
				if updateErr != nil {
					continue
				}
			}

			fmt.Printf("Auto-discovered idle VPC namespace for Pod v2 testing: %s\n", nsName)
			return nsName, nil
		}
	}

	// 2. If VC client is configured and useWCPSetup, dynamically create dedicated namespace
	if testData != nil && testData.useWCPSetup() {
		testNS := "e2e-p2-" + getRandomString()
		fmt.Printf("No existing idle VPC namespace found; creating dedicated test namespace: %s\n", testNS)
		if err := testData.createVCNamespace(testNS); err != nil {
			return "", fmt.Errorf("failed to auto-create VC namespace %s: %w", testNS, err)
		}
		autoCreatedNamespace = testNS
		err = wait.PollUntilContextTimeout(ctx, 3*time.Second, 180*time.Second, true, func(c context.Context) (bool, error) {
			sList, e := crdClientset.CrdV1alpha1().SubnetSets(testNS).List(c, metav1.ListOptions{})
			if e != nil {
				return false, nil
			}
			for _, set := range sList.Items {
				if set.Labels[common.LabelDefaultNetwork] == common.DefaultPodNetwork && set.Spec.IPAddressType != "" {
					return true, nil
				}
			}
			return false, nil
		})
		if err != nil {
			return "", fmt.Errorf("timed out waiting for default Pod SubnetSet in %s: %w", testNS, err)
		}
		return testNS, nil
	}

	return "", fmt.Errorf("no idle VPC namespace found and cannot auto-create via VC API")
}

func podV2Main(m *testing.M) int {
	kubeconfigPath := ""
	if f := flag.Lookup("remote.kubeconfig"); f != nil {
		kubeconfigPath = f.Value.String()
	}
	if kubeconfigPath == "" {
		if p, err := provider.GetKubeconfigPath(); err == nil {
			kubeconfigPath = p
		}
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Load kubeconfig failed:", err)
		return 2
	}
	cfg.Timeout = 30 * time.Second
	testData = &TestData{kubeConfig: cfg}
	testData.clientset, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	testData.crdClientset, err = versioned.NewForConfig(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	config.UpdateConfigFilePath(testOptions.operatorConfigPath)
	cf, err := config.NewNSXOperatorConfigFromFile()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Load local NSX client configuration:", err)
		return 2
	}
	cf.NsxConfig.HttpTimeout = 30
	if err = testData.createNSXClients(cf); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if testOptions.vcUser != "" && testOptions.vcPassword != "" {
		testData.vcClient = newVcClient(cf.VCEndPoint, cf.HttpsPort, testOptions.vcUser, testOptions.vcPassword)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *p2RunBudget)
	defer cancel()

	// Auto-discover operator namespace, deployment and container if not provided
	if *p2OperatorNS == "" || *p2Deployment == "" || *p2Container == "" {
		discoveredNS, discoveredDep, discoveredContainer, dErr := autoDiscoverOperator(testData.clientset)
		if dErr == nil {
			if *p2OperatorNS == "" {
				*p2OperatorNS = discoveredNS
			}
			if *p2Deployment == "" {
				*p2Deployment = discoveredDep
			}
			if *p2Container == "" {
				*p2Container = discoveredContainer
			}
			fmt.Printf("Auto-discovered operator: namespace=%s deployment=%s container=%s\n", *p2OperatorNS, *p2Deployment, *p2Container)
		}
	}
	if *p2OperatorNS == "" || *p2Deployment == "" {
		fmt.Fprintln(os.Stderr, "Pod v2 requires -podv2-operator-namespace and -podv2-operator-deployment; see test/e2e/PODV2.md")
		return 2
	}

	// Auto-discover or auto-create test namespace if not provided
	if !*p2CleanupOnly && *p2Namespace == "" {
		ns, nErr := autoDiscoverOrCreateNamespace(ctx, testData.clientset, testData.crdClientset)
		if nErr != nil {
			fmt.Fprintf(os.Stderr, "Pod v2 requires -podv2-namespace: %v; see test/e2e/PODV2.md\n", nErr)
			return 2
		}
		*p2Namespace = ns
	}
	defer cleanupAutoCreatedNamespace()

	s := &p2Suite{ctx: ctx, cancel: cancel, dynamic: dyn, started: time.Now()}
	podV2Suite = s
	if *p2CleanupOnly {
		if err := s.loadJournal(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if s.secret == nil {
			fmt.Println("No interrupted Pod v2 run to clean up.")
			return 0
		}
		if err := s.initOperatorCRD(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := s.cleanup(); err != nil {
			fmt.Fprintln(os.Stderr, "CLEANUP FAILED:", err)
			return 1
		}
		fmt.Println("CLEANUP PASS: original operator restored; no test-owned resources remain.")
		return 0
	}
	filter := flag.Lookup("test.run").Value.String()
	if filter == "" || filter == "TestPodV2" || filter == "^TestPodV2$" {
		_ = flag.Set("test.run", "^TestPodV2$")
	} else if strings.HasPrefix(filter, "TestPodV2/") {
		_ = flag.Set("test.run", "^TestPodV2$/"+strings.TrimPrefix(filter, "TestPodV2/"))
	} else if !strings.HasPrefix(filter, "^TestPodV2$") {
		fmt.Fprintln(os.Stderr, "Use -run '^TestPodV2$/TB02' to select a case; unrelated tests are disabled on this branch")
		return 2
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-sig:
			fmt.Fprintln(os.Stderr, "Interrupted: cancelling test work; rollback will run.")
			cancel()
		case <-done:
		}
	}()
	testResultTracker.startTime = time.Now()
	ret := m.Run()
	// Safety net for failures before test cleanup registration. Idempotent.
	if err := s.cleanup(); err != nil {
		fmt.Fprintln(os.Stderr, "CLEANUP FAILED:", err)
		ret = 1
	}
	testResultTracker.PrintSummary()
	return ret
}

func (s *p2Suite) journalName() string { return *p2Deployment + "-podv2-e2e-state" }
func (s *p2Suite) loadJournal() error {
	secret, err := testData.clientset.CoreV1().Secrets(*p2OperatorNS).Get(s.ctx, s.journalName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(secret.Data["journal.json"], &s.journal); err != nil {
		return err
	}
	if secret.Labels[p2Label] != s.journal.Run || s.journal.Deployment.Name != *p2Deployment || s.journal.Deployment.Namespace != *p2OperatorNS {
		return fmt.Errorf("journal ownership mismatch")
	}
	s.secret = secret
	return nil
}
func (s *p2Suite) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(s.journal)
	if err != nil {
		return err
	}
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		sec, e := testData.clientset.CoreV1().Secrets(*p2OperatorNS).Get(s.ctx, s.journalName(), metav1.GetOptions{})
		if e != nil {
			return e
		}
		if sec.UID != s.secret.UID {
			return fmt.Errorf("journal UID changed")
		}
		sec.Data["journal.json"] = b
		updated, e := testData.clientset.CoreV1().Secrets(*p2OperatorNS).Update(s.ctx, sec, metav1.UpdateOptions{})
		if e == nil {
			s.secret = updated
		}
		return e
	})
}
func (s *p2Suite) step(t *testing.T, name string) {
	t.Helper()
	s.stage = name
	t.Logf("STAGE %s", name)
}
func (s *p2Suite) poll(ctx context.Context, name string, f func(context.Context) (bool, string, error)) error {
	last := "no observation"
	nextProgress := time.Now().Add(20 * time.Second)
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, *p2Timeout, true, func(c context.Context) (bool, error) {
		ok, observation, e := f(c)
		last = observation
		if e != nil {
			last = fmt.Sprintf("%s; API error: %v", observation, e)
		}
		if !ok && time.Now().After(nextProgress) {
			fmt.Printf("WAIT stage=%s last=%s\n", name, last)
			nextProgress = time.Now().Add(20 * time.Second)
		}
		// Authentication and authorization failures are not eventual consistency.
		if apierrors.IsForbidden(e) || apierrors.IsUnauthorized(e) {
			return false, e
		}
		return ok && e == nil, nil
	})
	if err != nil {
		return fmt.Errorf("stage=%s: %w; last observation: %s", name, err, last)
	}
	return nil
}
func (s *p2Suite) wait(t *testing.T, name string, f func(context.Context) (bool, string, error)) {
	t.Helper()
	s.step(t, name)
	require.NoError(t, s.poll(s.ctx, name, f))
}
func (s *p2Suite) exec(ctx context.Context, ns, pod, container string, command []string) (string, error) {
	// The legacy exec helper logs stdout, which would expose ncp.ini credentials.
	req := testData.clientset.CoreV1().RESTClient().Post().Namespace(ns).Resource("pods").Name(pod).SubResource("exec").VersionedParams(&corev1.PodExecOptions{Container: container, Command: command, Stdout: true, Stderr: true}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(testData.kubeConfig, "POST", req.URL())
	if err != nil {
		return "", err
	}
	var out, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &out, Stderr: &stderr})
	// Callers decide what can be logged; config reads never include output in errors.
	return out.String(), err
}
func (s *p2Suite) operatorPods(ctx context.Context) ([]corev1.Pod, error) {
	d, err := testData.clientset.AppsV1().Deployments(*p2OperatorNS).Get(ctx, *p2Deployment, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return nil, err
	}
	pods, err := testData.clientset.CoreV1().Pods(*p2OperatorNS).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}
func (s *p2Suite) preflight(t *testing.T) {
	s.step(t, "preflight: empty namespace, operator, transport node and rollback journal")
	if podV2CaseSelected("TB03_PrecreatedDHCP") && *p2DHCPNamespace == "" {
		t.Log("Note: -podv2-dhcp-namespace not provided; TB03_PrecreatedDHCP will be skipped")
	}
	require.Positive(t, *p2Timeout)
	require.Positive(t, *p2CleanupTimeout)
	existing, journalErr := testData.clientset.CoreV1().Secrets(*p2OperatorNS).Get(s.ctx, s.journalName(), metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(journalErr), "Cannot acquire run: existing journal=%t error=%v. Use -podv2-cleanup only for an interrupted run.", existing != nil, journalErr)
	ns, err := testData.clientset.CoreV1().Namespaces().Get(s.ctx, *p2Namespace, metav1.GetOptions{})
	require.NoError(t, err)
	require.Nil(t, ns.DeletionTimestamp)
	var d *appsv1.Deployment
	// Wait for the operator deployment to stabilize and become healthy after any prior restart or scaling.
	waitBudget := 180 * time.Second
	pollErr := wait.PollUntilContextTimeout(s.ctx, 3*time.Second, waitBudget, true, func(c context.Context) (bool, error) {
		currentD, getErr := testData.clientset.AppsV1().Deployments(*p2OperatorNS).Get(c, *p2Deployment, metav1.GetOptions{})
		if getErr != nil {
			return false, getErr
		}
		d = currentD
		if d.Status.ObservedGeneration >= d.Generation && d.Spec.Replicas != nil && d.Status.AvailableReplicas == *d.Spec.Replicas {
			return true, nil
		}
		return false, nil
	})
	require.NotNil(t, d, "failed to get operator deployment")
	require.NotNil(t, d.Spec.Replicas)
	require.Greater(t, *d.Spec.Replicas, int32(0))
	require.False(t, d.Spec.Paused)
	hpas, e := testData.clientset.AutoscalingV2().HorizontalPodAutoscalers(*p2OperatorNS).List(s.ctx, metav1.ListOptions{})
	require.NoError(t, e)
	for _, h := range hpas.Items {
		require.False(t, h.Spec.ScaleTargetRef.Kind == "Deployment" && h.Spec.ScaleTargetRef.Name == d.Name, "operator HPA would fight the test's stop/start")
	}

	if pollErr != nil {
		t.Logf("Operator deployment replicas (%d) did not reach available (%d) after %v: %v", *d.Spec.Replicas, d.Status.AvailableReplicas, waitBudget, pollErr)
		require.Greater(t, d.Status.AvailableReplicas, int32(0), "operator must have at least one available replica before testing")
		if *d.Spec.Replicas != d.Status.AvailableReplicas {
			t.Logf("Adapting baseline desired replicas from %d to %d to match available cluster capacity", *d.Spec.Replicas, d.Status.AvailableReplicas)
			d.Spec.Replicas = ptr.To[int32](d.Status.AvailableReplicas)
		}
	}
	if *p2Container == "" {
		for _, c := range d.Spec.Template.Spec.Containers {
			if c.Name == "nsx-operator" {
				*p2Container = c.Name
				break
			}
		}
		if *p2Container == "" && len(d.Spec.Template.Spec.Containers) == 1 {
			*p2Container = d.Spec.Template.Spec.Containers[0].Name
		}
		if *p2Container == "" {
			for _, c := range d.Spec.Template.Spec.Containers {
				if strings.Contains(c.Name, "operator") {
					*p2Container = c.Name
					break
				}
			}
		}
		require.NotEmpty(t, *p2Container, "Specify -podv2-operator-container")
	}
	found := false
	for _, c := range d.Spec.Template.Spec.Containers {
		if c.Name == *p2Container {
			found = true
			args := append(append([]string{}, c.Command...), c.Args...)
			for i, arg := range args {
				if arg == "-nsxconfig" || arg == "--nsxconfig" {
					if i+1 < len(args) && *p2ConfigPath == "/etc/nsx-ujo/ncp.ini" {
						*p2ConfigPath = args[i+1]
					}
					require.Less(t, i+1, len(args))
					require.Equal(t, args[i+1], *p2ConfigPath, "-podv2-config-path differs from operator -nsxconfig")
				}
				if strings.HasPrefix(arg, "-nsxconfig=") || strings.HasPrefix(arg, "--nsxconfig=") {
					cfgVal := strings.SplitN(arg, "=", 2)[1]
					if *p2ConfigPath == "/etc/nsx-ujo/ncp.ini" {
						*p2ConfigPath = cfgVal
					}
					require.Equal(t, cfgVal, *p2ConfigPath, "-podv2-config-path differs from operator -nsxconfig")
				}
			}
		}
	}
	require.True(t, found, "operator container does not exist")
	for _, v := range d.Spec.Template.Spec.Volumes {
		require.NotEqual(t, p2Volume, v.Name, "reserved test volume already present")
	}
	require.True(t, strings.HasPrefix(*p2ConfigPath, "/"), "operator config path must be absolute")
	sets, err := testData.crdClientset.CrdV1alpha1().SubnetSets("").List(s.ctx, metav1.ListOptions{})
	require.NoError(t, err)
	vpcNS := map[string]bool{*p2Namespace: true}
	if *p2DHCPNamespace != "" {
		vpcNS[*p2DHCPNamespace] = true
	}
	var defaultSet *api.SubnetSet
	for i := range sets.Items {
		set := &sets.Items[i]
		if set.Labels[common.LabelDefaultNetwork] != common.DefaultPodNetwork && set.Labels[common.LabelDefaultSubnetSet] != common.LabelDefaultPodSubnetSet {
			continue
		}
		if !isSystemNamespace(set.Namespace) {
			vpcNS[set.Namespace] = true
		}
		if set.Namespace == ns.Name {
			require.Nil(t, defaultSet, "multiple default Pod SubnetSets")
			defaultSet = set
		}
	}
	require.NotNil(t, defaultSet, "namespace needs a default Pod SubnetSet")
	require.NotEmpty(t, defaultSet.Spec.IPAddressType, "default Pod SubnetSet IP family is not populated")
	require.Equal(t, common.DefaultPodNetwork, defaultSet.Labels[common.LabelDefaultNetwork], "primary namespace default must carry nsx.vmware.com/default-network=pod")
	// The feature flag is global. Do not reconcile unrelated legacy workloads in v2.
	pods, err := testData.clientset.CoreV1().Pods("").List(s.ctx, metav1.ListOptions{})
	require.NoError(t, err)
	for _, p := range pods.Items {
		if isSystemNamespace(p.Namespace) {
			continue
		}
		if vpcNS[p.Namespace] && !p.Spec.HostNetwork {
			if p.Namespace == ns.Name || (*p2DHCPNamespace != "" && p.Namespace == *p2DHCPNamespace) {
				require.FailNow(t, "testbed is not quiescent", "existing Pod %s/%s in test namespace", p.Namespace, p.Name)
			}
			t.Logf("Notice: existing Pod %s/%s in unrelated VPC namespace %s", p.Namespace, p.Name, p.Namespace)
		}
	}
	ports, err := testData.crdClientset.CrdV1alpha1().SubnetPorts(ns.Name).List(s.ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Empty(t, ports.Items, "use a namespace without existing SubnetPorts")
	allPorts, e := testData.crdClientset.CrdV1alpha1().SubnetPorts("").List(s.ctx, metav1.ListOptions{})
	require.NoError(t, e)
	var testNamespacePorts []string
	var unrelatedPorts []string
	for _, port := range allPorts.Items {
		if isSystemNamespace(port.Namespace) {
			continue
		}
		if port.Namespace == ns.Name || (*p2DHCPNamespace != "" && port.Namespace == *p2DHCPNamespace) {
			testNamespacePorts = append(testNamespacePorts, fmt.Sprintf("%s/%s", port.Namespace, port.Name))
		} else {
			unrelatedPorts = append(unrelatedPorts, fmt.Sprintf("%s/%s", port.Namespace, port.Name))
		}
	}
	require.Empty(t, testNamespacePorts, "use a namespace without existing SubnetPorts: %v", testNamespacePorts)
	if len(unrelatedPorts) > 0 {
		t.Logf("Notice: cluster has existing SubnetPort CRs in unrelated namespaces: %v", unrelatedPorts)
	}
	stss, err := testData.clientset.AppsV1().StatefulSets(ns.Name).List(s.ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Empty(t, stss.Items)
	nsxPorts, err := s.ports(common.TagScopeNamespaceUID, string(ns.UID))
	require.NoError(t, err)
	require.Empty(t, nsxPorts, "namespace already has NSX ports")
	opPods, err := s.operatorPods(s.ctx)
	require.NoError(t, err)
	var running *corev1.Pod
	for i := range opPods {
		if opPods[i].Status.Phase == corev1.PodRunning && opPods[i].DeletionTimestamp == nil {
			running = &opPods[i]
			break
		}
	}
	require.NotNil(t, running)
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	original, err := s.exec(ctx, *p2OperatorNS, running.Name, *p2Container, []string{"cat", *p2ConfigPath})
	require.NoError(t, err, "cannot read runtime config (contents omitted)")
	_, err = p2.Config([]byte(original), true, false, false, false)
	require.NoError(t, err, "runtime config is not valid INI")
	ncp, err := s.dynamic.Resource(p2NCP).Get(s.ctx, restoreutil.NSXRestoreStatus, metav1.GetOptions{})
	require.True(t, err == nil || apierrors.IsNotFound(err), "read restore status: %v", err)
	s.journal = p2Journal{Run: fmt.Sprintf("p2-%s", getRandomString()[:8]), Namespace: ns.Name, NamespaceUID: ns.UID, Deployment: *d.DeepCopy(), Container: *p2Container, ConfigPath: *p2ConfigPath, DefaultSet: *defaultSet.DeepCopy()}
	if err == nil {
		s.journal.NCPExists = true
		s.journal.NCPUID = ncp.GetUID()
		s.journal.NCPAnnotations = ncp.GetAnnotations()
		force, parseErr := strconv.ParseBool(s.journal.NCPAnnotations[restoreutil.AnnotationForceRestore])
		require.False(t, force, "restore already forced")
		require.True(t, parseErr == nil || s.journal.NCPAnnotations[restoreutil.AnnotationForceRestore] == "", "invalid force_restore annotation")
	}
	require.NoError(t, s.initOperatorCRD())
	probe := defaultSet.DeepCopy()
	delete(probe.Labels, common.LabelDefaultNetwork)
	delete(probe.Labels, common.LabelDefaultSubnetSet)
	_, err = s.operatorCRD.CrdV1alpha1().SubnetSets(ns.Name).Update(s.ctx, probe, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
	require.NoError(t, err, "preflight: need permission to impersonate operator service account for default SubnetSet label changes")
	probeRestore := defaultSet.DeepCopy()
	_, err = s.operatorCRD.CrdV1alpha1().SubnetSets(ns.Name).Update(s.ctx, probeRestore, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
	require.NoError(t, err, "preflight: need permission to restore default SubnetSet labels")
	if *p2DHCPNamespace != "" {
		require.NotEqual(t, ns.Name, *p2DHCPNamespace, "DHCP fixture must use a separate namespace")
		dhcpNS, e := testData.clientset.CoreV1().Namespaces().Get(s.ctx, *p2DHCPNamespace, metav1.GetOptions{})
		require.NoError(t, e)
		existing, e := testData.crdClientset.CrdV1alpha1().SubnetPorts(dhcpNS.Name).List(s.ctx, metav1.ListOptions{})
		require.NoError(t, e)
		require.Empty(t, existing.Items)
		backends, e := s.ports(common.TagScopeNamespaceUID, string(dhcpNS.UID))
		require.NoError(t, e)
		require.Empty(t, backends)
		sets, e := testData.crdClientset.CrdV1alpha1().SubnetSets(dhcpNS.Name).List(s.ctx, metav1.ListOptions{LabelSelector: common.LabelDefaultNetwork + "=" + common.DefaultPodNetwork})
		require.NoError(t, e)
		require.Len(t, sets.Items, 1)
		require.NotNil(t, sets.Items[0].Spec.SubnetNames, "DHCP default must reference pre-created Subnets")
		require.NotEmpty(t, *sets.Items[0].Spec.SubnetNames)
		s.journal.DHCPNamespace = dhcpNS.Name
		s.journal.DHCPNamespaceUID = dhcpNS.UID
	}
	b, err := json.Marshal(s.journal)
	require.NoError(t, err)
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: s.journalName(), Namespace: *p2OperatorNS, Labels: map[string]string{p2Label: s.journal.Run}},
		Data: map[string][]byte{
			"journal.json": b,
			"original.ini": []byte(original),
			"active.ini":   []byte(original),
			"ncp.ini":      []byte(original),
		},
	}
	s.secret, err = testData.clientset.CoreV1().Secrets(*p2OperatorNS).Create(s.ctx, sec, metav1.CreateOptions{})
	require.NoError(t, err, "acquire exclusive run journal")
	t.Logf("RUN %s: namespace=%s; recovery: same invocation with -podv2-cleanup", s.journal.Run, ns.Name)
}

func (s *p2Suite) updateDeployment(ctx context.Context, f func(*appsv1.Deployment)) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		d, err := testData.clientset.AppsV1().Deployments(*p2OperatorNS).Get(ctx, *p2Deployment, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if d.UID != s.journal.Deployment.UID {
			return fmt.Errorf("operator Deployment UID changed; refusing overwrite")
		}
		f(d)
		_, err = testData.clientset.AppsV1().Deployments(*p2OperatorNS).Update(ctx, d, metav1.UpdateOptions{})
		return err
	})
}
func (s *p2Suite) stop(ctx context.Context) error {
	if err := s.updateDeployment(ctx, func(d *appsv1.Deployment) { d.Spec.Replicas = ptr.To[int32](0) }); err != nil {
		return err
	}
	return s.poll(ctx, "operator stopped", func(c context.Context) (bool, string, error) {
		p, e := s.operatorPods(c)
		return len(p) == 0, fmt.Sprintf("%d operator Pods remain", len(p)), e
	})
}
func (s *p2Suite) start(ctx context.Context) error {
	return s.updateDeployment(ctx, func(d *appsv1.Deployment) { d.Spec.Replicas = ptr.To[int32](1) })
}
func (s *p2Suite) normal(ctx context.Context) error {
	return s.poll(ctx, "operator running in normal mode", func(c context.Context) (bool, string, error) {
		d, e := testData.clientset.AppsV1().Deployments(*p2OperatorNS).Get(c, *p2Deployment, metav1.GetOptions{})
		if e != nil {
			return false, "get Deployment", e
		}
		if d.Status.ObservedGeneration < d.Generation || d.Status.AvailableReplicas < 1 {
			diag := fmt.Sprintf("generation=%d observed=%d available=%d", d.Generation, d.Status.ObservedGeneration, d.Status.AvailableReplicas)
			if pods, listErr := s.operatorPods(c); listErr == nil && len(pods) > 0 {
				var podStates []string
				for _, p := range pods {
					st := fmt.Sprintf("%s(phase=%s", p.Name, p.Status.Phase)
					for _, cs := range p.Status.ContainerStatuses {
						if cs.State.Waiting != nil {
							msg := cs.State.Waiting.Message
							if len(msg) > 60 {
								msg = msg[:60] + "..."
							}
							st += fmt.Sprintf(",%s:waiting[%s]:%s", cs.Name, cs.State.Waiting.Reason, msg)
						} else if cs.State.Terminated != nil {
							st += fmt.Sprintf(",%s:terminated[%s]", cs.Name, cs.State.Terminated.Reason)
						}
					}
					st += ")"
					podStates = append(podStates, st)
				}
				diag += fmt.Sprintf(" pods=[%s]", strings.Join(podStates, "; "))
			}
			return false, diag, nil
		}
		logs, e := s.operatorLogs(c, false)
		if e != nil || !strings.Contains(logs, "Enter normal mode") {
			return false, "waiting for Enter normal mode in current container", e
		}
		pods, e := s.operatorPods(c)
		if e != nil {
			return false, "list operator Pods", e
		}
		for _, p := range pods {
			if p.DeletionTimestamp != nil || p.Status.Phase != corev1.PodRunning {
				continue
			}
			readCtx, cancel := context.WithTimeout(c, 20*time.Second)
			actual, execErr := s.exec(readCtx, p.Namespace, p.Name, s.journal.Container, []string{"cat", p2ConfigMountFile})
			cancel()
			if execErr != nil {
				return false, "read mounted test configuration", execErr
			}
			if actual != string(s.secret.Data["active.ini"]) {
				return false, "operator runtime config must match the selected test settings (contents omitted)", nil
			}
			probeSet := &api.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "webhook-probe",
					Namespace: "default",
				},
				Spec: api.SubnetSetSpec{
					IPAddressType: api.IPAddressTypeIPv4,
					IPv4SubnetSize: 32,
					AccessMode:    api.AccessMode(api.AccessModePrivate),
				},
			}
			_, probeErr := testData.crdClientset.CrdV1alpha1().SubnetSets("default").Create(c, probeSet, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
			if probeErr != nil && (strings.Contains(probeErr.Error(), "failed calling webhook") || strings.Contains(probeErr.Error(), "connection refused")) {
				return false, fmt.Sprintf("waiting for operator validating webhook (%v)", probeErr), nil
			}
			return true, "", nil
		}
		return false, "no running operator Pod", nil
	})
}
func (s *p2Suite) operatorLogs(ctx context.Context, previous bool) (string, error) {
	pods, err := s.operatorPods(ctx)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	var failures []error
	for _, p := range pods {
		b, e := testData.clientset.CoreV1().Pods(p.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{Container: s.journal.Container, Previous: previous, LimitBytes: ptr.To[int64](128 * 1024)}).DoRaw(ctx)
		if e != nil {
			failures = append(failures, e)
			continue
		}
		out.Write(b)
	}
	if out.Len() == 0 && len(failures) > 0 {
		return "", errors.Join(failures...)
	}
	return out.String(), nil
}
func (s *p2Suite) configure(ctx context.Context, v2, enhance, restore, vif bool) error {
	if err := s.stop(ctx); err != nil {
		return err
	}
	b, err := p2.Config(s.secret.Data["original.ini"], v2, enhance, restore, vif)
	if err != nil {
		return err
	}
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		sec, e := testData.clientset.CoreV1().Secrets(*p2OperatorNS).Get(ctx, s.journalName(), metav1.GetOptions{})
		if e != nil {
			return e
		}
		if sec.UID != s.secret.UID {
			return fmt.Errorf("journal replaced")
		}
		sec.Data["active.ini"] = b
		sec.Data["ncp.ini"] = b
		updated, e := testData.clientset.CoreV1().Secrets(*p2OperatorNS).Update(ctx, sec, metav1.UpdateOptions{})
		if e == nil {
			s.secret = updated
		}
		return e
	})
	if err != nil {
		return err
	}
	return s.updateDeployment(ctx, func(d *appsv1.Deployment) {
		// Start from the saved template, so mode changes do not accumulate mounts.
		d.Spec.Template = *s.journal.Deployment.Spec.Template.DeepCopy()
		if d.Spec.Template.Annotations == nil {
			d.Spec.Template.Annotations = map[string]string{}
		}
		d.Spec.Template.Annotations[p2Label] = s.journal.Run
		d.Spec.Template.Spec.Volumes = append(d.Spec.Template.Spec.Volumes, corev1.Volume{Name: p2Volume, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: s.journalName()}}})
		for i := range d.Spec.Template.Spec.Containers {
			c := &d.Spec.Template.Spec.Containers[i]
			if c.Name != s.journal.Container {
				continue
			}
			// Mount our test config secret at a dedicated, non-overlapping directory
			// to avoid conflicting with existing read-only volume mounts (e.g. /etc/nsx-ujo).
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: p2Volume, MountPath: p2ConfigMountDir, ReadOnly: true})
			// Update -nsxconfig argument to point to the dedicated mounted config.
			replaceCfg := func(args []string) []string {
				if len(args) == 0 {
					return args
				}
				newArgs := make([]string, len(args))
				copy(newArgs, args)
				for idx, arg := range newArgs {
					if (arg == "-nsxconfig" || arg == "--nsxconfig") && idx+1 < len(newArgs) {
						newArgs[idx+1] = p2ConfigMountFile
					} else if strings.HasPrefix(arg, "-nsxconfig=") {
						newArgs[idx] = "-nsxconfig=" + p2ConfigMountFile
					} else if strings.HasPrefix(arg, "--nsxconfig=") {
						newArgs[idx] = "--nsxconfig=" + p2ConfigMountFile
					}
				}
				return newArgs
			}
			c.Command = replaceCfg(c.Command)
			c.Args = replaceCfg(c.Args)
		}
	})
}
func (s *p2Suite) mode(t *testing.T, v2, enhance bool) {
	t.Helper()
	s.step(t, fmt.Sprintf("configure pod_v2=%t vpc_wcp_enhance=%t", v2, enhance))
	require.NoError(t, s.configure(s.ctx, v2, enhance, false, false))
	require.NoError(t, s.force(s.ctx, false))
	require.NoError(t, s.start(s.ctx))
	require.NoError(t, s.normal(s.ctx))
}
func (s *p2Suite) force(ctx context.Context, on bool) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		n, e := s.dynamic.Resource(p2NCP).Get(ctx, restoreutil.NSXRestoreStatus, metav1.GetOptions{})
		if apierrors.IsNotFound(e) {
			if !on {
				return nil
			}
			n = &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "nsx.vmware.com/v1", "kind": "NCPConfig", "metadata": map[string]interface{}{"name": restoreutil.NSXRestoreStatus}}}
			n.SetLabels(map[string]string{p2Label: s.journal.Run})
			n.SetAnnotations(map[string]string{restoreutil.AnnotationForceRestore: "true", restoreutil.AnnotationRestoreEndTime: "-1"})
			_, e = s.dynamic.Resource(p2NCP).Create(ctx, n, metav1.CreateOptions{})
			return e
		}
		if e != nil {
			return e
		}
		a := n.GetAnnotations()
		if a == nil {
			a = map[string]string{}
		}
		if on {
			a[restoreutil.AnnotationForceRestore] = "true"
		} else {
			delete(a, restoreutil.AnnotationForceRestore)
		}
		n.SetAnnotations(a)
		_, e = s.dynamic.Resource(p2NCP).Update(ctx, n, metav1.UpdateOptions{})
		return e
	})
}
func (s *p2Suite) restoreStamp(ctx context.Context) (string, error) {
	n, e := s.dynamic.Resource(p2NCP).Get(ctx, restoreutil.NSXRestoreStatus, metav1.GetOptions{})
	if apierrors.IsNotFound(e) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	return n.GetAnnotations()[restoreutil.AnnotationRestoreEndTime], nil
}

func (s *p2Suite) ports(scope, value string) ([]model.VpcSubnetPort, error) {
	query := fmt.Sprintf("resource_type:VpcSubnetPort AND tags.scope:%s AND tags.tag:%s AND marked_for_delete:false", strings.ReplaceAll(scope, "/", "\\/"), strings.ReplaceAll(value, ":", "\\:"))
	var cursor *string
	var out []model.VpcSubnetPort
	for {
		res, e := testData.nsxClient.QueryClient.List(query, cursor, nil, ptr.To[int64](500), nil, nil)
		if e != nil {
			return nil, e
		}
		for _, raw := range res.Results {
			v, e := common.NewConverter().ConvertToGolang(raw, model.VpcSubnetPortBindingType())
			if e != nil {
				return nil, errors.Join(e...)
			}
			p, ok := v.(model.VpcSubnetPort)
			if !ok {
				return nil, fmt.Errorf("unexpected NSX result %T", v)
			}
			out = append(out, p)
		}
		if res.Cursor == nil || *res.Cursor == "" {
			break
		}
		if cursor != nil && *cursor == *res.Cursor {
			return nil, fmt.Errorf("NSX search cursor did not advance")
		}
		cursor = res.Cursor
	}
	return out, nil
}
func (s *p2Suite) deletePort(ctx context.Context, p model.VpcSubnetPort) error {
	if p.Path == nil {
		return fmt.Errorf("port missing path")
	}
	parts, err := p2.PortPath(*p.Path)
	if err != nil {
		return err
	}
	// Both the namespace and a recorded Pod UID must match, even during recovery.
	owned := false
	nsOK := false
	for _, tag := range p.Tags {
		if ptr.Deref(tag.Scope, "") == common.TagScopeNamespaceUID && ptr.Deref(tag.Tag, "") == string(s.namespaceUID()) {
			nsOK = true
		}
		if ptr.Deref(tag.Scope, "") == common.TagScopePodUID {
			for _, uid := range s.journal.PodUIDs {
				if ptr.Deref(tag.Tag, "") == uid {
					owned = true
				}
			}
		}
	}
	if !owned || !nsOK {
		return fmt.Errorf("refusing to delete port %s: ownership mismatch", *p.Path)
	}
	err = testData.nsxClient.PortClient.Delete(parts[0], parts[1], parts[2], parts[3], parts[4])
	if err != nil && !p2NSXNotFound(err) {
		return err
	}
	return s.poll(ctx, "NSX port absent "+*p.Path, func(c context.Context) (bool, string, error) {
		_, e := testData.nsxClient.PortClient.Get(parts[0], parts[1], parts[2], parts[3], parts[4])
		if e != nil && !p2NSXNotFound(e) {
			return false, "GET after deletion", e
		}
		if p2NSXNotFound(e) { // Confirm direct 404 and eventual search-index removal.
			ps, qe := s.ports(common.TagScopeNamespaceUID, string(s.namespaceUID()))
			if qe != nil {
				return false, "search after delete", qe
			}
			for _, x := range ps {
				if ptr.Deref(x.Path, "") == *p.Path {
					return false, "port still indexed", nil
				}
			}
			return true, "port no longer present", nil
		}
		return false, "port still exists", nil
	})
}
func (s *p2Suite) rememberPod(p *corev1.Pod) error {
	for _, u := range s.journal.PodUIDs {
		if u == string(p.UID) {
			return nil
		}
	}
	s.journal.PodUIDs = append(s.journal.PodUIDs, string(p.UID))
	return s.save()
}
func (s *p2Suite) labels() map[string]string { return map[string]string{p2Label: s.journal.Run} }
func (s *p2Suite) options() metav1.ListOptions {
	return metav1.ListOptions{LabelSelector: p2Label + "=" + s.journal.Run}
}

func (s *p2Suite) diagnostics(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	t.Logf("DIAGNOSTICS run=%s stage=%s elapsed=%s", s.journal.Run, s.stage, time.Since(s.started).Round(time.Second))
	dir := *p2Artifacts + "/" + s.journal.Run + "/" + strings.ReplaceAll(t.Name(), "/", "_")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Logf("create diagnostics: %v", err)
		return
	}
	write := func(name string, v interface{}) {
		b, e := json.MarshalIndent(v, "", "  ")
		if e == nil {
			e = os.WriteFile(dir+"/"+name, b, 0600)
		}
		if e != nil {
			t.Logf("diagnostics %s: %v", name, e)
		}
	}
	pods, e := testData.clientset.CoreV1().Pods(s.namespace()).List(ctx, s.options())
	if e == nil {
		write("pods.json", pods)
	}
	crs, e := testData.crdClientset.CrdV1alpha1().SubnetPorts(s.namespace()).List(ctx, metav1.ListOptions{})
	if e == nil {
		write("subnetports.json", crs)
		for _, c := range crs.Items {
			t.Logf("CR %s uid=%s attachment=%s conditions=%+v", c.Name, c.UID, c.Status.Attachment.ID, c.Status.Conditions)
		}
	}
	ps, e := s.ports(common.TagScopeNamespaceUID, string(s.namespaceUID()))
	if e == nil {
		write("nsx-ports.json", ps)
		t.Logf("NSX ports in test namespace: %d", len(ps))
	} else {
		t.Logf("NSX query: %v", e)
	}
	events, e := testData.clientset.CoreV1().Events(s.namespace()).List(ctx, metav1.ListOptions{})
	if e == nil {
		write("events.json", events)
	}
	opEvents, e := testData.clientset.CoreV1().Events(*p2OperatorNS).List(ctx, metav1.ListOptions{})
	if e == nil {
		write("operator-events.json", opEvents)
		for _, ev := range opEvents.Items {
			if ev.Type == corev1.EventTypeWarning {
				t.Logf("Operator NS Warning Event: %s %s: %s", ev.InvolvedObject.Kind, ev.InvolvedObject.Name, ev.Message)
			}
		}
	}
	for _, previous := range []bool{false, true} {
		logs, e := s.operatorLogs(ctx, previous)
		if e == nil {
			e = os.WriteFile(fmt.Sprintf("%s/operator-previous-%t.log", dir, previous), []byte(logs), 0600)
		}
		if e != nil {
			// In case the target container (e.g. nsx-operator) is not ready, also attempt to dump logs from all containers
			t.Logf("operator log capture: %v", e)
			if pods, listErr := s.operatorPods(ctx); listErr == nil {
				for _, p := range pods {
					for _, c := range p.Spec.Containers {
						cLogs, cErr := testData.clientset.CoreV1().Pods(p.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{Container: c.Name, Previous: previous, LimitBytes: ptr.To[int64](64 * 1024)}).DoRaw(ctx)
						if cErr == nil && len(cLogs) > 0 {
							_ = os.WriteFile(fmt.Sprintf("%s/%s-%s-prev-%t.log", dir, p.Name, c.Name, previous), cLogs, 0600)
						}
					}
				}
			}
		}
	}
	t.Logf("Diagnostics: %s", dir)
}

// Keep cleanup on a fresh context: cancellation and failed assertions must not
// cancel rollback. A journal survives API outages/SIGKILL for a later retry.
func (s *p2Suite) cleanup() error {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if s.secret == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), *p2CleanupTimeout*2/3)
	defer cancel()
	var errs []error
	add := func(e error) {
		if e != nil {
			errs = append(errs, e)
		}
	}
	// Stop restore loops first. Do not delete the journal/config while mounted.
	if e := s.stop(ctx); e != nil {
		return fmt.Errorf("stop operator for cleanup: %w; journal retained", e)
	}
	add(s.force(ctx, false))
	// Run cleanup with normal v2 reconciliation and no StatefulSet retention.
	// Temporarily use the recovery context for journal writes as well.
	oldCtx := s.ctx
	s.ctx = ctx
	defer func() { s.ctx = oldCtx }()
	if cfgErr := s.configure(ctx, true, false, false, false); cfgErr == nil {
		if startErr := s.start(ctx); startErr == nil {
			add(s.normal(ctx))
		} else {
			add(startErr)
		}
	} else {
		add(cfgErr)
	}
	for _, namespace := range []string{s.journal.Namespace, s.journal.DHCPNamespace} {
		if namespace == "" {
			continue
		}
		s.activeNamespace = namespace
		add(s.cleanWorkloads(ctx))
	}
	s.activeNamespace = ""
	if e := s.checkNamespace(ctx); e != nil {
		add(e)
	} else {
		add(s.restoreDefault(ctx))
		add(s.cleanSubnetFixtures(ctx))
	}
	// Reserve an independent final third of the cleanup budget for restoring
	// the operator even when resource cleanup timed out.
	cancel()
	restoreCtx, restoreCancel := context.WithTimeout(context.Background(), *p2CleanupTimeout/3)
	defer restoreCancel()
	ctx = restoreCtx
	s.ctx = ctx
	// Stop before restoring original config and NCP state.
	if e := s.stop(ctx); e != nil {
		add(e)
		return errors.Join(errs...)
	}
	add(s.restoreNCP(ctx))
	add(s.updateDeployment(ctx, func(d *appsv1.Deployment) {
		d.Spec.Template = *s.journal.Deployment.Spec.Template.DeepCopy()
		d.Spec.Replicas = ptr.To(*s.journal.Deployment.Spec.Replicas)
	}))
	add(s.poll(ctx, "original operator healthy", func(c context.Context) (bool, string, error) {
		d, e := testData.clientset.AppsV1().Deployments(*p2OperatorNS).Get(c, *p2Deployment, metav1.GetOptions{})
		if e != nil {
			return false, "get Deployment", e
		}
		ok := reflect.DeepEqual(d.Spec.Template, s.journal.Deployment.Spec.Template) && d.Spec.Replicas != nil && *d.Spec.Replicas == *s.journal.Deployment.Spec.Replicas && d.Status.ObservedGeneration >= d.Generation && d.Status.AvailableReplicas == *d.Spec.Replicas
		return ok, fmt.Sprintf("available=%d desired=%d", d.Status.AvailableReplicas, ptr.Deref(d.Spec.Replicas, 0)), nil
	}))
	add(s.cleanReplicaSets(ctx))
	add(s.verifyOriginalState(ctx))
	if len(errs) > 0 {
		return fmt.Errorf("rollback incomplete; journal %s retained: %w", s.journalName(), errors.Join(errs...))
	}
	// Deleting the journal also removes the only temporary configuration Secret.
	e := testData.clientset.CoreV1().Secrets(*p2OperatorNS).Delete(ctx, s.journalName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &s.secret.UID}})
	if e != nil && !apierrors.IsNotFound(e) {
		return e
	}
	s.secret = nil
	cleanupAutoCreatedNamespace()
	fmt.Println("CLEANUP PASS: workloads/ports removed; original template, replicas, default SubnetSet label and restore annotations restored.")
	return nil
}
func (s *p2Suite) restoreNCP(ctx context.Context) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		n, e := s.dynamic.Resource(p2NCP).Get(ctx, restoreutil.NSXRestoreStatus, metav1.GetOptions{})
		if apierrors.IsNotFound(e) && !s.journal.NCPExists {
			return nil
		}
		if e != nil {
			return e
		}
		if !s.journal.NCPExists {
			if n.GetLabels()[p2Label] != s.journal.Run {
				return fmt.Errorf("NCPConfig is not owned by this run; refusing deletion")
			}
			return s.dynamic.Resource(p2NCP).Delete(ctx, n.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: ptr.To(n.GetUID())}})
		}
		if n.GetUID() != s.journal.NCPUID {
			return fmt.Errorf("NCPConfig UID changed")
		}
		a := n.GetAnnotations()
		if a == nil {
			a = map[string]string{}
		}
		for _, key := range []string{restoreutil.AnnotationForceRestore, restoreutil.AnnotationRestoreEndTime} {
			if v, ok := s.journal.NCPAnnotations[key]; ok {
				a[key] = v
			} else {
				delete(a, key)
			}
		}
		n.SetAnnotations(a)
		_, e = s.dynamic.Resource(p2NCP).Update(ctx, n, metav1.UpdateOptions{})
		return e
	})
}

func p2NSXNotFound(err error) bool {
	var value nsxerrors.NotFound
	var pointer *nsxerrors.NotFound
	return errors.As(err, &value) || errors.As(err, &pointer)
}

func (s *p2Suite) initOperatorCRD() error {
	cfg := rest.CopyConfig(testData.kubeConfig)
	sa := s.journal.Deployment.Spec.Template.Spec.ServiceAccountName
	if sa == "" {
		sa = "default"
	}
	cfg.Impersonate = rest.ImpersonationConfig{UserName: "system:serviceaccount:" + s.journal.Deployment.Namespace + ":" + sa}
	var err error
	s.operatorCRD, err = versioned.NewForConfig(cfg)
	return err
}
func (s *p2Suite) namespace() string {
	if s.activeNamespace != "" {
		return s.activeNamespace
	}
	return s.journal.Namespace
}
func (s *p2Suite) namespaceUID() types.UID {
	if s.activeNamespace != "" && s.activeNamespace == s.journal.DHCPNamespace {
		return s.journal.DHCPNamespaceUID
	}
	return s.journal.NamespaceUID
}

func podV2CaseSelected(name string) bool {
	filter := flag.Lookup("test.run").Value.String()
	parts := strings.SplitN(filter, "/", 3)
	if len(parts) < 2 {
		return true
	}
	expression, err := regexp.Compile(parts[1])
	return err != nil || expression.MatchString(name)
}

func (s *p2Suite) checkNamespace(ctx context.Context) error {
	n, e := testData.clientset.CoreV1().Namespaces().Get(ctx, s.namespace(), metav1.GetOptions{})
	if apierrors.IsNotFound(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if n.UID != s.namespaceUID() {
		return fmt.Errorf("test namespace UID changed; refusing mutation")
	}
	return nil
}

// Rolling the temporary config mount creates a ReplicaSet. Remove only the
// zero-replica ReplicaSets carrying this run's annotation and Deployment UID.
func (s *p2Suite) cleanReplicaSets(ctx context.Context) error {
	return s.poll(ctx, "temporary operator ReplicaSets removed", func(c context.Context) (bool, string, error) {
		sets, e := testData.clientset.AppsV1().ReplicaSets(*p2OperatorNS).List(c, metav1.ListOptions{})
		if e != nil {
			return false, "list ReplicaSets", e
		}
		remaining := 0
		for _, set := range sets.Items {
			if set.Spec.Template.Annotations[p2Label] != s.journal.Run {
				continue
			}
			owner := metav1.GetControllerOf(&set)
			if owner == nil || owner.UID != s.journal.Deployment.UID {
				return false, "test-marked ReplicaSet has unexpected owner", fmt.Errorf("ReplicaSet %s owner mismatch", set.Name)
			}
			remaining++
			if ptr.Deref(set.Spec.Replicas, 0) != 0 || set.Status.Replicas != 0 {
				continue
			}
			e = testData.clientset.AppsV1().ReplicaSets(set.Namespace).Delete(c, set.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &set.UID}, PropagationPolicy: ptr.To(metav1.DeletePropagationForeground)})
			if e != nil && !apierrors.IsNotFound(e) {
				return false, set.Name, e
			}
		}
		return remaining == 0, fmt.Sprintf("temporary ReplicaSets=%d", remaining), nil
	})
}

func (s *p2Suite) verifyOriginalState(ctx context.Context) error {
	if err := s.checkNamespace(ctx); err != nil {
		return err
	}
	set, e := testData.crdClientset.CrdV1alpha1().SubnetSets(s.journal.Namespace).Get(ctx, s.journal.DefaultSet.Name, metav1.GetOptions{})
	if e != nil && !apierrors.IsNotFound(e) {
		return e
	}
	if e == nil {
		if set.UID != s.journal.DefaultSet.UID || !reflect.DeepEqual(set.Spec, s.journal.DefaultSet.Spec) || set.Labels[common.LabelDefaultNetwork] != s.journal.DefaultSet.Labels[common.LabelDefaultNetwork] || set.Labels[common.LabelDefaultSubnetSet] != s.journal.DefaultSet.Labels[common.LabelDefaultSubnetSet] {
			return fmt.Errorf("original default SubnetSet identity/spec/label was not restored")
		}
	}
	n, e := s.dynamic.Resource(p2NCP).Get(ctx, restoreutil.NSXRestoreStatus, metav1.GetOptions{})
	if !s.journal.NCPExists {
		if apierrors.IsNotFound(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if n.GetLabels()[p2Label] == s.journal.Run {
			return fmt.Errorf("test-created NCPConfig remains")
		}
		return nil
	}
	if e != nil {
		return e
	}
	if n.GetUID() != s.journal.NCPUID {
		return fmt.Errorf("original NCPConfig UID changed")
	}
	for _, key := range []string{restoreutil.AnnotationForceRestore, restoreutil.AnnotationRestoreEndTime} {
		got, has := n.GetAnnotations()[key]
		want, had := s.journal.NCPAnnotations[key]
		if got != want || has != had {
			return fmt.Errorf("restore annotation %s was not restored", key)
		}
	}
	return nil
}
