package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	ipBlocksInfoCRDName = "ip-blocks-info"
	defaultOrg          = "default"
	defaultProject      = "project-quality"
	defaultVPCProfile   = "default"
)

func TestIPBlocksInfo(t *testing.T) {
	return
	t.Run("case=InitialIPBlocksInfo", InitialIPBlocksInfo)
	t.Run("case=CustomIPBlocksInfo", CustomIPBlocksInfo)
}

func InitialIPBlocksInfo(t *testing.T) {
	privateTGWIPCIDRs, externalIPCIDRs := getDefaultIPBlocksCidrs(t)
	assertIPBlocksInfo(t, privateTGWIPCIDRs, externalIPCIDRs)
}

func CustomIPBlocksInfo(t *testing.T) {
	// Create Private IPBlocks
	ipBlockName := "ipblocksinfo-test-10.0.0.0-netmask-28"
	ipBlockCidr := "10.0.0.0/28"
	err := testData.nsxClient.IPBlockClient.Patch(defaultOrg, defaultProject, ipBlockName, model.IpAddressBlock{
		Cidr:       &ipBlockCidr,
		Visibility: common.String("PRIVATE"),
	})
	require.NoError(t, err)
	defer func() {
		testData.nsxClient.IPBlockClient.Delete(defaultOrg, defaultProject, ipBlockName)
	}()

	// Create VPC Connectivity Profile
	vpcProfileName := "ipblocksinfo-test"
	vpcProfile, err := testData.nsxClient.VPCConnectivityProfilesClient.Get(defaultOrg, defaultProject, defaultVPCProfile)
	require.NoError(t, err)
	err = testData.nsxClient.VPCConnectivityProfilesClient.Patch(defaultOrg, defaultProject, vpcProfileName, model.VpcConnectivityProfile{
		TransitGatewayPath: vpcProfile.TransitGatewayPath,
		ExternalIpBlocks:   vpcProfile.ExternalIpBlocks,
		PrivateTgwIpBlocks: []string{fmt.Sprintf("/orgs/%s/projects/%s/infra/ip-blocks/%s", defaultOrg, defaultProject, ipBlockName)},
	})
	require.NoError(t, err)
	defer func() {
		err := testData.nsxClient.VPCConnectivityProfilesClient.Delete(defaultOrg, defaultProject, vpcProfileName)
		require.NoError(t, err)
	}()

	// Create VPC with the profile above
	vpcId := "ipblocks-info-test"
	err = testData.nsxClient.VPCClient.Patch(defaultOrg, defaultProject, vpcId, model.Vpc{})
	require.NoError(t, err)
	vpcAttachmentId := "default"
	err = testData.nsxClient.VpcAttachmentClient.Patch(defaultOrg, defaultProject, vpcId, vpcAttachmentId, model.VpcAttachment{
		VpcConnectivityProfile: common.String(fmt.Sprintf("/orgs/%s/projects/%s/vpc-connectivity-profiles/%s", defaultOrg, defaultProject, vpcProfileName)),
	})
	require.NoError(t, err)
	defer func() {
		err := testData.nsxClient.VpcAttachmentClient.Delete(defaultOrg, defaultProject, vpcId, vpcAttachmentId)
		require.NoError(t, err)
	}()

	// Create VPCNetworkConfigurations
	vpcConfigName := "vpc-config-ipblocks-info"
	_, err = testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Create(context.TODO(), &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: vpcConfigName,
		},
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			NSXProject: fmt.Sprintf("/orgs/%s/projects/%s", defaultOrg, defaultProject),
			VPC:        vpcId,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	privateTGWIPCIDRs, externalIPCIDRs := getDefaultIPBlocksCidrs(t)
	defer func() {
		// Delete VPCNetworkConfigurations and check
		err = testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Delete(context.TODO(), vpcConfigName, metav1.DeleteOptions{})
		require.NoError(t, err)
		assertIPBlocksInfo(t, privateTGWIPCIDRs, externalIPCIDRs)
	}()

	// Check IPBlocksInfo
	updatedPrivateTGWIPCIDRs := append(privateTGWIPCIDRs, ipBlockCidr)
	assertIPBlocksInfo(t, updatedPrivateTGWIPCIDRs, externalIPCIDRs)
}

func getDefaultIPBlocksCidrs(t *testing.T) (privateTGWIPCIDRs []string, externalIPCIDRs []string) {
	vpcProfile, err := testData.nsxClient.VPCConnectivityProfilesClient.Get(defaultOrg, defaultProject, defaultVPCProfile)
	require.NoError(t, err)
	// Assume only one ipblock in default VPC Connectivity Profile
	externalBlock := vpcProfile.ExternalIpBlocks[0]
	privateTGWBlock := vpcProfile.PrivateTgwIpBlocks[0]

	results, err := testData.queryResource(common.ResourceTypeIPBlock, []string{})
	require.NoError(t, err)
	res := transSearchResponsetoIPBlock(results)
	count := 0
	for _, ipblock := range res {
		if count >= 2 {
			break
		}
		if *ipblock.Path == externalBlock {
			externalIPCIDRs = append(externalIPCIDRs, *ipblock.Cidr)
			count++
		}
		if *ipblock.Path == privateTGWBlock {
			privateTGWIPCIDRs = append(privateTGWIPCIDRs, *ipblock.Cidr)
			count++
		}
	}
	return
}

func assertIPBlocksInfo(t *testing.T, privateTGWIPCIDRs []string, externalIPCIDRs []string) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		res, err := testData.crdClientset.CrdV1alpha1().IPBlocksInfos().Get(context.TODO(), ipBlocksInfoCRDName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			log.Error(err, "Error fetching IPBlocksInfo", "IPBlocksInfo", res, "Name", ipBlocksInfoCRDName)
			return false, fmt.Errorf("error when waiting for IPBlocksInfo")
		}
		log.V(2).Info("IPBlocksInfo cidrs", "externalIPCIDRs", res.ExternalIPCIDRs, "privateTGWIPCIDRs", res.PrivateTGWIPCIDRs)
		if nsxutil.CompareArraysWithoutOrder(res.ExternalIPCIDRs, externalIPCIDRs) && nsxutil.CompareArraysWithoutOrder(res.PrivateTGWIPCIDRs, privateTGWIPCIDRs) {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)
	return
}

func transSearchResponsetoIPBlock(response model.SearchResponse) []model.IpAddressBlock {
	var resources []model.IpAddressBlock
	if response.Results == nil {
		return resources
	}
	for _, result := range response.Results {
		obj, err := common.NewConverter().ConvertToGolang(result, model.IpAddressBlockBindingType())
		if err != nil {
			log.Info("Failed to convert to golang subnet", "error", err)
			return resources
		}
		if ipblock, ok := obj.(model.IpAddressBlock); ok {
			resources = append(resources, ipblock)
		}
	}
	return resources
}
