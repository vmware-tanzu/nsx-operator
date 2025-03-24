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

var (
	log         = &logger.Log
	emptyKeySet = sets.New[InventoryKey]()
)

type InventoryService struct {
	commonservice.Service
	ApplicationInstanceStore *ApplicationInstanceStore
	ApplicationStore         *ApplicationStore
	ProjectStore             *ProjectStore
	ClusterNodeStore         *ClusterNodeStore
	NetworkPolicyStore       *NetworkPolicyStore
	IngressPolicyStore       *IngressPolicyStore
	ClusterStore             *ClusterStore

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
	inventoryService.ApplicationInstanceStore = &ApplicationInstanceStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{string(ContainerApplicationInstance): indexFunc}),
	}}
	inventoryService.ClusterStore = &ClusterStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{string(ContainerCluster): indexFunc}),
	}}
	inventoryService.ApplicationStore = &ApplicationStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{string(ContainerApplication): indexFunc}),
	}}
	inventoryService.ClusterNodeStore = &ClusterNodeStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{string(ContainerClusterNode): indexFunc}),
	}}
	inventoryService.NetworkPolicyStore = &NetworkPolicyStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{string(ContainerNetworkPolicy): indexFunc}),
	}}
	inventoryService.IngressPolicyStore = &IngressPolicyStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{string(ContainerIngressPolicy): indexFunc}),
	}}
	inventoryService.ProjectStore = &ProjectStore{ResourceStore: commonservice.ResourceStore{
		Indexer: cache.NewIndexer(keyFunc, cache.Indexers{string(ContainerProject): indexFunc}),
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
		err = s.ClusterStore.Add(&cluster)
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
	err = s.ClusterStore.Add(&cluster)
	if err != nil {
		log.Error(err, "Add cluster to store")
		return err
	}
	log.Info("A new ContainerCluster is added", "cluster", newContainerCluster.DisplayName)
	return nil
}

func (s *InventoryService) SyncInventoryStoreByType(clusterId string) error {
	log.Info("Populating inventory object from NSX")
	err := s.initContainerProject(clusterId)
	if err != nil {
		return err
	}
	err = s.initContainerApplicationInstance(clusterId)
	if err != nil {
		return err
	}
	err = s.initContainerApplication(clusterId)
	if err != nil {
		return err
	}
	err = s.initContainerClusterNode(clusterId)
	if err != nil {
		return err
	}
	err = s.initContainerNetworkPolicy(clusterId)
	if err != nil {
		return err
	}
	err = s.initContainerIngressPolicy(clusterId)
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
		case ContainerProject:
			retryKey := s.SyncContainerProject(name, key)
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
	log.Info("Delete inventory resource", "resource_type", resourceType, "external_id", externalId)
	switch resourceType {
	case ContainerProject:
		inventoryObject := s.ProjectStore.GetByKey(externalId)
		if inventoryObject == nil {
			return nil
		}
		s.DeleteInventoryObject(resourceType, externalId, inventoryObject)
		return s.DeleteContainerProject(externalId, inventoryObject.(*containerinventory.ContainerProject))

	case ContainerApplicationInstance:
		inventoryObject := s.ApplicationInstanceStore.GetByKey(externalId)
		if inventoryObject == nil {
			return nil
		}
		s.DeleteInventoryObject(resourceType, externalId, inventoryObject)
		return s.DeleteContainerApplicationInstance(externalId, inventoryObject.(*containerinventory.ContainerApplicationInstance))
	default:
		return fmt.Errorf("unknown resource_type: %v for external_id %s", resourceType, externalId)
	}

}

func (s *InventoryService) DeleteInventoryObject(resourceType InventoryType, externalId string, inventoryObject interface{}) {
	deletedInfo := map[string]interface{}{
		"resource_type": resourceType,
		"external_id":   externalId,
	}
	s.requestBuffer = append(s.requestBuffer, containerinventory.ContainerInventoryObject{
		ContainerObject:  deletedInfo,
		ObjectUpdateType: operationDelete,
	})
	switch resourceType {
	case ContainerProject:
		s.pendingDelete[externalId] = inventoryObject.(*containerinventory.ContainerProject)
	case ContainerApplicationInstance:
		s.pendingDelete[externalId] = inventoryObject.(*containerinventory.ContainerApplicationInstance)
	}
}

func (s *InventoryService) sendNSXRequestAndUpdateInventoryStore() error {
	if len(s.requestBuffer) > 0 {
		log.V(1).Info("Send update to NSX clusterId ", "ContainerInventoryData", s.requestBuffer)
		// TODO, check the context.TODO() be replaced by NsxApiClient related todo
		resp, err := s.NSXClient.NsxApiClient.ContainerInventoryApi.AddContainerInventoryUpdateUpdates(context.Background(),
			s.NSXConfig.Cluster,
			containerinventory.ContainerInventoryData{ContainerInventoryObjects: s.requestBuffer})

		// Update NSX Inventory store when the request succeeds.
		if resp != nil {
			log.Info("NSX request response", "response code", resp.StatusCode)
		}
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
		case string(ContainerProject):
			project := addItem.(*containerinventory.ContainerProject)
			err := s.ProjectStore.Add(project)
			if err != nil {
				return err
			}
		case string(ContainerApplicationInstance):
			instance := addItem.(*containerinventory.ContainerApplicationInstance)
			err := s.ApplicationInstanceStore.Add(instance)
			if err != nil {
				return err
			}

		}
	}
	for _, deleteItem := range s.pendingDelete {
		switch reflect.ValueOf(deleteItem).Elem().FieldByName("ResourceType").String() {
		case string(ContainerProject):
			project := deleteItem.(*containerinventory.ContainerProject)
			err := s.ProjectStore.Delete(project)
			if err != nil {
				return err
			}
		case string(ContainerApplicationInstance):
			instance := deleteItem.(*containerinventory.ContainerApplicationInstance)
			err := s.ApplicationInstanceStore.Delete(instance)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
