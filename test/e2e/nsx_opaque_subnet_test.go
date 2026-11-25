package e2e

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	nsxerrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// SharedOpaqueSubnetCreatesSegmentPort runs sequentially with the other shared
// Subnet cases because it temporarily adds a share to the namespace's config.
func SharedOpaqueSubnetCreatesSegmentPort(t *testing.T) {
	ctx := context.Background()
	nsxClient := testData.nsxClient
	namespaces := testData.clientset.CoreV1().Namespaces()
	configs := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations()
	subnets := testData.crdClientset.CrdV1alpha1().Subnets(precreatedSubnetNs1)
	ports := testData.crdClientset.CrdV1alpha1().SubnetPorts(precreatedSubnetNs1)

	source, err := namespaces.Get(ctx, precreatedSubnetNsTargetNs, metav1.GetOptions{})
	require.NoError(t, err)
	sourceConfig := source.Annotations[common.AnnotationVPCNetworkConfig]
	require.NotEmpty(t, sourceConfig)
	vpcPath := getVPCPathFromVPCNetworkConfiguration(t, sourceConfig)
	vpcInfo, err := common.ParseVPCResourcePath(vpcPath)
	require.NoError(t, err)
	target, err := namespaces.Get(ctx, precreatedSubnetNs1, metav1.GetOptions{})
	require.NoError(t, err)
	configName := target.Annotations[common.AnnotationVPCNetworkConfig]
	require.NotEmpty(t, configName)

	id := "e2e-opaque-" + getRandomString()
	segmentPath := "/infra/segments/" + id
	subnetPath := vpcPath + "/subnets/" + id
	associatedResource, err := common.ConvertSubnetPathToAssociatedResource(subnetPath)
	require.NoError(t, err)
	var createdPort *v1alpha1.SubnetPort
	var segmentCreated, subnetCreated, shareAdded bool

	// Stop cleanup if a dependency cannot be removed. Never cascade-delete the
	// segment to hide a leaked port or remove fixtures still referenced by a CR.
	t.Cleanup(func() {
		if createdPort != nil {
			err := ports.Delete(ctx, createdPort.Name, metav1.DeleteOptions{})
			if !apierrors.IsNotFound(err) && !assert.NoError(t, err, "delete test SubnetPort") {
				return
			}
			err = wait.PollUntilContextTimeout(ctx, time.Second, defaultTimeout, true, func(ctx context.Context) (bool, error) {
				if _, err := ports.Get(ctx, createdPort.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
					return false, err
				}
				tags := []string{common.TagScopeSubnetPortCRUID, string(createdPort.UID)}
				for _, resourceType := range []string{common.ResourceTypeSegmentPort, common.ResourceTypeSubnetPort} {
					result, err := testData.queryResource(resourceType, tags)
					if err != nil || len(result.Results) != 0 {
						return false, err
					}
				}
				return true, nil
			})
			if !assert.NoError(t, err, "wait for test port deletion from Kubernetes and NSX") {
				return
			}
		}
		if shareAdded {
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				config, err := configs.Get(ctx, configName, metav1.GetOptions{})
				if err != nil {
					return err
				}
				remaining := config.Spec.Subnets[:0]
				for _, shared := range config.Spec.Subnets {
					if shared.Path != subnetPath {
						remaining = append(remaining, shared)
					}
				}
				config.Spec.Subnets = remaining
				_, err = configs.Update(ctx, config, metav1.UpdateOptions{})
				return err
			})
			if !assert.NoError(t, err, "remove only the test's shared Subnet path") {
				return
			}
			err = wait.PollUntilContextTimeout(ctx, time.Second, defaultTimeout, true, func(ctx context.Context) (bool, error) {
				list, err := subnets.List(ctx, metav1.ListOptions{})
				if err != nil {
					return false, err
				}
				for _, subnet := range list.Items {
					if subnet.Annotations[common.AnnotationAssociatedResource] == associatedResource {
						return false, nil
					}
				}
				return true, nil
			})
			if !assert.NoError(t, err, "wait for shared Subnet CR removal") {
				return
			}
		}
		if subnetCreated {
			err := nsxClient.SubnetsClient.Delete(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, id)
			if !opaqueSubnetNSXNotFound(err) && !assert.NoError(t, err, "delete test opaque Subnet") {
				return
			}
			err = wait.PollUntilContextTimeout(ctx, time.Second, defaultTimeout, true, func(ctx context.Context) (bool, error) {
				_, err := nsxClient.SubnetsClient.Get(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, id)
				if opaqueSubnetNSXNotFound(err) {
					return true, nil
				}
				return false, err
			})
			if !assert.NoError(t, err, "wait for NSX opaque Subnet deletion") {
				return
			}
		}
		if segmentCreated {
			err := nsxClient.SegmentsClient.Delete(id)
			if !opaqueSubnetNSXNotFound(err) {
				assert.NoError(t, err, "delete test segment")
			}
		}
	})

	// NSX assigns the default overlay transport zone to the isolated segment.
	err = nsxClient.SegmentsClient.Patch(id, model.Segment{Id: common.String(id), DisplayName: common.String(id)})
	require.NoError(t, err, "create test segment (requires a default overlay transport zone)")
	segmentCreated = true
	realizeService := realizestate.InitializeRealizeState(common.Service{NSXClient: nsxClient.Client})
	require.NoError(t, realizeService.CheckRealizeState(util.NSXTRealizeRetry, segmentPath, nil))
	segment, err := nsxClient.SegmentsClient.Get(id)
	require.NoError(t, err)
	require.NotNil(t, segment.RealizationId)
	require.NotEmpty(t, *segment.RealizationId)

	// Create the NSX tracking Subnet directly; a normal Subnet CR would create
	// an operator-managed VPC Subnet instead of exercising the share workflow.
	err = nsxClient.SubnetsClient.Patch(vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, id, model.VpcSubnet{
		Id: common.String(id), DisplayName: common.String(id),
		AccessMode:         common.String(model.VpcSubnet_ACCESS_MODE_L2_ONLY),
		IpAddressType:      common.String(model.VpcSubnet_IP_ADDRESS_TYPE_IPV4),
		SubnetDhcpConfig:   &model.SubnetDhcpConfig{Mode: common.String(model.SubnetDhcpConfig_MODE_DEACTIVATED)},
		SubnetDhcpv6Config: &model.SubnetDhcpv6Config{Mode: common.String(model.SubnetDhcpv6Config_MODE_DEACTIVATED)},
		Tags:               []model.Tag{{Scope: common.String(common.TagScopeWCPSegmentTrackingSubnet), Tag: common.String(segmentPath)}},
	})
	require.NoError(t, err, "create NSX opaque Subnet linked to the test segment")
	subnetCreated = true
	shareAdded = true
	updateVPCNetworkConfigSubnets(t, precreatedSubnetNs1, createSharedSubnets([]string{subnetPath}), true)

	var shared *v1alpha1.Subnet
	err = wait.PollUntilContextTimeout(ctx, time.Second, 2*defaultTimeout, true, func(ctx context.Context) (bool, error) {
		list, err := subnets.List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		for i := range list.Items {
			if list.Items[i].Annotations[common.AnnotationAssociatedResource] == associatedResource {
				shared = &list.Items[i]
				return shared.Status.Shared && controllercommon.IsObjectReady(shared.Status.Conditions), nil
			}
		}
		return false, nil
	})
	require.NoError(t, err, "wait for the operator-generated shared opaque Subnet CR")
	require.Equal(t, segmentPath, shared.Annotations[common.AnnotationSegmentPath])
	require.Equal(t, v1alpha1.AccessModeL2Only, shared.Spec.AccessMode)

	createdPort, err = ports.Create(ctx, &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{Name: id, Namespace: precreatedSubnetNs1},
		Spec:       v1alpha1.SubnetPortSpec{Subnet: shared.Name},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	port := assureSubnetPort(t, precreatedSubnetNs1, createdPort.Name, conditionSubnetPortRealized)

	// Query by CR UID, not name: namespaces and previous test runs can reuse names.
	tags := []string{common.TagScopeSubnetPortCRUID, string(port.UID)}
	var segmentPort model.SegmentPort
	err = wait.PollUntilContextTimeout(ctx, time.Second, defaultTimeout, true, func(ctx context.Context) (bool, error) {
		result, err := testData.queryResource(common.ResourceTypeSegmentPort, tags)
		if err != nil || len(result.Results) == 0 {
			return false, err
		}
		if len(result.Results) != 1 {
			return false, fmt.Errorf("expected one SegmentPort for CR %s, got %d", port.UID, len(result.Results))
		}
		obj, conversionErrors := common.NewConverter().ConvertToGolang(result.Results[0], model.SegmentPortBindingType())
		if len(conversionErrors) != 0 {
			return false, errors.Join(conversionErrors...)
		}
		var ok bool
		segmentPort, ok = obj.(model.SegmentPort)
		if !ok {
			return false, fmt.Errorf("unexpected SegmentPort search result %T", obj)
		}
		return true, nil
	})
	require.NoError(t, err, "wait for SegmentPort in the NSX search index")
	require.NotNil(t, segmentPort.Id)
	require.NotNil(t, segmentPort.ParentPath)
	require.NotNil(t, segmentPort.Path)
	assert.Equal(t, common.String(common.ResourceTypeSegmentPort), segmentPort.ResourceType)
	assert.Equal(t, segmentPath, *segmentPort.ParentPath)
	assert.Equal(t, segmentPath+"/ports/"+*segmentPort.Id, *segmentPort.Path)
	require.NotNil(t, segmentPort.Attachment)
	require.NotNil(t, segmentPort.Attachment.Id)
	require.NotEmpty(t, port.Status.Attachment.ID)
	assert.Equal(t, port.Status.Attachment.ID, *segmentPort.Attachment.Id)
	assert.Equal(t, *segment.RealizationId, port.Status.NetworkInterfaceConfig.LogicalSwitchUUID)

	vpcPorts, err := testData.queryResource(common.ResourceTypeSubnetPort, tags)
	require.NoError(t, err)
	assert.Empty(t, vpcPorts.Results, "opaque Subnet must create a SegmentPort, not a VpcSubnetPort")
}

func opaqueSubnetNSXNotFound(err error) bool {
	var nsxErr *nsxutil.NSXApiError
	var notFound nsxerrors.NotFound
	var notFoundPtr *nsxerrors.NotFound
	return errors.Is(err, nsxutil.HttpNotFoundError) ||
		(errors.As(err, &nsxErr) && nsxErr.Type() == nsxerrors.ErrorType_NOT_FOUND) ||
		errors.As(err, &notFound) || errors.As(err, &notFoundPtr)
}
