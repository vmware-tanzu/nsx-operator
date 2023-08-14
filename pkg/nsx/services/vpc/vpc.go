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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
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

	log.Info("successfully deleted NSX VPC", "nsxVPC", pathInfo.VPCID)
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
			log.Error(err, "failed to delete ip block", "PATH", block)
			return err
		}
		vpcCRUid := ""
		for _, tag := range vpc.Tags {
			if *tag.Scope == common.TagScopeVPCCRUID {
				vpcCRUid = *tag.Tag
			}
		}
		log.V(2).Info("search ip block from store using index and path", "index", common.TagScopeVPCCRUID, "value", vpcCRUid, "path", block)
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
				message := fmt.Sprintf("invalid cidr %s for vpc %s", pCidr, obj.Name)
				fmtError := errors.New(message)
				log.Error(fmtError, message)
				return nil, fmtError
			}
			// check if private ip block already exist
			// use cidr_project_ns as search key
			key := generateIPBlockSearchKey(pCidr, string(obj.UID))
			log.Info("using key to search from ipblock store", "key", key)
			block := service.IpblockStore.GetByKey(key)
			if block == nil {
				log.Info("no ip block found in stroe for cidr", "cidr", pCidr)
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
					message := fmt.Sprintf("failed to create private ip block for cidr %s for vpc %s", pCidr, obj.Name)
					ipblockError := errors.New(message)
					log.Error(ipblockError, message)
					return nil, ipblockError
				}
				createdBlock, err := service.NSXClient.IPBlockClient.Get(VPCDefaultOrg, nc.NsxtProject, blockId)
				if err != nil {
					// created by can not get, ignore this error
					log.Info("failed to read ip blocks from nsxt", "Project", nc.NsxtProject, "IPBlock", blockId)
					continue
				}
				// update ip block store
				service.IpblockStore.Add(createdBlock)
				path[pCidr] = *createdBlock.Path
			} else {
				eBlock := block.(model.IpAddressBlock)
				path[pCidr] = *eBlock.Path
				log.Info("ip block found in stroe for cidr using key", "cidr", pCidr, "key", key)
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

func (service *VPCService) CreateorUpdateVPC(obj *v1alpha1.VPC) (*model.Vpc, error) {
	// check from VPC store if vpc already exist
	updateVpc := false
	existingVPC := service.VpcStore.GetVPCsByNamespace(obj.Namespace)
	if existingVPC != nil && len(existingVPC) != 0 { // We now consider only one VPC for one namespace
		updateVpc = true
		log.Info("VPC already exist, updating vpc object")
	}

	// read corresponding vpc network config from store
	nc_name, err := service.getNetworkconfigNameFromNS(obj.Namespace)
	if err != nil {
		log.Error(err, "failed to get network config name for vpc", "VPC", obj.Name)
		return nil, err
	}
	nc, _exist := service.GetVPCNetworkConfig(nc_name)
	if !_exist {
		message := fmt.Sprintf("network config %s not found", nc_name)
		log.Info(message)
		return nil, errors.New(message)
	}

	log.Info("read network config from store", "NetworkConfig", nc_name)

	paths, err := service.CreatOrUpdatePrivateIPBlock(obj, nc)
	if err != nil {
		log.Error(err, "failed to process private ip blocks, push event back to queue")
		return nil, err
	}

	// if all private ip blocks are created, then create nsx vpc resource.
	nsxVPC := &model.Vpc{}
	if updateVpc {
		log.Info("vpc resource already exist on nsx, updating vpc", "VPC", existingVPC[0].DisplayName)
		nsxVPC = &existingVPC[0]
	} else {
		log.Info("vpc does not exist on nsx, creating vpc", "VPC", obj.Name)
		nsxVPC = nil
	}

	createdVpc, err := buildNSXVPC(obj, nc, service.NSXConfig.Cluster, paths, nsxVPC)
	if err != nil {
		log.Error(err, "failed to build nsx vpc object")
		return nil, err
	}

	// if there is not change in public cidr and private cidr, build partial vpc will return nil
	if createdVpc == nil {
		log.Info("no vpc changed, skip create or update process")
		return &existingVPC[0], nil
	}

	log.Info("creating nsx vpc resource", "VPC", *createdVpc.Id)
	err = service.NSXClient.VPCClient.Patch(VPCDefaultOrg, nc.NsxtProject, *createdVpc.Id, *createdVpc)
	if err != nil {
		log.Error(err, "failed to create vpc", "Project", nc.NsxtProject, "Namespace", obj.Namespace)
		// TODO: this seems to be a nsx bug, in some case, even if nsx returns failed but the object is still created.
		// in this condition, we still need to read the object and update it into store, or else operator will create multiple
		// vpcs for this namespace.
		log.Info("try to read vpc object although vpc creation failed", "VPC", *createdVpc.Id)
		failedVpc, rErr := service.NSXClient.VPCClient.Get(VPCDefaultOrg, nc.NsxtProject, *createdVpc.Id)
		if rErr != nil {
			// failed to read, but already created, we consider this scenario as success, but store may not sync with nsx
			log.Info("confirmed vpc is not created", "VPC", createdVpc.Id)
			return nil, err
		} else {
			// vpc created anyway, update store, and in this scenario, we condsider creating successfully
			log.Info("read vpcs from nsx after creation failed, still update vpc store", "VPC", *createdVpc.Id)
			service.VpcStore.Add(failedVpc)
			return &failedVpc, nil
		}
	}

	newVpc, err := service.NSXClient.VPCClient.Get(VPCDefaultOrg, nc.NsxtProject, *createdVpc.Id)
	if err != nil {
		// failed to read, but already created, we consider this scenario as success, but store may not sync with nsx
		log.Error(err, "failed to read vpc object after creating", "VPC", createdVpc.Id)
		return &newVpc, nil
	}

	service.VpcStore.Add(newVpc)
	return &newVpc, nil
}
