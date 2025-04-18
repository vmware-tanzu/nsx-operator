package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	pkgutil "github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const (
	preVPCLabelKey = "prevpc-test"
	podImage       = "netfvt-docker-local.packages.vcfd.broadcom.net/humanux/http_https_echo:latest"
	containerName  = "netexec-container"

	lbServicePort     = int32(8080)
	podPort           = int32(80)
	resourceReadyTime = 5 * time.Minute
	nsDeleteTime      = 2 * time.Minute

	nsPrivilegedLabel = "pod-security.kubernetes.io/enforce"
)

var (
	projectPathFormat                = "/orgs/%s/projects/%s"
	defaultConnectivityProfileFormat = "/orgs/%s/projects/%s/vpc-connectivity-profiles/default"
)

func TestPreCreatedVPC(t *testing.T) {
	orgID, projectID, vpcID := setupVPC(t)
	nsName := "test-prevpc"
	projectPath := fmt.Sprintf(projectPathFormat, orgID, projectID)
	profilePath := fmt.Sprintf(defaultConnectivityProfileFormat, orgID, projectID)
	preCreatedVPCPath := fmt.Sprintf(common.VPCKey, orgID, projectID, vpcID)
	log.Info("Created VPC on NSX", "path", preCreatedVPCPath)
	defer func() {
		log.Info("Deleting the created VPC from NSX", "path", preCreatedVPCPath)
		ctx := context.Background()
		if pollErr := wait.PollUntilContextTimeout(ctx, 10*time.Second, resourceReadyTime, true, func(ctx context.Context) (done bool, err error) {
			if err := testData.nsxClient.VPCClient.Delete(orgID, projectID, vpcID, common.Bool(true)); err != nil {
				return false, nil
			}
			log.Info("The pre-created VPC is successfully deleted", "path", preCreatedVPCPath)
			return true, nil
		}); pollErr != nil {
			log.Error(pollErr, "Failed to delete the pre-created VPC within 5m after the test", "path", preCreatedVPCPath)
		}
	}()

	// Test: create NetworkConfig and NS using the pre-created VPC
	useVCAPI := testData.useWCPSetup()
	if useVCAPI {
		err := testData.vcClient.startSession()
		require.NoError(t, err, "A new VC session should be created for test")
		defer func() {
			testData.vcClient.closeSession()
		}()
	}

	err := createVPCNamespace(nsName, projectPath, profilePath, preCreatedVPCPath, nil, useVCAPI)
	require.NoError(t, err, "VPCNetworkConfiguration and Namespace should be created")
	log.Info("Created test Namespace", "Namespace", nsName)

	defer func() {
		deleteVPCNamespace(nsName, useVCAPI)
		_, err = testData.nsxClient.VPCClient.Get(orgID, projectID, vpcID)
		require.NoError(t, err, "Pre-Created VPC should exist after the K8s Namespace is deleted")
	}()
	// Wait until the created NetworkInfo is ready.
	getNetworkInfoWithPrivateIPs(t, nsName, nsName)
	log.Info("New Namespace's networkInfo is ready", "Namespace", nsName)

	// Test create LB Service inside the NS
	podName := "prevpc-service-pod"
	svcName := "prevpc-loadbalancer"
	err = createLBService(nsName, svcName, podName)
	require.NoError(t, err, "K8s LoadBalancer typed Service should be created")
	log.Info("Created LoadBalancer Service in the Namespace", "Namespace", nsName, "Service", svcName)

	// Wait until Pod has allocated IP
	_, err = testData.podWaitForIPs(resourceReadyTime, podName, nsName)
	require.NoErrorf(t, err, "Pod '%s/%s' is not ready within time %s", nsName, podName, resourceReadyTime.String())
	log.Info("Server Pod for the LoadBalancer Service in the Namespace is ready", "Namespace", nsName, "Service", svcName, "Pod", podName)

	// Wait until LoadBalancer Service has external IP.
	svc, err := testData.serviceWaitFor(resourceReadyTime, nsName, svcName, func(svc *corev1.Service) (bool, error) {
		lbStatuses := svc.Status.LoadBalancer.Ingress
		if len(lbStatuses) == 0 {
			return false, nil
		}
		lbStatus := lbStatuses[0]
		if lbStatus.IP == "" {
			return false, nil
		}
		return true, nil
	})
	require.NoErrorf(t, err, "K8s LoadBalancer typed Service should get an external IP within time %s", resourceReadyTime)
	svcUID := svc.UID
	lbIP := svc.Status.LoadBalancer.Ingress[0].IP
	log.Info("Load Balancer Service has been assigned with external IP", "Namespace", nsName, "Service", svcName, "ExternalIP", lbIP)

	// Create client Pod inside the NS
	clientPodName := "prevpc-client-pod"
	_, err = testData.createPod(nsName, clientPodName, containerName, podImage, corev1.ProtocolTCP, podPort)
	require.NoErrorf(t, err, "Client Pod '%s/%s' should be created", nsName, clientPodName)
	_, err = testData.podWaitForIPs(resourceReadyTime, clientPodName, nsName)
	require.NoErrorf(t, err, "Client Pod '%s/%s' is not ready within time %s", nsName, clientPodName, resourceReadyTime.String())
	log.Info("Client Pod in the Namespace is ready", "Namespace", nsName, "Service", svcName, "Pod", clientPodName)

	// Test traffic from client Pod to LB Service
	trafficErr := checkTrafficByCurl(nsName, clientPodName, containerName, lbIP, lbServicePort, 5*time.Second, 30*time.Second)
	require.NoError(t, trafficErr, "LoadBalancer traffic should work")
	log.Info("Verified traffic from client Pod to the LoadBalancer Service")

	// Test NSX LB VS should be removed after K8s LB Service is deleted
	err = testData.deleteService(nsName, svcName)
	require.NoError(t, err, "Service should be deleted")
	log.Info("Deleted the LoadBalancer Service")
	err = testData.waitForLBVSDeletion(resourceReadyTime, string(svcUID))
	require.NoErrorf(t, err, "NSX resources should be removed after K8s LoadBalancer Service is deleted")
	log.Info("NSX resources for the LoadBalancer Service are removed")
}

func deleteVPCNamespace(nsName string, usingVCAPI bool) {
	if usingVCAPI {
		if err := testData.vcClient.deleteNamespace(nsName); err != nil {
			log.Error(err, "Failed to delete Namespace on VCenter", "namespace", nsName)
		}
		return
	}

	vpcConfigName := fmt.Sprintf("%s-vpcconfig-%s", nsName, getRandomString())
	deleteVPCNamespaceOnK8s(nsName, vpcConfigName)
}

func deleteVPCNamespaceOnK8s(nsName string, vpcConfigName string) {
	ctx := context.Background()
	if err := testData.deleteNamespace(nsName, nsDeleteTime); err != nil {
		log.Error(err, "Failed to delete VPCNetworkConfiguration", "name", vpcConfigName)
	}
	if err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Delete(ctx, vpcConfigName, metav1.DeleteOptions{}); err != nil {
		log.Error(err, "Failed to delete VPCNetworkConfiguration %s", "name", vpcConfigName)
	}
}

func setupVPC(tb testing.TB) (string, string, string) {
	systemVPC, err := testData.waitForSystemNetworkConfigReady(5 * time.Minute)
	require.NoError(tb, err)

	nsxProjectPath := systemVPC.Spec.NSXProject
	reExp := regexp.MustCompile(`/orgs/([^/]+)/projects/([^/]+)([/\S+]*)`)
	matches := reExp.FindStringSubmatch(nsxProjectPath)
	orgID, projectID := matches[1], matches[2]
	systemVPCStatus := systemVPC.Status.VPCs[0]
	useNSXLB := systemVPCStatus.NSXLoadBalancerPath != ""

	vpcID := fmt.Sprintf("testvpc-%s", getRandomString())
	if err := testData.createVPC(orgID, projectID, vpcID, []string{customizedPrivateCIDR1}, useNSXLB); err != nil {
		tb.Fatalf("Unable to create a VPC on NSX: %v", err)
	}
	return orgID, projectID, vpcID
}

func createVPCNamespace(nsName, projectPath, profilePath, vpcPath string, privateIPs []string, usingVCAPI bool) error {
	if usingVCAPI {
		return testData.createPreVPCNamespaceByVCenter(nsName, vpcPath)
	}

	vpcConfigName := fmt.Sprintf("%s-vpcconfig-%s", nsName, getRandomString())
	return createVPCNamespaceOnK8s(nsName, vpcConfigName, projectPath, profilePath, vpcPath, privateIPs)
}

func createVPCNamespaceOnK8s(nsName, vpcConfigName, projectPath, profilePath, vpcPath string, privateIPs []string) error {
	ctx := context.Background()
	vpcNetConfig := &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: vpcConfigName,
		},
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			NSXProject:             projectPath,
			VPCConnectivityProfile: profilePath,
			VPC:                    vpcPath,
			PrivateIPs:             privateIPs,
		},
	}

	if _, err := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Create(ctx, vpcNetConfig, metav1.CreateOptions{}); err != nil {
		log.Error(err, "Failed to create VPCNetworkConfiguration", "name", vpcNetConfig)
		return err
	}

	if err := testData.createNamespace(nsName, func(ns *corev1.Namespace) {
		if ns.Annotations == nil {
			ns.Annotations = map[string]string{}
		}
		ns.Annotations[common.AnnotationVPCNetworkConfig] = vpcConfigName
		if ns.Labels == nil {
			ns.Labels = map[string]string{}
		}
		ns.Labels[nsPrivilegedLabel] = "privileged"
	}); err != nil {
		// Clean up the created VPCNetworkConfiguration.
		log.Error(err, "Failed to create Namespace", "name", nsName)
		if delErr := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Delete(ctx, vpcConfigName, metav1.DeleteOptions{}); delErr != nil {
			log.Error(err, "Failed to delete VPCNetworkConfiguration", "name", vpcConfigName)
		}
		return err
	}
	return nil
}

func createLBService(nsName, svcName, podName string) error {
	podLabels := map[string]string{
		preVPCLabelKey: svcName,
	}
	if _, err := testData.createPod(nsName, podName, containerName, podImage, corev1.ProtocolTCP, podPort, func(pod *corev1.Pod) {
		for k, v := range podLabels {
			pod.Labels[k] = v
		}
	}); err != nil {
		log.Error(err, "Failed to create Pod", "namespace", nsName, "name", podName)
		return err
	}
	if _, err := testData.createService(nsName, svcName, lbServicePort, podPort, corev1.ProtocolTCP, podLabels, corev1.ServiceTypeLoadBalancer); err != nil {
		log.Error(err, "Failed to create LoadBalancer Service", "namespace", nsName, "name", svcName)
		return err
	}
	return nil
}

func getRandomString() string {
	timestamp := time.Now().UnixNano()
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d", timestamp)))
	return hex.EncodeToString(hash[:])[:8]
}

func (data *TestData) createVPC(orgID, projectID, vpcID string, privateIPs []string, useNSXLB bool) error {
	createdVPC := &model.Vpc{
		Id:            common.String(vpcID),
		DisplayName:   common.String("e2e-test-pre-vpc"),
		IpAddressType: common.String("IPV4"),
		PrivateIps:    privateIPs,
		ResourceType:  common.String(common.ResourceTypeVpc),
	}
	vpcPath := fmt.Sprintf(common.VPCKey, orgID, projectID, vpcID)
	var lbsPath string
	var createdLBS *model.LBService
	if !useNSXLB {
		loadBalancerVPCEndpointEnabled := true
		createdVPC.LoadBalancerVpcEndpoint = &model.LoadBalancerVPCEndpoint{Enabled: &loadBalancerVPCEndpointEnabled}
	} else {
		lbsPath = fmt.Sprintf("%s/vpc-lbs/default", vpcPath)
		createdLBS = &model.LBService{
			Id:               common.String("default"),
			ConnectivityPath: common.String(vpcPath),
			Size:             common.String(model.LBService_SIZE_SMALL),
			ResourceType:     common.String(common.ResourceTypeLBService),
		}
	}
	attachmentPath := fmt.Sprintf("%s/attachments/default", vpcPath)
	attachment := &model.VpcAttachment{
		Id:                     common.String("default"),
		VpcConnectivityProfile: common.String(fmt.Sprintf(defaultConnectivityProfileFormat, orgID, projectID)),
	}
	svc := &vpc.VPCService{}
	orgRoot, err := svc.WrapHierarchyVPC(orgID, projectID, createdVPC, createdLBS, attachment)
	if err != nil {
		log.Error(err, "Failed to build HAPI request for VPC related resources")
		return err
	}
	enforceRevisionCheckParam := false
	if err := data.nsxClient.OrgRootClient.Patch(*orgRoot, &enforceRevisionCheckParam); err != nil {
		return err
	}

	log.Info("Successfully requested VPC on NSX", "path", vpcPath)
	realizeService := realizestate.InitializeRealizeState(common.Service{NSXClient: data.nsxClient.Client})
	if pollErr := wait.PollUntilContextTimeout(context.Background(), 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		if err = realizeService.CheckRealizeState(pkgutil.NSXTRealizeRetry, vpcPath, []string{common.GatewayInterfaceId}); err != nil {
			log.Error(err, "NSX VPC is not yet realized", "path", vpcPath)
			return false, nil
		}
		if lbsPath != "" {
			if err := realizeService.CheckRealizeState(pkgutil.NSXTRealizeRetry, lbsPath, []string{}); err != nil {
				log.Error(err, "NSX LBS is not yet realized", "path", lbsPath)
				return false, nil
			}
		}
		if err = realizeService.CheckRealizeState(pkgutil.NSXTRealizeRetry, attachmentPath, []string{}); err != nil {
			log.Error(err, "VPC attachment is not yet realized", "path", attachmentPath)
			return false, nil
		}
		return true, nil
	}); pollErr != nil {
		log.Error(pollErr, "Failed to realize VPC and related resources within 2m")
		data.nsxClient.VPCClient.Delete(orgID, projectID, vpcID, common.Bool(true))
		if err := data.nsxClient.VPCClient.Delete(orgID, projectID, vpcID, common.Bool(true)); err != nil {
			log.Error(err, "Failed to recursively delete NSX VPC", "path", vpcPath)
		}
		return pollErr
	}
	return nil
}

func (data *TestData) waitForSystemNetworkConfigReady(timeout time.Duration) (*v1alpha1.VPCNetworkConfiguration, error) {
	var systemConfig *v1alpha1.VPCNetworkConfiguration
	if pollErr := wait.PollUntilContextTimeout(context.Background(), 20*time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		systemConfig, err = data.crdClientset.CrdV1alpha1().VPCNetworkConfigurations().Get(ctx, "system", metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if len(systemConfig.Status.VPCs) == 0 {
			return false, nil
		}
		systemVPC := systemConfig.Status.VPCs[0]
		if systemVPC.VPCPath == "" {
			return false, nil
		}
		return true, nil
	}); pollErr != nil {
		log.Error(pollErr, "Failed to wait for system VPCNetworkConfiguration to be ready", "timeout", timeout.String())
		return nil, pollErr
	}
	return systemConfig, nil
}

func (data *TestData) waitForLBVSDeletion(timeout time.Duration, svcID string) error {
	lbServiceTags := []string{"ncp/service_uid", svcID}
	if pollErr := wait.PollUntilContextTimeout(context.Background(), 10*time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		// Check NSX VirtualServer deletion.
		vsResults, err := data.queryResource("LBVirtualServer", lbServiceTags)
		if err != nil {
			return false, err
		}
		if len(vsResults.Results) > 0 {
			return false, nil
		}
		// Check NSX LBPool deletion.
		lbPoolResults, err := data.queryResource("LBPool", lbServiceTags)
		if err != nil {
			return false, err
		}
		if len(lbPoolResults.Results) > 0 {
			return false, nil
		}
		// Check NSX IP Allocation deletion.
		ipAllocationResults, err := data.queryResource("VpcIpAddressAllocation", lbServiceTags)
		if err != nil {
			return false, err
		}
		if len(ipAllocationResults.Results) > 0 {
			return false, nil
		}
		return true, nil
	}); pollErr != nil {
		log.Error(pollErr, "Failed to delete LoadBalancer Service related resources on NSX")
		return pollErr
	}
	return nil
}

func (data *TestData) createPreVPCNamespaceByVCenter(nsName, vpcPath string) error {
	svID, err := data.vcClient.getSupervisorID()
	if err != nil {
		return fmt.Errorf("failed to get a valid supervisor ID: %v", err)
	}
	err = data.vcClient.createNamespaceWithPreCreatedVPC(nsName, vpcPath, svID)
	if err != nil {
		return fmt.Errorf("failed to create Namespace on VCenter: %v", err)
	}
	ctx := context.Background()
	getErr := wait.PollUntilContextTimeout(ctx, 2*time.Second, 10*time.Second, false, func(ctx context.Context) (done bool, err error) {
		_, statusCode, err := data.vcClient.getNamespaceInfoByName(nsName)
		if statusCode == http.StatusNotFound {
			return false, nil
		}
		if err != nil {
			return true, err
		}
		return true, nil
	})
	if getErr != nil {
		data.vcClient.deleteNamespace(nsName)
		return fmt.Errorf("failed to create Namespace on VCenter, delete it: %v", err)
	}

	return nil
}
