package inventory

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"

	"github.com/vmware/go-vmware-nsxt/containerinventory"
	"k8s.io/apimachinery/pkg/util/sets"
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

	INVENTORY_MAX_DIS_TAGS = 20
	INVENTORY_K8S_PREFIX   = "dis:k8s:"
	MAX_TAG_LEN            = 256
	MAX_RESOURCE_TYPE_LEN  = 128
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
	log         = &logger.Log
	emptyKeySet = sets.New[InventoryKey]()
)

type InventoryService struct {
	commonservice.Service
	applicationInstanceStore *ApplicationInstanceStore
	clusterStore             *ClusteStore

	requestBuffer  []containerinventory.ContainerInventoryObject
	pending_add    map[string]interface{}
	pending_delete map[string]interface{}

	stalePods map[string]interface{}
}

func InitializeService(service commonservice.Service) (*InventoryService, error) {
	inventoryService := &InventoryService{
		requestBuffer:  make([]containerinventory.ContainerInventoryObject, 0),
		pending_add:    make(map[string]interface{}),
		pending_delete: make(map[string]interface{}),
		stalePods:      make(map[string]interface{}),
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
	cluster, err := s.GetContainerCluster()
	// If there is no such cluster, create one.
	// Otherwise, sync with NSX for different types of inventory objects.
	if err != nil {
		log.Error(err, "Cannot find existing container cluster, will create one")
	} else {
		s.clusterStore.Add(&cluster)
		return nil
	}

	newContainerCluster := s.BuildInventoryCluster()
	cluster, err = s.AddContainerCluster(newContainerCluster)
	if err != nil {
		return err
	}
	s.clusterStore.Add(&cluster)
	log.Info("A new ContainerCluster is added", "cluster", newContainerCluster.DisplayName)
	return nil
}

func (s *InventoryService) SyncInventoryStoreByType(clusterId string) error {
	err := s.syncContainerApplicationInstance(clusterId)
	if err != nil {
		return err
	}
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

func (s *InventoryService) SyncInventoryObject(bufferedKeys sets.Set[InventoryKey]) (sets.Set[InventoryKey], error) {
	retryKeys := sets.New[InventoryKey]()
	startTime := time.Now()
	defer func() {
		log.Info("Finished syncing inventory object", "duration", time.Since(startTime))
	}()
	for key := range bufferedKeys {
		log.V(1).Info("Syncing inventory object", "object key", key)
		namespace, name, err := cache.SplitMetaNamespaceKey(key.Key)
		if err != nil {
			log.Error(err, "Failed to split meta namespace key", "key", key)
			continue
		}
		switch key.InventoryType {

		case ContainerApplicationInstance:
			retryKey := s.SyncContainerApplicationInstance(name, namespace, key)
			if retryKey != nil {
				retryKeys.Insert(*retryKey)
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

		if resource_type == ContainerApplicationInstance {
			return s.DeleteContainerApplicationInstance(external_id, inventoryObject.(*containerinventory.ContainerApplicationInstance))
		}
	}
	return nil
}

func (s *InventoryService) sendNSXRequestAndUpdateInventoryStore() error {
	if len(s.requestBuffer) > 0 {
		log.V(1).Info("Send update to NSX clusterId ", "ContainerInventoryData", s.requestBuffer)
		// TODO, check the context.TODO() be replaced by NsxApiClient related todo
		resp, err := s.NSXClient.NsxApiClient.ContainerInventoryApi.AddContainerInventoryUpdateUpdates(context.Background(), s.NSXConfig.Cluster, containerinventory.ContainerInventoryData{ContainerInventoryObjects: s.requestBuffer})

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
			instacne := add_item.(containerinventory.ContainerApplicationInstance)
			err := s.applicationInstanceStore.Add(&instacne)
			if err != nil {
				return err
			}

		}
	}
	for _, delete_item := range s.pending_delete {
		switch reflect.ValueOf(delete_item).FieldByName("ResourceType").String() {
		case string(ContainerApplicationInstance):
			instance := delete_item.(containerinventory.ContainerApplicationInstance)
			err := s.applicationInstanceStore.Delete(&instance)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
