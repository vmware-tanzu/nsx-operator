/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"strings"

	"github.com/sirupsen/logrus"
	vspherelog "github.com/vmware/vsphere-automation-sdk-go/runtime/log"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains/security_policies"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
)

type Client struct {
	NsxConfig      *config.NSXOperatorConfig
	RestConnector  *client.RestConnector
	QueryClient    search.QueryClient
	GroupClient    domains.GroupsClient
	SecurityClient domains.SecurityPoliciesClient
	RuleClient     security_policies.RulesClient
}

func restConnector(c *Cluster) *client.RestConnector {
	connector, _ := c.NewRestConnector()
	return connector
}

func GetClient() *Client {

	// Set log level for vsphere-automation-sdk-go
	logger := logrus.New()
	vspherelog.SetLogger(logger)

	cf, _ := config.NewNSXOperatorConfigFromFile()
	c := NewConfig(strings.Join(cf.NsxApiManagers, ","), cf.NsxApiUser, cf.NsxApiPassword, "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil)
	cluster, _ := NewCluster(c)
	queryClient := search.NewQueryClient(restConnector(cluster))
	groupClient := domains.NewGroupsClient(restConnector(cluster))
	securityClient := domains.NewSecurityPoliciesClient(restConnector(cluster))
	ruleClient := security_policies.NewRulesClient(restConnector(cluster))
	return &Client{
		NsxConfig:      cf,
		RestConnector:  restConnector(cluster),
		QueryClient:    queryClient,
		GroupClient:    groupClient,
		SecurityClient: securityClient,
		RuleClient:     ruleClient,
	}
}
