package vpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log             = logger.Log
	ctx             = context.Background()
	ResourceTypeVPC = common.ResourceTypeVpc
	NewConverter    = common.NewConverter

	// The following variables are defined as interface, they should be initialized as concrete type
	vpcStore     common.Store
	ipblockStore common.Store

	// this store contains mapping relation of network config name and network config entity
	VPCNetworkConfigMap = map[string]VPCNetworkConfigInfo{}

	// this map contains mapping relation between namespace and the network config it uses.
	VPCNSNetworkconfigMap = map[string]string{}

	VPCDefaultOrg             = "default"
	VPCIPBlockPathPrefix      = "/infra/ip-blocks/"
	resourceType              = "resource_type"
	EnforceRevisionCheckParam = false
	MarkedForDelete           = true
)

type VPCService struct {
	common.Service
	VpcStore     *VPCStore
	IpblockStore *IPBlockStore
}

func (s *VPCService) RegisterVPCNetworkConfig(ncCRName string, info VPCNetworkConfigInfo) {
	VPCNetworkConfigMap[ncCRName] = info
}

func (s *VPCService) UnregisterVPCNetworkConfig(ncCRName string) {
	delete(VPCNetworkConfigMap, ncCRName)
}

func (s *VPCService) GetVPCNetworkConfig(ncCRName string) (VPCNetworkConfigInfo, bool) {
	nc, exist := VPCNetworkConfigMap[ncCRName]
	return nc, exist
}

func (s *VPCService) RegisterNamespaceNetworkconfigBinding(ns string, ncCRName string) {
	VPCNSNetworkconfigMap[ns] = ncCRName
}

func (s *VPCService) UnRegisterNamespaceNetworkconfigBinding(ns string) {
	delete(VPCNSNetworkconfigMap, ns)
}

// find the namespace list which is using the given network configuration
func (s *VPCService) GetNamespacesByNetworkconfigName(nc string) []string {
	result := []string{}
	for key, value := range VPCNSNetworkconfigMap {
		if value == nc {
			result = append(result, key)
		}
	}
	return result
}

func (s *VPCService) GetVPCNetworkConfigByNamespace(ns string) *VPCNetworkConfigInfo {
	ncName, nameExist := VPCNSNetworkconfigMap[ns]
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
func (s *VPCService) ValidateNetworkConfig(nc VPCNetworkConfigInfo) bool {
	return nc.PrivateIPv4CIDRs != nil && len(nc.PrivateIPv4CIDRs) != 0
}

// InitializeVPC sync NSX resources
func InitializeVPC(service common.Service) (*VPCService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(2)

	VPCService := &VPCService{Service: service}

	VPCService.VpcStore = &VPCStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeVPCCRUID: indexFunc}),
		BindingType: model.VpcBindingType(),
	}}

	VPCService.IpblockStore = &IPBlockStore{ResourceStore: common.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{
			common.TagScopeVPCCRUID: indexFunc,
			common.IndexKeyPathPath: indexPathFunc}),
		BindingType: model.IpAddressBlockBindingType(),
	}}

	//initialize vpc store and ip blocks store
	go VPCService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeVpc, nil, VPCService.VpcStore)
	go VPCService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeIPBlock, nil, VPCService.IpblockStore)

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

func (s *VPCService) GetVPCsByNamespace(namespace string) []model.Vpc {
	return s.VpcStore.GetVPCsByNamespace(namespace)
}

func (service *VPCService) ListVPC() []model.Vpc {
	vpcs := service.VpcStore.List()
	vpcSet := []model.Vpc{}
	for _, vpc := range vpcs {
		vpcSet = append(vpcSet, vpc.(model.Vpc))
	}
	return vpcSet
}

func (service *VPCService) DeleteVPC(path string) error {
	pathInfo, err := common.ParseVPCResourcePath(path)
	if err != nil {
		return err
	}
	vpcClient := service.NSXClient.VPCClient
	vpc := service.VpcStore.GetByKey(pathInfo.VPCID)
	if vpc == nil {
		return nil
	}

	if err := vpcClient.Delete(pathInfo.OrgID, pathInfo.ProjectID, pathInfo.VPCID); err != nil {
		return err
	}
	vpc.MarkedForDelete = &MarkedForDelete
	if err := service.VpcStore.Operate(vpc); err != nil {
		return err
	}

	log.Info("successfully deleted NSX VPC", "VPC", pathInfo.VPCID)
	return nil
}

func (service *VPCService) DeleteIPBlock(vpc model.Vpc) error {
	blocks := vpc.PrivateIpv4Blocks
	if blocks == nil || len(blocks) == 0 {
		log.Info("no private cidr list, skip deleting private ip blocks")
		return nil
	}

	ipblockClient := service.NSXClient.IPBlockClient
	for _, block := range blocks {
		parts := strings.Split(block, "/")
		log.Info("deleting private ip block", "ORG", parts[2], "Project", parts[4], "ID", parts[7])
		if err := ipblockClient.Delete(parts[2], parts[4], parts[7]); err != nil {
			log.Error(err, "failed to delete ip block", "Path", block)
			return err
		}
		vpcCRUid := ""
		for _, tag := range vpc.Tags {
			if *tag.Scope == common.TagScopeVPCCRUID {
				vpcCRUid = *tag.Tag
			}
		}
		log.V(2).Info("search ip block from store using index and path", "index", common.TagScopeVPCCRUID, "Value", vpcCRUid, "Path", block)
		// using index vpc cr id may get multiple ipblocks, add path to filter the correct one
		ipblock := service.IpblockStore.GetByIndex(common.IndexKeyPathPath, block)
		if ipblock != nil {
			log.Info("deleting ip blocks", "IPBlock", ipblock)
			ipblock.MarkedForDelete = &MarkedForDelete
			service.IpblockStore.Operate(ipblock)
		}
	}
	log.Info("successfully deleted all ip blocks")
	return nil
}

func (service *VPCService) CreatOrUpdatePrivateIPBlock(obj *v1alpha1.VPC, nc VPCNetworkConfigInfo) (map[string]string, error) {
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
			key := generateIPBlockSearchKey(pCidr, string(obj.UID))
			log.Info("using key to search from ipblock store", "Key", key)
			block := service.IpblockStore.GetByKey(key)
			if block == nil {
				log.Info("no ip block found in stroe for cidr", "CIDR", pCidr)
				blockId := nc.NsxtProject + "_" + ip.String() + "_" + obj.Namespace
				addr, _ := netip.ParseAddr(ip.String())
				ipType := util.If(addr.Is4(), model.IpAddressBlock_IP_ADDRESS_TYPE_IPV4, model.IpAddressBlock_IP_ADDRESS_TYPE_IPV6).(string)
				blockType := model.IpAddressBlock_VISIBILITY_PRIVATE
				block := model.IpAddressBlock{
					DisplayName:   &blockId,
					Id:            &blockId,
					Tags:          buildPrivateIPBlockTags(service.NSXConfig.Cluster, nc.NsxtProject, obj.Namespace, string(obj.UID)),
					Cidr:          &pCidr,
					IpAddressType: &ipType,
					Visibility:    &blockType,
				}
				log.Info("creating ip block", "IPBlock", blockId, "VPC", obj.Name)
				// can not find private ip block from store, create one
				_err := service.NSXClient.IPBlockClient.Patch(VPCDefaultOrg, nc.NsxtProject, blockId, block)
				if _err != nil {
					message := fmt.Sprintf("failed to create private ip block for cidr %s for VPC %s", pCidr, obj.Name)
					ipblockError := errors.New(message)
					log.Error(ipblockError, message)
					return nil, ipblockError
				}
				createdBlock, err := service.NSXClient.IPBlockClient.Get(VPCDefaultOrg, nc.NsxtProject, blockId)
				if err != nil {
					// created by can not get, ignore this error
					log.Info("failed to read ip blocks from NSX", "Project", nc.NsxtProject, "IPBlock", blockId)
					continue
				}
				// update ip block store
				service.IpblockStore.Add(createdBlock)
				path[pCidr] = *createdBlock.Path
			} else {
				eBlock := block.(model.IpAddressBlock)
				path[pCidr] = *eBlock.Path
				log.Info("ip block found in stroe for cidr using key", "CIDR", pCidr, "Key", key)
			}
		}
	}
	return path, nil
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
	if annos == nil || len(annos) == 0 {
		return common.DefaultNetworkConfigName, nil
	}

	ncName, exist := annos[common.AnnotationVPCNetworkConfig]
	if !exist {
		return common.DefaultNetworkConfigName, nil
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
	var cursor *string = nil
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

func (s *VPCService) CreateorUpdateVPC(obj *v1alpha1.VPC) (*model.Vpc, error) {
	// check from VPC store if vpc already exist
	updateVpc := false
	existingVPC := s.VpcStore.GetVPCsByNamespace(obj.Namespace)
	if existingVPC != nil && len(existingVPC) != 0 { // We now consider only one VPC for one namespace
		updateVpc = true
		log.Info("VPC already exist, updating NSX VPC object", "VPC", existingVPC[0].Id)
	}

	// read corresponding vpc network config from store
	nc_name, err := s.getNetworkconfigNameFromNS(obj.Namespace)
	if err != nil {
		log.Error(err, "failed to get network config name for VPC when creating NSX VPC", "VPC", obj.Name)
		return nil, err
	}
	nc, _exist := s.GetVPCNetworkConfig(nc_name)
	if !_exist {
		message := fmt.Sprintf("failed to read network config %s when creating NSX VPC", nc_name)
		log.Info(message)
		return nil, errors.New(message)
	}

	log.Info("read network config from store", "NetworkConfig", nc_name)

	paths, err := s.CreatOrUpdatePrivateIPBlock(obj, nc)
	if err != nil {
		log.Error(err, "failed to process private ip blocks, push event back to queue")
		return nil, err
	}

	// if all private ip blocks are created, then create nsx vpc resource.
	nsxVPC := &model.Vpc{}
	if updateVpc {
		log.Info("VPC resource already exist on NSX, updating VPC", "VPC", existingVPC[0].DisplayName)
		nsxVPC = &existingVPC[0]
	} else {
		log.Info("VPC does not exist on NSX, creating VPC", "VPC", obj.Name)
		nsxVPC = nil
	}

	createdVpc, err := buildNSXVPC(obj, nc, s.NSXConfig.Cluster, paths, nsxVPC)
	if err != nil {
		log.Error(err, "failed to build NSX VPC object")
		return nil, err
	}

	// if there is not change in public cidr and private cidr, build partial vpc will return nil
	if createdVpc == nil {
		log.Info("no VPC changes detect, skip creating or updating process")
		return &existingVPC[0], nil
	}

	log.Info("creating NSX VPC", "VPC", *createdVpc.Id)
	err = s.NSXClient.VPCClient.Patch(VPCDefaultOrg, nc.NsxtProject, *createdVpc.Id, *createdVpc)
	if err != nil {
		log.Error(err, "failed to create VPC", "Project", nc.NsxtProject, "Namespace", obj.Namespace)
		// TODO: this seems to be a nsx bug, in some case, even if nsx returns failed but the object is still created.
		// in this condition, we still need to read the object and update it into store, or else operator will create multiple
		// vpcs for this namespace.
		log.Info("try to read VPC although VPC creation failed", "VPC", *createdVpc.Id)
		failedVpc, rErr := s.NSXClient.VPCClient.Get(VPCDefaultOrg, nc.NsxtProject, *createdVpc.Id)
		if rErr != nil {
			// failed to read, but already created, we consider this scenario as success, but store may not sync with nsx
			log.Info("confirmed VPC is not created", "VPC", createdVpc.Id)
			return nil, err
		} else {
			// vpc created anyway, update store, and in this scenario, we condsider creating successfully
			log.Info("read VPCs from NSX after creation failed, still update VPC store", "VPC", *createdVpc.Id)
			s.VpcStore.Add(failedVpc)
			return &failedVpc, nil
		}
	}

	// get the created vpc from nsx, it contains the path of the resources
	newVpc, err := s.NSXClient.VPCClient.Get(VPCDefaultOrg, nc.NsxtProject, *createdVpc.Id)
	if err != nil {
		// failed to read, but already created, we consider this scenario as success, but store may not sync with nsx
		log.Error(err, "failed to read VPC object after creating or updating", "VPC", createdVpc.Id)
		return nil, err
	}

	realizeService := realizestate.InitializeRealizeState(s.Service)
	if err = realizeService.CheckRealizeState(retry.DefaultRetry, *newVpc.Path, "RealizedLogicalRouter"); err != nil {
		log.Error(err, "failed to check VPC realization state", "VPC", *createdVpc.Id)
		if realizestate.IsRealizeStateError(err) {
			log.Error(err, "the created VPC is in error realization state, cleaning the resource", "VPC", *createdVpc.Id)
			// delete the nsx vpc object and re-created in next loop
			if err := s.DeleteVPC(*newVpc.Path); err != nil {
				log.Error(err, "cleanup VPC failed", "VPC", *createdVpc.Id)
				return nil, err
			}
		}
		return nil, err
	}

	s.VpcStore.Add(newVpc)
	return &newVpc, nil
}
