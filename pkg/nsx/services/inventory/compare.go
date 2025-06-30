package inventory

import (
	"reflect"
	"strings"

	"github.com/vmware/go-vmware-nsxt/containerinventory"
)

func compareResources(pre interface{}, cur interface{}) map[string]interface{} {
	property := make(map[string]interface{})
	property["external_id"] = reflect.ValueOf(cur).FieldByName("ExternalId").String()
	property["resource_type"] = reflect.ValueOf(cur).FieldByName("ResourceType").String()
	switch property["resource_type"] {
	case string(ContainerProject):
		compareContainerProject(pre, cur, property)
	case string(ContainerApplicationInstance):
		compareContainerApplicationInstance(pre, cur, property)
	case string(ContainerApplication):
		compareContainerApplication(pre, cur, property)
	case string(ContainerIngressPolicy):
		compareContainerIngressPolicy(pre, cur, property)
	case string(ContainerClusterNode):
		compareContainerClusterNode(pre, cur, property)
	case string(ContainerNetworkPolicy):
		compareNetworkPolicy(pre, cur, property)
	}
	log.Debug("Compare resource", "property", property)
	return property
}

func compareContainerProject(pre interface{}, cur interface{}, property map[string]interface{}) {
	curProject := cur.(containerinventory.ContainerProject)
	preProject := containerinventory.ContainerProject{}
	if pre != nil {
		preProject = pre.(containerinventory.ContainerProject)
	}
	if pre == nil {
		property["display_name"] = curProject.DisplayName
		property["container_cluster_id"] = curProject.ContainerClusterId
	}
	if pre == nil || !reflect.DeepEqual(preProject.Tags, curProject.Tags) {
		property["tags"] = curProject.Tags
	}
	if pre == nil || !reflect.DeepEqual(preProject.NetworkStatus, curProject.NetworkStatus) {
		property["network_status"] = curProject.NetworkStatus
	}
	if pre == nil || !reflect.DeepEqual(preProject.NetworkErrors, curProject.NetworkErrors) {
		property["network_errors"] = curProject.NetworkErrors
	}
}

func compareContainerApplicationInstance(pre interface{}, cur interface{}, property map[string]interface{}) {
	preAppInstance := containerinventory.ContainerApplicationInstance{}
	if pre != nil {
		preAppInstance = pre.(containerinventory.ContainerApplicationInstance)
	}
	curAppInstance := cur.(containerinventory.ContainerApplicationInstance)
	// Handle the case when pre is nil, which means this is a new resource
	if pre == nil {
		property["display_name"] = curAppInstance.DisplayName
		property["container_cluster_id"] = curAppInstance.ContainerClusterId
		property["container_project_id"] = curAppInstance.ContainerProjectId
	}
	if pre == nil || !reflect.DeepEqual(preAppInstance.Tags, curAppInstance.Tags) {
		property["tags"] = curAppInstance.Tags
	}
	if pre == nil || preAppInstance.ClusterNodeId != curAppInstance.ClusterNodeId {
		property["cluster_node_id"] = curAppInstance.ClusterNodeId
	}
	if pre == nil || !reflect.DeepEqual(preAppInstance.ContainerApplicationIds, curAppInstance.ContainerApplicationIds) {
		property["container_application_ids"] = curAppInstance.ContainerApplicationIds
	}
	if pre == nil || preAppInstance.Status != curAppInstance.Status {
		property["status"] = curAppInstance.Status
	}
	if pre == nil || !reflect.DeepEqual(preAppInstance.NetworkStatus, curAppInstance.NetworkStatus) {
		property["network_status"] = curAppInstance.NetworkStatus
	}
	if pre == nil || !reflect.DeepEqual(preAppInstance.NetworkErrors, curAppInstance.NetworkErrors) {
		property["network_errors"] = curAppInstance.NetworkErrors
	}
	if pre == nil && len(curAppInstance.OriginProperties) == 1 {
		property["origin_properties"] = curAppInstance.OriginProperties
	} else if pre != nil && !reflect.DeepEqual(preAppInstance.OriginProperties, curAppInstance.OriginProperties) {
		if isIPChanged(preAppInstance, curAppInstance) {
			property["origin_properties"] = curAppInstance.OriginProperties
		}
	}
}

func compareContainerIngressPolicy(pre interface{}, cur interface{}, property map[string]interface{}) {
	preIngressPolicy := containerinventory.ContainerIngressPolicy{}
	if pre != nil {
		preIngressPolicy = pre.(containerinventory.ContainerIngressPolicy)
	}
	curIngressPolicy := cur.(containerinventory.ContainerIngressPolicy)
	if pre == nil {
		property["display_name"] = curIngressPolicy.DisplayName
		property["container_cluster_id"] = curIngressPolicy.ContainerClusterId
		property["container_project_id"] = curIngressPolicy.ContainerProjectId
	}
	if pre == nil || !reflect.DeepEqual(preIngressPolicy.Tags, curIngressPolicy.Tags) {
		property["tags"] = curIngressPolicy.Tags
	}
	if pre == nil || preIngressPolicy.Spec != curIngressPolicy.Spec {
		property["spec"] = curIngressPolicy.Spec
	}
	if pre == nil || !reflect.DeepEqual(preIngressPolicy.NetworkStatus, curIngressPolicy.NetworkStatus) {
		property["network_status"] = curIngressPolicy.NetworkStatus
	}
	if pre == nil || !reflect.DeepEqual(preIngressPolicy.NetworkErrors, curIngressPolicy.NetworkErrors) {
		property["network_errors"] = curIngressPolicy.NetworkErrors
	}
	if pre == nil || !reflect.DeepEqual(preIngressPolicy.ContainerApplicationIds, curIngressPolicy.ContainerApplicationIds) {
		property["container_application_ids"] = curIngressPolicy.ContainerApplicationIds
	}
}

func isIPChanged(pre containerinventory.ContainerApplicationInstance, cur containerinventory.ContainerApplicationInstance) bool {
	preIPs := ""
	curIPs := ""
	for _, prop := range pre.OriginProperties {
		if prop.Key == "ip" {
			preIPs = prop.Value
			log.Info("Pod previously has IP ", "pod", pre.DisplayName, "ips", preIPs)
		}
	}
	for _, prop := range cur.OriginProperties {
		if prop.Key == "ip" {
			curIPs = prop.Value
			log.Info("Pod now has IP", "pod", cur.DisplayName, "ips", curIPs)
		}
	}
	preIPArr := strings.Split(preIPs, ",")
	curIPArr := strings.Split(curIPs, ",")
	if len(preIPArr) != len(curIPArr) {
		return true
	} else if len(curIPArr) == 1 && preIPArr[0] != curIPArr[0] {
		return true
	} else if len(curIPArr) == 2 {
		if !((preIPArr[0] == curIPArr[0] && preIPArr[1] == curIPArr[1]) || (preIPArr[0] == curIPArr[1] && preIPArr[1] == curIPArr[0])) {
			return true
		}
	}
	return false
}

func compareContainerApplication(pre interface{}, cur interface{}, property map[string]interface{}) {
	preApplication := containerinventory.ContainerApplication{}
	if pre != nil {
		preApplication = pre.(containerinventory.ContainerApplication)
	}
	curApplication := cur.(containerinventory.ContainerApplication)
	if pre == nil {
		property["display_name"] = curApplication.DisplayName
		property["container_cluster_id"] = curApplication.ContainerClusterId
		property["container_project_id"] = curApplication.ContainerProjectId
	}
	if pre == nil || !reflect.DeepEqual(preApplication.Tags, curApplication.Tags) {
		property["tags"] = curApplication.Tags
	}
	if pre == nil || preApplication.Status != curApplication.Status {
		property["status"] = curApplication.Status
	}
	if pre == nil || preApplication.NetworkStatus != curApplication.NetworkStatus {
		property["network_status"] = curApplication.NetworkStatus
	}
	if pre == nil || !reflect.DeepEqual(preApplication.NetworkErrors, curApplication.NetworkErrors) {
		property["network_errors"] = curApplication.NetworkErrors
	}
	if pre == nil || !reflect.DeepEqual(preApplication.OriginProperties, curApplication.OriginProperties) {
		property["origin_properties"] = curApplication.OriginProperties
	}
}

func compareContainerClusterNode(pre interface{}, cur interface{}, property map[string]interface{}) {
	preClusterNode := containerinventory.ContainerClusterNode{}
	if pre != nil {
		preClusterNode = pre.(containerinventory.ContainerClusterNode)
	}
	curClusterNode := cur.(containerinventory.ContainerClusterNode)
	if pre == nil {
		property["display_name"] = curClusterNode.DisplayName
		property["container_cluster_id"] = curClusterNode.ContainerClusterId
	}
	if pre == nil || !reflect.DeepEqual(preClusterNode.Tags, curClusterNode.Tags) {
		property["tags"] = curClusterNode.Tags
	}
	if pre == nil || !reflect.DeepEqual(preClusterNode.IpAddresses, curClusterNode.IpAddresses) {
		property["ip_addresses"] = curClusterNode.IpAddresses
	}
	if pre == nil || !reflect.DeepEqual(preClusterNode.NetworkStatus, curClusterNode.NetworkStatus) {
		property["network_status"] = curClusterNode.NetworkStatus
	}
	if pre == nil || !reflect.DeepEqual(preClusterNode.NetworkErrors, curClusterNode.NetworkErrors) {
		property["network_errors"] = curClusterNode.NetworkErrors
	}
	if pre == nil || !reflect.DeepEqual(preClusterNode.OriginProperties, curClusterNode.OriginProperties) {
		property["origin_properties"] = curClusterNode.OriginProperties
	}
}

func compareNetworkPolicy(pre interface{}, cur interface{}, property map[string]interface{}) {
	preNetworkPolicy := containerinventory.ContainerNetworkPolicy{}
	if pre != nil {
		preNetworkPolicy = pre.(containerinventory.ContainerNetworkPolicy)
	}
	curNetworkPolicy := cur.(containerinventory.ContainerNetworkPolicy)
	if pre == nil {
		property["display_name"] = curNetworkPolicy.DisplayName
		property["container_cluster_id"] = curNetworkPolicy.ContainerClusterId
		property["container_project_id"] = curNetworkPolicy.ContainerProjectId
	}
	if pre == nil || !reflect.DeepEqual(preNetworkPolicy.Tags, curNetworkPolicy.Tags) {
		property["tags"] = cur.(containerinventory.ContainerNetworkPolicy).Tags
	}
	if pre == nil || preNetworkPolicy.Spec != curNetworkPolicy.Spec {
		property["spec"] = cur.(containerinventory.ContainerNetworkPolicy).Spec
	}
	if pre == nil || preNetworkPolicy.PolicyType != curNetworkPolicy.PolicyType {
		property["policy_type"] = cur.(containerinventory.ContainerNetworkPolicy).PolicyType
	}
	if pre == nil || !reflect.DeepEqual(preNetworkPolicy.NetworkStatus, curNetworkPolicy.NetworkStatus) {
		property["network_status"] = curNetworkPolicy.NetworkStatus
	}
	if pre == nil || !reflect.DeepEqual(preNetworkPolicy.NetworkErrors, curNetworkPolicy.NetworkErrors) {
		property["network_errors"] = curNetworkPolicy.NetworkErrors
	}
	if pre == nil || !reflect.DeepEqual(preNetworkPolicy.OriginProperties, curNetworkPolicy.OriginProperties) {
		property["origin_properties"] = curNetworkPolicy.OriginProperties
	}
}
