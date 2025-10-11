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

func (s *InventoryService) IsNamespaceDeleted(name, externalId string) bool {
	namespaceObj := &corev1.Namespace{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name}, namespaceObj)
	if apierrors.IsNotFound(err) || ((err == nil) && (string(namespaceObj.UID) != externalId)) {
		return true
	} else {
		return false
	}
}

func (s *InventoryService) SyncContainerProject(name string, key InventoryKey) *InventoryKey {
	namespace := &corev1.Namespace{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name}, namespace)
	externalId := key.ExternalId
	if apierrors.IsNotFound(err) ||
		((err == nil) && (string(namespace.UID) != externalId)) {
		err = s.DeleteResource(externalId, ContainerProject)
		if err != nil {
			log.Error(err, "Delete ContainerProject Resource error", "key", key)
			return &key
		}
	} else if err == nil {
		retry := s.BuildNamespace(namespace)
		if retry {
			return &key
		}
	} else {
		log.Error(err, "Unexpected error is found while processing Namespace")
	}
	return nil
}

func (s *InventoryService) initContainerProject(clusterId string) error {
	cursor := ""
	log.Info("Retrieving ContainerProjects for cluster", "cluster id", clusterId)
	for {
		opts := &nsxt.ListContainerProjectsOpts{ContainerClusterId: optional.NewString(clusterId)}
		if cursor != "" {
			opts.Cursor = optional.NewString(cursor)
		}
		projects, _, err := s.NSXClient.NsxApiClient.ContainerProjectsApi.ListContainerProjects(context.Background(), opts)
		if err != nil {
			return fmt.Errorf("failed to retrieve container projects err: %w", err)
		}
		for _, project := range projects.Results {
			err = s.ProjectStore.Add(&project)
			if err != nil {
				return err
			}
		}
		if cursor = projects.Cursor; cursor == "" {
			break
		}
	}
	return nil
}

func (s *InventoryService) CleanStaleInventoryContainerProject() error {
	log.Trace("Clean stale InventoryContainerProject")
	containerProjects := s.ProjectStore.List()
	for _, containerProject := range containerProjects {
		project := containerProject.(*containerinventory.ContainerProject)
		// Check if the namespace still exists in the K8s cluster
		if s.IsNamespaceDeleted(project.DisplayName, project.ExternalId) {
			// Namespace doesn't exist or its ID changed - delete the stale container project
			log.Info("Found stale container project", "Name", project.DisplayName, "ExternalId", project.ExternalId)
			err := s.DeleteResource(project.ExternalId, ContainerProject)
			if err != nil {
				log.Error(err, "Failed to delete stale container project", "Name", project.DisplayName, "ExternalId", project.ExternalId)
				return err
			}
		}
	}
	return nil
}
