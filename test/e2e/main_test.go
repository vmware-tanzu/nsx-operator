// e2e test for nsx-operator, in nsx_operator directory, run command below:
// e2e=true go test -v github.com/vmware-tanzu/nsx-operator/test/e2e -remote.sshconfig /root/.ssh/config -remote.kubeconfig /root/. kube/config -operator-cfg-path /etc/nsx-ujo/ncp.ini -test.timeout 15m
// Note: set a reasonable timeout when running e2e tests, otherwise the test will be terminated by the framework.
package e2e

import (
	"flag"
	"log"
	"math/rand"
	"os"
	"testing"
	"time"
)

// setupLogging creates a temporary directory to export the test logs if necessary. If a directory
// was provided by the user, it checks that the directory exists.
func (tOptions *TestOptions) setupLogging() func() {
	if tOptions.logsExportDir == "" {
		name, err := os.MkdirTemp("", "nsx-operator-e2e-test-")
		if err != nil {
			log.Fatalf("Error when creating temporary directory to export logs: %v", err)
		}
		log.Printf("Test logs (if any) will be exported under the '%s' directory", name)
		tOptions.logsExportDir = name
		// we will delete the temporary directory if no logs are exported
		return func() {
			if empty, _ := IsDirEmpty(name); empty {
				log.Printf("Removing empty logs directory '%s'", name)
				_ = os.Remove(name)
			} else {
				log.Printf("Logs exported under '%s', it is your responsibility to delete the directory when you no longer need it", name)
			}
		}
	} else {
		fInfo, err := os.Stat(tOptions.logsExportDir)
		if err != nil {
			log.Fatalf("Cannot stat provided directory '%s': %v", tOptions.logsExportDir, err)
		}
		if !fInfo.Mode().IsDir() {
			log.Fatalf("'%s' is not a valid directory", tOptions.logsExportDir)
		}
	}
	// no-op cleanup function
	return func() {}
}

// testMain is meant to be called by TestMain and enables the use of defer statements.
func testMain(m *testing.M) int {
	flag.StringVar(&testOptions.providerName, "provider", "remote", "K8s test cluster provider")
	flag.StringVar(&testOptions.providerConfigPath, "provider-cfg-path", "", "Optional config file for provider")
	flag.StringVar(&testOptions.logsExportDir, "logs-export-dir", "", "Export directory for test logs")
	flag.StringVar(&testOptions.operatorConfigPath, "operator-cfg-path", "/etc/nsx-ujo/ncp.ini", "config file for operator")
	flag.BoolVar(&testOptions.logsExportOnSuccess, "logs-export-on-success", false, "Export logs even when a test is successful")
	flag.BoolVar(&testOptions.withIPPool, "ippool", false, "Run tests include IPPool tests")
	flag.Parse()

	if err := initProvider(); err != nil {
		log.Fatalf("Error when initializing provider: %v", err)
	}

	cleanupLogging := testOptions.setupLogging()
	defer cleanupLogging()

	log.Println("Creating clientSets")

	if err := NewTestData(testOptions.operatorConfigPath); err != nil {
		log.Fatalf("Error when creating client: %v", err)
		return 1
	}
	log.Println("Collecting information about K8s cluster")
	if err := collectClusterInfo(); err != nil {
		log.Fatalf("Error when collecting information about K8s cluster: %v", err)
	} else {
		if clusterInfo.podV4NetworkCIDR != "" {
			log.Printf("Pod IPv4 network: '%s'", clusterInfo.podV4NetworkCIDR)
		}
		if clusterInfo.podV6NetworkCIDR != "" {
			log.Printf("Pod IPv6 network: '%s'", clusterInfo.podV6NetworkCIDR)
		}
		log.Printf("Num nodes: %d", clusterInfo.numNodes)
	}

	rand.Seed(time.Now().UnixNano())
	ret := m.Run()
	return ret
}

func TestMain(m *testing.M) {
	if os.Getenv("e2e") == "true" {
		os.Exit(testMain(m))
	}
}
