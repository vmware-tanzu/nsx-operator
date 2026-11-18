package e2e

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

const namespaceCleanupAnnotation = "e2e.nsx.vmware.com/cleanup-token"

var cleanupOnce sync.Once
var cleanupError error

var namespaceRegistry = struct {
	sync.Mutex
	vc             map[string]bool
	k8s            map[string]types.UID
	k8sTokens      map[string]string
	cleanupStarted bool
	creates        sync.WaitGroup
	createLocks    map[string]*sync.Mutex
	createCancels  map[string]context.CancelFunc
}{vc: make(map[string]bool), k8s: make(map[string]types.UID), k8sTokens: make(map[string]string)}

func beginNamespaceCreate(namespace string, isVCNamespace bool) (context.Context, context.CancelFunc, bool, bool) {
	namespaceRegistry.Lock()
	if namespaceRegistry.cleanupStarted {
		namespaceRegistry.Unlock()
		return nil, nil, false, false
	}
	key := "k8s/" + namespace
	if isVCNamespace {
		key = "vc/" + namespace
	}
	if namespaceRegistry.createLocks == nil {
		namespaceRegistry.createLocks = make(map[string]*sync.Mutex)
	}
	lock := namespaceRegistry.createLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		namespaceRegistry.createLocks[key] = lock
	}
	namespaceRegistry.Unlock()
	lock.Lock()
	namespaceRegistry.Lock()
	if namespaceRegistry.cleanupStarted {
		namespaceRegistry.Unlock()
		lock.Unlock()
		return nil, nil, false, false
	}
	var wasTracked bool
	if isVCNamespace {
		_, wasTracked = namespaceRegistry.vc[namespace]
	} else {
		_, wasTracked = namespaceRegistry.k8s[namespace]
	}
	createCtx, createCancel := context.WithCancel(context.Background())
	namespaceRegistry.creates.Add(1)
	if namespaceRegistry.createCancels == nil {
		namespaceRegistry.createCancels = make(map[string]context.CancelFunc)
	}
	namespaceRegistry.createCancels[key] = createCancel
	namespaceRegistry.Unlock()
	return createCtx, createCancel, true, wasTracked
}

func trackNamespaceForCleanup(namespace string, isVCNamespace bool) bool {
	namespaceRegistry.Lock()
	defer namespaceRegistry.Unlock()
	if namespaceRegistry.cleanupStarted {
		return false
	}
	if isVCNamespace {
		if _, exists := namespaceRegistry.vc[namespace]; !exists {
			namespaceRegistry.vc[namespace] = false
		}
	} else {
		if _, exists := namespaceRegistry.k8s[namespace]; !exists {
			namespaceRegistry.k8s[namespace] = ""
		}
	}
	return true
}

func endNamespaceCreate(namespace string, isVCNamespace bool, createCancel context.CancelFunc) {
	key := "k8s/" + namespace
	if isVCNamespace {
		key = "vc/" + namespace
	}
	createCancel()
	namespaceRegistry.Lock()
	delete(namespaceRegistry.createCancels, key)
	lock := namespaceRegistry.createLocks[key]
	namespaceRegistry.Unlock()
	namespaceRegistry.creates.Done()
	lock.Unlock()
}

func untrackVCNamespace(namespace string) {
	namespaceRegistry.Lock()
	defer namespaceRegistry.Unlock()
	delete(namespaceRegistry.vc, namespace)
}

func untrackK8sNamespace(namespace string) {
	namespaceRegistry.Lock()
	defer namespaceRegistry.Unlock()
	delete(namespaceRegistry.k8s, namespace)
	delete(namespaceRegistry.k8sTokens, namespace)
}

// Pre-defined namespace names for e2e tests. TestMain selects the namespaces
// for suites selected by the Go test run/skip patterns.
var (
	// Security Policy test namespaces - each test gets its own isolated VC namespace(s) for parallel execution
	NsSecurityPolicyBasic           = "e2e-sp-basic-" + getRandomString()
	NsSecurityPolicyAddDeleteRule   = "e2e-sp-rule-" + getRandomString()
	NsSecurityPolicyMatchExpr       = "e2e-sp-expr-" + getRandomString()
	NsSecurityPolicyVPCFromIngress  = "e2e-sp-vpc-in-" + getRandomString()
	NsSecurityPolicyVPCToEgress     = "e2e-sp-vpc-eg-" + getRandomString()
	NsSecurityPolicyNPWithoutPodWeb = "e2e-sp-np-wopod-web-" + getRandomString()
	NsSecurityPolicyNPWithPodClient = "e2e-sp-np-wpod-cli-" + getRandomString()
	NsSecurityPolicyNPWithPodWeb    = "e2e-sp-np-wpod-web-" + getRandomString()
	NsSecurityPolicyNativeInv       = "e2e-sp-native-inv-" + getRandomString()
	NsNetworkPolicyMultipleIn       = "e2e-np-multi-in-" + getRandomString()

	// NsIPv6PolicyVC IPv6 policy test namespace (VC-based, isolated from other security policy tests).
	NsIPv6PolicyVC = "e2e-ipv6-policy-vc-" + getRandomString()

	// NsInventorySync Inventory sync test namespaces - need VC namespace for pod creation
	NsInventorySync = "e2e-inventory-" + getRandomString()

	NsIPAddressAllocation = "e2e-ipalloc-" + getRandomString()

	NsDNSRecord = "e2e-dnsrecord-" + getRandomString()

	// NsLoadBalancerLB LoadBalancer test namespaces - need VC namespace for pod creation
	NsLoadBalancerLB  = "e2e-lb-" + getRandomString()
	NsLoadBalancerPod = "e2e-lb-pod-" + getRandomString()

	// NsCreateVM VM test namespace - need VC namespace for VM creation
	NsCreateVM = "e2e-vm-" + getRandomString()

	// NsSubnetPrecreated1 Subnet precreated test namespaces
	NsSubnetPrecreated1      = "e2e-subnet-pre1-" + getRandomString()
	NsSubnetPrecreated2      = "e2e-subnet-pre2-" + getRandomString()
	NsSubnetPrecreatedTarget = "e2e-subnet-pre-target-" + getRandomString()
)

type vcNamespaceTestSuite struct {
	testName    string
	subtestPath []string
	namespaces  []string
}

// vcNamespaceTestSuites maps top-level E2E tests to their shared VC namespace fixtures.
// A suite initializes its full fixture set together so parallel subtests can use it.
var vcNamespaceTestSuites = []vcNamespaceTestSuite{
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"ParallelTests", "testSecurityPolicyBasicTraffic"},
		namespaces:  []string{NsSecurityPolicyBasic},
	},
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"ParallelTests", "testSecurityPolicyAddDeleteRule"},
		namespaces:  []string{NsSecurityPolicyAddDeleteRule},
	},
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"ParallelTests", "testSecurityPolicyMatchExpression"},
		namespaces:  []string{NsSecurityPolicyMatchExpr},
	},
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"ParallelTests", "testSecurityPolicyVPCFromFieldIngress"},
		namespaces:  []string{NsSecurityPolicyVPCFromIngress},
	},
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"ParallelTests", "testSecurityPolicyVPCToFieldEgress"},
		namespaces:  []string{NsSecurityPolicyVPCToEgress},
	},
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"ParallelTests", "testSecurityPolicyNamedPortWithoutPod"},
		namespaces:  []string{NsSecurityPolicyNPWithoutPodWeb},
	},
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"ParallelTests", "testSecurityPolicyNamedPorWithPod"},
		namespaces:  []string{NsSecurityPolicyNPWithPodClient, NsSecurityPolicyNPWithPodWeb},
	},
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"ParallelTests", "testSecurityPolicyNativeInventoryGroup"},
		namespaces:  []string{NsSecurityPolicyNativeInv},
	},
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"ParallelTests", "testNetworkPolicyMultipleIn"},
		namespaces:  []string{NsNetworkPolicyMultipleIn},
	},
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"IPv6SecurityPolicyTests", "testIPv6SecurityPolicy"},
		namespaces:  []string{NsIPv6PolicyVC},
	},
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"IPv6SecurityPolicyTests", "testIPv6NetworkPolicy"},
		namespaces:  []string{NsIPv6PolicyVC},
	},
	{
		testName:    "TestSecurityPolicy",
		subtestPath: []string{"IPv6SecurityPolicyTests", "testDualStackNetworkPolicy"},
		namespaces:  []string{NsIPv6PolicyVC},
	},
	{testName: "TestInventorySync", namespaces: []string{NsInventorySync}},
	{testName: "TestLoadBalancer", namespaces: []string{NsLoadBalancerLB, NsLoadBalancerPod}},
	{testName: "TestCreateVM", namespaces: []string{NsCreateVM}},
	{testName: "TestIPAddressAllocation", namespaces: []string{NsIPAddressAllocation}},
	{testName: "TestDNSRecord", namespaces: []string{NsDNSRecord}},
	{
		testName: "TestSubnetPrecreated",
		namespaces: []string{
			NsSubnetPrecreated1,
			NsSubnetPrecreated2,
			NsSubnetPrecreatedTarget,
		},
	},
}

// VCNamespacesForTestSelection returns namespace fixtures selected by
// -test.run and -test.skip. Patterns are matched by slash-separated test-name
// components, like the Go test runner. SecurityPolicy fixtures are selected per
// subtest because that suite has isolated namespaces; other fixtures are shared
// by all subtests in their top-level suite.
func VCNamespacesForTestSelection(runPattern, skipPattern string) ([]string, error) {
	runPatterns := splitTestPattern(runPattern)
	skipPatterns := splitTestPattern(skipPattern)
	if _, err := compileTestPatternAlternatives(runPatterns); err != nil {
		return nil, fmt.Errorf("invalid test run pattern %q: %w", runPattern, err)
	}
	if skipPattern != "" {
		if _, err := compileTestPatternAlternatives(skipPatterns); err != nil {
			return nil, fmt.Errorf("invalid test skip pattern %q: %w", skipPattern, err)
		}
	}

	namespaces := make([]string, 0)
	seen := make(map[string]struct{})
	for _, suite := range vcNamespaceTestSuites {
		selected, err := testPathMatches(runPatterns, suite.testName, suite.subtestPath, false)
		if err != nil {
			return nil, fmt.Errorf("invalid test run pattern %q: %w", runPattern, err)
		}
		skipped := false
		if skipPattern != "" {
			skipped, err = testPathMatches(skipPatterns, suite.testName, suite.subtestPath, true)
			if err != nil {
				return nil, fmt.Errorf("invalid test skip pattern %q: %w", skipPattern, err)
			}
		}
		if !selected || skipped {
			continue
		}
		for _, namespace := range suite.namespaces {
			if _, ok := seen[namespace]; ok {
				continue
			}
			seen[namespace] = struct{}{}
			namespaces = append(namespaces, namespace)
		}
	}
	return namespaces, nil
}

// splitTestPattern follows testing's slash and alternation boundaries. Slashes
// inside a regexp group, character class, or escape sequence remain in that
// regexp component.
func splitTestPattern(pattern string) [][]string {
	alternatives := make([][]string, 0, strings.Count(pattern, "|")+1)
	parts := make([]string, 0, strings.Count(pattern, "/")+1)
	componentStart := 0
	charClassDepth := 0
	parenDepth := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '[':
			charClassDepth++
		case ']':
			if charClassDepth--; charClassDepth < 0 {
				charClassDepth = 0
			}
		case '(':
			if charClassDepth == 0 {
				parenDepth++
			}
		case ')':
			if charClassDepth == 0 {
				parenDepth--
			}
		case '\\':
			i++
		case '/':
			if charClassDepth == 0 && parenDepth == 0 {
				parts = append(parts, pattern[componentStart:i])
				componentStart = i + 1
			}
		case '|':
			if charClassDepth == 0 && parenDepth == 0 {
				parts = append(parts, pattern[componentStart:i])
				alternatives = append(alternatives, parts)
				parts = make([]string, 0, strings.Count(pattern[i+1:], "/")+1)
				componentStart = i + 1
			}
		}
	}
	parts = append(parts, pattern[componentStart:])
	return append(alternatives, parts)
}

func compileTestPatternAlternatives(alternatives [][]string) ([][]*regexp.Regexp, error) {
	compiled := make([][]*regexp.Regexp, 0, len(alternatives))
	for _, parts := range alternatives {
		matchers, err := compileTestPatternParts(parts)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, matchers)
	}
	return compiled, nil
}

func compileTestPatternParts(parts []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(parts))
	for _, part := range parts {
		matcher, err := regexp.Compile(part)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, matcher)
	}
	return compiled, nil
}

func testPathMatches(patternAlternatives [][]string, testName string, subtestPath []string, isSkipPattern bool) (bool, error) {
	compiledAlternatives, err := compileTestPatternAlternatives(patternAlternatives)
	if err != nil {
		return false, err
	}
	for i, patternParts := range patternAlternatives {
		compiled := compiledAlternatives[i]
		if !compiled[0].MatchString(testName) {
			continue
		}

		// A suite-level fixture is needed if any subtest in that suite is selected.
		// A subtest-level skip cannot prove that all consumers of the shared fixture
		// are skipped, so keep it selected.
		if len(subtestPath) == 0 {
			if !isSkipPattern || len(patternParts) == 1 {
				return true, nil
			}
			continue
		}

		if isSkipPattern && len(patternParts) == 1 {
			return true, nil
		}
		if len(patternParts) > len(subtestPath)+1 {
			if isSkipPattern {
				continue
			}
			patternParts = patternParts[:len(subtestPath)+1]
		}
		matched := true
		for j := 1; j < len(patternParts); j++ {
			if !compiled[j].MatchString(subtestPath[j-1]) {
				matched = false
				break
			}
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// InitVCNamespaces creates the selected suite namespaces concurrently because
// each VC namespace can take time to become ready.
func InitVCNamespaces(namespaces ...string) error {
	if len(namespaces) == 0 {
		log.Info("No VC namespace fixtures selected for this test run")
		return nil
	}
	if testData == nil {
		log.Info("Skipping VC namespace creation - testData is nil")
		return nil
	}

	// Create VC namespaces (slower, required for pod/VM creation)
	if !testData.useWCPSetup() {
		log.Info("Skipping VC namespace creation - not using WCP setup")
		return nil
	}

	log.Info("Creating VC namespaces selected by test run", "count", len(namespaces))
	// Create namespaces concurrently
	g := new(errgroup.Group)
	for _, ns := range namespaces {
		ns := ns
		g.Go(func() error {
			if err := testData.createVCNamespace(ns); err != nil {
				log.Error(err, "Failed to create VC namespace", "namespace", ns)
				return err
			}
			log.Info("Created VC namespace", "namespace", ns)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to initialize selected VC namespaces: %w", err)
	}

	log.Info("Selected e2e VC namespaces created successfully")
	return nil
}

// CleanupVCNamespaces deletes the specified VC namespaces
// Use this in individual tests via deferring to clean up as soon as the test completes
// This improves concurrency by releasing resources early
func CleanupVCNamespaces(namespaces ...string) error {
	if testData == nil || !testData.useWCPSetup() {
		return nil
	}

	namespaceRegistry.Lock()
	trackedNamespaces := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		if confirmed := namespaceRegistry.vc[namespace]; confirmed {
			trackedNamespaces = append(trackedNamespaces, namespace)
		}
	}
	namespaceRegistry.Unlock()
	if len(trackedNamespaces) == 0 {
		return nil
	}

	g := new(errgroup.Group)
	for _, ns := range trackedNamespaces {
		ns := ns
		g.Go(func() error {
			if err := testData.deleteVCNamespace(ns); err != nil {
				log.Error(err, "Failed to delete VC namespace", "namespace", ns)
				return fmt.Errorf("failed to delete VC namespace %q: %w", ns, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// CleanupAllNamespaces deletes all namespaces at the end of tests
// This is a safety net cleanup - most namespaces should be deleted by individual tests
func CleanupAllNamespaces() error {
	cleanupOnce.Do(func() {
		namespaceRegistry.Lock()
		namespaceRegistry.cleanupStarted = true
		createCancels := make([]context.CancelFunc, 0, len(namespaceRegistry.createCancels))
		for _, cancel := range namespaceRegistry.createCancels {
			createCancels = append(createCancels, cancel)
		}
		namespaceRegistry.Unlock()
		for _, cancel := range createCancels {
			cancel()
		}
		// No create can add to the wait group after cleanupStarted is set under
		// the registry lock. Cancel setup/readiness waits, then wait for any
		// bounded create requests already admitted before taking the final snapshot.
		namespaceRegistry.creates.Wait()

		namespaceRegistry.Lock()
		vcNamespaces := make(map[string]bool, len(namespaceRegistry.vc))
		for namespace, confirmed := range namespaceRegistry.vc {
			vcNamespaces[namespace] = confirmed
		}
		type k8sCleanupEntry struct {
			uid   types.UID
			token string
		}
		k8sNamespaces := make(map[string]k8sCleanupEntry, len(namespaceRegistry.k8s))
		for namespace, uid := range namespaceRegistry.k8s {
			if _, isVCNamespace := namespaceRegistry.vc[namespace]; !isVCNamespace {
				k8sNamespaces[namespace] = k8sCleanupEntry{uid: uid, token: namespaceRegistry.k8sTokens[namespace]}
			}
		}
		namespaceRegistry.Unlock()

		log.Info("Requesting safety net namespace cleanup", "vcCount", len(vcNamespaces), "k8sCount", len(k8sNamespaces))
		if testData == nil {
			return
		}
		g := new(errgroup.Group)
		var errorMu sync.Mutex
		var cleanupErrors []error
		runCleanup := func(fn func() error) {
			g.Go(func() error {
				if err := fn(); err != nil {
					log.Error(err, "Namespace cleanup request failed")
					errorMu.Lock()
					cleanupErrors = append(cleanupErrors, err)
					errorMu.Unlock()
				}
				return nil
			})
		}
		for namespace, confirmed := range vcNamespaces {
			namespace, confirmed := namespace, confirmed
			runCleanup(func() error {
				if testData.vcClient == nil {
					return fmt.Errorf("cannot delete VC namespace %q without a VC client", namespace)
				}
				if !confirmed {
					queryCtx, queryCancel := context.WithTimeout(context.Background(), 30*time.Second)
					err := wait.PollUntilContextCancel(queryCtx, time.Second, true, func(ctx context.Context) (bool, error) {
						_, status, err := testData.vcClient.getNamespaceInfoByNameContext(ctx, namespace)
						if status == http.StatusNotFound {
							return false, nil
						}
						return err == nil, err
					})
					queryCancel()
					if err != nil {
						return fmt.Errorf("cannot resolve uncertain creation of VC namespace %q: %w", namespace, err)
					}
				}
				deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer deleteCancel()
				if err := testData.vcClient.deleteNamespaceContext(deleteCtx, namespace); err != nil {
					log.Error(err, "Failed to request VC namespace deletion", "namespace", namespace)
					return fmt.Errorf("delete VC namespace %q: %w", namespace, err)
				}
				return nil
			})
		}
		for namespace, entry := range k8sNamespaces {
			namespace, entry := namespace, entry
			runCleanup(func() error {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				uid := entry.uid
				if uid == "" {
					observed, err := testData.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
					if err != nil {
						if errors.IsNotFound(err) {
							return nil
						}
						return fmt.Errorf("cannot resolve uncertain creation of namespace %q: %w", namespace, err)
					}
					if entry.token == "" || observed.Annotations[namespaceCleanupAnnotation] != entry.token {
						return fmt.Errorf("cannot establish ownership of namespace %q after uncertain creation", namespace)
					}
					uid = observed.UID
				}
				cancel()
				deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer deleteCancel()
				gracePeriodSeconds := int64(0)
				propagationPolicy := metav1.DeletePropagationForeground
				err := testData.clientset.CoreV1().Namespaces().Delete(deleteCtx, namespace, metav1.DeleteOptions{
					GracePeriodSeconds: &gracePeriodSeconds,
					Preconditions:      &metav1.Preconditions{UID: &uid},
					PropagationPolicy:  &propagationPolicy,
				})
				if err != nil && !errors.IsNotFound(err) {
					log.Error(err, "Failed to request Kubernetes namespace deletion", "namespace", namespace)
					return fmt.Errorf("delete Kubernetes namespace %q: %w", namespace, err)
				}
				return nil
			})
		}
		_ = g.Wait() // Wait for delete requests to return, not for namespace removal.
		cleanupError = stderrors.Join(cleanupErrors...)
		if cleanupError != nil {
			log.Error(cleanupError, "Some namespace cleanup requests failed")
		} else {
			log.Info("Namespace deletion requests submitted")
		}
	})
	return cleanupError
}
