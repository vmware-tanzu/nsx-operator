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
	}
	log.Info("Compare resource", "updateProperties", updateProperties)
	return updateProperties
}

func compareContainerProject(pre interface{}, cur interface{}, property *map[string]interface{}) {
	updateProperties := *property
	if pre == nil || !reflect.DeepEqual(pre.(containerinventory.ContainerProject).Tags, cur.(containerinventory.ContainerProject).Tags) {
		updateProperties["display_name"] = cur.(containerinventory.ContainerProject).DisplayName
		updateProperties["container_cluster_id"] = cur.(containerinventory.ContainerProject).ContainerClusterId
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
