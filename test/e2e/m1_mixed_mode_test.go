package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	v1 "k8s.io/api/core/v1"
	v1networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"

	"github.com/vmware-tanzu/nsx-operator/test/e2e/providers/exec"
)

func waitForSubnetSet(t *testing.T, namespace string, timeout time.Duration) {
	fmt.Printf("Waiting for SubnetSet in Namespace %s...\n", namespace)
	start := time.Now()
	for time.Since(start) < timeout {
		subnetSets, err := testData.crdClientset.CrdV1alpha1().SubnetSets(namespace).List(context.TODO(), metav1.ListOptions{})
		require.NoError(t, err)
		if len(subnetSets.Items) > 0 {
			fmt.Printf("✅ Namespace %s got SubnetSet (took %v)\n", namespace, time.Since(start).Round(time.Second))
			return
		}
		time.Sleep(2 * time.Second)
	}
	fmt.Printf("❌ Namespace %s did not get SubnetSet within %v\n", namespace, timeout)
}

func printPodDetails(t *testing.T, namespace, podName string) {
	pod, err := testData.clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	require.NoError(t, err)
	fmt.Printf("--- Pod %s/%s ---\n", namespace, podName)
	fmt.Printf("  Phase: %s\n", pod.Status.Phase)
	fmt.Printf("  PodIP: %s\n", pod.Status.PodIP)
	if len(pod.Annotations) > 0 {
		fmt.Printf("  Annotations:\n")
		for k, v := range pod.Annotations {
			fmt.Printf("    %s: %s\n", k, v)
		}
	} else {
		fmt.Printf("  Annotations: <none>\n")
	}
	fmt.Println()
}

func verifyNoLogsForNamespace(t *testing.T, namespace string) {
	fmt.Printf("Verifying no processing logs for namespace %s in nsx-ncp and nsx-operator...\n", namespace)

	pods, err := testData.clientset.CoreV1().Pods("vmware-system-nsx").List(context.TODO(), metav1.ListOptions{
		LabelSelector: "component=nsx-ncp",
	})
	require.NoError(t, err)
	require.NotEmpty(t, pods.Items, "Expected to find nsx-ncp pod")

	ncpPod := pods.Items[0]

	// Poll up to 30 seconds for nsx-operator logs to reflect bare namespace skip
	var opLogsStr string
	start := time.Now()
	for time.Since(start) < 30*time.Second {
		opLogsReq := testData.clientset.CoreV1().Pods("vmware-system-nsx").GetLogs(ncpPod.Name, &v1.PodLogOptions{
			Container:    "nsx-operator",
			SinceSeconds: func(i int64) *int64 { return &i }(180), // Check last 3 minutes
		})
		opLogs, err := opLogsReq.DoRaw(context.TODO())
		if err == nil {
			opLogsStr = string(opLogs)
			if strings.Contains(opLogsStr, namespace) {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}

	require.Contains(t, opLogsStr, namespace, "nsx-operator should log about the bare namespace")
	require.Contains(t, opLogsStr, "Skipping Namespace: not a VPC namespace", "nsx-operator should explicitly log that it skips the bare namespace")

	// Check nsx-ncp logs
	ncpLogsReq := testData.clientset.CoreV1().Pods("vmware-system-nsx").GetLogs(ncpPod.Name, &v1.PodLogOptions{
		Container:    "nsx-ncp",
		SinceSeconds: func(i int64) *int64 { return &i }(180), // Check last 3 minutes
	})
	ncpLogs, err := ncpLogsReq.DoRaw(context.TODO())
	require.NoError(t, err)
	ncpLogsStr := string(ncpLogs)
	// nsx-ncp logs "Skipping" at DEBUG level, which is not visible in INFO level, so it should not contain the namespace name
	require.NotContains(t, ncpLogsStr, namespace, "nsx-ncp should not process bare namespace (or only log at debug level)")

	fmt.Println("✅ Verified correct skip logs for bare namespace in both containers")
}

func isCapabilityActiveOnCluster() bool {
	if testData == nil || testData.kubeConfig == nil {
		return false
	}
	dynClient, err := dynamic.NewForConfig(testData.kubeConfig)
	if err != nil {
		return false
	}
	capabilitiesGVR := schema.GroupVersionResource{
		Group:    "iaas.vmware.com",
		Version:  "v1alpha1",
		Resource: "capabilities",
	}
	capCR, err := dynClient.Resource(capabilitiesGVR).Get(context.TODO(), "supervisor-capabilities", metav1.GetOptions{})
	if err != nil {
		return false
	}
	status, found, err := unstructured.NestedMap(capCR.Object, "status")
	if err != nil || !found {
		return false
	}
	supervisor, found, err := unstructured.NestedMap(status, "supervisor")
	if err != nil || !found {
		return false
	}
	cap, ok := supervisor["supports_per_namespace_network_provider"]
	if !ok {
		return false
	}
	capMap, ok := cap.(map[string]interface{})
	if !ok {
		return false
	}
	activated, ok := capMap["activated"]
	if ok {
		if b, ok := activated.(bool); ok {
			return b
		}
	}
	return false
}

func setupVcCapabilityWithCleanup(t *testing.T) {
	if isCapabilityActiveOnCluster() {
		fmt.Println("supports_per_namespace_network_provider is already active on Supervisor")
		return
	}

	if testData == nil || testData.vcClient == nil {
		t.Skip("Skipping TestM1MixedMode: supports_per_namespace_network_provider capability is not active and vcClient is nil")
		return
	}

	vcHost := testData.vcClient.url.Hostname()
	vcPassword := testOptions.vcRootPassword
	if vcPassword == "" {
		vcPassword = testOptions.vcPassword
	}
	if vcPassword == "" {
		t.Skip("Skipping TestM1MixedMode: supports_per_namespace_network_provider capability is not active and VC SSH password is not set")
		return
	}

	sshConfig := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			ssh.Password(vcPassword),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106
		Timeout:         10 * time.Second,
	}

	// Step 1: Backup and update VC supervisor-capabilities.yaml, then restart wcp
	enableCmd := `
if [ ! -f /etc/vmware/wcp/supervisor-capabilities.yaml.bak ]; then
    cp /etc/vmware/wcp/supervisor-capabilities.yaml /etc/vmware/wcp/supervisor-capabilities.yaml.bak
fi
python3 -c '
import yaml
with open("/etc/vmware/wcp/supervisor-capabilities.yaml", "r") as f:
    data = yaml.safe_load(f)
for item in data.get("supervisor", []):
    if item.get("name") == "supports_per_namespace_network_provider":
        item["enabled"] = True
        item["activatedWhenRule"] = ""
with open("/etc/vmware/wcp/supervisor-capabilities.yaml", "w") as f:
    yaml.safe_dump(data, f)
'
vmon-cli --restart wcp
`
	fmt.Printf("Updating VC (%s) capability config and restarting wcp service...\n", vcHost)
	code, stdout, stderr, err := exec.RunSSHCommand(vcHost+":22", sshConfig, enableCmd)
	if err != nil || code != 0 {
		fmt.Printf("Failed to enable VC capability via SSH (code=%d, stdout=%s, stderr=%s, err=%v)\n", code, stdout, stderr, err)
		if !isCapabilityActiveOnCluster() {
			t.Skipf("Skipping TestM1MixedMode: supports_per_namespace_network_provider is not active on Supervisor and VC SSH setup failed: %v", err)
			return
		}
	}

	// Step 2: Register t.Cleanup for automatic revert upon test completion
	t.Cleanup(func() {
		fmt.Printf("Restoring original VC (%s) capability config and restarting wcp service in t.Cleanup...\n", vcHost)
		revertCmd := `
if [ -f /etc/vmware/wcp/supervisor-capabilities.yaml.bak ]; then
    mv /etc/vmware/wcp/supervisor-capabilities.yaml.bak /etc/vmware/wcp/supervisor-capabilities.yaml
    vmon-cli --restart wcp
fi
`
		_, _, _, _ = exec.RunSSHCommand(vcHost+":22", sshConfig, revertCmd)
	})

	// Wait up to 120 seconds for WCP to restart and supervisor-capabilities CR to be updated
	fmt.Println("Waiting for supports_per_namespace_network_provider capability to become active on Supervisor...")
	pollStart := time.Now()
	for time.Since(pollStart) < 120*time.Second {
		if isCapabilityActiveOnCluster() {
			fmt.Printf("✅ supports_per_namespace_network_provider is now active (took %v)\n", time.Since(pollStart).Round(time.Second))
			return
		}
		time.Sleep(3 * time.Second)
	}

	if !isCapabilityActiveOnCluster() {
		t.Skip("Skipping TestM1MixedMode: supports_per_namespace_network_provider did not become active within 120s after restarting WCP")
	}
}

func TestM1MixedMode(t *testing.T) {
	TrackTest(t)

	setupVcCapabilityWithCleanup(t)

	// We create our own namespaces with specific annotations
	vpcNsName := "m1-vpc-ns-" + getRandomString()
	bareNsName := "m1-bare-ns-" + getRandomString()

	t.Cleanup(func() {
		testData.clientset.CoreV1().Namespaces().Delete(context.TODO(), vpcNsName, metav1.DeleteOptions{})
		testData.clientset.CoreV1().Namespaces().Delete(context.TODO(), bareNsName, metav1.DeleteOptions{})
	})

	// Create VPC Namespace
	vpcNamespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: vpcNsName,
			Annotations: map[string]string{
				"nsx.vmware.com/vpc_network_config": "default",
			},
		},
	}
	_, err := testData.clientset.CoreV1().Namespaces().Create(context.TODO(), vpcNamespace, metav1.CreateOptions{})
	require.NoError(t, err)

	// Create Bare Namespace
	bareNamespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: bareNsName,
		},
	}
	_, err = testData.clientset.CoreV1().Namespaces().Create(context.TODO(), bareNamespace, metav1.CreateOptions{})
	require.NoError(t, err)

	// Wait a bit for namespace processing
	time.Sleep(5 * time.Second)

	// Create an INVALID NetworkPolicy in VPC namespace
	// We deliberately create an invalid NetworkPolicy (matchExpressions with > 5 values).
	// If nsx-operator processes this, it will definitely add an error annotation (nsx-op/error).
	vpcNP := &v1networking.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-np-vpc",
			Namespace: vpcNsName,
		},
		Spec: v1networking.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "app",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"v1", "v2", "v3", "v4", "v5", "v6"}, // > 5 values is invalid for nsx-operator
					},
				},
			},
			PolicyTypes: []v1networking.PolicyType{v1networking.PolicyTypeIngress},
		},
	}
	_, err = testData.clientset.NetworkingV1().NetworkPolicies(vpcNsName).Create(context.TODO(), vpcNP, metav1.CreateOptions{})
	require.NoError(t, err)

	// Create a pod in VPC namespace
	vpcPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-vpc",
			Namespace: vpcNsName,
			Labels: map[string]string{
				"app": "test-vpc",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "nginx",
					Image: "nginx:alpine",
				},
			},
		},
	}
	_, err = testData.clientset.CoreV1().Pods(vpcNsName).Create(context.TODO(), vpcPod, metav1.CreateOptions{})
	require.NoError(t, err)

	// Create a pod in Bare namespace
	barePod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-bare",
			Namespace: bareNsName,
			Labels: map[string]string{
				"app": "test-bare",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "nginx",
					Image: "nginx:alpine",
				},
			},
		},
	}
	_, err = testData.clientset.CoreV1().Pods(bareNsName).Create(context.TODO(), barePod, metav1.CreateOptions{})
	require.NoError(t, err)

	// Create an INVALID NetworkPolicy in Bare namespace
	// We deliberately create an invalid NetworkPolicy (egress with ipBlock and named port).
	// If NCP processes this namespace, it will definitely add an error annotation (ncp/error).
	// Therefore, if we find NO error annotation, it's strong proof that NCP completely ignored it.
	portProtocol := v1.ProtocolTCP
	namedPort := "my-named-port"
	bareNP := &v1networking.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-np-bare",
			Namespace: bareNsName,
		},
		Spec: v1networking.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test-bare"},
			},
			PolicyTypes: []v1networking.PolicyType{v1networking.PolicyTypeEgress},
			Egress: []v1networking.NetworkPolicyEgressRule{
				{
					To: []v1networking.NetworkPolicyPeer{
						{
							IPBlock: &v1networking.IPBlock{
								CIDR: "10.0.0.0/24",
							},
						},
					},
					Ports: []v1networking.NetworkPolicyPort{
						{
							Protocol: &portProtocol,
							Port: &intstr.IntOrString{
								Type:   intstr.String,
								StrVal: namedPort,
							},
						},
					},
				},
			},
		},
	}
	_, err = testData.clientset.NetworkingV1().NetworkPolicies(bareNsName).Create(context.TODO(), bareNP, metav1.CreateOptions{})
	require.NoError(t, err)

	// Wait for Operator to process VPC namespace
	waitForSubnetSet(t, vpcNsName, 60*time.Second)

	// Fetch and print Pod details
	printPodDetails(t, vpcNsName, "test-pod-vpc")
	printPodDetails(t, bareNsName, "test-pod-bare")

	// Verify Bare Namespace Self-Blocking
	// 1. Verify no SubnetSet is created in bare namespace (NSX-Operator VPC NS controller ignored it)
	subnetSets, err := testData.crdClientset.CrdV1alpha1().SubnetSets(bareNsName).List(context.TODO(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Empty(t, subnetSets.Items, "Expected no SubnetSets in bare namespace")

	// 2. Verify no ncp/snat_ip annotation on bare namespace (NCP T1 NS controller ignored it)
	updatedBareNs, err := testData.clientset.CoreV1().Namespaces().Get(context.TODO(), bareNsName, metav1.GetOptions{})
	require.NoError(t, err)
	_, hasSnatIP := updatedBareNs.Annotations["ncp/snat_ip"]
	require.False(t, hasSnatIP, "Expected no ncp/snat_ip annotation on bare namespace")

	// 3. Verify no logs in nsx-operator and nsx-ncp for the bare namespace
	verifyNoLogsForNamespace(t, bareNsName)

	// 4. Verify the invalid NetworkPolicy does NOT have an error annotation
	// If NCP had processed it, it would have added an error annotation because of the invalid spec.
	updatedBareNP, err := testData.clientset.NetworkingV1().NetworkPolicies(bareNsName).Get(context.TODO(), "test-np-bare", metav1.GetOptions{})
	require.NoError(t, err)
	_, hasBareError := updatedBareNP.Annotations["ncp/error"]
	require.False(t, hasBareError, "Expected no ncp/error annotation on the invalid NetworkPolicy in Bare NS, which proves NCP ignored it")

	updatedVpcNP, err := testData.clientset.NetworkingV1().NetworkPolicies(vpcNsName).Get(context.TODO(), "test-np-vpc", metav1.GetOptions{})
	require.NoError(t, err)
	_, hasVpcNcpError := updatedVpcNP.Annotations["ncp/error"]
	require.False(t, hasVpcNcpError, "Expected no ncp/error annotation on the invalid NetworkPolicy in VPC NS, which proves NCP ignored it")
	_, hasVpcOpError := updatedVpcNP.Annotations["nsx-op/error"]
	require.True(t, hasVpcOpError, "Expected nsx-op/error annotation on the invalid NetworkPolicy in VPC NS, which proves nsx-operator processed it")

	fmt.Printf("Created VPC Pod in %s\n", vpcNsName)
	fmt.Printf("Created Bare Pod in %s\n", bareNsName)
	fmt.Println("Verified Bare Namespace Self-Blocking successfully")
}
