package ipblocksinfo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log                 = &logger.Log
	ipBlocksInfoCRDName = "ip-blocks-info"
	syncInterval        = 10 * time.Minute
	retryInterval       = 30 * time.Second
	updateLock          = &sync.Mutex{}
)

type IPBlocksInfoService struct {
	common.Service
	SyncTask       *IPBlocksInfoSyncTask
	defaultProject string
}

type IPBlocksInfoSyncTask struct {
	syncInterval  time.Duration
	retryInterval time.Duration
	nextRun       time.Time
	mu            sync.Mutex
	resetChan     chan struct{}
}

func NewIPBlocksInfoSyncTask(syncInterval time.Duration, retryInterval time.Duration) *IPBlocksInfoSyncTask {
	return &IPBlocksInfoSyncTask{
		syncInterval:  syncInterval,
		retryInterval: retryInterval,
		nextRun:       time.Now().Add(retryInterval),
		resetChan:     make(chan struct{}),
	}
}

func InitializeIPBlocksInfoService(service common.Service) *IPBlocksInfoService {
	ipBlocksInfoService := &IPBlocksInfoService{
		Service:  service,
		SyncTask: NewIPBlocksInfoSyncTask(syncInterval, retryInterval),
	}
	go ipBlocksInfoService.StartPeriodicSync()
	return ipBlocksInfoService
}

func (s *IPBlocksInfoService) StartPeriodicSync() {
	for {
		s.SyncTask.mu.Lock()
		timeTowait := time.Until(s.SyncTask.nextRun)
		s.SyncTask.mu.Unlock()

		select {
		case <-time.After(timeTowait):
			var interval time.Duration
			if err := s.SyncIPBlocksInfo(context.TODO()); err != nil {
				log.Error(err, "failed to synchronize IPBlocksInfo")
				interval = s.SyncTask.retryInterval
			} else {
				interval = s.SyncTask.syncInterval
			}
			s.SyncTask.mu.Lock()
			s.SyncTask.nextRun = time.Now().Add(interval)
			s.SyncTask.mu.Unlock()
		case <-s.SyncTask.resetChan:
			s.SyncTask.mu.Lock()
			s.SyncTask.nextRun = time.Now().Add(s.SyncTask.syncInterval)
			s.SyncTask.mu.Unlock()
		}
	}
}

func (s *IPBlocksInfoService) ResetPeriodicSync() {
	s.SyncTask.resetChan <- struct{}{}
}

func (s *IPBlocksInfoService) UpdateIPBlocksInfo(ctx context.Context, vpcConfigCR *v1alpha1.VPCNetworkConfiguration) error {
	log.V(1).Info("update IPBlocksInfo for VPCNetworkConfiguration", "name", vpcConfigCR.Name)
	externalIPCIDRs, privateTGWIPCIDRs, err := s.getIPBlockCIDRsByVPCConfig([]v1alpha1.VPCNetworkConfiguration{*vpcConfigCR})
	if err != nil {
		return err
	}
	// create or update IPBlocksInfo CR
	ipBlocksInfo := &v1alpha1.IPBlocksInfo{
		ObjectMeta: metav1.ObjectMeta{
			Name: ipBlocksInfoCRDName,
		},
		ExternalIPCIDRs:   externalIPCIDRs,
		PrivateTGWIPCIDRs: privateTGWIPCIDRs,
	}
	return s.createOrUpdateIPBlocksInfo(ctx, ipBlocksInfo, true)
}

func (s *IPBlocksInfoService) SyncIPBlocksInfo(ctx context.Context) error {
	log.V(1).Info("start to synchronize IPBlocksInfo")
	// List all VpcNetworkConfiguration CRs
	crdVpcNetworkConfigurationList := &v1alpha1.VPCNetworkConfigurationList{}
	err := s.Client.List(ctx, crdVpcNetworkConfigurationList)
	if err != nil {
		log.Error(err, "failed to list VpcnetworkConfiguration CR")
		return err
	}
	externalIPCIDRs, privateTGWIPCIDRs, err := s.getIPBlockCIDRsByVPCConfig(crdVpcNetworkConfigurationList.Items)
	if err != nil {
		return err
	}

	// create or update IPBlocksInfo CR
	ipBlocksInfo := &v1alpha1.IPBlocksInfo{
		ObjectMeta: metav1.ObjectMeta{
			Name: ipBlocksInfoCRDName,
		},
		ExternalIPCIDRs:   externalIPCIDRs,
		PrivateTGWIPCIDRs: privateTGWIPCIDRs,
	}
	return s.createOrUpdateIPBlocksInfo(ctx, ipBlocksInfo, false)
}

func (s *IPBlocksInfoService) createOrUpdateIPBlocksInfo(ctx context.Context, ipBlocksInfo *v1alpha1.IPBlocksInfo, incremental bool) error {
	updateLock.Lock()
	defer updateLock.Unlock()
	ipBlocksInfoOld := &v1alpha1.IPBlocksInfo{}
	namespacedName := types.NamespacedName{Name: ipBlocksInfo.Name}
	err := s.Client.Get(ctx, namespacedName, ipBlocksInfoOld)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "failed to get IPBlocksInfo CR", "name", ipBlocksInfo.Name)
			return err
		} else {
			err = s.Client.Create(ctx, ipBlocksInfo)
			if err != nil {
				log.Error(err, "failed to create IPBlocksInfo CR", "name", ipBlocksInfo.Name)
				return err
			}
			log.V(1).Info("successfully created IPBlocksInfo CR", "IPBlocksInfo", ipBlocksInfo)
			return err
		}
	}
	if incremental {
		ipBlocksInfo.ExternalIPCIDRs = util.MergeArraysWithoutDuplicate(ipBlocksInfoOld.ExternalIPCIDRs, ipBlocksInfo.ExternalIPCIDRs)
		ipBlocksInfo.PrivateTGWIPCIDRs = util.MergeArraysWithoutDuplicate(ipBlocksInfoOld.PrivateTGWIPCIDRs, ipBlocksInfo.PrivateTGWIPCIDRs)
	}
	if util.CompareArraysWithoutOrder(ipBlocksInfoOld.ExternalIPCIDRs, ipBlocksInfo.ExternalIPCIDRs) &&
		util.CompareArraysWithoutOrder(ipBlocksInfoOld.PrivateTGWIPCIDRs, ipBlocksInfo.PrivateTGWIPCIDRs) {
		// no need to update if all IPBlocks do not change
		return nil
	}
	ipBlocksInfoOld.ExternalIPCIDRs = ipBlocksInfo.ExternalIPCIDRs
	ipBlocksInfoOld.PrivateTGWIPCIDRs = ipBlocksInfo.PrivateTGWIPCIDRs
	err = s.Client.Update(ctx, ipBlocksInfoOld)
	if err != nil {
		log.Error(err, "failed to update IPBlocksInfo CR", "name", ipBlocksInfoOld.Name)
		return err
	}
	log.V(1).Info("successfully updated IPBlocksInfo CR", "IPBlocksInfo", ipBlocksInfoOld)
	return nil
}

func (s *IPBlocksInfoService) getIPBlockCIDRsByVPCConfig(vpcConfigList []v1alpha1.VPCNetworkConfiguration) (externalIPCIDRs []string, privateTGWIPCIDRs []string, err error) {
	// Map saves the resource path and if it associated with a default project
	vpcConnectivityProfileProjectMap := make(map[string]bool)
	vpcs := sets.New[string]()
	for _, vpcConfigCR := range vpcConfigList {
		// all auto-created VPCs share the same VPCConnectivityProfile which is associated with default project
		// only archieve the VPCConnectivityProfile for the default one
		if vpcConfigCR.Spec.VPCConnectivityProfile != "" {
			isDefault := isDefaultNetworkConfigCR(vpcConfigCR)
			if isDefault {
				path := vpcConfigCR.Spec.VPCConnectivityProfile
				// add project path prefix for id only VPCConnectivityProfile
				pathSlice := strings.Split(path, "/")
				if len(pathSlice) == 1 {
					path = fmt.Sprintf("%s/vpc-connectivity-profiles/%s", vpcConfigCR.Spec.NSXProject, path)
				}
				vpcConnectivityProfileProjectMap[path] = true
				s.defaultProject = vpcConfigCR.Spec.NSXProject
			}
		} else {
			// For pre-created VPCNetworkConfigurations, get VPC
			path := vpcConfigCR.Spec.VPC
			// add project path prefix for id only VPC
			pathSlice := strings.Split(path, "/")
			if len(pathSlice) == 1 {
				path = fmt.Sprintf("%s/vpcs/%s", vpcConfigCR.Spec.NSXProject, path)
			}
			vpcs.Insert(path)
		}
	}
	// Skip the IPBlocksInfo updating before the default project is found
	if s.defaultProject == "" {
		return externalIPCIDRs, privateTGWIPCIDRs, fmt.Errorf("default project not found, try later")
	}

	// for all VPC path, get VPCConnectivityProfile from VPC attachment
	vpcAttachmentStore := NewVpcAttachmentStore()
	queryParam := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeVpcAttachment)
	count, err := s.SearchResource(common.ResourceTypeVpcAttachment, queryParam, vpcAttachmentStore, nil)
	if err != nil {
		log.Error(err, "failed to query VPC attachment")
		return externalIPCIDRs, privateTGWIPCIDRs, err
	}
	log.V(2).Info("successfully fetch all VPC Attachment from NSX", "count", count)

	for vpcPath := range vpcs {
		vpcResInfo, err := common.ParseVPCResourcePath(vpcPath)
		if err != nil {
			return externalIPCIDRs, privateTGWIPCIDRs, fmt.Errorf("invalid VPC path %s", vpcPath)
		}
		// for pre-created VPC, mark as default for those under default project
		vpcProjectPath := fmt.Sprintf("/orgs/%s/projects/%s", vpcResInfo.OrgID, vpcResInfo.ProjectID)
		vpcAttachments := vpcAttachmentStore.GetByVpcPath(vpcPath)
		if len(vpcAttachments) == 0 {
			err = fmt.Errorf("no VPC attachment found")
			log.Error(err, "get VPC attachment", "VPC Path", vpcPath)
			return externalIPCIDRs, privateTGWIPCIDRs, err
		}
		log.V(2).Info("successfully fetch VPC attachment", "path", vpcPath, "VPC Attachment", vpcAttachments[0])
		vpcConnectivityProfile := vpcAttachments[0].VpcConnectivityProfile
		if vpcProjectPath == s.defaultProject {
			vpcConnectivityProfileProjectMap[*vpcConnectivityProfile] = true
		} else {
			vpcConnectivityProfileProjectMap[*vpcConnectivityProfile] = false
		}
	}

	// for all VPCConnectivityProfile, get all external IPBlocks and project IPBlock for default project
	externalIPBlockPaths := sets.New[string]()
	privateTgwIPBlockPaths := sets.New[string]()

	vpcConnectivityProfileStore := &VPCConnectivityProfileStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.VpcConnectivityProfileBindingType(),
	}}
	queryParam = fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeVpcConnectivityProfile)
	count, err = s.SearchResource(common.ResourceTypeVpcConnectivityProfile, queryParam, vpcConnectivityProfileStore, nil)
	if err != nil {
		return externalIPCIDRs, privateTGWIPCIDRs, err
	}
	log.V(2).Info("successfully fetch all VPCConnectivityProfile from NSX", "count", count)

	for profilePath, isDefault := range vpcConnectivityProfileProjectMap {
		obj := vpcConnectivityProfileStore.GetByKey(profilePath)
		if obj == nil {
			return externalIPCIDRs, privateTGWIPCIDRs, fmt.Errorf("failed to get VPCConnectivityProfile %s from NSX", profilePath)
		}
		vpcConnectivityProfile := obj.(*model.VpcConnectivityProfile)
		log.V(2).Info("successfully fetch VPCConnectivityProfile", "path", profilePath, "isDefault", isDefault)
		// save external_ip_blocks path in set for all profile
		for _, externalIPBlock := range vpcConnectivityProfile.ExternalIpBlocks {
			externalIPBlockPaths.Insert(externalIPBlock)
		}
		// save private_tgw_ip_blocks path in set for profile associated with default project
		if isDefault {
			for _, privateTgwIpBlocks := range vpcConnectivityProfile.PrivateTgwIpBlocks {
				privateTgwIPBlockPaths.Insert(privateTgwIpBlocks)
			}
		}
	}

	// get IPBlock CIDRs from NSX
	ipBlockStore := &IPBlockStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.IpAddressBlockBindingType(),
	}}
	queryParam = fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeIPBlock)
	count, err = s.SearchResource(common.ResourceTypeIPBlock, queryParam, ipBlockStore, nil)
	if err != nil {
		return externalIPCIDRs, privateTGWIPCIDRs, err
	}
	log.V(2).Info("successfully fetch all IPBlocks from NSX", "count", count)

	if externalIPCIDRs, err = s.getIPBlockCIDRsFromStore(externalIPBlockPaths, ipBlockStore); err != nil {
		return nil, nil, err
	}
	if privateTGWIPCIDRs, err = s.getIPBlockCIDRsFromStore(privateTgwIPBlockPaths, ipBlockStore); err != nil {
		return nil, nil, err
	}
	return externalIPCIDRs, privateTGWIPCIDRs, nil
}

func isDefaultNetworkConfigCR(vpcConfigCR v1alpha1.VPCNetworkConfiguration) bool {
	annos := vpcConfigCR.GetAnnotations()
	val, exist := annos[common.AnnotationDefaultNetworkConfig]
	if exist {
		boolVar, err := strconv.ParseBool(val)
		if err != nil {
			log.Error(err, "failed to parse annotation to check default NetworkConfig", "Annotation", annos[common.AnnotationDefaultNetworkConfig])
			return false
		}
		return boolVar
	}
	return false
}

func (s *IPBlocksInfoService) getIPBlockCIDRsFromStore(pathSet sets.Set[string], ipBlockStore *IPBlockStore) ([]string, error) {
	ipCIDRs := []string{}
	for path := range pathSet {
		obj := ipBlockStore.GetByKey(path)
		if obj == nil {
			return nil, fmt.Errorf("failed to get IPBlock %s from NSX", path)
		}
		ipblock := obj.(*model.IpAddressBlock)
		if ipblock.Cidr == nil {
			return nil, fmt.Errorf("failed to get CIDR from ipblock %s", path)
		}
		log.V(2).Info("successfully get cidr for IPBblock", "path", path, "cidr", *ipblock.Cidr)
		ipCIDRs = append(ipCIDRs, *ipblock.Cidr)
	}
	return ipCIDRs, nil
}
