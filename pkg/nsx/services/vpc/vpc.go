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
	VPCKey          = "/orgs/%s/projects/%s/vpcs/%s"
	albEndpointPath = "policy/api/v1/infra/sites/default/enforcement-points/alb-endpoint"
	NSXLB           = LBProvider("nsx-lb")
	AVILB           = LBProvider("avi")
	NoneLB          = LBProvider("none")
)

var (
	log                       = &logger.Log
	ResourceTypeVPC           = common.ResourceTypeVpc
	NewConverter              = common.NewConverter
	globalLbProvider          = NoneLB
	lbProviderMutex           = &sync.Mutex{}
	MarkedForDelete           = true
	EnforceRevisionCheckParam = false
)

type VPCNetworkInfoStore struct {
	sync.RWMutex
	VPCNetworkConfigMap map[string]common.VPCNetworkConfigInfo
}

type VPCNsNetworkConfigStore struct {
	sync.Mutex
	VPCNSNetworkConfigMap map[string]string
}

type VPCService struct {
	common.Service
	VpcStore                *VPCStore
	LbsStore                *LBSStore
	VPCNetworkConfigStore   VPCNetworkInfoStore
	VPCNSNetworkConfigStore VPCNsNetworkConfigStore
	defaultNetworkConfigCR  *common.VPCNetworkConfigInfo
}

func (s *VPCService) GetDefaultNetworkConfig() (bool, *common.VPCNetworkConfigInfo) {
	if s.defaultNetworkConfigCR == nil {
		return false, nil
	}
	return true, s.defaultNetworkConfigCR
}

func (s *VPCService) RegisterVPCNetworkConfig(ncCRName string, info common.VPCNetworkConfigInfo) {
	s.VPCNetworkConfigStore.Lock()
	s.VPCNetworkConfigStore.VPCNetworkConfigMap[ncCRName] = info
	if info.IsDefault {
		s.defaultNetworkConfigCR = &info
	}
	s.VPCNetworkConfigStore.Unlock()
}

func (s *VPCService) UnregisterVPCNetworkConfig(ncCRName string) {
	s.VPCNetworkConfigStore.Lock()
	delete(s.VPCNetworkConfigStore.VPCNetworkConfigMap, ncCRName)
	s.VPCNetworkConfigStore.Unlock()
}

func (s *VPCService) GetVPCNetworkConfig(ncCRName string) (common.VPCNetworkConfigInfo, bool) {
	nc, exist := s.VPCNetworkConfigStore.VPCNetworkConfigMap[ncCRName]
	return nc, exist
}

func (s *VPCService) RegisterNamespaceNetworkconfigBinding(ns string, ncCRName string) {
	s.VPCNSNetworkConfigStore.Lock()
	s.VPCNSNetworkConfigStore.VPCNSNetworkConfigMap[ns] = ncCRName
	s.VPCNSNetworkConfigStore.Unlock()
}

func (s *VPCService) UnRegisterNamespaceNetworkconfigBinding(ns string) {
	s.VPCNSNetworkConfigStore.Lock()
	delete(s.VPCNSNetworkConfigStore.VPCNSNetworkConfigMap, ns)
	s.VPCNSNetworkConfigStore.Unlock()
}

// GetNamespacesByNetworkconfigName find the namespace list which is using the given network configuration
func (s *VPCService) GetNamespacesByNetworkconfigName(nc string) []string {
	var result []string
	for key, value := range s.VPCNSNetworkConfigStore.VPCNSNetworkConfigMap {
		if value == nc {
			result = append(result, key)
		}
	}
	return result
}

func (s *VPCService) GetVPCNetworkConfigByNamespace(ns string) *common.VPCNetworkConfigInfo {
	ncName, nameExist := s.VPCNSNetworkConfigStore.VPCNSNetworkConfigMap[ns]
	if !nameExist {
		log.Info("Failed to get network config name for Namespace", "Namespace", ns)
		return nil
	}

	nc, ncExist := s.GetVPCNetworkConfig(ncName)
	if !ncExist {
		log.Info("Failed to get network config info using network config name", "Name", ncName)
		return nil
	}
	return &nc
}

// TBD: for now, if network config info do not contains private cidr, we consider this is
// incorrect configuration, and skip creating this VPC CR
func (s *VPCService) ValidateNetworkConfig(nc common.VPCNetworkConfigInfo) bool {
	if IsPreCreatedVPC(nc) {
		// if network config is using a pre-created VPC, skip the check on PrivateIPs.
		return true
	}
	return nc.PrivateIPs != nil && len(nc.PrivateIPs) != 0
}

// InitializeVPC sync NSX resources
func InitializeVPC(service common.Service) (*VPCService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	VPCService := &VPCService{Service: service}
	VPCService.VpcStore = &VPCStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeNamespaceUID: vpcIndexNamespaceIDFunc,
			common.TagScopeNamespace:    vpcIndexNamespaceNameFunc,
		}),
		BindingType: model.VpcBindingType(),
	}}
	VPCService.LbsStore = &LBSStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.LBServiceBindingType(),
	}}

	VPCService.VPCNetworkConfigStore = VPCNetworkInfoStore{
		VPCNetworkConfigMap: make(map[string]common.VPCNetworkConfigInfo),
	}
	VPCService.VPCNSNetworkConfigStore = VPCNsNetworkConfigStore{
		VPCNSNetworkConfigMap: make(map[string]string),
	}

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

func (s *VPCService) addClusterTag(query string) string {
	tagScopeClusterKey := strings.Replace(common.TagScopeNCPCluster, "/", "\\/", -1)
	tagScopeClusterValue := strings.Replace(s.NSXClient.NsxConfig.Cluster, ":", "\\:", -1)
	tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagScopeClusterKey, tagScopeClusterValue)
	return query + " AND " + tagParam
}

func (s *VPCService) addNCPCreatedForTag(query string) string {
	tagScopeClusterKey := strings.Replace(common.TagScopeNCPCreateFor, "/", "\\/", -1)
	tagScopeClusterValue := strings.Replace(common.TagValueSLB, ":", "\\:", -1)
	tagParam := fmt.Sprintf("tags.scope:%s AND tags.tag:%s", tagScopeClusterKey, tagScopeClusterValue)
	return query + " AND " + tagParam
}

func (s *VPCService) ListCert() []model.TlsCertificate {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.TlsCertificateBindingType(),
	}}
	query := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeTlsCertificate)
	query = s.addClusterTag(query)
	count, searchErr := s.SearchResource("", query, store, nil)
	if searchErr != nil {
		log.Error(searchErr, "Failed to query certificate", "query", query)
	} else {
		log.V(1).Info("Query certificate", "count", count)
	}
	certs := store.List()
	var certsSet []model.TlsCertificate
	for _, cert := range certs {
		certsSet = append(certsSet, *cert.(*model.TlsCertificate))
	}
	return certsSet
}

func (s *VPCService) DeleteCert(id string) error {
	certClient := s.NSXClient.CertificateClient
	if err := certClient.Delete(id); err != nil {
		return err
	}
	log.Info("Successfully deleted NCP created certificate", "certificate", id)
	return nil
}

func (s *VPCService) ListShare() []model.Share {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.ShareBindingType(),
	}}
	query := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeShare)
	query = s.addClusterTag(query)
	count, searchErr := s.SearchResource("", query, store, nil)
	if searchErr != nil {
		log.Error(searchErr, "Failed to query share", "query", query)
	} else {
		log.V(1).Info("Query share", "count", count)
	}
	shares := store.List()
	var sharesSet []model.Share
	for _, cert := range shares {
		sharesSet = append(sharesSet, *cert.(*model.Share))
	}
	return sharesSet
}

func (s *VPCService) DeleteShare(shareId string) error {
	shareClient := s.NSXClient.ShareClient
	if err := shareClient.Delete(shareId); err != nil {
		return err
	}
	log.Info("Successfully deleted NCP created share", "share", shareId)
	return nil
}

func (s *VPCService) ListSharedResource() []model.SharedResource {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.SharedResourceBindingType(),
	}}
	query := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeSharedResource)
	query = s.addClusterTag(query)
	count, searchErr := s.SearchResource("", query, store, nil)
	if searchErr != nil {
		log.Error(searchErr, "Failed to query sharedResource", "query", query)
	} else {
		log.V(1).Info("Query sharedResource", "count", count)
	}
	sharedResources := store.List()
	var sharedResourcesSet []model.SharedResource
	for _, sharedResource := range sharedResources {
		sharedResourcesSet = append(sharedResourcesSet, *sharedResource.(*model.SharedResource))
	}
	return sharedResourcesSet
}

func (s *VPCService) DeleteSharedResource(shareId, id string) error {
	sharedResourceClient := s.NSXClient.SharedResourceClient
	if err := sharedResourceClient.Delete(shareId, id); err != nil {
		return err
	}
	log.Info("Successfully deleted NCP created sharedResource", "shareId", shareId, "sharedResource", id)
	return nil
}

func (s *VPCService) ListLBAppProfile() []model.LBAppProfile {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.LBAppProfileBindingType(),
	}}
	query := fmt.Sprintf("(%s:%s OR %s:%s OR %s:%s)",
		common.ResourceType, common.ResourceTypeLBHttpProfile,
		common.ResourceType, common.ResourceTypeLBFastTcpProfile,
		common.ResourceType, common.ResourceTypeLBFastUdpProfile)
	query = s.addClusterTag(query)
	count, searchErr := s.SearchResource("", query, store, nil)
	if searchErr != nil {
		log.Error(searchErr, "Failed to query LBAppProfile", "query", query)
	} else {
		log.V(1).Info("Query LBAppProfile", "Count", count)
	}
	lbAppProfiles := store.List()
	var lbAppProfilesSet []model.LBAppProfile
	for _, lbAppProfile := range lbAppProfiles {
		lbAppProfilesSet = append(lbAppProfilesSet, *lbAppProfile.(*model.LBAppProfile))
	}
	return lbAppProfilesSet
}

func (s *VPCService) DeleteLBAppProfile(id string) error {
	lbAppProfileClient := s.NSXClient.LbAppProfileClient
	boolValue := false
	if err := lbAppProfileClient.Delete(id, &boolValue); err != nil {
		return err
	}
	log.Info("Successfully deleted NCP created lbAppProfile", "lbAppProfile", id)
	return nil
}

func (s *VPCService) ListLBPersistenceProfile() []model.LBPersistenceProfile {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.LBPersistenceProfileBindingType(),
	}}
	query := fmt.Sprintf("(%s:%s OR %s:%s)",
		common.ResourceType, common.ResourceTypeLBCookiePersistenceProfile,
		common.ResourceType, common.ResourceTypeLBSourceIpPersistenceProfile)
	query = s.addClusterTag(query)
	count, searchErr := s.SearchResource("", query, store, nil)
	if searchErr != nil {
		log.Error(searchErr, "Failed to query LBPersistenceProfile", "query", query)
	} else {
		log.V(1).Info("Query LBPersistenceProfile", "count", count)
	}
	lbPersistenceProfiles := store.List()
	var lbPersistenceProfilesSet []model.LBPersistenceProfile
	for _, lbPersistenceProfile := range lbPersistenceProfiles {
		lbPersistenceProfilesSet = append(lbPersistenceProfilesSet, *lbPersistenceProfile.(*model.LBPersistenceProfile))
	}
	return lbPersistenceProfilesSet
}

func (s *VPCService) DeleteLBPersistenceProfile(id string) error {
	lbPersistenceProfilesClient := s.NSXClient.LbPersistenceProfilesClient
	boolValue := false
	if err := lbPersistenceProfilesClient.Delete(id, &boolValue); err != nil {
		return err
	}
	log.Info("Successfully deleted NCP created lbPersistenceProfile", "lbPersistenceProfile", id)
	return nil
}

func (s *VPCService) ListLBMonitorProfile() []model.LBMonitorProfile {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.LBMonitorProfileBindingType(),
	}}
	query := fmt.Sprintf("(%s:%s OR %s:%s)",
		common.ResourceType, common.ResourceTypeLBHttpMonitorProfile,
		common.ResourceType, common.ResourceTypeLBTcpMonitorProfile)
	query = s.addClusterTag(query)
	count, searchErr := s.SearchResource("", query, store, nil)
	if searchErr != nil {
		log.Error(searchErr, "Failed to query LBMonitorProfile", "query", query)
	} else {
		log.V(1).Info("Query LBMonitorProfile", "count", count)
	}
	lbMonitorProfiles := store.List()
	var lbMonitorProfilesSet []model.LBMonitorProfile
	for _, lbMonitorProfile := range lbMonitorProfiles {
		lbMonitorProfilesSet = append(lbMonitorProfilesSet, *lbMonitorProfile.(*model.LBMonitorProfile))
	}
	return lbMonitorProfilesSet
}

func (s *VPCService) DeleteLBMonitorProfile(id string) error {
	lbMonitorProfilesClient := s.NSXClient.LbMonitorProfilesClient
	boolValue := false
	//nolint:staticcheck // SA1019 ignore this!
	if err := lbMonitorProfilesClient.Delete(id, &boolValue); err != nil {
		return err
	}
	log.Info("Successfully deleted NCP created lbMonitorProfile", "lbMonitorProfile", id)
	return nil
}

func (s *VPCService) ListLBVirtualServer() []model.LBVirtualServer {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.LBVirtualServerBindingType(),
	}}
	query := fmt.Sprintf("(%s:%s)",
		common.ResourceType, common.ResourceTypeLBVirtualServer)
	query = s.addClusterTag(query)
	query = s.addNCPCreatedForTag(query)
	count, searchErr := s.SearchResource("", query, store, nil)
	if searchErr != nil {
		log.Error(searchErr, "Failed to query LBVirtualServer", "query", query)
	} else {
		log.V(1).Info("Query LBVirtualServer", "count", count)
	}
	lbVirtualServers := store.List()
	var lbVirtualServersSet []model.LBVirtualServer
	for _, lbVirtualServer := range lbVirtualServers {
		lbVirtualServersSet = append(lbVirtualServersSet, *lbVirtualServer.(*model.LBVirtualServer))
	}
	return lbVirtualServersSet
}

func (s *VPCService) DeleteLBVirtualServer(path string) error {
	lbVirtualServersClient := s.NSXClient.VpcLbVirtualServersClient
	boolValue := false
	paths := strings.Split(path, "/")

	if len(paths) < common.VPCLbResourcePathMinSegments {
		// skip virtual server under infra
		log.Info("Failed to parse virtual server path", "path", path)
		return nil
	}
	if err := lbVirtualServersClient.Delete(paths[2], paths[4], paths[6], paths[8], &boolValue); err != nil {
		return err
	}
	log.Info("Successfully deleted NCP created lbVirtualServer", "lbVirtualServer", path)
	return nil
}

func (s *VPCService) ListLBPool() []model.LBPool {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.LBPoolBindingType(),
	}}
	query := fmt.Sprintf("(%s:%s)",
		common.ResourceType, common.ResourceTypeLBPool)
	query = s.addClusterTag(query)
	query = s.addNCPCreatedForTag(query)
	count, searchErr := s.SearchResource("", query, store, nil)
	if searchErr != nil {
		log.Error(searchErr, "Failed to query LBPool", "query", query)
	} else {
		log.V(1).Info("Query LBPool", "Count", count)
	}
	lbPools := store.List()
	var lbPoolsSet []model.LBPool
	for _, lbPool := range lbPools {
		lbPoolsSet = append(lbPoolsSet, *lbPool.(*model.LBPool))
	}
	return lbPoolsSet
}

func (s *VPCService) DeleteLBPool(path string) error {
	lbPoolsClient := s.NSXClient.VpcLbPoolsClient
	boolValue := false
	paths := strings.Split(path, "/")
	if len(paths) < 8 {
		// skip lb pool under infra
		log.Info("Failed to parse lb pool path", "path", path)
		return nil
	}
	if err := lbPoolsClient.Delete(paths[2], paths[4], paths[6], paths[8], &boolValue); err != nil {
		return err
	}
	log.Info("Successfully deleted NCP created lbPool", "lbPool", path)
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

	annos := obj.Annotations
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
		exist, nc := s.GetDefaultNetworkConfig()
		if !exist {
			err := errors.New("failed to locate default network config")
			log.Error(err, "Can not find default network config from cache", "Namespace", ns)
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

func (s *VPCService) GetVpcConnectivityProfile(nc *common.VPCNetworkConfigInfo, vpcConnectivityProfilePath string) (*model.VpcConnectivityProfile, error) {
	parts := strings.Split(vpcConnectivityProfilePath, "/")
	if len(parts) < 1 {
		return nil, fmt.Errorf("failed to check VPCConnectivityProfile(%s) length", nc.VPCConnectivityProfile)
	}
	vpcConnectivityProfileName := parts[len(parts)-1]
	vpcConnectivityProfile, err := s.Service.NSXClient.VPCConnectivityProfilesClient.Get(nc.Org, nc.NSXProject, vpcConnectivityProfileName)
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
		if existingVPC.LoadBalancerVpcEndpoint.Enabled == nil || !*existingVPC.LoadBalancerVpcEndpoint.Enabled {
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

func (s *VPCService) CreateOrUpdateVPC(ctx context.Context, obj *v1alpha1.NetworkInfo, nc *common.VPCNetworkConfigInfo, lbProvider LBProvider) (*model.Vpc, error) {
	// check from VPC store if VPC already exist
	ns := obj.Namespace
	nsObj := &v1.Namespace{}
	// get Namespace
	if err := s.Client.Get(ctx, types.NamespacedName{Name: obj.Namespace}, nsObj); err != nil {
		log.Error(err, "Unable to fetch Namespace", "Name", obj.Namespace)
		return nil, err
	}

	// Return pre-created VPC resource if it is used in the VPCNetworkConfiguration
	if nc != nil && IsPreCreatedVPC(*nc) {
		preVPC, err := s.GetVPCFromNSXByPath(nc.VPCPath)
		if err != nil {
			log.Error(err, "Failed to get existing VPC from NSX", "vpcPath", nc.VPCPath)
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
	createdVpc, err := buildNSXVPC(obj, nsObj, *nc, s.NSXConfig.Cluster, nsxVPC, lbProvider == AVILB, lbProviderChanged)
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
		vpcPath := fmt.Sprintf(VPCKey, nc.Org, nc.NSXProject, nc.Name)
		var relaxScaleValidation *bool
		if s.NSXConfig.NsxConfig.RelaxNSXLBScaleValication {
			relaxScaleValidation = common.Bool(true)
		}
		createdLBS, _ = buildNSXLBS(obj, nsObj, s.NSXConfig.Cluster, lbsSize, vpcPath, relaxScaleValidation)
	}
	// build HAPI request
	createdAttachment, _ := buildVpcAttachment(obj, nsObj, s.NSXConfig.Cluster, nc.VPCConnectivityProfile)
	orgRoot, err := s.WrapHierarchyVPC(nc.Org, nc.NSXProject, createdVpc, createdLBS, createdAttachment)
	if err != nil {
		log.Error(err, "Failed to build HAPI request")
		return nil, err
	}

	if err := s.createNSXVPC(createdVpc, nc, orgRoot); err != nil {
		return nil, err
	}

	// get the created vpc from nsx, it contains the path of the resources
	newVpc, err := s.NSXClient.VPCClient.Get(nc.Org, nc.NSXProject, *createdVpc.Id)
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

func (s *VPCService) createNSXVPC(createdVpc *model.Vpc, nc *common.VPCNetworkConfigInfo, orgRoot *model.OrgRoot) error {
	log.Info("Creating NSX VPC", "VPC", *createdVpc.Id)
	err := s.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "Failed to create VPC", "Project", nc.NSXProject, "Namespace")
		// TODO: this seems to be a nsx bug, in some case, even if nsx returns failed but the object is still created.
		log.Info("Failed to read VPC although VPC creation", "VPC", *createdVpc.Id)
		failedVpc, rErr := s.NSXClient.VPCClient.Get(nc.Org, nc.NSXProject, *createdVpc.Id)
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
	if err := realizeService.CheckRealizeState(util.NSXTDefaultRetry, newVpcPath); err != nil {
		log.Error(err, "Failed to check VPC realization state", "VPC", *createdVpc.Id)
		if realizestate.IsRealizeStateError(err) {
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

func (s *VPCService) checkLBSRealization(createdLBS *model.LBService, createdVpc *model.Vpc, nc *common.VPCNetworkConfigInfo, newVpcPath string) error {
	if createdLBS == nil {
		return nil
	}
	newLBS, err := s.NSXClient.VPCLBSClient.Get(nc.Org, nc.NSXProject, *createdVpc.Id, *createdLBS.Id)
	if err != nil || newLBS.ConnectivityPath == nil {
		log.Error(err, "Failed to read LBS object after creating or updating", "LBS", createdLBS.Id)
		return err
	}
	s.LbsStore.Add(&newLBS)

	log.V(2).Info("Check LBS realization state", "LBS", *createdLBS.Id)
	realizeService := realizestate.InitializeRealizeState(s.Service)
	if err = realizeService.CheckRealizeState(util.NSXTLBVSDefaultRetry, *newLBS.Path); err != nil {
		log.Error(err, "Failed to check LBS realization state", "LBS", *createdLBS.Id)
		if realizestate.IsRealizeStateError(err) {
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

func (s *VPCService) checkVpcAttachmentRealization(createdAttachment *model.VpcAttachment, createdVpc *model.Vpc, nc *common.VPCNetworkConfigInfo, newVpcPath string) error {
	if createdAttachment == nil {
		return nil
	}
	newAttachment, err := s.NSXClient.VpcAttachmentClient.Get(nc.Org, nc.NSXProject, *createdVpc.Id, *createdAttachment.Id)
	if err != nil || newAttachment.VpcConnectivityProfile == nil {
		log.Error(err, "Failed to read VPC attachment object after creating or updating", "VpcAttachment", createdAttachment.Id)
		return err
	}
	log.V(2).Info("Check VPC attachment realization state", "VpcAttachment", *createdAttachment.Id)
	realizeService := realizestate.InitializeRealizeState(s.Service)
	if err = realizeService.CheckRealizeState(util.NSXTLBVSDefaultRetry, *newAttachment.Path); err != nil {
		log.Error(err, "Failed to check VPC attachment realization state", "VpcAttachment", *createdAttachment.Id)
		if realizestate.IsRealizeStateError(err) {
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

func (s *VPCService) GetGatewayConnectionTypeFromConnectionPath(connectionPath string) (string, error) {
	/* examples of connection_path:
	   /infra/distributed-gateway-connections/gateway-101
	   /infra/gateway-connections/tenant-1
	*/
	parts := strings.Split(connectionPath, "/")
	if len(parts) != 4 || parts[1] != "infra" {
		return "", fmt.Errorf("unexpected connectionPath %s", connectionPath)
	}
	return parts[2], nil
}

func (s *VPCService) ValidateGatewayConnectionStatus(nc *common.VPCNetworkConfigInfo) (bool, string, error) {
	var connectionPaths []string // i.e. gateway connection paths
	var cursor *string
	pageSize := int64(1000)
	markedForDelete := false
	res, err := s.NSXClient.VPCConnectivityProfilesClient.List(nc.Org, nc.NSXProject, cursor, &markedForDelete, nil, &pageSize, nil, nil)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return false, "", err
	}
	for _, profile := range res.Results {
		transitGatewayPath := *profile.TransitGatewayPath
		parts := strings.Split(transitGatewayPath, "/")
		transitGatewayId := parts[len(parts)-1]
		res, err := s.NSXClient.TransitGatewayAttachmentClient.List(nc.Org, nc.NSXProject, transitGatewayId, nil, &markedForDelete, nil, nil, nil, nil)
		err = nsxutil.TransNSXApiError(err)

		if err != nil {
			return false, "", err
		}
		for _, attachment := range res.Results {
			connectionPaths = append(connectionPaths, *attachment.ConnectionPath)
		}
	}
	// Case 1: there's no gateway connection paths.
	if len(connectionPaths) == 0 {
		return false, common.ReasonGatewayConnectionNotSet, nil
	}

	// Case 2: detected distributed gateway connection which is not supported.
	for _, connectionPath := range connectionPaths {
		gatewayConnectionType, err := s.GetGatewayConnectionTypeFromConnectionPath(connectionPath)
		if err != nil {
			return false, "", err
		}
		if gatewayConnectionType != "gateway-connections" {
			return false, common.ReasonDistributedGatewayConnectionNotSupported, nil
		}
	}
	return true, "", nil
}

func (s *VPCService) Cleanup(ctx context.Context) error {
	vpcs := s.ListVPC()
	log.Info("Cleaning up VPCs", "Count", len(vpcs))
	for _, vpc := range vpcs {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			// first clean avi subnet ports, or else vpc delete will fail
			if err := CleanAviSubnetPorts(ctx, s.NSXClient.Cluster, *vpc.Path); err != nil {
				return err
			}

			if err := s.DeleteVPC(*vpc.Path); err != nil {
				return err
			}
		}
	}

	// Delete NCP created resources (share/sharedResources/cert/LBAppProfile/LBPersistentProfile
	sharedResources := s.ListSharedResource()
	log.Info("Cleaning up sharedResources", "Count", len(sharedResources))
	for _, sharedResource := range sharedResources {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			parentPath := strings.Split(*sharedResource.ParentPath, "/")
			shareId := parentPath[len(parentPath)-1]
			if err := s.DeleteSharedResource(shareId, *sharedResource.Id); err != nil {
				return err
			}
		}
	}

	shares := s.ListShare()
	log.Info("Cleaning up shares", "Count", len(shares))
	for _, share := range shares {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			if err := s.DeleteShare(*share.Id); err != nil {
				return err
			}
		}
	}

	certs := s.ListCert()
	log.Info("Cleaning up certificates", "Count", len(certs))
	for _, cert := range certs {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			if err := s.DeleteCert(*cert.Id); err != nil {
				return err
			}
		}
	}

	lbAppProfiles := s.ListLBAppProfile()
	log.Info("Cleaning up lbAppProfiles", "Count", len(lbAppProfiles))
	for _, lbAppProfile := range lbAppProfiles {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			if err := s.DeleteLBAppProfile(*lbAppProfile.Id); err != nil {
				return err
			}
		}
	}

	lbPersistenceProfiles := s.ListLBPersistenceProfile()
	log.Info("Cleaning up lbPersistenceProfiles", "Count", len(lbPersistenceProfiles))
	for _, lbPersistenceProfile := range lbPersistenceProfiles {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			if err := s.DeleteLBPersistenceProfile(*lbPersistenceProfile.Id); err != nil {
				return err
			}
		}
	}

	lbMonitorProfiles := s.ListLBMonitorProfile()
	log.Info("Cleaning up lbMonitorProfiles", "Count", len(lbMonitorProfiles))
	for _, lbMonitorProfile := range lbMonitorProfiles {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			if err := s.DeleteLBMonitorProfile(*lbMonitorProfile.Id); err != nil {
				return err
			}
		}
	}

	// Clean vs/lb pool created for pre-created vpc
	lbVirtualServers := s.ListLBVirtualServer()
	log.Info("Cleaning up lbVirtualServers", "Count", len(lbVirtualServers))
	for _, lbVirtualServer := range lbVirtualServers {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			if err := s.DeleteLBVirtualServer(*lbVirtualServer.Path); err != nil {
				return err
			}
		}
	}

	lbPools := s.ListLBPool()
	log.Info("Cleaning up lbPools", "Count", len(lbPools))
	for _, lbPool := range lbPools {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			if err := s.DeleteLBPool(*lbPool.Path); err != nil {
				return err
			}
		}
	}
	// We don't clean client_ssl_profile as client_ssl_profile is not created by ncp or nsx-operator
	return nil
}

func (s *VPCService) ListVPCInfo(ns string) []common.VPCResourceInfo {
	var VPCInfoList []common.VPCResourceInfo
	nc := s.GetVPCNetworkConfigByNamespace(ns)
	// Return the pre-created VPC resource info if it is set in VPCNetworkConfiguration.
	if nc != nil && IsPreCreatedVPC(*nc) {
		vpcResourceInfo, err := common.ParseVPCResourcePath(nc.VPCPath)
		if err != nil {
			log.Error(err, "Failed to get vpc info from vpc path", "vpc path", nc.VPCPath)
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
		vpcResourceInfo.PrivateIpv4Blocks = v.PrivateIpv4Blocks
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

func (s *VPCService) EdgeClusterEnabled(nc *common.VPCNetworkConfigInfo) bool {
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
		vpcConnectivityProfile, getErr = s.GetVpcConnectivityProfile(nc, nc.VPCConnectivityProfile)
		if getErr != nil {
			return getErr
		}
		log.V(1).Info("VPC connectivity profile retrieved", "profile", *vpcConnectivityProfile)
		return nil
	}); err != nil {
		log.Error(err, "Failed to retrieve VPC connectivity profile", "profile", nc.VPCConnectivityProfile)
		return false
	}
	return s.IsEnableAutoSNAT(vpcConnectivityProfile)
}

func GetAlbEndpoint(cluster *nsx.Cluster) error {
	_, err := cluster.HttpGet(albEndpointPath)
	return err
}

func (s *VPCService) IsEnableAutoSNAT(vpcConnectivityProfile *model.VpcConnectivityProfile) bool {
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

func (s *VPCService) GetLBProvider() LBProvider {
	lbProviderMutex.Lock()
	defer lbProviderMutex.Unlock()
	if globalLbProvider != NoneLB {
		log.V(1).Info("LB provider", "current provider", globalLbProvider)
		return globalLbProvider
	}

	ncName := common.SystemVPCNetworkConfigurationName
	netConfig, found := s.GetVPCNetworkConfig(ncName)
	if !found {
		log.Info("Get LB provider", "No system network config found", ncName)
		return NoneLB
	}
	nc := &netConfig

	edgeEnable := s.EdgeClusterEnabled(nc)
	globalLbProvider = s.getLBProvider(edgeEnable)
	log.Info("Get LB provider", "provider", globalLbProvider)
	return globalLbProvider
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
func (s *VPCService) GetNamespacesWithPreCreatedVPCs() map[string]string {
	nsVpcMap := make(map[string]string)
	for ncName, cfg := range s.VPCNetworkConfigStore.VPCNetworkConfigMap {
		if IsPreCreatedVPC(cfg) {
			for _, ns := range s.GetNamespacesByNetworkconfigName(ncName) {
				nsVpcMap[ns] = cfg.VPCPath
			}
		}
	}
	return nsVpcMap
}

func IsPreCreatedVPC(nc common.VPCNetworkConfigInfo) bool {
	return nc.VPCPath != ""
}
