/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package serviceendpoint

import (
	"errors"
	"fmt"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log             = logger.Log
	String          = common.String
	MarkedForDelete = true

	// errNotRealized means keep polling.
	errNotRealized = errors.New("not realized yet")
)

type ServiceEndpointService struct {
	common.Service
	ServiceEndpointStore *ServiceEndpointStore
	VPCService           common.VPCServiceProvider
}

// InitializeServiceEndpoint loads existing NSX state into the store.
func InitializeServiceEndpoint(service common.Service, vpcService common.VPCServiceProvider) (*ServiceEndpointService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	serviceEndpointService := &ServiceEndpointService{Service: service, VPCService: vpcService}
	serviceEndpointService.ServiceEndpointStore = buildServiceEndpointStore()

	wg.Add(1)
	go serviceEndpointService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeVpcServiceEndpoint,
		[]model.Tag{{Scope: String(common.TagScopeCluster), Tag: String(service.NSXClient.NsxConfig.Cluster)}},
		serviceEndpointService.ServiceEndpointStore)

	go func() {
		wg.Wait()
		close(wgDone)
	}()
	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		return serviceEndpointService, err
	}
	return serviceEndpointService, nil
}

// CreateOrUpdateServiceEndpoint applies the CR to NSX if it changed.
func (service *ServiceEndpointService) CreateOrUpdateServiceEndpoint(obj *v1alpha1.ServiceEndpoint) error {
	nsxServiceEndpoint, err := service.BuildServiceEndpoint(obj)
	if err != nil {
		return err
	}

	existingServiceEndpoint, err := service.indexedServiceEndpoint(obj.UID)
	if err != nil {
		log.Error(err, "Failed to get serviceendpoint", "UID", obj.UID)
		return err
	}
	if existingServiceEndpoint != nil {
		// Keep the existing NSX id and display_name.
		nsxServiceEndpoint.Id = String(*existingServiceEndpoint.Id)
		nsxServiceEndpoint.DisplayName = String(*existingServiceEndpoint.DisplayName)
		if !common.CompareResource(ServiceEndpointToComparable(existingServiceEndpoint), ServiceEndpointToComparable(nsxServiceEndpoint)) {
			log.Info("ServiceEndpoint is not changed", "UID", obj.UID)
			return nil
		}
	}

	return service.Apply(nsxServiceEndpoint)
}

// Apply patches NSX and waits for realization.
func (service *ServiceEndpointService) Apply(nsxServiceEndpoint *model.VpcServiceEndpoint) error {
	ns := service.GetServiceEndpointNamespace(nsxServiceEndpoint)
	vpcInfo := service.VPCService.ListVPCInfo(ns)
	if len(vpcInfo) == 0 {
		return fmt.Errorf("no VPC found for ServiceEndpoint in namespace %s", ns)
	}
	orgID, projectID, vpcID := vpcInfo[0].OrgID, vpcInfo[0].ProjectID, vpcInfo[0].ID

	err := service.NSXClient.VpcServiceEndpointClient.Patch(orgID, projectID, vpcID, *nsxServiceEndpoint.Id, *nsxServiceEndpoint)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return err
	}

	nsxServiceEndpointNew, err := service.NSXClient.VpcServiceEndpointClient.Get(orgID, projectID, vpcID, *nsxServiceEndpoint.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return err
	}

	if err := service.checkServiceEndpointRealizeState(orgID, projectID, vpcID, *nsxServiceEndpoint.Id); err != nil {
		return err
	}

	return service.ServiceEndpointStore.Apply(&nsxServiceEndpointNew)
}

// checkServiceEndpointRealizeState polls until NSX reports SUCCESS or ERROR.
// TODO: confirm with NSX whether this status API is fully implemented; it hasn't
// reliably returned SUCCESS in testing.
func (service *ServiceEndpointService) checkServiceEndpointRealizeState(orgID, projectID, vpcID, id string) error {
	return retry.OnError(util.NSXTRealizeRetry, func(err error) bool {
		return errors.Is(err, errNotRealized)
	}, func() error {
		status, err := service.NSXClient.VpcServiceEndpointStatusClient.Get(orgID, projectID, vpcID, id)
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			return err
		}
		if status.Status == nil {
			return errNotRealized
		}
		switch *status.Status {
		case model.VpcServiceEndpointStatus_STATUS_SUCCESS:
			return nil
		case model.VpcServiceEndpointStatus_STATUS_ERROR:
			return fmt.Errorf("VpcServiceEndpoint %s realization failed", id)
		default:
			// keep polling
			return errNotRealized
		}
	})
}

func (service *ServiceEndpointService) DeleteServiceEndpointByNSXResource(nsxServiceEndpoint *model.VpcServiceEndpoint) error {
	vpcResourceInfo, err := common.ParseVPCResourcePath(*nsxServiceEndpoint.Path)
	if err != nil {
		return err
	}
	err = service.NSXClient.VpcServiceEndpointClient.Delete(vpcResourceInfo.OrgID, vpcResourceInfo.ProjectID, vpcResourceInfo.VPCID, *nsxServiceEndpoint.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return err
	}
	nsxServiceEndpoint.MarkedForDelete = &MarkedForDelete
	return service.ServiceEndpointStore.Apply(nsxServiceEndpoint)
}

func (service *ServiceEndpointService) DeleteServiceEndpoint(obj interface{}) error {
	var err error
	var nsxServiceEndpoint *model.VpcServiceEndpoint
	switch o := obj.(type) {
	case *v1alpha1.ServiceEndpoint:
		nsxServiceEndpoint, err = service.indexedServiceEndpoint(o.UID)
	case types.UID:
		nsxServiceEndpoint, err = service.indexedServiceEndpoint(o)
	case *model.VpcServiceEndpoint:
		nsxServiceEndpoint = o
	}
	if err != nil {
		log.Error(err, "Failed to get serviceendpoint for deletion", "obj", obj)
		return err
	}
	if nsxServiceEndpoint == nil {
		log.Info("ServiceEndpoint not found in store, skip deletion", "obj", obj)
		return nil
	}
	return service.DeleteServiceEndpointByNSXResource(nsxServiceEndpoint)
}

func (service *ServiceEndpointService) DeleteServiceEndpointByNamespacedName(namespace, name string) error {
	objs := service.ServiceEndpointStore.List()
	for _, obj := range objs {
		serviceEndpoint, ok := obj.(*model.VpcServiceEndpoint)
		if !ok {
			continue
		}
		namespaceMatch, nameMatch := false, false
		for _, tag := range serviceEndpoint.Tags {
			if *tag.Scope == common.TagScopeNamespace && *tag.Tag == namespace {
				namespaceMatch = true
			}
			if *tag.Scope == common.TagScopeServiceEndpointCRName && *tag.Tag == name {
				nameMatch = true
			}
		}
		if namespaceMatch && nameMatch {
			if err := service.DeleteServiceEndpoint(serviceEndpoint); err != nil {
				log.Error(err, "Failed to delete ServiceEndpoint", "Namespace", namespace, "Name", name)
				return err
			}
		}
	}
	return nil
}

func (service *ServiceEndpointService) ListServiceEndpointID() sets.Set[string] {
	return service.ServiceEndpointStore.ListIndexFuncValues(common.TagScopeServiceEndpointCRUID)
}

func (service *ServiceEndpointService) GetServiceEndpointNamespace(nsxServiceEndpoint *model.VpcServiceEndpoint) string {
	for _, tag := range nsxServiceEndpoint.Tags {
		if *tag.Scope == common.TagScopeNamespace {
			return *tag.Tag
		}
	}
	return ""
}

func (service *ServiceEndpointService) serviceEndpointIdExists(id string) bool {
	return service.ServiceEndpointStore.GetByKey(id) != nil
}
