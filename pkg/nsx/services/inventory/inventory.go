package inventory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"

	"github.com/vmware/go-vmware-nsxt/common"
	"github.com/vmware/go-vmware-nsxt/containerinventory"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

type InventoryType string

const (
	// Inventory object types
	ContainerCluster             InventoryType = "ContainerCluster"
	ContainerClusterNode         InventoryType = "ContainerClusterNode"
	ContainerProject             InventoryType = "ContainerProject"
	ContainerApplication         InventoryType = "ContainerApplication"
	ContainerApplicationInstance InventoryType = "ContainerApplicationInstance"
	ContainerNetworkPolicy       InventoryType = "ContainerNetworkPolicy"
	ContainerEgress              InventoryType = "ContainerEgress"
	ContainerIPPool              InventoryType = "ContainerIpPool"
)

type InventoryKey struct {
	InventoryType InventoryType
	ExternalId    string
	Key           string
}

const (
	operation_create = "CREATE"
	operation_update = "UPDATE"
	operation_delete = "DELETE"
	operation_none   = "NONE"

	INVENTORY_STATUS_UP      = "UP"
	INVENTORY_STATUS_DOWN    = "DOWN"
	INVENTORY_STATUS_UNKNOWN = "UNKNOWN"
)

var (
	log = &logger.Log
)

type InventoryService struct {
	commonservice.Service
	applicationInstanceStore *ApplicationInstanceStore

	requestBuffer  []containerinventory.ContainerInventoryObject
	pending_add    map[string]interface{}
	pending_delete map[string]interface{}

	stalePods map[string]interface{}
}

func InitializeService(service commonservice.Service) (*InventoryService, error) {
	inventoryservice := &InventoryService{
		requestBuffer:  make([]containerinventory.ContainerInventoryObject, 0),
		pending_add:    make(map[string]interface{}),
		pending_delete: make(map[string]interface{}),
		stalePods:      make(map[string]interface{}),
	}

	// TODO, Inventory store should have its own store
	inventoryservice.applicationInstanceStore = &ApplicationInstanceStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{commonservice.TagScopeNCPPod: indexFunc}),
	}}
	inventoryservice.Service = service
	return inventoryservice, nil
}

func (s *InventoryService) AddPod(pod *corev1.Pod) (retry bool) {
	log.Info("Add pod ", "Pod", pod.Name, "namespace", pod.Namespace)
	retry = false
	// Calculate the services related to this Pod from pending_add or inventory store.
	container_application_ids := []string{}
	if s.pending_add[string(pod.UID)] != nil {
		containerApplicationInstance := s.pending_add[string(pod.UID)].(containerinventory.ContainerApplicationInstance)
		container_application_ids = containerApplicationInstance.ContainerApplicationIds
	}
	preContainerApplicationInstance := s.applicationInstanceStore.GetByKey(string(pod.UID))
	if preContainerApplicationInstance != nil && (len(container_application_ids) == 0) {
		container_application_ids = preContainerApplicationInstance.(containerinventory.ContainerApplicationInstance).ContainerApplicationIds
	}
	namespaceName := pod.Namespace
	namespace := &corev1.Namespace{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: namespaceName}, namespace)
	if err != nil {
		retry = true
		log.Error(errors.New("Cannot find namespace for Pod"), "Cannot find namespace for Pod", pod)
		return
	}

	node := &corev1.Node{}
	err = s.Client.Get(context.TODO(), types.NamespacedName{Name: pod.Spec.NodeName}, node)
	if err != nil {
		if pod.Spec.NodeName != "" {
			// retry when pod has Node but Node is missing in NodeInformer
			retry = true
		}
		log.Error(err, "Cannot find node for Pod", "pod", pod, "retry", retry)
		return
	}
	status := INVENTORY_STATUS_DOWN
	if pod.Status.Phase == v1.PodRunning {
		status = INVENTORY_STATUS_UP
	} else if pod.Status.Phase == v1.PodUnknown {
		status = INVENTORY_STATUS_UNKNOWN
	}

	ips := ""
	if len(pod.Status.PodIPs) == 1 {
		ips = pod.Status.PodIPs[0].IP
	} else if len(pod.Status.PodIPs) == 2 {
		ips = pod.Status.PodIPs[0].IP + "," + pod.Status.PodIPs[1].IP
	} else {
		log.Info("Unexpected pod IPs found", "pod ips", pod.Status.PodIPs)
	}
	var originProperties []common.KeyValuePair
	if ips == "" {
		originProperties = nil
	} else {
		originProperties = []common.KeyValuePair{
			{
				Key:   "ip",
				Value: ips,
			},
		}
	}

	containerApplicationInstance := containerinventory.ContainerApplicationInstance{
		DisplayName:  pod.Name,
		ResourceType: string(ContainerApplicationInstance),
		// TODO: get tags from pod.Labels
		Tags:                    []common.Tag{},
		ClusterNodeId:           string(node.UID),
		ContainerApplicationIds: container_application_ids,
		// TODO: get cluster id fron config
		ContainerClusterId: "",
		// TODO: get namespace uid
		ContainerProjectId: string(""),
		ExternalId:         string(pod.UID),
		NetworkErrors:      nil,
		NetworkStatus:      "",
		OriginProperties:   originProperties,
		Status:             status,
	}

	operation, _ := s.compareAndMergeUpdate(preContainerApplicationInstance, containerApplicationInstance)
	if operation != operation_none {
		s.pending_add[containerApplicationInstance.ExternalId] = containerApplicationInstance
	}
	return
}

func (s *InventoryService) compareAndMergeUpdate(pre interface{}, cur interface{}) (string, map[string]interface{}) {
	updateProperties := make(map[string]interface{})
	updateProperties["external_id"] = reflect.ValueOf(cur).FieldByName("ExternalId").String()
	updateProperties["resource_type"] = reflect.ValueOf(cur).FieldByName("ResourceType").String()

	switch updateProperties["resource_type"] {

	case string(ContainerProject):
		if pre == nil {
			updateProperties["display_name"] = cur.(containerinventory.ContainerProject).DisplayName
			updateProperties["container_cluster_id"] = cur.(containerinventory.ContainerProject).ContainerClusterId
		}
		if pre == nil || !reflect.DeepEqual(pre.(containerinventory.ContainerProject).Tags, cur.(containerinventory.ContainerProject).Tags) {
			updateProperties["tags"] = cur.(containerinventory.ContainerProject).Tags
		}

	case string(ContainerApplicationInstance):
		if pre == nil {
			updateProperties["display_name"] = cur.(containerinventory.ContainerApplicationInstance).DisplayName
			updateProperties["container_cluster_id"] = cur.(containerinventory.ContainerApplicationInstance).ContainerClusterId
			updateProperties["container_project_id"] = cur.(containerinventory.ContainerApplicationInstance).ContainerProjectId
		}
		if pre == nil || !reflect.DeepEqual(pre.(containerinventory.ContainerApplicationInstance).Tags, cur.(containerinventory.ContainerApplicationInstance).Tags) {
			updateProperties["tags"] = cur.(containerinventory.ContainerApplicationInstance).Tags
		}
		if pre == nil || pre.(containerinventory.ContainerApplicationInstance).ClusterNodeId != cur.(containerinventory.ContainerApplicationInstance).ClusterNodeId {
			updateProperties["cluster_node_id"] = cur.(containerinventory.ContainerApplicationInstance).ClusterNodeId
		}
		if pre == nil || !reflect.DeepEqual(pre.(containerinventory.ContainerApplicationInstance).ContainerApplicationIds, cur.(containerinventory.ContainerApplicationInstance).ContainerApplicationIds) {
			updateProperties["container_application_ids"] = cur.(containerinventory.ContainerApplicationInstance).ContainerApplicationIds
		}
		if pre == nil || pre.(containerinventory.ContainerApplicationInstance).Status != cur.(containerinventory.ContainerApplicationInstance).Status {
			updateProperties["status"] = cur.(containerinventory.ContainerApplicationInstance).Status
		}
		if pre == nil && len(cur.(containerinventory.ContainerApplicationInstance).OriginProperties) == 1 {
			updateProperties["origin_properties"] = cur.(containerinventory.ContainerApplicationInstance).OriginProperties
		} else if pre != nil && !reflect.DeepEqual(pre.(containerinventory.ContainerApplicationInstance).OriginProperties, cur.(containerinventory.ContainerApplicationInstance).OriginProperties) {
			if s.isIPChanged(pre.(containerinventory.ContainerApplicationInstance), cur.(containerinventory.ContainerApplicationInstance)) {
				updateProperties["origin_properties"] = cur.(containerinventory.ContainerApplicationInstance).OriginProperties
			}
		}
	}
	if pre == nil {
		s.requestBuffer = append(s.requestBuffer, containerinventory.ContainerInventoryObject{ContainerObject: updateProperties, ObjectUpdateType: operation_create})
		return operation_create, updateProperties
	} else if len(updateProperties) > 2 {
		s.requestBuffer = append(s.requestBuffer, containerinventory.ContainerInventoryObject{ContainerObject: updateProperties, ObjectUpdateType: operation_update})
		return operation_update, updateProperties
	} else {
		return operation_none, nil
	}

}
func (s *InventoryService) isIPChanged(pre containerinventory.ContainerApplicationInstance, cur containerinventory.ContainerApplicationInstance) bool {
	preIPs := ""
	curIPs := ""
	for _, prop := range pre.OriginProperties {
		if prop.Key == "ip" {
			preIPs = prop.Value
			log.Info("Pod previously has IP ", "pod", pre.DisplayName, "ips", preIPs)
		}
	}
	for _, prop := range cur.OriginProperties {
		if prop.Key == "ip" {
			curIPs = prop.Value
			log.Info("Pod now has IP", "pod", cur.DisplayName, "ips", curIPs)
		}
	}
	preIPArr := strings.Split(preIPs, ",")
	curIPArr := strings.Split(curIPs, ",")
	if len(preIPArr) != len(curIPArr) {
		return true
	} else if len(curIPArr) == 1 && preIPArr[0] != curIPArr[0] {
		return true
	} else if len(curIPArr) == 2 {
		if !((preIPArr[0] == curIPArr[0] && preIPArr[1] == curIPArr[1]) || (preIPArr[0] == curIPArr[1] && preIPArr[1] == curIPArr[0])) {
			return true
		}
	}
	return false
}

type empty struct{}
type KeySet map[InventoryKey]empty

func (s KeySet) Has(item InventoryKey) bool {
	_, exists := s[item]
	return exists
}

func (s KeySet) Insert(item InventoryKey) {
	s[item] = empty{}
}

func (s KeySet) Delete(item InventoryKey) {
	delete(s, item)
}

func (s *InventoryService) SyncInventoryObject(bufferedKeys KeySet) (KeySet, error) {
	retryKeys := KeySet{}
	startTime := time.Now()
	defer func() {
		log.Info("Finished syncing inventory object since ", "time", time.Since(startTime))
	}()
	for key := range bufferedKeys {
		log.V(1).Info("Syncing inventory object", "object key", key)
		externalId := key.ExternalId
		namespace, name, err := cache.SplitMetaNamespaceKey(key.Key)
		if err != nil {
			log.Error(err, "Failed to split meta namespace key", "key", key)
			continue
		}
		switch key.InventoryType {

		case ContainerApplicationInstance:
			pod := &corev1.Pod{}
			err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, pod)
			if apierrors.IsNotFound(err) ||
				((err == nil) && (string(pod.UID) != externalId)) {
				s.DeleteResource(externalId, ContainerApplicationInstance)
			} else if err == nil {
				retry := s.AddPod(pod)
				if retry {
					retryKeys.Insert(key)
				}
			} else {
				log.Error(err, "Unexpected error is found while processing pod")
			}

		}
	}

	err := s.sendNSXRequestAndUpdateInventoryStore()
	if err != nil {
		return bufferedKeys, err
	}

	return retryKeys, err
}

func (s *InventoryService) DeleteResource(external_id string, resource_type InventoryType) error {
	log.V(1).Info("Delete inventory resource", "resource_type", resource_type, "external_id", external_id)
	var inventoryObject interface{} = nil
	exists := false
	switch resource_type {

	case ContainerApplicationInstance:
		inventoryObject = s.applicationInstanceStore.GetByKey(external_id)
		if inventoryObject != nil {
			exists = true
		}
	default:
		return fmt.Errorf("unknown resource_type : %v for external_id %s", resource_type, external_id)
	}

	if exists {
		deletedInfo := make(map[string]interface{})
		deletedInfo["resource_type"] = resource_type
		deletedInfo["external_id"] = external_id
		s.requestBuffer = append(s.requestBuffer, containerinventory.ContainerInventoryObject{ContainerObject: deletedInfo, ObjectUpdateType: operation_delete})
		s.pending_delete[external_id] = inventoryObject

		// Update Pods which used to be connected to this removed service.
		if resource_type == ContainerApplication {
			namespaceId := inventoryObject.(containerinventory.ContainerApplication).ContainerProjectId
			if namespaceId != "" {
				/*
					project, exists, _ := s.projectStore.GetByKey(namespaceId)
					if exists {
						s.removeServiceIdForPods(external_id, namespaceId, project.(containerinventory.ContainerProject).DisplayName, []string{})
					}
				*/
			} else {
				return fmt.Errorf("cannot update Pods for removed service id : %s, name : %s because namespaceId is empty", external_id, inventoryObject.(containerinventory.ContainerApplication).DisplayName)
			}
		}
	}
	return nil
}

func (s *InventoryService) sendNSXRequestAndUpdateInventoryStore() error {
	if len(s.requestBuffer) > 0 {
		log.Info("Send update to NSX clusterId ", "ContainerInventoryData", s.requestBuffer)
		// TODO, check the context.TODO() be replaced by InventoryClient related todo
		resp, err := s.NSXClient.InventoryClient.ContainerInventoryApi.AddContainerInventoryUpdateUpdates(context.TODO(), s.NSXConfig.Cluster, containerinventory.ContainerInventoryData{ContainerInventoryObjects: s.requestBuffer})

		// Update NSX Inventory store when the request succeeds.
		log.V(1).Info("NSX request response", "response", resp)
		if err == nil {
			err = s.updateInventoryStore()
		}
		s.requestBuffer = make([]containerinventory.ContainerInventoryObject, 0)
		s.pending_add = make(map[string]interface{})
		s.pending_delete = make(map[string]interface{})
		return err
	}
	return nil
}

func (s *InventoryService) updateInventoryStore() error {
	log.Info("Update Inventory store after NSX request succeeds")
	for _, add_item := range s.pending_add {
		switch reflect.ValueOf(add_item).FieldByName("ResourceType").String() {

		case string(ContainerApplicationInstance):
			err := s.applicationInstanceStore.Add(add_item.(containerinventory.ContainerApplicationInstance))
			if err != nil {
				return err
			}

		}
	}
	for _, delete_item := range s.pending_delete {
		switch reflect.ValueOf(delete_item).FieldByName("ResourceType").String() {

		case string(ContainerApplicationInstance):
			err := s.applicationInstanceStore.Delete(delete_item.(containerinventory.ContainerApplicationInstance))
			if err != nil {
				return err
			}
		}
	}
	return nil
}
