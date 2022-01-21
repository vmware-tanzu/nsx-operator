/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package services

import (
	"encoding/json"
	"testing"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains/security_policies"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/cluster"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
)

var host string = "10.180.127.117,10.180.119.135,10.180.114.111"

func TestGroup(t *testing.T) {
	domainIDParam := "default"
	config := cluster.NewConfig(host, "admin", "Admin!23Admin", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := cluster.NewCluster(config)
	connector, _ := cluster.NewRestConnector()
	groupClient := domains.NewGroupsClient(connector)
	result, _ := groupClient.List(domainIDParam, nil, nil, nil, nil, nil, nil, nil)
	res, _ := json.Marshal(result)
	t.Log("response is ", string(res))
}

func TestSecurityPolicy(t *testing.T) {
	domainIDParam := "default"
	config := cluster.NewConfig(host, "admin", "Admin!23Admin", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := cluster.NewCluster(config)
	connector, _ := cluster.NewRestConnector()
	securityClient := domains.NewSecurityPoliciesClient(connector)
	result, _ := securityClient.List(domainIDParam, nil, nil, nil, nil, nil, nil, nil)
	res, _ := json.Marshal(result)
	t.Log("response is ", string(res))
}

func TestRule(t *testing.T) {
	domainIDParam := "default"
	config := cluster.NewConfig(host, "admin", "Admin!23Admin", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := cluster.NewCluster(config)
	connector, _ := cluster.NewRestConnector()
	securityClient := domains.NewSecurityPoliciesClient(connector)
	ruleClient := security_policies.NewRulesClient(connector)
	result, _ := securityClient.List(domainIDParam, nil, nil, nil, nil, nil, nil, nil)
	for _, securityPolicy := range result.Results {
		result, _ := ruleClient.List(domainIDParam, *securityPolicy.Id, nil, nil, nil, nil, nil, nil)
		res, _ := json.Marshal(result)
		t.Log("response is ", string(res))
	}
}

func TestQuery(t *testing.T) {
	config := cluster.NewConfig(host, "admin", "Admin!23Admin", "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := cluster.NewCluster(config)
	connector, _ := cluster.NewRestConnector()
	queryClient := search.NewQueryClient(connector)
	queryParam := "resource_type:Group AND tags.tag:k8scl-one"
	response, _ := queryClient.List(queryParam, nil, nil, nil, nil, nil)
	typeConverter := connector.TypeConverter()
	for _, g := range response.Results {
		a, _ := typeConverter.ConvertToGolang(g, model.GroupBindingType())
		c, _ := a.(model.Group)
		t.Log(c.Id)
	}

	queryParam = "resource_type:securitypolicy"
	response, _ = queryClient.List(queryParam, nil, nil, nil, nil, nil)
	for _, g := range response.Results {
		a, _ := typeConverter.ConvertToGolang(g, model.SecurityPolicyBindingType())
		c, _ := a.(model.SecurityPolicy)
		t.Log(c.Id)
	}

	queryParam = "resource_type:rule"
	response, _ = queryClient.List(queryParam, nil, nil, nil, nil, nil)
	for _, g := range response.Results {
		a, _ := typeConverter.ConvertToGolang(g, model.RuleBindingType())
		c, _ := a.(model.Rule)
		t.Log(c.Id)
	}
}
