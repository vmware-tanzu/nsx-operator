package vpc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

const (
	AviSEIngressAllowRuleId    = "avi-se-ingress-allow-rule"
	VPCAviSEGroupId            = "avi-se-vms"
	VpcDefaultSecurityPolicyId = "default-layer3-section"
	GroupKey                   = "/orgs/%s/projects/%s/vpcs/%s/groups/%s"
	SecurityPolicyKey          = "/orgs/%s/projects/%s/vpcs/%s/security-policies/%s"
	RuleKey                    = "/orgs/%s/projects/%s/vpcs/%s/security-policies/%s/rules/%s"
)

var (
	log             = logger.Log
	ctx             = context.Background()
	ResourceTypeVPC = common.ResourceTypeVpc
	NewConverter    = common.NewConverter

	MarkedForDelete    = true
	enableAviAllowRule = false
)

type VPCNetworkInfoStore struct {
	sync.Mutex
	VPCNetworkConfigMap map[string]common.VPCNetworkConfigInfo
}

type VPCNsNetworkConfigStore struct {
	sync.Mutex
	VPCNSNetworkConfigMap map[string]string
}

type VPCService struct {
	common.Service
	VpcStore                *VPCStore
	IpblockStore            *IPBlockStore
	VPCNetworkConfigStore   VPCNetworkInfoStore
	VPCNSNetworkConfigStore VPCNsNetworkConfigStore
	defaultNetworkConfigCR  *common.VPCNetworkConfigInfo
	AVIAllowRule
}
type AVIAllowRule struct {
	GroupStore          *AviGroupStore
	RuleStore           *AviRuleStore
	SecurityPolicyStore *AviSecurityPolicyStore
	PubIpblockStore     *PubIPblockStore
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
	return nc.PrivateIPv4CIDRs != nil && len(nc.PrivateIPv4CIDRs) != 0
}

// InitializeVPC sync NSX resources
func InitializeVPC(service common.Service) (*VPCService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	VPCService := &VPCService{Service: service}
	enableAviAllowRule = service.NSXClient.FeatureEnabled(nsx.VpcAviRule)
	if enableAviAllowRule {
		log.Info("support avi allow rule")
		wg.Add(5)
	} else {
		log.Info("disable avi allow rule")
		wg.Add(2)
	}
	VPCService.VpcStore = &VPCStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.VpcBindingType(),
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
	//initialize vpc store and ip blocks store
	go VPCService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeVpc, nil, VPCService.VpcStore)
	go VPCService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeIPBlock, nil, VPCService.IpblockStore)

	//initalize avi rule related store
	if enableAviAllowRule {
		VPCService.RuleStore = &AviRuleStore{ResourceStore: common.ResourceStore{
			Indexer:     cache.NewIndexer(keyFuncAVI, nil),
			BindingType: model.RuleBindingType(),
		}}
		VPCService.GroupStore = &AviGroupStore{ResourceStore: common.ResourceStore{
			Indexer:     cache.NewIndexer(keyFuncAVI, nil),
			BindingType: model.GroupBindingType(),
		}}
		VPCService.SecurityPolicyStore = &AviSecurityPolicyStore{ResourceStore: common.ResourceStore{
			Indexer:     cache.NewIndexer(keyFuncAVI, nil),
			BindingType: model.SecurityPolicyBindingType(),
		}}
		VPCService.PubIpblockStore = &PubIPblockStore{ResourceStore: common.ResourceStore{
			Indexer:     cache.NewIndexer(keyFuncAVI, nil),
			BindingType: model.IpAddressBlockBindingType(),
		}}
		go VPCService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeGroup, nil, VPCService.GroupStore)
		go VPCService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeRule, nil, VPCService.RuleStore)

		query := fmt.Sprintf("%s:%s AND visibility:EXTERNAL", common.ResourceType, common.ResourceTypeIPBlock)
		go VPCService.PopulateResourcetoStore(&wg, fatalErrors, common.ResourceTypeIPBlock, query, VPCService.PubIpblockStore, nil)
	}

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
		return err
	}
	vpc.MarkedForDelete = &MarkedForDelete
	if err := s.VpcStore.Apply(vpc); err != nil {
		return err
	}

	log.Info("successfully deleted NSX VPC", "VPC", pathInfo.VPCID)
	return nil
}

func (s *VPCService) deleteIPBlock(path string) error {
	ipblockClient := s.NSXClient.IPBlockClient
	parts := strings.Split(path, "/")
	log.Info("deleting private ip block", "ORG", parts[2], "Project", parts[4], "ID", parts[7])
	if err := ipblockClient.Delete(parts[2], parts[4], parts[7]); err != nil {
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

func (s *VPCService) CreateOrUpdatePrivateIPBlock(obj *v1alpha1.NetworkInfo, nsObj *v1.Namespace, nc common.VPCNetworkConfigInfo) (map[string]string,
	error) {
	// if network config contains PrivateIPV4CIDRs section, create private ip block for each cidr
	path := map[string]string{}
	if nc.PrivateIPv4CIDRs != nil {
		for _, pCidr := range nc.PrivateIPv4CIDRs {
			log.Info("start processing private cidr", "cidr", pCidr)
			// if parse success, then check if private cidr exist, here we suppose it must be a cidr format string
			ip, _, err := net.ParseCIDR(pCidr)
			if err != nil {
				message := fmt.Sprintf("invalid cidr %s for VPC %s", pCidr, obj.Name)
				fmtError := errors.New(message)
				log.Error(fmtError, message)
				return nil, fmtError
			}
			// check if private ip block already exist
			// use cidr_project_ns as search key
			key := generateIPBlockSearchKey(pCidr, string(nsObj.UID))
			log.Info("using key to search from ipblock store", "Key", key)
			block := s.IpblockStore.GetByKey(key)
			if block == nil {
				log.Info("no ip block found in store for cidr", "CIDR", pCidr)
				block := buildPrivateIpBlock(obj, nsObj, pCidr, ip.String(), nc.NsxtProject, s.NSXConfig.Cluster)
				log.Info("creating ip block", "IPBlock", block.Id, "VPC", obj.Name)
				// can not find private ip block from store, create one
				_err := s.NSXClient.IPBlockClient.Patch(nc.Org, nc.NsxtProject, *block.Id, block)
				if _err != nil {
					message := fmt.Sprintf("failed to create private ip block for cidr %s for VPC %s", pCidr, obj.Name)
					ipblockError := errors.New(message)
					log.Error(ipblockError, message)
					return nil, ipblockError
				}
				ignoreIpblockUsage := true
				createdBlock, err := s.NSXClient.IPBlockClient.Get(nc.Org, nc.NsxtProject, *block.Id, &ignoreIpblockUsage)
				if err != nil {
					// created by can not get, ignore this error
					log.Info("failed to read ip blocks from NSX", "Project", nc.NsxtProject, "IPBlock", block.Id)
					continue
				}
				// update ip block store
				s.IpblockStore.Add(&createdBlock)
				path[pCidr] = *createdBlock.Path
			} else {
				eBlock := block.(*model.IpAddressBlock)
				path[pCidr] = *eBlock.Path
				log.Info("ip block found in store for cidr using key", "CIDR", pCidr, "Key", key)
			}
		}
	}
	return path, nil
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

func (s *VPCService) getNetworkconfigNameFromNS(ns string) (string, error) {
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
	if err != nil {
		log.Error(err, "failed to read AVI subnet", "VPC", vpc.Id)
		return "", "", err
	}
	path := *subnet.Path

	statusList, err := statusClient.List(info.OrgID, info.ProjectID, info.VPCID, common.AVISubnetLBID)
	if err != nil {
		log.Error(err, "failed to read AVI subnet status", "VPC", vpc.Id)
		return "", "", err
	}

	if len(statusList.Results) == 0 {
		log.Info("AVI subnet status not found", "VPC", vpc.Id)
		return "", "", err
	}

	cidr := *statusList.Results[0].NetworkAddress
	log.Info("read AVI subnet properties", "Path", path, "CIDR", cidr)
	return path, cidr, nil
}

func (s *VPCService) CreateOrUpdateVPC(obj *v1alpha1.NetworkInfo) (*model.Vpc, *common.VPCNetworkConfigInfo, error) {
	// check from VPC store if vpc already exist
	ns := obj.Namespace
	updateVpc := false
	nsObj := &v1.Namespace{}
	// get name obj
	if err := s.Client.Get(ctx, types.NamespacedName{Name: obj.Namespace}, nsObj); err != nil {
		log.Error(err, "unable to fetch namespace", "name", obj.Namespace)
		return nil, nil, err
	}

	// read corresponding vpc network config from store
	ncName, err := s.getNetworkconfigNameFromNS(obj.Namespace)
	if err != nil {
		log.Error(err, "failed to get network config name for VPC when creating NSX VPC", "VPC", obj.Name)
		return nil, nil, err
	}
	nc, _exist := s.GetVPCNetworkConfig(ncName)
	if !_exist {
		message := fmt.Sprintf("failed to read network config %s when creating NSX VPC", ncName)
		log.Info(message)
		return nil, nil, errors.New(message)
	}

	// check if this namespace vpc share from others, if yes
	// then check if the shared vpc created or not, if yes
	// then directly return this vpc, if not, requeue
	isShared, err := s.IsSharedVPCNamespaceByNS(ns)
	if err != nil {
		return nil, nil, err
	}

	existingVPC := s.GetVPCsByNamespace(ns)
	if len(existingVPC) != 0 { // We now consider only one VPC for one namespace
		if isShared {
			log.Info("The shared VPC already exist", "Namespace", ns)
			return existingVPC[0], &nc, nil
		}
		updateVpc = true
		log.Info("VPC already exist, updating NSX VPC object", "VPC", existingVPC[0].Id)
	} else if isShared {
		message := fmt.Sprintf("the shared VPC is not created yet, namespace %s", ns)
		return nil, nil, errors.New(message)
	}

	log.Info("read network config from store", "NetworkConfig", ncName)

	paths, err := s.CreateOrUpdatePrivateIPBlock(obj, nsObj, nc)
	if err != nil {
		log.Error(err, "failed to process private ip blocks, push event back to queue")
		return nil, nil, err
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

	createdVpc, err := buildNSXVPC(obj, nsObj, nc, s.NSXConfig.Cluster, paths, nsxVPC)
	if err != nil {
		log.Error(err, "failed to build NSX VPC object")
		return nil, nil, err
	}

	// if there is not change in public cidr and private cidr, build partial vpc will return nil
	if createdVpc == nil {
		log.Info("no VPC changes detect, skip creating or updating process")
		return existingVPC[0], &nc, nil
	}

	log.Info("creating NSX VPC", "VPC", *createdVpc.Id)
	err = s.NSXClient.VPCClient.Patch(nc.Org, nc.NsxtProject, *createdVpc.Id, *createdVpc)
	if err != nil {
		log.Error(err, "failed to create VPC", "Project", nc.NsxtProject, "Namespace", obj.Namespace)
		// TODO: this seems to be a nsx bug, in some case, even if nsx returns failed but the object is still created.
		// in this condition, we still need to read the object and update it into store, or else operator will create multiple
		// vpcs for this namespace.
		log.Info("try to read VPC although VPC creation failed", "VPC", *createdVpc.Id)
		failedVpc, rErr := s.NSXClient.VPCClient.Get(nc.Org, nc.NsxtProject, *createdVpc.Id)
		if rErr != nil {
			// failed to read, but already created, we consider this scenario as success, but store may not sync with nsx
			log.Info("confirmed VPC is not created", "VPC", createdVpc.Id)
			return nil, nil, err
		} else {
			// vpc created anyway, update store, and in this scenario, we condsider creating successfully
			log.Info("read VPCs from NSX after creation failed, still update VPC store", "VPC", *createdVpc.Id)
			s.VpcStore.Add(&failedVpc)
			return &failedVpc, &nc, nil
		}
	}

	// get the created vpc from nsx, it contains the path of the resources
	newVpc, err := s.NSXClient.VPCClient.Get(nc.Org, nc.NsxtProject, *createdVpc.Id)
	if err != nil {
		// failed to read, but already created, we consider this scenario as success, but store may not sync with nsx
		log.Error(err, "failed to read VPC object after creating or updating", "VPC", createdVpc.Id)
		return nil, nil, err
	}

	realizeService := realizestate.InitializeRealizeState(s.Service)
	if err = realizeService.CheckRealizeState(retry.DefaultRetry, *newVpc.Path, "RealizedLogicalRouter"); err != nil {
		log.Error(err, "failed to check VPC realization state", "VPC", *createdVpc.Id)
		if realizestate.IsRealizeStateError(err) {
			log.Error(err, "the created VPC is in error realization state, cleaning the resource", "VPC", *createdVpc.Id)
			// delete the nsx vpc object and re-created in next loop
			if err := s.DeleteVPC(*newVpc.Path); err != nil {
				log.Error(err, "cleanup VPC failed", "VPC", *createdVpc.Id)
				return nil, nil, err
			}
		}
		return nil, nil, err
	}

	s.VpcStore.Add(&newVpc)
	return &newVpc, &nc, nil
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
			if err := CleanAviSubnetPorts(ctx, s.NSXClient.Cluster, *vpc.Path); err != nil {
				return err
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

	return nil
}

func (service *VPCService) needUpdateRule(rule *model.Rule, externalCIDRs []string) bool {
	des := rule.DestinationGroups
	currentDesSet := sets.Set[string]{}
	for _, group := range des {
		currentDesSet.Insert(group)
	}
	if len(externalCIDRs) != len(currentDesSet) {
		return true
	}
	for _, cidr := range externalCIDRs {
		if !currentDesSet.Has(cidr) {
			return true
		}
	}
	return false
}

func (service *VPCService) getIpblockCidr(blocks []string) (result []string, err error) {
	for _, cidr := range blocks {
		ipblock := service.PubIpblockStore.GetByKey(cidr)
		if ipblock == nil {
			// in case VPC using the new ipblock, search the ipblock from nsxt
			// return error, and retry next time when the ipblock is synced into store
			err = errors.New("ipblock not found")
			log.Error(err, "failed to get public ipblock", "path", cidr)
			query := fmt.Sprintf("%s:%s AND visibility:EXTERNAL", common.ResourceType, common.ResourceTypeIPBlock)
			count, searcherr := service.SearchResource(common.ResourceTypeIPBlock, query, service.PubIpblockStore, nil)
			if searcherr != nil {
				log.Error(searcherr, "failed to query public ipblock", "query", query)
			} else {
				log.V(1).Info("query public ipblock", "count", count)
			}
			return
		} else {
			result = append(result, *ipblock.Cidr)
		}
	}
	return
}

func (service *VPCService) CreateOrUpdateAVIRule(vpc *model.Vpc, namespace string) error {
	if !enableAviAllowRule {
		return nil
	}
	if !nsxutil.IsLicensed(nsxutil.FeatureDFW) {
		log.Info("avi rule cannot be created or updated due to no DFW license")
		return nil
	}
	vpcInfo, err := common.ParseVPCResourcePath(*vpc.Path)
	if err != nil {
		log.Error(err, "failed to parse VPC Resource Path: ", *vpc.Path)
		return err
	}
	orgId := vpcInfo.OrgID
	projectId := vpcInfo.ProjectID
	ruleId := AviSEIngressAllowRuleId
	groupId := VPCAviSEGroupId
	spId := VpcDefaultSecurityPolicyId

	if !service.checkAVISecurityPolicyExist(orgId, projectId, *vpc.Id, spId) {
		return errors.New("avi security policy not found")
	}
	allowrule, err := service.getAVIAllowRule(orgId, projectId, *vpc.Id, spId, ruleId)
	if err != nil {
		log.Info("avi rule is not found, creating")
	}
	externalCIDRs, err := service.getIpblockCidr(vpc.ExternalIpv4Blocks)
	if err != nil {
		return err
	}
	log.Info("avi rule get external cidr", "cidr", externalCIDRs)
	if allowrule != nil {
		if !service.needUpdateRule(allowrule, externalCIDRs) {
			log.Info("avi rule is not changed, skip updating avi rule")
			return nil
		} else {
			log.Info("avi rule changed", "previous", allowrule.DestinationGroups, "current", externalCIDRs)
		}
	}

	group, err := service.getorCreateAVIGroup(orgId, projectId, *vpc.Id, groupId)
	if err != nil {
		log.Error(err, "failed to get avi group", "group", groupId)
		return err
	}

	newrule, err := service.buildAVIAllowRule(vpc, externalCIDRs, *group.Path, ruleId, projectId)
	log.Info("creating avi rule", "rule", newrule)
	if err != nil {
		log.Error(err, "failed to build avi rule", "rule", newrule)
		return err
	}

	err = service.NSXClient.VPCRuleClient.Patch(orgId, projectId, *vpc.Id, spId, *newrule.Id, *newrule)
	if err != nil {
		log.Error(err, "failed to create avi rule", "rule", newrule)
		return err
	}
	nsxrule, err := service.NSXClient.VPCRuleClient.Get(orgId, projectId, *vpc.Id, spId, *newrule.Id)
	if err != nil {
		log.Error(err, "failed to get avi rule", "rule", nsxrule)
		return err
	}
	service.RuleStore.Add(&nsxrule)
	log.Info("created avi rule successfully")
	return nil
}

func (service *VPCService) getorCreateAVIGroup(orgId string, projectId string, vpcId string, groupId string) (*model.Group, error) {
	groupPtr, err := service.getAVIGroup(orgId, projectId, vpcId, groupId)
	if err != nil {
		log.Info("create avi group", "group", groupId)
		groupPtr, err = service.createAVIGroup(orgId, projectId, vpcId, groupId)
		if err != nil {
			log.Error(err, "failed to create avi group", "group", groupId)
			return groupPtr, err
		}
		service.GroupStore.Add(groupPtr)
	}
	return groupPtr, err
}

func (service *VPCService) buildAVIGroupTag(vpcId string) []model.Tag {
	return []model.Tag{
		{
			Scope: common.String(common.TagScopeCluster),
			Tag:   common.String(service.NSXConfig.Cluster),
		},
		{
			Scope: common.String(common.TagScopeVersion),
			Tag:   common.String(strings.Join(common.TagValueVersion, ".")),
		},
		{
			Scope: common.String(common.TagScopeGroupType),
			Tag:   common.String(common.TagValueGroupAvi),
		},
	}
}

func (service *VPCService) createAVIGroup(orgId string, projectId string, vpcId string, groupId string) (*model.Group, error) {
	group := model.Group{}
	group.Tags = service.buildAVIGroupTag(vpcId)
	expression := service.buildExpression("Condition", "VpcSubnet", "AVI_SUBNET_LB|", "Tag", "EQUALS", "EQUALS")
	group.Expression = []*data.StructValue{expression}
	group.DisplayName = common.String(groupId)

	err := service.NSXClient.VpcGroupClient.Patch(orgId, projectId, vpcId, groupId, group)
	if err != nil {
		return &group, err
	}
	nsxgroup, err := service.NSXClient.VpcGroupClient.Get(orgId, projectId, vpcId, groupId)
	return &nsxgroup, err
}

func (service *VPCService) buildExpression(resource_type, member_type, value, key, operator, scope_op string) *data.StructValue {
	return data.NewStructValue(
		"",
		map[string]data.DataValue{
			"resource_type":  data.NewStringValue(resource_type),
			"member_type":    data.NewStringValue(member_type),
			"value":          data.NewStringValue(value),
			"key":            data.NewStringValue(key),
			"operator":       data.NewStringValue(operator),
			"scope_operator": data.NewStringValue(scope_op),
		},
	)
}

func (service *VPCService) buildAVIAllowRule(obj *model.Vpc, externalCIDRs []string, groupId, ruleId, projectId string) (*model.Rule, error) {
	rule := &model.Rule{}
	rule.Action = common.String(model.Rule_ACTION_ALLOW)
	rule.Direction = common.String(model.Rule_DIRECTION_IN_OUT)
	rule.Scope = append(rule.Scope, groupId)
	rule.SequenceNumber = common.Int64(math.MaxInt32 - 1)
	rule.DestinationGroups = externalCIDRs
	rule.SourceGroups = append(rule.SourceGroups, "Any")
	name := fmt.Sprintf("PROJECT-%s-VPC-%s-%s", projectId, *obj.Id, ruleId)
	rule.DisplayName = common.String(name)
	rule.Id = common.String(ruleId)
	rule.Services = []string{"ANY"}
	rule.IsDefault = common.Bool(true)
	tags := []model.Tag{
		{
			Scope: common.String(common.TagScopeCluster),
			Tag:   common.String(service.NSXConfig.Cluster),
		},
		{
			Scope: common.String(common.TagScopeVersion),
			Tag:   common.String(strings.Join(common.TagValueVersion, ".")),
		},
	}
	rule.Tags = tags
	return rule, nil
}

func (service *VPCService) getAVIAllowRule(orgId string, projectId string, vpcId string, spId string, ruleId string) (*model.Rule, error) {
	key := fmt.Sprintf(RuleKey, orgId, projectId, vpcId, spId, ruleId)
	rule := service.RuleStore.GetByKey(key)
	if rule == nil {
		log.Info("avi rule not found", "key", key)
		return nil, errors.New("avi rule not found")
	}
	return rule, nil
}

func (service *VPCService) getAVIGroup(orgId string, projectId string, vpcId string, groupId string) (*model.Group, error) {
	key := fmt.Sprintf(GroupKey, orgId, projectId, vpcId, groupId)
	group := service.GroupStore.GetByKey(key)
	var err error
	if group == nil {
		log.Info("avi se group not found", "key", key)
		err = errors.New("avi se group not found")
	}
	return group, err
}

// checkAVISecurityPolicyExist returns true if security policy for that VPC already exists
// this security policy created by NSXT once VPC created
// if not found, wait until it created
func (service *VPCService) checkAVISecurityPolicyExist(orgId string, projectId string, vpcId string, spId string) bool {
	key := fmt.Sprintf(SecurityPolicyKey, orgId, projectId, vpcId, spId)
	sp := service.SecurityPolicyStore.GetByKey(key)
	if sp != nil {
		return true
	}
	nsxtsp, err := service.NSXClient.VPCSecurityClient.Get(orgId, projectId, vpcId, spId)
	if err != nil {
		log.Error(err, "failed to get avi security policy", "key", key)
		return false
	}
	service.SecurityPolicyStore.Add(&nsxtsp)
	return true
}

func (service *VPCService) ListVPCInfo(ns string) []common.VPCResourceInfo {
	var VPCInfoList []common.VPCResourceInfo
	vpcs := service.GetVPCsByNamespace(ns) // Transparently call the VPCService.GetVPCsByNamespace method
	for _, v := range vpcs {
		vpcResourceInfo, err := common.ParseVPCResourcePath(*v.Path)
		if err != nil {
			log.Error(err, "Failed to get vpc info from vpc path", "vpc path", *v.Path)
		}
		vpcResourceInfo.ExternalIPv4Blocks = v.ExternalIpv4Blocks
		vpcResourceInfo.PrivateIpv4Blocks = v.PrivateIpv4Blocks
		VPCInfoList = append(VPCInfoList, vpcResourceInfo)
	}
	return VPCInfoList
}
