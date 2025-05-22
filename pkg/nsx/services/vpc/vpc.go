package vpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	stderrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

type LBProvider string

const (
	albEndpointPath = "policy/api/v1/infra/sites/default/enforcement-points/alb-endpoint"
	NSXLB           = LBProvider("nsx-lb")
	AVILB           = LBProvider("avi")
	NoneLB          = LBProvider("none")

	nsxVpcNameIndexKey = "nsxVpcNameIndex"
)

var (
	log                           = &logger.Log
	ResourceTypeVPC               = common.ResourceTypeVpc
	NewConverter                  = common.NewConverter
	globalLbProvider              = NoneLB
	lbProviderMutex               = &sync.Mutex{}
	MarkedForDelete               = true
	EnforceRevisionCheckParam     = false
	TypeGatewayConnection         = "gateway-connections"
	TypeDistributedVlanConnection = "distributed-vlan-connections"
)

type VPCService struct {
	common.Service
	VpcStore *VPCStore
	LbsStore *LBSStore
}

func (s *VPCService) GetDefaultNetworkConfig() (*v1alpha1.VPCNetworkConfiguration, error) {
	vpcNetworkConfigList := &v1alpha1.VPCNetworkConfigurationList{}
	err := s.Client.List(context.Background(), vpcNetworkConfigList)
	if err != nil {
		return nil, err
	}

	for _, vpcConfigCR := range vpcNetworkConfigList.Items {
		if common.IsDefaultNetworkConfigCR(&vpcConfigCR) {
			return &vpcConfigCR, nil
		}
	}
	return nil, fmt.Errorf("failed to locate default NetworkConfig")
}

func (s *VPCService) GetVPCNetworkConfig(ncCRName string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
	vpcNetworkConfig := &v1alpha1.VPCNetworkConfiguration{}
	err := s.Client.Get(context.Background(), types.NamespacedName{Name: ncCRName}, vpcNetworkConfig)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return vpcNetworkConfig, true, err
}

// GetNamespacesByNetworkconfigName find the namespace list which is using the given network configuration
func (s *VPCService) GetNamespacesByNetworkconfigName(nc string) ([]string, error) {
	var result []string
	namespaces := &v1.NamespaceList{}
	err := s.Client.List(context.Background(), namespaces)
	if err != nil {
		log.Error(err, "Failed to list Namespaces")
		return result, err
	}
	for _, ns := range namespaces.Items {
		name, err := s.GetNetworkconfigNameFromAnnotation(ns.Name, ns.Annotations)
		if err != nil {
			continue
		}
		if name == nc {
			result = append(result, ns.Name)
		}
	}
	return result, nil
}

func (s *VPCService) GetVPCNetworkConfigByNamespace(ns string) (*v1alpha1.VPCNetworkConfiguration, error) {
	ncName, err := s.GetNetworkconfigNameFromNS(context.Background(), ns)
	if err != nil {
		log.Error(err, "Failed to get NetworkConfig name for Namespace", "Namespace", ns)
		return nil, err
	}

	nc, ncExist, err := s.GetVPCNetworkConfig(ncName)
	if err != nil {
		log.Error(err, "Failed to get NetworkConfig info using NetworkConfig name", "Name", ncName)
		return nil, err
	}
	if !ncExist {
		log.Info("NetworkConfig info does not exist", "Name", ncName)
		return nil, nil
	}
	return nc, nil
}

// TBD: for now, if network config info do not contains private cidr, we consider this is
// incorrect configuration, and skip creating this VPC CR
func (s *VPCService) ValidateNetworkConfig(nc *v1alpha1.VPCNetworkConfiguration) error {
	// Skip creating VPC if NSX Project Path is invalid
	_, _, err := common.NSXProjectPathToId(nc.Spec.NSXProject)
	if err != nil {
		err = fmt.Errorf("invalid NSXProject: %s, error: %w", nc.Spec.NSXProject, err)
		log.Error(err, "Failed to parse NSXProject in NetworkConfig")
		return err
	}
	if IsPreCreatedVPC(nc) {
		// if network config is using a pre-created VPC, skip the check on PrivateIPs.
		return nil
	}
	if nc.Spec.PrivateIPs != nil && len(nc.Spec.PrivateIPs) != 0 {
		return nil
	}
	return fmt.Errorf("missing private cidr")
}

// InitializeVPC sync NSX resources
func InitializeVPC(service common.Service) (*VPCService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error, 2)

	VPCService := &VPCService{Service: service}
	VPCService.VpcStore = &VPCStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeNamespaceUID: vpcIndexNamespaceIDFunc,
			common.TagScopeNamespace:    vpcIndexNamespaceNameFunc,
			nsxVpcNameIndexKey:          vpcIndexVpcNameFunc,
		}),
		BindingType: model.VpcBindingType(),
	}}

	VPCService.LbsStore = &LBSStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.LBServiceBindingType(),
	}}
	// Note: waitgroup.Add must be called before its consumptions.
	wg.Add(2)
	// initialize vpc store, lbs store
	go VPCService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeVpc, nil, VPCService.VpcStore)
	go VPCService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeLBService, nil, VPCService.LbsStore)

	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return VPCService, err
	}

	return VPCService, nil
}

func (s *VPCService) GetCurrentVPCsByNamespace(ctx context.Context, namespace string) []*model.Vpc {
	namespaceObj, sharedNamespace, err := s.resolveSharedVPCNamespace(ctx, namespace)
	if err != nil {
		log.Error(err, "Failed to get Namespace")
		return nil
	}
	if sharedNamespace == nil {
		return s.VpcStore.GetVPCsByNamespaceIDFromStore(string(namespaceObj.UID))
	}
	return s.VpcStore.GetVPCsByNamespaceIDFromStore(string(sharedNamespace.UID))
}

func (s *VPCService) GetVPCsByNamespace(namespace string) []*model.Vpc {
	return s.VpcStore.GetVPCsByNamespaceFromStore(namespace)
}

func (s *VPCService) ListVPC() []model.Vpc {
	vpcs := s.VpcStore.List()
	var vpcSet []model.Vpc
	for _, vpc := range vpcs {
		vpcSet = append(vpcSet, *vpc.(*model.Vpc))
	}
	return vpcSet
}

// DeleteVPC will try to delete VPC resource from NSX.
func (s *VPCService) DeleteVPC(path string) error {
	pathInfo, err := common.ParseVPCResourcePath(path)
	if err != nil {
		return err
	}
	vpcClient := s.NSXClient.VPCClient

	if err := vpcClient.Delete(pathInfo.OrgID, pathInfo.ProjectID, pathInfo.VPCID, common.Bool(true)); err != nil {
		err = nsxutil.TransNSXApiError(err)
		return err
	}
	lbs := s.LbsStore.GetByKey(pathInfo.VPCID)
	if lbs != nil {
		s.LbsStore.Delete(lbs)
	}

	vpc := s.VpcStore.GetByKey(pathInfo.VPCID)
	// When deleting vpc due to realization failure in VPC creation process. the VPC is created on NSX side,
	// but not insert in to VPC store, in this condition, the vpc could not be found in vpc store.
	if vpc == nil {
		log.Info("VPC not found in vpc store, skip cleaning VPC store", "VPC", pathInfo.VPCID)
		return nil
	}
	vpc.MarkedForDelete = &MarkedForDelete
	if err := s.VpcStore.Apply(vpc); err != nil {
		return err
	}

	log.Info("Successfully deleted NSX VPC", "VPC", pathInfo.VPCID)
	return nil
}

func (s *VPCService) IsSharedVPCNamespaceByNS(ctx context.Context, ns string) (bool, error) {
	_, sharedNamespaceObj, err := s.resolveSharedVPCNamespace(ctx, ns)
	if err != nil {
		return false, err
	}
	if sharedNamespaceObj == nil {
		return false, nil
	}
	if sharedNamespaceObj.Name != ns {
		return true, nil
	}
	return false, err
}

func (s *VPCService) getNamespace(ctx context.Context, name string) (*v1.Namespace, error) {
	obj := &v1.Namespace{}
	if err := s.Client.Get(ctx, types.NamespacedName{Name: name}, obj); err != nil {
		log.Error(err, "Failed to fetch Namespace", "Namespace", name)
		return nil, err
	}
	return obj, nil
}

// resolveSharedVPCNamespace will resolve the Namespace relationship based on VPC sharing,
// whether a shared VPC Namespace exists.
func (s *VPCService) resolveSharedVPCNamespace(ctx context.Context, ns string) (*v1.Namespace, *v1.Namespace, error) {
	obj, err := s.getNamespace(ctx, ns)
	if err != nil {
		return nil, nil, err
	}

	annos := obj.Annotations
	// If no annotation on ns, then this is not a shared VPC ns
	if len(annos) == 0 {
		return obj, nil, nil
	}

	// If no annotation nsx.vmware.com/shared_vpc_namespace on ns, this is not a shared vpc
	nsForSharedVPCs, exist := annos[common.AnnotationSharedVPCNamespace]
	if !exist {
		return obj, nil, nil
	}
	if nsForSharedVPCs == ns {
		return nil, obj, nil
	}
	sharedNamespace, err := s.getNamespace(ctx, nsForSharedVPCs)
	if err != nil {
		// if sharedNamespace does not exist, add this case for security,
		// It shouldn't happen that a shared Namespace doesn't exist but is used as an annotation on another Namespace.
		return nil, nil, err
	}
	return nil, sharedNamespace, nil
}

func (s *VPCService) GetNetworkconfigNameFromNS(ctx context.Context, ns string) (string, error) {
	obj, err := s.getNamespace(ctx, ns)
	if err != nil {
		return "", err
	}
	return s.GetNetworkconfigNameFromAnnotation(ns, obj.Annotations)
}

func (s *VPCService) GetNetworkconfigNameFromAnnotation(ns string, annos map[string]string) (string, error) {
	useDefault := false
	// use default network config
	if len(annos) == 0 {
		useDefault = true
	}

	ncName, exist := annos[common.AnnotationVPCNetworkConfig]
	if !exist {
		useDefault = true
	}

	if useDefault {
		nc, err := s.GetDefaultNetworkConfig()
		if err != nil {
			log.Error(err, "Can not find default NetworkConfig", "Namespace", ns)
			return "", err
		}
		return nc.Name, nil
	}
	return ncName, nil
}

func (s *VPCService) GetDefaultSNATIP(vpc model.Vpc) (string, error) {
	ruleClient := s.NSXClient.NATRuleClient
	info, err := common.ParseVPCResourcePath(*vpc.Path)
	if err != nil {
		log.Error(err, "Failed to parse VPC path to get default SNAT ip", "Path", vpc.Path)
		return "", err
	}
	var cursor *string
	// TODO: support scale scenario
	pageSize := int64(1000)
	markedForDelete := false
	results, err := ruleClient.List(info.OrgID, info.ProjectID, info.VPCID, common.DefaultSNATID, cursor, &markedForDelete, nil, &pageSize, nil, nil)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to read SNAT rule list to get default SNAT ip", "VPC", vpc.Id)
		return "", err
	}

	if results.Results == nil || len(results.Results) == 0 {
		log.Info("No SNAT rule found under VPC", "VPC", vpc.Id)
		return "", nil
	}

	// if there is multiple private ip block in vpc, there will also be multiple snat rules, but they are using
	// the same snat ip, so just using the first snat rule to get snat ip.
	return *results.Results[0].TranslatedNetwork, nil
}

func (s *VPCService) GetAVISubnetInfo(vpc model.Vpc) (string, string, error) {
	subnetsClient := s.NSXClient.SubnetsClient
	statusClient := s.NSXClient.SubnetStatusClient
	info, err := common.ParseVPCResourcePath(*vpc.Path)
	if err != nil {
		return "", "", err
	}

	subnet, err := subnetsClient.Get(info.OrgID, info.ProjectID, info.VPCID, common.AVISubnetLBID)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to read AVI subnet", "VPC", vpc.Id)
		return "", "", err
	}
	path := *subnet.Path

	statusList, err := statusClient.List(info.OrgID, info.ProjectID, info.VPCID, common.AVISubnetLBID)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to read AVI subnet status", "VPC", vpc.Id)
		return "", "", err
	}

	if len(statusList.Results) == 0 {
		log.Info("AVI subnet status not found", "VPC", vpc.Id)
		return "", "", err
	}

	if statusList.Results[0].NetworkAddress == nil {
		err := fmt.Errorf("invalid status result: %+v", statusList.Results[0])
		log.Error(err, "Subnet status does not have network address", "Subnet", common.AVISubnetLBID)
		return "", "", err
	}

	cidr := *statusList.Results[0].NetworkAddress
	log.Info("Read AVI subnet properties", "Path", path, "CIDR", cidr)
	return path, cidr, nil
}

func (s *VPCService) GetNSXLBSNATIP(vpc model.Vpc, interfaceID string) (string, error) {
	log.Info("Getting VPC NSX LB SNAT IP", "VPC", *vpc.Id)
	vpcInfo, err := common.ParseVPCResourcePath(*vpc.Path)
	if err != nil {
		return "", err
	}

	realizeService := realizestate.InitializeRealizeState(s.Service)
	realizedPath := fmt.Sprintf("/orgs/%s/projects/%s/realized-state/enforcement-points/default/vpcs/%s/%s",
		vpcInfo.OrgID, vpcInfo.ProjectID, vpcInfo.VPCID, interfaceID)
	log.Info("Getting VPC NSX LB SNAT IP", "Path", realizedPath)
	lbIP, err := realizeService.GetPolicyInterfaceIP(realizedPath)
	if err != nil {
		log.Error(err, "Failed to get VPC NSX LB SNAT IP", "VPC", *vpc.Id)
		return "", err
	}
	log.Info("Getting VPC NSX LB SNAT IP", "VPC", *vpc.Id, "SNATIP", lbIP)
	return lbIP, nil
}

func (s *VPCService) GetVpcConnectivityProfile(nc *v1alpha1.VPCNetworkConfiguration, vpcConnectivityProfilePath string) (*model.VpcConnectivityProfile, error) {
	parts := strings.Split(vpcConnectivityProfilePath, "/")
	if len(parts) < 1 {
		return nil, fmt.Errorf("failed to check VPCConnectivityProfile(%s) length", nc.Spec.VPCConnectivityProfile)
	}
	vpcConnectivityProfileName := parts[len(parts)-1]
	org, project, err := common.NSXProjectPathToId(nc.Spec.NSXProject)
	if err != nil {
		log.Error(err, "Failed to parse NSX project in NetworkConfig", "ProjectPath", nc.Spec.NSXProject)
		return nil, err
	}
	vpcConnectivityProfile, err := s.Service.NSXClient.VPCConnectivityProfilesClient.Get(org, project, vpcConnectivityProfileName)
	if err != nil {
		log.Error(err, "Failed to get NSX VPCConnectivityProfile object", "vpcConnectivityProfileName", vpcConnectivityProfileName)
		return nil, err
	}
	return &vpcConnectivityProfile, nil
}

/*
IsLBProviderChanged is used to judge if the lb provider is changed from day0 to day2

	The lb provider is allowed to be NoneLB in day0, and changed to AIVLB or NSXLB in day2
	return true if the lb provider is changed
*/
func (s *VPCService) IsLBProviderChanged(existingVPC *model.Vpc, lbProvider LBProvider) bool {
	if existingVPC == nil {
		return false
	}
	if lbProvider == AVILB {
		if existingVPC.LoadBalancerVpcEndpoint == nil || existingVPC.LoadBalancerVpcEndpoint.Enabled == nil ||
			!*existingVPC.LoadBalancerVpcEndpoint.Enabled {
			log.Info("Need to update AVI LoadBalancerVpcEndpoint")
			return true
		}
	}
	if lbProvider == NSXLB {
		pathInfo, _ := common.ParseVPCResourcePath(*existingVPC.Path)
		lbs := s.LbsStore.GetByKey(pathInfo.VPCID)
		if lbs == nil {
			return true
		}
	}
	return false
}

func (s *VPCService) CreateOrUpdateVPC(ctx context.Context, obj *v1alpha1.NetworkInfo, nc *v1alpha1.VPCNetworkConfiguration, lbProvider LBProvider, serviceClusterReady bool, restoreMode bool) (*model.Vpc, error) {
	// parse project path in NetworkConfig
	org, project, err := common.NSXProjectPathToId(nc.Spec.NSXProject)
	if err != nil {
		log.Error(err, "Failed to parse NSX project in NetworkConfig", "ProjectPath", nc.Spec.NSXProject)
		return nil, err
	}
	// check from VPC store if VPC already exist
	ns := obj.Namespace
	nsObj := &v1.Namespace{}
	// get Namespace
	if err := s.Client.Get(ctx, types.NamespacedName{Name: obj.Namespace}, nsObj); err != nil {
		log.Error(err, "Unable to fetch Namespace", "Name", obj.Namespace)
		return nil, err
	}

	// Return pre-created VPC resource if it is used in the VPCNetworkConfiguration
	if nc != nil && IsPreCreatedVPC(nc) {
		preVPC, err := s.GetVPCFromNSXByPath(nc.Spec.VPC)
		if err != nil {
			log.Error(err, "Failed to get existing VPC from NSX", "vpcPath", nc.Spec.VPC)
			return nil, err
		}
		return preVPC, nil
	}

	// check if this namespace vpc share from others, if yes
	// then check if the shared vpc created or not, if yes
	// then directly return this vpc, if not, requeue
	isShared, err := s.IsSharedVPCNamespaceByNS(ctx, ns)
	if err != nil {
		return nil, err
	}

	existingVPC := s.GetCurrentVPCsByNamespace(ctx, ns)
	updateVpc := len(existingVPC) != 0
	if updateVpc && isShared { // We now consider only one VPC for one namespace
		log.Info("The shared VPC already exist", "Namespace", ns)
		return existingVPC[0], nil
	} else if isShared {
		return nil, fmt.Errorf("the shared VPC is not created yet, Namespace %s", ns)
	}

	// if all private ip blocks are created, then create nsx vpc resource.
	var nsxVPC *model.Vpc
	if updateVpc {
		log.Info("VPC already exist on NSX, updating NSX VPC object", "VPC", existingVPC[0].Id, "Name", existingVPC[0].DisplayName)
		nsxVPC = existingVPC[0]
	} else {
		log.Info("VPC does not exist on NSX, creating VPC", "VPC", obj.Name)
	}

	lbProviderChanged := s.IsLBProviderChanged(nsxVPC, lbProvider)
	createdVpc, err := s.buildNSXVPC(obj, nsObj, *nc, s.NSXConfig.Cluster, nsxVPC, lbProvider == AVILB, lbProviderChanged, serviceClusterReady)
	if err != nil {
		log.Error(err, "Failed to build NSX VPC object")
		return nil, err
	}

	// if there is no change in public cidr and private cidr, build partial vpc will return nil
	if createdVpc == nil {
		log.Info("No VPC changes detect, skip create/update process")
		return existingVPC[0], nil
	}

	// build NSX LBS
	var createdLBS *model.LBService
	if lbProvider == NSXLB {
		lbsSize := s.NSXConfig.NsxConfig.GetNSXLBSize()
		vpcPath := fmt.Sprintf(common.VPCKey, org, project, nc.Name)
		var relaxScaleValidation *bool
		if s.NSXConfig.NsxConfig.RelaxNSXLBScaleValication {
			relaxScaleValidation = common.Bool(true)
		}
		createdLBS, _ = buildNSXLBS(obj, nsObj, s.NSXConfig.Cluster, lbsSize, vpcPath, relaxScaleValidation)
	}
	// build HAPI request
	createdAttachment, _ := buildVpcAttachment(obj, nsObj, s.NSXConfig.Cluster, nc.Spec.VPCConnectivityProfile, restoreMode)
	orgRoot, err := s.WrapHierarchyVPC(org, project, createdVpc, createdLBS, createdAttachment)
	if err != nil {
		log.Error(err, "Failed to build HAPI request")
		return nil, err
	}

	if err := s.createNSXVPC(createdVpc, nc, orgRoot); err != nil {
		return nil, err
	}

	// get the created vpc from nsx, it contains the path of the resources
	newVpc, err := s.NSXClient.VPCClient.Get(org, project, *createdVpc.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		// failed to read, but already created, we consider this scenario as success, but store may not sync with nsx
		log.Error(err, "Failed to read VPC object after creating or updating", "VPC", createdVpc.Id)
		return nil, err
	}

	// Check VPC realization state
	if err := s.checkVPCRealizationState(createdVpc, *newVpc.Path); err != nil {
		return nil, err
	}

	if err := s.VpcStore.Add(&newVpc); err != nil {
		return nil, err
	}

	// Check LBS realization
	if err := s.checkLBSRealization(createdLBS, createdVpc, nc, *newVpc.Path); err != nil {
		return nil, err
	}

	// Check VpcAttachment realization
	if err := s.checkVpcAttachmentRealization(createdAttachment, createdVpc, nc, *newVpc.Path); err != nil {
		return nil, err
	}

	return &newVpc, nil
}

func (s *VPCService) createNSXVPC(createdVpc *model.Vpc, nc *v1alpha1.VPCNetworkConfiguration, orgRoot *model.OrgRoot) error {
	log.Info("Creating NSX VPC", "VPC", *createdVpc.Id)
	org, project, err := common.NSXProjectPathToId(nc.Spec.NSXProject)
	if err != nil {
		log.Error(err, "Failed to parse NSX project in NetworkConfig", "ProjectPath", nc.Spec.NSXProject)
		return err
	}
	err = s.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to create VPC", "Project", project, "VPC", createdVpc.DisplayName)
		// TODO: this seems to be a nsx bug, in some case, even if nsx returns failed but the object is still created.
		log.Info("Failed to read VPC although VPC creation", "VPC", *createdVpc.Id)
		failedVpc, rErr := s.NSXClient.VPCClient.Get(org, project, *createdVpc.Id)
		rErr = nsxutil.TransNSXApiError(rErr)
		if rErr != nil {
			// failed to read, but already created, we consider this scenario as success, but store may not sync with nsx
			log.Info("Confirmed VPC is not created", "VPC", createdVpc.Id)
			return err
		} else {
			// vpc created anyway, in this case, we consider this vpc is created successfully and continue to realize process
			log.Info("VPC created although NSX return error, continue to check realization", "VPC", *failedVpc.Id)
		}
	}
	return nil
}

func (s *VPCService) checkVPCRealizationState(createdVpc *model.Vpc, newVpcPath string) error {
	log.V(2).Info("Check VPC realization state", "VPC", *createdVpc.Id)
	realizeService := realizestate.InitializeRealizeState(s.Service)
	if err := realizeService.CheckRealizeState(util.NSXTRealizeRetry, newVpcPath, []string{common.GatewayInterfaceId}); err != nil {
		log.Error(err, "Failed to check VPC realization state", "VPC", *createdVpc.Id)
		if nsxutil.IsRealizeStateError(err) {
			log.Error(err, "The created VPC is in error realization state, cleaning the resource", "VPC", *createdVpc.Id)
			// delete the nsx vpc object and re-create it in the next loop
			if err := s.DeleteVPC(newVpcPath); err != nil {
				log.Error(err, "Cleanup VPC failed", "VPC", *createdVpc.Id)
				return err
			}
		}
		return err
	}
	return nil
}

func (s *VPCService) checkLBSRealization(createdLBS *model.LBService, createdVpc *model.Vpc, nc *v1alpha1.VPCNetworkConfiguration, newVpcPath string) error {
	if createdLBS == nil {
		return nil
	}
	org, project, err := common.NSXProjectPathToId(nc.Spec.NSXProject)
	if err != nil {
		log.Error(err, "Failed to parse NSX project in NetworkConfig", "ProjectPath", nc.Spec.NSXProject)
		return err
	}
	newLBS, err := s.NSXClient.VPCLBSClient.Get(org, project, *createdVpc.Id, *createdLBS.Id)
	if err != nil || newLBS.ConnectivityPath == nil {
		log.Error(err, "Failed to read LBS object after creating or updating", "LBS", createdLBS.Id)
		if err == nil {
			err = fmt.Errorf("ConnectivityPath in LBS is empty")
		}
		return err
	}
	s.LbsStore.Add(&newLBS)

	log.V(2).Info("Check LBS realization state", "LBS", *createdLBS.Id)
	realizeService := realizestate.InitializeRealizeState(s.Service)
	if err = realizeService.CheckRealizeState(util.NSXTRealizeRetry, *newLBS.Path, []string{}); err != nil {
		log.Error(err, "Failed to check LBS realization state", "LBS", *createdLBS.Id)
		if nsxutil.IsRealizeStateError(err) {
			log.Error(err, "The created LBS is in error realization state, cleaning the resource", "LBS", *createdLBS.Id)
			// delete the nsx vpc object and re-create it in the next loop
			if err := s.DeleteVPC(newVpcPath); err != nil {
				log.Error(err, "Cleanup VPC failed", "VPC", *createdVpc.Id)
				return err
			}
		}
		return err
	}
	return nil
}

func (s *VPCService) checkVpcAttachmentRealization(createdAttachment *model.VpcAttachment, createdVpc *model.Vpc, nc *v1alpha1.VPCNetworkConfiguration, newVpcPath string) error {
	if createdAttachment == nil {
		return nil
	}
	org, project, err := common.NSXProjectPathToId(nc.Spec.NSXProject)
	if err != nil {
		log.Error(err, "Failed to parse NSX project in NetworkConfig", "ProjectPath", nc.Spec.NSXProject)
		return err
	}
	newAttachment, err := s.NSXClient.VpcAttachmentClient.Get(org, project, *createdVpc.Id, *createdAttachment.Id)
	if err != nil || newAttachment.VpcConnectivityProfile == nil {
		log.Error(err, "Failed to read VPC attachment object after creating or updating", "VpcAttachment", createdAttachment.Id)
		if err == nil {
			err = fmt.Errorf("VpcConnectivityProfile in VPCAttachment is empty")
		}
		return err
	}
	log.V(2).Info("Check VPC attachment realization state", "VpcAttachment", *createdAttachment.Id)
	realizeService := realizestate.InitializeRealizeState(s.Service)
	if err = realizeService.CheckRealizeState(util.NSXTRealizeRetry, *newAttachment.Path, []string{}); err != nil {
		log.Error(err, "Failed to check VPC attachment realization state", "VpcAttachment", *createdAttachment.Id)
		if nsxutil.IsRealizeStateError(err) {
			log.Error(err, "The created VPC attachment is in error realization state, cleaning the resource", "VpcAttachment", *createdAttachment.Id)
			// delete the nsx vpc object and re-create it in the next loop
			if err := s.DeleteVPC(newVpcPath); err != nil {
				log.Error(err, "Cleanup VPC failed", "VPC", *createdVpc.Id)
				return err
			}
		}
		return err
	}
	return nil
}

func (s *VPCService) GetConnectionTypeFromConnectionPath(connectionPath string) (string, error) {
	/* examples of connection_path:
	   /infra/distributed-vlan-connections/gateway-101
	   /infra/gateway-connections/tenant-1
	*/
	parts := strings.Split(connectionPath, "/")
	if len(parts) != 4 || parts[1] != "infra" {
		return "", fmt.Errorf("unexpected connectionPath %s", connectionPath)
	}
	return parts[2], nil
}

func (s *VPCService) ValidateConnectionStatus(nc *v1alpha1.VPCNetworkConfiguration, profilePath string) (*common.VPCConnectionStatus, error) {
	org, project, err := common.NSXProjectPathToId(nc.Spec.NSXProject)
	if err != nil {
		log.Error(err, "Failed to parse NSX project in NetworkConfig", "ProjectPath", nc.Spec.NSXProject)
		return nil, err
	}

	status := &common.VPCConnectionStatus{
		GatewayConnectionReady:  false,
		GatewayConnectionReason: common.ReasonGatewayConnectionNotSet,
		ServiceClusterReady:     false,
		ServiceClusterReason:    common.ReasonServiceClusterNotSet,
	}

	profile, err := s.GetVpcConnectivityProfile(nc, profilePath)
	if err != nil {
		log.Error(err, "Failed to get VPCConnectivityProfile", "path", profilePath)
		return nil, err
	}

	// If ServiceGateway is not enable, both gateway connection and service cluster are not ready
	if profile.ServiceGateway == nil || profile.ServiceGateway.Enable == nil || !(*profile.ServiceGateway.Enable) || len(profile.ServiceGateway.EdgeClusterPaths) == 0 {
		return status, nil
	}
	// For DTGW, distributed-vlan-connection will be set in TGW connection.
	// For CTGW, gateway-connection will be set in TGW connection.
	var connectionPaths []string // i.e. TGW connection paths
	markedForDelete := false
	transitGatewayPath := *profile.TransitGatewayPath
	parts := strings.Split(transitGatewayPath, "/")
	transitGatewayId := parts[len(parts)-1]
	res, err := s.NSXClient.TransitGatewayAttachmentClient.List(org, project, transitGatewayId, nil, &markedForDelete, nil, nil, nil, nil)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return status, err
	}
	for _, attachment := range res.Results {
		connectionPaths = append(connectionPaths, *attachment.ConnectionPath)
	}
	for _, connectionPath := range connectionPaths {
		connectionType, err := s.GetConnectionTypeFromConnectionPath(connectionPath)
		if err != nil {
			return status, err
		}
		if connectionType == TypeGatewayConnection {
			status.GatewayConnectionReady = true
			status.GatewayConnectionReason = ""
		} else if connectionType == TypeDistributedVlanConnection {
			status.ServiceClusterReady = true
			status.ServiceClusterReason = ""
		}
	}
	return status, nil
}

func (s *VPCService) ListVPCInfo(ns string) []common.VPCResourceInfo {
	var VPCInfoList []common.VPCResourceInfo
	nc, err := s.GetVPCNetworkConfigByNamespace(ns)
	if err != nil {
		log.Error(err, "Failed to Get NetworkConfig by Namespace", "Namespace", ns)
		return VPCInfoList
	}
	// Return the pre-created VPC resource info if it is set in VPCNetworkConfiguration.
	if nc != nil && IsPreCreatedVPC(nc) {
		vpcResourceInfo, err := common.ParseVPCResourcePath(nc.Spec.VPC)
		if err != nil {
			log.Error(err, "Failed to get VPC info from VPC path", "VPCPath", nc.Spec.VPC)
		} else {
			VPCInfoList = append(VPCInfoList, vpcResourceInfo)
		}
		return VPCInfoList
	}

	// List VPCs from local store.
	vpcs := s.GetCurrentVPCsByNamespace(context.Background(), ns)
	log.Info("Got VPCs by Namespace from store", "Namespace", ns, "VPCCount", len(vpcs))
	for _, v := range vpcs {
		vpcResourceInfo, err := common.ParseVPCResourcePath(*v.Path)
		if err != nil {
			log.Error(err, "Failed to get VPC info from VPC path", "VPCPath", *v.Path)
		}
		vpcResourceInfo.PrivateIps = v.PrivateIps
		VPCInfoList = append(VPCInfoList, vpcResourceInfo)
	}
	return VPCInfoList
}

func (s *VPCService) GetDefaultNSXLBSPathByVPC(vpcID string) string {
	vpcLBS := s.LbsStore.GetByKey(vpcID)
	if vpcLBS == nil {
		return ""
	}
	return *vpcLBS.Path
}

func (s *VPCService) GetVpcConnectivityProfileWithRetry(nc *v1alpha1.VPCNetworkConfiguration) (*model.VpcConnectivityProfile, error) {
	isRetryableError := func(err error) bool {
		if err == nil {
			return false
		}
		_, errorType := nsxutil.DumpAPIError(err)
		return errorType != nil && (*errorType == stderrors.ErrorType_SERVICE_UNAVAILABLE || *errorType == stderrors.ErrorType_TIMED_OUT)
	}

	var vpcConnectivityProfile *model.VpcConnectivityProfile
	if err := retry.OnError(retry.DefaultBackoff, isRetryableError, func() error {
		var getErr error
		vpcConnectivityProfile, getErr = s.GetVpcConnectivityProfile(nc, nc.Spec.VPCConnectivityProfile)
		if getErr != nil {
			return getErr
		}
		log.V(1).Info("VPC connectivity profile retrieved", "profile", *vpcConnectivityProfile)
		return nil
	}); err != nil {
		log.Error(err, "Failed to retrieve VPC connectivity profile", "profile", nc.Spec.VPCConnectivityProfile)
		return nil, err
	}
	return vpcConnectivityProfile, nil
}

func GetAlbEndpoint(cluster *nsx.Cluster) error {
	_, err := cluster.HttpGet(albEndpointPath)
	return err
}

func IsEnableAutoSNAT(vpcConnectivityProfile *model.VpcConnectivityProfile) bool {
	if vpcConnectivityProfile.ServiceGateway == nil || vpcConnectivityProfile.ServiceGateway.Enable == nil {
		return false
	}
	if *vpcConnectivityProfile.ServiceGateway.Enable {
		if vpcConnectivityProfile.ServiceGateway.NatConfig == nil || vpcConnectivityProfile.ServiceGateway.NatConfig.EnableDefaultSnat == nil {
			return false
		}
		return *vpcConnectivityProfile.ServiceGateway.NatConfig.EnableDefaultSnat
	}
	return false
}

func (s *VPCService) GetLBProvider() (LBProvider, error) {
	lbProviderMutex.Lock()
	defer lbProviderMutex.Unlock()
	if globalLbProvider != NoneLB {
		log.V(1).Info("LB provider", "current provider", globalLbProvider)
		return globalLbProvider, nil
	}

	ncName := common.SystemVPCNetworkConfigurationName
	netConfig, found, err := s.GetVPCNetworkConfig(ncName)
	if err != nil {
		return "", err
	}
	if !found {
		log.Info("Get LB provider", "No system NetworkConfig found", ncName)
		return NoneLB, nil
	}

	vpcConnectivityProfile, err := s.GetVpcConnectivityProfileWithRetry(netConfig)
	if err != nil {
		return "", err
	}

	// We check EnableAutoSNAT because it is the last step when enabling the service gateway
	serviceGatewayEnable := IsEnableAutoSNAT(vpcConnectivityProfile)
	globalLbProvider = s.getLBProvider(serviceGatewayEnable)
	log.Info("Get LB provider", "provider", globalLbProvider)
	return globalLbProvider, nil
}

func (s *VPCService) getLBProvider(edgeEnable bool) LBProvider {
	// if no Alb endpoint found, return nsx-lb
	// if found, and nsx lbs found, return nsx-lb
	// else return avi
	log.Info("Checking lb provider")
	if s.Service.NSXConfig.UseAVILoadBalancer {
		albEndpointFound := false
		if err := retry.OnError(retry.DefaultBackoff, func(err error) bool {
			if err == nil {
				return false
			}
			if errors.Is(err, nsxutil.HttpCommonError) {
				return true
			} else {
				return false
			}
		}, func() error {
			return GetAlbEndpoint(s.Service.NSXClient.Cluster)
		}); err == nil {
			albEndpointFound = true
		}
		if albEndpointFound && len(s.LbsStore.List()) == 0 {
			return AVILB
		}
	}
	if edgeEnable {
		return NSXLB
	}
	return NoneLB
}

func (s *VPCService) GetVPCFromNSXByPath(vpcPath string) (*model.Vpc, error) {
	vpcResInfo, err := common.ParseVPCResourcePath(vpcPath)
	if err != nil {
		log.Error(err, "Failed to parse VPCResourceInfo from the given VPC path", "VPC", vpcPath)
		return nil, err
	}
	vpc, err := s.NSXClient.VPCClient.Get(vpcResInfo.OrgID, vpcResInfo.ProjectID, vpcResInfo.VPCID)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to read VPC object from NSX", "VPC", vpcPath)
		return nil, err
	}

	return &vpc, nil
}

func (s *VPCService) GetLBSsFromNSXByVPC(vpcPath string) (string, error) {
	vpcResInfo, err := common.ParseVPCResourcePath(vpcPath)
	if err != nil {
		log.Error(err, "Failed to parse VPCResourceInfo from the given VPC path", "VPC", vpcPath)
		return "", err
	}
	includeMarkForDeleted := false
	lbs, err := s.NSXClient.VPCLBSClient.List(vpcResInfo.OrgID, vpcResInfo.ProjectID, vpcResInfo.VPCID, nil, &includeMarkForDeleted, nil, nil, nil, nil)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to read LB services in VPC under from NSX", "VPC", vpcPath)
		return "", err
	}

	if len(lbs.Results) == 0 {
		log.V(1).Info("No NSX LB", "VPC Path", vpcPath)
		return "", nil
	}
	lbsPath := *lbs.Results[0].Path
	return lbsPath, nil
}

// GetAllVPCsFromNSX gets all the existing VPCs on NSX. It returns a map, the key is VPC's path, and the
// value is the VPC resource.
func (s *VPCService) GetAllVPCsFromNSX() map[string]model.Vpc {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.VpcBindingType(),
	}}
	query := fmt.Sprintf("(%s:%s)", common.ResourceType, common.ResourceTypeVpc)
	count, searchErr := s.SearchResource("", query, store, nil)
	if searchErr != nil {
		log.Error(searchErr, "Failed to query VPC from NSX", "query", query)
	} else {
		log.V(1).Info("Query VPC", "count", count)
	}
	vpcMap := make(map[string]model.Vpc)
	for _, obj := range store.List() {
		vpc := *obj.(*model.Vpc)
		vpcPath := vpc.Path
		vpcMap[*vpcPath] = vpc
	}
	return vpcMap
}

// GetNamespacesWithPreCreatedVPCs returns a map of the Namespaces which use the pre-created VPCs. The
// key of the map is the Namespace name, and the value is the pre-created VPC path used in the NetworkInfo
// within this Namespace.
func (s *VPCService) GetNamespacesWithPreCreatedVPCs() (map[string]string, error) {
	nsVpcMap := make(map[string]string)

	vpcNetworkConfigList := &v1alpha1.VPCNetworkConfigurationList{}
	err := s.Client.List(context.Background(), vpcNetworkConfigList)
	if err != nil {
		return nil, err
	}

	for _, cfgCR := range vpcNetworkConfigList.Items {
		if IsPreCreatedVPC(&cfgCR) {
			nsList, err := s.GetNamespacesByNetworkconfigName(cfgCR.Name)
			if err != nil {
				return nsVpcMap, err
			}
			for _, ns := range nsList {
				nsVpcMap[ns] = cfgCR.Spec.VPC
			}
		}
	}
	return nsVpcMap, nil
}

func (s *VPCService) cleanupAviSubnetPorts(ctx context.Context) error {
	vpcs := s.ListVPC()
	for _, vpc := range vpcs {
		if err := CleanAviSubnetPorts(ctx, s.NSXClient.Cluster, *vpc.Path); err != nil {
			return err
		}
	}
	return nil
}

func IsPreCreatedVPC(nc *v1alpha1.VPCNetworkConfiguration) bool {
	return nc.Spec.VPC != ""
}

// IsDefaultNSXProject checks if the given project is a default project
func (s *VPCService) IsDefaultNSXProject(orgID, projectID string) (bool, error) {
	proj, err := s.NSXClient.ProjectClient.Get(orgID, projectID, nil)
	if err != nil {
		log.Error(err, "Failed to get project", "ProjectID", projectID)
		return false, err
	}
	if proj.Default != nil && *proj.Default {
		return true, nil
	}
	return false, nil
}
