package inventory

import (
	"context"
	"crypto/sha1" // #nosec G505: not used for security purposes
	"fmt"
	"sort"

	"github.com/vmware/go-vmware-nsxt/common"
	"github.com/vmware/go-vmware-nsxt/containerinventory"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"

	ctrcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func (s *InventoryService) BuildPod(pod *corev1.Pod) (retry bool) {
	log.Info("Add Pod ", "Pod", pod.Name, "Namespace", pod.Namespace)
	retry = false
	// Calculate the services related to this Pod from pendingAdd or inventory store.
	var containerApplicationIds []string
	if s.pendingAdd[string(pod.UID)] != nil {
		containerApplicationInstance := s.pendingAdd[string(pod.UID)].(*containerinventory.ContainerApplicationInstance)
		containerApplicationIds = containerApplicationInstance.ContainerApplicationIds
	}

	preContainerApplicationInstance := s.ApplicationInstanceStore.GetByKey(string(pod.UID))
	if preContainerApplicationInstance != nil && (len(containerApplicationIds) == 0) {
		containerApplicationIds = preContainerApplicationInstance.(*containerinventory.ContainerApplicationInstance).ContainerApplicationIds
		preContainerApplicationInstance = *preContainerApplicationInstance.(*containerinventory.ContainerApplicationInstance)

	}
	namespace, err := s.GetNamespace(pod.Namespace)
	if err != nil {
		retry = true
		log.Error(err, "Failed to build Pod", "Pod", pod)
		return
	}
	node := &corev1.Node{}
	err = s.Client.Get(context.TODO(), types.NamespacedName{Name: pod.Spec.NodeName}, node)
	if err != nil {
		if pod.Spec.NodeName != "" {
			// retry when pod has Node but Node is missing in NodeInformer
			retry = true
		}
		log.Error(err, "Cannot find node for Pod", "Pod", pod.Name, "Namespace", pod.Namespace, "Node", pod.Spec.NodeName, "retry", retry)
	}

	status := InventoryStatusDown
	if pod.Status.Phase == corev1.PodRunning {
		status = InventoryStatusUp
	} else if pod.Status.Phase == corev1.PodUnknown {
		status = InventoryStatusUnknown
	}

	// Create network errors from pod conditions and status message
	var networkErrors []common.NetworkError
	networkStatus := NetworkStatusHealthy
	if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodSucceeded {
		networkStatus = NetworkStatusUnhealthy
		// Check for a message in pod status
		if pod.Status.Message != "" {
			networkErrors = append(networkErrors, common.NetworkError{
				ErrorMessage: pod.Status.Message,
			})
		}
		// Check conditions
		for _, condition := range pod.Status.Conditions {
			// Only add error messages from conditions that indicate a problem
			// Skip success messages like "Pod has been successfully created/updated"
			if condition.Message != "" && condition.Status != corev1.ConditionTrue {
				networkErrors = append(networkErrors, common.NetworkError{
					ErrorMessage: condition.Message,
				})
			}
		}
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
		NetworkErrors:           networkErrors,
		NetworkStatus:           networkStatus,
		OriginProperties:        originProperties,
		Status:                  status,
	}
	log.V(1).Info("Build pod", "current instance", containerApplicationInstance, "pre instance", preContainerApplicationInstance)
	operation, _ := s.compareAndMergeUpdate(preContainerApplicationInstance, containerApplicationInstance)
	if operation != operationNone {
		s.pendingAdd[containerApplicationInstance.ExternalId] = &containerApplicationInstance
	}
	return
}

func (s *InventoryService) GetNamespace(namespace string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: namespace}, ns)
	if err != nil {
		log.Error(err, "Failed to find namespace", namespace)
		return nil, err
	}
	return ns, nil
}

func (s *InventoryService) BuildIngress(ingress *networkingv1.Ingress) (retry bool) {
	log.V(1).Info("Add Ingress", "Name", ingress.Name, "Namespace", ingress.Namespace)
	namespace, err := s.GetNamespace(ingress.Namespace)
	retry = true
	if err != nil {
		log.Error(err, "Cannot find namespace for Ingress", "Ingress", ingress)
		return
	}
	spec, err := yaml.Marshal(ingress.Spec)
	if err != nil {
		log.Error(err, "Failed to dump spec for ingress", "Ingress", ingress)
		return
	}

	preIngress := s.IngressPolicyStore.GetByKey(string(ingress.UID))
	if preIngress != nil {
		preIngress = *preIngress.(*containerinventory.ContainerIngressPolicy)
	}

	// Get network errors from ingress annotations
	var networkErrors []common.NetworkError
	networkStatus := NetworkStatusHealthy
	for key, value := range ingress.Annotations {
		if key == NcpLbError {
			networkStatus = NetworkStatusUnhealthy
			networkErrors = append(networkErrors, common.NetworkError{
				ErrorMessage: key + ":" + value,
			})
		}
	}

	containerIngress := containerinventory.ContainerIngressPolicy{
		DisplayName:             ingress.Name,
		ResourceType:            string(ContainerIngressPolicy),
		Tags:                    GetTagsFromLabels(ingress.Labels),
		ContainerApplicationIds: nil,
		ContainerClusterId:      s.NSXConfig.Cluster,
		ContainerProjectId:      string(namespace.UID),
		ExternalId:              string(ingress.UID),
		NetworkErrors:           networkErrors,
		NetworkStatus:           networkStatus,
		OriginProperties:        nil,
		Spec:                    string(spec),
	}
	appids := s.getIngressAppIds(ingress)
	if len(appids) > 0 {
		containerIngress.ContainerApplicationIds = appids
	}
	log.V(1).Info("Build ingress", "current instance", containerIngress, "pre instance", preIngress)
	operation, _ := s.compareAndMergeUpdate(preIngress, containerIngress)
	if operation != operationNone {
		s.pendingAdd[containerIngress.ExternalId] = &containerIngress
	}
	retry = false
	return
}

func (s *InventoryService) BuildInventoryCluster() containerinventory.ContainerCluster {
	scope := containerinventory.DiscoveredResourceScope{
		ScopeId:   s.NSXConfig.Cluster,
		ScopeType: "CONTAINER_CLUSTER"}

	clusterType := InventoryClusterTypeSupervisor
	clusterName := s.NSXConfig.Cluster
	var networkErrors []common.NetworkError
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
		CniType:        InventoryClusterCNIType,
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
		log.V(1).Info("Inventory compare", "updated properties", updateProperties)
		return operationUpdate, updateProperties
	} else {
		return operationNone, nil
	}
}

func (s *InventoryService) BuildNamespace(namespace *corev1.Namespace) (retry bool) {
	log.Info("Building Namespace", "Namespace", namespace.Name)
	retry = false

	preContainerProject := s.ProjectStore.GetByKey(string(namespace.UID))
	if preContainerProject != nil {
		preContainerProject = *preContainerProject.(*containerinventory.ContainerProject)
	}

	// Extract network errors from annotations
	var networkErrors []common.NetworkError
	networkStatus := NetworkStatusHealthy

	// Check for VPC error annotation
	if errorMsg, exists := namespace.Annotations[ctrcommon.AnnotationNamespaceVPCError]; exists && errorMsg != "" {
		networkErrors = append(networkErrors, common.NetworkError{
			ErrorMessage: errorMsg,
		})
		networkStatus = NetworkStatusUnhealthy
	}

	containerProject := containerinventory.ContainerProject{
		DisplayName:        namespace.Name,
		ResourceType:       string(ContainerProject),
		Tags:               GetTagsFromLabels(namespace.Labels),
		ContainerClusterId: s.NSXConfig.Cluster,
		ExternalId:         string(namespace.UID),
		NetworkErrors:      networkErrors,
		NetworkStatus:      networkStatus,
	}

	operation, _ := s.compareAndMergeUpdate(preContainerProject, containerProject)
	if operation != operationNone {
		s.pendingAdd[containerProject.ExternalId] = &containerProject
	} else {
		log.Info("Skip, namespace not updated", "Namespace", namespace.Name)
	}
	return
}

func (s *InventoryService) BuildService(service *corev1.Service) (retry bool) {
	log.Info("Building Service", "Service", service.Name, "Namespace", service.Namespace)
	retry = false

	preContainerApplication := s.ApplicationStore.GetByKey(string(service.UID))
	if preContainerApplication != nil {
		preContainerApplication = *preContainerApplication.(*containerinventory.ContainerApplication)
	}

	namespace := &corev1.Namespace{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: service.Namespace}, namespace)
	if err != nil {
		retry = true
		log.Error(err, "Failed to get namespace for Service", "Service", service)
		return
	}

	var networkErrors []common.NetworkError

	// Get pods from the endpoint
	netStatus := NetworkStatusHealthy
	status := InventoryStatusUp
	podIDs, hasAddr := GetPodIDsFromEndpoint(context.TODO(), s.Client, service.Name, service.Namespace)

	// Check if a service has a selector - services without selectors are valid
	hasSelector := len(service.Spec.Selector) > 0

	if len(podIDs) > 0 {
		status = InventoryStatusUp
	} else if hasAddr {
		status = InventoryStatusUnknown
		networkErrors = append(networkErrors, common.NetworkError{
			ErrorMessage: "Service endpoint status is unknown",
		})
	} else if hasSelector {
		// Only mark as down if service has selector but no endpoints
		status = InventoryStatusDown
		netStatus = NetworkStatusUnhealthy
		networkErrors = append(networkErrors, common.NetworkError{
			ErrorMessage: "Failed to get endpoints for Service",
		})
	} else {
		// Service without a selector is valid even without endpoints
		status = InventoryStatusUp
	}

	// Get network errors from service annotations
	if status != InventoryStatusUp {
		// Check for NCP errors in service annotations
		for key, value := range service.Annotations {
			if util.Contains(ServiceNCPErrors, key) {
				networkErrors = append(networkErrors, common.NetworkError{
					ErrorMessage: key + ":" + value,
				})
			}
		}
	}

	// Update the Pods' service IDs, which are related to this service
	retry = s.synchronizeServiceIDsWithApplicationInstances(podIDs, service)

	serviceType := "ClusterIP"
	if string(service.Spec.Type) != "" {
		serviceType = string(service.Spec.Type)
	}
	originProperties := []common.KeyValuePair{
		{
			Key:   "type",
			Value: serviceType,
		},
	}
	if (service.Spec.ClusterIP != "") && (service.Spec.ClusterIP != "None") {
		originProperties = append(originProperties, common.KeyValuePair{
			Key:   "ip",
			Value: service.Spec.ClusterIP,
		})
	}

	containerApplication := containerinventory.ContainerApplication{
		DisplayName:        service.Name,
		ResourceType:       string(ContainerApplication),
		Tags:               GetTagsFromLabels(service.Labels),
		ContainerClusterId: s.NSXConfig.Cluster,
		ContainerProjectId: string(namespace.UID),
		ExternalId:         string(service.UID),
		NetworkErrors:      networkErrors,
		NetworkStatus:      netStatus,
		OriginProperties:   originProperties,
		Status:             status,
	}

	log.V(1).Info("Build service", "current application", containerApplication, "pre application", preContainerApplication)
	operation, _ := s.compareAndMergeUpdate(preContainerApplication, containerApplication)
	if operation != operationNone {
		s.pendingAdd[containerApplication.ExternalId] = &containerApplication
	} else {
		log.Info("Skip, service not updated", "Service", service.Name, "Namespace", service.Namespace)
	}
	return
}

func (s *InventoryService) synchronizeServiceIDsWithApplicationInstances(podUIDs []string, service *corev1.Service) (retry bool) {
	for _, podUID := range podUIDs {
		if s.updateServiceIDsForApplicationInstance(podUID, service) {
			log.Info("Endpoint creation is before pod creation, retry service to establish correlation", "Pod", podUID, "Service", service.Name)
			return true
		}
	}
	s.removeStaleServiceIDsFromApplicationInstances(podUIDs, service)
	return false
}

func (s *InventoryService) applyServiceIDUpdates(instance *containerinventory.ContainerApplicationInstance, serviceUIDs []string) {
	instance.ContainerApplicationIds = serviceUIDs
	diff := map[string]interface{}{
		"external_id":               instance.ExternalId,
		"resource_type":             string(ContainerApplicationInstance),
		"container_application_ids": serviceUIDs,
	}
	s.requestBuffer = append(s.requestBuffer, containerinventory.ContainerInventoryObject{ContainerObject: diff, ObjectUpdateType: operationUpdate})
	s.pendingAdd[instance.ExternalId] = instance
}

func (s *InventoryService) updateServiceIDsForApplicationInstance(podUID string, service *corev1.Service) (retry bool) {
	applicationInstance := s.ApplicationInstanceStore.GetByKey(podUID)
	if applicationInstance == nil {
		return true
	}

	// Prefer the pendingAdd instance if available
	if s.pendingAdd[podUID] != nil {
		applicationInstance = s.pendingAdd[podUID]
	}

	ctx := context.TODO()
	pod, err := GetPodByUID(ctx, s.Client, types.UID(podUID), service.Namespace)
	if err != nil {
		log.Error(err, "Failed to get Pod by UID", "PodUID", podUID, "Namespace", service.Namespace)
		return true
	}
	serviceUIDs, err := GetServicesUIDByPodUID(ctx, s.Client, pod.UID, pod.Namespace)
	if err != nil {
		log.Error(err, "Failed to get services UIDs by pod UID", "Pod UID", pod.UID, "Namespace", pod.Namespace)
		return true
	}

	updatedInstance := applicationInstance.(*containerinventory.ContainerApplicationInstance)
	s.applyServiceIDUpdates(updatedInstance, serviceUIDs)
	return false
}

func (s *InventoryService) removeStaleServiceIDsFromApplicationInstances(podUIDs []string, service *corev1.Service) {
	allInstances := s.ApplicationInstanceStore.List()
	for _, instObj := range allInstances {
		inst := instObj.(*containerinventory.ContainerApplicationInstance)
		if util.Contains(podUIDs, inst.ExternalId) {
			continue
		}
		// Remove any ContainerApplicationIds that are not in the new serviceUIDs
		if !util.Contains(inst.ContainerApplicationIds, string(service.UID)) {
			continue
		}
		// Filter out the service UID from the list
		newIds := util.FilterOut(inst.ContainerApplicationIds, string(service.UID))
		s.applyServiceIDUpdates(inst, newIds)
	}
}

func (s *InventoryService) BuildNode(node *corev1.Node) (retry bool) {
	log.Info("Building Node", "Node", node.Name)
	retry = false

	preContainerClusterNode := s.ClusterNodeStore.GetByKey(string(node.UID))
	if preContainerClusterNode != nil {
		preContainerClusterNode = *preContainerClusterNode.(*containerinventory.ContainerClusterNode)
	}

	// Extract node IP addresses
	var ipAddresses []string
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP || addr.Type == corev1.NodeExternalIP {
			ipAddresses = append(ipAddresses, addr.Address)
		}
	}

	// Determine node status
	status := NetworkStatusHealthy
	if !isNodeReady(node) {
		status = NetworkStatusUnhealthy
	}

	// Create network errors from node conditions
	var networkErrors []common.NetworkError
	if !isNodeReady(node) {
		for _, condition := range node.Status.Conditions {
			networkErrors = append(networkErrors, common.NetworkError{
				ErrorMessage: condition.Message,
			})
		}
		// Sort network errors by lastTransitionTime in descending order
		sort.Slice(networkErrors, func(i, j int) bool {
			return node.Status.Conditions[i].LastTransitionTime.Time.After(node.Status.Conditions[j].LastTransitionTime.Time)
		})
	}

	// Create origin properties
	var originProperties []common.KeyValuePair
	if node.Status.NodeInfo.KubeletVersion != "" {
		originProperties = append(originProperties, common.KeyValuePair{
			Key:   "kubelet_version",
			Value: node.Status.NodeInfo.KubeletVersion,
		})
	}
	if node.Status.NodeInfo.OSImage != "" {
		originProperties = append(originProperties, common.KeyValuePair{
			Key:   "os_image",
			Value: node.Status.NodeInfo.OSImage,
		})
	}

	containerClusterNode := containerinventory.ContainerClusterNode{
		DisplayName:        node.Name,
		ResourceType:       string(ContainerClusterNode),
		Tags:               GetTagsFromLabels(node.Labels),
		ContainerClusterId: s.NSXConfig.Cluster,
		ExternalId:         string(node.UID),
		IpAddresses:        ipAddresses,
		NetworkErrors:      networkErrors,
		NetworkStatus:      status,
		OriginProperties:   originProperties,
	}

	log.V(1).Info("Build node", "current instance", containerClusterNode, "pre instance", preContainerClusterNode)

	operation, _ := s.compareAndMergeUpdate(preContainerClusterNode, containerClusterNode)
	if operation != operationNone {
		s.pendingAdd[containerClusterNode.ExternalId] = &containerClusterNode
	} else {
		log.Info("Skip, node not updated", "Node", node.Name)
	}
	return
}

// isNodeReady returns true if a node is ready; false otherwise.
func isNodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func (s *InventoryService) BuildNetworkPolicy(networkPolicy *networkingv1.NetworkPolicy) (retry bool) {
	log.Info("Building NetworkPolicy", "NetworkPolicy", networkPolicy.Name, "Namespace", networkPolicy.Namespace)
	retry = false

	preContainerNetworkPolicy := s.NetworkPolicyStore.GetByKey(string(networkPolicy.UID))
	if preContainerNetworkPolicy != nil {
		preContainerNetworkPolicy = *preContainerNetworkPolicy.(*containerinventory.ContainerNetworkPolicy)
	}

	namespace := &corev1.Namespace{}
	err := s.Client.Get(context.TODO(), types.NamespacedName{Name: networkPolicy.Namespace}, namespace)
	if err != nil {
		retry = true
		log.Error(err, "Failed to get namespace for NetworkPolicy", "NetworkPolicy", networkPolicy)
		return
	}

	spec, err := yaml.Marshal(networkPolicy.Spec)
	if err != nil {
		log.Error(err, "Failed to dump spec for NetworkPolicy", "NetworkPolicy", networkPolicy)
		return
	}

	// Get network errors from network policy annotations
	networkStatus := NetworkStatusHealthy
	var networkErrors []common.NetworkError
	for key, value := range networkPolicy.Annotations {
		if key == ctrcommon.NSXOperatorError {
			networkStatus = NetworkStatusUnhealthy
			networkErrors = append(networkErrors, common.NetworkError{
				ErrorMessage: key + ":" + value,
			})
		}
	}

	containerNetworkPolicy := containerinventory.ContainerNetworkPolicy{
		DisplayName:        networkPolicy.Name,
		ResourceType:       string(ContainerNetworkPolicy),
		Tags:               GetTagsFromLabels(networkPolicy.Labels),
		ContainerClusterId: s.NSXConfig.Cluster,
		ContainerProjectId: string(namespace.UID),
		ExternalId:         string(networkPolicy.UID),
		NetworkErrors:      networkErrors,
		NetworkStatus:      networkStatus,
		PolicyType:         NetworkPolicyType,
		Spec:               string(spec),
	}

	operation, _ := s.compareAndMergeUpdate(preContainerNetworkPolicy, containerNetworkPolicy)
	if operation != operationNone {
		s.pendingAdd[containerNetworkPolicy.ExternalId] = &containerNetworkPolicy
	} else {
		log.Info("Skip, network policy not updated", "NetworkPolicy", networkPolicy.Name)
	}
	return
}
