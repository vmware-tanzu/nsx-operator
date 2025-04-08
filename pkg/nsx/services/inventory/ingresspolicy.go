package inventory

import (
	"context"
	"errors"
	"fmt"

	"github.com/antihax/optional"
	nsxt "github.com/vmware/go-vmware-nsxt"
	"github.com/vmware/go-vmware-nsxt/containerinventory"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
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

func (s *InventoryService) SyncContainerIngressPolicy(name string, namespace string, key InventoryKey) *InventoryKey {
	ingress := &v1.Ingress{}
	externalId := key.ExternalId
	if deleted := s.IsIngressDeleted(namespace, name, externalId, ingress); deleted {
		err := s.DeleteResource(externalId, ContainerIngressPolicy)
		if err != nil {
			log.Error(err, "Delete ContainerIngressPolicy Resource error", "key", key)
			return &key
		}
	} else if ingress.UID == types.UID(externalId) {
		retry := s.BuildIngress(ingress)
		if retry {
			return &key
		}
	} else {
		log.Error(errors.New("no ingress found"), "Unexpected error is found while processing Ingress", "key", key)
	}
	return nil
}

func (s *InventoryService) getIngressAppIds(ingress *v1.Ingress) []string {
	//collect service name from ingress defaultBackend and rule
	serviceSet := sets.Set[string]{}

	// Check defaultBackend
	if ingress.Spec.DefaultBackend != nil && ingress.Spec.DefaultBackend.Service != nil {
		serviceSet.Insert(ingress.Spec.DefaultBackend.Service.Name)
	}

	// Check rules
	for _, rule := range ingress.Spec.Rules {
		if rule.HTTP != nil {
			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service != nil {
					serviceSet.Insert(path.Backend.Service.Name)
				}
			}
		}
	}

	// Convert map keys to slice
	result := []string{}
	for serviceName := range serviceSet {
		service := &corev1.Service{}
		err := s.Client.Get(context.TODO(), types.NamespacedName{Name: serviceName, Namespace: ingress.Namespace}, service)
		if err != nil {
			log.Error(err, "Failed to get service", "service name", serviceName)
			continue
		}
		result = append(result, string(service.UID))
	}
	return result
}

func (s *InventoryService) IsIngressDeleted(namespace, name, externalId string, ingress *v1.Ingress) bool {
	if ingress == nil {
		ingress = &v1.Ingress{}
	}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, ingress)
	if apierrors.IsNotFound(err) ||
		((err == nil) && (string(ingress.UID) != externalId)) {
		return true
	} else {
		log.Error(err, "Check Ingress deleted", "Ingress", name, "Namespace", namespace, "External id", externalId)
		return false
	}
}

func (c *InventoryService) CleanStaleInventoryIngressPolicy() error {
	log.Info("Clean stale InventoryIngressPolicy")
	containerIngressPolicies := c.IngressPolicyStore.List()
	for _, ingressPolicy := range containerIngressPolicies {
		ingress := ingressPolicy.(*containerinventory.ContainerIngressPolicy)
		project := c.ProjectStore.GetByKey(ingress.ContainerProjectId)
		if project == nil {
			log.Info("Cannot find ContainerProject by id, so clean up stale InventoryIngressPolicy", "Project Id", ingress.ContainerProjectId,
				"Ingress name", ingress.DisplayName, "External Id", ingress.ExternalId)
			err := c.DeleteResource(ingress.ExternalId, ContainerIngressPolicy)
			if err != nil {
				log.Error(err, "Clean stale InventoryIngressPolicy", "External Id", ingress.ExternalId)
				return err
			}
		} else if c.IsIngressDeleted(project.(*containerinventory.ContainerProject).DisplayName, ingress.DisplayName, ingress.ExternalId, nil) {
			log.Info("Clean stale InventoryIngressPolicy", "Name", ingress.DisplayName, "External Id", ingress.ExternalId)
			err := c.DeleteResource(ingress.ExternalId, ContainerIngressPolicy)
			if err != nil {
				log.Error(err, "Clean stale InventoryIngressPolicy", "External Id", ingress.ExternalId)
				return err
			}
		}
	}
	return nil
}
