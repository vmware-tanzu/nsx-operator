/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
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

	go subnetPortService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeSubnetPort, nil, subnetPortService.SubnetPortStore)
	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
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
					// Use Subnet Path instead of Subnet ID as shared Subnet ID on different VPC can be the same
					servicecommon.IndexKeySubnetPath:      subnetPortIndexBySubnetPath,
					servicecommon.TagScopeStatefulSetUID:  subnetPortIndexByStatefulSetUID,
					servicecommon.TagScopeStatefulSetName: subnetPortIndexByStatefulSetName,
					servicecommon.IndexKeyAllStsPorts:     subnetPortIndexBySts,
				}),
			BindingType: model.VpcSubnetPortBindingType(),
		}}
}

// allIPAddressesRealized reports whether every expected address has landed, not just the first.
func allIPAddressesRealized(ipAddresses []v1alpha1.NetworkInterfaceIPAddress, expectedCount int) bool {
	if len(ipAddresses) < expectedCount {
		return false
	}
	for _, ipConfig := range ipAddresses {
		if ipConfig.IPAddress == "" {
			return false
		}
	}
	return true
}

func (service *SubnetPortService) portAlreadyRealized(obj interface{}, nsxSubnetPort *model.VpcSubnetPort) bool {
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		// expectedIPCount is driven by StaticIPAllocationType, not InterfaceIPType: on a
		// mixed-mode Subnet a dual-stack port (InterfaceIPType IPv4IPv6) may statically
		// allocate only one family (e.g. StaticIPAllocationType IPv4), in which case only
		// one IP is ever realized via AllocateAddresses BOTH/IP_POOL - the other family
		// comes from DHCP/SLAAC and isn't tracked here.
		expectedIPCount := 1
		switch o.Spec.StaticIPAllocationType {
		case v1alpha1.StaticIPAllocationTypeIPv4IPv6:
			expectedIPCount = 2
		case "", v1alpha1.StaticIPAllocationTypeNone:
			// Not backfilled yet, or genuinely no static allocation (the AllocateAddresses
			// check below already ensures we only reach here when static allocation is
			// actually active) - fall back to InterfaceIPType as a best-effort default.
			if o.Spec.InterfaceIPType == v1alpha1.IPAddressTypeIPv4IPv6 {
				expectedIPCount = 2
			}
		}
		if len(o.Spec.AddressBindings) > expectedIPCount {
			expectedIPCount = len(o.Spec.AddressBindings)
		}
		// In the scale case, the port's realized binding may not be set immediately after port creation, so need to check it.
		if v := nsxSubnetPort.Attachment.AllocateAddresses; v != nil && (*v == "BOTH" || *v == "IP_POOL") {
			if !allIPAddressesRealized(o.Status.NetworkInterfaceConfig.IPAddresses, expectedIPCount) {
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
			// If annotation attachment ID is changed in restore mode, we need to update the annotation with the latest subnetport state
			if value, exist := annotations[servicecommon.AnnotationAttachment]; exist {
				if value == *nsxSubnetPort.Attachment.Id {
					return true
				}
			}
		}
	}
	return false
}

func (service *SubnetPortService) CreateOrUpdateSubnetPort(obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string, isVmSubnetPort bool, restoreMode bool, interfaceIPType v1alpha1.IPAddressType) (*model.SegmentPortState, error) {
	var uid string
	var attachmentID string
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		uid = string(o.UID)
		attachmentID = o.Status.Attachment.ID
	case *v1.Pod:
		uid = string(o.UID)
		if value, exist := o.Annotations[servicecommon.AnnotationAttachment]; exist {
			attachmentID = value
		}
	}
	log.Info("Creating or updating subnetport", "nsxSubnetPort.Id", uid, "nsxSubnetPath", *nsxSubnet.Path)
	nsxSubnetPort, err := service.buildSubnetPort(obj, nsxSubnet, contextID, tags, isVmSubnetPort, restoreMode, interfaceIPType)
	if err != nil {
		log.Error(err, "failed to build NSX subnet port", "nsxSubnetPort.Id", uid, "*nsxSubnet.Path", *nsxSubnet.Path, "contextID", contextID)
		return nil, err
	}
	existingSubnetPort := service.SubnetPortStore.GetByKey(*nsxSubnetPort.Id)
	isChanged := true
	if existingSubnetPort != nil {
		// The existing port's attachment ID should not be changed in any case.
		if existingSubnetPort.Attachment != nil {
			nsxSubnetPort.Attachment.Id = existingSubnetPort.Attachment.Id
		}
		nsxSubnetPort.AddressBindings = mergeSubnetPortAddressBinding(existingSubnetPort.AddressBindings, nsxSubnetPort.AddressBindings)
		isChanged = servicecommon.CompareResource(SubnetPortToComparable(existingSubnetPort), SubnetPortToComparable(nsxSubnetPort))
	}
	// In restore mode, restore attachment id in k8s CR when NSX >= 9.2
	if restoreMode && nsx.RestoreVifFeatureEnabled(service.NSXClient, service.NSXConfig) && attachmentID != "" {
		nsxSubnetPort.Attachment.Id = &attachmentID
	}
	subnetInfo, err := servicecommon.ParseVPCResourcePath(*nsxSubnet.Path)
	if err != nil {
		return nil, err
	}
	if !isChanged {
		log.Info("NSX subnet port not changed, skipping the update", "nsxSubnetPort.Id", nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
		if !restoreMode && service.portAlreadyRealized(obj, nsxSubnetPort) {
			log.Debug("The subnet port is already realized, skip checking the state", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
			return nil, nil
		}
	} else {
		log.Info("Updating the NSX subnet port", "existingSubnetPort", existingSubnetPort, "desiredSubnetPort", nsxSubnetPort)
		err = service.NSXClient.PortClient.Patch(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxSubnetPort.Id, *nsxSubnetPort)
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			log.Error(err, "failed to create or update subnet port", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
			return nil, err
		}
		err = service.SubnetPortStore.Apply(nsxSubnetPort)
		if err != nil {
			return nil, err
		}
		if existingSubnetPort != nil {
			log.Info("Updated NSX subnet port", "nsxSubnetPort.Path", *nsxSubnetPort.Path)
		} else {
			log.Info("Created NSX subnet port", "nsxSubnetPort.Path", *nsxSubnetPort.Path)
		}
	}
	nsxSubnetPortState, err := service.CheckSubnetPortState(obj, *nsxSubnet.Path)
	if err != nil {
		if nsxutil.IsRealizeStateError(err) {
			log.Error(err, "check and update NSX subnet port state failed, would retry with delay", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
		} else {
			log.Error(err, "check and update NSX subnet port state failed, would retry exponentially", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
		}
		return nil, err
	}
	createdNSXSubnetPort, err := service.NSXClient.PortClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxSubnetPort.Id)
	if err != nil {
		log.Error(err, "check and update NSX subnet port failed, would retry exponentially", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
		return nil, err
	}
	err = service.SubnetPortStore.Apply(&createdNSXSubnetPort)
	if err != nil {
		return nil, err
	}
	if isChanged {
		log.Info("Successfully created or updated subnetport", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPortState", nsxSubnetPortState)
	} else {
		log.Info("Subnetport already existed", "subnetport", *nsxSubnetPort.Id, "nsxSubnetPortState", nsxSubnetPortState)
	}
	return nsxSubnetPortState, nil
}

func mergeSubnetPortAddressBinding(existingAddressBinding []model.PortAddressBindingEntry, desiredAddressBinding []model.PortAddressBindingEntry) []model.PortAddressBindingEntry {
	// Keep existing bindings when desired is empty (restore mode or BOTH→IP_POOL transition):
	// updating with IP only after MAC was pool-allocated causes NSX realization error
	// "Modifying MAC address bindings of LogicalPort ... is not allowed as they were allocated from MAC Pool."
	if len(existingAddressBinding) > 0 && len(desiredAddressBinding) == 0 {
		return existingAddressBinding
	}
	// Copy missing IP or MAC from existing binding at matching index.
	// NSX enforces binding count immutability for multi-binding ports (count changes are
	// rejected with CIF_LOGICALPORT_CANNOT_MODIFY_ADDRESS_BINDING), so no count reconciliation
	// is needed here — mismatches surface as RealizeStateErrors and retry.
	for i := range desiredAddressBinding {
		if i >= len(existingAddressBinding) {
			break
		}
		if desiredAddressBinding[i].IpAddress == nil && existingAddressBinding[i].IpAddress != nil {
			desiredAddressBinding[i].IpAddress = existingAddressBinding[i].IpAddress
		}
		if desiredAddressBinding[i].MacAddress == nil && existingAddressBinding[i].MacAddress != nil {
			desiredAddressBinding[i].MacAddress = existingAddressBinding[i].MacAddress
		}
	}
	return desiredAddressBinding
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
	if nsxSubnetPort.Path == nil {
		return errors.New("subnet port path is nil")
	}
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

func (service *SubnetPortService) GetPortsOfSubnet(subnetPath string) (ports []*model.VpcSubnetPort) {
	subnetPortList := service.SubnetPortStore.GetByIndex(servicecommon.IndexKeySubnetPath, subnetPath)
	return subnetPortList
}

// isPortStaticAllocated reports whether a realized port's addresses come from a static
// IP pool (AllocateAddresses BOTH/IP_POOL) rather than a DHCP/MAC pool.
func isPortStaticAllocated(port *model.VpcSubnetPort) bool {
	if port.Attachment == nil || port.Attachment.AllocateAddresses == nil {
		return false
	}
	switch *port.Attachment.AllocateAddresses {
	case "BOTH", "IP_POOL":
		return true
	default:
		return false
	}
}

// countNSXAddressBindingsByFamily is the NSX-model equivalent of
// util.CountAddressBindingsByFamily, used for a realized port's AddressBindings.
func countNSXAddressBindingsByFamily(bindings []model.PortAddressBindingEntry) (ipv4Count, ipv6Count int) {
	for _, binding := range bindings {
		if binding.IpAddress == nil || *binding.IpAddress == "" {
			ipv4Count++
			continue
		}
		ip := net.ParseIP(*binding.IpAddress)
		if ip != nil && ip.To4() == nil {
			ipv6Count++
		} else {
			ipv4Count++
		}
	}
	return ipv4Count, ipv6Count
}

// countExistingIPsForPool counts IPs (not ports) consumed from the pool being checked, for
// the given family. Only ports sourced from that pool are counted, so a DHCP-sourced port on
// a mixed-mode Subnet doesn't count against static capacity (or vice versa). A port with
// multiple bindings in the requested family counts for all of them, not just one.
func (service *SubnetPortService) countExistingIPsForPool(subnetPath string, useStaticPool bool, useIPv6 bool) int {
	count := 0
	for _, port := range service.GetPortsOfSubnet(subnetPath) {
		if isPortStaticAllocated(port) != useStaticPool {
			continue
		}
		if len(port.AddressBindings) == 0 {
			// No explicit bindings recorded: assume the legacy single-IP-per-family shape.
			count++
			continue
		}
		ipv4Count, ipv6Count := countNSXAddressBindingsByFamily(port.AddressBindings)
		if useIPv6 {
			count += ipv6Count
		} else {
			count += ipv4Count
		}
	}
	return count
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
	if service.SubnetPortStore == nil {
		return result
	}
	subnetports := service.SubnetPortStore.GetByIndex(servicecommon.TagScopeNamespace, ns)
	for _, subnetport := range subnetports {
		tagname := nsxutil.FindTag(subnetport.Tags, servicecommon.TagScopePodName)
		if tagname == name {
			result = append(result, subnetport)
		}
	}
	return result
}

func (service *SubnetPortService) ResetSubnetTotalIP(path string) {
	obj, ok := service.SubnetPortStore.PortCountInfo.Load(path)
	if !ok {
		log.Info("No SubnetPort count info for the Subnet, no need to reset totalIP", "nsxSubnetPath", path)
		return
	}
	info := obj.(*CountInfo)
	info.lock.Lock()
	defer info.lock.Unlock()
	info.totalStaticIP = 0
	info.totalDhcpIP = 0
}

func (service *SubnetPortService) ListSubnetPortByStsName(ns string, stsName string) []*model.VpcSubnetPort {
	var result []*model.VpcSubnetPort
	if service.SubnetPortStore == nil {
		return result
	}
	subnetports := service.SubnetPortStore.GetByIndex(servicecommon.TagScopeStatefulSetName, stsName)
	for _, subnetport := range subnetports {
		tagname := nsxutil.FindTag(subnetport.Tags, servicecommon.TagScopeNamespace)
		if tagname == ns {
			result = append(result, subnetport)
		}
	}
	return result
}

func (service *SubnetPortService) ListSubnetPortByStsUid(ns string, stsUid string) []*model.VpcSubnetPort {
	var result []*model.VpcSubnetPort
	if service.SubnetPortStore == nil {
		return result
	}
	subnetports := service.SubnetPortStore.GetByIndex(servicecommon.TagScopeStatefulSetUID, stsUid)
	for _, subnetport := range subnetports {
		tagname := nsxutil.FindTag(subnetport.Tags, servicecommon.TagScopeNamespace)
		if tagname == ns {
			result = append(result, subnetport)
		}
	}
	return result
}

// AllocatePortFromSubnet checks capacity for all IPs the SubnetPort requests (addressBindings
// may ask for more than one per family) and reserves them if available. staticIPAllocationType
// decides, per family, whether the IP is drawn from the static pool or the DHCP pool - needed
// for mixed-mode Subnets where both exist at once.
func (service *SubnetPortService) AllocatePortFromSubnet(subnet *model.VpcSubnet, sharedSubnet bool, interfaceIPType v1alpha1.IPAddressType, staticIPAllocationType v1alpha1.StaticIPAllocationType, addressBindings []v1alpha1.PortAddressBinding) (bool, error) {
	subnetInfo, _ := servicecommon.ParseVPCResourcePath(*subnet.Path)

	info := &CountInfo{}
	obj, ok := service.SubnetPortStore.PortCountInfo.LoadOrStore(*subnet.Path, info)
	info = obj.(*CountInfo)

	info.lock.Lock()
	defer info.lock.Unlock()

	ipv4Count, ipv6Count := util.CountAddressBindingsByFamily(addressBindings)
	if util.IPAddressTypeIncludesIPv4(interfaceIPType) && ipv4Count == 0 {
		ipv4Count = 1
	}
	if util.IPAddressTypeIncludesIPv6(interfaceIPType) && ipv6Count == 0 {
		ipv6Count = 1
	}
	useStaticIPv4 := util.StaticIPAllocationTypeIncludesIPv4(staticIPAllocationType)
	useStaticIPv6 := util.StaticIPAllocationTypeIncludesIPv6(staticIPAllocationType)

	// Handle IPv4 capacity check
	if util.IPAddressTypeIncludesIPv4(interfaceIPType) {
		hasCapacity, err := service.checkIPv4Capacity(subnet, sharedSubnet, info, ok, subnetInfo, useStaticIPv4, ipv4Count)
		if !hasCapacity {
			return false, err
		}
	}

	// Handle IPv6 capacity check
	if util.IPAddressTypeIncludesIPv6(interfaceIPType) {
		hasCapacity, err := service.checkIPv6Capacity(subnet, sharedSubnet, info, ok, subnetInfo, useStaticIPv6, ipv6Count)
		if !hasCapacity {
			return false, err
		}
	}

	// Increment counters for successful allocation
	if util.IPAddressTypeIncludesIPv4(interfaceIPType) {
		if useStaticIPv4 {
			info.dirtyStaticCount += ipv4Count
			log.Trace("Allocate Subnetport to IPv4 static pool", "Subnet", *subnet.Path, "ipCount", ipv4Count, "dirtyStaticCount", info.dirtyStaticCount)
		} else {
			info.dirtyDhcpCount += ipv4Count
			log.Trace("Allocate Subnetport to IPv4 dhcp pool", "Subnet", *subnet.Path, "ipCount", ipv4Count, "dirtyDhcpCount", info.dirtyDhcpCount)
		}
	}
	if util.IPAddressTypeIncludesIPv6(interfaceIPType) {
		if useStaticIPv6 {
			info.dirtyStaticCountIPv6 += ipv6Count
			log.Trace("Allocate Subnetport to IPv6 static pool", "Subnet", *subnet.Path, "ipCount", ipv6Count, "dirtyStaticCountIPv6", info.dirtyStaticCountIPv6)
		} else {
			info.dirtyDhcpCountIPv6 += ipv6Count
			log.Trace("Allocate Subnetport to IPv6 dhcp pool", "Subnet", *subnet.Path, "ipCount", ipv6Count, "dirtyDhcpCountIPv6", info.dirtyDhcpCountIPv6)
		}
	}

	return true, nil
}

// checkIPv4Capacity checks for ipCount more IPv4 addresses in the static or DHCP pool,
// per useStaticPool.
func (service *SubnetPortService) checkIPv4Capacity(subnet *model.VpcSubnet, sharedSubnet bool, info *CountInfo, existedEntry bool, subnetInfo servicecommon.VPCResourceInfo, useStaticPool bool, ipCount int) (bool, error) {
	dhcpMode := model.SubnetDhcpConfig_MODE_DEACTIVATED
	if subnet.SubnetDhcpConfig != nil && subnet.SubnetDhcpConfig.Mode != nil {
		dhcpMode = *subnet.SubnetDhcpConfig.Mode
	}

	staticIpAllocationEnabled := false
	if subnet.AdvancedConfig != nil && subnet.AdvancedConfig.StaticIpAllocation != nil && subnet.AdvancedConfig.StaticIpAllocation.Enabled != nil {
		staticIpAllocationEnabled = *subnet.AdvancedConfig.StaticIpAllocation.Enabled
	}
	// For DHCP Deactivated mode Subnet, if staticIpAllocation enable:false, skip check IP count
	// and always return true
	if dhcpMode == model.SubnetDhcpConfig_MODE_DEACTIVATED && !staticIpAllocationEnabled {
		return true, nil
	}

	var allocatedIPNumber int
	if useStaticPool {
		// When SubnetIPReservation is created/deleted, we will reset the info.totalStaticIP and
		// expect the totalStaticIP is updated from NSX ip pool API.
		// For shared Subnet, user can create IPReservation and SubnetPort from NSX side.
		// We call NSX ip pool API to for latest total ip and requested ip.
		if !existedEntry || info.totalStaticIP == 0 || sharedSubnet {
			staticIPPool, err := service.NSXClient.IPPoolClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, "static-ipv4-default")
			if err != nil {
				log.Error(err, "Failed to get Subnet static IP Pool static-ipv4-default", "Subnet", *subnet.Path)
				return false, err
			}
			if staticIPPool.PoolUsage != nil && staticIPPool.PoolUsage.TotalIps != nil {
				info.totalStaticIP = int(*staticIPPool.PoolUsage.TotalIps)
			}
			if sharedSubnet && staticIPPool.PoolUsage != nil && staticIPPool.PoolUsage.RequestedIpAllocations != nil {
				allocatedIPNumber = int(*staticIPPool.PoolUsage.RequestedIpAllocations)
			}
		}
	} else if dhcpMode == model.SubnetDhcpConfig_MODE_SERVER {
		// For DHCP Server mode Subnet, get total IPs from DHCP IP Pool from NSX each time
		// since user might update reservedIPRanges for the subnet and it impacts the DHCP Pool size
		dhcpServerStats, err := service.NSXClient.DhcpServerConfigStatsClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, nil, nil, nil, nil, nil, nil, nil)
		if err != nil {
			log.Error(err, "Failed to get Subnet dhcp-server-config stats", "Subnet", *subnet.Path)
			return false, err
		}
		if len(dhcpServerStats.IpPoolStats) > 0 && dhcpServerStats.IpPoolStats[0].PoolSize != nil {
			info.totalDhcpIP = int(*dhcpServerStats.IpPoolStats[0].PoolSize)
		}
		if sharedSubnet && len(dhcpServerStats.IpPoolStats) > 0 && dhcpServerStats.IpPoolStats[0].AllocatedNumber != nil {
			allocatedIPNumber = int(*dhcpServerStats.IpPoolStats[0].AllocatedNumber)
		}
	} else if !existedEntry && dhcpMode == model.SubnetDhcpConfig_MODE_RELAY {
		// For DHCP Relay mode Subnet, assume 4 reserved IPs
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
		info.totalDhcpIP = totalIP - 4
	}

	if time.Since(info.exhaustedCheckTime) < IPReleaseTime {
		return false, nil
	}

	existingIPCount := service.countExistingIPsForPool(*subnet.Path, useStaticPool, false)
	// A shared Subnet can be used by other supervisors or other places where SubnetPort
	// is created and not in operator cache.
	// For DHCPServer Subnet, the allocated IP number wont change before the VM request IP
	// from the DHCPServer.
	// Thus we use the max number of port record in store and allocated number from API to
	// reduce the possibility to create SubnetPort on a Subnet without available IP
	if sharedSubnet {
		existingIPCount = max(existingIPCount, allocatedIPNumber)
	}

	if useStaticPool {
		return info.dirtyStaticCount+existingIPCount+ipCount <= info.totalStaticIP, nil
	}
	return info.dirtyDhcpCount+existingIPCount+ipCount <= info.totalDhcpIP, nil
}

// checkIPv6Capacity checks for ipCount more IPv6 addresses in the static or DHCP pool,
// per useStaticPool.
func (service *SubnetPortService) checkIPv6Capacity(subnet *model.VpcSubnet, sharedSubnet bool, info *CountInfo, isNewEntry bool, subnetInfo servicecommon.VPCResourceInfo, useStaticPool bool, ipCount int) (bool, error) {
	dhcpv6Mode := model.SubnetDhcpv6Config_MODE_DEACTIVATED
	if subnet.SubnetDhcpv6Config != nil && subnet.SubnetDhcpv6Config.Mode != nil {
		dhcpv6Mode = *subnet.SubnetDhcpv6Config.Mode
	}

	staticIpAllocationEnabled := false
	if subnet.AdvancedConfig != nil && subnet.AdvancedConfig.StaticIpAllocation != nil && subnet.AdvancedConfig.StaticIpAllocation.Enabled != nil {
		staticIpAllocationEnabled = *subnet.AdvancedConfig.StaticIpAllocation.Enabled
	}
	// For DHCP Deactivated mode Subnet, if staticIpAllocation enable:false, skip check IP count
	if dhcpv6Mode == model.SubnetDhcpv6Config_MODE_DEACTIVATED && !staticIpAllocationEnabled {
		return true, nil
	}

	var allocatedIPNumberIPv6 int
	if !useStaticPool {
		// DHCPv6 pool stats need NSX's VpcIpv6 feature; on older versions DhcpIpv6 stays nil.
		if !service.NSXClient.NSXCheckVersion(nsx.IPv6) {
			log.Info("DHCPv6 pool statistics unavailable on this NSX version; allowing allocation", "Subnet", *subnet.Path)
			return true, nil
		}
		dhcpServerStats, err := service.NSXClient.DhcpServerConfigStatsClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, nil, nil, nil, nil, nil, nil, nil)
		if err != nil {
			log.Error(err, "Failed to get Subnet dhcp-server-config stats for IPv6", "Subnet", *subnet.Path)
			return false, err
		}
		if dhcpServerStats.DhcpIpv6 == nil || len(dhcpServerStats.DhcpIpv6.IpPoolStats) == 0 {
			log.Info("DHCPv6 pool statistics not present in response; allowing allocation", "Subnet", *subnet.Path)
			return true, nil
		}
		if dhcpServerStats.DhcpIpv6.IpPoolStats[0].PoolSize != nil {
			info.totalDhcpIPv6 = int(*dhcpServerStats.DhcpIpv6.IpPoolStats[0].PoolSize)
		}
		if sharedSubnet && dhcpServerStats.DhcpIpv6.IpPoolStats[0].AllocatedNumber != nil {
			allocatedIPNumberIPv6 = int(*dhcpServerStats.DhcpIpv6.IpPoolStats[0].AllocatedNumber)
		}

		if time.Since(info.exhaustedCheckTime) < IPReleaseTime {
			return false, nil
		}

		existingIPCount := service.countExistingIPsForPool(*subnet.Path, false, true)
		if sharedSubnet {
			existingIPCount = max(existingIPCount, allocatedIPNumberIPv6)
		}

		return info.dirtyDhcpCountIPv6+existingIPCount+ipCount <= info.totalDhcpIPv6, nil
	}

	if !isNewEntry || info.totalStaticIPv6 == 0 || sharedSubnet {
		staticIPPoolIPv6, err := service.NSXClient.IPPoolClient.Get(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, "static-ipv6-default")
		if err != nil {
			log.Error(err, "Failed to get Subnet static IP Pool static-ipv6-default", "Subnet", *subnet.Path)
			return false, err
		}
		if staticIPPoolIPv6.PoolUsage != nil && staticIPPoolIPv6.PoolUsage.TotalIps != nil {
			info.totalStaticIPv6 = int(*staticIPPoolIPv6.PoolUsage.TotalIps)
		}
		if sharedSubnet && staticIPPoolIPv6.PoolUsage != nil && staticIPPoolIPv6.PoolUsage.RequestedIpAllocations != nil {
			allocatedIPNumberIPv6 = int(*staticIPPoolIPv6.PoolUsage.RequestedIpAllocations)
		}
	}

	if time.Since(info.exhaustedCheckTime) < IPReleaseTime {
		return false, nil
	}

	existingIPCount := service.countExistingIPsForPool(*subnet.Path, true, true)
	if sharedSubnet {
		existingIPCount = max(existingIPCount, allocatedIPNumberIPv6)
	}

	return info.dirtyStaticCountIPv6+existingIPCount+ipCount <= info.totalStaticIPv6, nil
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

// ReleasePortInSubnet undoes AllocatePortFromSubnet's reservation. Params must match the
// original Allocate call so the same pool counters are decremented.
func (service *SubnetPortService) ReleasePortInSubnet(path string, interfaceIPType v1alpha1.IPAddressType, staticIPAllocationType v1alpha1.StaticIPAllocationType, addressBindings []v1alpha1.PortAddressBinding) {
	obj, ok := service.SubnetPortStore.PortCountInfo.Load(path)
	if !ok {
		log.Error(nil, "Subnet does not have Subnetport to remove", "Subnet", path)
		return
	}
	info := obj.(*CountInfo)
	info.lock.Lock()
	defer info.lock.Unlock()

	ipv4Count, ipv6Count := util.CountAddressBindingsByFamily(addressBindings)
	if util.IPAddressTypeIncludesIPv4(interfaceIPType) && ipv4Count == 0 {
		ipv4Count = 1
	}
	if util.IPAddressTypeIncludesIPv6(interfaceIPType) && ipv6Count == 0 {
		ipv6Count = 1
	}

	if util.IPAddressTypeIncludesIPv4(interfaceIPType) {
		if util.StaticIPAllocationTypeIncludesIPv4(staticIPAllocationType) {
			if info.dirtyStaticCount < ipv4Count {
				log.Error(nil, "Subnet does not have IPv4 static IP to remove for SubnetPort", "Subnet", path)
			} else {
				info.dirtyStaticCount -= ipv4Count
				log.Trace("Release Subnetport from Subnet", "Subnet", path, "dirtyStaticCount", info.dirtyStaticCount)
			}
		} else {
			if info.dirtyDhcpCount < ipv4Count {
				log.Error(nil, "Subnet does not have IPv4 dhcp IP to remove for SubnetPort", "Subnet", path)
			} else {
				info.dirtyDhcpCount -= ipv4Count
				log.Trace("Release Subnetport from Subnet", "Subnet", path, "dirtyDhcpCount", info.dirtyDhcpCount)
			}
		}
	}

	if util.IPAddressTypeIncludesIPv6(interfaceIPType) {
		if util.StaticIPAllocationTypeIncludesIPv6(staticIPAllocationType) {
			if info.dirtyStaticCountIPv6 < ipv6Count {
				log.Error(nil, "Subnet does not have IPv6 static IP to remove for SubnetPort", "Subnet", path)
			} else {
				info.dirtyStaticCountIPv6 -= ipv6Count
				log.Trace("Release Subnetport from Subnet", "Subnet", path, "dirtyStaticCountIPv6", info.dirtyStaticCountIPv6)
			}
		} else {
			if info.dirtyDhcpCountIPv6 < ipv6Count {
				log.Error(nil, "Subnet does not have IPv6 dhcp IP to remove for SubnetPort", "Subnet", path)
			} else {
				info.dirtyDhcpCountIPv6 -= ipv6Count
				log.Trace("Release Subnetport from Subnet", "Subnet", path, "dirtyDhcpCountIPv6", info.dirtyDhcpCountIPv6)
			}
		}
	}
}

// IsEmptySubnet check if there is any SubnetPort created or being creating on the Subnet.
func (service *SubnetPortService) IsEmptySubnet(path string) bool {
	portCount := len(service.GetPortsOfSubnet(path))
	obj, ok := service.SubnetPortStore.PortCountInfo.Load(path)
	if ok {
		info := obj.(*CountInfo)
		portCount += info.dirtyStaticCount + info.dirtyDhcpCount + info.dirtyStaticCountIPv6 + info.dirtyDhcpCountIPv6
	}
	return portCount < 1
}

func (service *SubnetPortService) DeletePortCount(path string) {
	log.Debug("Subnet is deleted from SubnetPort count record", "path", path)
	service.SubnetPortStore.PortCountInfo.Delete(path)
}

func (service *SubnetPortService) GetAllVIFs() (*VifStore, error) {
	vifStore := NewVifStore()
	pageSize := int64(1000)
	var allVIFs []mpmodel.VirtualNetworkInterface
	cursor := ""
	for {
		vifsPage, err := service.NSXClient.VifsClient.List(&cursor, nil, nil, nil, nil, &pageSize, nil, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to list VIFs: %w", err)
		}
		allVIFs = append(allVIFs, vifsPage.Results...)
		if vifsPage.Cursor == nil || *vifsPage.Cursor == "" {
			break
		}
		cursor = *vifsPage.Cursor
	}
	for _, vif := range allVIFs {
		vifStore.Add(&vif)
	}
	log.Info("Initialized VIF store", "count", len(allVIFs))
	return &vifStore, nil
}

// GetDefaultInterfaceIPType returns the SubnetPort default interfaceIPType
// when interfaceIPType is not set,
// the default value is IPv4 if parent Subnet/SubnetSet IPAddressType is IPv4 or IPv4IPv6;
// the default value is IPv6 if parent Subnet/SubnetSet IPAddressType is IPv6.
func GetDefaultInterfaceIPType(interfaceIPType v1alpha1.IPAddressType, parentIPAddressType v1alpha1.IPAddressType) v1alpha1.IPAddressType {
	if interfaceIPType != "" {
		return interfaceIPType
	}
	if parentIPAddressType == v1alpha1.IPAddressTypeIPv6 {
		return v1alpha1.IPAddressTypeIPv6
	}
	return v1alpha1.IPAddressTypeIPv4
}
