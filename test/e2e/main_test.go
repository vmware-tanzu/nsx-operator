// e2e test for nsx-operator, in nsx_operator directory, run the command below:
// e2e=true go test -v github.com/vmware-tanzu/nsx-operator/test/e2e -remote.sshconfig /root/.ssh/config -remote.kubeconfig /root/.kube/config
// -operator-cfg-path /etc/nsx-ujo/ncp.ini -test.timeout 15m
// Note: set a reasonable timeout when running e2e tests, otherwise the test will be terminated by the framework.
package e2e

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

// TestResult holds information about a single test result
type TestResult struct {
	Name      string
	Passed    bool
	Skipped   bool
	Duration  time.Duration
	StartTime time.Time
}

// TestResultTracker tracks test results for summary reporting
type TestResultTracker struct {
	mu        sync.Mutex
	results   map[string]*TestResult
	startTime time.Time
}

var testResultTracker = &TestResultTracker{
	results: make(map[string]*TestResult),
}

// StartTest marks a test as started
func (tr *TestResultTracker) StartTest(name string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.results[name] = &TestResult{
		Name:      name,
		StartTime: time.Now(),
	}
}

// EndTest marks a test as completed with its result
func (tr *TestResultTracker) EndTest(name string, passed, skipped bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if result, exists := tr.results[name]; exists {
		result.Passed = passed
		result.Skipped = skipped
		result.Duration = time.Since(result.StartTime)
	}
}

// TrackTest registers a test and automatically records its result when it completes.
// Call this at the beginning of each top-level test function:
//
//	func TestSomething(t *testing.T) {
//	    TrackTest(t)
//	    // ... test logic ...
//	}
func TrackTest(t *testing.T) {
	testResultTracker.StartTest(t.Name())
	t.Cleanup(func() {
		testResultTracker.EndTest(t.Name(), !t.Failed(), t.Skipped())
	})
}

// RunSubtest runs a subtest and automatically tracks its result.
// Use this instead of t.Run() to include subtests in the test summary tree.
//
//	RunSubtest(t, "subtestName", func(t *testing.T) {
//	    // ... subtest logic ...
//	})
func RunSubtest(t *testing.T, name string, f func(t *testing.T)) bool {
	return t.Run(name, func(t *testing.T) {
		testResultTracker.StartTest(t.Name())
		t.Cleanup(func() {
			testResultTracker.EndTest(t.Name(), !t.Failed(), t.Skipped())
		})
		f(t)
	})
}

// testTreeNode represents a node in the test hierarchy tree
type testTreeNode struct {
	name     string
	result   *TestResult // nil for intermediate nodes
	children map[string]*testTreeNode
}

// buildTestTree builds a tree structure from test names (e.g., "TestA/SubB/SubC")
func buildTestTree(results map[string]*TestResult) *testTreeNode {
	root := &testTreeNode{name: "root", children: make(map[string]*testTreeNode)}

	for name, result := range results {
		parts := strings.Split(name, "/")
		current := root

		for i, part := range parts {
			if current.children[part] == nil {
				current.children[part] = &testTreeNode{
					name:     part,
					children: make(map[string]*testTreeNode),
				}
			}
			current = current.children[part]
			// Set result on the leaf node
			if i == len(parts)-1 {
				current.result = result
			}
		}
	}
	return root
}

// getStatusIcon returns the appropriate icon for test status
func getStatusIcon(result *TestResult) string {
	if result == nil {
		return "📁"
	}
	if result.Skipped {
		return "⏭️ "
	}
	if result.Passed {
		return "✅"
	}
	return "❌"
}

// printTreeNode recursively prints a tree node with proper indentation
func printTreeNode(node *testTreeNode, prefix string, isLast bool, results map[string]*TestResult) {
	// Sort children for consistent output
	var childNames []string
	for name := range node.children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)

	for i, childName := range childNames {
		child := node.children[childName]
		isLastChild := i == len(childNames)-1

		// Determine the connector
		connector := "├── "
		if isLastChild {
			connector = "└── "
		}

		// Get status icon and duration
		icon := getStatusIcon(child.result)
		durationStr := ""
		if child.result != nil {
			durationStr = fmt.Sprintf(" (%s)", child.result.Duration.Round(time.Millisecond))
		}

		fmt.Printf("%s%s%s %s%s\n", prefix, connector, icon, child.name, durationStr)

		// Recursively print children
		newPrefix := prefix
		if isLastChild {
			newPrefix += "    "
		} else {
			newPrefix += "│   "
		}
		printTreeNode(child, newPrefix, isLastChild, results)
	}
}

// PrintSummary prints a summary of all test results in tree format
func (tr *TestResultTracker) PrintSummary() {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if len(tr.results) == 0 {
		return
	}

	var passed, failed, skipped int

	// Calculate actual wall-clock duration from start to end of all tests
	totalDuration := time.Since(tr.startTime)

	for _, result := range tr.results {
		if result.Skipped {
			skipped++
		} else if result.Passed {
			passed++
		} else {
			failed++
		}
	}

	// Print summary header
	fmt.Println()
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("                         E2E TEST SUMMARY")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("Total: %d | Passed: %d | Failed: %d | Skipped: %d\n",
		len(tr.results), passed, failed, skipped)
	fmt.Printf("Total Duration: %s\n", totalDuration.Round(time.Second))
	fmt.Println(strings.Repeat("-", 80))

	// Build and print tree structure
	fmt.Println("\n📊 TEST RESULTS:")
	tree := buildTestTree(tr.results)
	printTreeNode(tree, "", true, tr.results)

	// Print failed tests summary if any
	if failed > 0 {
		fmt.Println("\n" + strings.Repeat("-", 80))
		fmt.Println("❌ FAILED TESTS SUMMARY:")
		var failedNames []string
		for name, result := range tr.results {
			if !result.Passed && !result.Skipped {
				failedNames = append(failedNames, name)
			}
		}
		sort.Strings(failedNames)
		for _, name := range failedNames {
			fmt.Printf("   • %s\n", name)
		}
	}

	fmt.Println(strings.Repeat("=", 80))
	if failed > 0 {
		fmt.Printf("RESULT: ❌ FAILED (%d test(s) failed)\n", failed)
	} else {
		fmt.Println("RESULT: ✅ ALL TESTS PASSED")
	}
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println()
}

// testMain is meant to be called by TestMain and enables the use of defer statements.
func testMain(m *testing.M) int {
	flag.StringVar(&testOptions.providerName, "provider", "remote", "K8s test cluster provider")
	flag.StringVar(&testOptions.providerConfigPath, "provider-cfg-path", "", "Optional config file for provider")
	flag.StringVar(&testOptions.logsExportDir, "logs-export-dir", "", "Export directory for test logs")
	flag.StringVar(&testOptions.operatorConfigPath, "operator-cfg-path", "/etc/nsx-ujo/ncp.ini", "config file for operator")
	flag.BoolVar(&testOptions.logsExportOnSuccess, "logs-export-on-success", false, "Export logs even when a test is successful")
	flag.StringVar(&testOptions.vcUser, "vc-user", "", "The username used to request vCenter API session")
	flag.StringVar(&testOptions.vcPassword, "vc-password", "", "The password used by the user when requesting vCenter API session")
	flag.BoolVar(&testOptions.debugLog, "debug", false, "")
	flag.IntVar(&testOptions.logLevel, "log-level", 0, "")
	flag.BoolVar(&testOptions.logColor, "log-color", false, "Enable ANSI color in log output.")
	flag.Parse()

	log = logger.ZapCustomLogger(testOptions.debugLog, testOptions.logLevel, testOptions.logColor)
	logger.Log = log
	// Set the controller-runtime logger to prevent the warning about log.SetLogger(...) never being called
	logf.SetLogger(log.Logger)

	if err := initProvider(); err != nil {
		log.Error(err, "Error when initializing provider")
		panic(err)
	}

	log.Info("Creating clientSets")

	if err := NewTestData(testOptions.operatorConfigPath, testOptions.vcUser, testOptions.vcPassword); err != nil {
		log.Error(err, "Error when creating client")
		return 1
	}

	log.Info("Collecting information about K8s cluster")
	if err := collectClusterInfo(); err != nil {
		log.Error(err, "Error when collecting information about K8s cluster")
		panic(err)
	}
	if clusterInfo.podV4NetworkCIDR != "" {
		log.Info("Pod IPv4: ", "network", clusterInfo.podV4NetworkCIDR)
	}
	if clusterInfo.podV6NetworkCIDR != "" {
		log.Info("Pod IPv6: ", "network", clusterInfo.podV6NetworkCIDR)
	}

	// Batch create all VC namespaces once
	if err := InitAllNamespaces(); err != nil {
		log.Error(err, "failed to init all e2e namespaces")
	}

	// Handle Ctrl+C to trigger cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Info("Received signal, triggering cleanup", "signal", sig)
		CleanupAllNamespaces()
		os.Exit(1)
	}()

	// Set up a timeout for the entire e2e test suite (30 minutes)
	const e2eTimeout = 30 * time.Minute
	timeoutChan := time.After(e2eTimeout)
	go func() {
		<-timeoutChan
		log.Info("⚠️  WARNING: E2E test suite exceeded timeout, forcing cleanup", "timeout", e2eTimeout)
		testResultTracker.PrintSummary()
		CleanupAllNamespaces()
		os.Exit(1)
	}()

	// Print ASCII art banner
	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                                                                                ║")
	fmt.Println("║    ███╗   ██╗███████╗██╗  ██╗      ██████╗ ██████╗ ███████╗██████╗  █████╗    ║")
	fmt.Println("║    ████╗  ██║██╔════╝╚██╗██╔╝     ██╔═══██╗██╔══██╗██╔════╝██╔══██╗██╔══██╗   ║")
	fmt.Println("║    ██╔██╗ ██║███████╗ ╚███╔╝      ██║   ██║██████╔╝█████╗  ██████╔╝███████║   ║")
	fmt.Println("║    ██║╚██╗██║╚════██║ ██╔██╗      ██║   ██║██╔═══╝ ██╔══╝  ██╔══██╗██╔══██║   ║")
	fmt.Println("║    ██║ ╚████║███████║██╔╝ ██╗     ╚██████╔╝██║     ███████╗██║  ██║██║  ██║   ║")
	fmt.Println("║    ╚═╝  ╚═══╝╚══════╝╚═╝  ╚═╝      ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═╝╚═╝  ╚═╝   ║")
	fmt.Println("║                                                                                ║")
	fmt.Println("║                           🧪 End-to-End Test Suite 🧪                         ║")
	fmt.Println("║                                                                                ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	testResultTracker.startTime = time.Now()
	ret := m.Run()

	// Print test summary at the end
	testResultTracker.PrintSummary()

	CleanupAllNamespaces()
	return ret
}

func TestMain(m *testing.M) {
	if os.Getenv("e2e") == "true" {
		os.Exit(testMain(m))
	}
}
