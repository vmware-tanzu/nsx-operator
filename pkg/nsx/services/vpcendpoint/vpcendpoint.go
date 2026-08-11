/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcendpoint

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

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

type VPCEndpointService struct {
	common.Service
	VPCEndpointStore    *VPCEndpointStore
	VPCService          common.VPCServiceProvider
	IPAllocationService common.IPAddressAllocationServiceProvider
}

// InitializeVPCEndpoint loads existing NSX state into the store.
func InitializeVPCEndpoint(service common.Service, vpcService common.VPCServiceProvider, ipAllocationService common.IPAddressAllocationServiceProvider) (*VPCEndpointService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	vpcEndpointService := &VPCEndpointService{Service: service, VPCService: vpcService, IPAllocationService: ipAllocationService}
	vpcEndpointService.VPCEndpointStore = buildVPCEndpointStore()

	wg.Add(1)
	go vpcEndpointService.InitializeResourceStore(&wg, fatalErrors, common.ResourceTypeVpcEndpoint,
		[]model.Tag{{Scope: String(common.TagScopeCluster), Tag: String(service.NSXClient.NsxConfig.Cluster)}},
		vpcEndpointService.VPCEndpointStore)

	go func() {
		wg.Wait()
		close(wgDone)
	}()
	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		return vpcEndpointService, err
	}
	return vpcEndpointService, nil
}

// resolveIPAllocationPath resolves ipAllocationName to its NSX path.
func (service *VPCEndpointService) resolveIPAllocationPath(ctx context.Context, namespace, ipAllocCRName string) (string, error) {
	ipAllocCR := &v1alpha1.IPAddressAllocation{}
	if err := service.Client.Get(ctx, k8sclient.ObjectKey{Namespace: namespace, Name: ipAllocCRName}, ipAllocCR); err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("IPAddressAllocation CR %s/%s not found", namespace, ipAllocCRName)
		}
		return "", fmt.Errorf("failed to get IPAddressAllocation CR %s/%s: %w", namespace, ipAllocCRName, err)
	}

	nsxAllocation, err := service.IPAllocationService.GetIPAddressAllocationByOwner(ipAllocCR)
	if err != nil {
		return "", fmt.Errorf("failed to look up NSX allocation for IPAddressAllocation CR %s/%s: %w", namespace, ipAllocCRName, err)
	}
	if nsxAllocation == nil {
		return "", fmt.Errorf("NSX allocation for IPAddressAllocation CR %s/%s not found in store", namespace, ipAllocCRName)
	}
	if nsxAllocation.Path == nil || *nsxAllocation.Path == "" {
		return "", fmt.Errorf("NSX allocation for IPAddressAllocation CR %s/%s has no policy path", namespace, ipAllocCRName)
	}
	return *nsxAllocation.Path, nil
}

// resolveServiceEndpointPath parses the CCI name into an NSX path; not a k8s lookup.
func (service *VPCEndpointService) resolveServiceEndpointPath(serviceEndpointName string) (string, error) {
	return common.GetVpcServiceEndpointPathFromCCIName(serviceEndpointName)
}

// CreateOrUpdateVPCEndpoint resolves both references and applies the CR to NSX if it changed.
func (service *VPCEndpointService) CreateOrUpdateVPCEndpoint(ctx context.Context, namespace string, obj *v1alpha1.VPCEndpoint) error {
	ipAllocationPath, err := service.resolveIPAllocationPath(ctx, namespace, obj.Spec.IPAllocationName)
	if err != nil {
		return err
	}
	serviceEndpointPath, err := service.resolveServiceEndpointPath(obj.Spec.ServiceEndpointName)
	if err != nil {
		return err
	}

	nsxVPCEndpoint, err := service.BuildVPCEndpoint(obj, ipAllocationPath, serviceEndpointPath)
	if err != nil {
		return err
	}

	existingVPCEndpoint, err := service.indexedVPCEndpoint(obj.UID)
	if err != nil {
		log.Error(err, "Failed to get vpcendpoint", "UID", obj.UID)
		return err
	}
	if existingVPCEndpoint != nil {
		// Keep the existing NSX id and display_name.
		nsxVPCEndpoint.Id = String(*existingVPCEndpoint.Id)
		nsxVPCEndpoint.DisplayName = String(*existingVPCEndpoint.DisplayName)
		if !common.CompareResource(VPCEndpointToComparable(existingVPCEndpoint), VPCEndpointToComparable(nsxVPCEndpoint)) {
			log.Info("VPCEndpoint is not changed", "UID", obj.UID)
			return nil
		}
	}

	return service.Apply(namespace, nsxVPCEndpoint)
}

// Apply patches NSX and waits for realization.
func (service *VPCEndpointService) Apply(namespace string, nsxVPCEndpoint *model.VpcEndpoint) error {
	vpcInfo := service.VPCService.ListVPCInfo(namespace)
	if len(vpcInfo) == 0 {
		return fmt.Errorf("no VPC found for VPCEndpoint in namespace %s", namespace)
	}
	orgID, projectID, vpcID := vpcInfo[0].OrgID, vpcInfo[0].ProjectID, vpcInfo[0].ID

	err := service.NSXClient.VpcEndpointClient.Patch(orgID, projectID, vpcID, *nsxVPCEndpoint.Id, *nsxVPCEndpoint)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return err
	}

	nsxVPCEndpointNew, err := service.NSXClient.VpcEndpointClient.Get(orgID, projectID, vpcID, *nsxVPCEndpoint.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return err
	}

	if err := service.checkVPCEndpointRealizeState(orgID, projectID, vpcID, *nsxVPCEndpoint.Id); err != nil {
		return err
	}

	return service.VPCEndpointStore.Apply(&nsxVPCEndpointNew)
}

// checkVPCEndpointRealizeState polls until NSX reports SUCCESS or ERROR.
func (service *VPCEndpointService) checkVPCEndpointRealizeState(orgID, projectID, vpcID, id string) error {
	return retry.OnError(util.NSXTRealizeRetry, func(err error) bool {
		return errors.Is(err, errNotRealized)
	}, func() error {
		status, err := service.NSXClient.VpcEndpointStatusClient.Get(orgID, projectID, vpcID, id)
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			return err
		}
		if status.Status == nil {
			return errNotRealized
		}
		switch *status.Status {
		case model.VpcEndpointStatus_STATUS_SUCCESS:
			return nil
		case model.VpcEndpointStatus_STATUS_ERROR:
			return fmt.Errorf("VpcEndpoint %s realization failed", id)
		default:
			// keep polling
			return errNotRealized
		}
	})
}

func (service *VPCEndpointService) DeleteVPCEndpointByNSXResource(nsxVPCEndpoint *model.VpcEndpoint) error {
	vpcResourceInfo, err := common.ParseVPCResourcePath(*nsxVPCEndpoint.Path)
	if err != nil {
		return err
	}
	err = service.NSXClient.VpcEndpointClient.Delete(vpcResourceInfo.OrgID, vpcResourceInfo.ProjectID, vpcResourceInfo.VPCID, *nsxVPCEndpoint.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		return err
	}
	nsxVPCEndpoint.MarkedForDelete = &MarkedForDelete
	return service.VPCEndpointStore.Apply(nsxVPCEndpoint)
}

func (service *VPCEndpointService) DeleteVPCEndpoint(obj interface{}) error {
	var err error
	var nsxVPCEndpoint *model.VpcEndpoint
	switch o := obj.(type) {
	case *v1alpha1.VPCEndpoint:
		nsxVPCEndpoint, err = service.indexedVPCEndpoint(o.UID)
	case types.UID:
		nsxVPCEndpoint, err = service.indexedVPCEndpoint(o)
	case *model.VpcEndpoint:
		nsxVPCEndpoint = o
	}
	if err != nil {
		log.Error(err, "Failed to get vpcendpoint for deletion", "obj", obj)
		return err
	}
	if nsxVPCEndpoint == nil {
		log.Info("VPCEndpoint not found in store, skip deletion", "obj", obj)
		return nil
	}
	return service.DeleteVPCEndpointByNSXResource(nsxVPCEndpoint)
}

func (service *VPCEndpointService) DeleteVPCEndpointByNamespacedName(namespace, name string) error {
	objs := service.VPCEndpointStore.List()
	for _, obj := range objs {
		vpcEndpoint, ok := obj.(*model.VpcEndpoint)
		if !ok {
			continue
		}
		namespaceMatch, nameMatch := false, false
		for _, tag := range vpcEndpoint.Tags {
			if *tag.Scope == common.TagScopeNamespace && *tag.Tag == namespace {
				namespaceMatch = true
			}
			if *tag.Scope == common.TagScopeVPCEndpointCRName && *tag.Tag == name {
				nameMatch = true
			}
		}
		if namespaceMatch && nameMatch {
			if err := service.DeleteVPCEndpoint(vpcEndpoint); err != nil {
				log.Error(err, "Failed to delete VPCEndpoint", "Namespace", namespace, "Name", name)
				return err
			}
		}
	}
	return nil
}

func (service *VPCEndpointService) ListVPCEndpointID() sets.Set[string] {
	return service.VPCEndpointStore.ListIndexFuncValues(common.TagScopeVPCEndpointCRUID)
}

func (service *VPCEndpointService) vpcEndpointIdExists(id string) bool {
	return service.VPCEndpointStore.GetByKey(id) != nil
}
