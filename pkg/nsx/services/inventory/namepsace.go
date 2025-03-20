package inventory

import (
	"context"
	"fmt"

	"github.com/antihax/optional"
	nsxt "github.com/vmware/go-vmware-nsxt"
)

func (s *InventoryService) initContainerProject(clusterId string) error {
	cursor := ""
	log.Info("Retrieving ContainerProject for cluster", "cluster id", clusterId)
	for {
		opts := &nsxt.ListContainerProjectsOpts{ContainerClusterId: optional.NewString(clusterId)}
		if cursor != "" {
			opts.Cursor = optional.NewString(cursor)
		}
		projects, _, err := s.NSXClient.NsxApiClient.ContainerProjectsApi.ListContainerProjects(context.Background(), opts)
		if err != nil {
			return fmt.Errorf("failed to retrieve ContainerProject err: %w", err)
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
