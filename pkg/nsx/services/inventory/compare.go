package inventory

import (
	"reflect"
	"strings"

	"github.com/vmware/go-vmware-nsxt/containerinventory"
)

func compareResources(pre interface{}, cur interface{}) map[string]interface{} {
	updateProperties := make(map[string]interface{})
	updateProperties["external_id"] = reflect.ValueOf(cur).FieldByName("ExternalId").String()
	updateProperties["resource_type"] = reflect.ValueOf(cur).FieldByName("ResourceType").String()
	switch updateProperties["resource_type"] {
	case string(ContainerProject):
		compareContainerProject(pre, cur, &updateProperties)
	case string(ContainerApplicationInstance):
		compareContainerApplicationInstance(pre, cur, &updateProperties)
	case string(ContainerApplication):
		compareContainerApplication(pre, cur, &updateProperties)
	case string(ContainerIngressPolicy):
		compareContainerIngressPolicy(pre, cur, &updateProperties)
	}
	log.Info("Compare resource", "updateProperties", updateProperties)
	return updateProperties
}

func compareContainerProject(pre interface{}, cur interface{}, property *map[string]interface{}) {
	updateProperties := *property
	if pre == nil {
		updateProperties["display_name"] = cur.(containerinventory.ContainerProject).DisplayName
		updateProperties["container_cluster_id"] = cur.(containerinventory.ContainerProject).ContainerClusterId
	}
	if pre == nil || !reflect.DeepEqual(pre.(containerinventory.ContainerProject).Tags, cur.(containerinventory.ContainerProject).Tags) {
		updateProperties["tags"] = cur.(containerinventory.ContainerProject).Tags
	}
}

func compareContainerApplicationInstance(pre interface{}, cur interface{}, property *map[string]interface{}) {
	updateProperties := *property
	if pre == nil {
		updateProperties["display_name"] = cur.(containerinventory.ContainerApplicationInstance).DisplayName
		updateProperties["container_cluster_id"] = cur.(containerinventory.ContainerApplicationInstance).ContainerClusterId
		updateProperties["container_project_id"] = cur.(containerinventory.ContainerApplicationInstance).ContainerProjectId
	}
	if pre == nil || !reflect.DeepEqual(pre.(containerinventory.ContainerApplicationInstance).Tags, cur.(containerinventory.ContainerApplicationInstance).Tags) {
		updateProperties["tags"] = cur.(containerinventory.ContainerApplicationInstance).Tags
	}
	if pre == nil || pre.(containerinventory.ContainerApplicationInstance).ClusterNodeId != cur.(containerinventory.ContainerApplicationInstance).ClusterNodeId {
		updateProperties["cluster_node_id"] = cur.(containerinventory.ContainerApplicationInstance).ClusterNodeId
	}
	if pre == nil || !reflect.DeepEqual(pre.(containerinventory.ContainerApplicationInstance).ContainerApplicationIds, cur.(containerinventory.ContainerApplicationInstance).ContainerApplicationIds) {
		updateProperties["container_application_ids"] = cur.(containerinventory.ContainerApplicationInstance).ContainerApplicationIds
	}
	if pre == nil || pre.(containerinventory.ContainerApplicationInstance).Status != cur.(containerinventory.ContainerApplicationInstance).Status {
		updateProperties["status"] = cur.(containerinventory.ContainerApplicationInstance).Status
	}
	if pre == nil && len(cur.(containerinventory.ContainerApplicationInstance).OriginProperties) == 1 {
		updateProperties["origin_properties"] = cur.(containerinventory.ContainerApplicationInstance).OriginProperties
	} else if pre != nil && !reflect.DeepEqual(pre.(containerinventory.ContainerApplicationInstance).OriginProperties, cur.(containerinventory.ContainerApplicationInstance).OriginProperties) {
		if isIPChanged(pre.(containerinventory.ContainerApplicationInstance), cur.(containerinventory.ContainerApplicationInstance)) {
			updateProperties["origin_properties"] = cur.(containerinventory.ContainerApplicationInstance).OriginProperties
		}
	}
}

func compareContainerIngressPolicy(pre interface{}, cur interface{}, property *map[string]interface{}) {
	updateProperties := *property
	if pre == nil {
		updateProperties["display_name"] = cur.(containerinventory.ContainerIngressPolicy).DisplayName
		updateProperties["container_cluster_id"] = cur.(containerinventory.ContainerIngressPolicy).ContainerClusterId
		updateProperties["container_project_id"] = cur.(containerinventory.ContainerIngressPolicy).ContainerProjectId
	}
	if pre == nil || !reflect.DeepEqual(pre.(containerinventory.ContainerIngressPolicy).Tags, cur.(containerinventory.ContainerIngressPolicy).Tags) {
		updateProperties["tags"] = cur.(containerinventory.ContainerIngressPolicy).Tags
	}
	if pre == nil || pre.(containerinventory.ContainerIngressPolicy).Spec != cur.(containerinventory.ContainerIngressPolicy).Spec {
		updateProperties["spec"] = cur.(containerinventory.ContainerIngressPolicy).Spec
	}

	if pre == nil || !reflect.DeepEqual(pre.(containerinventory.ContainerIngressPolicy).ContainerApplicationIds, cur.(containerinventory.ContainerIngressPolicy).ContainerApplicationIds) {
		updateProperties["container_application_ids"] = cur.(containerinventory.ContainerIngressPolicy).ContainerApplicationIds
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

func compareContainerApplication(pre interface{}, cur interface{}, property *map[string]interface{}) {
	updateProperties := *property
	if pre == nil {
		updateProperties["display_name"] = cur.(containerinventory.ContainerApplication).DisplayName
		updateProperties["container_cluster_id"] = cur.(containerinventory.ContainerApplication).ContainerClusterId
		updateProperties["container_project_id"] = cur.(containerinventory.ContainerApplication).ContainerProjectId
	}
	if pre == nil || !reflect.DeepEqual(pre.(containerinventory.ContainerApplication).Tags, cur.(containerinventory.ContainerApplication).Tags) {
		updateProperties["tags"] = cur.(containerinventory.ContainerApplication).Tags
	}
	if pre == nil || pre.(containerinventory.ContainerApplication).Status != cur.(containerinventory.ContainerApplication).Status {
		updateProperties["status"] = cur.(containerinventory.ContainerApplication).Status
	}
	if pre == nil || !reflect.DeepEqual(pre.(containerinventory.ContainerApplication).OriginProperties, cur.(containerinventory.ContainerApplication).OriginProperties) {
		updateProperties["origin_properties"] = cur.(containerinventory.ContainerApplication).OriginProperties
	}
}
