package inventory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	commonservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsx_util "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"

	"github.com/vmware/go-vmware-nsxt/containerinventory"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
)

var (
	log = logger.Log
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

func InitializeService(service commonservice.Service, cleanup bool) (*InventoryService, error) {
	inventoryService := NewInventoryService(service)
	err := inventoryService.Initialize(cleanup)
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

func (s *InventoryService) Initialize(cleanup bool) error {
	err := s.initContainerCluster(cleanup)
	if err != nil {
		log.Error(err, "Init inventory cluster error")
		return err
	}
	if cleanup {
		return nil
	}
	clusterUUID := util.GetClusterUUID(s.NSXConfig.Cluster).String()
	err = s.SyncInventoryStoreByType(clusterUUID)
	if err != nil {
		return err
	}
	return nil
}

func (s *InventoryService) initContainerCluster(cleanup bool) error {
	cluster, err := s.GetContainerCluster(cleanup)
	// If there is no such cluster, create one.
	// Otherwise, sync with NSX for different types of inventory objects.
	if err == nil {
		err = s.ClusterStore.Add(&cluster)
		if err != nil {
			log.Error(err, "Add cluster to store")
		}
		return err
	}
	if cleanup {
		if errors.Is(err, nsx_util.HttpNotFoundError) {
			log.Info("No existing container cluster found")
			return nil
		}
		return err
	}
	if !errors.Is(err, nsx_util.HttpNotFoundError) {
		return err
	}
	log.Info("Cannot find existing container cluster, will create one")
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

func (s *InventoryService) SyncInventoryStoreByType(clusterUUID string) error {
	log.Info("Populating inventory object from NSX")
	err := s.initContainerProject(clusterUUID)
	if err != nil {
		return err
	}
	err = s.initContainerApplicationInstance(clusterUUID)
	if err != nil {
		return err
	}
	err = s.initContainerApplication(clusterUUID)
	if err != nil {
		return err
	}
	err = s.initContainerClusterNode(clusterUUID)
	if err != nil {
		return err
	}
	err = s.initContainerNetworkPolicy(clusterUUID)
	if err != nil {
		return err
	}
	err = s.initContainerIngressPolicy(clusterUUID)
	if err != nil {
		return err
	}
	return nil

}

func (s *InventoryService) SyncInventoryObject(bufferedKeys sets.Set[InventoryKey]) (sets.Set[InventoryKey], error) {
	retryKeys := sets.New[InventoryKey]()
	startTime := time.Now()
	defer func() {
		log.Trace("Finished syncing inventory object", "duration", time.Since(startTime))
	}()
	for key := range bufferedKeys {
		log.Trace("Syncing inventory object", "object key", key)
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
		case ContainerApplication:
			retryKey := s.SyncContainerApplication(name, namespace, key)
			if retryKey != nil {
				retryKeys.Insert(*retryKey)
			}
		case ContainerIngressPolicy:
			retryKey := s.SyncContainerIngressPolicy(name, namespace, key)
			if retryKey != nil {
				retryKeys.Insert(*retryKey)
			}
		case ContainerClusterNode:
			retryKey := s.SyncContainerClusterNode(name, key)
			if retryKey != nil {
				retryKeys.Insert(*retryKey)
			}
		case ContainerNetworkPolicy:
			retryKey := s.SyncContainerNetworkPolicy(name, namespace, key)
			if retryKey != nil {
				retryKeys.Insert(*retryKey)
			}
		}
	}

	err := s.sendNSXRequestAndUpdateInventoryStore(context.Background())
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
	case ContainerApplication:
		inventoryObject := s.ApplicationStore.GetByKey(externalId)
		if inventoryObject == nil {
			return nil
		}
		s.DeleteInventoryObject(resourceType, externalId, inventoryObject)
	case ContainerApplicationInstance:
		inventoryObject := s.ApplicationInstanceStore.GetByKey(externalId)
		if inventoryObject == nil {
			return nil
		}
		s.DeleteInventoryObject(resourceType, externalId, inventoryObject)
		return s.DeleteContainerApplicationInstance(externalId, inventoryObject.(*containerinventory.ContainerApplicationInstance))
	case ContainerIngressPolicy:
		inventoryObject := s.IngressPolicyStore.GetByKey(externalId)
		if inventoryObject == nil {
			return nil
		}
		s.DeleteInventoryObject(resourceType, externalId, inventoryObject)
		return nil
	case ContainerClusterNode:
		inventoryObject := s.ClusterNodeStore.GetByKey(externalId)
		if inventoryObject == nil {
			return nil
		}
		s.DeleteInventoryObject(resourceType, externalId, inventoryObject)
	case ContainerNetworkPolicy:
		inventoryObject := s.NetworkPolicyStore.GetByKey(externalId)
		if inventoryObject == nil {
			return nil
		}
		s.DeleteInventoryObject(resourceType, externalId, inventoryObject)
	default:
		return fmt.Errorf("unknown resource_type: %v for external_id %s", resourceType, externalId)
	}
	return nil
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
	case ContainerApplication:
		s.pendingDelete[externalId] = inventoryObject.(*containerinventory.ContainerApplication)
	case ContainerApplicationInstance:
		s.pendingDelete[externalId] = inventoryObject.(*containerinventory.ContainerApplicationInstance)
	case ContainerIngressPolicy:
		s.pendingDelete[externalId] = inventoryObject.(*containerinventory.ContainerIngressPolicy)
	case ContainerClusterNode:
		s.pendingDelete[externalId] = inventoryObject.(*containerinventory.ContainerClusterNode)
	case ContainerNetworkPolicy:
		s.pendingDelete[externalId] = inventoryObject.(*containerinventory.ContainerNetworkPolicy)
	}
}

func (s *InventoryService) sendNSXRequestAndUpdateInventoryStore(ctx context.Context) error {
	if len(s.requestBuffer) > 0 {
		log.Info("Send update to inventory", "ContainerInventoryData", s.requestBuffer)
		// TODO, check the context.TODO() be replaced by NsxApiClient related todo
		resp, err := s.NSXClient.NsxApiClient.ContainerInventoryApi.AddContainerInventoryUpdateUpdates(ctx,
			util.GetClusterUUID(s.NSXConfig.Cluster).String(),
			containerinventory.ContainerInventoryData{ContainerInventoryObjects: s.requestBuffer})

		// Update NSX Inventory store when the request succeeds.
		if resp != nil {
			log.Trace("NSX request response", "response code", resp.StatusCode)
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
	log.Trace("Update Inventory store after NSX request succeeds")
	for _, addItem := range s.pendingAdd {
		switch reflect.ValueOf(addItem).Elem().FieldByName("ResourceType").String() {
		case string(ContainerProject):
			project := addItem.(*containerinventory.ContainerProject)
			err := s.ProjectStore.Add(project)
			if err != nil {
				return err
			}
		case string(ContainerApplication):
			instance := addItem.(*containerinventory.ContainerApplication)
			err := s.ApplicationStore.Add(instance)
			if err != nil {
				return err
			}
		case string(ContainerApplicationInstance):
			instance := addItem.(*containerinventory.ContainerApplicationInstance)
			err := s.ApplicationInstanceStore.Add(instance)
			if err != nil {
				return err
			}
		case string(ContainerIngressPolicy):
			instance := addItem.(*containerinventory.ContainerIngressPolicy)
			err := s.IngressPolicyStore.Add(instance)
			if err != nil {
				return err
			}

		case string(ContainerClusterNode):
			node := addItem.(*containerinventory.ContainerClusterNode)
			err := s.ClusterNodeStore.Add(node)
			if err != nil {
				return err
			}
		case string(ContainerNetworkPolicy):
			instance := addItem.(*containerinventory.ContainerNetworkPolicy)
			err := s.NetworkPolicyStore.Add(instance)
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
		case string(ContainerApplication):
			instance := deleteItem.(*containerinventory.ContainerApplication)
			err := s.ApplicationStore.Delete(instance)
			if err != nil {
				return err
			}
		case string(ContainerApplicationInstance):
			instance := deleteItem.(*containerinventory.ContainerApplicationInstance)
			err := s.ApplicationInstanceStore.Delete(instance)
			if err != nil {
				return err
			}
		case string(ContainerNetworkPolicy):
			instance := deleteItem.(*containerinventory.ContainerNetworkPolicy)
			err := s.NetworkPolicyStore.Delete(instance)
			if err != nil {
				return err
			}
		case string(ContainerIngressPolicy):
			instance := deleteItem.(*containerinventory.ContainerIngressPolicy)
			err := s.IngressPolicyStore.Delete(instance)
			if err != nil {
				return err
			}
		case string(ContainerClusterNode):
			node := deleteItem.(*containerinventory.ContainerClusterNode)
			err := s.ClusterNodeStore.Delete(node)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *InventoryService) UpdatePendingAdd(externalId string, inventoryObject interface{}) {
	s.pendingAdd[externalId] = inventoryObject
}

// CleanupBeforeVPCDeletion cleans up all clusters registered in the inventory. Since the resources in inventory
// has no dependency on the exact VPC, we will perform the operation before cleaning up VPCs.
func (s *InventoryService) CleanupBeforeVPCDeletion(ctx context.Context) error {
	//Cleanup cluster
	clusters := s.ClusterStore.List()
	log.Info("Starting inventory cluster cleanup", "clusterCount", len(clusters), "status", "attempting")
	if len(clusters) == 0 {
		log.Info("No inventory cluster found while cleanup inventory cluster", "count", 0)
		return nil
	}
	cluster := clusters[0].(*containerinventory.ContainerCluster)
	clusterID := cluster.ExternalId
	clusterName := cluster.DisplayName
	log.Info("Attempting to delete inventory cluster", "clusterID", clusterID, "clusterName", clusterName)
	err := s.DeleteContainerCluster(clusterID, ctx)
	if err != nil {
		log.Error(err, "Failed to delete inventory cluster from NSX", "clusterID", clusterID, "clusterName", clusterName, "status", "failed")
		return err
	}
	err = s.ClusterStore.Delete(cluster)
	if err != nil {
		log.Error(err, "Failed to delete inventory cluster from store", "clusterID", clusterID, "clusterName", clusterName, "status", "failed")
		return err
	}
	log.Info("Successfully deleted inventory cluster", "clusterID", clusterID, "clusterName", clusterName, "status", "success")
	return nil
}
