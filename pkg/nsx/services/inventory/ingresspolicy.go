package inventory

import (
	"context"
	"fmt"

	"github.com/antihax/optional"
	nsxt "github.com/vmware/go-vmware-nsxt"
)

func (s *InventoryService) initContainerIngressPolicy(clusterId string) error {
	cursor := ""
	log.Info("Retrieving ContainerIngressPolicy for cluster", "cluster id", clusterId)
	for {
		opts := &nsxt.ListContainerIngressPoliciesOpts{ContainerClusterId: optional.NewString(clusterId)}
		if cursor != "" {
			opts.Cursor = optional.NewString(cursor)
		}
		ingressPolicies, _, err := s.NSXClient.NsxApiClient.ContainerClustersApi.ListContainerIngressPolicies(context.Background(), opts)
		if err != nil {
			return fmt.Errorf("failed to retrieve ContainerIngressPolicy err: %w", err)
		}
		for _, ingressPolicy := range ingressPolicies.Results {
			err = s.IngressPolicyStore.Add(&ingressPolicy)
			if err != nil {
				return err
			}
		}
		if cursor = ingressPolicies.Cursor; cursor == "" {
			break
		}
	}
	return nil
}
