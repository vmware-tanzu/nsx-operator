package inventory

import (
	"context"
	"fmt"

	"github.com/antihax/optional"
	nsxt "github.com/vmware/go-vmware-nsxt"
)

func (s *InventoryService) initContainerNetworkPolicy(clusterId string) error {
	cursor := ""
	log.Info("Retrieving ContainerNetworkPolicy for cluster", "cluster id", clusterId)
	for {
		opts := &nsxt.ListContainerNetworkPoliciesOpts{ContainerClusterId: optional.NewString(clusterId)}
		if cursor != "" {
			opts.Cursor = optional.NewString(cursor)
		}
		networkPolicies, _, err := s.NSXClient.NsxApiClient.ContainerClustersApi.ListContainerNetworkPolicies(context.Background(), opts)
		if err != nil {
			return fmt.Errorf("failed to retrieve ContainerNetworkPolicy err: %w", err)
		}
		for _, networkpolicy := range networkPolicies.Results {
			err = s.NetworkPolicyStore.Add(&networkpolicy)
			if err != nil {
				return err
			}
		}
		if cursor = networkPolicies.Cursor; cursor == "" {
			break
		}
	}
	return nil
}
