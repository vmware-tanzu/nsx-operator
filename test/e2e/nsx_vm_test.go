package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateVM(t *testing.T) {
	t.Run("testCreateVMBasic", func(t *testing.T) { testCreateVMBasic(t) })
}

func testCreateVMBasic(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	ns := "test-create-vm-basic-2"

	err := testData.createVCNamespace(ns)
	if err != nil {
		t.Fatalf("Failed to create VC namespace: %v", err)
	}
	defer func() {
		err := testData.deleteVCNamespace(ns)
		if err != nil {
			t.Fatalf("Failed to delete VC namespace: %v", err)
		}
	}()

	// Create public vm
	storageClassName, storagePolicyID, _ := testData.vcClient.getStoragePolicyID()
	// storageClass uses - instead of _
	storageClassName = strings.ReplaceAll(storageClassName, "_", "-")
	log.V(1).Info("Get storage policy", "storagePolicyID", storagePolicyID, "storageClassName", storageClassName)
	clusterImage, _ := testData.vcClient.getClusterVirtualMachineImage()
	log.V(1).Info("Get cluster image", "clusterImage", clusterImage)
	// replace clusterImage with the real image name, storagePolicyID with the real storage policy ID in public_vm.yaml
	publicVMPath, _ := filepath.Abs("./manifest/testVM/public_vm.yaml")

	// use sed to replace the image name and storage policy ID
	sedCmd := fmt.Sprintf("sed -i 's/{$imageName}/%s/g' %s", clusterImage, publicVMPath)
	sedCmd = fmt.Sprintf("%s && sed -i 's/{$storageClass}/%s/g' %s", sedCmd, storageClassName, publicVMPath)

	cmd := exec.Command("bash", "-c", sedCmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.V(1).Info("sedCmd", "sedCmd", sedCmd)
	if err := cmd.Run(); err != nil {
		log.Error(err, "Failed to execute sed command", "stderr", stderr.String())
		t.Fatalf("Failed to execute sed command: %v", err)
	}

	log.V(1).Info("stdout", "stdout", stdout.String())
	log.V(1).Info("stderr", "stderr", stderr.String())

	require.NoError(t, applyYAML(publicVMPath, ns))
	defer deleteYAML(publicVMPath, ns)
	// creating vm takes time
	ipv4, err := testData.vmWaitFor(resourceReadyTime*2, ns, "public-vm")
	log.Info("Get public VM IP", "ipv4", ipv4)
	assert.NoError(t, err)
	assert.NotEmpty(t, ipv4)
	err = testSSHConnection(ipv4, "vmware", "Admin!23", 22)
	assert.NoError(t, err)
	log.Info("Public VM is ready")
}
