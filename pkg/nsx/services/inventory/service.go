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

func (s *InventoryService) isApplicationDeleted(namespace, name, externalId string) bool {
	service := &corev1.Service{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, service)
	return apierrors.IsNotFound(err) || ((err == nil) && (string(service.UID) != externalId))
}

func (s *InventoryService) initContainerApplication(clusterId string) error {
	cursor := ""
	log.Info("Retrieving ContainerApplication for cluster", "cluster id", clusterId)
	for {
		opts := &nsxt.ListContainerApplicationsOpts{ContainerClusterId: optional.NewString(clusterId)}
		if cursor != "" {
			opts.Cursor = optional.NewString(cursor)
		}
		applications, _, err := s.NSXClient.NsxApiClient.ContainerApplicationsApi.ListContainerApplications(context.Background(), opts)
		if err != nil {
			return fmt.Errorf("failed to retrieve ContainerApplication err: %w", err)
		}
		for _, application := range applications.Results {
			err = s.ApplicationStore.Add(&application)
			if err != nil {
				return err
			}
		}
		if cursor = applications.Cursor; cursor == "" {
			break
		}
	}
	return nil
}

func (s *InventoryService) SyncContainerApplication(name, namespace string, key InventoryKey) *InventoryKey {
	service := &corev1.Service{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, service)
	externalId := key.ExternalId
	if apierrors.IsNotFound(err) ||
		((err == nil) && (string(service.UID) != externalId)) {
		err = s.DeleteResource(externalId, ContainerApplication)
		if err != nil {
			log.Error(err, "Delete ContainerApplication Resource error", "key", key)
			return &key
		}
	} else if err == nil {
		retry := s.BuildService(service)
		if retry {
			return &key
		}
	} else {
		log.Error(err, "Unexpected error is found while processing Service")
	}
	return nil
}

func (s *InventoryService) CleanStaleInventoryApplication() error {
	log.V(1).Info("Clean stale InventoryApplication")
	inventoryApplications := s.ApplicationStore.List()
	for _, inventoryApplication := range inventoryApplications {
		inventoryApplication := inventoryApplication.(*containerinventory.ContainerApplication)
		project := s.ProjectStore.GetByKey(inventoryApplication.ContainerProjectId)
		if project == nil {
			log.Info("Cannot find ContainerProject by id, so clean up stale ContainerApplication", "Project Id",
				inventoryApplication.ContainerProjectId, "Application name", inventoryApplication.DisplayName, "External Id", inventoryApplication.ExternalId)
			err := s.DeleteResource(inventoryApplication.ExternalId, ContainerApplication)
			if err != nil {
				log.Error(err, "Clean stale InventoryApplication", "External Id", inventoryApplication.ExternalId)
				return err
			}
		} else if s.isApplicationDeleted(project.(*containerinventory.ContainerProject).DisplayName, inventoryApplication.DisplayName, inventoryApplication.ExternalId) {
			log.Info("Clean stale inventoryApplication", "Name", inventoryApplication.DisplayName, "External Id", inventoryApplication.ExternalId)
			err := s.DeleteResource(inventoryApplication.ExternalId, ContainerApplication)
			if err != nil {
				log.Error(err, "Clean stale InventoryApplication", "External Id", inventoryApplication.ExternalId)
				return err
			}
		}
	}
	return nil
}
