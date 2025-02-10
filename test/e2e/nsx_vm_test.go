package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCreateVM(t *testing.T) {
	t.Run("testCreateVMBasic", func(t *testing.T) { testCreateVMBasic(t) })
}

func testCreateVMBasic(t *testing.T) {
	_, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()

	ns := "test-create-vm-basic"

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
	time.Sleep(time.Hour * 10)

	// Create public vm
	storagePolicyID, _ := testData.vcClient.getStoragePolicyID()
	log.V(1).Info("Get storage policy", "storagePolicyID", storagePolicyID)
	clusterImage, _ := testData.vcClient.getClusterVirtualMachineImage()
	log.V(1).Info("Get cluster image", "clusterImage", clusterImage)
	// replace clusterImage with the real image name, storagePolicyID with the real storage policy ID in public_vm.yaml
	publicVMPath, _ := filepath.Abs("./manifest/testVM/public_vm.yaml")
	// use sed to replace the image name and storage policy ID
	sedCmd := fmt.Sprintf("sed -i 's/{$imageName}/%s/g' %s", clusterImage, publicVMPath)
	sedCmd = fmt.Sprintf("%s && sed -i 's/{$storageClass}/%s/g' %s", sedCmd, storagePolicyID, publicVMPath)
	cmd := exec.Command("bash", "-c", sedCmd)
	var stdout, stderr bytes.Buffer
	log.V(1).Info("sedCmd", "sedCmd", sedCmd)
	command := exec.Command("bash", "-c", sedCmd)
	command.Stdout = &stdout
	command.Stderr = &stderr
	log.V(1).Info("stdout", "stdout", stdout.String())
	log.V(1).Info("stderr", "stderr", stderr.String())

	log.Info("Executing", "cmd", cmd)
	require.NoError(t, applyYAML(publicVMPath, ns))
	defer deleteYAML(publicVMPath, ns)
	time.Sleep(time.Hour * 10)
}
