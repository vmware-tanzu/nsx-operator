/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"errors"
	"net/http"
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
	NSXChecker     NSXHealthChecker
}

var minVersion = [3]int64{3, 2, 0}

type NSXHealthChecker struct {
	cluster *Cluster
}

func (ck *NSXHealthChecker) CheckNSXHealth(req *http.Request) error {
	health := ck.cluster.Health()
	if GREEN == health || ORANGE == health {
		return nil
	} else {
		log.V(1).Info("NSX cluster status is down: ", " Current status: ", health)
		return errors.New("NSX Current Status is down")
	}
}

func restConnector(c *Cluster) *client.RestConnector {
	connector, _ := c.NewRestConnector()
	return connector
}

func GetClient(cf *config.NSXOperatorConfig) *Client {
	// Set log level for vsphere-automation-sdk-go
	logger := logrus.New()
	vspherelog.SetLogger(logger)
	c := NewConfig(strings.Join(cf.NsxApiManagers, ","), cf.NsxApiUser, cf.NsxApiPassword, "", 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, cf.GetTokenProvider(), nil, cf.Thumbprint)
	cluster, _ := NewCluster(c)
	nsxVersion, err := cluster.GetVersion()
	if err != nil {
		log.Error(err, "get version error")
		return nil
	}
	err = nsxVersion.Validate(minVersion)
	if err != nil {
		log.Error(err, "validate version error")
		return nil
	}
	queryClient := search.NewQueryClient(restConnector(cluster))
	groupClient := domains.NewGroupsClient(restConnector(cluster))
	securityClient := domains.NewSecurityPoliciesClient(restConnector(cluster))
	ruleClient := security_policies.NewRulesClient(restConnector(cluster))
	nsxChecker := &NSXHealthChecker{
		cluster: cluster,
	}
	return &Client{
		NsxConfig:      cf,
		RestConnector:  restConnector(cluster),
		QueryClient:    queryClient,
		GroupClient:    groupClient,
		SecurityClient: securityClient,
		RuleClient:     ruleClient,
		NSXChecker:     *nsxChecker,
	}
}
