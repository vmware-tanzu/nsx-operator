package e2e

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	vpcNetworkConfigCRName = "default"
	// subnetDeletionTimeout requires a bigger value than defaultTimeout, it's because that it takes some time for NSX to
	// recycle allocated IP addresses and NSX VPCSubnet won't be deleted until all IP addresses have been recycled.
	subnetDeletionTimeout = 600 * time.Second
)

var (
	subnetTestNamespace       = fmt.Sprintf("subnet-e2e-%s", getRandomString())
	subnetTestNamespaceShared = fmt.Sprintf("subnet-e2e-shared-%s", getRandomString())
	subnetTestNamespaceTarget = fmt.Sprintf("target-ns-%s", getRandomString())

	precreatedSubnetNs1        = fmt.Sprintf("precreated-subnet-ns1-%s", getRandomString())
	precreatedSubnetNs2        = fmt.Sprintf("precreated-subnet-ns2-%s", getRandomString())
	precreatedSubnetNsTargetNs = fmt.Sprintf("precreated-target-ns-%s", getRandomString())
)

func verifySubnetSetCR(subnetSet string) bool {
	vpcNetworkConfig, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(context.TODO(), vpcNetworkConfigCRName, v1.GetOptions{})
	if err != nil {
		log.Error(err, "Failed to get VPCNetworkConfiguration", "name", vpcNetworkConfigCRName)
		return false
	}
	subnetSetCR, err := testData.crdClientset.CrdV1alpha1().SubnetSets(subnetTestNamespace).Get(context.TODO(), subnetSet, v1.GetOptions{})
	if err != nil {
		log.Error(err, "Failed to get SubnetSet", "namespace", subnetTestNamespace, "name", subnetSet)
		return false
	}

	if subnetSetCR.Spec.IPv4SubnetSize != vpcNetworkConfig.Spec.DefaultSubnetSize {
		log.Error(nil, "IPv4SubnetSize mismatch", "IPv4SubnetSize", subnetSetCR.Spec.IPv4SubnetSize, "expected", vpcNetworkConfig.Spec.DefaultSubnetSize)
		return false
	}
	return true
}

func TestSubnetSet(t *testing.T) {
	setupTest(t, subnetTestNamespace)

	targetNs := &corev1.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name: subnetTestNamespaceTarget,
			Annotations: map[string]string{
				common.AnnotationSharedVPCNamespace: subnetTestNamespaceTarget,
			},
		},
	}
	_, err := testData.clientset.CoreV1().Namespaces().Create(context.TODO(), targetNs, v1.CreateOptions{})
	require.NoError(t, err)
	defer func() {
		_ = testData.clientset.CoreV1().Namespaces().Delete(context.TODO(), subnetTestNamespaceTarget, v1.DeleteOptions{})
	}()

	sharedNs := &corev1.Namespace{
		ObjectMeta: v1.ObjectMeta{
			Name: subnetTestNamespaceShared,
			Annotations: map[string]string{
				common.AnnotationSharedVPCNamespace: subnetTestNamespaceTarget,
			},
		},
	}
	_, err = testData.clientset.CoreV1().Namespaces().Create(context.TODO(), sharedNs, v1.CreateOptions{})
	require.NoError(t, err)
	defer func() {
		_ = testData.clientset.CoreV1().Namespaces().Delete(context.TODO(), subnetTestNamespaceShared, v1.DeleteOptions{})
	}()

	t.Cleanup(func() {
		teardownTest(t, subnetTestNamespace, subnetDeletionTimeout)
		teardownTest(t, subnetTestNamespaceShared, subnetDeletionTimeout)
		teardownTest(t, subnetTestNamespaceTarget, subnetDeletionTimeout)
	})

	t.Run("case=DefaultSubnetSet", defaultSubnetSet)
	t.Run("case=UserSubnetSet", UserSubnetSet)
	t.Run("case=SharedSubnetSet", sharedSubnetSet)
	t.Run("case=SubnetCIDR", SubnetCIDR)
	t.Run("case=NoIPSubnet", NoIPSubnet)
	t.Run("case=SubnetValidate", SubnetValidate)
	t.Run("case=SubnetPortWithIPAM", SubnetPortWithIPAM)
	t.Run("case=SubnetPortWithDHCP", SubnetPortWithDHCP)
}

func TestSubnetPrecreated(t *testing.T) {
	// Use pre-created namespaces instead of creating new ones
	// Map the old variable names to pre-created namespace names
	precreatedSubnetNs1 = NsSubnetPrecreated1
	precreatedSubnetNs2 = NsSubnetPrecreated2
	precreatedSubnetNsTargetNs = NsSubnetPrecreatedTarget

	t.Run("case=PrecreatedSubnetBasic", PrecreatedSharedSubnetBasic)
	t.Run("case=PrecreatedSubnetRemovePath", PrecreatedSharedSubnetRemovePath)
	t.Run("case=PrecreatedSharedSubnetAddPath", PrecreatedSharedSubnetAddPath)
	t.Run("case=PrecreatedSharedSubnetDeleteFail", PrecreatedSharedSubnetDeleteFail)
	t.Run("case=PrecreatedSharedSubnetUpdateFail", PrecreatedSharedSubnetUpdateFail)
	t.Run("case=NormalSubnetManagedByNSXOp", NormalSubnetManagedByNSXOp)
	t.Run("case=SubnetWithAssociatedResourceAnnotation", SubnetWithAssociatedResourceAnnotation)
	// Comment it, since it requires a long time to run.
	//t.Run("case=PrecreatedSharedSubnetPoll", PrecreatedSharedSubnetPoll)
}

func transSearchResponsetoSubnet(response model.SearchResponse) []model.VpcSubnet {
	var resources []model.VpcSubnet
	if response.Results == nil {
		return resources
	}
	for _, result := range response.Results {
		obj, err := common.NewConverter().ConvertToGolang(result, model.VpcSubnetBindingType())
		if err != nil {
			log.Info("Failed to convert to golang subnet", "error", err)
			return resources
		}
		if subnet, ok := obj.(model.VpcSubnet); ok {
			resources = append(resources, subnet)
		}
	}
	return resources
}

func fetchSubnetBySubnetSet(t *testing.T, subnetSet *v1alpha1.SubnetSet) model.VpcSubnet {
	tags := []string{common.TagScopeSubnetSetCRUID, string(subnetSet.UID)}
	results, err := testData.queryResource(common.ResourceTypeSubnet, tags)
	require.NoError(t, err)
	subnets := transSearchResponsetoSubnet(results)
	require.True(t, len(subnets) > 0, "No NSX Subnet found")
	return subnets[0]
}

func transSearchResponsetoSubnetPort(response model.SearchResponse) []model.VpcSubnetPort {
	var resources []model.VpcSubnetPort
	if response.Results == nil {
		return resources
	}
	for _, result := range response.Results {
		obj, err := common.NewConverter().ConvertToGolang(result, model.VpcSubnetPortBindingType())
		if err != nil {
			log.Info("Failed to convert to golang SubnetPort", "error", err)
			return resources
		}
		if subnetport, ok := obj.(model.VpcSubnetPort); ok {
			resources = append(resources, subnetport)
		}
	}
	return resources
}

func defaultSubnetSet(t *testing.T) {
	// 1. Check whether default-vm-subnetset and default-pod-subnetset are created.
	assureSubnetSet(t, subnetTestNamespace, common.DefaultVMSubnetSet)
	assureSubnetSet(t, subnetTestNamespace, common.DefaultPodSubnetSet)

	// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
	require.True(t, verifySubnetSetCR(common.DefaultVMSubnetSet))
	require.True(t, verifySubnetSetCR(common.DefaultPodSubnetSet))

	portPath, _ := filepath.Abs("./manifest/testSubnet/subnetport_1.yaml")
	require.NoError(t, applyYAML(portPath, subnetTestNamespace))
	assureSubnetPort(t, subnetTestNamespace, "port-e2e-test-1", conditionSubnetPortRealized)
	defer deleteYAML(portPath, subnetTestNamespace)

	// 3. Check SubnetSet CR status should be updated with NSX Subnet info.
	subnetSet, err := testData.crdClientset.CrdV1alpha1().SubnetSets(subnetTestNamespace).Get(context.TODO(), common.DefaultPodSubnetSet, v1.GetOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")
	// 4. Check NSX Subnet allocation.
	networkAddress := subnetSet.Status.Subnets[0].NetworkAddresses
	assert.True(t, len(networkAddress) > 0, "No network address in SubnetSet")

	// 5. Check adding NSX Subnet tags.
	ns, err := testData.clientset.CoreV1().Namespaces().Get(context.TODO(), subnetTestNamespace, v1.GetOptions{})
	require.NoError(t, err)
	labelKey, labelValue := "subnet-e2e", "add"
	ns.Labels[labelKey] = labelValue
	_, err = testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	require.NoError(t, err)

	vpcSubnet := fetchSubnetBySubnetSet(t, subnetSet)
	found := false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey && *tag.Tag == labelValue {
			found = true
			break
		}
	}
	assert.True(t, found, "Failed to add tags for NSX Subnet %s", *(vpcSubnet.Id))

	// 6. Check updating NSX Subnet tags.
	ns, err = testData.clientset.CoreV1().Namespaces().Get(context.TODO(), subnetTestNamespace, v1.GetOptions{})
	require.NoError(t, err)
	labelValue = "update"
	ns.Labels[labelKey] = labelValue
	_, err = testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	require.NoError(t, err)
	vpcSubnet = fetchSubnetBySubnetSet(t, subnetSet)
	found = false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey && *tag.Tag == labelValue {
			found = true
			break
		}
	}
	assert.True(t, found, "Failed to update tags for NSX Subnet %s", *(vpcSubnet.Id))

	// 7. Check deleting NSX Subnet tags.
	ns, err = testData.clientset.CoreV1().Namespaces().Get(context.TODO(), subnetTestNamespace, v1.GetOptions{})
	require.NoError(t, err)
	delete(ns.Labels, labelKey)
	newNs, err := testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	require.NoError(t, err)
	log.Trace("New Namespace", "Namespace", newNs)
	vpcSubnet = fetchSubnetBySubnetSet(t, subnetSet)
	found = false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey {
			found = true
			break
		}
	}
	assert.False(t, found, "Failed to delete tags for NSX Subnet %s", *(vpcSubnet.Id))
}

func UserSubnetSet(t *testing.T) {
	subnetSetYAMLs := []string{
		"./manifest/testSubnet/subnetset-static.yaml",
		"./manifest/testSubnet/subnetset-dhcp.yaml",
	}
	subnetSetNames := []string{
		"user-subnetset-static",
		"user-subnetset-dhcp",
	}
	portYAMLs := []string{
		"./manifest/testSubnet/subnetport-in-static-subnetset.yaml",
		"./manifest/testSubnet/subnetport-in-dhcp-subnetset.yaml",
	}
	portNames := []string{
		"port-in-static-subnetset",
		"port-in-dhcp-subnetset",
	}
	for idx, subnetSetYAML := range subnetSetYAMLs {
		subnetSetName := subnetSetNames[idx]
		portYAML := portYAMLs[idx]
		portName := portNames[idx]
		// 1. Check SubnetSet created by user.
		subnetSetPath, _ := filepath.Abs(subnetSetYAML)
		deleteYAML(subnetSetPath, subnetTestNamespace)

		require.NoError(t, applyYAML(subnetSetPath, subnetTestNamespace))

		assureSubnetSet(t, subnetTestNamespace, subnetSetName)

		// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
		require.True(t, verifySubnetSetCR(subnetSetName))

		portPath, _ := filepath.Abs(portYAML)
		require.NoError(t, applyYAML(portPath, subnetTestNamespace))
		assureSubnetPort(t, subnetTestNamespace, portName, conditionSubnetPortRealized)

		// 3. Check SubnetSet CR status should be updated with NSX Subnet info.
		subnetSet, err := testData.crdClientset.CrdV1alpha1().SubnetSets(subnetTestNamespace).Get(context.TODO(), subnetSetName, v1.GetOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")

		// 4. Check IP address is (not) allocated to SubnetPort.
		err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 100*time.Second, false, func(ctx context.Context) (bool, error) {
			port, err := testData.crdClientset.CrdV1alpha1().SubnetPorts(subnetTestNamespace).Get(context.TODO(), portName, v1.GetOptions{})
			if err != nil {
				log.Error(err, "Check SubnetPort", "port", port)
				return false, err
			}
			if port == nil || len(port.Status.NetworkInterfaceConfig.IPAddresses) == 0 {
				return false, nil
			}
			log.Trace("Check IP address", "IPAddress", port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "portName", portName)
			if portName == "port-in-static-subnetset" {
				if port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress != "" {
					return true, nil
				}
			} else if portName == "port-in-dhcp-subnetset" {
				if port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress == "" {
					return true, nil
				}
			}
			return false, nil
		})
		require.NoError(t, err)

		// 5. Check NSX Subnet allocation.
		networkAddress := subnetSet.Status.Subnets[0].NetworkAddresses
		assert.True(t, len(networkAddress) > 0, "No network address in SubnetSet")
		deleteYAML(portPath, subnetTestNamespace)
		deleteYAML(subnetSetPath, subnetTestNamespace)
	}
}

func sharedSubnetSet(t *testing.T) {
	// 1. Check whether default-vm-subnetset and default-pod-subnetset are created.
	assureSubnetSet(t, subnetTestNamespaceTarget, common.DefaultVMSubnetSet)
	assureSubnetSet(t, subnetTestNamespaceTarget, common.DefaultPodSubnetSet)
	assureSubnetSet(t, subnetTestNamespaceShared, common.DefaultVMSubnetSet)
	assureSubnetSet(t, subnetTestNamespaceShared, common.DefaultPodSubnetSet)

	// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
	require.True(t, verifySubnetSetCR(common.DefaultVMSubnetSet))
	require.True(t, verifySubnetSetCR(common.DefaultPodSubnetSet))

	portPath, _ := filepath.Abs("./manifest/testSubnet/subnetport_3.yaml")
	require.NoError(t, applyYAML(portPath, subnetTestNamespaceShared))

	assureSubnetPort(t, subnetTestNamespaceShared, "port-e2e-test-3", conditionSubnetPortRealized)
	defer deleteYAML(portPath, subnetTestNamespaceShared)

	// 3. Check SubnetSet CR status should be updated with NSX Subnet info.
	subnetSet, err := testData.crdClientset.CrdV1alpha1().SubnetSets(subnetTestNamespaceShared).Get(context.TODO(), common.DefaultVMSubnetSet, v1.GetOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")

	// 4. Check IP address is allocated to SubnetPort.
	port, err := testData.crdClientset.CrdV1alpha1().SubnetPorts(subnetTestNamespaceShared).Get(context.TODO(), "port-e2e-test-3", v1.GetOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "No IP address in SubnetPort")

	// 5. Check Subnet CIDR contains SubnetPort IP.
	portIP := net.ParseIP(strings.Split(port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "/")[0])
	_, subnetCIDR, err := net.ParseCIDR(subnetSet.Status.Subnets[0].NetworkAddresses[0])
	require.NoError(t, err)
	require.True(t, subnetCIDR.Contains(portIP))
}

func SubnetCIDR(t *testing.T) {
	subnet := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-dhcp-cidr",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetSpec{
			SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
				Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
			},
		},
	}
	// Create a Subnet with DHCPServer mode
	_, err := testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Create(context.TODO(), subnet, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		err = nil
	}
	require.NoError(t, err)
	assureSubnet(t, subnetTestNamespace, subnet.Name, "")
	allocatedSubnet, err := testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
	require.NoError(t, err)
	subnetCRUID := string(allocatedSubnet.UID)
	nsxSubnets := testData.fetchSubnetBySubnetUID(t, subnetCRUID)
	require.Equal(t, 1, len(nsxSubnets))

	targetCIDR := allocatedSubnet.Status.NetworkAddresses[0]
	// Delete the Subnet
	err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Delete(context.TODO(), subnet.Name, v1.DeleteOptions{})
	require.NoError(t, err)

	err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 100*time.Second, false, func(ctx context.Context) (bool, error) {
		_, err := testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
		if err != nil && errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	require.NoError(t, err)
	err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 100*time.Second, false, func(ctx context.Context) (bool, error) {
		nsxSubnets = testData.fetchSubnetBySubnetUID(t, subnetCRUID)
		return len(nsxSubnets) == 0 || *nsxSubnets[0].MarkedForDelete == true, nil
	})
	require.NoError(t, err)

	// Create another Subnet with the same IPAddresses
	subnet.Spec.IPAddresses = []string{targetCIDR}
	_, err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Create(context.TODO(), subnet, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		log.Error(err, "Create Subnet error")
		err = nil
	}
	require.NoError(t, err)
	assureSubnet(t, subnetTestNamespace, subnet.Name, "")
	allocatedSubnet, err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, targetCIDR, allocatedSubnet.Status.NetworkAddresses[0])

	newSubnetCRUID := string(allocatedSubnet.UID)
	nsxSubnets = testData.fetchSubnetBySubnetUID(t, newSubnetCRUID)
	require.Equal(t, 1, len(nsxSubnets))

	// Delete the Subnet
	err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Delete(context.TODO(), subnet.Name, v1.DeleteOptions{})
	require.NoError(t, err)

	err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 100*time.Second, false, func(ctx context.Context) (bool, error) {
		_, err := testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
		if err != nil && errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		nsxSubnets = testData.fetchSubnetBySubnetUID(t, newSubnetCRUID)
		return len(nsxSubnets) == 0 || *nsxSubnets[0].MarkedForDelete == true
	}, 100*time.Second, 1*time.Second)
}

func (data *TestData) fetchSubnetBySubnetUID(t *testing.T, subnetUID string) (res []model.VpcSubnet) {
	tags := []string{common.TagScopeSubnetCRUID, subnetUID}
	results, err := testData.queryResource(common.ResourceTypeSubnet, tags)
	require.NoError(t, err)
	res = transSearchResponsetoSubnet(results)
	return
}

func assureSubnet(t *testing.T, ns, subnetName string, conditionMsg string) (res *v1alpha1.Subnet) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 2*defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, 2*defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		res, err = testData.crdClientset.CrdV1alpha1().Subnets(ns).Get(context.Background(), subnetName, v1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			log.Error(err, "Error fetching Subnet", "subnet", res, "namespace", ns, "name", subnetName)
			return false, fmt.Errorf("error when waiting for Subnet %s", subnetName)
		}
		log.Trace("Subnet status", "status", res.Status)
		for _, con := range res.Status.Conditions {
			if con.Type == v1alpha1.Ready && con.Status == corev1.ConditionTrue && strings.Contains(con.Message, conditionMsg) {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err)
	return res
}

func assureSubnetSet(t *testing.T, ns, subnetSetName string) (res *v1alpha1.SubnetSet) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 2*defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, 2*defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		res, err = testData.crdClientset.CrdV1alpha1().SubnetSets(ns).Get(context.Background(), subnetSetName, v1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			log.Error(err, "SubnetSet", res, "Namespace", ns, "Name", subnetSetName)
			return false, fmt.Errorf("error when waiting for SubnetSet %s", subnetSetName)
		}
		log.Trace("SubnetSets status", "status", res.Status)
		for _, con := range res.Status.Conditions {
			if con.Type == v1alpha1.Ready && con.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err)
	return
}

func conditionSubnetPortRealized(subnetPort *v1alpha1.SubnetPort, args ...string) (bool, error) {
	if len(args) > 0 {
		expectedIP := args[0]
		if len(subnetPort.Status.NetworkInterfaceConfig.IPAddresses) == 0 || !strings.Contains(subnetPort.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, expectedIP+"/") {
			log.Debug("SubnetPort IP address does not match", "expectedIP", expectedIP, "subnetPort.Status.NetworkInterfaceConfig.IPAddresses", subnetPort.Status.NetworkInterfaceConfig.IPAddresses)
			return false, nil
		}
	}
	if len(args) == 2 {
		expectedMAC := args[1]
		if !strings.Contains(subnetPort.Status.NetworkInterfaceConfig.MACAddress, expectedMAC) {
			log.Debug("SubnetPort MAC address does not match", "expected", expectedMAC, "actual", subnetPort.Status.NetworkInterfaceConfig.MACAddress)
			return false, nil
		}
	}
	for _, con := range subnetPort.Status.Conditions {
		if con.Type == v1alpha1.Ready && con.Status == corev1.ConditionTrue {
			log.Debug("SubnetPort is ready", "subnetPort", subnetPort.Name)
			return true, nil
		}
	}
	return false, nil
}

func assureSubnetPort(t *testing.T, ns, subnetPortName string, condition func(subnetport *v1alpha1.SubnetPort, args ...string) (bool, error), args ...string) (res *v1alpha1.SubnetPort) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 2*defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, 2*defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		res, err = testData.crdClientset.CrdV1alpha1().SubnetPorts(ns).Get(context.Background(), subnetPortName, v1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			log.Error(err, "SubnetPort", res, "Namespace", ns, "Name", subnetPortName)
			return false, fmt.Errorf("error when waiting for SubnetPort: %s", subnetPortName)
		}
		log.Trace("SubnetPort status", "status", res.Status)
		return condition(res, args...)
	})
	require.NoError(t, err)
	return res
}

func createSubnetWithCheck(t *testing.T, subnet *v1alpha1.Subnet) (res *v1alpha1.Subnet) {
	_, err := testData.crdClientset.CrdV1alpha1().Subnets(subnet.Namespace).Create(context.TODO(), subnet, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		err = nil
	}
	require.NoError(t, err)
	res = assureSubnet(t, subnet.Namespace, subnet.Name, "")
	return res
}

func createSubnetPortWithCheck(t *testing.T, subnetPort *v1alpha1.SubnetPort, args ...string) (res *v1alpha1.SubnetPort) {
	_, err := testData.crdClientset.CrdV1alpha1().SubnetPorts(subnetPort.Namespace).Create(context.TODO(), subnetPort, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		err = nil
	}
	require.NoError(t, err)
	port := assureSubnetPort(t, subnetPort.Namespace, subnetPort.Name, conditionSubnetPortRealized, args...)
	return port
}

func NoIPSubnet(t *testing.T) {
	noIPSubnet := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-no-ip",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetSpec{
			AccessMode: v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(false),
				},
			},
			SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
				Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated),
			},
		},
	}
	createSubnetWithCheck(t, noIPSubnet)

	noIPSubnetPort := &v1alpha1.SubnetPort{
		ObjectMeta: v1.ObjectMeta{
			Name:      "port-in-no-ip-subnet",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetPortSpec{
			Subnet: "subnet-no-ip",
		},
	}
	portCreated := createSubnetPortWithCheck(t, noIPSubnetPort)
	require.NotNil(t, portCreated.Status.NetworkInterfaceConfig, "No NetworkInterfaceConfig in SubnetPort")
	require.Empty(t, portCreated.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "IPAddresses should be empty for Subnet with no IP addresses")
	require.Equal(t, true, portCreated.Status.NetworkInterfaceConfig.DHCPDeactivatedOnSubnet, "DHCPDeactivatedOnSubnet should be true for Subnet with no IP addresses")
}

func SubnetValidate(t *testing.T) {
	// Ensure that the staticIPAllocation and DHCP cannot be enabled at the same time.
	subnetStaticDHCPServer := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-static-dhcpserver",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetSpec{
			AccessMode: v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
			SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
				Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
			},
		},
	}
	_, err := testData.crdClientset.CrdV1alpha1().Subnets(subnetStaticDHCPServer.Namespace).Create(context.TODO(), subnetStaticDHCPServer, v1.CreateOptions{})
	require.NotNil(t, err, "Subnet with staticIPAllocation enabled should not be created with DHCPServer mode")

	// Ensure that when staticIPAllocation disabled, the DHCP mode is able to be changed from DHCPDeactivated to DHCPServer
	subnetDHCPModify := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-dhcp-modify-1",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetSpec{
			AccessMode: v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(false),
				},
			},
			SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
				Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated),
			},
		},
	}
	subnetDHCPModifyCreated := createSubnetWithCheck(t, subnetDHCPModify)
	subnetDHCPModifyCreated.Spec.SubnetDHCPConfig.Mode = v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer)
	_, err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Update(context.TODO(), subnetDHCPModifyCreated, v1.UpdateOptions{})
	require.Nil(t, err, "Subnet DHCP mode should be able to be changed from DHCPDeactivated to DHCPServer")
	assureSubnet(t, subnetTestNamespace, subnetDHCPModifyCreated.Name, "DHCPServer")

	// Ensure that the NSX operator can populate the staticIPAllocation field in Subnet with DHCPServer mode.
	subnetOnlyDHCP := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-only-dhcp",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetSpec{
			AccessMode: v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
				Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
			},
		},
	}
	subnetOnlyDHCPCreated := createSubnetWithCheck(t, subnetOnlyDHCP)
	require.Equal(t, false, *subnetOnlyDHCPCreated.Spec.AdvancedConfig.StaticIPAllocation.Enabled, "StaticIPAllocation should be disabled for Subnet with DHCPServer mode")
	subnetOnlyNoDHCP := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-only-no-dhcp",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetSpec{
			AccessMode: v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
				Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated),
			},
		},
	}
	subnetOnlyNoDHPCreated := createSubnetWithCheck(t, subnetOnlyNoDHCP)
	require.Equal(t, true, *subnetOnlyNoDHPCreated.Spec.AdvancedConfig.StaticIPAllocation.Enabled, "StaticIPAllocation should be enabled for Subnet with DHCPDeactivated mode")
}

func SubnetPortWithIPAM(t *testing.T) {
	ipamSubnet := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-cidr",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 16,
		},
	}
	// Create the IPAM Subnet
	subnetCreated := createSubnetWithCheck(t, ipamSubnet)
	require.Equal(t, 1, len(subnetCreated.Status.NetworkAddresses))
	ip, cidr, err := net.ParseCIDR(subnetCreated.Status.NetworkAddresses[0])
	require.Nil(t, err)
	ip = ip.To4()
	require.NotNil(t, ip, "Subnet IP should be ipv4")
	// get the first available IP from the Subnet cidr
	ip[3] += 4

	// Case 1: SubnetPort with a valid IP
	subnetportInMACPool := &v1alpha1.SubnetPort{
		ObjectMeta: v1.ObjectMeta{
			Name:      "port-with-ip-1",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetPortSpec{
			Subnet: "subnet-cidr",
			AddressBindings: []v1alpha1.PortAddressBinding{
				{
					IPAddress: ip.To4().String(),
				},
			},
		},
	}
	subnetportInMACPool = createSubnetPortWithCheck(t, subnetportInMACPool)
	nsxSubnetPort := fetchSubnetPortBySubnetPortUID(t, string(subnetportInMACPool.GetUID()))
	require.Equal(t, ip.String(), *nsxSubnetPort.AddressBindings[0].IpAddress)
	require.NotNil(t, nsxSubnetPort.Attachment)
	require.Equal(t, "BOTH", *(*nsxSubnetPort.Attachment).AllocateAddresses)

	// Case 2: SubnetPort with MAC out of NSX MAC POOL
	// NSX MAC pool range is usually 04:50:56:00:00:00-04:50:56:00:ff:ff
	// Hard code the MAC POOL as we use IP_POOL as allocate_addresses for
	// MAC out of the NSX pool and NSX does not check the MAC conflicts for IP_POOL
	macOutOfPool := "ff:50:56:00:00:00"
	ip[3] += 1
	subnetportOutOfMACPool := &v1alpha1.SubnetPort{
		ObjectMeta: v1.ObjectMeta{
			Name:      "port-with-ip-2",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetPortSpec{
			Subnet: "subnet-cidr",
			AddressBindings: []v1alpha1.PortAddressBinding{
				{
					IPAddress:  ip.To4().String(),
					MACAddress: macOutOfPool,
				},
			},
		},
	}
	createSubnetPortWithCheck(t, subnetportOutOfMACPool)
	subnetportOutOfMACPool = createSubnetPortWithCheck(t, subnetportOutOfMACPool)
	nsxSubnetPort = fetchSubnetPortBySubnetPortUID(t, string(subnetportOutOfMACPool.GetUID()))
	require.Equal(t, ip.String(), *nsxSubnetPort.AddressBindings[0].IpAddress)
	require.Equal(t, macOutOfPool, *nsxSubnetPort.AddressBindings[0].MacAddress)
	require.NotNil(t, nsxSubnetPort.Attachment)
	require.Equal(t, "IP_POOL", *(*nsxSubnetPort.Attachment).AllocateAddresses)

	// Case 3: SubnetPort with an IP out of Subnet CIDR
	// Set a finite for retry loop
	for range 5 {
		ip, err = randomIPv4()
		if err == nil && !cidr.Contains(ip) {
			break
		}
	}
	subnetportOutOfSubnet := &v1alpha1.SubnetPort{
		ObjectMeta: v1.ObjectMeta{
			Name:      "port-with-ip-3",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetPortSpec{
			Subnet: "subnet-cidr",
			AddressBindings: []v1alpha1.PortAddressBinding{
				{
					IPAddress: ip.To4().String(),
				},
			},
		},
	}
	_, err = testData.crdClientset.CrdV1alpha1().SubnetPorts(subnetTestNamespace).Create(context.TODO(), subnetportOutOfSubnet, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		err = nil
	}
	require.NoError(t, err)
	conditionMsg := fmt.Sprintf("IP Address %s does not belong to any of the existing ranges in the pool", ip.To4().String())
	assureSubnetPort(t, subnetTestNamespace, subnetportOutOfSubnet.Name, func(subnetport *v1alpha1.SubnetPort, args ...string) (bool, error) {
		for _, con := range subnetport.Status.Conditions {
			if con.Type == v1alpha1.Ready && con.Status == corev1.ConditionFalse && strings.Contains(con.Message, conditionMsg) {
				return true, nil
			}
		}
		return false, nil
	})
}

func SubnetPortWithDHCP(t *testing.T) {
	macInVCMACPool := "00:50:56:ba:d6:7a"
	macInNSXMACPool := "04:50:56:ba:d6:7a"
	dhcpSubnet := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-dhcp",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetSpec{
			SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
				Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer),
			},
		},
	}
	// Create a Subnet with DHCPServer mode and reserved IP ranges.
	// As it's hard to know the available CIDRs in the IP block in advance,
	// we first create a Subnet with DHCPServer mode only to get the available CIDR,
	// then delete and recreate the Subnet with the available CIDR as specified IPAddresses.
	subnetCreated := createSubnetWithCheck(t, dhcpSubnet)
	ipAddresses := subnetCreated.Status.NetworkAddresses[0]
	require.NotEmpty(t, ipAddresses, "No IP address in Subnet")
	_, subnetCIDR, err := net.ParseCIDR(ipAddresses)
	require.NoError(t, err, "Failed to parse Subnet CIDR")
	// get the first available IP from the Subnet cidr
	ip := subnetCIDR.IP.To4()
	require.NotNil(t, ip, "Subnet IP should be ipv4")
	ip[3] += 4
	// ipEnd is a deep copy of ip
	ipEnd := make(net.IP, len(ip))
	copy(ipEnd, ip)
	ipEnd[3] += 9 // reserve 10 IPs
	ipRange := fmt.Sprintf("%s-%s", ip.String(), ipEnd.String())
	dhcpSubnet.Spec.IPAddresses = []string{ipAddresses}
	dhcpSubnet.Spec.SubnetDHCPConfig.DHCPServerAdditionalConfig.ReservedIPRanges = []string{ipRange}
	// Delete subnet, then recreate it with specific IPAddresses
	err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Delete(context.TODO(), dhcpSubnet.Name, v1.DeleteOptions{})
	require.NoError(t, err)
	err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 100*time.Second, false, func(ctx context.Context) (bool, error) {
		_, err := testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Get(context.TODO(), dhcpSubnet.Name, v1.GetOptions{})
		if err != nil && errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	require.NoError(t, err)
	subnetCreated = createSubnetWithCheck(t, dhcpSubnet)
	log.Info("DHCP Subnet with reserved IP ranges created", "Subnet", subnetCreated)
	require.Equal(t, 1, len(subnetCreated.Status.NetworkAddresses))
	require.Equal(t, ipAddresses, subnetCreated.Status.NetworkAddresses[0])
	// Case 1: SubnetPort with a MAC in VC MAC POOL, check the IP/MAC are in the SubnetPort status.
	subnetPortInVCMACPool := &v1alpha1.SubnetPort{
		ObjectMeta: v1.ObjectMeta{
			Name:      "port-in-dhcp-subnet-with-address-bindings-vc-macpool",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetPortSpec{
			Subnet: "subnet-dhcp",
			AddressBindings: []v1alpha1.PortAddressBinding{
				{
					IPAddress:  ip.String(),
					MACAddress: macInVCMACPool,
				},
			},
		},
	}
	subnetPortCreated := createSubnetPortWithCheck(t, subnetPortInVCMACPool, ip.String(), macInVCMACPool)
	require.Equal(t, false, subnetPortCreated.Status.NetworkInterfaceConfig.DHCPDeactivatedOnSubnet, "DHCPDeactivatedOnSubnet should be false for Subnet with DHCPServer mode")
	// Case 2: SubnetPort with a MAC in NSX MAC POOL, check the IP/MAC are in the SubnetPort status.
	subnetPortInNSXMACPool := &v1alpha1.SubnetPort{
		ObjectMeta: v1.ObjectMeta{
			Name:      "port-in-dhcp-subnet-with-address-bindings-nsx-macpool",
			Namespace: subnetTestNamespace,
		},
		Spec: v1alpha1.SubnetPortSpec{
			Subnet: "subnet-dhcp",
			AddressBindings: []v1alpha1.PortAddressBinding{
				{
					IPAddress:  ipEnd.String(),
					MACAddress: macInNSXMACPool,
				},
			},
		},
	}
	subnetPortCreated = createSubnetPortWithCheck(t, subnetPortInNSXMACPool, ipEnd.String(), macInNSXMACPool)
	require.Equal(t, false, subnetPortCreated.Status.NetworkInterfaceConfig.DHCPDeactivatedOnSubnet, "DHCPDeactivatedOnSubnet should be false for Subnet with DHCPServer mode")
}

func randomIPv4() (net.IP, error) {
	b := make([]byte, 4)
	_, err := rand.Read(b)
	if err != nil {
		return nil, err
	}
	return net.IPv4(b[0], b[1], b[2], b[3]), nil
}

func fetchSubnetPortBySubnetPortUID(t *testing.T, subnetPortUID string) *model.VpcSubnetPort {
	tags := []string{common.TagScopeSubnetPortCRUID, subnetPortUID}
	results, err := testData.queryResource(common.ResourceTypeSubnetPort, tags)
	require.NoError(t, err)
	res := transSearchResponsetoSubnetPort(results)
	return &res[0]
}

// createSharedSubnet gets an existing shared subnet from NSX side and returns the subnet path
func createSharedSubnet(t *testing.T, subnetName string) string {
	subnetID := fmt.Sprintf("%s-%s", subnetName, getRandomString()[:5])

	// Create subnet using Kubernetes API
	subnet := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      subnetID,
			Namespace: precreatedSubnetNsTargetNs,
		},
		Spec: v1alpha1.SubnetSpec{
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
		},
	}

	// Create the subnet
	_, err := testData.crdClientset.CrdV1alpha1().Subnets(precreatedSubnetNsTargetNs).Create(context.TODO(), subnet, v1.CreateOptions{})
	require.NoError(t, err, "Failed to create subnet using Kubernetes API")

	// Wait for the subnet to be ready
	createdSubnet := assureSubnet(t, precreatedSubnetNsTargetNs, subnetID, "")

	// Get the NSX subnet to extract VPC path information
	subnetUID := string(createdSubnet.UID)
	nsxSubnets := testData.fetchSubnetBySubnetUID(t, subnetUID)
	require.NotEmpty(t, nsxSubnets, "No subnet found with UID %s", subnetUID)

	// Use the first subnet from the query results
	nsxSubnet := nsxSubnets[0]
	require.NotNil(t, nsxSubnet.Path, "Subnet path is nil")

	subnetPath := *nsxSubnet.Path
	log.Info("Using existing subnet from NSX", "path", subnetPath, "subnetID", *nsxSubnet.Id)
	return subnetPath
}

func createSharedSubnets(subnetPaths []string) []v1alpha1.SharedSubnet {
	length := len(subnetPaths)
	sharedSubnets := make([]v1alpha1.SharedSubnet, length)
	for i, path := range subnetPaths {
		sharedSubnets[i] = v1alpha1.SharedSubnet{
			Path: path,
		}
	}
	return sharedSubnets
}

// updateVPCNetworkConfigSubnets is a helper function that updates VPC network configuration subnets
// Parameters:
// - t: testing.T instance
// - namespace: namespace containing the VPC network config
// - subnetPaths: list of subnet paths to set or append
// - appendMode: if true, append to existing subnets; if false, replace all subnets
func updateVPCNetworkConfigSubnets(t *testing.T, namespace string, subnetPaths []v1alpha1.SharedSubnet, appendMode bool) {
	// Get the vpc_network_config annotation from the namespace
	nsObj, err := testData.clientset.CoreV1().Namespaces().Get(context.TODO(), namespace, v1.GetOptions{})
	require.NoError(t, err)
	vpcNetworkConfigName := nsObj.Annotations[common.AnnotationVPCNetworkConfig]
	require.NotEmpty(t, vpcNetworkConfigName, "vpc_network_config annotation should not be empty")

	// Get the VPC network configuration using the annotation value
	vpcNetworkConfig, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(context.TODO(), vpcNetworkConfigName, v1.GetOptions{})
	require.NoError(t, err)

	// Update subnets based on mode
	if appendMode {
		vpcNetworkConfig.Spec.Subnets = append(vpcNetworkConfig.Spec.Subnets, subnetPaths...)
	} else {
		vpcNetworkConfig.Spec.Subnets = subnetPaths
	}

	_, err = testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Update(context.TODO(), vpcNetworkConfig, v1.UpdateOptions{})
	require.NoError(t, err)
}

// listNamespaceSubnets lists all subnets in a namespace
func listNamespaceSubnets(ctx context.Context, namespace string) (*v1alpha1.SubnetList, error) {
	subnetList, err := testData.crdClientset.CrdV1alpha1().Subnets(namespace).List(ctx, v1.ListOptions{})
	if err != nil {
		log.Error(err, "Failed to list subnets", "namespace", namespace)
	}
	return subnetList, err
}

// verifySharedSubnets is a unified function that verifies shared subnets in a namespace
// Parameters:
// - t: testing.T instance
// - namespace: namespace to check
// - expectedCount: expected number of shared subnets (0 for none, 1 for single, >1 for multiple)
// - namePrefixes: optional list of name prefixes to validate (ignored if expectedCount is 0)
// Returns:
// - []*v1alpha1.Subnet: list of found shared subnets (empty if expectedCount is 0)
func verifySharedSubnets(t *testing.T, namespace string, expectedCount int, namePrefixes ...string) []*v1alpha1.Subnet {
	var actionMsg string
	switch {
	case expectedCount == 0:
		actionMsg = "to be removed"
	case expectedCount == 1:
		actionMsg = "to be created"
	default:
		actionMsg = fmt.Sprintf("%d to be created", expectedCount)
	}

	log.Info(fmt.Sprintf("Waiting for shared subnet(s) %s", actionMsg), "namespace", namespace)
	var sharedSubnets []*v1alpha1.Subnet

	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 100*time.Second, false, func(ctx context.Context) (bool, error) {
		// Get all subnets from the namespace
		subnetList, err := listNamespaceSubnets(ctx, namespace)
		if err != nil {
			return false, err
		}

		// Find and collect shared subnets
		sharedSubnets = []*v1alpha1.Subnet{}
		log.Info("Checking subnets in namespace", "namespace", namespace, "count", len(subnetList.Items))
		for i := range subnetList.Items {
			subnet := &subnetList.Items[i]
			log.Info("Examining subnet", "namespace", namespace, "name", subnet.Name, "shared", subnet.Status.Shared)
			if subnet.Status.Shared {
				sharedSubnets = append(sharedSubnets, subnet)
			}
		}

		// Check if we have the expected number of shared subnets
		if len(sharedSubnets) != expectedCount {
			log.Info("Waiting for expected number of shared subnets",
				"namespace", namespace,
				"currentCount", len(sharedSubnets),
				"expectedCount", expectedCount)
			return false, nil
		}

		// If expectedCount is 0, we're done
		if expectedCount == 0 {
			return true, nil
		}

		// Verify name prefixes if provided and if we expect subnets
		if len(namePrefixes) > 0 {
			for _, subnet := range sharedSubnets {
				hasExpectedPrefix := false
				for _, prefix := range namePrefixes {
					if strings.HasPrefix(subnet.Name, prefix) {
						hasExpectedPrefix = true
						break
					}
				}
				if !hasExpectedPrefix {
					log.Info("Shared subnet has unexpected name prefix",
						"namespace", namespace,
						"name", subnet.Name,
						"expectedPrefixes", namePrefixes)
					return false, nil
				}
			}
		}

		return true, nil
	})

	// Handle assertions based on the expected count
	if expectedCount == 0 {
		require.NoError(t, err, "Shared subnet should be removed from %s", namespace)
	} else {
		require.NoError(t, err, "Failed to find %d shared subnet(s) in %s", expectedCount, namespace)
		require.Equal(t, expectedCount, len(sharedSubnets),
			"Expected %d shared subnet(s) in %s, got %d", expectedCount, namespace, len(sharedSubnets))
	}

	return sharedSubnets
}

// verifyNoSharedSubnet verifies that no shared subnet exists in the namespace
func verifyNoSharedSubnet(t *testing.T, namespace string) {
	verifySharedSubnets(t, namespace, 0)
}

// verifySharedSubnet verifies that a shared subnet with the expected name prefix exists in the namespace
func verifySharedSubnet(t *testing.T, namespace string, namePrefix string) *v1alpha1.Subnet {
	subnets := verifySharedSubnets(t, namespace, 1, namePrefix)
	return subnets[0]
}

// verifyMultipleSharedSubnets verifies that multiple shared subnets with the expected name prefixes exist in the namespace
func verifyMultipleSharedSubnets(t *testing.T, namespace string, expectedCount int, namePrefixes ...string) []*v1alpha1.Subnet {
	return verifySharedSubnets(t, namespace, expectedCount, namePrefixes...)
}

// PrecreatedSharedSubnetBasic tests sharing a subnet with multiple namespaces
func PrecreatedSharedSubnetBasic(t *testing.T) {
	subnetName := "shared-subnet"
	subnetPath := createSharedSubnet(t, subnetName)
	// Update precreatedSubnetNs1 VPC networkconfig CR with shared subnet
	updateVPCNetworkConfigSubnets(t, precreatedSubnetNs1, createSharedSubnets([]string{subnetPath}), false)
	// Update precreatedSubnetNs2 VPC networkconfig CR with shared subnet
	updateVPCNetworkConfigSubnets(t, precreatedSubnetNs2, createSharedSubnets([]string{subnetPath}), false)
	// Verify the shared subnets exist in precreatedSubnetNs1 and precreatedSubnetNs2 with correct properties
	sharedSubnet1 := verifySharedSubnet(t, precreatedSubnetNs1, subnetName)
	sharedSubnet2 := verifySharedSubnet(t, precreatedSubnetNs2, subnetName)
	log.Info("Shared subnet verification complete", "ns1_subnet", sharedSubnet1.Name, "ns2_subnet", sharedSubnet2.Name)
}

// PrecreatedSharedSubnetRemovePath tests removing a shared subnet path from one namespace
func PrecreatedSharedSubnetRemovePath(t *testing.T) {
	// Remove the shared subnet path from precreatedSubnetNs1 vpcNetworkConfig
	// precreatedSubnetNs1 should not have the shared subnet anymore, but precreatedSubnetNs2 should still have it
	updateVPCNetworkConfigSubnets(t, precreatedSubnetNs1, createSharedSubnets([]string{}), false)
	// Verify the shared subnet is removed from precreatedSubnetNs1
	verifyNoSharedSubnet(t, precreatedSubnetNs1)
	// Verify precreatedSubnetNs2 still has the shared subnet with correct properties
	sharedSubnet2 := verifySharedSubnet(t, precreatedSubnetNs2, "shared-subnet")
	log.Info("Shared subnet verification after removal complete", "ns2_subnet", sharedSubnet2.Name)
}

// updateSubnetConnectivityState updates a subnet's connectivity state in NSX
func updateSubnetConnectivityState(t *testing.T, subnetCR *v1alpha1.Subnet, connectivityState string) {
	// Find the subnet in NSX by subnet UID
	subnetUID := string(subnetCR.UID)
	subnets := testData.fetchSubnetBySubnetUID(t, subnetUID)
	require.NotEmpty(t, subnets, "No subnet found with UID %s", subnetUID)

	// Use the first subnet from the query results
	subnet := subnets[0]
	require.NotNil(t, subnet.Path, "Subnet path is nil")

	// Parse the VPC resource path to get orgID, projectID, vpcID
	parts := strings.Split(*subnet.Path, "/")
	require.GreaterOrEqual(t, len(parts), 4, "Invalid subnet path format")

	orgID := parts[2] // /orgs/{orgID}/projects/{projectID}/vpcs/{vpcID}/subnets/{subnetID}
	projectID := parts[4]
	vpcID := parts[6]
	subnetID := *subnet.Id

	// Update the subnet's connectivity state using the existing subnet object
	updatedSubnet := subnet
	if updatedSubnet.AdvancedConfig == nil {
		updatedSubnet.AdvancedConfig = &model.SubnetAdvancedConfig{}
	}
	updatedSubnet.AdvancedConfig.ConnectivityState = &connectivityState

	log.Info("Updating subnet connectivity state using SubnetsClient", "subnetID", subnetID, "state", connectivityState)
	err := testData.nsxClient.SubnetsClient.Patch(orgID, projectID, vpcID, subnetID, updatedSubnet)
	require.NoError(t, err, "Failed to update subnet connectivity state")
	log.Info("Subnet connectivity state updated successfully", "subnetID", subnetID, "state", connectivityState)
}

// PrecreatedSharedSubnetAddPath tests adding another shared subnet path to a namespace
func PrecreatedSharedSubnetAddPath(t *testing.T) {
	// Create a second shared subnet
	subnetName := "shared-subnet-2"
	subnetPath := createSharedSubnet(t, subnetName)
	// Add the new subnet path to the existing paths in precreatedSubnetNs2 VPC network config
	updateVPCNetworkConfigSubnets(t, precreatedSubnetNs2, createSharedSubnets([]string{subnetPath}), true)
	// Verify precreatedSubnetNs2 has two shared subnets with correct properties
	sharedSubnets := verifyMultipleSharedSubnets(t, precreatedSubnetNs2, 2, "shared-subnet", "shared-subnet-2")
	log.Info("Multiple shared subnet verification complete", "ns2_subnet1", sharedSubnets[0].Name, "ns2_subnet2", sharedSubnets[1].Name)
}

// findSubnetByNamePrefix finds a subnet with the given name prefix in a list of subnets
func findSubnetByNamePrefix(subnets []*v1alpha1.Subnet, namePrefix string) *v1alpha1.Subnet {
	for _, subnet := range subnets {
		if strings.HasPrefix(subnet.Name, namePrefix) {
			return subnet
		}
	}
	return nil
}

// getOppositeConnectivityState returns the opposite connectivity state
func getOppositeConnectivityState(currentState v1alpha1.ConnectivityState) string {
	if currentState == v1alpha1.ConnectivityStateConnected {
		return "DISCONNECTED"
	}
	return "CONNECTED"
}

// convertToConnectivityState converts a string state to v1alpha1.ConnectivityState
func convertToConnectivityState(state string) v1alpha1.ConnectivityState {
	if state == "CONNECTED" {
		return v1alpha1.ConnectivityStateConnected
	}
	return v1alpha1.ConnectivityStateDisconnected
}

// waitForConnectivityStateUpdate waits for a subnet's connectivity state to update
func waitForConnectivityStateUpdate(t *testing.T, namespace, subnetName, expectedStateStr string) *v1alpha1.Subnet {
	expectedState := convertToConnectivityState(expectedStateStr)

	log.Info("Waiting for subnet connectivity state to update", "subnet", subnetName, "expected", expectedStateStr)
	err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 12*time.Minute, false, func(ctx context.Context) (bool, error) {
		updatedSubnet, err := testData.crdClientset.CrdV1alpha1().Subnets(namespace).Get(context.TODO(), subnetName, v1.GetOptions{})
		if err != nil {
			return false, err
		}

		log.Info("Current connectivity state", "subnet", updatedSubnet.Name, "state", updatedSubnet.Spec.AdvancedConfig.ConnectivityState)
		return updatedSubnet.Spec.AdvancedConfig.ConnectivityState == expectedState, nil
	})
	require.NoError(t, err, "Failed to update connectivity state for subnet %s", subnetName)

	// Get the final updated subnet
	updatedSubnet, err := testData.crdClientset.CrdV1alpha1().Subnets(namespace).Get(context.TODO(), subnetName, v1.GetOptions{})
	require.NoError(t, err)

	require.Equal(t, expectedState, updatedSubnet.Spec.AdvancedConfig.ConnectivityState,
		"Subnet %s connectivity state should be %s, got %s",
		subnetName, expectedState, updatedSubnet.Spec.AdvancedConfig.ConnectivityState)

	return updatedSubnet
}

// PrecreatedSharedSubnetPoll tests if nsx-operator subnet_poll can update a shared subnet
func PrecreatedSharedSubnetPoll(t *testing.T) {
	// Find the shared-subnet-2 in precreatedSubnetNs2
	sharedSubnets := verifyMultipleSharedSubnets(t, precreatedSubnetNs2, 2, "shared-subnet", "shared-subnet-2")
	// Find the shared-subnet-2 instance
	subnet2 := findSubnetByNamePrefix(sharedSubnets, "shared-subnet-2")
	require.NotNil(t, subnet2, "Could not find shared-subnet-2 in namespace %s", precreatedSubnetNs2)
	// Check the initial connectivity state and determine the new state
	initialState := subnet2.Spec.AdvancedConfig.ConnectivityState
	newState := getOppositeConnectivityState(initialState)
	log.Info("Initial connectivity state", "subnet", subnet2.Name, "state", initialState)
	// Update the subnet's connectivity state
	updateSubnetConnectivityState(t, subnet2, newState)
	// Wait for and verify the connectivity state update
	updatedSubnet := waitForConnectivityStateUpdate(t, precreatedSubnetNs2, subnet2.Name, newState)
	log.Info("Subnet connectivity state updated successfully",
		"subnet", updatedSubnet.Name,
		"initialState", initialState,
		"newState", updatedSubnet.Spec.AdvancedConfig.ConnectivityState)
}

// PrecreatedSharedSubnetDeleteFail tests that attempting to delete a shared subnet fails
func PrecreatedSharedSubnetDeleteFail(t *testing.T) {
	// Find the shared subnets in precreatedSubnetNs2
	sharedSubnets := verifyMultipleSharedSubnets(t, precreatedSubnetNs2, 2, "shared-subnet")
	// Find any shared subnet instance (use the second one if available)
	var subnet2 *v1alpha1.Subnet
	if len(sharedSubnets) >= 2 {
		subnet2 = sharedSubnets[1]
	} else {
		subnet2 = sharedSubnets[0]
	}
	require.NotNil(t, subnet2, "Could not find a shared subnet in namespace %s", precreatedSubnetNs2)
	log.Info("Attempting to delete shared subnet", "subnet", subnet2.Name)
	// Attempt to delete the subnet
	err := testData.crdClientset.CrdV1alpha1().Subnets(precreatedSubnetNs2).Delete(context.TODO(), subnet2.Name, v1.DeleteOptions{})
	// Verify that the deletion fails with a specific error
	require.Error(t, err, "Deleting shared subnet %s should fail", subnet2.Name)
	// Verify the subnet still exists
	stillExists := false
	err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		_, err := testData.crdClientset.CrdV1alpha1().Subnets(precreatedSubnetNs2).Get(context.TODO(), subnet2.Name, v1.GetOptions{})
		if err == nil {
			stillExists = true
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err, "Error while checking if subnet still exists")
	require.True(t, stillExists, "Shared subnet %s should still exist after deletion attempt", subnet2.Name)
	log.Info("Verified that shared subnet cannot be deleted", "subnet", subnet2.Name)
}

// PrecreatedSharedSubnetUpdateFail tests that attempting to update vpcName or vlanConnection in a shared subnet fails
func PrecreatedSharedSubnetUpdateFail(t *testing.T) {
	// Find the shared subnets in precreatedSubnetNs2
	sharedSubnets := verifyMultipleSharedSubnets(t, precreatedSubnetNs2, 2, "shared-subnet")
	// Find any shared subnet instance (use the second one if available)
	var subnet2 *v1alpha1.Subnet
	if len(sharedSubnets) >= 2 {
		subnet2 = sharedSubnets[1]
	} else {
		subnet2 = sharedSubnets[0]
	}
	require.NotNil(t, subnet2, "Could not find a shared subnet in namespace %s", precreatedSubnetNs2)

	// Save original values for verification later
	originalVPCName := subnet2.Spec.VPCName
	originalVLANConnectionName := subnet2.Spec.VLANConnectionName

	log.Info("Attempting to update VPCName in shared subnet", "subnet", subnet2.Name)

	// First try to update VPCName
	updatedSubnet := subnet2.DeepCopy()
	updatedSubnet.Spec.VPCName = "new-vpc-name"
	_, err := testData.crdClientset.CrdV1alpha1().Subnets(precreatedSubnetNs2).Update(context.TODO(), updatedSubnet, v1.UpdateOptions{})

	// Verify that the update fails with a specific error
	require.Error(t, err, "Updating VPCName in shared subnet %s should fail", subnet2.Name)
	require.Contains(t, err.Error(), "vpcName is immutable", "Error message should indicate that VPCName is immutable")

	// Now try to update VLANConnection
	log.Info("Attempting to update VLANConnection in shared subnet", "subnet", subnet2.Name)

	// Get the latest version of the subnet
	latestSubnet, err := testData.crdClientset.CrdV1alpha1().Subnets(precreatedSubnetNs2).Get(context.TODO(), subnet2.Name, v1.GetOptions{})
	require.NoError(t, err, "Failed to get latest version of subnet %s", subnet2.Name)

	updatedSubnet = latestSubnet.DeepCopy()
	updatedSubnet.Spec.VLANConnectionName = "faked-vlan-connection-path"
	_, err = testData.crdClientset.CrdV1alpha1().Subnets(precreatedSubnetNs2).Update(context.TODO(), updatedSubnet, v1.UpdateOptions{})

	// Verify that the update fails or is denied
	require.Contains(t, err.Error(), "denied", "Error message should indicate that the update is denied")

	// Final verification that the subnet properties remain unchanged
	finalSubnet, err := testData.crdClientset.CrdV1alpha1().Subnets(precreatedSubnetNs2).Get(context.TODO(), subnet2.Name, v1.GetOptions{})
	require.NoError(t, err, "Failed to get final subnet %s", subnet2.Name)
	require.Equal(t, originalVPCName, finalSubnet.Spec.VPCName, "VPCName should remain unchanged")
	require.Equal(t, originalVLANConnectionName, finalSubnet.Spec.VLANConnectionName,
		"VLANConnection should remain unchanged")

	log.Info("Verified that shared subnet properties cannot be updated", "subnet", subnet2.Name)
}

// NormalSubnetManagedByNSXOp tests that when a namespace user creates a normal Subnet CR in the namespace,
// its status.shared is false, and when getting the subnet from NSX, its tags contain nsx/managed-by:nsx-op
func NormalSubnetManagedByNSXOp(t *testing.T) {
	// Create a normal subnet in the namespace
	normalSubnet := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "normal-subnet",
			Namespace: precreatedSubnetNs1,
		},
		Spec: v1alpha1.SubnetSpec{
			AccessMode: v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
			SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
				Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated),
			},
		},
	}

	// Create the subnet and verify it's created successfully
	createdSubnet := createSubnetWithCheck(t, normalSubnet)
	require.NotNil(t, createdSubnet, "Failed to create normal subnet in namespace %s", precreatedSubnetNs1)

	// Verify that status.shared is false
	require.False(t, createdSubnet.Status.Shared, "Normal subnet should have status.shared=false")
	log.Info("Verified normal subnet has status.shared=false", "subnet", createdSubnet.Name)

	// Get the subnet from NSX using the subnet UID
	subnetCRUID := string(createdSubnet.UID)
	nsxSubnets := testData.fetchSubnetBySubnetUID(t, subnetCRUID)
	require.Equal(t, 1, len(nsxSubnets), "Expected to find exactly one NSX subnet for the created subnet")

	// Check that the NSX subnet has the nsx/managed-by:nsx-op tag
	nsxSubnet := nsxSubnets[0]
	found := false
	for _, tag := range nsxSubnet.Tags {
		if *tag.Scope == common.TagScopeManagedBy && *tag.Tag == common.AutoCreatedTagValue {
			found = true
			break
		}
	}
	require.True(t, found, "NSX subnet should have the tag %s:%s", common.TagScopeManagedBy, common.AutoCreatedTagValue)
	log.Info("Verified NSX subnet has the correct managed-by tag", "subnet", createdSubnet.Name)

	// Clean up - delete the subnet
	err := testData.crdClientset.CrdV1alpha1().Subnets(precreatedSubnetNs1).Delete(context.TODO(), normalSubnet.Name, v1.DeleteOptions{})
	require.NoError(t, err, "Failed to delete normal subnet %s", normalSubnet.Name)

	// Wait for the NSX subnet to be deleted
	err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		nsxSubnets := testData.fetchSubnetBySubnetUID(t, subnetCRUID)
		return len(nsxSubnets) == 0, nil
	})
	require.NoError(t, err, "Timed out waiting for subnet to be deleted")
	log.Info("Successfully deleted normal subnet", "subnet", normalSubnet.Name)
}

// SubnetWithAssociatedResourceAnnotation tests that creating a normal SubnetCR with
// nsx.vmware.com/associated-resource annotation should be refused
func SubnetWithAssociatedResourceAnnotation(t *testing.T) {
	// Create a subnet with the associated-resource annotation
	subnetWithAnnotation := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-with-annotation",
			Namespace: precreatedSubnetNs1,
			Annotations: map[string]string{
				common.AnnotationAssociatedResource: "project1:vpc1:subnet1",
			},
		},
		Spec: v1alpha1.SubnetSpec{
			AccessMode: v1alpha1.AccessMode(v1alpha1.AccessModePrivate),
			AdvancedConfig: v1alpha1.SubnetAdvancedConfig{
				StaticIPAllocation: v1alpha1.StaticIPAllocation{
					Enabled: common.Bool(true),
				},
			},
			SubnetDHCPConfig: v1alpha1.SubnetDHCPConfig{
				Mode: v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated),
			},
		},
	}

	// Attempt to create the subnet and verify it's refused
	_, err := testData.crdClientset.CrdV1alpha1().Subnets(precreatedSubnetNs1).Create(context.TODO(), subnetWithAnnotation, v1.CreateOptions{})

	// Verify that the creation is refused with an appropriate error message
	require.Error(t, err, "Creating subnet with associated-resource annotation should fail")
	require.Contains(t, err.Error(), "denied", "Error message should mention the denied")

	log.Info("Verified that creating a subnet with associated-resource annotation is refused", "error", err.Error())
}
