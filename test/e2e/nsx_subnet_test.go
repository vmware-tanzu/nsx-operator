package e2e

import (
	"context"
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
	subnetTestNamespace       = "subnet-e2e"
	subnetTestNamespaceShared = "subnet-e2e-shared"
	subnetTestNamespaceTarget = "target-ns"
	vpcNetworkConfigCRName    = "default"
	// subnetDeletionTimeout requires a bigger value than defaultTimeout, it's because that it takes some time for NSX to
	// recycle allocated IP addresses and NSX VPCSubnet won't be deleted until all IP addresses have been recycled.
	subnetDeletionTimeout = 600 * time.Second
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
	nsPath, _ := filepath.Abs("./manifest/testSubnet/shared_ns.yaml")
	require.NoError(t, applyYAML(nsPath, ""))

	t.Cleanup(func() {
		teardownTest(t, subnetTestNamespace, subnetDeletionTimeout)
		teardownTest(t, subnetTestNamespaceShared, subnetDeletionTimeout)
		teardownTest(t, subnetTestNamespaceTarget, subnetDeletionTimeout)
	})

	t.Run("case=DefaultSubnetSet", defaultSubnetSet)
	// TODO: Subnet test with DHCP enable required to update service profile after
	// upgrade to new NSX which supports subnetDHCPConfig
	t.Run("case=UserSubnetSet", UserSubnetSet)
	t.Run("case=SharedSubnetSet", sharedSubnetSet)
	t.Run("case=SubnetCIDR", SubnetCIDR)
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

func defaultSubnetSet(t *testing.T) {
	// 1. Check whether default-vm-subnetset and default-pod-subnetset are created.
	assureSubnetSet(t, subnetTestNamespace, common.DefaultVMSubnetSet)
	assureSubnetSet(t, subnetTestNamespace, common.DefaultPodSubnetSet)

	// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
	require.True(t, verifySubnetSetCR(common.DefaultVMSubnetSet))
	require.True(t, verifySubnetSetCR(common.DefaultPodSubnetSet))

	portPath, _ := filepath.Abs("./manifest/testSubnet/subnetport_1.yaml")
	require.NoError(t, applyYAML(portPath, subnetTestNamespace))
	assureSubnetPort(t, subnetTestNamespace, "port-e2e-test-1")
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
	log.V(2).Info("New Namespace", "Namespace", newNs)
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
		assureSubnetPort(t, subnetTestNamespace, portName)

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
			log.V(2).Info("Check IP address", "IPAddress", port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "portName", portName)
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

	// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
	require.True(t, verifySubnetSetCR(common.DefaultVMSubnetSet))
	require.True(t, verifySubnetSetCR(common.DefaultPodSubnetSet))

	portPath, _ := filepath.Abs("./manifest/testSubnet/subnetport_3.yaml")
	require.NoError(t, applyYAML(portPath, subnetTestNamespaceShared))

	assureSubnetPort(t, subnetTestNamespaceShared, "port-e2e-test-3")
	defer deleteYAML(portPath, subnetTestNamespaceShared)

	// 3. Check SubnetSet CR status should be updated with NSX Subnet info.
	subnetSet, err := testData.crdClientset.CrdV1alpha1().SubnetSets(subnetTestNamespaceTarget).Get(context.TODO(), common.DefaultVMSubnetSet, v1.GetOptions{})
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

	// Change the DHCP mode from DHCPServer to DHCPDeactived
	allocatedSubnet.Spec.SubnetDHCPConfig.Mode = v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeDeactivated)
	_, err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Update(context.TODO(), allocatedSubnet, v1.UpdateOptions{})
	require.NoError(t, err)
	allocatedSubnet = assureSubnet(t, subnetTestNamespace, subnet.Name, v1alpha1.DHCPConfigModeDeactivated)
	nsxSubnets = testData.fetchSubnetBySubnetUID(t, newSubnetCRUID)
	require.Equal(t, 1, len(nsxSubnets))
	require.Equal(t, "DHCP_DEACTIVATED", *nsxSubnets[0].SubnetDhcpConfig.Mode)
	require.Equal(t, true, *nsxSubnets[0].AdvancedConfig.StaticIpAllocation.Enabled)

	// Change the DHCP mode from DHCPDeactived to DHCPServer
	allocatedSubnet.Spec.SubnetDHCPConfig.Mode = v1alpha1.DHCPConfigMode(v1alpha1.DHCPConfigModeServer)
	_, err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Update(context.TODO(), allocatedSubnet, v1.UpdateOptions{})
	require.Contains(t, err.Error(), "subnetDHCPConfig cannot switch from DHCPDeactivated to other modes")

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
		log.V(2).Info("Subnet status", "status", res.Status)
		for _, con := range res.Status.Conditions {
			if con.Type == v1alpha1.Ready && con.Status == corev1.ConditionTrue && strings.Contains(con.Message, conditionMsg) {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err)
	return
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
		log.V(2).Info("SubnetSets status", "status", res.Status)
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

func assureSubnetPort(t *testing.T, ns, subnetPortName string) (res *v1alpha1.SubnetPort) {
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
		log.V(2).Info("SubnetPort status", "status", res.Status)
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
