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
	mpsearch "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/search"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/trust_management"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/trust_management/principal_identities"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains/security_policies"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/sites/enforcement_points"
	vpc_search "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/search"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
)

const (
	VPC = iota
	SecurityPolicy
	ServiceAccount
	ServiceAccountRestore
	ServiceAccountCertRotation
	StaticRoute
	AllFeatures
)

var FeaturesName = [AllFeatures]string{"VPC", "SECURITY_POLICY", "NSX_SERVICE_ACCOUNT", "NSX_SERVICE_ACCOUNT_RESTORE", "NSX_SERVICE_ACCOUNT_CERT_ROTATION", "STATIC_ROUTE"}

type Client struct {
	NsxConfig     *config.NSXOperatorConfig
	RestConnector *client.RestConnector

	QueryClient                search.QueryClient
	VPCQueryClient             vpc_search.QueryClient
	GroupClient                domains.GroupsClient
	SecurityClient             domains.SecurityPoliciesClient
	RuleClient                 security_policies.RulesClient
	InfraClient                nsx_policy.InfraClient
	ClusterControlPlanesClient enforcement_points.ClusterControlPlanesClient

	MPQueryClient             mpsearch.QueryClient
	CertificatesClient        trust_management.CertificatesClient
	PrincipalIdentitiesClient trust_management.PrincipalIdentitiesClient
	WithCertificateClient     principal_identities.WithCertificateClient

	NSXChecker    NSXHealthChecker
	NSXVerChecker NSXVersionChecker
}

var nsx320Version = [3]int64{3, 2, 0}
var nsx401Version = [3]int64{4, 0, 1}
var nsx412Version = [3]int64{4, 1, 2}
var nsx413Version = [3]int64{4, 1, 3}

type NSXHealthChecker struct {
	cluster *Cluster
}

type NSXVersionChecker struct {
	cluster          *Cluster
	featureSupported [AllFeatures]bool
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
	c := NewConfig(strings.Join(cf.NsxApiManagers, ","), cf.NsxApiUser, cf.NsxApiPassword, cf.CaFile, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, cf.GetTokenProvider(), nil, cf.Thumbprint)
	cluster, _ := NewCluster(c)

	queryClient := search.NewQueryClient(restConnector(cluster))
	groupClient := domains.NewGroupsClient(restConnector(cluster))
	securityClient := domains.NewSecurityPoliciesClient(restConnector(cluster))
	ruleClient := security_policies.NewRulesClient(restConnector(cluster))
	infraClient := nsx_policy.NewInfraClient(restConnector(cluster))
	vpcQueryClient := vpc_search.NewQueryClient(restConnector(cluster))
	clusterControlPlanesClient := enforcement_points.NewClusterControlPlanesClient(restConnector(cluster))

	mpQueryClient := mpsearch.NewQueryClient(restConnector(cluster))
	certificatesClient := trust_management.NewCertificatesClient(restConnector(cluster))
	principalIdentitiesClient := trust_management.NewPrincipalIdentitiesClient(restConnector(cluster))
	withCertificateClient := principal_identities.NewWithCertificateClient(restConnector(cluster))

	nsxChecker := &NSXHealthChecker{
		cluster: cluster,
	}
	nsxVersionChecker := &NSXVersionChecker{
		cluster:          cluster,
		featureSupported: [AllFeatures]bool{},
	}

	nsxClient := &Client{
		NsxConfig:      cf,
		RestConnector:  restConnector(cluster),
		QueryClient:    queryClient,
		GroupClient:    groupClient,
		SecurityClient: securityClient,
		RuleClient:     ruleClient,
		InfraClient:    infraClient,

		ClusterControlPlanesClient: clusterControlPlanesClient,

		MPQueryClient:             mpQueryClient,
		CertificatesClient:        certificatesClient,
		PrincipalIdentitiesClient: principalIdentitiesClient,
		WithCertificateClient:     withCertificateClient,

		NSXChecker:     *nsxChecker,
		NSXVerChecker:  *nsxVersionChecker,
		VPCQueryClient: vpcQueryClient,
	}
	// NSX version check will be restarted during SecurityPolicy reconcile
	// So, it's unnecessary to exit even if failed in the first time
	if !nsxClient.NSXCheckVersion(SecurityPolicy) {
		err := errors.New("SecurityPolicy feature support check failed")
		log.Error(err, "Initial NSX version check for SecurityPolicy got error")
	}
	if !nsxClient.NSXCheckVersion(ServiceAccount) {
		err := errors.New("NSXServiceAccount feature support check failed")
		log.Error(err, "initial NSX version check for NSXServiceAccount got error")
	}
	if !nsxClient.NSXCheckVersion(ServiceAccountRestore) {
		err := errors.New("NSXServiceAccountRestore feature support check failed")
		log.Error(err, "initial NSX version check for NSXServiceAccountRestore got error")
	}
	if !nsxClient.NSXCheckVersion(ServiceAccountCertRotation) {
		err := errors.New("ServiceAccountCertRotation feature support check failed")
		log.Error(err, "initial NSX version check for ServiceAccountCertRotation got error")
	}

	return nsxClient
}

func (client *Client) NSXCheckVersion(feature int) bool {
	if client.NSXVerChecker.featureSupported[feature] {
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

	if !nsxVersion.featureSupported(feature) {
		err = errors.New("NSX version check failed")
		log.Error(err, FeaturesName[feature]+"feature is not supported", "current version", nsxVersion.NodeVersion)
		return false
	}
	client.NSXVerChecker.featureSupported[feature] = true
	return true
}
