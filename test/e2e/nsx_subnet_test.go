package e2e

import (
	"context"
	"log"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	subnetSetCRType           = "subnetsets.crd.nsx.vmware.com"
	subnetPortCRType          = "subnetports.crd.nsx.vmware.com"
	subnetTestNamespace       = "subnet-e2e"
	subnetTestNamespaceShared = "subnet-e2e-shared"
	subnetTestNamespaceTarget = "target-ns"
	vpcNetworkConfigCRName    = "default"
	// subnetDeletionTimeout requires a bigger value than defaultTimeout, it's because that it takes some time for NSX to
	// recycle allocated IP addresses and NSX VPCSubnet won't be deleted until all IP addresses have been recycled.
	subnetDeletionTimeout = 600 * time.Second
)

func verifySubnetSetCR(subnetSet string) bool {
	vpcNetworkConfig, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(context.TODO(), vpcNetworkConfigCRName,
		v1.GetOptions{})
	if err != nil {
		log.Printf("Failed to get VPCNetworkConfiguration %s: %v", vpcNetworkConfigCRName, err)
		return false
	}
	subnetSetCR, err := testData.crdClientset.CrdV1alpha1().SubnetSets(subnetTestNamespace).Get(context.TODO(), subnetSet, v1.GetOptions{})
	if err != nil {
		log.Printf("Failed to get %s/%s: %s", subnetTestNamespace, subnetSet, err)
		return false
	}

	if subnetSetCR.Spec.IPv4SubnetSize != vpcNetworkConfig.Spec.DefaultSubnetSize {
		log.Printf("IPv4SubnetSize is %d, while it's expected to be %d", subnetSetCR.Spec.IPv4SubnetSize, vpcNetworkConfig.Spec.DefaultSubnetSize)
		return false
	}
	return true
}

func TestSubnetSet(t *testing.T) {
	setupTest(t, subnetTestNamespace)
	nsPath, _ := filepath.Abs("./manifest/testSubnet/shared_ns.yaml")
	err := applyYAML(nsPath, "")
	assert.NoError(t, err)

	t.Cleanup(func() {
		teardownTest(t, subnetTestNamespace, subnetDeletionTimeout)
		teardownTest(t, subnetTestNamespaceShared, subnetDeletionTimeout)
		teardownTest(t, subnetTestNamespaceTarget, subnetDeletionTimeout)
	})

	t.Run("case=DefaultSubnetSet", defaultSubnetSet)
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
			log.Printf("Failed to convert to golang subnet: %v", err)
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
	assert.NoError(t, err)
	subnets := transSearchResponsetoSubnet(results)
	assert.True(t, len(subnets) > 0, "No NSX subnet found")
	return subnets[0]
}

func defaultSubnetSet(t *testing.T) {
	// 1. Check whether default-vm-subnetset and default-pod-subnetset are created.
	err := testData.waitForCRReadyOrDeleted(defaultTimeout, subnetSetCRType, subnetTestNamespace, common.DefaultVMSubnetSet, Ready)
	assert.NoError(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, subnetSetCRType, subnetTestNamespace, common.DefaultPodSubnetSet, Ready)
	assert.NoError(t, err)

	// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
	assert.True(t, verifySubnetSetCR(common.DefaultVMSubnetSet))
	assert.True(t, verifySubnetSetCR(common.DefaultPodSubnetSet))

	portPath, _ := filepath.Abs("./manifest/testSubnet/subnetport_1.yaml")
	err = applyYAML(portPath, subnetTestNamespace)
	assert.NoError(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, subnetPortCRType, subnetTestNamespace, "port-e2e-test-1", Ready)
	assert.NoError(t, err)
	defer deleteYAML(portPath, subnetTestNamespace)

	// 3. Check SubnetSet CR status should be updated with NSX subnet info.
	subnetSet, err := testData.crdClientset.CrdV1alpha1().SubnetSets(subnetTestNamespace).Get(context.TODO(), common.DefaultPodSubnetSet, v1.GetOptions{})
	assert.NoError(t, err)
	assert.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")
	// 4. Check NSX subnet allocation.
	networkAddress := subnetSet.Status.Subnets[0].NetworkAddresses
	assert.True(t, len(networkAddress) > 0, "No network address in SubnetSet")

	// 5. Check adding NSX subnet tags.
	ns, err := testData.clientset.CoreV1().Namespaces().Get(context.TODO(), subnetTestNamespace, v1.GetOptions{})
	assert.NoError(t, err)
	labelKey, labelValue := "subnet-e2e", "add"
	ns.Labels[labelKey] = labelValue
	_, err = testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	assert.NoError(t, err)

	vpcSubnet := fetchSubnetBySubnetSet(t, subnetSet)
	found := false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey && *tag.Tag == labelValue {
			found = true
			break
		}
	}
	assert.True(t, found, "Failed to add tags for NSX subnet %s", *(vpcSubnet.Id))

	// 6. Check updating NSX subnet tags.
	ns, err = testData.clientset.CoreV1().Namespaces().Get(context.TODO(), subnetTestNamespace, v1.GetOptions{})
	assert.NoError(t, err)
	labelValue = "update"
	ns.Labels[labelKey] = labelValue
	_, err = testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	assert.NoError(t, err)
	vpcSubnet = fetchSubnetBySubnetSet(t, subnetSet)
	found = false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey && *tag.Tag == labelValue {
			found = true
			break
		}
	}
	assert.True(t, found, "Failed to update tags for NSX subnet %s", *(vpcSubnet.Id))

	// 7. Check deleting NSX subnet tags.
	ns, err = testData.clientset.CoreV1().Namespaces().Get(context.TODO(), subnetTestNamespace, v1.GetOptions{})
	assert.NoError(t, err)
	delete(ns.Labels, labelKey)
	newNs, err := testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	assert.NoError(t, err)
	t.Logf("new Namespace: %+v", newNs)
	vpcSubnet = fetchSubnetBySubnetSet(t, subnetSet)
	found = false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey {
			found = true
			break
		}
	}
	assert.False(t, found, "Failed to delete tags for NSX subnet %s", *(vpcSubnet.Id))
}

func UserSubnetSet(t *testing.T) {
	subnetSetYAMLs := []string{
		"./manifest/testSubnet/subnetset-static.yaml",
		"./manifest/testSubnet/subnetset-dhcp.yaml",
	}
	subnetSetNames := []string{
		"user-pod-subnetset-static",
		"user-pod-subnetset-dhcp",
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
		err := applyYAML(subnetSetPath, subnetTestNamespace)
		assert.NoError(t, err)
		err = testData.waitForCRReadyOrDeleted(defaultTimeout, subnetSetCRType, subnetTestNamespace, subnetSetName, Ready)
		assert.NoError(t, err)

		// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
		assert.True(t, verifySubnetSetCR(subnetSetName))

		portPath, _ := filepath.Abs(portYAML)
		err = applyYAML(portPath, subnetTestNamespace)
		assert.NoError(t, err)
		err = testData.waitForCRReadyOrDeleted(defaultTimeout, subnetPortCRType, subnetTestNamespace, portName, Ready)
		assert.NoError(t, err)
		defer deleteYAML(portPath, subnetTestNamespace)

		// 3. Check SubnetSet CR status should be updated with NSX subnet info.
		subnetSet, err := testData.crdClientset.CrdV1alpha1().SubnetSets(subnetTestNamespace).Get(context.TODO(), subnetSetName, v1.GetOptions{})
		assert.NoError(t, err)
		assert.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")

		// 4. Check IP address is (not) allocated to SubnetPort.
		err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 100*time.Second, false, func(ctx context.Context) (bool, error) {
			port, err := testData.crdClientset.CrdV1alpha1().SubnetPorts(subnetTestNamespace).Get(context.TODO(), portName, v1.GetOptions{})
			if err != nil {
				return false, err
			}
			if port == nil || len(port.Status.NetworkInterfaceConfig.IPAddresses) == 0 {
				return false, nil
			}
			if portName == "port-in-static-subnetset" {
				if port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress != "" {
					return true, nil
				}
				// assert.NotEmpty(t, port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "No IP address in SubnetPort")
			} else if portName == "port-in-dhcp-subnetset" {
				assert.Empty(t, port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "DHCP port shouldn't have IP Address")
				if port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress == "" {
					return true, nil
				}
			}
			return false, nil
		})
		assert.NoError(t, err)

		// 5. Check NSX subnet allocation.
		networkaddress := subnetSet.Status.Subnets[0].NetworkAddresses
		assert.True(t, len(networkaddress) > 0, "No network address in SubnetSet")
	}
}

func sharedSubnetSet(t *testing.T) {
	// 1. Check whether default-vm-subnetset and default-pod-subnetset are created.
	err := testData.waitForCRReadyOrDeleted(defaultTimeout, subnetSetCRType, subnetTestNamespaceTarget, common.DefaultVMSubnetSet, Ready)
	assert.NoError(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, subnetSetCRType, subnetTestNamespaceTarget, common.DefaultPodSubnetSet, Ready)
	assert.NoError(t, err)

	// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
	assert.True(t, verifySubnetSetCR(common.DefaultVMSubnetSet))
	assert.True(t, verifySubnetSetCR(common.DefaultPodSubnetSet))

	portPath, _ := filepath.Abs("./manifest/testSubnet/subnetport_3.yaml")
	err = applyYAML(portPath, subnetTestNamespaceShared)
	assert.NoError(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, subnetPortCRType, subnetTestNamespaceShared, "port-e2e-test-3", Ready)
	assert.NoError(t, err)
	defer deleteYAML(portPath, subnetTestNamespaceShared)

	// 3. Check SubnetSet CR status should be updated with NSX subnet info.
	subnetSet, err := testData.crdClientset.CrdV1alpha1().SubnetSets(subnetTestNamespaceTarget).Get(context.TODO(), common.DefaultVMSubnetSet, v1.GetOptions{})
	assert.NoError(t, err)
	assert.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")

	// 4. Check IP address is allocated to SubnetPort.
	port, err := testData.crdClientset.CrdV1alpha1().SubnetPorts(subnetTestNamespaceShared).Get(context.TODO(), "port-e2e-test-3", v1.GetOptions{})
	assert.NoError(t, err)
	assert.NotEmpty(t, port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "No IP address in SubnetPort")

	// 5. Check Subnet CIDR contains SubnetPort IP.

	portIP := net.ParseIP(strings.Split(port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "/")[0])
	_, subnetCIDR, err := net.ParseCIDR(subnetSet.Status.Subnets[0].NetworkAddresses[0])
	assert.NoError(t, err)
	assert.True(t, subnetCIDR.Contains(portIP))
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
	_, err := testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Create(context.TODO(), subnet, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		err = nil
	}
	assert.NoError(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, "subnets.crd.nsx.vmware.com", subnetTestNamespace, subnet.Name, Ready)
	assert.NoError(t, err)
	allocatedSubnet, err := testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
	assert.NoError(t, err)
	nsxSubnets := testData.fetchSubnetByNamespace(t, subnetTestNamespace, false)
	assert.Equal(t, 1, len(nsxSubnets))

	targetCIDR := allocatedSubnet.Status.NetworkAddresses[0]
	err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Delete(context.TODO(), subnet.Name, v1.DeleteOptions{})
	assert.NoError(t, err)

	err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 100*time.Second, false, func(ctx context.Context) (bool, error) {
		_, err := testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
		if err != nil && errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	assert.NoError(t, err)
	nsxSubnets = testData.fetchSubnetByNamespace(t, subnetTestNamespace, true)
	assert.Equal(t, true, len(nsxSubnets) <= 1)

	subnet.Spec.IPAddresses = []string{targetCIDR}
	_, err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Create(context.TODO(), subnet, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		t.Logf("Create Subnet error: %+v", err)
		err = nil
	}
	assert.NoError(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout*2, "subnets.crd.nsx.vmware.com", subnetTestNamespace, subnet.Name, Ready)
	assert.NoError(t, err)
	allocatedSubnet, err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, targetCIDR, allocatedSubnet.Status.NetworkAddresses[0])

	nsxSubnets = testData.fetchSubnetByNamespace(t, subnetTestNamespace, false)
	assert.Equal(t, 1, len(nsxSubnets))

	err = testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Delete(context.TODO(), subnet.Name, v1.DeleteOptions{})
	assert.NoError(t, err)

	err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 100*time.Second, false, func(ctx context.Context) (bool, error) {
		_, err := testData.crdClientset.CrdV1alpha1().Subnets(subnetTestNamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
		if err != nil && errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	assert.NoError(t, err)

	nsxSubnets = testData.fetchSubnetByNamespace(t, subnetTestNamespace, true)
	assert.Equal(t, true, len(nsxSubnets) <= 1)
}

func (data *TestData) fetchSubnetByNamespace(t *testing.T, ns string, isMarkForDelete bool) (res []model.VpcSubnet) {
	tags := []string{common.TagScopeNamespace, ns}
	results, err := testData.queryResource(common.ResourceTypeSubnet, tags)
	assert.NoError(t, err)
	subnets := transSearchResponsetoSubnet(results)
	for _, subnet := range subnets {
		if *subnet.MarkedForDelete == isMarkForDelete {
			res = append(res, subnet)
		}
	}
	return
}
