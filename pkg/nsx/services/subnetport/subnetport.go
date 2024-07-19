/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                    = &logger.Log
	ResourceTypeSubnetPort = servicecommon.ResourceTypeSubnetPort
	MarkedForDelete        = true
)

type SubnetPortService struct {
	servicecommon.Service
	SubnetPortStore *SubnetPortStore
}

// InitializeSubnetPort sync NSX resources.
func InitializeSubnetPort(service servicecommon.Service) (*SubnetPortService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(1)

	subnetPortService := &SubnetPortService{Service: service}

	subnetPortService.SubnetPortStore = &SubnetPortStore{ResourceStore: servicecommon.ResourceStore{
		Indexer: cache.NewIndexer(
			keyFunc,
			cache.Indexers{
				servicecommon.TagScopeSubnetPortCRUID: subnetPortIndexByCRUID,
				servicecommon.TagScopePodUID:          subnetPortIndexByPodUID,
				servicecommon.IndexKeySubnetID:        subnetPortIndexBySubnetID,
			}),
		BindingType: model.VpcSubnetPortBindingType(),
	}}

	go subnetPortService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeSubnetPort, nil, subnetPortService.SubnetPortStore)

	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return subnetPortService, err
	}

	return subnetPortService, nil
}

func (service *SubnetPortService) CreateOrUpdateSubnetPort(obj interface{}, nsxSubnet *model.VpcSubnet, contextID string, tags *map[string]string) (*model.SegmentPortState, error) {
	var uid string
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		uid = string(o.UID)
	case *v1.Pod:
		uid = string(o.UID)
	}
	log.Info("creating or updating subnetport", "nsxSubnetPort.Id", uid, "nsxSubnetPath", *nsxSubnet.Path)
	nsxSubnetPort, err := service.buildSubnetPort(obj, nsxSubnet, contextID, tags)
	if err != nil {
		log.Error(err, "failed to build NSX subnet port", "nsxSubnetPort.Id", uid, "*nsxSubnet.Path", *nsxSubnet.Path, "contextID", contextID)
		return nil, err
	}
	existingSubnetPort := service.SubnetPortStore.GetByKey(*nsxSubnetPort.Id)
	isChanged := true
	if existingSubnetPort != nil {
		isChanged = servicecommon.CompareResource(SubnetPortToComparable(existingSubnetPort), SubnetPortToComparable(nsxSubnetPort))
	}
	if !isChanged {
		log.Info("NSX subnet port not changed, skipping the update", "nsxSubnetPort.Id", nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
		// We don't need to update it but still need to check realized state.
	} else {
		log.Info("updating the NSX subnet port", "existingSubnetPort", existingSubnetPort, "desiredSubnetPort", nsxSubnetPort)
		subnetInfo, err := servicecommon.ParseVPCResourcePath(*nsxSubnet.Path)
		if err != nil {
			return nil, err
		}
		err = service.NSXClient.PortClient.Patch(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxSubnetPort.Id, *nsxSubnetPort)
		err = nsxutil.NSXApiError(err)
		if err != nil {
			log.Error(err, "failed to create or update subnet port", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
			return nil, err
		}
		err = service.SubnetPortStore.Apply(nsxSubnetPort)
		if err != nil {
			return nil, err
		}
		if existingSubnetPort != nil {
			log.Info("updated NSX subnet port", "nsxSubnetPort.Path", *nsxSubnetPort.Path)
		} else {
			log.Info("created NSX subnet port", "nsxSubnetPort.Path", *nsxSubnetPort.Path)
		}
	}
	enableDHCP := false
	if (*nsxSubnet).DhcpConfig != nil && *nsxSubnet.DhcpConfig.EnableDhcp {
		enableDHCP = true
	}
	nsxSubnetPortState, err := service.CheckSubnetPortState(obj, *nsxSubnet.Path, enableDHCP)
	if err != nil {
		log.Error(err, "check and update NSX subnet port state failed, would retry exponentially", "nsxSubnetPort.Id", *nsxSubnetPort.Id, "nsxSubnetPath", *nsxSubnet.Path)
		return nil, err
	}
	if isChanged {
		log.Info("successfully created or updated subnetport", "nsxSubnetPort.Id", *nsxSubnetPort.Id)
	} else {
		log.Info("subnetport already existed", "subnetport", *nsxSubnetPort.Id)
	}
	return nsxSubnetPortState, nil
}

// CheckSubnetPortState will check the port realized status then get the port state to prepare the CR status.
func (service *SubnetPortService) CheckSubnetPortState(obj interface{}, nsxSubnetPath string, enableDHCP bool) (*model.SegmentPortState, error) {
	var objMeta metav1.ObjectMeta
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		objMeta = o.ObjectMeta
	case *v1.Pod:
		objMeta = o.ObjectMeta
	}
	portID := util.GenerateIDByObject(&objMeta)
	nsxSubnetPort := service.SubnetPortStore.GetByKey(portID)
	if nsxSubnetPort == nil {
		return nil, errors.New("failed to get subnet port from store")
	}
	realizeService := realizestate.InitializeRealizeState(service.Service)
	backoff := wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   2.0,
		Jitter:   0,
		Steps:    6,
	}
	if err := realizeService.CheckRealizeState(backoff, *nsxSubnetPort.Path, "RealizedLogicalPort"); err != nil {
		log.Error(err, "failed to get realized status", "subnetport path", *nsxSubnetPort.Path)
		if realizestate.IsRealizeStateError(err) {
			log.Error(err, "the created subnet port is in error realization state, cleaning the resource", "subnetport", portID)
			// only recreate subnet port on RealizationErrorStateError.
			if err := service.DeleteSubnetPort(portID); err != nil {
				log.Error(err, "cleanup error subnetport failed", "subnetport", portID)
				return nil, err
			}
		}
		return nil, err
	}
	// TODO: avoid to get subnetport state again if we already got it.
	nsxPortState, err := service.GetSubnetPortState(obj, nsxSubnetPath)
	if err != nil {
		return nil, err
	}
	log.Info("got the NSX subnet port state", "nsxPortState.RealizedBindings", nsxPortState.RealizedBindings, "uid", portID)
	if len(nsxPortState.RealizedBindings) == 0 && !enableDHCP {
		return nsxPortState, errors.New("empty realized bindings")
	}
	return nsxPortState, nil
}

func (service *SubnetPortService) GetSubnetPortState(obj interface{}, nsxSubnetPath string) (*model.SegmentPortState, error) {
	var uid types.UID
	switch o := obj.(type) {
	case *v1alpha1.SubnetPort:
		uid = o.UID
	case *v1.Pod:
		uid = o.UID
	}
	nsxOrgID, nsxProjectID, nsxVPCID, nsxSubnetID := nsxutil.ParseVPCPath(nsxSubnetPath)
	nsxSubnetPortState, err := service.NSXClient.PortStateClient.Get(nsxOrgID, nsxProjectID, nsxVPCID, nsxSubnetID, string(uid), nil, nil)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to get subnet port state", "nsxSubnetPortID", uid, "nsxSubnetPath", nsxSubnetPath)
		return nil, err
	}
	return &nsxSubnetPortState, nil
}

func (service *SubnetPortService) DeleteSubnetPort(portID string) error {
	nsxSubnetPort := service.SubnetPortStore.GetByKey(portID)
	if nsxSubnetPort == nil || nsxSubnetPort.Id == nil {
		log.Info("NSX subnet port is not found in store, skip deleting it", "id", portID)
		return nil
	}
	nsxOrgID, nsxProjectID, nsxVPCID, nsxSubnetID := nsxutil.ParseVPCPath(*nsxSubnetPort.Path)
	err := service.NSXClient.PortClient.Delete(nsxOrgID, nsxProjectID, nsxVPCID, nsxSubnetID, portID)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to delete subnetport", "nsxSubnetPort.Path", *nsxSubnetPort.Path)
		return err
	}
	if err = service.SubnetPortStore.Delete(portID); err != nil {
		return err
	}
	log.Info("successfully deleted nsxSubnetPort", "nsxSubnetPortID", portID)
	return nil
}

func (service *SubnetPortService) ListNSXSubnetPortIDForCR() sets.Set[string] {
	log.V(2).Info("listing subnet port CR UIDs")
	subnetPortSet := sets.New[string]()
	for _, subnetPortCRUid := range service.SubnetPortStore.ListIndexFuncValues(servicecommon.TagScopeSubnetPortCRUID).UnsortedList() {
		subnetPortIDs, _ := service.SubnetPortStore.IndexKeys(servicecommon.TagScopeSubnetPortCRUID, subnetPortCRUid)
		subnetPortSet.Insert(subnetPortIDs...)
	}
	return subnetPortSet
}

func (service *SubnetPortService) ListNSXSubnetPortIDForPod() sets.Set[string] {
	log.V(2).Info("listing pod UIDs")
	subnetPortSet := sets.New[string]()
	for _, podUID := range service.SubnetPortStore.ListIndexFuncValues(servicecommon.TagScopePodUID).UnsortedList() {
		subnetPortIDs, _ := service.SubnetPortStore.IndexKeys(servicecommon.TagScopePodUID, podUID)
		subnetPortSet.Insert(subnetPortIDs...)
	}
	return subnetPortSet
}

// TODO: merge the logic to subnet service when subnet implementation is done.
func (service *SubnetPortService) GetGatewayPrefixForSubnetPort(obj *v1alpha1.SubnetPort, nsxSubnetPath string) (string, int, error) {
	subnetInfo, err := servicecommon.ParseVPCResourcePath(nsxSubnetPath)
	if err != nil {
		return "", -1, err
	}
	// TODO: if the port is not the first on the same subnet, try to get the info from existing realized subnetport CR to avoid query NSX API again.
	statusList, err := service.NSXClient.SubnetStatusClient.List(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID)
	err = nsxutil.NSXApiError(err)
	if err != nil {
		log.Error(err, "failed to get subnet status")
		return "", -1, err
	}
	if len(statusList.Results) == 0 {
		err := errors.New("empty status result")
		log.Error(err, "no subnet status found")
		return "", -1, err
	}
	status := statusList.Results[0]
	if status.GatewayAddress == nil {
		err := fmt.Errorf("invalid status result: %+v", status)
		log.Error(err, "subnet status does not have gateway address", "nsxSubnetPath", nsxSubnetPath)
		return "", -1, err
	}
	gateway, err := util.RemoveIPPrefix(*status.GatewayAddress)
	if err != nil {
		return "", -1, err
	}
	prefix, err := util.GetIPPrefix(*status.GatewayAddress)
	if err != nil {
		return "", -1, err
	}
	return gateway, prefix, nil
}

func (service *SubnetPortService) GetSubnetPathForSubnetPortFromStore(nsxSubnetPortID string) string {
	existingSubnetPort := service.SubnetPortStore.GetByKey(nsxSubnetPortID)
	if existingSubnetPort == nil {
		log.Info("subnetport is not found in store", "nsxSubnetPortID", nsxSubnetPortID)
		return ""
	}
	if existingSubnetPort.ParentPath == nil {
		return ""
	}
	return *existingSubnetPort.ParentPath
}

func (service *SubnetPortService) GetPortsOfSubnet(nsxSubnetID string) (ports []*model.VpcSubnetPort) {
	subnetPortList := service.SubnetPortStore.GetByIndex(servicecommon.IndexKeySubnetID, nsxSubnetID)
	return subnetPortList
}

func (service *SubnetPortService) Cleanup(ctx context.Context) error {
	subnetPorts := service.SubnetPortStore.List()
	log.Info("cleanup subnetports", "count", len(subnetPorts))
	for _, subnetPort := range subnetPorts {
		subnetPortID := *subnetPort.(*model.VpcSubnetPort).Id
		select {
		case <-ctx.Done():
			return errors.Join(nsxutil.TimeoutFailed, ctx.Err())
		default:
			err := service.DeleteSubnetPort(subnetPortID)
			if err != nil {
				log.Error(err, "cleanup subnetport failed", "subnetPortID", subnetPortID)
				return err
			}

		}
	}
	return nil
}
