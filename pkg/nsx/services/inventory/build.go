package inventory

import (
	"context"
	"crypto/sha1" // #nosec G505: not used for security purposes
	"errors"
	"fmt"
	"sort"

	"github.com/vmware/go-vmware-nsxt/common"
	"github.com/vmware/go-vmware-nsxt/containerinventory"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (s *InventoryService) BuildPod(pod *corev1.Pod) (retry bool) {
	log.Info("Add Pod ", "Pod", pod.Name, "Namespace", pod.Namespace)
	retry = false
	// Calculate the services related to this Pod from pendingAdd or inventory store.
	containerApplicationIds := []string{}
	if s.pendingAdd[string(pod.UID)] != nil {
		containerApplicationInstance := s.pendingAdd[string(pod.UID)].(containerinventory.ContainerApplicationInstance)
		containerApplicationIds = containerApplicationInstance.ContainerApplicationIds
	}
	object := s.applicationInstanceStore.GetByKey(string(pod.UID))
	var preContainerApplicationInstance containerinventory.ContainerApplicationInstance
	if object != nil && (len(containerApplicationIds) == 0) {
		preContainerApplicationInstance = *object.(*containerinventory.ContainerApplicationInstance)
		containerApplicationIds = preContainerApplicationInstance.ContainerApplicationIds
	}
	namespaceName := pod.Namespace
	namespace := &corev1.Namespace{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: namespaceName}, namespace)
	if err != nil {
		retry = true
		log.Error(errors.New("Not find namespace for Pod"), "Failed to build Pod", "Pod", pod)
		return
	}

	node := &corev1.Node{}
	err = s.Client.Get(context.TODO(), types.NamespacedName{Name: pod.Spec.NodeName}, node)
	if err != nil {
		if pod.Spec.NodeName != "" {
			// retry when pod has Node but Node is missing in NodeInformer
			retry = true
		}
		log.Error(err, "Cannot find node for Pod", "Pod", pod, "retry", retry)
		return
	}
	status := InventoryStatusDown
	if pod.Status.Phase == corev1.PodRunning {
		status = InventoryStatusUp
	} else if pod.Status.Phase == corev1.PodUnknown {
		status = InventoryStatusUnknown
	}

	ips := ""
	if len(pod.Status.PodIPs) == 1 {
		ips = pod.Status.PodIPs[0].IP
	} else if len(pod.Status.PodIPs) == 2 {
		ips = pod.Status.PodIPs[0].IP + "," + pod.Status.PodIPs[1].IP
	} else {
		log.Info("Unexpected Pod IPs found", "Pod ips", pod.Status.PodIPs)
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
		DisplayName:             pod.Name,
		ResourceType:            string(ContainerApplicationInstance),
		Tags:                    GetTagsFromLabels(pod.Labels),
		ClusterNodeId:           string(node.UID),
		ContainerApplicationIds: containerApplicationIds,
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
		s.pendingAdd[containerApplicationInstance.ExternalId] = &containerApplicationInstance
	}
	return
}

func (s *InventoryService) BuildInventoryCluster() containerinventory.ContainerCluster {
	scope := containerinventory.DiscoveredResourceScope{
		ScopeId:   s.NSXConfig.Cluster,
		ScopeType: "CONTAINER_CLUSTER"}

	clusterType := InventoryClusterTypeWCP
	clusterName := s.NSXConfig.Cluster
	networkErrors := []common.NetworkError{}
	infra := &containerinventory.ContainerInfrastructureInfo{}
	infra.InfraType = InventoryInfraTypeVsphere
	newContainerCluster := containerinventory.ContainerCluster{
		DisplayName:    clusterName,
		ResourceType:   string(ContainerCluster),
		Scope:          []containerinventory.DiscoveredResourceScope{scope},
		ClusterType:    clusterType,
		ExternalId:     s.NSXConfig.Cluster,
		NetworkErrors:  networkErrors,
		NetworkStatus:  NetworkStatusHealthy,
		Infrastructure: infra,
		// report nsx-operator version
	}
	return newContainerCluster
}

func GetTagsFromLabels(labels map[string]string) []common.Tag {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	tags := make([]common.Tag, 0)
	maxKeyNum := len(keys)
	if maxKeyNum > InventoryMaxDisTags {
		maxKeyNum = InventoryMaxDisTags
	}
	for _, sortKey := range keys[:maxKeyNum] {
		scope := InventoryK8sPrefix + normalize(sortKey, MaxResourceTypeLen-len(InventoryK8sPrefix))
		maxTagLen := len(labels[sortKey])
		if maxTagLen > MaxTagLen {
			maxTagLen = MaxTagLen
		}
		tags = append(tags, common.Tag{
			Scope: scope,
			Tag:   labels[sortKey][:maxTagLen],
		})
	}
	return tags
}

func normalize(name string, maxLength int) string {
	if len(name) <= maxLength {
		return name
	}
	// #nosec G401: not used for security purposes
	hashId := sha1.Sum([]byte(name))
	nameLength := maxLength - 9
	newname := fmt.Sprintf("%s-%s", name[:nameLength], hashId[:8])
	log.Info("Name exceeds max length of supported by NSX. Truncate name to newname",
		"maxLength", maxLength, "name", name, "newname", newname)
	return newname
}

func (s *InventoryService) compareAndMergeUpdate(pre interface{}, cur interface{}) (string, map[string]interface{}) {
	updateProperties := compareResources(pre, cur)
	if pre == nil {
		s.requestBuffer = append(s.requestBuffer, containerinventory.ContainerInventoryObject{ContainerObject: updateProperties, ObjectUpdateType: operationCreate})
		return operationCreate, updateProperties
	} else if len(updateProperties) > 2 {
		s.requestBuffer = append(s.requestBuffer, containerinventory.ContainerInventoryObject{ContainerObject: updateProperties, ObjectUpdateType: operationUpdate})
		return operationUpdate, updateProperties
	} else {
		return operationNone, nil
	}
}
