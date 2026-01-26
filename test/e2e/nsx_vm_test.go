package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateVM(t *testing.T) {
	TrackTest(t)
	StartParallel(t)

	// Clean up namespace when VM tests complete
	t.Cleanup(func() { CleanupVCNamespaces(NsCreateVM) })

	// ParallelTests: VM tests use independent resources and can run concurrently
	RunSubtest(t, "ParallelTests", func(t *testing.T) {
		RunSubtest(t, "testCreateVMBasic", func(t *testing.T) {
			StartParallel(t)
			testCreateVMBasic(t)
		})
	})
}

func testCreateVMBasic(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	// Use pre-created namespace
	ns := NsCreateVM

	// Create public vm
	storageClassName, storagePolicyID, _ := testData.vcClient.getStoragePolicyID()
	// storageClass uses - instead of _
	storageClassName = strings.ReplaceAll(storageClassName, "_", "-")
	log.Debug("Get storage policy", "storagePolicyID", storagePolicyID, "storageClassName", storageClassName)
	clusterImage, _ := testData.vcClient.getClusterVirtualMachineImage()
	log.Debug("Get cluster image", "clusterImage", clusterImage)
	// replace clusterImage with the real image name, storagePolicyID with the real storage policy ID in public_vm.yaml
	publicVMPath, _ := filepath.Abs("./manifest/testVM/public_vm.yaml")

	// use sed to replace the image name and storage policy ID
	sedCmd := fmt.Sprintf("sed -i 's/{$imageName}/%s/g' %s", clusterImage, publicVMPath)
	sedCmd = fmt.Sprintf("%s && sed -i 's/{$storageClass}/%s/g' %s", sedCmd, storageClassName, publicVMPath)

	cmd := exec.Command("bash", "-c", sedCmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.Debug("sedCmd", "sedCmd", sedCmd)
	if err := cmd.Run(); err != nil {
		log.Error(err, "Failed to execute sed command", "stderr", stderr.String())
		t.Fatalf("Failed to execute sed command: %v", err)
	}

	require.NoError(t, applyYAML(publicVMPath, ns))
	defer deleteYAML(publicVMPath, ns)
	// creating vm takes time
	log.Info("Waiting for VM to get IP", "vmName", "public-vm", "namespace", ns, "timeout", resourceReadyTime*2)
	ipv4, err := testData.vmWaitFor(resourceReadyTime*2, ns, "public-vm")
	if err != nil {
		// Log VM status for debugging
		statusCmd := exec.Command("kubectl", "get", "vm", "public-vm", "-n", ns, "-o", "yaml")
		statusOutput, _ := statusCmd.CombinedOutput()
		log.Error(err, "Failed to get VM IP", "vmName", "public-vm", "namespace", ns, "vmStatus", string(statusOutput))

		// Log events for the VM
		eventsCmd := exec.Command("kubectl", "get", "events", "-n", ns, "--field-selector", "involvedObject.name=public-vm", "--sort-by=.lastTimestamp")
		eventsOutput, _ := eventsCmd.CombinedOutput()
		log.Info("VM events", "events", string(eventsOutput))
	}
	log.Info("Get public VM IP", "ipv4", ipv4)
	assert.NoError(t, err)
	assert.NotEmpty(t, ipv4)
	err = testSSHConnection(ipv4, "vmware", "Admin!23", 22, 5*time.Second, 3, 2*time.Second)
	assert.NoError(t, err)
	log.Info("Public VM is ready")
}
