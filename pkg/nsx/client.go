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
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/domains/security_policies"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/shares"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/sites/enforcement_points"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects"
	project_infra "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/infra"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/infra/realized_state"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/transit_gateways"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs/nat"
	vpc_sp "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs/security_policies"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs/subnets"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs/subnets/ip_pools"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects/vpcs/subnets/ports"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/search"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
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
	RestConnector client.Connector
	Cluster       *Cluster

	QueryClient    search.QueryClient
	GroupClient    domains.GroupsClient
	SecurityClient domains.SecurityPoliciesClient
	RuleClient     security_policies.RulesClient
	InfraClient    nsx_policy.InfraClient

	ClusterControlPlanesClient enforcement_points.ClusterControlPlanesClient
	HostTransPortNodesClient   enforcement_points.HostTransportNodesClient
	SubnetStatusClient         subnets.StatusClient
	RealizedEntitiesClient     realized_state.RealizedEntitiesClient
	MPQueryClient              mpsearch.QueryClient
	CertificatesClient         trust_management.CertificatesClient
	PrincipalIdentitiesClient  trust_management.PrincipalIdentitiesClient
	WithCertificateClient      principal_identities.WithCertificateClient

	// for AVI security policy rule
	VPCSecurityClient vpcs.SecurityPoliciesClient
	VPCRuleClient     vpc_sp.RulesClient

	OrgRootClient                  nsx_policy.OrgRootClient
	ProjectInfraClient             projects.InfraClient
	VPCClient                      projects.VpcsClient
	VPCConnectivityProfilesClient  projects.VpcConnectivityProfilesClient
	IPBlockClient                  project_infra.IpBlocksClient
	StaticRouteClient              vpcs.StaticRoutesClient
	NATRuleClient                  nat.NatRulesClient
	VpcGroupClient                 vpcs.GroupsClient
	PortClient                     subnets.PortsClient
	PortStateClient                ports.StateClient
	IPPoolClient                   subnets.IpPoolsClient
	IPAllocationClient             ip_pools.IpAllocationsClient
	SubnetsClient                  vpcs.SubnetsClient
	RealizedStateClient            realized_state.RealizedEntitiesClient
	IPAddressAllocationClient      vpcs.IpAddressAllocationsClient
	VPCLBSClient                   vpcs.VpcLbsClient
	VpcLbVirtualServersClient      vpcs.VpcLbVirtualServersClient
	VpcLbPoolsClient               vpcs.VpcLbPoolsClient
	VpcAttachmentClient            vpcs.AttachmentsClient
	ProjectClient                  orgs.ProjectsClient
	TransitGatewayClient           projects.TransitGatewaysClient
	TransitGatewayAttachmentClient transit_gateways.AttachmentsClient
	CertificateClient              infra.CertificatesClient
	ShareClient                    infra.SharesClient
	SharedResourceClient           shares.ResourcesClient
	LbAppProfileClient             infra.LbAppProfilesClient
	LbPersistenceProfilesClient    infra.LbPersistenceProfilesClient
	LbMonitorProfilesClient        infra.LbMonitorProfilesClient

	NSXChecker    NSXHealthChecker
	NSXVerChecker NSXVersionChecker
}

var (
	nsx320Version = [3]int64{3, 2, 0}
	nsx401Version = [3]int64{4, 0, 1}
	nsx411Version = [3]int64{4, 1, 1}
	nsx412Version = [3]int64{4, 1, 2}
	nsx413Version = [3]int64{4, 1, 3}
)

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

func restConnector(c *Cluster) client.Connector {
	connector, _ := c.NewRestConnector()
	return connector
}

func GetClient(cf *config.NSXOperatorConfig) *Client {
	// Set log level for vsphere-automation-sdk-go
	logger := logrus.New()
	vspherelog.SetLogger(logger)
	defaultHttpTimeout := 20
	if cf.DefaultTimeout > 0 {
		defaultHttpTimeout = cf.DefaultTimeout
	}
	c := NewConfig(strings.Join(cf.NsxApiManagers, ","), cf.NsxApiUser, cf.NsxApiPassword, cf.CaFile, 10, 3, defaultHttpTimeout, 20, true, true, true,
		ratelimiter.AIMD, cf.GetTokenProvider(), nil, cf.Thumbprint)
	c.EnvoyHost = cf.EnvoyHost
	c.EnvoyPort = cf.EnvoyPort
	cluster, _ := NewCluster(c)

	queryClient := search.NewQueryClient(restConnector(cluster))
	groupClient := domains.NewGroupsClient(restConnector(cluster))
	securityClient := domains.NewSecurityPoliciesClient(restConnector(cluster))
	ruleClient := security_policies.NewRulesClient(restConnector(cluster))
	infraClient := nsx_policy.NewInfraClient(restConnector(cluster))

	clusterControlPlanesClient := enforcement_points.NewClusterControlPlanesClient(restConnector(cluster))
	hostTransportNodesClient := enforcement_points.NewHostTransportNodesClient(restConnector(cluster))
	realizedEntitiesClient := realized_state.NewRealizedEntitiesClient(restConnector(cluster))
	mpQueryClient := mpsearch.NewQueryClient(restConnector(cluster))
	certificatesClient := trust_management.NewCertificatesClient(restConnector(cluster))
	principalIdentitiesClient := trust_management.NewPrincipalIdentitiesClient(restConnector(cluster))
	withCertificateClient := principal_identities.NewWithCertificateClient(restConnector(cluster))

	orgRootClient := nsx_policy.NewOrgRootClient(restConnector(cluster))
	projectInfraClient := projects.NewInfraClient(restConnector(cluster))
	projectClient := orgs.NewProjectsClient(restConnector(cluster))
	vpcClient := projects.NewVpcsClient(restConnector(cluster))
	vpcConnectivityProfilesClient := projects.NewVpcConnectivityProfilesClient(restConnector(cluster))
	ipBlockClient := project_infra.NewIpBlocksClient(restConnector(cluster))
	staticRouteClient := vpcs.NewStaticRoutesClient(restConnector(cluster))
	natRulesClient := nat.NewNatRulesClient(restConnector(cluster))
	vpcGroupClient := vpcs.NewGroupsClient(restConnector(cluster))
	portClient := subnets.NewPortsClient(restConnector(cluster))
	portStateClient := ports.NewStateClient(restConnector(cluster))
	ipPoolClient := subnets.NewIpPoolsClient(restConnector(cluster))
	ipAllocationClient := ip_pools.NewIpAllocationsClient(restConnector(cluster))
	subnetsClient := vpcs.NewSubnetsClient(restConnector(cluster))
	subnetStatusClient := subnets.NewStatusClient(restConnector(cluster))
	realizedStateClient := realized_state.NewRealizedEntitiesClient(restConnector(cluster))
	ipAddressAllocationClient := vpcs.NewIpAddressAllocationsClient(restConnector(cluster))
	vpcLBSClient := vpcs.NewVpcLbsClient(restConnector(cluster))
	vpcLbVirtualServersClient := vpcs.NewVpcLbVirtualServersClient(restConnector(cluster))
	vpcLbPoolsClient := vpcs.NewVpcLbPoolsClient(restConnector(cluster))
	vpcAttachmentClient := vpcs.NewAttachmentsClient(restConnector(cluster))

	vpcSecurityClient := vpcs.NewSecurityPoliciesClient(restConnector(cluster))
	vpcRuleClient := vpc_sp.NewRulesClient(restConnector(cluster))

	transitGatewayClient := projects.NewTransitGatewaysClient(restConnector(cluster))
	transitGatewayAttachmentClient := transit_gateways.NewAttachmentsClient(restConnector(cluster))

	nsxChecker := &NSXHealthChecker{
		cluster: cluster,
	}
	nsxVersionChecker := &NSXVersionChecker{
		cluster:          cluster,
		featureSupported: [AllFeatures]bool{},
	}

	nsxClient := &Client{
		NsxConfig:                  cf,
		RestConnector:              restConnector(cluster),
		QueryClient:                queryClient,
		GroupClient:                groupClient,
		SecurityClient:             securityClient,
		RuleClient:                 ruleClient,
		InfraClient:                infraClient,
		Cluster:                    cluster,
		ClusterControlPlanesClient: clusterControlPlanesClient,
		HostTransPortNodesClient:   hostTransportNodesClient,
		RealizedEntitiesClient:     realizedEntitiesClient,
		MPQueryClient:              mpQueryClient,
		CertificatesClient:         certificatesClient,
		PrincipalIdentitiesClient:  principalIdentitiesClient,
		WithCertificateClient:      withCertificateClient,

		OrgRootClient:                  orgRootClient,
		ProjectInfraClient:             projectInfraClient,
		VPCClient:                      vpcClient,
		VPCConnectivityProfilesClient:  vpcConnectivityProfilesClient,
		IPBlockClient:                  ipBlockClient,
		StaticRouteClient:              staticRouteClient,
		NATRuleClient:                  natRulesClient,
		VpcGroupClient:                 vpcGroupClient,
		PortClient:                     portClient,
		PortStateClient:                portStateClient,
		SubnetStatusClient:             subnetStatusClient,
		VPCSecurityClient:              vpcSecurityClient,
		VPCRuleClient:                  vpcRuleClient,
		VPCLBSClient:                   vpcLBSClient,
		VpcLbVirtualServersClient:      vpcLbVirtualServersClient,
		VpcLbPoolsClient:               vpcLbPoolsClient,
		VpcAttachmentClient:            vpcAttachmentClient,
		ProjectClient:                  projectClient,
		NSXChecker:                     *nsxChecker,
		NSXVerChecker:                  *nsxVersionChecker,
		IPPoolClient:                   ipPoolClient,
		IPAllocationClient:             ipAllocationClient,
		SubnetsClient:                  subnetsClient,
		RealizedStateClient:            realizedStateClient,
		IPAddressAllocationClient:      ipAddressAllocationClient,
		TransitGatewayClient:           transitGatewayClient,
		TransitGatewayAttachmentClient: transitGatewayAttachmentClient,
	}
	// NSX version check will be restarted during SecurityPolicy reconcile
	// So, it's unnecessary to exit even if failed in the first time
	if !nsxClient.NSXCheckVersion(SecurityPolicy) {
		err := errors.New("SecurityPolicy feature support check failed")
		log.Error(err, "initial NSX version check for SecurityPolicy got error")
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

func (client *Client) FeatureEnabled(feature int) bool {
	return client.NSXVerChecker.featureSupported[feature] == true
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
