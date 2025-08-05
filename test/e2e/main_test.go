// e2e test for nsx-operator, in nsx_operator directory, run command below:
// e2e=true go test -v github.com/vmware-tanzu/nsx-operator/test/e2e -remote.sshconfig /root/.ssh/config -remote.kubeconfig /root/.kube/config -operator-cfg-path /etc/nsx-ujo/ncp.ini -test.timeout 90m -coverprofile cover-e2e.out -vc-user administrator@vsphere.local -vc-password UWOYj4Ltlsd.*h8T -debug=true
// Note: set a reasonable timeout when running e2e tests, otherwise the test will be terminated by the framework.
package e2e

import (
	"flag"
	"os"
	"testing"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/test/e2e/framework"
)

var testOptions framework.TestOptions
var log = &logger.Log

// testMain is meant to be called by TestMain and enables the use of defer statements.
func testMain(m *testing.M) int {
	flag.StringVar(&testOptions.ProviderName, "provider", "remote", "K8s test cluster provider")
	flag.StringVar(&testOptions.ProviderConfigPath, "provider-cfg-path", "", "Optional config file for provider")
	flag.StringVar(&testOptions.LogsExportDir, "logs-export-dir", "", "Export directory for test logs")
	flag.StringVar(&testOptions.OperatorConfigPath, "operator-cfg-path", "/etc/nsx-ujo/ncp.ini", "config file for operator")
	flag.BoolVar(&testOptions.LogsExportOnSuccess, "logs-export-on-success", false, "Export logs even when a test is successful")
	flag.StringVar(&testOptions.VCUser, "vc-user", "", "The username used to request vCenter API session")
	flag.StringVar(&testOptions.VCPassword, "vc-password", "", "The password used by the user when requesting vCenter API session")
	flag.BoolVar(&testOptions.DebugLog, "debug", false, "")
	flag.Parse()

	if testOptions.DebugLog {
		logf.SetLogger(logger.ZapLogger(true, 2))
	} else {
		logf.SetLogger(logger.ZapLogger(false, 0))
	}

	if err := framework.InitProvider(&testOptions); err != nil {
		log.Error(err, "Error when initializing provider")
		panic(err)
	}

	log.Info("Creating clientSets")

	if err := framework.NewTestData(testOptions.OperatorConfigPath, testOptions.VCUser, testOptions.VCPassword); err != nil {
		log.Error(err, "Error when creating client")
		return 1
	}

	log.Info("Collecting information about K8s cluster")
	if err := framework.CollectClusterInfo(); err != nil {
		log.Error(err, "Error when collecting information about K8s cluster")
		panic(err)
	}
	if framework.ClusterInfoData.PodV4NetworkCIDR != "" {
		log.Info("Pod IPv4: ", "network", framework.ClusterInfoData.PodV4NetworkCIDR)
	}
	if framework.ClusterInfoData.PodV6NetworkCIDR != "" {
		log.Info("Pod IPv6: ", "network", framework.ClusterInfoData.PodV6NetworkCIDR)
	}

	// Only run inventory tests
	log.Info("Running only inventory tests")

	ret := m.Run()
	return ret
}

func TestMain(m *testing.M) {
	if os.Getenv("e2e") == "true" {
		os.Exit(testMain(m))
	}
}
