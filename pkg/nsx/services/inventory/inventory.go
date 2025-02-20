package inventory

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"

	"github.com/vmware/go-vmware-nsxt/containerinventory"
	corev1 "k8s.io/api/core/v1"
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

	// Inventory cluster type
	INVENTORY_CLUSTER_TYPE_WCP = "WCP"

	INVENTORY_CLUSTER_CNI_TYPE = "NSX-Operator"

	// Inventory network status
	NETWORK_STATUS_HEALTHY   = "HEALTHY"
	NETWORK_STATUS_UNHEALTHY = "UNHEALTHY"

	// Inventory infra
	INVENTORY_INFRA_TYPE_VSPHERE = "vSphere"
)

type InventoryKey struct {
	InventoryType InventoryType
	ExternalId    string
	Key           string
}

const (
	operationCreate = "CREATE"
	operationUpdate = "UPDATE"
	operationDelete = "DELETE"
	operationNone   = "NONE"

	InventoryStausUp       = "UP"
	InventoryStatusDown    = "DOWN"
	InventoryStatusUnknown = "UNKNOWN"
)

var (
	log = &logger.Log
)

type InventoryService struct {
	commonservice.Service
	applicationInstanceStore *ApplicationInstanceStore
	clusterStore             *ClusteStore

	requestBuffer  []containerinventory.ContainerInventoryObject
	pending_add    map[string]interface{}
	pending_delete map[string]interface{}
}

func InitializeService(service commonservice.Service) (*InventoryService, error) {
	inventoryService := &InventoryService{
		requestBuffer:  make([]containerinventory.ContainerInventoryObject, 0),
		pending_add:    make(map[string]interface{}),
		pending_delete: make(map[string]interface{}),
	}

	// TODO, Inventory store should have its own store
	inventoryService.applicationInstanceStore = &ApplicationInstanceStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{string(ContainerApplicationInstance): indexFunc}),
	}}
	inventoryService.clusterStore = &ClusteStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{string(ContainerCluster): indexFunc}),
	}}
	inventoryService.Service = service
	return inventoryService, nil
}

func (s *InventoryService) Init() error {
	_, err := s.GetContainerCluster()
	// If there is no such cluster, create one.
	// Otherwise, sync with NSX for different types of inventory objects.
	if err != nil {
		log.Error(err, "Cannot find existing container cluster, will create one")
	} else {
		return nil
	}

	newContainerCluster := s.BuildInentoryCluster()
	_, err = s.AddContainerCluster(newContainerCluster)
	if err != nil {
		return err
	}
	log.Info("A new ContainerCluster is added", "cluster", newContainerCluster.DisplayName)
	return nil
}

func (s *InventoryService) compareAndMergeUpdate(pre interface{}, cur interface{}) (string, map[string]interface{}) {
	updateProperties := compareResources(pre, cur)
	if pre == nil {
		s.requestBuffer = append(s.requestBuffer, containerinventory.ContainerInventoryObject{ContainerObject: updateProperties, ObjectUpdateType: operationCreate})
		return operationCreate, updateProperties
	} else if len(updateProperties) > 2 {
		s.requestBuffer = append(s.requestBuffer, containerinventory.ContainerInventoryObject{ContainerObject: updateProperties, ObjectUpdateType: operationUpdate})
		return operationUpdate, updateProperties
	} else {
		return operationNone, nil
	}
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
				retry := s.BuildPod(pod)
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
	var inventoryObject interface{}
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
		s.requestBuffer = append(s.requestBuffer, containerinventory.ContainerInventoryObject{ContainerObject: deletedInfo, ObjectUpdateType: operationDelete})
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
		log.V(1).Info("Send update to NSX clusterId ", "ContainerInventoryData", s.requestBuffer)
		// TODO, check the context.TODO() be replaced by InventoryClient related todo
		resp, err := s.NSXClient.InventoryClient.ContainerInventoryApi.AddContainerInventoryUpdateUpdates(context.Background(), s.NSXConfig.Cluster, containerinventory.ContainerInventoryData{ContainerInventoryObjects: s.requestBuffer})

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

func (s *InventoryService) GetContainerCluster() (containerinventory.ContainerCluster, error) {
	log.Info("Send request to NSX to get cluster ", "cluster id", s.NSXConfig.Cluster)
	containerCluster, _, err := s.NSXClient.InventoryClient.ContainerClustersApi.GetContainerCluster(context.TODO(), s.NSXConfig.Cluster)
	if err != nil {
		return containerCluster, err
	}
	err = s.clusterStore.Add(containerCluster)
	return containerCluster, err
}

func (s *InventoryService) AddContainerCluster(cluster containerinventory.ContainerCluster) (containerinventory.ContainerCluster, error) {
	log.Info("Send request to NSX to create cluster", "cluster", s.NSXConfig.Cluster)
	cluster.ClusterType = INVENTORY_CLUSTER_TYPE_WCP
	cluster, _, err := s.NSXClient.InventoryClient.ContainerClustersApi.AddContainerCluster(context.TODO(), cluster)
	if err != nil {
		return cluster, err
	}
	err = s.clusterStore.Add(cluster)
	return cluster, err
}
