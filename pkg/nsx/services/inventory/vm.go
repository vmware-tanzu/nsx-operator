package inventory

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	nsxvmwarecomv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
)

const (
	vmAddTagsURL    = "api/v1/fabric/virtual-machines?action=add_tags"
	vmRemoveTagsURL = "api/v1/fabric/virtual-machines?action=remove_tags"
	vmSearchURL     = "api/v1/search/query"
)

type vmTagUpdate struct {
	ExternalID string  `json:"external_id"`
	Tags       []vmTag `json:"tags"`
}

type vmTag struct {
	Scope string `json:"scope"`
	Tag   string `json:"tag"`
}

// initTaggedVMs populates the taggedVMs store from NSX Inventory at startup.
// It uses the NSX search API to query only VMs that have the nsx-op/cluster-name tag,
// avoiding listing all fabric virtual machines.
func (s *InventoryService) initTaggedVMs() error {
	log.Info("Populating tagged VM store from NSX Inventory")
	tagScopeEscaped := strings.ReplaceAll(TagScopeClusterName, "/", "\\/")
	query := fmt.Sprintf("resource_type:VirtualMachine AND tags.scope:%s", tagScopeEscaped)
	cursor := ""
	for {
		searchURL := fmt.Sprintf("%s?query=%s", vmSearchURL, url.QueryEscape(query))
		if cursor != "" {
			searchURL = fmt.Sprintf("%s&cursor=%s", searchURL, cursor)
		}
		resp, err := s.NSXClient.Cluster.HttpGet(searchURL)
		if err != nil {
			return fmt.Errorf("failed to search tagged virtual machines: %w", err)
		}

		results, _ := resp["results"].([]interface{})
		for _, r := range results {
			vmMap, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			externalID, _ := vmMap["external_id"].(string)
			displayName, _ := vmMap["display_name"].(string)
			tags, _ := vmMap["tags"].([]interface{})
			for _, t := range tags {
				tagMap, ok := t.(map[string]interface{})
				if !ok {
					continue
				}
				scope, _ := tagMap["scope"].(string)
				if scope == TagScopeClusterName {
					tagValue, _ := tagMap["tag"].(string)
					s.taggedVMs[externalID] = tagValue
					log.Debug("Found previously tagged VM in NSX Inventory",
						"displayName", displayName, "externalID", externalID)
					break
				}
			}
		}

		nextCursor, _ := resp["cursor"].(string)
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	log.Info("Tagged VM store populated", "count", len(s.taggedVMs))
	return nil
}

// SyncVirtualMachineTag handles the tagging/untagging of VirtualMachine objects
// in NSX Inventory with the nsx-op/cluster-name tag.
func (s *InventoryService) SyncVirtualMachineTag(name, namespace string, key InventoryKey) *InventoryKey {
	vm := &vmv1alpha1.VirtualMachine{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, vm)
	if apierrors.IsNotFound(err) {
		delete(s.taggedVMs, key.ExternalId)
		log.Info("VirtualMachine not found, will be removed from NSX Inventory",
			"name", name, "namespace", namespace)
		return nil
	}
	if err != nil {
		log.Error(err, "Failed to get VirtualMachine, will retry", "name", name, "namespace", namespace)
		return &key
	}

	externalID := getVMExternalID(vm)
	if externalID == "" {
		log.Error(nil, "VM has no InstanceUUID, cannot process", "namespace", namespace, "vm", name)
		return nil
	}

	capiClusterName := vm.Labels[CAPIClusterNameLabel]
	nsxSA, err := s.findRealizedNSXServiceAccountForCluster(namespace, capiClusterName)
	if err != nil {
		log.Error(err, "Failed to look up NSXServiceAccount, will retry",
			"namespace", namespace, "cluster", capiClusterName)
		return &key
	}

	if nsxSA == nil || nsxSA.Status.Phase != nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized {
		existingTag, ok := s.taggedVMs[externalID]
		if !ok {
			return nil
		}
		if err := s.RemoveClusterNameTagFromVM(externalID, existingTag); err != nil {
			log.Error(err, "Failed to remove tag from VM, will retry",
				"namespace", namespace, "vm", name, "externalID", externalID)
			return &key
		}
		delete(s.taggedVMs, externalID)
		log.Info("Removed cluster-name tag from VM in NSX Inventory",
			"namespace", namespace, "vm", name)
		return nil
	}

	if _, ok := s.taggedVMs[externalID]; ok {
		return nil
	}

	clusterName := nsxSA.Status.ClusterName
	if clusterName == "" {
		log.Error(nil, "NSXServiceAccount has empty clusterName",
			"namespace", namespace, "vm", name, "serviceAccount", nsxSA.Name)
		return nil
	}

	if err := s.addClusterNameTagToVM(externalID, clusterName); err != nil {
		log.Error(err, "Failed to add cluster-name tag to VM, will retry",
			"namespace", namespace, "vm", name, "externalID", externalID)
		return &key
	}

	s.taggedVMs[externalID] = clusterName
	log.Info("Successfully tagged VM in NSX Inventory",
		"namespace", namespace, "vm", name, "clusterName", clusterName)
	return nil
}

// findRealizedNSXServiceAccountForCluster looks up a realized NSXServiceAccount
// in the given namespace whose OwnerReference Cluster matches the specified clusterName.
func (s *InventoryService) findRealizedNSXServiceAccountForCluster(namespace, clusterName string) (*nsxvmwarecomv1alpha1.NSXServiceAccount, error) {
	nsxSAList := &nsxvmwarecomv1alpha1.NSXServiceAccountList{}
	if err := s.Client.List(context.TODO(), nsxSAList, &client.ListOptions{
		Namespace: namespace,
	}); err != nil {
		return nil, fmt.Errorf("failed to list NSXServiceAccounts in namespace %s: %w", namespace, err)
	}

	for i := range nsxSAList.Items {
		sa := &nsxSAList.Items[i]
		if sa.DeletionTimestamp.IsZero() && sa.Status.Phase == nsxvmwarecomv1alpha1.NSXServiceAccountPhaseRealized && ownerClusterMatches(sa, clusterName) {
			return sa, nil
		}
	}
	return nil, nil
}

// ownerClusterMatches checks if the NSXServiceAccount has an OwnerReference
// to a CAPI Cluster with the given name.
func ownerClusterMatches(sa *nsxvmwarecomv1alpha1.NSXServiceAccount, clusterName string) bool {
	if clusterName == "" {
		return false
	}
	for _, ref := range sa.OwnerReferences {
		if ref.Kind == "Cluster" && strings.Contains(ref.APIVersion, "cluster.x-k8s.io") && ref.Name == clusterName {
			return true
		}
	}
	return false
}

// getVMExternalID extracts the instance UUID from a VirtualMachine CR.
// NSX Inventory uses the vSphere instanceUuid as external_id for VirtualMachine objects.
func getVMExternalID(vm *vmv1alpha1.VirtualMachine) string {
	return vm.Status.InstanceUUID
}

// addClusterNameTagToVM adds the nsx-op/cluster-name tag to the NSX Inventory
// VirtualMachine object. Uses the add_tags API to preserve any existing tags.
func (s *InventoryService) addClusterNameTagToVM(externalID, clusterName string) error {
	update := vmTagUpdate{
		ExternalID: externalID,
		Tags: []vmTag{
			{
				Scope: TagScopeClusterName,
				Tag:   clusterName,
			},
		},
	}

	_, err := s.NSXClient.Cluster.HttpPost(vmAddTagsURL, update)
	if err != nil {
		return fmt.Errorf("failed to add VM tags via NSX API: %w", err)
	}
	return nil
}

// RemoveClusterNameTagFromVM removes the nsx-op/cluster-name tag with the
// specified value from the NSX Inventory VirtualMachine object.
// The NSX remove_tags API requires both scope and tag value to match exactly.
func (s *InventoryService) RemoveClusterNameTagFromVM(externalID, tagValue string) error {
	update := vmTagUpdate{
		ExternalID: externalID,
		Tags: []vmTag{
			{
				Scope: TagScopeClusterName,
				Tag:   tagValue,
			},
		},
	}

	_, err := s.NSXClient.Cluster.HttpPost(vmRemoveTagsURL, update)
	if err != nil {
		return fmt.Errorf("failed to remove VM tag via NSX API: %w", err)
	}
	return nil
}
