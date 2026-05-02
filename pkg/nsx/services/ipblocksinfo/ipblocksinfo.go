package ipblocksinfo

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	api_errors "k8s.io/apimachinery/pkg/api/errors"
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
	log                 = logger.Log
	ipBlocksInfoCRDName = "ip-blocks-info"
	syncInterval        = 10 * time.Minute
	retryInterval       = 30 * time.Second
	updateLock          = &sync.Mutex{}
)

type IPBlocksInfoService struct {
	common.Service
	subnetService  common.SubnetServiceProvider
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

func InitializeIPBlocksInfoService(service common.Service, subnetService common.SubnetServiceProvider) *IPBlocksInfoService {
	ipBlocksInfoService := &IPBlocksInfoService{
		Service:       service,
		SyncTask:      NewIPBlocksInfoSyncTask(syncInterval, retryInterval),
		subnetService: subnetService,
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

// SplitCIDRsByIPFamily splits CIDR strings into IPv4-only and IPv6-only slices based on parsed address family.
func SplitCIDRsByIPFamily(cidrs []string) (ipv4 []string, ipv6 []string) {
	for _, cidr := range cidrs {
		ip, _, err := net.ParseCIDR(cidr)
		if err != nil {
			log.Error(err, "failed to parse CIDR for IP family split", "cidr", cidr)
			continue
		}
		if ip.To4() != nil {
			ipv4 = append(ipv4, cidr)
		} else {
			ipv6 = append(ipv6, cidr)
		}
	}
	return ipv4, ipv6
}

// SplitIPPoolRangesByFamily splits IP pool ranges into IPv4-only and IPv6-only slices using the start address family.
func SplitIPPoolRangesByFamily(ranges []v1alpha1.IPPoolRange) (ipv4 []v1alpha1.IPPoolRange, ipv6 []v1alpha1.IPPoolRange) {
	for _, r := range ranges {
		ip := net.ParseIP(r.Start)
		if ip == nil {
			log.Info("skipping IP pool range with unparseable start", "start", r.Start, "end", r.End)
			continue
		}
		if ip.To4() != nil {
			ipv4 = append(ipv4, r)
		} else {
			ipv6 = append(ipv6, r)
		}
	}
	return ipv4, ipv6
}

// mergeIPCidrs merges target CIDRs into source CIDRs if not already covered by source.
// Works for both IPv4 and IPv6 when source and target use the same address family.
// Assume there were no duplicate CIDRs in target and no element of target is a subset of another element in target.
// consider using radix tree or sort + binary search for large scale
func (s *IPBlocksInfoService) mergeIPCidrs(source []string, target []string) []string {
	if len(source) == 0 {
		return target
	}
	// Parse source CIDRs
	var sourceNets []*net.IPNet
	var result []string
	for _, cidr := range source {
		_, net, err := net.ParseCIDR(cidr)
		if err != nil {
			log.Error(err, "failed to parse CIDR", "cidr", cidr)
			continue
		}
		sourceNets = append(sourceNets, net)
		result = append(result, cidr)
	}

	for _, t := range target {
		ip, tNet, err := net.ParseCIDR(t)
		if err != nil {
			log.Error(err, "failed to parse CIDR", "cidr", t)
			continue
		}
		covered := false
		for _, sNet := range sourceNets {
			// Check if tNet is fully contained in sNet
			if cidrSubset(ip, tNet, sNet) {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, t)
			sourceNets = append(sourceNets, tNet)
		}
	}
	return result
}

// cidrSubset returns true if tNet is a subset of sNet
func cidrSubset(ip net.IP, tNet, sNet *net.IPNet) bool {
	return sNet.Contains(ip) && maskContains(sNet.Mask, tNet.Mask)
}

// maskContains returns true if sMask is equal or shorter than tMask
func maskContains(sMask, tMask net.IPMask) bool {
	onesS, bitsS := sMask.Size()
	onesT, bitsT := tMask.Size()
	return bitsS == bitsT && onesT >= onesS
}

func (s *IPBlocksInfoService) UpdateIPBlocksInfo(ctx context.Context, vpcConfigCR *v1alpha1.VPCNetworkConfiguration) error {
	log.Debug("update IPBlocksInfo for VPCNetworkConfiguration", "name", vpcConfigCR.Name)
	return s.updateIPBlocksInfo(ctx, []v1alpha1.VPCNetworkConfiguration{*vpcConfigCR}, true)
}

func (s *IPBlocksInfoService) updateIPBlocksInfo(ctx context.Context, vpcConfigList []v1alpha1.VPCNetworkConfiguration, incremental bool) error {
	externalIPCIDRs, privateTGWIPCIDRs, externalIPRanges, privateTGWIPRanges, err := s.getIPBlockCIDRsByVPCConfig(vpcConfigList)
	if err != nil {
		return err
	}
	externalSubnetCIDRs, privateTGWSubnetCIDRS, err := s.getSharedSubnetsCIDRs(vpcConfigList)
	if err != nil {
		return err
	}
	externalIPCIDRs = s.mergeIPCidrs(externalIPCIDRs, externalSubnetCIDRs)
	privateTGWIPCIDRs = s.mergeIPCidrs(privateTGWIPCIDRs, privateTGWSubnetCIDRS)
	extV4, extV6 := SplitCIDRsByIPFamily(externalIPCIDRs)
	privV4, privV6 := SplitCIDRsByIPFamily(privateTGWIPCIDRs)
	extRv4, extRv6 := SplitIPPoolRangesByFamily(externalIPRanges)
	privRv4, privRv6 := SplitIPPoolRangesByFamily(privateTGWIPRanges)
	// create or update IPBlocksInfo CR
	ipBlocksInfo := &v1alpha1.IPBlocksInfo{
		ObjectMeta: metav1.ObjectMeta{
			Name: ipBlocksInfoCRDName,
		},
		ExternalIPCIDRs:       extV4,
		ExternalIPv6CIDRs:     extV6,
		PrivateTGWIPCIDRs:     privV4,
		PrivateTGWIPv6CIDRs:   privV6,
		ExternalIPRanges:      extRv4,
		ExternalIPv6Ranges:    extRv6,
		PrivateTGWIPRanges:    privRv4,
		PrivateTGWIPv6Ranges:  privRv6,
	}
	return s.createOrUpdateIPBlocksInfo(ctx, ipBlocksInfo, incremental)
}

func (s *IPBlocksInfoService) SyncIPBlocksInfo(ctx context.Context) error {
	log.Debug("start to synchronize IPBlocksInfo")
	// List all VpcNetworkConfiguration CRs
	crdVpcNetworkConfigurationList := &v1alpha1.VPCNetworkConfigurationList{}
	err := s.Client.List(ctx, crdVpcNetworkConfigurationList)
	if err != nil {
		log.Error(err, "failed to list VpcnetworkConfiguration CR")
		return err
	}
	return s.updateIPBlocksInfo(ctx, crdVpcNetworkConfigurationList.Items, false)
}

// migrateLegacyIPv6IntoDedicatedFields moves IPv6 CIDRs/ranges out of the IPv4-oriented fields into
// externalIPv6*/privateTGWIPv6* fields. Returns true if the in-memory object was changed and should be persisted.
func (s *IPBlocksInfoService) migrateLegacyIPv6IntoDedicatedFields(info *v1alpha1.IPBlocksInfo) (changed bool) {
	extV4, extV6 := SplitCIDRsByIPFamily(info.ExternalIPCIDRs)
	if len(extV6) > 0 {
		changed = true
		info.ExternalIPv6CIDRs = s.mergeIPCidrs(extV6, info.ExternalIPv6CIDRs)
	}
	info.ExternalIPCIDRs = extV4

	privV4, privV6 := SplitCIDRsByIPFamily(info.PrivateTGWIPCIDRs)
	if len(privV6) > 0 {
		changed = true
		info.PrivateTGWIPv6CIDRs = s.mergeIPCidrs(privV6, info.PrivateTGWIPv6CIDRs)
	}
	info.PrivateTGWIPCIDRs = privV4

	extRv4, extRv6 := SplitIPPoolRangesByFamily(info.ExternalIPRanges)
	if len(extRv6) > 0 {
		changed = true
		info.ExternalIPv6Ranges = util.MergeArraysWithoutDuplicate(extRv6, info.ExternalIPv6Ranges)
	}
	info.ExternalIPRanges = extRv4

	privRv4, privRv6 := SplitIPPoolRangesByFamily(info.PrivateTGWIPRanges)
	if len(privRv6) > 0 {
		changed = true
		info.PrivateTGWIPv6Ranges = util.MergeArraysWithoutDuplicate(privRv6, info.PrivateTGWIPv6Ranges)
	}
	info.PrivateTGWIPRanges = privRv4
	return changed
}

func (s *IPBlocksInfoService) createOrUpdateIPBlocksInfo(ctx context.Context, ipBlocksInfo *v1alpha1.IPBlocksInfo, incremental bool) error {
	updateLock.Lock()
	defer updateLock.Unlock()
	ipBlocksInfoOld := &v1alpha1.IPBlocksInfo{}
	namespacedName := types.NamespacedName{Name: ipBlocksInfo.Name}
	err := s.Client.Get(ctx, namespacedName, ipBlocksInfoOld)
	if err != nil {
		if !api_errors.IsNotFound(err) {
			log.Error(err, "failed to get IPBlocksInfo CR", "name", ipBlocksInfo.Name)
			return err
		} else {
			err = s.Client.Create(ctx, ipBlocksInfo)
			if err != nil {
				log.Error(err, "failed to create IPBlocksInfo CR", "name", ipBlocksInfo.Name)
				return err
			}
			log.Debug("Successfully created IPBlocksInfo CR", "IPBlocksInfo", ipBlocksInfo)
			return err
		}
	}
	migrateNeeded := s.migrateLegacyIPv6IntoDedicatedFields(ipBlocksInfoOld)
	if incremental {
		oldExtV4, oldExtV6 := SplitCIDRsByIPFamily(ipBlocksInfoOld.ExternalIPCIDRs)
		oldPrivV4, oldPrivV6 := SplitCIDRsByIPFamily(ipBlocksInfoOld.PrivateTGWIPCIDRs)
		oldExtRv4, oldExtRv6 := SplitIPPoolRangesByFamily(ipBlocksInfoOld.ExternalIPRanges)
		oldPrivRv4, oldPrivRv6 := SplitIPPoolRangesByFamily(ipBlocksInfoOld.PrivateTGWIPRanges)
		ipBlocksInfo.ExternalIPCIDRs = s.mergeIPCidrs(oldExtV4, ipBlocksInfo.ExternalIPCIDRs)
		ipBlocksInfo.ExternalIPv6CIDRs = s.mergeIPCidrs(s.mergeIPCidrs(oldExtV6, ipBlocksInfoOld.ExternalIPv6CIDRs), ipBlocksInfo.ExternalIPv6CIDRs)
		ipBlocksInfo.PrivateTGWIPCIDRs = s.mergeIPCidrs(oldPrivV4, ipBlocksInfo.PrivateTGWIPCIDRs)
		ipBlocksInfo.PrivateTGWIPv6CIDRs = s.mergeIPCidrs(s.mergeIPCidrs(oldPrivV6, ipBlocksInfoOld.PrivateTGWIPv6CIDRs), ipBlocksInfo.PrivateTGWIPv6CIDRs)
		ipBlocksInfo.ExternalIPRanges = util.MergeArraysWithoutDuplicate(oldExtRv4, ipBlocksInfo.ExternalIPRanges)
		ipBlocksInfo.ExternalIPv6Ranges = util.MergeArraysWithoutDuplicate(util.MergeArraysWithoutDuplicate(oldExtRv6, ipBlocksInfoOld.ExternalIPv6Ranges), ipBlocksInfo.ExternalIPv6Ranges)
		ipBlocksInfo.PrivateTGWIPRanges = util.MergeArraysWithoutDuplicate(oldPrivRv4, ipBlocksInfo.PrivateTGWIPRanges)
		ipBlocksInfo.PrivateTGWIPv6Ranges = util.MergeArraysWithoutDuplicate(util.MergeArraysWithoutDuplicate(oldPrivRv6, ipBlocksInfoOld.PrivateTGWIPv6Ranges), ipBlocksInfo.PrivateTGWIPv6Ranges)
	}
	if util.CompareArraysWithoutOrder(ipBlocksInfoOld.ExternalIPCIDRs, ipBlocksInfo.ExternalIPCIDRs) &&
		util.CompareArraysWithoutOrder(ipBlocksInfoOld.PrivateTGWIPCIDRs, ipBlocksInfo.PrivateTGWIPCIDRs) &&
		util.CompareArraysWithoutOrder(ipBlocksInfoOld.ExternalIPRanges, ipBlocksInfo.ExternalIPRanges) &&
		util.CompareArraysWithoutOrder(ipBlocksInfoOld.PrivateTGWIPRanges, ipBlocksInfo.PrivateTGWIPRanges) &&
		util.CompareArraysWithoutOrder(ipBlocksInfoOld.ExternalIPv6CIDRs, ipBlocksInfo.ExternalIPv6CIDRs) &&
		util.CompareArraysWithoutOrder(ipBlocksInfoOld.PrivateTGWIPv6CIDRs, ipBlocksInfo.PrivateTGWIPv6CIDRs) &&
		util.CompareArraysWithoutOrder(ipBlocksInfoOld.ExternalIPv6Ranges, ipBlocksInfo.ExternalIPv6Ranges) &&
		util.CompareArraysWithoutOrder(ipBlocksInfoOld.PrivateTGWIPv6Ranges, ipBlocksInfo.PrivateTGWIPv6Ranges) {
		if !migrateNeeded {
			log.Debug("IPBlocksInfo CR is up to date, no need to update", "name", ipBlocksInfoOld.Name)
			// no need to update if all IPBlocks do not change
			return nil
		}
	}
	ipBlocksInfoOld.ExternalIPCIDRs = ipBlocksInfo.ExternalIPCIDRs
	ipBlocksInfoOld.ExternalIPv6CIDRs = ipBlocksInfo.ExternalIPv6CIDRs
	ipBlocksInfoOld.PrivateTGWIPCIDRs = ipBlocksInfo.PrivateTGWIPCIDRs
	ipBlocksInfoOld.PrivateTGWIPv6CIDRs = ipBlocksInfo.PrivateTGWIPv6CIDRs
	ipBlocksInfoOld.ExternalIPRanges = ipBlocksInfo.ExternalIPRanges
	ipBlocksInfoOld.ExternalIPv6Ranges = ipBlocksInfo.ExternalIPv6Ranges
	ipBlocksInfoOld.PrivateTGWIPRanges = ipBlocksInfo.PrivateTGWIPRanges
	ipBlocksInfoOld.PrivateTGWIPv6Ranges = ipBlocksInfo.PrivateTGWIPv6Ranges

	err = s.Client.Update(ctx, ipBlocksInfoOld)
	if err != nil {
		log.Error(err, "failed to update IPBlocksInfo CR", "name", ipBlocksInfoOld.Name)
		return err
	}
	log.Debug("Successfully updated IPBlocksInfo CR", "IPBlocksInfo", ipBlocksInfoOld)
	return nil
}

func (s *IPBlocksInfoService) getSharedSubnetsCIDRs(vpcConfigList []v1alpha1.VPCNetworkConfiguration) (externalIPCIDRs []string, privateTGWIPCIDRs []string, err error) {
	sharedSubnet := sets.New[string]()
	for _, vpcConfigCR := range vpcConfigList {
		for _, subnet := range vpcConfigCR.Spec.Subnets {
			sharedSubnet.Insert(subnet.Path)
		}
	}
	for _, subnetPath := range sharedSubnet.UnsortedList() {
		vpcInfo, err := common.ParseVPCResourcePath(subnetPath)
		if err != nil {
			log.Warn("failed to parse VPC resource path: err", err, "path", subnetPath)
			continue
		}
		associate := fmt.Sprintf("%s:%s:%s", vpcInfo.ProjectID, vpcInfo.VPCID, vpcInfo.ID)
		subnet, err := s.subnetService.GetNSXSubnetFromCacheOrAPI(associate, false)
		if err != nil {
			log.Warn("failed to get nsx subnet: err", err, "subnetPath", associate)
			continue
		}

		switch *subnet.AccessMode {
		case model.VpcSubnet_ACCESS_MODE_PUBLIC:
			externalIPCIDRs = append(externalIPCIDRs, subnet.IpAddresses...)

		case model.VpcSubnet_ACCESS_MODE_PRIVATE_TGW:
			project := fmt.Sprintf("/orgs/%s/projects/%s", vpcInfo.OrgID, vpcInfo.ProjectID)
			if project == s.defaultProject {
				privateTGWIPCIDRs = append(privateTGWIPCIDRs, subnet.IpAddresses...)
			}
		}
	}
	return externalIPCIDRs, privateTGWIPCIDRs, nil
}

func (s *IPBlocksInfoService) getIPBlockCIDRsByVPCConfig(vpcConfigList []v1alpha1.VPCNetworkConfiguration) (externalIPCIDRs, privateTGWIPCIDRs []string, externalIPRanges, privateTGWIPRanges []v1alpha1.IPPoolRange, err error) {
	// Map saves the resource path and if it associated with a default project
	vpcConnectivityProfileProjectMap := make(map[string]bool)
	vpcs := sets.New[string]()
	var count uint64
	for _, vpcConfigCR := range vpcConfigList {
		// all auto-created VPCs share the same VPCConnectivityProfile which is associated with default project
		// only archieve the VPCConnectivityProfile for the default one
		if vpcConfigCR.Spec.VPCConnectivityProfile != "" {
			isDefault := common.IsDefaultNetworkConfigCR(&vpcConfigCR)
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
		err = fmt.Errorf("default project not found, try later")
		return
	}

	// for all VPC path, get VPCConnectivityProfile from VPC attachment
	vpcAttachmentStore := NewVpcAttachmentStore()
	queryParam := fmt.Sprintf("%s:%s", common.ResourceType, common.ResourceTypeVpcAttachment)
	count, err = s.SearchResource(common.ResourceTypeVpcAttachment, queryParam, vpcAttachmentStore, nil)
	if err != nil {
		log.Error(err, "failed to query VPC attachment")
		return
	}
	log.Trace("successfully fetch all VPC Attachment from NSX", "count", count)

	for vpcPath := range vpcs {
		var vpcResInfo common.VPCResourceInfo
		vpcResInfo, err = common.ParseVPCResourcePath(vpcPath)
		if err != nil {
			log.Error(err, "failed to parse VPC resource path")
			return
		}
		// for pre-created VPC, mark as default for those under default project
		vpcProjectPath := fmt.Sprintf("/orgs/%s/projects/%s", vpcResInfo.OrgID, vpcResInfo.ProjectID)
		vpcAttachments := vpcAttachmentStore.GetByVpcPath(vpcPath)
		// pre-created VPC may not have vpc attachments
		if len(vpcAttachments) == 0 {
			log.Debug("No VPC attachment found", "VPC Path", vpcPath)
			continue
		}
		log.Trace("Successfully fetch VPC attachment", "path", vpcPath, "VPC Attachment", vpcAttachments[0])
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
		return
	}
	log.Trace("successfully fetch all VPCConnectivityProfile from NSX", "count", count)

	for profilePath, isDefault := range vpcConnectivityProfileProjectMap {
		obj := vpcConnectivityProfileStore.GetByKey(profilePath)
		if obj == nil {
			err = fmt.Errorf("failed to get VPCConnectivityProfile %s from NSX", profilePath)
			return
		}
		vpcConnectivityProfile := obj.(*model.VpcConnectivityProfile)
		log.Trace("successfully fetch VPCConnectivityProfile", "path", profilePath, "isDefault", isDefault)
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
		return
	}
	log.Debug("successfully fetch all IPBlocks from NSX", "count", count)
	return s.getCIDRsRangesFromStore(externalIPBlockPaths, privateTgwIPBlockPaths, ipBlockStore)
}

func (s *IPBlocksInfoService) getCIDRsRangesFromStore(externalIPBlockPaths, privateTgwIPBlockPaths sets.Set[string], ipBlockStore *IPBlockStore) (externalIPCIDRs, privateTGWIPCIDRs []string, externalIPRanges, privateTGWIPRanges []v1alpha1.IPPoolRange, err error) {
	externalIPCIDRs, externalIPRanges, err = s.getIPBlockCIDRsAndRangesFromStore(externalIPBlockPaths, ipBlockStore)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	privateTGWIPCIDRs, privateTGWIPRanges, err = s.getIPBlockCIDRsAndRangesFromStore(privateTgwIPBlockPaths, ipBlockStore)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return externalIPCIDRs, privateTGWIPCIDRs, externalIPRanges, privateTGWIPRanges, nil
}

// getIPBlockCIDRsAndRangesFromStore retrieves the CIDRs/CIDR/ranges for the given IPBlock paths from the store.
// It will not return error if no CIDRs/CIDR found, since one ipblock may only have ranges
func (s *IPBlocksInfoService) getIPBlockCIDRsAndRangesFromStore(pathSet sets.Set[string], ipBlockStore *IPBlockStore) (cidrs []string, ranges []v1alpha1.IPPoolRange, err error) {
	ipCIDRs := []string{}
	ipRanges := []v1alpha1.IPPoolRange{}
	for path := range pathSet {
		obj := ipBlockStore.GetByKey(path)
		if obj == nil {
			err := fmt.Errorf("failed to get IPBlock %s from NSX", path)
			log.Error(err, "get CIDRs/Ranges from ipblock")
			return nil, nil, err
		}
		ipblock := obj.(*model.IpAddressBlock)
		if ipblock.Cidrs != nil {
			ipCIDRs = append(ipCIDRs, ipblock.Cidrs...)
			log.Trace("Successfully get cidrs for IPBlock", "path", path, "cidrs", ipblock.Cidrs)
		} else if ipblock.Cidr != nil { //nolint:staticcheck //ipblock.Cidr is deprecated
			ipCIDRs = append(ipCIDRs, *ipblock.Cidr)                                             //nolint:staticcheck //ipblock.Cidr is deprecated
			log.Trace("Successfully get cidrs for IPBlock", "path", path, "cidrs", ipblock.Cidr) //nolint:staticcheck //ipblock.Cidr is deprecated
		} else {
			log.Info("No CIDRs found for IPBlock", "path", path)
		}
		if ipblock.Ranges != nil {
			log.Trace("Successfully get ranges for IPBlock", "path", path, "ranges", ipblock.Ranges)
			for _, r := range ipblock.Ranges {
				ipRanges = append(ipRanges, v1alpha1.IPPoolRange{
					Start: *r.Start,
					End:   *r.End,
				})
			}
		}
	}
	log.Trace("Successfully get all CIDRs/Ranges from IPBlocks", "cidrs", ipCIDRs, "ranges", ipRanges, "pathset", pathSet.UnsortedList())
	return ipCIDRs, ipRanges, nil
}
