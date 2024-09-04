package vpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

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

const (
	VpcDefaultSecurityPolicyId = "default-layer3-section"
	VPCKey                     = "/orgs/%s/projects/%s/vpcs/%s"
	GroupKey                   = "/orgs/%s/projects/%s/vpcs/%s/groups/%s"
	SecurityPolicyKey          = "/orgs/%s/projects/%s/vpcs/%s/security-policies/%s"
	RuleKey                    = "/orgs/%s/projects/%s/vpcs/%s/security-policies/%s/rules/%s"
	albEndpointPath            = "policy/api/v1/infra/sites/default/enforcement-points/alb-endpoint"
	LBProviderNSX              = "nsx-lb"
	LBProviderAVI              = "avi"
)

var (
	log                       = &logger.Log
	ctx                       = context.Background()
	ResourceTypeVPC           = common.ResourceTypeVpc
	NewConverter              = common.NewConverter
	lbProvider                = ""
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
	IpblockStore            *IPBlockStore
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

// find the namespace list which is using the given network configuration
func (s *VPCService) GetNamespacesByNetworkconfigName(nc string) []string {
	result := []string{}
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
		log.Info("failed to get network config name for namespace", "Namespace", ns)
		return nil
	}

	nc, ncExist := s.GetVPCNetworkConfig(ncName)
	if !ncExist {
		log.Info("failed to get network config info using network config name", "Name", ncName)
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
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.VpcBindingType(),
	}}
	VPCService.LbsStore = &LBSStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.LBServiceBindingType(),
	}}

	VPCService.IpblockStore = &IPBlockStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.IndexKeyPathPath: indexPathFunc}),
		BindingType: model.IpAddressBlockBindingType(),
	}}
	VPCService.VPCNetworkConfigStore = VPCNetworkInfoStore{
		VPCNetworkConfigMap: make(map[string]common.VPCNetworkConfigInfo),
	}
	VPCService.VPCNSNetworkConfigStore = VPCNsNetworkConfigStore{
		VPCNSNetworkConfigMap: make(map[string]string),
	}
	// initialize vpc store, lbs store and ip blocks store
	go VPCService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeVpc, nil, VPCService.VpcStore)
	go VPCService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeLBService, nil, VPCService.LbsStore)
	go VPCService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeIPBlock, nil, VPCService.IpblockStore)
	wg.Add(3)
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

func (s *VPCService) GetVPCsByNamespace(namespace string) []*model.Vpc {
	sns, err := s.getSharedVPCNamespaceFromNS(namespace)
	if err != nil {
		log.Error(err, "Failed to get namespace.")
		return nil
	}
	return s.VpcStore.GetVPCsByNamespace(util.If(sns == "", namespace, sns).(string))
}

func (s *VPCService) ListVPC() []model.Vpc {
	vpcs := s.VpcStore.List()
	vpcSet := []model.Vpc{}
	for _, vpc := range vpcs {
		vpcSet = append(vpcSet, *vpc.(*model.Vpc))
	}
	return vpcSet
}

func (s *VPCService) DeleteVPC(path string) error {
	pathInfo, err := common.ParseVPCResourcePath(path)
	if err != nil {
		return err
	}
	vpcClient := s.NSXClient.VPCClient
	vpc := s.VpcStore.GetByKey(pathInfo.VPCID)
	if vpc == nil {
		return nil
	}

	if err := vpcClient.Delete(pathInfo.OrgID, pathInfo.ProjectID, pathInfo.VPCID); err != nil {
		err = nsxutil.NSXApiError(err)
		return err
	}
	lbs := s.LbsStore.GetByKey(pathInfo.VPCID)
	if lbs != nil {
		s.LbsStore.Delete(lbs)
	}
	vpc.MarkedForDelete = &MarkedForDelete
	if err := s.VpcStore.Apply(vpc); err != nil {
		return err
	}

	log.Info("successfully deleted NSX VPC", "VPC", pathInfo.VPCID)
	return nil
}

func (s *VPCService) addClusterTag(query string) string {
	tagScopeClusterKey := strings.Replace(common.TagScopeNCPCluster, "/", "\\/", -1)
	tagScopeClusterValue := strings.Replace(s.NSXClient.NsxConfig.Cluster, ":", "\\:", -1)
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
	count, searcherr := s.SearchResource(common.ResourceTypeTlsCertificate, query, store, nil)
	if searcherr != nil {
		log.Error(searcherr, "failed to query certificate", "query", query)
	} else {
		log.V(1).Info("query certificate", "count", count)
	}
	certs := store.List()
	certsSet := []model.TlsCertificate{}
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
	log.Info("successfully deleted NCP created certificate", "certificate", id)
	return nil
}

func (s *VPCService) ListShare() []model.Share {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.ShareBindingType(),
	}}
	query := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeShare)
	query = s.addClusterTag(query)
	count, searcherr := s.SearchResource(common.ResourceTypeShare, query, store, nil)
	if searcherr != nil {
		log.Error(searcherr, "failed to query share", "query", query)
	} else {
		log.V(1).Info("query share", "count", count)
	}
	shares := store.List()
	sharesSet := []model.Share{}
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
	log.Info("successfully deleted NCP created share", "share", shareId)
	return nil
}

func (s *VPCService) ListSharedResource() []model.SharedResource {
	store := &ResourceStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.SharedResourceBindingType(),
	}}
	query := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeSharedResource)
	query = s.addClusterTag(query)
	count, searcherr := s.SearchResource(common.ResourceTypeSharedResource, query, store, nil)
	if searcherr != nil {
		log.Error(searcherr, "failed to query sharedResource", "query", query)
	} else {
		log.V(1).Info("query sharedResource", "count", count)
	}
	sharedResources := store.List()
	sharedResourcesSet := []model.SharedResource{}
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
	log.Info("successfully deleted NCP created sharedResource", "shareId", shareId, "sharedResource", id)
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
	count, searcherr := s.SearchResource(common.ResourceTypeLBHttpProfile, query, store, nil)
	if searcherr != nil {
		log.Error(searcherr, "failed to query LBAppProfile", "query", query)
	} else {
		log.V(1).Info("query LBAppProfile", "count", count)
	}
	lbAppProfiles := store.List()
	lbAppProfilesSet := []model.LBAppProfile{}
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
	log.Info("successfully deleted NCP created lbAppProfile", "lbAppProfile", id)
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
	count, searcherr := s.SearchResource("", query, store, nil)
	if searcherr != nil {
		log.Error(searcherr, "failed to query LBPersistenceProfile", "query", query)
	} else {
		log.V(1).Info("query LBPersistenceProfile", "count", count)
	}
	lbPersistenceProfiles := store.List()
	lbPersistenceProfilesSet := []model.LBPersistenceProfile{}
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
	log.Info("successfully deleted NCP created lbPersistenceProfile", "lbPersistenceProfile", id)
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
	count, searcherr := s.SearchResource("", query, store, nil)
	if searcherr != nil {
		log.Error(searcherr, "failed to query LBMonitorProfile", "query", query)
	} else {
		log.V(1).Info("query LBMonitorProfile", "count", count)
	}
	lbMonitorProfiles := store.List()
	lbMonitorProfilesSet := []model.LBMonitorProfile{}
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
	log.Info("successfully deleted NCP created lbMonitorProfile", "lbMonitorProfile", id)
	return nil
}

func (s *VPCService) deleteIPBlock(path string) error {
	ipblockClient := s.NSXClient.IPBlockClient
	parts := strings.Split(path, "/")
	log.Info("deleting private ip block", "ORG", parts[2], "Project", parts[4], "ID", parts[7])
	if err := ipblockClient.Delete(parts[2], parts[4], parts[7]); err != nil {
		err = nsxutil.NSXApiError(err)
		log.Error(err, "failed to delete ip block", "Path", path)
		return err
	}
	return nil
}

func (s *VPCService) DeleteIPBlockInVPC(vpc model.Vpc) error {
	blocks := vpc.PrivateIpv4Blocks
	if len(blocks) == 0 {
		log.Info("no private cidr list, skip deleting private ip blocks")
		return nil
	}

	for _, block := range blocks {
		if err := s.deleteIPBlock(block); err != nil {
			return err
		}
		nsUID := ""
		for _, tag := range vpc.Tags {
			if *tag.Scope == common.TagScopeNamespaceUID {
				nsUID = *tag.Tag
			}
		}
		log.V(2).Info("search ip block from store using index and path", "index", common.TagScopeNamespaceUID, "Value", nsUID, "Path", block)
		// using index vpc cr id may get multiple ipblocks, add path to filter the correct one
		ipblock := s.IpblockStore.GetByIndex(common.IndexKeyPathPath, block)
		if ipblock != nil {
			log.Info("deleting ip blocks", "IPBlock", ipblock)
			ipblock.MarkedForDelete = &MarkedForDelete
			s.IpblockStore.Apply(ipblock)
		}
	}
	log.Info("successfully deleted all ip blocks")
	return nil
}

func (s *VPCService) IsSharedVPCNamespaceByNS(ns string) (bool, error) {
	shared_ns, err := s.getSharedVPCNamespaceFromNS(ns)
	if err != nil {
		return false, err
	}
	if shared_ns == "" {
		return false, nil
	}
	if shared_ns != ns {
		return true, nil
	}
	return false, err
}

func (s *VPCService) getSharedVPCNamespaceFromNS(ns string) (string, error) {
	obj := &v1.Namespace{}
	if err := s.Client.Get(ctx, types.NamespacedName{
		Name:      ns,
		Namespace: ns,
	}, obj); err != nil {
		log.Error(err, "failed to fetch namespace", "Namespace", ns)
		return "", err
	}

	annos := obj.Annotations
	// If no annotaion on ns, then this is not a shared VPC ns
	if len(annos) == 0 {
		return "", nil
	}

	// If no annotation nsx.vmware.com/shared_vpc_namespace on ns, this is not a shared vpc
	shared_ns, exist := annos[common.AnnotationSharedVPCNamespace]
	if !exist {
		return "", nil
	}
	return shared_ns, nil
}

func (s *VPCService) GetNetworkconfigNameFromNS(ns string) (string, error) {
	obj := &v1.Namespace{}
	if err := s.Client.Get(ctx, types.NamespacedName{
		Name:      ns,
		Namespace: ns,
	}, obj); err != nil {
		log.Error(err, "failed to fetch namespace", "Namespace", ns)
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
			log.Error(err, "can not find default network config from cache", "Namespace", ns)
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
		log.Error(err, "failed to parse VPC path to get default SNAT ip", "Path", vpc.Path)
		return "", err
	}
	var cursor *string
	// TODO: support scale scenario
	pageSize := int64(1000)
	markedForDelete := false
	results, err := ruleClient.List(info.OrgID, info.ProjectID, info.VPCID, common.DefaultSNATID, cursor, &markedForDelete, nil, &pageSize, nil, nil)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to read SNAT rule list to get default SNAT ip", "VPC", vpc.Id)
		return "", err
	}

	if results.Results == nil || len(results.Results) == 0 {
		log.Info("no SNAT rule found under VPC", "VPC", vpc.Id)
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
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to read AVI subnet", "VPC", vpc.Id)
		return "", "", err
	}
	path := *subnet.Path

	statusList, err := statusClient.List(info.OrgID, info.ProjectID, info.VPCID, common.AVISubnetLBID)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to read AVI subnet status", "VPC", vpc.Id)
		return "", "", err
	}

	if len(statusList.Results) == 0 {
		log.Info("AVI subnet status not found", "VPC", vpc.Id)
		return "", "", err
	}

	if statusList.Results[0].NetworkAddress == nil {
		err := fmt.Errorf("invalid status result: %+v", statusList.Results[0])
		log.Error(err, "subnet status does not have network address", "Subnet", common.AVISubnetLBID)
		return "", "", err
	}

	cidr := *statusList.Results[0].NetworkAddress
	log.Info("read AVI subnet properties", "Path", path, "CIDR", cidr)
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
		log.Error(err, "failed to get NSX VPCConnectivityProfile object", "vpcConnectivityProfileName", vpcConnectivityProfileName)
		return nil, err
	}
	return &vpcConnectivityProfile, nil
}

func (s *VPCService) CreateOrUpdateVPC(obj *v1alpha1.NetworkInfo, nc *common.VPCNetworkConfigInfo) (*model.Vpc, error) {
	// check from VPC store if vpc already exist
	ns := obj.Namespace
	updateVpc := false
	nsObj := &v1.Namespace{}
	// get name obj
	if err := s.Client.Get(ctx, types.NamespacedName{Name: obj.Namespace}, nsObj); err != nil {
		log.Error(err, "unable to fetch namespace", "name", obj.Namespace)
		return nil, err
	}

	// Return pre-created VPC resource if it is used in the VPCNetworkConfiguration
	if IsPreCreatedVPC(*nc) {
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
	isShared, err := s.IsSharedVPCNamespaceByNS(ns)
	if err != nil {
		return nil, err
	}

	existingVPC := s.GetVPCsByNamespace(ns)
	if len(existingVPC) != 0 { // We now consider only one VPC for one namespace
		if isShared {
			log.Info("The shared VPC already exist", "Namespace", ns)
			return existingVPC[0], nil
		}
		updateVpc = true
		log.Info("VPC already exist, updating NSX VPC object", "VPC", existingVPC[0].Id)
	} else if isShared {
		message := fmt.Sprintf("the shared VPC is not created yet, namespace %s", ns)
		return nil, errors.New(message)
	}

	// if all private ip blocks are created, then create nsx vpc resource.
	nsxVPC := &model.Vpc{}
	if updateVpc {
		log.Info("VPC resource already exist on NSX, updating VPC", "VPC", existingVPC[0].DisplayName)
		nsxVPC = existingVPC[0]
	} else {
		log.Info("VPC does not exist on NSX, creating VPC", "VPC", obj.Name)
		nsxVPC = nil
	}

	createdVpc, err := buildNSXVPC(obj, nsObj, *nc, s.NSXConfig.Cluster, nsxVPC, !s.NSXLBEnabled())
	if err != nil {
		log.Error(err, "failed to build NSX VPC object")
		return nil, err
	}

	// if there is no change in public cidr and private cidr, build partial vpc will return nil
	if createdVpc == nil {
		log.Info("no VPC changes detect, skip creating or updating process")
		return existingVPC[0], nil
	}

	// build NSX LBS
	var createdLBS *model.LBService
	if s.NSXLBEnabled() {
		lbsSize := s.NSXConfig.NsxConfig.GetNSXLBSize()
		vpcPath := fmt.Sprintf(VPCKey, nc.Org, nc.NSXProject, nc.Name)
		var relaxScaleValidation *bool
		if s.NSXConfig.NsxConfig.RelaxNSXLBScaleValication {
			relaxScaleValidation = common.Bool(true)
		}
		createdLBS, _ = buildNSXLBS(obj, nsObj, s.NSXConfig.Cluster, lbsSize, vpcPath, relaxScaleValidation)
	}
	// build HAPI request
	orgRoot, err := s.WrapHierarchyVPC(nc.Org, nc.NSXProject, createdVpc, createdLBS)
	if err != nil {
		log.Error(err, "failed to build HAPI request")
		return nil, err
	}

	log.Info("creating NSX VPC", "VPC", *createdVpc.Id)
	err = s.NSXClient.OrgRootClient.Patch(*orgRoot, &EnforceRevisionCheckParam)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to create VPC", "Project", nc.NSXProject, "Namespace", obj.Namespace)
		// TODO: this seems to be a nsx bug, in some case, even if nsx returns failed but the object is still created.
		log.Info("try to read VPC although VPC creation failed", "VPC", *createdVpc.Id)
		failedVpc, rErr := s.NSXClient.VPCClient.Get(nc.Org, nc.NSXProject, *createdVpc.Id)
		rErr = nsxutil.NSXApiError(rErr)
		if rErr != nil {
			// failed to read, but already created, we consider this scenario as success, but store may not sync with nsx
			log.Info("confirmed VPC is not created", "VPC", createdVpc.Id)
			return nil, err
		} else {
			// vpc created anyway, in this case, we consider this vpc is created successfully and continue to realize process
			log.Info("vpc created although nsx return error, continue to check realization", "VPC", *failedVpc.Id)
		}
	}

	// get the created vpc from nsx, it contains the path of the resources
	newVpc, err := s.NSXClient.VPCClient.Get(nc.Org, nc.NSXProject, *createdVpc.Id)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		// failed to read, but already created, we consider this scenario as success, but store may not sync with nsx
		log.Error(err, "failed to read VPC object after creating or updating", "VPC", createdVpc.Id)
		return nil, err
	}

	log.V(2).Info("check VPC realization state", "VPC", *createdVpc.Id)
	realizeService := realizestate.InitializeRealizeState(s.Service)
	if err = realizeService.CheckRealizeState(util.NSXTDefaultRetry, *newVpc.Path, "RealizedLogicalRouter"); err != nil {
		log.Error(err, "failed to check VPC realization state", "VPC", *createdVpc.Id)
		if realizestate.IsRealizeStateError(err) {
			log.Error(err, "the created VPC is in error realization state, cleaning the resource", "VPC", *createdVpc.Id)
			// delete the nsx vpc object and re-create it in the next loop
			// TODO(gran) DeleteVPC will check VpcStore but new Vpc is not in store at this moment. Is it correct?
			if err := s.DeleteVPC(*newVpc.Path); err != nil {
				log.Error(err, "cleanup VPC failed", "VPC", *createdVpc.Id)
				return nil, err
			}
		}
		return nil, err
	}

	s.VpcStore.Add(&newVpc)

	// Check LBS realization
	if createdLBS != nil {
		newLBS, err := s.NSXClient.VPCLBSClient.Get(nc.Org, nc.NSXProject, *createdVpc.Id, *createdLBS.Id)
		if err != nil {
			log.Error(err, "failed to read LBS object after creating or updating", "LBS", createdLBS.Id)
			return nil, err
		}
		s.LbsStore.Add(&newLBS)

		log.V(2).Info("check LBS realization state", "LBS", *createdLBS.Id)
		realizeService := realizestate.InitializeRealizeState(s.Service)
		if err = realizeService.CheckRealizeState(util.NSXTLBVSDefaultRetry, *newLBS.Path, ""); err != nil {
			log.Error(err, "failed to check LBS realization state", "LBS", *createdLBS.Id)
			if realizestate.IsRealizeStateError(err) {
				log.Error(err, "the created LBS is in error realization state, cleaning the resource", "LBS", *createdLBS.Id)
				// delete the nsx vpc object and re-create it in the next loop
				if err := s.DeleteVPC(*newVpc.Path); err != nil {
					log.Error(err, "cleanup VPC failed", "VPC", *createdVpc.Id)
					return nil, err
				}
			}
			return nil, err
		}
	}

	return &newVpc, nil
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
	// Case 1: the project has the full list of edge clusters, so if the project doesn't have edge,
	// we can say that the edge is not deployed.
	var projectEdges []string
	project, err := s.NSXClient.ProjectClient.Get(nc.Org, nc.NSXProject, nil)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		return false, "", err
	}
	for _, siteInfo := range project.SiteInfos {
		projectEdges = append(projectEdges, siteInfo.EdgeClusterPaths...)
	}
	if len(projectEdges) == 0 {
		return false, common.ReasonEdgeMissingInProject, nil
	}

	var connectionPaths []string // i.e. gateway connection paths
	var profiles []model.VpcConnectivityProfile
	var cursor *string
	pageSize := int64(1000)
	markedForDelete := false
	res, err := s.NSXClient.VPCConnectivityProfilesClient.List(nc.Org, nc.NSXProject, cursor, &markedForDelete, nil, &pageSize, nil, nil)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		return false, "", err
	}
	profiles = append(profiles, res.Results...)
	for _, profile := range profiles {
		transitGatewayPath := *profile.TransitGatewayPath
		parts := strings.Split(transitGatewayPath, "/")
		transitGatewayId := parts[len(parts)-1]
		res, err := s.NSXClient.TransitGatewayAttachmentClient.List(nc.Org, nc.NSXProject, transitGatewayId, nil, &markedForDelete, nil, nil, nil, nil)
		err = nsxutil.NSXApiError(err)
		if err != nil {
			return false, "", err
		}
		for _, attachment := range res.Results {
			connectionPaths = append(connectionPaths, *attachment.ConnectionPath)
		}
	}
	// Case 2: there's no gateway connection paths.
	if len(connectionPaths) == 0 {
		return false, common.ReasonGatewayConnectionNotSet, nil
	}

	// Case 3: detected distributed gateway connection which is not supported.
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
	log.Info("cleaning up vpcs", "Count", len(vpcs))
	for _, vpc := range vpcs {
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			// first clean avi subnet ports, or else vpc delete will fail
			if !s.NSXLBEnabled() {
				if err := CleanAviSubnetPorts(ctx, s.NSXClient.Cluster, *vpc.Path); err != nil {
					return err
				}
			}
			if err := s.DeleteVPC(*vpc.Path); err != nil {
				return err
			}
		}
	}

	ipblocks := s.IpblockStore.List()
	log.Info("cleaning up ipblocks", "Count", len(ipblocks))
	for _, ipblock := range ipblocks {
		ipb := ipblock.(*model.IpAddressBlock)
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			if err := s.deleteIPBlock(*ipb.Path); err != nil {
				return err
			}
		}
	}

	// Delete NCP created resources (share/sharedResources/cert/LBAppProfile/LBPersistentProfile
	sharedResources := s.ListSharedResource()
	log.Info("cleaning up sharedResources", "Count", len(sharedResources))
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
	log.Info("cleaning up shares", "Count", len(shares))
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
	log.Info("cleaning up certificates", "Count", len(certs))
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
	log.Info("cleaning up lbAppProfiles", "Count", len(lbAppProfiles))
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
	log.Info("cleaning up lbPersistenceProfiles", "Count", len(lbPersistenceProfiles))
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
	log.Info("cleaning up lbMonitorProfiles", "Count", len(lbMonitorProfiles))
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
	// We don't clean client_ssl_profile as client_ssl_profile is not created by ncp or nsx-operator
	return nil
}

func (service *VPCService) ListVPCInfo(ns string) []common.VPCResourceInfo {
	var VPCInfoList []common.VPCResourceInfo
	nc := service.GetVPCNetworkConfigByNamespace(ns)
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
	vpcs := service.GetVPCsByNamespace(ns) // Transparently call the VPCService.GetVPCsByNamespace method
	for _, v := range vpcs {
		vpcResourceInfo, err := common.ParseVPCResourcePath(*v.Path)
		if err != nil {
			log.Error(err, "Failed to get vpc info from vpc path", "vpc path", *v.Path)
		}
		vpcResourceInfo.PrivateIpv4Blocks = v.PrivateIpv4Blocks
		VPCInfoList = append(VPCInfoList, vpcResourceInfo)
	}
	return VPCInfoList
}

func (s *VPCService) GetNSXLBSPath(lbsId string) string {
	vpcLBS := s.LbsStore.GetByKey(lbsId)
	if vpcLBS == nil {
		return ""
	}
	return *vpcLBS.Path
}

func GetAlbEndpoint(cluster *nsx.Cluster) error {
	_, err := cluster.HttpGet(albEndpointPath)
	return err
}

func (vpcService *VPCService) NSXLBEnabled() bool {
	lbProviderMutex.Lock()
	defer lbProviderMutex.Unlock()

	if lbProvider == "" {
		lbProvider = vpcService.getLBProvider()
	}
	return lbProvider == LBProviderNSX
}

func (vpcService *VPCService) getLBProvider() string {
	// if no Alb endpoint found, return nsx-lb
	// if found, and nsx lbs found, return nsx-lb
	// else return avi
	if !vpcService.Service.NSXConfig.UseAVILoadBalancer {
		return LBProviderNSX
	}
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
		return GetAlbEndpoint(vpcService.Service.NSXClient.Cluster)
	}); err == nil {
		albEndpointFound = true
	}
	if !albEndpointFound {
		return LBProviderNSX
	}
	if len(vpcService.LbsStore.List()) > 0 {
		return LBProviderNSX
	}
	return LBProviderAVI
}

func (service *VPCService) GetVPCFromNSXByPath(vpcPath string) (*model.Vpc, error) {
	vpcResInfo, err := common.ParseVPCResourcePath(vpcPath)
	if err != nil {
		log.Error(err, "failed to parse VPCResourceInfo from the given VPC path", "VPC", vpcPath)
		return nil, err
	}
	vpc, err := service.NSXClient.VPCClient.Get(vpcResInfo.OrgID, vpcResInfo.ProjectID, vpcResInfo.VPCID)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to read VPC object from NSX", "VPC", vpcPath)
		return nil, err
	}

	return &vpc, nil
}

func IsPreCreatedVPC(nc common.VPCNetworkConfigInfo) bool {
	return nc.VPCPath != ""
}
