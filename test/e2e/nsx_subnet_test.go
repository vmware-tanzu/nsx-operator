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
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	SubnetSetCRType        = "subnetsets.crd.nsx.vmware.com"
	SubnetPortCRType       = "subnetports.crd.nsx.vmware.com"
	E2ENamespace           = "subnet-e2e"
	E2ENamespaceShared     = "subnet-e2e-shared"
	E2ENamespaceTarget     = "target-ns"
	VPCNetworkConfigCRName = "default"
	// SubnetDeletionTimeout requires a bigger value than defaultTimeout, it's because that it takes some time for NSX to
	// recycle allocated IP addresses and NSX VPCSubnet won't be deleted until all IP addresses have been recycled.
	SubnetDeletionTimeout = 600 * time.Second
)

func verifySubnetSetCR(subnetSet string) bool {
	vpcNetworkConfig, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(context.TODO(), VPCNetworkConfigCRName,
		v1.GetOptions{})
	if err != nil {
		log.Printf("Failed to get VPCNetworkConfiguration %s: %v", VPCNetworkConfigCRName, err)
		return false
	}
	subnetSetCR, err := testData.crdClientset.CrdV1alpha1().SubnetSets(E2ENamespace).Get(context.TODO(), subnetSet, v1.GetOptions{})
	if err != nil {
		log.Printf("Failed to get %s/%s: %s", E2ENamespace, subnetSet, err)
		return false
	}

	if subnetSetCR.Spec.IPv4SubnetSize != vpcNetworkConfig.Spec.DefaultSubnetSize {
		log.Printf("IPv4SubnetSize is %d, while it's expected to be %d", subnetSetCR.Spec.IPv4SubnetSize, vpcNetworkConfig.Spec.DefaultSubnetSize)
		return false
	}
	return true
}

func TestSubnetSet(t *testing.T) {
	setupTest(t, E2ENamespace)
	nsPath, _ := filepath.Abs("./manifest/testSubnet/shared_ns.yaml")
	err := applyYAML(nsPath, "")
	assertNil(t, err)

	t.Cleanup(func() {
		teardownTest(t, E2ENamespace, SubnetDeletionTimeout)
		teardownTest(t, E2ENamespaceShared, SubnetDeletionTimeout)
		teardownTest(t, E2ENamespaceTarget, SubnetDeletionTimeout)
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
	assertNil(t, err)
	subnets := transSearchResponsetoSubnet(results)
	assertTrue(t, len(subnets) > 0, "No NSX subnet found")
	return subnets[0]
}

func defaultSubnetSet(t *testing.T) {
	// 1. Check whether default-vm-subnetset and default-pod-subnetset are created.
	err := testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetSetCRType, E2ENamespace, common.DefaultVMSubnetSet, Ready)
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetSetCRType, E2ENamespace, common.DefaultPodSubnetSet, Ready)
	assertNil(t, err)

	// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
	assertTrue(t, verifySubnetSetCR(common.DefaultVMSubnetSet))
	assertTrue(t, verifySubnetSetCR(common.DefaultPodSubnetSet))

	portPath, _ := filepath.Abs("./manifest/testSubnet/subnetport_1.yaml")
	err = applyYAML(portPath, E2ENamespace)
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetPortCRType, E2ENamespace, "port-e2e-test-1", Ready)
	assertNil(t, err)
	defer deleteYAML(portPath, E2ENamespace)

	// 3. Check SubnetSet CR status should be updated with NSX subnet info.
	subnetSet, err := testData.crdClientset.CrdV1alpha1().SubnetSets(E2ENamespace).Get(context.TODO(), common.DefaultPodSubnetSet, v1.GetOptions{})
	assertNil(t, err)
	assert.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")
	// 4. Check NSX subnet allocation.
	networkAddress := subnetSet.Status.Subnets[0].NetworkAddresses
	assertTrue(t, len(networkAddress) > 0, "No network address in SubnetSet")

	// 5. Check adding NSX subnet tags.
	ns, err := testData.clientset.CoreV1().Namespaces().Get(context.TODO(), E2ENamespace, v1.GetOptions{})
	assertNil(t, err)
	labelKey, labelValue := "subnet-e2e", "add"
	ns.Labels[labelKey] = labelValue
	_, err = testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	assertNil(t, err)

	vpcSubnet := fetchSubnetBySubnetSet(t, subnetSet)
	found := false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey && *tag.Tag == labelValue {
			found = true
			break
		}
	}
	assertTrue(t, found, "Failed to add tags for NSX subnet %s", *(vpcSubnet.Id))

	// 6. Check updating NSX subnet tags.
	ns, err = testData.clientset.CoreV1().Namespaces().Get(context.TODO(), E2ENamespace, v1.GetOptions{})
	assertNil(t, err)
	labelValue = "update"
	ns.Labels[labelKey] = labelValue
	_, err = testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	assertNil(t, err)
	vpcSubnet = fetchSubnetBySubnetSet(t, subnetSet)
	found = false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey && *tag.Tag == labelValue {
			found = true
			break
		}
	}
	assertTrue(t, found, "Failed to update tags for NSX subnet %s", *(vpcSubnet.Id))

	// 7. Check deleting NSX subnet tags.
	ns, err = testData.clientset.CoreV1().Namespaces().Get(context.TODO(), E2ENamespace, v1.GetOptions{})
	assertNil(t, err)
	delete(ns.Labels, labelKey)
	newNs, err := testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	assertNil(t, err)
	t.Logf("new Namespace: %+v", newNs)
	vpcSubnet = fetchSubnetBySubnetSet(t, subnetSet)
	found = false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey {
			found = true
			break
		}
	}
	assertFalse(t, found, "Failed to delete tags for NSX subnet %s", *(vpcSubnet.Id))
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
		err := applyYAML(subnetSetPath, E2ENamespace)
		assertNil(t, err)
		err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetSetCRType, E2ENamespace, subnetSetName, Ready)
		assertNil(t, err)

		// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
		assertTrue(t, verifySubnetSetCR(subnetSetName))

		portPath, _ := filepath.Abs(portYAML)
		err = applyYAML(portPath, E2ENamespace)
		assertNil(t, err)
		err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetPortCRType, E2ENamespace, portName, Ready)
		assertNil(t, err)
		defer deleteYAML(portPath, E2ENamespace)

		// 3. Check SubnetSet CR status should be updated with NSX subnet info.
		subnetSet, err := testData.crdClientset.CrdV1alpha1().SubnetSets(E2ENamespace).Get(context.TODO(), subnetSetName, v1.GetOptions{})
		assertNil(t, err)
		assert.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")

		// 4. Check IP address is (not) allocated to SubnetPort.
		port, err := testData.crdClientset.CrdV1alpha1().SubnetPorts(E2ENamespace).Get(context.TODO(), portName, v1.GetOptions{})
		assertNil(t, err)
		if portName == "port-in-static-subnetset" {
			assert.NotEmpty(t, port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "No IP address in SubnetPort")
		} else if portName == "port-in-dhcp-subnetset" {
			assert.Empty(t, port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "DHCP port shouldn't have IP Address")
		}

		// 5. Check NSX subnet allocation.
		networkaddress := subnetSet.Status.Subnets[0].NetworkAddresses
		assertTrue(t, len(networkaddress) > 0, "No network address in SubnetSet")
	}
}

func sharedSubnetSet(t *testing.T) {
	// 1. Check whether default-vm-subnetset and default-pod-subnetset are created.
	err := testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetSetCRType, E2ENamespaceTarget, common.DefaultVMSubnetSet, Ready)
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetSetCRType, E2ENamespaceTarget, common.DefaultPodSubnetSet, Ready)
	assertNil(t, err)

	// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
	assertTrue(t, verifySubnetSetCR(common.DefaultVMSubnetSet))
	assertTrue(t, verifySubnetSetCR(common.DefaultPodSubnetSet))

	portPath, _ := filepath.Abs("./manifest/testSubnet/subnetport_3.yaml")
	err = applyYAML(portPath, E2ENamespaceShared)
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetPortCRType, E2ENamespaceShared, "port-e2e-test-3", Ready)
	assertNil(t, err)
	defer deleteYAML(portPath, E2ENamespaceShared)

	// 3. Check SubnetSet CR status should be updated with NSX subnet info.
	subnetSet, err := testData.crdClientset.CrdV1alpha1().SubnetSets(E2ENamespaceTarget).Get(context.TODO(), common.DefaultVMSubnetSet, v1.GetOptions{})
	assertNil(t, err)
	assert.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")

	// 4. Check IP address is allocated to SubnetPort.
	port, err := testData.crdClientset.CrdV1alpha1().SubnetPorts(E2ENamespaceShared).Get(context.TODO(), "port-e2e-test-3", v1.GetOptions{})
	assertNil(t, err)
	assert.NotEmpty(t, port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "No IP address in SubnetPort")

	// 5. Check Subnet CIDR contains SubnetPort IP.

	portIP := net.ParseIP(strings.Split(port.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress, "/")[0])
	_, subnetCIDR, err := net.ParseCIDR(subnetSet.Status.Subnets[0].NetworkAddresses[0])
	assertNil(t, err)
	assertTrue(t, subnetCIDR.Contains(portIP))
}

func SubnetCIDR(t *testing.T) {
	subnet := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			Name:      "subnet-dhcp-cidr",
			Namespace: E2ENamespace,
		},
		Spec: v1alpha1.SubnetSpec{
			DHCPConfig: v1alpha1.DHCPConfig{
				EnableDHCP: true,
			},
		},
	}
	_, err := testData.crdClientset.CrdV1alpha1().Subnets(E2ENamespace).Create(context.TODO(), subnet, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		err = nil
	}
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, "subnets.crd.nsx.vmware.com", E2ENamespace, subnet.Name, Ready)
	assertNil(t, err)
	allocatedSubnet, err := testData.crdClientset.CrdV1alpha1().Subnets(E2ENamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
	assertNil(t, err)
	nsxSubnets := testData.fetchSubnetByNamespace(t, E2ENamespace, false)
	assert.Equal(t, 1, len(nsxSubnets))

	targetCIDR := allocatedSubnet.Status.NetworkAddresses[0]
	err = testData.crdClientset.CrdV1alpha1().Subnets(E2ENamespace).Delete(context.TODO(), subnet.Name, v1.DeleteOptions{})
	assertNil(t, err)

	err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 100*time.Second, false, func(ctx context.Context) (bool, error) {
		_, err := testData.crdClientset.CrdV1alpha1().Subnets(E2ENamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
		if err != nil && errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	assertNil(t, err)
	nsxSubnets = testData.fetchSubnetByNamespace(t, E2ENamespace, true)
	assert.Equal(t, true, len(nsxSubnets) <= 1)

	subnet.Spec.IPAddresses = []string{targetCIDR}
	_, err = testData.crdClientset.CrdV1alpha1().Subnets(E2ENamespace).Create(context.TODO(), subnet, v1.CreateOptions{})
	if err != nil && errors.IsAlreadyExists(err) {
		t.Logf("Create Subnet error: %+v", err)
		err = nil
	}
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout*2, "subnets.crd.nsx.vmware.com", E2ENamespace, subnet.Name, Ready)
	assertNil(t, err)
	allocatedSubnet, err = testData.crdClientset.CrdV1alpha1().Subnets(E2ENamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
	assertNil(t, err)
	assert.Equal(t, targetCIDR, allocatedSubnet.Status.NetworkAddresses[0])

	nsxSubnets = testData.fetchSubnetByNamespace(t, E2ENamespace, false)
	assert.Equal(t, 1, len(nsxSubnets))

	err = testData.crdClientset.CrdV1alpha1().Subnets(E2ENamespace).Delete(context.TODO(), subnet.Name, v1.DeleteOptions{})
	assertNil(t, err)

	err = wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 100*time.Second, false, func(ctx context.Context) (bool, error) {
		_, err := testData.crdClientset.CrdV1alpha1().Subnets(E2ENamespace).Get(context.TODO(), subnet.Name, v1.GetOptions{})
		if err != nil && errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	assertNil(t, err)

	nsxSubnets = testData.fetchSubnetByNamespace(t, E2ENamespace, true)
	assert.Equal(t, true, len(nsxSubnets) <= 1)
}

func (data *TestData) fetchSubnetByNamespace(t *testing.T, ns string, isMarkForDelete bool) (res []model.VpcSubnet) {
	tags := []string{common.TagScopeNamespace, ns}
	results, err := testData.queryResource(common.ResourceTypeSubnet, tags)
	assertNil(t, err)
	subnets := transSearchResponsetoSubnet(results)
	for _, subnet := range subnets {
		if *subnet.MarkedForDelete == isMarkForDelete {
			res = append(res, subnet)
		}
	}
	return
}
