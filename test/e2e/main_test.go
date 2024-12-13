// e2e test for nsx-operator, in nsx_operator directory, run command below:
// e2e=true go test -v github.com/vmware-tanzu/nsx-operator/test/e2e -remote.sshconfig /root/.ssh/config -remote.kubeconfig /root/. kube/config -operator-cfg-path /etc/nsx-ujo/ncp.ini -test.timeout 15m
// Note: set a reasonable timeout when running e2e tests, otherwise the test will be terminated by the framework.
package e2e

import (
	"flag"
	"os"
	"testing"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

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
	flag.Parse()

	if testOptions.debugLog {
		logf.SetLogger(logger.ZapLogger(true, 2))
	} else {
		logf.SetLogger(logger.ZapLogger(false, 0))

	}

	if err := initProvider(); err != nil {
		log.Error(err, "Error when initializing provider")
		panic(err)
	}

	log.Info("Creating clientSets")

	if err := NewTestData(testOptions.operatorConfigPath); err != nil {
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

	ret := m.Run()
	return ret
}

func TestMain(m *testing.M) {
	if os.Getenv("e2e") == "true" {
		os.Exit(testMain(m))
	}
}
