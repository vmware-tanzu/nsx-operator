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
	InventoryClusterTypeWCP = "WCP"

	InventoryClusterCNIType = "NSX-Operator"

	// Inventory network status
	NetworkStatusHealthy   = "HEALTHY"
	NetworkStatusUnhealthy = "UNHEALTHY"

	// Inventory infra
	InventoryInfraTypeVsphere = "vSphere"

	InventoryMaxDisTags = 20
	InventoryK8sPrefix  = "dis:k8s:"
	MaxTagLen           = 256
	MaxResourceTypeLen  = 128
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

	InventoryStatusUp      = "UP"
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
	clusterStore             *ClusterStore

	requestBuffer []containerinventory.ContainerInventoryObject
	pendingAdd    map[string]interface{}
	pendingDelete map[string]interface{}

	stalePods map[string]interface{}
}

func InitializeService(service commonservice.Service) (*InventoryService, error) {
	inventoryService := NewInventoryService(service)
	err := inventoryService.Initialize()
	return inventoryService, err
}

func NewInventoryService(service commonservice.Service) *InventoryService {
	inventoryService := &InventoryService{
		requestBuffer: make([]containerinventory.ContainerInventoryObject, 0),
		pendingAdd:    make(map[string]interface{}),
		pendingDelete: make(map[string]interface{}),
		stalePods:     make(map[string]interface{}),
	}

	// TODO, Inventory store should have its own store
	inventoryService.applicationInstanceStore = &ApplicationInstanceStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{string(ContainerApplicationInstance): indexFunc}),
	}}
	inventoryService.clusterStore = &ClusterStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{string(ContainerCluster): indexFunc}),
	}}
	inventoryService.Service = service
	return inventoryService
}

func (s *InventoryService) Initialize() error {
	err := s.initContainerCluster()
	if err != nil {
		log.Error(err, "Init inventory cluster error")
		return err
	}
	err = s.SyncInventoryStoreByType(s.NSXConfig.Cluster)
	if err != nil {
		return err
	}
	return nil
}

func (s *InventoryService) initContainerCluster() error {
	cluster, err := s.GetContainerCluster()
	// If there is no such cluster, create one.
	// Otherwise, sync with NSX for different types of inventory objects.
	if err == nil {
		err = s.clusterStore.Add(&cluster)
		if err != nil {
			log.Error(err, "Add cluster to store")
		}
		return err
	}
	log.Error(err, "Cannot find existing container cluster, will create one")
	newContainerCluster := s.BuildInventoryCluster()
	cluster, err = s.AddContainerCluster(newContainerCluster)
	if err != nil {
		return err
	}
	err = s.clusterStore.Add(&cluster)
	if err != nil {
		log.Error(err, "Add cluster to store")
		return err
	}
	log.Info("A new ContainerCluster is added", "cluster", newContainerCluster.DisplayName)
	return nil
}

func (s *InventoryService) SyncInventoryStoreByType(clusterId string) error {
	log.Info("Populating inventory object from NSX")
	err := s.initContainerApplicationInstance(clusterId)
	if err != nil {
		return err
	}
	return nil
}

func (s *InventoryService) SyncInventoryObject(bufferedKeys sets.Set[InventoryKey]) (sets.Set[InventoryKey], error) {
	retryKeys := sets.New[InventoryKey]()
	startTime := time.Now()
	defer func() {
		log.Info("Finished syncing inventory object", "duration", time.Since(startTime))
	}()
	for key := range bufferedKeys {
		log.Info("Syncing inventory object", "object key", key)
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

func (s *InventoryService) DeleteResource(externalId string, resourceType InventoryType) error {
	log.V(1).Info("Delete inventory resource", "resource_type", resourceType, "external_id", externalId)
	var inventoryObject interface{}
	exists := false
	switch resourceType {

	case ContainerApplicationInstance:
		inventoryObject = s.applicationInstanceStore.GetByKey(externalId)
		if inventoryObject != nil {
			exists = true
		}
	default:
		return fmt.Errorf("unknown resource_type : %v for external_id %s", resourceType, externalId)
	}

	if exists {
		deletedInfo := make(map[string]interface{})
		deletedInfo["resource_type"] = resourceType
		deletedInfo["external_id"] = externalId
		s.requestBuffer = append(s.requestBuffer, containerinventory.ContainerInventoryObject{ContainerObject: deletedInfo,
			ObjectUpdateType: operationDelete})
		s.pendingDelete[externalId] = inventoryObject.(*containerinventory.ContainerApplicationInstance)

		if resourceType == ContainerApplicationInstance {
			return s.DeleteContainerApplicationInstance(externalId, inventoryObject.(*containerinventory.ContainerApplicationInstance))
		}
	}
	return nil
}

func (s *InventoryService) sendNSXRequestAndUpdateInventoryStore() error {
	if len(s.requestBuffer) > 0 {
		log.V(1).Info("Send update to NSX clusterId ", "ContainerInventoryData", s.requestBuffer)
		// TODO, check the context.TODO() be replaced by NsxApiClient related todo
		resp, err := s.NSXClient.NsxApiClient.ContainerInventoryApi.AddContainerInventoryUpdateUpdates(context.Background(),
			s.NSXConfig.Cluster,
			containerinventory.ContainerInventoryData{ContainerInventoryObjects: s.requestBuffer})

		// Update NSX Inventory store when the request succeeds.
		log.V(1).Info("NSX request response", "response", resp)
		if err == nil {
			err = s.updateInventoryStore()
		}
		s.requestBuffer = make([]containerinventory.ContainerInventoryObject, 0)
		s.pendingAdd = make(map[string]interface{})
		s.pendingDelete = make(map[string]interface{})
		return err
	}
	return nil
}

func (s *InventoryService) updateInventoryStore() error {
	log.Info("Update Inventory store after NSX request succeeds")
	for _, addItem := range s.pendingAdd {
		switch reflect.ValueOf(addItem).Elem().FieldByName("ResourceType").String() {

		case string(ContainerApplicationInstance):
			instance := addItem.(*containerinventory.ContainerApplicationInstance)
			err := s.applicationInstanceStore.Add(instance)
			if err != nil {
				return err
			}

		}
	}
	for _, deleteItem := range s.pendingDelete {
		switch reflect.ValueOf(deleteItem).Elem().FieldByName("ResourceType").String() {
		case string(ContainerApplicationInstance):
			instance := deleteItem.(*containerinventory.ContainerApplicationInstance)
			err := s.applicationInstanceStore.Delete(instance)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
