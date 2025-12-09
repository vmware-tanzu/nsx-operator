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
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	ipBlocksInfoCRDName = "ip-blocks-info"
	defaultOrg          = "default"
	defaultProject      = "project-quality"
	defaultVPCProfile   = ""
	enableIPRangesCIDRs = false
)

func TestIPBlocksInfo(t *testing.T) {
	// initialize vpc profile id
	getDefaultVPCProfileID(t)
	t.Run("case=InitialIPBlocksInfo", InitialIPBlocksInfo)
	t.Run("case=CustomIPBlocksInfo", CustomIPBlocksInfo)
}

func getDefaultVPCProfileID(t *testing.T) {
	if defaultVPCProfile != "" {
		return
	}
	result, err := testData.nsxClient.VPCConnectivityProfilesClient.List(defaultOrg, defaultProject, nil, common.Bool(false), nil, nil, nil, nil)
	require.NoError(t, err)

	for _, vpcProfile := range result.Results {
		if vpcProfile.IsDefault != nil && *vpcProfile.IsDefault {
			defaultVPCProfile = *vpcProfile.Id
			break
		}
	}
	require.NotEqual(t, "", defaultVPCProfile, "No default VPC Profile is found for default Project")
}

func InitialIPBlocksInfo(t *testing.T) {
	privateTGWIPCIDRs, externalIPCIDRs, privateRanges, externalRanges := getDefaultIPBlocksCidrs(t)
	assertIPBlocksInfo(t, privateTGWIPCIDRs, externalIPCIDRs, privateRanges, externalRanges)
}

func stringPointer(s string) *string {
	return &s
}

func CustomIPBlocksInfo(t *testing.T) {
	// Create Private IPBlocks
	var ipBlockCidrs []string
	var ipBlockRanges []model.IpPoolRange
	var ipBlockCidr string
	ipBlockName := "ipblocksinfo-test-10.0.0.0-netmask-28"
	if enableIPRangesCIDRs {
		ipBlockCidrs = []string{"10.0.0.0/28", "10.0.1.0/28"}
		ipBlockRanges = []model.IpPoolRange{{Start: stringPointer("10.0.2.0"), End: stringPointer("10.0.2.15")},
			{Start: stringPointer("10.0.2.50"), End: stringPointer("10.0.2.60")}}
		err := testData.nsxClient.IPBlockClient.Patch(defaultOrg, defaultProject, ipBlockName, model.IpAddressBlock{
			Cidrs:      ipBlockCidrs,
			Ranges:     ipBlockRanges,
			Visibility: common.String("PRIVATE"),
		})
		require.NoError(t, err)
	} else {
		ipBlockCidr = "10.0.0.0/28"
		err := testData.nsxClient.IPBlockClient.Patch(defaultOrg, defaultProject, ipBlockName, model.IpAddressBlock{
			Cidr:       &ipBlockCidr,
			Visibility: common.String("PRIVATE"),
		})
		require.NoError(t, err)
	}

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
		log.Info("Deleting VPC Connectivity Profile", "vpcProfileName", vpcProfileName)
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
		log.Info("Deleting VPC", "vpcId", vpcId, "attachmentId", vpcAttachmentId)
		deleteChild := true
		err := testData.nsxClient.VPCClient.Delete(defaultOrg, defaultProject, vpcId, &deleteChild)
		require.NoError(t, err)
		err = testData.nsxClient.VpcAttachmentClient.Delete(defaultOrg, defaultProject, vpcId, vpcAttachmentId)
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

	privateTGWIPCIDRs, externalIPCIDRs, privateRanges, externalRanges := getDefaultIPBlocksCidrs(t)
	defer func() {
		// Delete VPCNetworkConfigurations and check
		log.Info("Deleting VPCNetworkConfigurations", "vpcConfigName", vpcConfigName)
		err = testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Delete(context.TODO(), vpcConfigName, metav1.DeleteOptions{})
		require.NoError(t, err)
		assertIPBlocksInfo(t, privateTGWIPCIDRs, externalIPCIDRs, privateRanges, externalRanges)
	}()

	// Check IPBlocksInfo
	var updatedPrivateTGWIPCIDRs []string
	if enableIPRangesCIDRs {
		updatedPrivateTGWIPCIDRs = append(privateTGWIPCIDRs, ipBlockCidrs...)
		var updatePrivateRanges = privateRanges
		for _, r := range ipBlockRanges {
			updatePrivateRanges = append(updatePrivateRanges, v1alpha1.IPPoolRange{Start: *r.Start, End: *r.End})
		}
		log.Debug("Updated private TGW", "updatedPrivateTGWIPCIDRs", updatedPrivateTGWIPCIDRs, "privateRanges", privateRanges)
		assertIPBlocksInfo(t, updatedPrivateTGWIPCIDRs, externalIPCIDRs, updatePrivateRanges, externalRanges)
	} else {
		updatedPrivateTGWIPCIDRs = append(privateTGWIPCIDRs, ipBlockCidr)
		assertIPBlocksInfo(t, updatedPrivateTGWIPCIDRs, externalIPCIDRs, privateRanges, externalRanges)
	}
}

func getDefaultIPBlocksCidrs(t *testing.T) (privateTGWIPCIDRs []string, externalIPCIDRs []string, privateRanges []v1alpha1.IPPoolRange, externalRanges []v1alpha1.IPPoolRange) {
	vpcProfile, err := testData.nsxClient.VPCConnectivityProfilesClient.Get(defaultOrg, defaultProject, defaultVPCProfile)
	require.NoError(t, err)

	results, err := testData.queryResource(common.ResourceTypeIPBlock, []string{})
	require.NoError(t, err)
	res := transSearchResponsetoIPBlock(results)

	for _, ipblock := range res {
		if util.Contains(vpcProfile.ExternalIpBlocks, *ipblock.Path) {
			if len(ipblock.Ranges) != 0 {
				enableIPRangesCIDRs = true
				for _, r := range ipblock.Ranges {
					ipRange := v1alpha1.IPPoolRange{
						Start: *r.Start,
						End:   *r.End,
					}
					externalRanges = append(externalRanges, ipRange)
				}
			}
			if len(ipblock.Cidrs) != 0 {
				enableIPRangesCIDRs = true
				externalIPCIDRs = append(externalIPCIDRs, ipblock.Cidrs...)
			}
			if !enableIPRangesCIDRs && ipblock.Cidr != nil { //nolint:staticcheck //ipblock.Cidr is deprecated
				externalIPCIDRs = append(externalIPCIDRs, *ipblock.Cidr) //nolint:staticcheck //ipblock.Cidr is deprecated
			}

		}
		if util.Contains(vpcProfile.PrivateTgwIpBlocks, *ipblock.Path) {
			if len(ipblock.Ranges) != 0 {
				enableIPRangesCIDRs = true
				for _, r := range ipblock.Ranges {
					ipRange := v1alpha1.IPPoolRange{
						Start: *r.Start,
						End:   *r.End,
					}
					privateRanges = append(privateRanges, ipRange)
				}
			}
			if len(ipblock.Cidrs) != 0 {
				enableIPRangesCIDRs = true
				privateTGWIPCIDRs = append(privateTGWIPCIDRs, ipblock.Cidrs...)
			}
			if !enableIPRangesCIDRs && ipblock.Cidr != nil { //nolint:staticcheck //ipblock.Cidr is deprecated
				privateTGWIPCIDRs = append(privateTGWIPCIDRs, *ipblock.Cidr) //nolint:staticcheck //ipblock.Cidr is deprecated
			}
		}
	}
	return
}

func assertIPBlocksInfo(t *testing.T, privateTGWIPCIDRs []string, externalIPCIDRs []string, privateRanges []v1alpha1.IPPoolRange, externalRanges []v1alpha1.IPPoolRange) {
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	log.Debug("Expecting IPBlocksInfo", "externalIPCIDRs", externalIPCIDRs, "externalIPRanges", externalRanges, "privateTGWIPCIDRs", privateTGWIPCIDRs, "privateTGWIPRanges", privateRanges)
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, 120*time.Second, false, func(ctx context.Context) (done bool, err error) {
		res, err := testData.crdClientset.CrdV1alpha1().IPBlocksInfos().Get(context.TODO(), ipBlocksInfoCRDName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			log.Error(err, "Error fetching IPBlocksInfo", "IPBlocksInfo", res, "Name", ipBlocksInfoCRDName)
			return false, fmt.Errorf("error when waiting for IPBlocksInfo")
		}
		log.Trace("IPBlocksInfo cidrs", "externalIPCIDRs", res.ExternalIPCIDRs, "privateTGWIPCIDRs", res.PrivateTGWIPCIDRs, "externalIPRanges", res.ExternalIPRanges, "privateTGWIPRanges", res.PrivateTGWIPRanges)
		if nsxutil.CompareArraysWithoutOrder(res.ExternalIPCIDRs, externalIPCIDRs) && nsxutil.CompareArraysWithoutOrder(res.PrivateTGWIPCIDRs, privateTGWIPCIDRs) && nsxutil.CompareArraysWithoutOrder(res.ExternalIPRanges, externalRanges) && nsxutil.CompareArraysWithoutOrder(res.PrivateTGWIPRanges, privateRanges) {
			return true, nil
		}
		log.Trace("IPBlocksInfo cidrs", "externalIPCIDRs", res.ExternalIPCIDRs, "externalIPRanges", res.ExternalIPRanges, "privateTGWIPCIDRs", res.PrivateTGWIPCIDRs, "privateTGWIPRanges", res.PrivateTGWIPRanges)
		return false, nil
	})
	require.NoError(t, err)
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
