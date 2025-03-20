package inventory

import (
	"context"
	"fmt"

	"github.com/antihax/optional"
	nsxt "github.com/vmware/go-vmware-nsxt"
	"github.com/vmware/go-vmware-nsxt/containerinventory"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

func (s *InventoryService) SyncContainerApplicationInstance(name string, namespace string, key InventoryKey) *InventoryKey {
	pod := &corev1.Pod{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, pod)
	externalId := key.ExternalId
	if apierrors.IsNotFound(err) ||
		((err == nil) && (string(pod.UID) != externalId)) {
		err = s.DeleteResource(externalId, ContainerApplicationInstance)
		if err != nil {
			log.Error(err, "Delete ContainerApplicationInstance Resource error", "key", key)
			return &key
		}
	} else if err == nil {
		retry := s.BuildPod(pod)
		if retry {
			return &key
		}
	} else {
		log.Error(err, "Unexpected error is found while processing Pod")
	}
	return nil
}

func (s *InventoryService) initContainerApplicationInstance(clusterId string) error {
	cursor := ""
	log.Info("Retrieving ContainerApplicationInstances for cluster", "cluster id", clusterId)
	for {
		opts := &nsxt.ListContainerApplicationInstancesOpts{ContainerClusterId: optional.NewString(clusterId)}
		if cursor != "" {
			opts.Cursor = optional.NewString(cursor)
		}
		applicationInstances, _, err := s.NSXClient.NsxApiClient.ContainerApplicationsApi.ListContainerApplicationInstances(context.Background(), opts)
		if err != nil {
			return fmt.Errorf("failed to retrieve ContainerApplicationInstances err: %w", err)
		}
		for _, applicationInstance := range applicationInstances.Results {
			if applicationInstance.ContainerProjectId == "" {
				// ContainerProjectId is the index for applicationInstanceStore and cannot be empty.
				// The case should not happen and we need to put this one into stale cache and clean it during resync if it did happen.
				s.stalePods[applicationInstance.ExternalId] = &applicationInstance
				err = fmt.Errorf("empty ContainerProjectId for application instance %s, external_id %s", applicationInstance.DisplayName, applicationInstance.ExternalId)
				log.Error(err, "Retrieving ContainerApplicationInstances")
				continue
			}
			err = s.ApplicationInstanceStore.Add(&applicationInstance)
			if err != nil {
				return err
			}
		}
		if cursor = applicationInstances.Cursor; cursor == "" {
			break
		}
	}
	return nil
}

func (s *InventoryService) DeleteContainerApplicationInstance(externalId string, inventoryObject *containerinventory.ContainerApplicationInstance) error {
	namespaceId := inventoryObject.ContainerProjectId
	if namespaceId != "" {
		// TODO, check if need to update the service
		/*
			project, exists, _ := s.projectStore.GetByKey(namespaceId)
			if exists {
				s.removeServiceIdForPods(external_id, namespaceId, project.(containerinventory.ContainerProject).DisplayName, []string{})
			}
		*/
	} else {
		return fmt.Errorf("cannot update Pods for removed service id : %s, name : %s because namespaceId is empty", externalId, inventoryObject.DisplayName)
	}
	return nil
}
