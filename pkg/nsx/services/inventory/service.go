package inventory

import (
	"context"
	"fmt"

	"github.com/antihax/optional"
	nsxt "github.com/vmware/go-vmware-nsxt"
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
