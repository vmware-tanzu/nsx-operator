package inventory

import (
	"context"
	"errors"

	"github.com/vmware/go-vmware-nsxt/common"
	"github.com/vmware/go-vmware-nsxt/containerinventory"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (s *InventoryService) BuildPod(pod *corev1.Pod) (retry bool) {
	log.Info("Add pod ", "Pod", pod.Name, "namespace", pod.Namespace)
	retry = false
	// Calculate the services related to this Pod from pending_add or inventory store.
	container_application_ids := []string{}
	if s.pending_add[string(pod.UID)] != nil {
		containerApplicationInstance := s.pending_add[string(pod.UID)].(containerinventory.ContainerApplicationInstance)
		container_application_ids = containerApplicationInstance.ContainerApplicationIds
	}
	preContainerApplicationInstance := s.applicationInstanceStore.GetByKey(string(pod.UID))
	if preContainerApplicationInstance != nil && (len(container_application_ids) == 0) {
		container_application_ids = preContainerApplicationInstance.(containerinventory.ContainerApplicationInstance).ContainerApplicationIds
	}
	namespaceName := pod.Namespace
	namespace := &corev1.Namespace{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: namespaceName}, namespace)
	if err != nil {
		retry = true
		log.Error(errors.New("Cannot find namespace for Pod"), "Cannot find namespace for Pod", pod)
		return
	}

	node := &corev1.Node{}
	err = s.Client.Get(context.TODO(), types.NamespacedName{Name: pod.Spec.NodeName}, node)
	if err != nil {
		if pod.Spec.NodeName != "" {
			// retry when pod has Node but Node is missing in NodeInformer
			retry = true
		}
		log.Error(err, "Cannot find node for Pod", "pod", pod, "retry", retry)
		return
	}
	status := InventoryStatusDown
	if pod.Status.Phase == corev1.PodRunning {
		status = InventoryStausUp
	} else if pod.Status.Phase == corev1.PodUnknown {
		status = InventoryStatusUnknown
	}

	ips := ""
	if len(pod.Status.PodIPs) == 1 {
		ips = pod.Status.PodIPs[0].IP
	} else if len(pod.Status.PodIPs) == 2 {
		ips = pod.Status.PodIPs[0].IP + "," + pod.Status.PodIPs[1].IP
	} else {
		log.Info("Unexpected pod IPs found", "pod ips", pod.Status.PodIPs)
	}
	var originProperties []common.KeyValuePair
	if ips == "" {
		originProperties = nil
	} else {
		originProperties = []common.KeyValuePair{
			{
				Key:   "ip",
				Value: ips,
			},
		}
	}

	containerApplicationInstance := containerinventory.ContainerApplicationInstance{
		DisplayName:  pod.Name,
		ResourceType: string(ContainerApplicationInstance),
		// TODO: get tags from pod.Labels
		Tags:                    []common.Tag{},
		ClusterNodeId:           string(node.UID),
		ContainerApplicationIds: container_application_ids,
		ContainerClusterId:      s.NSXConfig.Cluster,
		ContainerProjectId:      string(namespace.UID),
		ExternalId:              string(pod.UID),
		NetworkErrors:           nil,
		NetworkStatus:           "",
		OriginProperties:        originProperties,
		Status:                  status,
	}

	operation, _ := s.compareAndMergeUpdate(preContainerApplicationInstance, containerApplicationInstance)
	if operation != operationNone {
		s.pending_add[containerApplicationInstance.ExternalId] = containerApplicationInstance
	}
	return
}

func (s *InventoryService) BuildInentoryCluster() containerinventory.ContainerCluster {
	scope := containerinventory.DiscoveredResourceScope{
		ScopeId:   s.NSXConfig.Cluster,
		ScopeType: "CONTAINER_CLUSTER"}

	clusterType := INVENTORY_CLUSTER_TYPE_WCP
	clusterName := s.NSXConfig.Cluster
	networkErrors := []common.NetworkError{}
	infra := &containerinventory.ContainerInfrastructureInfo{}
	infra.InfraType = INVENTORY_INFRA_TYPE_VSPHERE
	newContainerCluster := containerinventory.ContainerCluster{
		DisplayName:    clusterName,
		ResourceType:   string(ContainerCluster),
		Scope:          []containerinventory.DiscoveredResourceScope{scope},
		ClusterType:    clusterType,
		ExternalId:     s.NSXConfig.Cluster,
		NetworkErrors:  networkErrors,
		NetworkStatus:  NETWORK_STATUS_HEALTHY,
		Infrastructure: infra,
		// report nsx-operator version
	}
	return newContainerCluster
}
