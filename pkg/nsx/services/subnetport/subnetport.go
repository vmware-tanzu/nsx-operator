/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	mp_model "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                    = logger.Log
	ResourceTypeSubnetPort = servicecommon.ResourceTypeSubnetPort
	MarkedForDelete        = true
	IPReleaseTime          = 2 * time.Minute
)

type SubnetPortService struct {
	servicecommon.Service
	SubnetPortStore            *SubnetPortStore
	VPCService                 servicecommon.VPCServiceProvider
	IpAddressAllocationService servicecommon.IPAddressAllocationServiceProvider
	builder                    *servicecommon.PolicyTreeBuilder[*model.VpcSubnetPort]
	macPool                    *mp_model.MacPool
}

// InitializeSubnetPort sync NSX resources.
func InitializeSubnetPort(service servicecommon.Service, vpcService servicecommon.VPCServiceProvider, ipAddressAllocationService servicecommon.IPAddressAllocationServiceProvider) (*SubnetPortService, error) {
	builder, _ := servicecommon.PolicyPathVpcSubnetPort.NewPolicyTreeBuilder()

	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(1)

	subnetPortService := &SubnetPortService{
		Service:                    service,
		VPCService:                 vpcService,
		IpAddressAllocationService: ipAddressAllocationService,
		builder:                    builder,
	}

	subnetPortService.SubnetPortStore = setupStore()

	if err := subnetPortService.loadNSXMacPool(); err != nil {
		return subnetPortService, err
	}

	go subnetPortService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeSubnetPort, nil, subnetPortService.SubnetPortStore)
	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		wg.Wait()
		close(fatalErrors)
		return subnetPortService, err
	}

	return subnetPortService, nil
}

func setupStore() *SubnetPortStore {
	return &SubnetPortStore{
		ResourceStore: servicecommon.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc,
				cache.Indexers{
					servicecommon.TagScopeSubnetPortCRUID: subnetPortIndexByCRUID,
					servicecommon.TagScopePodUID:          subnetPortIndexByPodUID,
					servicecommon.TagScopeVMNamespace:     subnetPortIndexNamespace,
					servicecommon.TagScopeNamespace:       subnetPortIndexPodNamespace,
					servicecommon.IndexKeySubnetID:        subnetPortIndexBySubnetID,
				}),
			BindingType: model.VpcSubnetPortBindingType(),
		}}
}

func (service *SubnetPortService) loadNSXMacPool() error {
	pageSize := int64(1000)
	macPools, err := service.NSXClient.MacPoolsClient.List(nil, nil, &pageSize, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to get NSX MAC Pools: %w", err)
	}
	for _, macPool := range macPools.Results {
		if macPool.DisplayName != nil && *macPool.DisplayName == defaultContainerMacPoolName {
			service.macPool = &macPool
			log.Debug("Get NSX MAC Pool", "MacPool", macPool)
		}
	}
	return nil
}

func (service *SubnetPortService) portAlreadyRealized(obj interface{}, nsxSubnetPort *model.VpcSubnetPort) bool {
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		// In the scale case, the port's realized binding may not be set immediately after port creation, so need to check it.
		if v := nsxSubnetPort.Attachment.AllocateAddresses; v != nil && (*v == "BOTH" || *v == "IP_POOL") {
			if len(o.Status.NetworkInterfaceConfig.IPAddresses) == 0 || o.Status.NetworkInterfaceConfig.IPAddresses[0].IPAddress == "" {
				return false
			}
		}
		if v := nsxSubnetPort.Attachment.AllocateAddresses; v != nil && (*v == "BOTH" || *v == "MAC_POOL") {
			if o.Status.NetworkInterfaceConfig.MACAddress == "" {
				return false
			}
		}
		for _, cond := range o.Status.Conditions {
			if cond.Reason == "SubnetPortReady" && cond.Status == v1.ConditionTrue && len(o.Status.Attachment.ID) > 0 && len(o.Status.NetworkInterfaceConfig.IPAddresses) > 0 && o.Status.NetworkInterfaceConfig.IPAddresses[0].Gateway != "" {
				return true
			}
		}
	case *v1.Pod:
		annotations := o.GetAnnotations()
		if annotations != nil {
			if _, exist := annotations[servicecommon.AnnotationPodMAC]; exist {
				return true
			}
		}
	}
	return false
}

func (service *SubnetPortService) CreateOrUpdateSubnetPort(obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool) (*model.SegmentPortState, bool, error) {
	var uid string
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		uid = string(o.UID)
	case *v1.Pod:
		uid = string(o.UID)
	}
	log.Info("Creating or updating subnetport", "nsxSubnetPort.Id", uid, "nsxSubnetPath", *nsxSubnet.Path)
	enableDHCP := util.NSXSubnetDHCPEnabled(nsxSubnet)
	nsxSubnetPort, err := service.buildSubnetPort(obj, nsxSubnet, contextID, tags, isVmSubnetPort, restoreMode)
	if err != nil {
		log.Error(err, "failed to build NSX subnet port", "nsxSubnetPort.Id", uid, "*nsxSubnet.Path", *nsxSubnet.Path, "contextID", contextID)
		return nil, false, err
	}
	existingSubnetPort := service.SubnetPortStore.GetByKey(*nsxSubnetPort.Id)
	isChanged := true
	if existingSubnetPort != nil {
		// The existing port's attachment ID should not be changed in any case.
		if existingSubnetPort.Attachment != nil {
			nsxSubnetPort.Attachment.Id = existingSubnetPort.Attachment.Id
		}
		isChanged = servicecommon.CompareResource(SubnetPortToComparable(existingSubnetPort), SubnetPortToComparable(nsxSubnetPort))
	}
	subnetInfo, err := servicecommon.ParseVPCResourcePath(*nsxSubnet.Path)
	if err != nil {
		return nil, false, err
	}
	if !isChanged {
		log.Info("NSX subnet port not changed, skipping the update", "nsxSubnetPort.Id", nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
		if !restoreMode && service.portAlreadyRealized(obj, nsxSubnetPort) {
			log.Debug("The subnet port is already realized, skip checking the state", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
			return nil, enableDHCP, nil
		}
	} else {
		log.Info("Updating the NSX subnet port", "existingSubnetPort", existingSubnetPort, "desiredSubnetPort", nsxSubnetPort)
		err = service.NSXClient.PortClient.Patch(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxSubnetPort.Id, *nsxSubnetPort)
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			log.Error(err, "failed to create or update subnet port", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
			return nil, false, err
		}
		err = service.SubnetPortStore.Apply(nsxSubnetPort)
		if err != nil {
			return nil, false, err
		}
		if existingSubnetPort != nil {
			log.Info("Updated NSX subnet port", "nsxSubnetPort.Path", *nsxSubnetPort.Path)
		} else {
			log.Info("Created NSX subnet port", "nsxSubnetPort.Path", *nsxSubnetPort.Path)
		}
	}
	nsxSubnetPortState, err := service.CheckSubnetPortState(obj, *nsxSubnet.Path)
	if err != nil {
		log.Error(err, "check and update NSX subnet port state failed, would retry exponentially", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
		return nil, false, err
	}
	createdNSXSubnetPort, err := service.NSXClient.PortClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxSubnetPort.Id)
	if err != nil {
		log.Error(err, "check and update NSX subnet port failed, would retry exponentially", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
		return nil, false, err
	}
	err = service.SubnetPortStore.Apply(&createdNSXSubnetPort)
	if err != nil {
		return nil, false, err
	}
	if isChanged {
		log.Info("Successfully created or updated subnetport", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPortState", nsxSubnetPortState)
	} else {
		log.Info("Subnetport already existed", "subnetport", *nsxSubnetPort.Id, "nsxSubnetPortState", nsxSubnetPortState)
	}
	return nsxSubnetPortState, enableDHCP, nil
}

// CheckSubnetPortState will check the port realized status then get the port state to prepare the CR status.
func (service *SubnetPortService) CheckSubnetPortState(obj interface{}, nsxSubnetPath string) (*model.SegmentPortState, error) {
	var objMeta metav1.ObjectMeta
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		objMeta = o.ObjectMeta
	case *v1.Pod:
		objMeta = o.ObjectMeta
	}

	nsxSubnetPort, err := service.SubnetPortStore.GetVpcSubnetPortByUID(objMeta.UID)
	if err != nil {
		return nil, err
	}
	if nsxSubnetPort == nil {
		return nil, errors.New("failed to get subnet port from store")
	}

	portID := *nsxSubnetPort.Id
	realizeService := realizestate.InitializeRealizeState(service.Service)

	if err := realizeService.CheckRealizeState(util.NSXTRealizeRetry, *nsxSubnetPort.Path, []string{}); err != nil {
		log.Error(err, "Failed to get realized status", "nsxSubnetPortPath", *nsxSubnetPort.Path)
		if nsxutil.IsRealizeStateError(err) {
			realizedStateErr := err.(*nsxutil.RealizeStateError)
			if realizedStateErr.GetCode() == nsxutil.IPAllocationErrorCode {
				service.updateExhaustedSubnet(nsxSubnetPath)
			}
			log.Error(err, "The created SubnetPort is in error realization state, cleaning the resource", "SubnetPort", portID)
			// only recreate subnet port on RealizationErrorStateError.
			if err := service.DeleteSubnetPortById(portID); err != nil {
				log.Error(err, "Cleanup error SubnetPort failed", "SubnetPort", portID)
				return nil, err
			}
		}
		return nil, err
	}
	// TODO: avoid to get subnetport state again if we already got it.
	nsxPortState, err := service.GetSubnetPortState(portID, nsxSubnetPath)
	if err != nil {
		return nil, err
	}
	log.Info("Got the NSX subnet port state", "nsxPortState.RealizedBindings", nsxPortState.RealizedBindings, "uid", portID)
	return nsxPortState, nil
}

func (service *SubnetPortService) GetSubnetPortState(nsxSubnetPortID string, nsxSubnetPath string) (*model.SegmentPortState, error) {
	subnetInfo, _ := servicecommon.ParseVPCResourcePath(nsxSubnetPath)
	nsxSubnetPortState, err := service.NSXClient.PortStateClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, nsxSubnetPortID, nil, nil)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "failed to get subnet port state", "nsxSubnetPortID", nsxSubnetPortID, "nsxSubnetPath", nsxSubnetPath)
		return nil, err
	}
	return &nsxSubnetPortState, nil
}

func (service *SubnetPortService) DeleteSubnetPort(nsxSubnetPort *model.VpcSubnetPort) error {
	subnetPortInfo, _ := servicecommon.ParseVPCResourcePath(*nsxSubnetPort.Path)
	err := service.NSXClient.PortClient.Delete(subnetPortInfo.OrgID, subnetPortInfo.ProjectID, subnetPortInfo.VPCID, subnetPortInfo.ParentID, *nsxSubnetPort.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "failed to delete nsxSubnetPort", "nsxSubnetPort.Path", *nsxSubnetPort.Path)
		return err
	}
	if err = service.SubnetPortStore.Delete(*nsxSubnetPort.Id); err != nil {
		return err
	}
	log.Info("Successfully deleted nsxSubnetPort", "nsxSubnetPortID", *nsxSubnetPort.Id)
	return nil
}

func (service *SubnetPortService) DeleteSubnetPortById(portID string) error {
	nsxSubnetPort := service.SubnetPortStore.GetByKey(portID)
	if nsxSubnetPort == nil || nsxSubnetPort.Id == nil {
		log.Info("NSX subnet port is not found in store, skip deleting it", "id", portID)
		return nil
	}
	return service.DeleteSubnetPort(nsxSubnetPort)
}

func (service *SubnetPortService) ListNSXSubnetPortIDForCR() sets.Set[string] {
	log.Trace("Listing subnet port CR UIDs")
	subnetPortSet := sets.New[string]()
	for _, subnetPortCRUid := range service.SubnetPortStore.ListIndexFuncValues(servicecommon.TagScopeSubnetPortCRUID).UnsortedList() {
		subnetPortIDs, _ := service.SubnetPortStore.IndexKeys(servicecommon.TagScopeSubnetPortCRUID, subnetPortCRUid)
		subnetPortSet.Insert(subnetPortIDs...)
	}
	return subnetPortSet
}

func (service *SubnetPortService) ListNSXSubnetPortIDForPod() sets.Set[string] {
	log.Trace("Listing pod UIDs")
	subnetPortSet := sets.New[string]()
	for _, podUID := range service.SubnetPortStore.ListIndexFuncValues(servicecommon.TagScopePodUID).UnsortedList() {
		subnetPortIDs, _ := service.SubnetPortStore.IndexKeys(servicecommon.TagScopePodUID, podUID)
		subnetPortSet.Insert(subnetPortIDs...)
	}
	return subnetPortSet
}

func (service *SubnetPortService) GetSubnetPathForSubnetPortFromStore(crUid types.UID) string {
	existingSubnetPort, err := service.SubnetPortStore.GetVpcSubnetPortByUID(crUid)
	if err != nil {
		log.Error(err, "Failed to use the CR UID to search VpcSubnetPort, return ''", "CR UID", crUid)
		return ""
	}
	if existingSubnetPort == nil {
		log.Info("SubnetPort is not found in store", "CR UID", crUid)
		return ""
	}
	if existingSubnetPort.ParentPath == nil {
		log.Info("SubnetPort has not set the VpcSubnet path", "CR UID", crUid, "Id", *existingSubnetPort.Id)
		return ""
	}
	return *existingSubnetPort.ParentPath
}

func (service *SubnetPortService) GetPortsOfSubnet(nsxSubnetID string) (ports []*model.VpcSubnetPort) {
	subnetPortList := service.SubnetPortStore.GetByIndex(servicecommon.IndexKeySubnetID, nsxSubnetID)
	return subnetPortList
}

func (service *SubnetPortService) ListSubnetPortIDsFromCRs(ctx context.Context) (sets.Set[string], error) {
	subnetPortList := &v1alpha1.SubnetPortList{}
	err := service.Client.List(ctx, subnetPortList)
	if err != nil {
		log.Error(err, "failed to list SubnetPort CR")
		return nil, err
	}

	crSubnetPortIDsSet := sets.New[string]()
	for _, subnetPort := range subnetPortList.Items {
		vpcSubnetPort, err := service.SubnetPortStore.GetVpcSubnetPortByUID(subnetPort.UID)
		if err != nil {
			log.Error(err, "Failed to get VpcSubnetPort by SubnetPort CR", "CR UID", subnetPort.UID)
			continue
		}
		if vpcSubnetPort != nil {
			crSubnetPortIDsSet.Insert(*vpcSubnetPort.Id)
		}
	}
	return crSubnetPortIDsSet, nil
}

func (service *SubnetPortService) ListSubnetPortByName(ns string, name string) []*model.VpcSubnetPort {
	var result []*model.VpcSubnetPort
	// Get all the SubnetPorts in the namespace, including VM and Pod(image fetcher) SubnetPorts
	vmSubnetPorts := service.SubnetPortStore.GetByIndex(servicecommon.TagScopeVMNamespace, ns)
	podSubnetPorts := service.SubnetPortStore.GetByIndex(servicecommon.TagScopeNamespace, ns)
	subnetPorts := append(vmSubnetPorts, podSubnetPorts...)
	for _, subnetport := range subnetPorts {
		tagName := nsxutil.FindTag(subnetport.Tags, servicecommon.TagScopeSubnetPortCRName)
		if tagName == name {
			result = append(result, subnetport)
		}
	}
	return result
}

func (service *SubnetPortService) ListSubnetPortByPodName(ns string, name string) []*model.VpcSubnetPort {
	var result []*model.VpcSubnetPort
	subnetports := service.SubnetPortStore.GetByIndex(servicecommon.TagScopeNamespace, ns)
	for _, subnetport := range subnetports {
		tagname := nsxutil.FindTag(subnetport.Tags, servicecommon.TagScopePodName)
		if tagname == name {
			result = append(result, subnetport)
		}
	}
	return result
}

// AllocatePortFromSubnet checks the number of SubnetPorts on the Subnet.
// If the Subnet has capacity for the new SubnetPorts, it will increase
// the number of SubnetPort under creation and return true.
func (service *SubnetPortService) AllocatePortFromSubnet(subnet *model.VpcSubnet) (bool, error) {
	dhcpMode := "DHCP_DEACTIVATED"
	subnetInfo, _ := servicecommon.ParseVPCResourcePath(*subnet.Path)
	if subnet.SubnetDhcpConfig != nil && subnet.SubnetDhcpConfig.Mode != nil {
		dhcpMode = *subnet.SubnetDhcpConfig.Mode
	}
	// For DHCP Deactivated mode Subnet, if staticIpAllocation enable:false, skip check IP count
	// and always return true
	staticIpAllocationEnabled := false
	if dhcpMode == "DHCP_DEACTIVATED" {
		if subnet.AdvancedConfig != nil && subnet.AdvancedConfig.StaticIpAllocation != nil && subnet.AdvancedConfig.StaticIpAllocation.Enabled != nil {
			staticIpAllocationEnabled = *subnet.AdvancedConfig.StaticIpAllocation.Enabled
		}
		if !staticIpAllocationEnabled {
			// for staticIpAllocation enable:false case, it can create SubnetPort and skip check the IP count
			return true, nil
		}
	}

	info := &CountInfo{}
	obj, ok := service.SubnetPortStore.PortCountInfo.LoadOrStore(*subnet.Path, info)
	info = obj.(*CountInfo)

	info.lock.Lock()
	defer info.lock.Unlock()

	// For DHCP Server mode Subnet, get total IPs from DHCP IP Pool from NSX each time
	// since user might update reservedIPRanges for the subnet and it impacts the DHCP Pool size
	if dhcpMode == "DHCP_SERVER" {
		dhcpServerStats, err := service.NSXClient.DhcpServerConfigStatsClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, nil, nil, nil, nil, nil, nil, nil)
		if err != nil {
			log.Error(err, "Failed to get Subnet dhcp-server-config stats", "Subnet", *subnet.Path)
			return false, err
		}
		if len(dhcpServerStats.IpPoolStats) > 0 && dhcpServerStats.IpPoolStats[0].PoolSize != nil {
			info.totalIP = int(*dhcpServerStats.IpPoolStats[0].PoolSize)
		}
	}

	if !ok {
		// For DHCP Deactivated mode Subnet, get total IPs from IP pool static-ipv4-default
		if dhcpMode == "DHCP_DEACTIVATED" {
			// only get Subnet total IPs from static IP Pool if staticIpAllocation enabled
			if staticIpAllocationEnabled {
				staticIPPool, err := service.NSXClient.IPPoolClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, "static-ipv4-default")
				if err != nil {
					log.Error(err, "Failed to get Subnet static IP Pool static-ipv4-default", "Subnet", *subnet.Path)
					return false, err
				}
				if staticIPPool.PoolUsage.TotalIps != nil {
					info.totalIP = int(*staticIPPool.PoolUsage.TotalIps)
				}
			}
		}
		// For DHCP Relay mode Subnet, assume 4 reserved IPs
		if dhcpMode == "DHCP_RELAY" {
			var totalIP int
			if subnet.Ipv4SubnetSize != nil {
				totalIP = int(*subnet.Ipv4SubnetSize)
			}
			if len(subnet.IpAddresses) > 0 {
				// totalIP will be overrided if IpAddresses are specified.
				totalIP, _ = util.CalculateIPFromCIDRs(subnet.IpAddresses)
			}
			// NSX reserves 4 ip addresses in each subnet for network address, gateway address,
			// dhcp server address and broadcast address.
			info.totalIP = totalIP - 4
		}
	}

	if time.Since(info.exhaustedCheckTime) < IPReleaseTime {
		return false, nil
	}
	// Number of SubnetPorts on the Subnet includes the SubnetPorts under creation
	// and the SubnetPorts already created
	existingPortCount := len(service.GetPortsOfSubnet(*subnet.Id))
	if info.dirtyCount+existingPortCount < info.totalIP {
		info.dirtyCount += 1
		log.Trace("Allocate Subnetport to Subnet", "Subnet", *subnet.Path, "dirtyPortCount", info.dirtyCount, "existingPortCount", existingPortCount)
		return true, nil
	}
	return false, nil
}

func (service *SubnetPortService) updateExhaustedSubnet(path string) {
	obj, ok := service.SubnetPortStore.PortCountInfo.Load(path)
	if !ok {
		log.Error(nil, "No SubnetPort created on the exhausted Subnet", "nsxSubnetPath", path)
		return
	}
	info := obj.(*CountInfo)
	info.lock.Lock()
	defer info.lock.Unlock()
	log.Trace("Mark Subnet as exhausted", "Subnet", path)
	info.exhaustedCheckTime = time.Now()
}

// ReleasePortInSubnet decreases the number of SubnetPort under creation.
func (service *SubnetPortService) ReleasePortInSubnet(path string) {
	obj, ok := service.SubnetPortStore.PortCountInfo.Load(path)
	if !ok {
		log.Error(nil, "Subnet does not have Subnetport to remove", "Subnet", path)
		return
	}
	info := obj.(*CountInfo)
	info.lock.Lock()
	defer info.lock.Unlock()
	if info.dirtyCount < 1 {
		log.Error(nil, "Subnet does not have Subnetport to remove", "Subnet", path)
		return
	}
	info.dirtyCount -= 1
	log.Trace("Release Subnetport from Subnet", "Subnet", path, "dirtyPortCount", info.dirtyCount)
}

// IsEmptySubnet check if there is any SubnetPort created or being creating on the Subnet.
func (service *SubnetPortService) IsEmptySubnet(id string, path string) bool {
	portCount := len(service.GetPortsOfSubnet(id))
	obj, ok := service.SubnetPortStore.PortCountInfo.Load(path)
	if ok {
		info := obj.(*CountInfo)
		portCount += info.dirtyCount
	}
	return portCount < 1
}

func (service *SubnetPortService) DeletePortCount(path string) {
	service.SubnetPortStore.PortCountInfo.Delete(path)
}
