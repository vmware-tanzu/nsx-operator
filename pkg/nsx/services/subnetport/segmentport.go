/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gofrs/uuid"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/realizestate"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// SegmentPort Comparable Adapter
type SegmentPort model.SegmentPort

func (sp *SegmentPort) Key() string {
	return *sp.Id
}

func (sp *SegmentPort) Value() data.DataValue {
	s := &SegmentPort{
		Id:          sp.Id,
		DisplayName: sp.DisplayName,
		Tags:        sp.Tags,
		Attachment:  sp.Attachment,
	}
	if sp.Attachment != nil {
		s.Attachment = &model.PortAttachment{
			AppId:      sp.Attachment.AppId,
			ContextId:  sp.Attachment.ContextId,
			Id:         sp.Attachment.Id,
			TrafficTag: sp.Attachment.TrafficTag,
			Type_:      sp.Attachment.Type_,
		}
	}
	dataValue, _ := ComparableToSegmentPort(s).GetDataValue__()
	return dataValue
}

func SegmentPortToComparable(sp *model.SegmentPort) common.Comparable {
	return (*SegmentPort)(sp)
}

func ComparableToSegmentPort(sp common.Comparable) *model.SegmentPort {
	return (*model.SegmentPort)(sp.(*SegmentPort))
}

// Indexing functions for SegmentPortStore
func segmentPortIndexByCRUID(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.SegmentPort:
		return filterTag(o.Tags, common.TagScopeSubnetPortCRUID), nil
	default:
		return nil, errors.New("segmentPortIndexByCRUID doesn't support unknown type")
	}
}

func segmentPortIndexByPodUID(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.SegmentPort:
		return filterTag(o.Tags, common.TagScopePodUID), nil
	default:
		return nil, errors.New("segmentPortIndexByPodUID doesn't support unknown type")
	}
}

func segmentPortIndexBySegmentPath(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.SegmentPort:
		if o.ParentPath == nil {
			return nil, errors.New("ParentPath is empty")
		}
		return []string{*o.ParentPath}, nil
	default:
		return nil, errors.New("segmentPortIndexBySegmentPath doesn't support unknown type")
	}
}

// SegmentPortStore manages model.SegmentPort resources in memory
type SegmentPortStore struct {
	common.ResourceStore
	PortCountInfo sync.Map
}

func setupSegmentPortStore() *SegmentPortStore {
	return &SegmentPortStore{
		ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc,
				cache.Indexers{
					common.TagScopeSubnetPortCRUID: segmentPortIndexByCRUID,
					common.TagScopePodUID:          segmentPortIndexByPodUID,
					common.IndexKeySegmentPath:     segmentPortIndexBySegmentPath,
				}),
			BindingType: model.SegmentPortBindingType(),
		},
	}
}

func (store *SegmentPortStore) GetSegmentPortByUID(uid types.UID) (*model.SegmentPort, error) {
	if store == nil {
		return nil, nil
	}
	var indexResults []interface{}
	for _, index := range []string{common.TagScopeSubnetPortCRUID, common.TagScopePodUID} {
		indexResult, err := store.ByIndex(index, string(uid))
		if err != nil {
			log.Error(err, "Failed to get SegmentPort", "index", index, "UID", string(uid))
			return nil, err
		}
		indexResults = append(indexResults, indexResult...)
	}

	if len(indexResults) > 0 {
		return indexResults[0].(*model.SegmentPort), nil
	}
	log.Info("Did not get SegmentPort with index", "UID", string(uid))
	return nil, nil
}

func (store *SegmentPortStore) GetByKey(key string) *model.SegmentPort {
	if store == nil {
		return nil
	}
	var segmentPort *model.SegmentPort
	obj := store.ResourceStore.GetByKey(key)
	if obj != nil {
		segmentPort = obj.(*model.SegmentPort)
	}
	return segmentPort
}

func (store *SegmentPortStore) GetByIndex(key string, value string) []*model.SegmentPort {
	if store == nil {
		return nil
	}
	segmentPorts := make([]*model.SegmentPort, 0)
	objs := store.ResourceStore.GetByIndex(key, value)
	for _, obj := range objs {
		segmentPorts = append(segmentPorts, obj.(*model.SegmentPort))
	}
	return segmentPorts
}

func (store *SegmentPortStore) DeleteMultipleObjects(ports []*model.SegmentPort) {
	for _, port := range ports {
		store.Delete(port)
	}
}

func (store *SegmentPortStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	segmentPort := i.(*model.SegmentPort)
	if segmentPort.MarkedForDelete != nil && *segmentPort.MarkedForDelete {
		err := store.Delete(segmentPort)
		log.Debug("delete SegmentPort from store", "segmentPort", segmentPort)
		if err != nil {
			return err
		}
	} else {
		err := store.Add(segmentPort)
		log.Debug("add SegmentPort to store", "segmentPort", segmentPort)
		if err != nil {
			return err
		}
	}
	return nil
}

// Build SegmentPort for Infra Segment
func (service *SubnetPortService) buildSegmentPort(
	subnetPort *v1alpha1.SubnetPort,
	segmentPath string,
	contextID string,
	labelTags *map[string]string,
	isVmSubnetPort bool,
	restoreMode bool,
) (*model.SegmentPort, error) {
	if segmentPath == "" {
		return nil, fmt.Errorf("segmentPath is empty")
	}

	objNamespace := subnetPort.Namespace
	var nsxCIFID uuid.UUID
	var err error
	attachmentID := subnetPort.Status.Attachment.ID
	if restoreMode && attachmentID != "" {
		if nsxCIFID, err = uuid.FromString(attachmentID); err != nil {
			log.Warn("Failed to parse attachment ID in restore mode, generating new UUID", "attachmentID", attachmentID, "error", err)
			nsxCIFID, err = uuid.NewGenWithOptions(uuid.WithRandomReader(bytes.NewReader([]byte(string(subnetPort.UID))))).NewV4()
		}
	} else {
		nsxCIFID, err = uuid.NewGenWithOptions(uuid.WithRandomReader(bytes.NewReader([]byte(string(subnetPort.UID))))).NewV4()
	}
	if err != nil {
		return nil, err
	}

	namespace := &corev1.Namespace{}
	namespacedName := types.NamespacedName{
		Name: objNamespace,
	}
	if err := service.Client.Get(context.Background(), namespacedName, namespace); err != nil {
		return nil, err
	}
	namespaceUid := namespace.UID

	segmentPortID, segmentPortName := service.BuildSegmentPortIdAndName(&subnetPort.ObjectMeta, namespaceUid)
	segmentPortPath := fmt.Sprintf("%s/ports/%s", segmentPath, segmentPortID)

	tags := util.BuildBasicTags(getCluster(service), subnetPort, namespaceUid)

	var tagsFiltered []model.Tag
	for _, tag := range tags {
		if isVmSubnetPort && *tag.Scope == common.TagScopeNamespaceUID {
			continue
		}
		if isVmSubnetPort && *tag.Scope == common.TagScopeNamespace {
			continue
		}
		if !isVmSubnetPort && *tag.Scope == common.TagScopeVMNamespaceUID {
			continue
		}
		if !isVmSubnetPort && *tag.Scope == common.TagScopeVMNamespace {
			continue
		}
		tagsFiltered = append(tagsFiltered, tag)
	}

	if labelTags != nil {
		labelKeys := make([]string, 0, len(*labelTags))
		for k := range *labelTags {
			labelKeys = append(labelKeys, k)
		}
		sort.Strings(labelKeys)
		for _, k := range labelKeys {
			tagsFiltered = append(tagsFiltered, model.Tag{Scope: common.String(k), Tag: common.String((*labelTags)[k])})
		}
	}

	nsxSegmentPort := &model.SegmentPort{
		DisplayName: String(segmentPortName),
		Id:          String(segmentPortID),
		Attachment: &model.PortAttachment{
			Id:         String(nsxCIFID.String()),
			TrafficTag: common.Int64(0),
			Type_:      String(model.PortAttachment_TYPE_INDEPENDENT),
		},
		Tags:       tagsFiltered,
		Path:       &segmentPortPath,
		ParentPath: &segmentPath,
	}
	return nsxSegmentPort, nil
}

func (service *SubnetPortService) BuildSegmentPortIdAndName(obj *metav1.ObjectMeta, namespaceUID types.UID) (string, string) {
	existingSegmentPort, err := service.SegmentPortStore.GetSegmentPortByUID(obj.GetUID())
	if err == nil && existingSegmentPort != nil {
		return *existingSegmentPort.Id, *existingSegmentPort.DisplayName
	}

	objWithNamespaceUID := &metav1.ObjectMeta{
		Name: obj.Name,
		UID:  namespaceUID,
	}
	return common.BuildUniqueIDWithRandomUUID(objWithNamespaceUID, util.GenerateIDByObject, func(id string) bool {
		return service.SegmentPortStore.GetByKey(id) != nil
	}), service.BuildSubnetPortName(obj)
}

// CreateOrUpdateSegmentPort creates or updates SegmentPort on NSX
func (service *SubnetPortService) CreateOrUpdateSegmentPort(
	subnetPort *v1alpha1.SubnetPort,
	segmentPath string,
	contextID string,
	tags *map[string]string,
	isVmSubnetPort bool,
	restoreMode bool,
) (*model.SegmentPortState, error) {
	if len(segmentPath) == 0 {
		return nil, fmt.Errorf("segmentPath is invalid or empty")
	}
	parts := strings.Split(strings.Trim(segmentPath, "/"), "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid segmentPath: %s", segmentPath)
	}
	segmentID := parts[len(parts)-1]

	uid := string(subnetPort.UID)
	attachmentID := subnetPort.Status.Attachment.ID
	log.Info("Creating or updating segmentport", "segmentPort.UID", uid, "segmentPath", segmentPath)

	nsxSegmentPort, err := service.buildSegmentPort(subnetPort, segmentPath, contextID, tags, isVmSubnetPort, restoreMode)
	if err != nil {
		log.Error(err, "failed to build NSX segment port", "segmentPort.UID", uid, "segmentPath", segmentPath, "contextID", contextID)
		return nil, err
	}

	existingSegmentPort := service.SegmentPortStore.GetByKey(*nsxSegmentPort.Id)
	isChanged := true
	if existingSegmentPort != nil {
		if existingSegmentPort.Attachment != nil {
			nsxSegmentPort.Attachment.Id = existingSegmentPort.Attachment.Id
		}
		isChanged = common.CompareResource(SegmentPortToComparable(existingSegmentPort), SegmentPortToComparable(nsxSegmentPort))
	}

	if restoreMode && nsx.RestoreVifFeatureEnabled(service.NSXClient, service.NSXConfig) && attachmentID != "" {
		nsxSegmentPort.Attachment.Id = &attachmentID
	}

	if !isChanged {
		log.Info("NSX segment port not changed, skipping the update", "nsxSegmentPort.Id", *nsxSegmentPort.Id, "segmentPath", segmentPath)
	} else {
		log.Info("Updating the NSX segment port", "existingSegmentPort", existingSegmentPort, "desiredSegmentPort", nsxSegmentPort)
		err = service.NSXClient.SegmentPortsClient.Patch(segmentID, *nsxSegmentPort.Id, *nsxSegmentPort)
		err = nsxutil.TransNSXApiError(err)
		if err != nil {
			log.Error(err, "failed to create or update segment port", "nsxSegmentPort.Id", *nsxSegmentPort.Id, "segmentPath", segmentPath)
			return nil, err
		}
		err = service.SegmentPortStore.Apply(nsxSegmentPort)
		if err != nil {
			return nil, err
		}
		if existingSegmentPort != nil {
			log.Info("Updated NSX segment port", "nsxSegmentPort.Path", *nsxSegmentPort.Path)
		} else {
			log.Info("Created NSX segment port", "nsxSegmentPort.Path", *nsxSegmentPort.Path)
		}
	}

	nsxSegmentPortState, err := service.CheckSegmentPortState(subnetPort, segmentPath)
	if err != nil {
		if nsxutil.IsRealizeStateError(err) {
			log.Error(err, "check and update NSX segment port state failed, would retry with delay", "nsxSegmentPort.Id", *nsxSegmentPort.Id, "segmentPath", segmentPath)
		} else {
			log.Error(err, "check and update NSX segment port state failed, would retry exponentially", "nsxSegmentPort.Id", *nsxSegmentPort.Id, "segmentPath", segmentPath)
		}
		return nil, err
	}

	createdNSXSegmentPort, err := service.NSXClient.SegmentPortsClient.Get(segmentID, *nsxSegmentPort.Id)
	if err != nil {
		log.Error(err, "check and update NSX segment port failed, would retry exponentially", "nsxSegmentPort.Id", *nsxSegmentPort.Id, "segmentPath", segmentPath)
		return nil, err
	}

	err = service.SegmentPortStore.Apply(&createdNSXSegmentPort)
	if err != nil {
		return nil, err
	}

	if isChanged {
		log.Info("Successfully created or updated segmentport", "nsxSegmentPort.Id", *nsxSegmentPort.Id, "nsxSegmentPortState", nsxSegmentPortState)
	} else {
		log.Info("Segmentport already existed", "segmentport", *nsxSegmentPort.Id, "nsxSegmentPortState", nsxSegmentPortState)
	}

	return nsxSegmentPortState, nil
}

func (service *SubnetPortService) CheckSegmentPortState(subnetPort *v1alpha1.SubnetPort, segmentPath string) (*model.SegmentPortState, error) {
	nsxSegmentPort, err := service.SegmentPortStore.GetSegmentPortByUID(subnetPort.UID)
	if err != nil {
		return nil, err
	}
	if nsxSegmentPort == nil {
		return nil, errors.New("failed to get segment port from store")
	}

	parts := strings.Split(strings.Trim(segmentPath, "/"), "/")
	segmentID := parts[len(parts)-1]
	portID := *nsxSegmentPort.Id
	realizeService := realizestate.InitializeRealizeState(service.Service)

	if err := realizeService.CheckRealizeState(util.NSXTRealizeRetry, *nsxSegmentPort.Path, []string{}); err != nil {
		log.Error(err, "Failed to get realized status", "nsxSegmentPortPath", *nsxSegmentPort.Path)
		if nsxutil.IsRealizeStateError(err) {
			log.Error(err, "The created SegmentPort is in error realization state, cleaning the resource", "SegmentPort", portID)
			if err := service.DeleteSegmentPortById(segmentID, portID); err != nil {
				log.Error(err, "Cleanup error SegmentPort failed", "SegmentPort", portID)
				return nil, err
			}
		}
		return nil, err
	}

	nsxPortState, err := service.GetSegmentPortState(segmentID, portID)
	if err != nil {
		return nil, err
	}
	log.Info("Got the NSX segment port state", "nsxPortState.RealizedBindings", nsxPortState.RealizedBindings, "uid", portID)
	return nsxPortState, nil
}

func (service *SubnetPortService) GetSegmentPortState(segmentID string, portID string) (*model.SegmentPortState, error) {
	nsxSegmentPortState, err := service.NSXClient.SegmentPortStateClient.Get(segmentID, portID, nil, nil)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "failed to get segment port state", "segmentID", segmentID, "portID", portID)
		return nil, err
	}
	return &nsxSegmentPortState, nil
}

func (service *SubnetPortService) DeleteSegmentPort(nsxSegmentPort *model.SegmentPort, segmentPath string) error {
	if nsxSegmentPort.Path == nil {
		return errors.New("segment port path is nil")
	}
	parts := strings.Split(strings.Trim(segmentPath, "/"), "/")
	segmentID := parts[len(parts)-1]
	err := service.NSXClient.SegmentPortsClient.Delete(segmentID, *nsxSegmentPort.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		log.Error(err, "failed to delete nsxSegmentPort", "nsxSegmentPort.Path", *nsxSegmentPort.Path)
		return err
	}
	if err = service.SegmentPortStore.Delete(*nsxSegmentPort.Id); err != nil {
		return err
	}
	log.Info("Successfully deleted nsxSegmentPort", "nsxSegmentPortID", *nsxSegmentPort.Id)
	return nil
}

func (service *SubnetPortService) DeleteSegmentPortById(segmentID string, portID string) error {
	nsxSegmentPort := service.SegmentPortStore.GetByKey(portID)
	if nsxSegmentPort == nil || nsxSegmentPort.Id == nil {
		log.Info("Segment port not found in store", "portID", portID)
		return nil
	}
	if len(segmentID) == 0 && nsxSegmentPort.ParentPath != nil {
		parts := strings.Split(strings.Trim(*nsxSegmentPort.ParentPath, "/"), "/")
		segmentID = parts[len(parts)-1]
	}
	err := service.NSXClient.SegmentPortsClient.Delete(segmentID, *nsxSegmentPort.Id)
	err = nsxutil.TransNSXApiError(err)
	if err != nil {
		portPath := ""
		if nsxSegmentPort.Path != nil {
			portPath = *nsxSegmentPort.Path
		}
		log.Error(err, "failed to delete nsxSegmentPort", "nsxSegmentPort.Path", portPath)
		return err
	}
	if err = service.SegmentPortStore.Delete(*nsxSegmentPort.Id); err != nil {
		return err
	}
	log.Info("Successfully deleted nsxSegmentPort", "nsxSegmentPortID", *nsxSegmentPort.Id)
	return nil
}
