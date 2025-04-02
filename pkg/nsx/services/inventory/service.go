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

func (s *InventoryService) DeleteContainerApplication(externalId string, inventoryObject *containerinventory.ContainerApplication) error {
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
