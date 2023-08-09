/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"errors"
	"strings"

	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
)

var (
	log                    = logger.Log
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
				servicecommon.IndexKeySubnetID:        subnetPortIndexBySubnetID,
			}),
		BindingType: model.SegmentPortBindingType(),
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

func (service *SubnetPortService) CreateOrUpdateSubnetPort(obj *v1alpha1.SubnetPort, nsxSubnetPath string) error {

	log.Info("Creating or updating subnetport", "subnetport", obj)
	nsxSubnetPort, err := service.buildSubnetPort(obj, nsxSubnetPath)
	if err != nil {
		log.Error(err, "failed to build NSX subnet port")
		return err
	}

	subnetInfo, err := servicecommon.ParseVPCResourcePath(nsxSubnetPath)
	if err != nil {
		log.Error(err, "failed to parse NSX subnet path", "path", nsxSubnetPath)
	}
	existingSubnetPort := service.SubnetPortStore.GetByKey(*nsxSubnetPort.Id)
	isChanged := servicecommon.CompareResource(SubnetPortToComparable(existingSubnetPort), SubnetPortToComparable(nsxSubnetPort))
	if !isChanged {
		log.Info("NSX subnet port not changed, skipping the update", "nsxSubnetPort.Id", nsxSubnetPort.Id)
		// We don't need to update it but still need to check realized state.
	} else {
		err = service.NSXClient.PortClient.Patch(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID, *nsxSubnetPort.Id, *nsxSubnetPort)
		if err != nil {
			log.Error(err, "failed to create or update subnet port")
			return err
		}
		err = service.SubnetPortStore.Operate(nsxSubnetPort)
		if err != nil {
			return err
		}
		if existingSubnetPort != nil {
			log.Info("updated NSX subnet port", "subnetPort", nsxSubnetPort)
		} else {
			log.Info("created NSX subnet port", "subnetPort", nsxSubnetPort)
		}
	}
	if err := service.CheckSubnetPortState(obj, nsxSubnetPath); err != nil {
		log.Error(err, "check and update NSX subnet port state failed, would retry exponentially", "subnetport", obj.UID)
		return err
	}
	log.Info("successfully created or updated subnetport", "subnetport", obj.UID)
	return nil
}

// CheckSubnetPortState will check the port realized status then get the port state to prepare the CR status.
func (service *SubnetPortService) CheckSubnetPortState(obj *v1alpha1.SubnetPort, nsxSubnetPath string) error {
	nsxSubnetPort := service.SubnetPortStore.GetByKey(string(obj.UID))
	if nsxSubnetPort == nil {
		return errors.New("failed to get subnet port from store")
	}
	realizeService := realizestate.InitializeRealizeState(service.Service)
	if err := realizeService.CheckRealizeState(retry.DefaultRetry, *nsxSubnetPort.Path, "RealizedLogicalPort"); err != nil {
		log.Error(err, "failed to get realized status", "subnetport path", *nsxSubnetPort.Path)
		if realizestate.IsRealizeStateError(err) {
			log.Error(err, "the created subnet port is in error realization state, cleaning the resource", "subnetport", obj.UID)
			// only recreate subnet port on RealizationErrorStateError.
			if err := service.DeleteSubnetPort(obj.UID); err != nil {
				log.Error(err, "cleanup error subnetport failed", "subnetport", obj.UID)
				return err
			}
		}
		return err
	}
	// TODO: avoid to get subnetport state again if we already got it.
	nsxPortState, err := service.GetSubnetPortState(obj, nsxSubnetPath)
	if err != nil {
		return err
	}
	log.Info("Got the NSX subnet port state", "nsxPortState.RealizedBindings", nsxPortState.RealizedBindings)

	ipAddress := v1alpha1.SubnetPortIPAddress{
		IP: *nsxPortState.RealizedBindings[0].Binding.IpAddress,
	}
	obj.Status.IPAddresses = []v1alpha1.SubnetPortIPAddress{ipAddress}
	obj.Status.MACAddress = strings.Trim(*nsxPortState.RealizedBindings[0].Binding.MacAddress, "\"")
	obj.Status.VIFID = *nsxPortState.Attachment.Id
	return nil
}

func (service *SubnetPortService) GetSubnetPortState(obj *v1alpha1.SubnetPort, nsxSubnetPath string) (*model.SegmentPortState, error) {
	nsxOrgID, nsxProjectID, nsxVPCID, nsxSubnetID := nsxutil.ParseVPCPath(nsxSubnetPath)
	nsxSubnetPortState, err := service.NSXClient.PortStateClient.Get(nsxOrgID, nsxProjectID, nsxVPCID, nsxSubnetID, string(obj.UID), nil, nil)
	if err != nil {
		log.Error(err, "failed to get subnet port state", "nsxSubnetPortID", obj.UID)
		return nil, err
	}
	return &nsxSubnetPortState, nil
}

func (service *SubnetPortService) DeleteSubnetPort(uid types.UID) error {
	nsxSubnetPort := service.SubnetPortStore.GetByKey(string(uid))
	if nsxSubnetPort.Id == nil {
		log.Info("NSX subnet port is not found in store, skip deleting it", "subnetPortCRUID", uid)
		return nil
	}
	nsxOrgID, nsxProjectID, nsxVPCID, nsxSubnetID := nsxutil.ParseVPCPath(*nsxSubnetPort.Path)
	err := service.NSXClient.PortClient.Delete(nsxOrgID, nsxProjectID, nsxVPCID, nsxSubnetID, string(uid))
	if err != nil {
		log.Error(err, "failed to delete subnetport", "nsxSubnetPortID", uid)
		return err
	}
	if err = service.SubnetPortStore.Delete(uid); err != nil {
		return err
	}
	log.Info("successfully deleted nsxSubnetPort", "nsxSubnetPortID", uid)
	return nil
}

func (service *SubnetPortService) ListNSXSubnetPortIDForCR() sets.String {
	log.V(2).Info("listing subnet port CR UIDs")
	subnetPortSet := service.SubnetPortStore.ListIndexFuncValues(servicecommon.TagScopeSubnetPortCRUID)
	return subnetPortSet
}

func (service *SubnetPortService) GetGatewayNetmaskForSubnetPort(obj *v1alpha1.SubnetPort, nsxSubnetPath string) (string, string, error) {
	// TODO: merge the logic to subnet service when subnet implementation is done.
	subnetInfo, err := servicecommon.ParseVPCResourcePath(nsxSubnetPath)
	if err != nil {
		return "", "", err
	}
	// TODO: if the port is not the first on the same subnet, try to get the info from existing realized subnetport CR to avoid query NSX API again.
	statusList, err := service.NSXClient.SubnetStatusClient.List(subnetInfo.OrgID, subnetInfo.ProjectID, subnetInfo.VPCID, subnetInfo.ID)
	if err != nil {
		log.Error(err, "failed to get subnet status")
		return "", "", err
	}
	if len(statusList.Results) == 0 {
		err := errors.New("empty status result")
		log.Error(err, "no subnet status found")
		return "", "", err
	}
	status := statusList.Results[0]
	gateway, err := util.RemoveIPPrefix(*status.GatewayAddress)
	if err != nil {
		return "", "", err
	}
	prefix, err := util.GetIPPrefix(*status.GatewayAddress)
	if err != nil {
		return "", "", err
	}
	mask, err := util.GetSubnetMask(prefix)
	if err != nil {
		return "", "", err
	}
	return gateway, mask, nil
}
