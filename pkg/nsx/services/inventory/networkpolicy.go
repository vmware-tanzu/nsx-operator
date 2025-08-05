package inventory

import (
	"context"
	"fmt"

	"github.com/antihax/optional"
	nsxt "github.com/vmware/go-vmware-nsxt"
	"github.com/vmware/go-vmware-nsxt/containerinventory"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

func (s *InventoryService) IsNetworkPolicyDeleted(namespace, name, externalId string) bool {
	networkPolicy := &networkingv1.NetworkPolicy{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, networkPolicy)
	if apierrors.IsNotFound(err) || ((err == nil) && (string(networkPolicy.UID) != externalId)) {
		return true
	} else {
		log.Error(err, "Check NetworkPolicy deleted", "NetworkPolicy", name, "Namespace", namespace, "External id", externalId)
		return false
	}
}

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

func (s *InventoryService) SyncContainerNetworkPolicy(name, namespace string, key InventoryKey) *InventoryKey {
	networkPolicy := &networkingv1.NetworkPolicy{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, networkPolicy)
	externalId := key.ExternalId
	if apierrors.IsNotFound(err) ||
		((err == nil) && (string(networkPolicy.UID) != externalId)) {
		err = s.DeleteResource(externalId, ContainerNetworkPolicy)
		if err != nil {
			log.Error(err, "Delete ContainerNetworkPolicy Resource error", "key", key)
			return &key
		}
	} else if err == nil {
		retry := s.BuildNetworkPolicy(networkPolicy)
		if retry {
			return &key
		}
	} else {
		log.Error(err, "Unexpected error is found while processing NetworkPolicy")
	}
	return nil
}

func (s *InventoryService) CleanStaleInventoryNetworkPolicy() error {
	log.V(1).Info("Clean stale InventoryNetworkPolicy")
	containerNetworkPolicies := s.NetworkPolicyStore.List()
	for _, networkPolicy := range containerNetworkPolicies {
		networkPolicyObj := networkPolicy.(*containerinventory.ContainerNetworkPolicy)
		project := s.ProjectStore.GetByKey(networkPolicyObj.ContainerProjectId)
		if project == nil {
			log.Info("Cannot find ContainerProject by id, so clean up stale InventoryNetworkPolicy", "Project Id", networkPolicyObj.ContainerProjectId,
				"NetworkPolicy name", networkPolicyObj.DisplayName, "External Id", networkPolicyObj.ExternalId)
			err := s.DeleteResource(networkPolicyObj.ExternalId, ContainerNetworkPolicy)
			if err != nil {
				log.Error(err, "Clean stale InventoryNetworkPolicy", "External Id", networkPolicyObj.ExternalId)
				return err
			}
		} else if s.IsNetworkPolicyDeleted(project.(*containerinventory.ContainerProject).DisplayName, networkPolicyObj.DisplayName, networkPolicyObj.ExternalId) {
			log.Info("Clean stale InventoryNetworkPolicy", "Name", networkPolicyObj.DisplayName, "External Id", networkPolicyObj.ExternalId)
			err := s.DeleteResource(networkPolicyObj.ExternalId, ContainerNetworkPolicy)
			if err != nil {
				log.Error(err, "Clean stale InventoryNetworkPolicy", "External Id", networkPolicyObj.ExternalId)
				return err
			}
		}
	}
	return nil
}
