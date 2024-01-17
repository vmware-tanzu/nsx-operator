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
	nsx_policy "github.com/vmware/vsphere-automation-sdk-go/services/nsxt"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains/security_policies"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

const (
	FeatureSecurityPolicy string = "SECURITY_POLICY"
)

type Client struct {
	NsxConfig      *config.NSXOperatorConfig
	RestConnector  *client.RestConnector
	QueryClient    search.QueryClient
	GroupClient    domains.GroupsClient
	SecurityClient domains.SecurityPoliciesClient
	RuleClient     security_policies.RulesClient
	InfraClient    nsx_policy.InfraClient
	NSXChecker     NSXHealthChecker
	NSXVerChecker  NSXVersionChecker
}

var nsx320Version = [3]int64{3, 2, 0}

type NSXHealthChecker struct {
	cluster *Cluster
}

type NSXVersionChecker struct {
	cluster                 *Cluster
	securityPolicySupported bool
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

	queryClient := search.NewQueryClient(restConnector(cluster))
	groupClient := domains.NewGroupsClient(restConnector(cluster))
	securityClient := domains.NewSecurityPoliciesClient(restConnector(cluster))
	ruleClient := security_policies.NewRulesClient(restConnector(cluster))
	infraClient := nsx_policy.NewInfraClient(restConnector(cluster))
	nsxChecker := &NSXHealthChecker{
		cluster: cluster,
	}
	nsxVersionChecker := &NSXVersionChecker{
		cluster:                 cluster,
		securityPolicySupported: false,
	}

	nsxClient := &Client{
		NsxConfig:      cf,
		RestConnector:  restConnector(cluster),
		QueryClient:    queryClient,
		GroupClient:    groupClient,
		SecurityClient: securityClient,
		RuleClient:     ruleClient,
		InfraClient:    infraClient,
		NSXChecker:     *nsxChecker,
		NSXVerChecker:  *nsxVersionChecker,
	}
	// NSX version check will be restarted during SecurityPolicy reconcile
	// So, it's unnecessary to exit even if failed in the first time
	if !nsxClient.NSXCheckVersionForSecurityPolicy() {
		err := errors.New("SecurityPolicy feature support check failed")
		log.Error(err, "initial NSX version check for SecurityPolicy got error")
	}

	return nsxClient
}

func (client *Client) NSXCheckVersionForSecurityPolicy() bool {
	if client.NSXVerChecker.securityPolicySupported {
		return true
	}

	nsxVersion, err := client.NSXVerChecker.cluster.GetVersion()
	if err != nil {
		log.Error(err, "get version error")
		return false
	}
	err = nsxVersion.Validate()
	if err != nil {
		log.Error(err, "validate version error")
		return false
	}

	if !nsxVersion.featureSupported(FeatureSecurityPolicy) {
		err = errors.New("NSX version check failed")
		log.Error(err, "SecurityPolicy feature is not supported", "current version", nsxVersion.NodeVersion, "required version", nsx320Version)
		return false
	}
	client.NSXVerChecker.securityPolicySupported = true
	return true
}

// ValidateLicense validates NSX license. init is used to indicate whether nsx-operator is init or not
// if not init, nsx-operator will check if license has been updated.
// once license updated, operator will restart
// if FeatureContainer license is false, operatore will restart
func (client *Client) ValidateLicense(init bool) error {
	log.Info("Checking NSX license")
	oldContainerLicense := util.IsLicensed(util.FeatureContainer)
	oldDfwLicense := util.IsLicensed(util.FeatureDFW)
	err := client.NSXChecker.cluster.FetchLicense()
	if err != nil {
		return err
	}
	if !util.IsLicensed(util.FeatureContainer) {
		err = errors.New("NSX license check failed")
		log.Error(err, "container license is not supported")
		return err
	}
	if !init {
		newContainerLicense := util.IsLicensed(util.FeatureContainer)
		newDfwLicense := util.IsLicensed(util.FeatureDFW)
		if newContainerLicense != oldContainerLicense || newDfwLicense != oldDfwLicense {
			log.Info("license updated, reset", "container license new value", newContainerLicense, "DFW license new value", newDfwLicense, "container license old value", oldContainerLicense, "DFW license old value", oldDfwLicense)
			return errors.New("license updated")
		}
	}
	return nil
}
