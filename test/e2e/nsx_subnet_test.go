package e2e

import (
	"context"
	"log"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	SubnetSetCRType        = "subnetsets"
	SubnetPortCRType       = "subnetport"
	E2ENamespace           = "subnet-e2e"
	E2ENamespaceShared     = "subnet-e2e-shared"
	E2ENamespaceTarget     = "target-ns"
	VPCNetworkConfigCRName = "default"
	UserSubnetSet          = "user-pod-subnetset"
	// SubnetDeletionTimeout requires a bigger value than defaultTimeout, it's because that it takes some time for NSX to
	// recycle allocated IP addresses and NSX VPCSubnet won't be deleted until all IP addresses have been recycled.
	SubnetDeletionTimeout = 300 * time.Second
)

func verifySubnetSetCR(subnetSet string, subnetType string) bool {
	vpcNetworkConfig, err := testData.crdClientset.NsxV1alpha1().VPCNetworkConfigurations().Get(context.TODO(), VPCNetworkConfigCRName, v1.GetOptions{})
	if err != nil {
		log.Printf("Failed to get VPCNetworkConfiguration %s: %v", VPCNetworkConfigCRName, err)
		return false
	}
	subnetSetCR, err := testData.crdClientset.NsxV1alpha1().SubnetSets(E2ENamespace).Get(context.TODO(), subnetSet, v1.GetOptions{})
	if err != nil {
		log.Printf("Failed to get %s/%s: %s", E2ENamespace, subnetSet, err)
		return false
	}
	var desiredSubnetType string
	if subnetType == "" {
		desiredSubnetType = vpcNetworkConfig.Spec.DefaultPodSubnetAccessMode
	} else {
		desiredSubnetType = subnetType
	}
	if string(subnetSetCR.Spec.AccessMode) != desiredSubnetType {
		log.Printf("AccessMode is %s, while it's expected to be %s", subnetSetCR.Spec.AccessMode, desiredSubnetType)
		return false
	}
	if subnetSetCR.Spec.IPv4SubnetSize != vpcNetworkConfig.Spec.DefaultIPv4SubnetSize {
		log.Printf("IPv4SubnetSize is %d, while it's expected to be %d", subnetSetCR.Spec.IPv4SubnetSize, vpcNetworkConfig.Spec.DefaultIPv4SubnetSize)
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
	t.Run("case=UserSubnetSet", userSubnetSet)
	t.Run("case=SharedSubnetSet", sharedSubnetSet)
}

func defaultSubnetSet(t *testing.T) {
	// 1. Check whether default-vm-subnetset and default-pod-subnetset are created.
	err := testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetSetCRType, E2ENamespace, common.DefaultVMSubnetSet, Ready)
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetSetCRType, E2ENamespace, common.DefaultPodSubnetSet, Ready)
	assertNil(t, err)

	// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
	assertTrue(t, verifySubnetSetCR(common.DefaultVMSubnetSet, "Private"))
	assertTrue(t, verifySubnetSetCR(common.DefaultPodSubnetSet, ""))

	portPath, _ := filepath.Abs("./manifest/testSubnet/subnetport_1.yaml")
	err = applyYAML(portPath, E2ENamespace)
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetPortCRType, E2ENamespace, "port-1", Ready)
	assertNil(t, err)
	defer deleteYAML(portPath, E2ENamespace)

	// 3. Check SubnetSet CR status should be updated with NSX subnet info.
	subnetSet, err := testData.crdClientset.NsxV1alpha1().SubnetSets(E2ENamespace).Get(context.TODO(), common.DefaultPodSubnetSet, v1.GetOptions{})
	assertNil(t, err)
	assert.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")
	// 4. Check NSX subnet allocation.
	subnetPath := subnetSet.Status.Subnets[0].NSXResourcePath
	vpcInfo, err := common.ParseVPCResourcePath(subnetPath)
	assertNil(t, err, "Failed to parse VPC resource path %s", subnetPath)
	vpcSubnet, err := testData.nsxClient.SubnetsClient.Get(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, vpcInfo.ID)
	assertNil(t, err, "Failed to get VPC subnet %s", vpcInfo.ID)

	// 5. Check adding NSX subnet tags.
	ns, err := testData.clientset.CoreV1().Namespaces().Get(context.TODO(), E2ENamespace, v1.GetOptions{})
	assertNil(t, err)
	labelKey, labelValue := "subnet-e2e", "add"
	ns.Labels[labelKey] = labelValue
	ns, err = testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	assertNil(t, err)
	vpcSubnet, err = testData.nsxClient.SubnetsClient.Get(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, vpcInfo.ID)
	assertNil(t, err)
	found := false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey && *tag.Tag == labelValue {
			found = true
			break
		}
	}
	assertTrue(t, found, "Failed to add tags for NSX subnet %s", vpcInfo.ID)

	// 6. Check updating NSX subnet tags.
	labelValue = "update"
	ns.Labels[labelKey] = labelValue
	ns, err = testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	assertNil(t, err)
	vpcSubnet, err = testData.nsxClient.SubnetsClient.Get(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, vpcInfo.ID)
	assertNil(t, err)
	found = false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey && *tag.Tag == labelValue {
			found = true
			break
		}
	}
	assertTrue(t, found, "Failed to update tags for NSX subnet %s", vpcInfo.ID)

	// 7. Check deleting NSX subnet tags.
	delete(ns.Labels, labelKey)
	_, err = testData.clientset.CoreV1().Namespaces().Update(context.TODO(), ns, v1.UpdateOptions{})
	time.Sleep(5 * time.Second)
	assertNil(t, err)
	vpcSubnet, err = testData.nsxClient.SubnetsClient.Get(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, vpcInfo.ID)
	assertNil(t, err)
	found = false
	for _, tag := range vpcSubnet.Tags {
		if *tag.Scope == labelKey {
			found = true
			break
		}
	}
	assertFalse(t, found, "Failed to delete tags for NSX subnet %s", vpcInfo.ID)
}

func userSubnetSet(t *testing.T) {
	// 1. Check SubnetSet created by user.
	subnetSetPath, _ := filepath.Abs("./manifest/testSubnet/subnetset.yaml")
	err := applyYAML(subnetSetPath, E2ENamespace)
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetSetCRType, E2ENamespace, UserSubnetSet, Ready)
	assertNil(t, err)

	// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
	assertTrue(t, verifySubnetSetCR(UserSubnetSet, "Private"))

	portPath, _ := filepath.Abs("./manifest/testSubnet/subnetport_2.yaml")
	err = applyYAML(portPath, E2ENamespace)
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetPortCRType, E2ENamespace, "port-2", Ready)
	assertNil(t, err)
	defer deleteYAML(portPath, E2ENamespace)

	// 3. Check SubnetSet CR status should be updated with NSX subnet info.
	subnetSet, err := testData.crdClientset.NsxV1alpha1().SubnetSets(E2ENamespace).Get(context.TODO(), UserSubnetSet, v1.GetOptions{})
	assertNil(t, err)
	assert.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")
	// 4. Check NSX subnet allocation.
	subnetPath := subnetSet.Status.Subnets[0].NSXResourcePath
	vpcInfo, err := common.ParseVPCResourcePath(subnetPath)
	assertNil(t, err, "Failed to parse VPC resource path %s", subnetPath)
	_, err = testData.nsxClient.SubnetsClient.Get(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, vpcInfo.ID)
	assertNil(t, err, "Failed to get VPC subnet %s", vpcInfo.ID)
}

func sharedSubnetSet(t *testing.T) {
	// 1. Check whether default-vm-subnetset and default-pod-subnetset are created.
	err := testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetSetCRType, E2ENamespaceTarget, common.DefaultVMSubnetSet, Ready)
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetSetCRType, E2ENamespaceTarget, common.DefaultPodSubnetSet, Ready)
	assertNil(t, err)

	// 2. Check `Ipv4SubnetSize` and `AccessMode` should be same with related fields in VPCNetworkConfig.
	assertTrue(t, verifySubnetSetCR(common.DefaultVMSubnetSet, "Private"))
	assertTrue(t, verifySubnetSetCR(common.DefaultPodSubnetSet, ""))

	portPath, _ := filepath.Abs("./manifest/testSubnet/subnetport_3.yaml")
	err = applyYAML(portPath, E2ENamespaceShared)
	assertNil(t, err)
	err = testData.waitForCRReadyOrDeleted(defaultTimeout, SubnetPortCRType, E2ENamespaceShared, "port-3", Ready)
	assertNil(t, err)
	defer deleteYAML(portPath, E2ENamespaceShared)

	// 3. Check SubnetSet CR status should be updated with NSX subnet info.
	subnetSet, err := testData.crdClientset.NsxV1alpha1().SubnetSets(E2ENamespaceTarget).Get(context.TODO(), common.DefaultVMSubnetSet, v1.GetOptions{})
	assertNil(t, err)
	assert.NotEmpty(t, subnetSet.Status.Subnets, "No Subnet info in SubnetSet")

	// 4. Check IP address is allocated to SubnetPort.
	port, err := testData.crdClientset.NsxV1alpha1().SubnetPorts(E2ENamespaceShared).Get(context.TODO(), "port-3", v1.GetOptions{})
	assertNil(t, err)
	assert.NotEmpty(t, port.Status.IPAddresses, "No IP address in SubnetPort")

	// 5. Check Subnet CIDR contains SubnetPort IP.
	portIP := net.ParseIP(port.Status.IPAddresses[0].IP)
	_, subnetCIDR, err := net.ParseCIDR(subnetSet.Status.Subnets[0].IPAddresses[0])
	assertNil(t, err)
	assertTrue(t, subnetCIDR.Contains(portIP))
}
