/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains/security_policies"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"
)

func TestClient(t *testing.T) {
	host := "10.191.254.13"
	config := NewConfig(host, "admin", "Admin!23Admin", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster, _ := NewCluster(config)

	domainIDParam := "k8scl-one"
	serviceName := "test_service"
	groupName := "test_group"

	err := createGroup(domainIDParam, groupName, cluster)
	assert.True(t, err == nil, fmt.Sprintf("Create group failed due to %v ", err))

	err = createService(serviceName, cluster)
	assert.True(t, err == nil, fmt.Sprintf("Create service failed due to %v ", err))

	err = createRule(domainIDParam, groupName, serviceName, cluster)
	assert.True(t, err == nil, fmt.Sprintf("Create rule failed due to %v ", err))

	connector, _ := cluster.NewRestConnector()
	fwClient := domains.NewSecurityPoliciesClient(connector)
	result, err := fwClient.List(domainIDParam, nil, nil, nil, nil, nil, nil, nil)
	assert.True(t, err == nil, fmt.Sprintf("Get security policy failed due to %v ", err))
	log.V(1).Info("get security policy", "resultCount", *result.ResultCount)
}

func TestQuery(t *testing.T) {
	host := "10.191.254.13, 10.191.248.221, 10.191.249.48 "
	config := NewConfig(host, "admin", "Admin!23Admin", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster, _ := NewCluster(config)

	connector, _ := cluster.NewRestConnector()
	queryClient := search.NewQueryClient(connector)
	queryParam := "Segment"
	for i := 0; i < 4; i++ {
		response, err := queryClient.List(url.QueryEscape(queryParam), nil, nil, nil, nil, nil)
		assert.True(t, err == nil, fmt.Sprintf("Query segment failed due to %v ", err))
		log.V(1).Info("query segment", "resultCount", *response.ResultCount)
	}
	for i := 0; i < 20; i++ {
		go func() {
			response, err := queryClient.List(url.QueryEscape(queryParam), nil, nil, nil, nil, nil)
			assert.True(t, err == nil, fmt.Sprintf("Query segment failed due to %v ", err))
			log.V(1).Info("query segment", "resultCount", *response.ResultCount)
		}()
		time.Sleep(time.Microsecond)
	}
	time.Sleep(time.Second * 3)
}

func createRule(domainIDParam string, groupName string, serviceName string, cluster *Cluster) error {
	securityPolicyIDParam := "hc_k8scl-one"

	connector, _ := cluster.NewRestConnector()
	rulesClient := security_policies.NewRulesClient(connector)
	ruleParam := model.Rule{}
	var number int64 = 99
	ruleParam.SequenceNumber = &number
	var action string = "ALLOW"
	ruleParam.Action = &action
	ruleParam.DestinationGroups = []string{"ANY"}
	ruleParam.SourceGroups = []string{"ANY"}
	ruleParam.Scope = []string{"/infra/domains/k8scl-one/groups/" + groupName}
	ruleParam.SourceGroups = []string{"192.168.0.3"}
	ruleParam.Services = []string{"/infra/services/" + serviceName}

	return rulesClient.Patch(domainIDParam, securityPolicyIDParam, "test_rules", ruleParam)
}

func createGroup(domainID string, groupName string, cluster *Cluster) error {
	connector, _ := cluster.NewRestConnector()
	groupClient := domains.NewGroupsClient(connector)
	groupParam := model.Group{}

	fields := make(map[string]data.DataValue)
	fields["member_type"] = data.NewStringValue("VirtualMachine")
	fields["value"] = data.NewStringValue("webvm")
	fields["key"] = data.NewStringValue("Tag")
	fields["operator"] = data.NewStringValue("EQUALS")
	fields["resource_type"] = data.NewStringValue("Condition")
	expression := data.NewStructValue("expression1", fields)
	expressions := []*data.StructValue{expression}

	groupParam.Expression = expressions
	discription := "web group"
	groupParam.Description = &discription
	groupParam.DisplayName = &groupName
	return groupClient.Patch(domainID, groupName, groupParam)
}

func createService(serviceName string, cluster *Cluster) error {
	connector, _ := cluster.NewRestConnector()
	serviceClient := infra.NewServicesClient(connector)
	serviceParam := model.Service{}
	discrition := "HTTP service"
	serviceParam.Description = &discrition
	serviceParam.DisplayName = &serviceName

	entry := make(map[string]data.DataValue)

	entry["resource_type"] = data.NewStringValue("L4PortSetServiceEntry")
	entry["display_name"] = data.NewStringValue("HTTPEntry")
	entry["l4_protocol"] = data.NewStringValue("TCP")
	ports := data.NewListValue()
	ports.Add(data.NewStringValue("8080"))
	entry["destination_ports"] = ports

	serviceParam.ServiceEntries = []*data.StructValue{data.NewStructValue("expression1", entry)}
	return serviceClient.Patch(serviceName, serviceParam)
}
