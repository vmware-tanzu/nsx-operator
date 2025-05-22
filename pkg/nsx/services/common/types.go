/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"fmt"
	"time"

	"github.com/openlyinc/pointy"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

const (
	HashLength                         int    = 8
	Base62HashLength                   int    = 6
	UUIDHashLength                     int    = 5
	MaxTagsCount                       int    = 26
	MaxTagScopeLength                  int    = 128
	MaxTagValueLength                  int    = 256
	MaxIdLength                        int    = 255
	MaxNameLength                      int    = 255
	MaxSubnetNameLength                int    = 80
	VPCLbResourcePathMinSegments       int    = 8
	PriorityNetworkPolicyAllowRule     int    = 2010
	PriorityNetworkPolicyIsolationRule int    = 2090
	TagScopeNCPCluster                 string = "ncp/cluster"
	TagScopeNCPProjectUID              string = "ncp/project_uid"
	TagScopeNCPCreateFor               string = "ncp/created_for"
	TagScopeNCPVIFProjectUID           string = "ncp/vif_project_uid"
	TagScopeNCPPod                     string = "ncp/pod"
	TagScopeNCPVNETInterface           string = "ncp/vnet_interface"
	TagScopeCreatedFor                 string = "nsx-op/created_for"
	TagScopeVersion                    string = "nsx-op/version"
	TagScopeCluster                    string = "nsx-op/cluster"
	TagScopeNamespace                  string = "nsx-op/namespace"
	TagScopeNamespaceUID               string = "nsx-op/namespace_uid"
	TagScopeSecurityPolicyCRName       string = "nsx-op/security_policy_cr_name"
	TagScopeSecurityPolicyCRUID        string = "nsx-op/security_policy_cr_uid"
	TagScopeSecurityPolicyName         string = "nsx-op/security_policy_name"
	TagScopeSecurityPolicyUID          string = "nsx-op/security_policy_uid"
	TagScopeNetworkPolicyName          string = "nsx-op/network_policy_name"
	TagScopeNetworkPolicyUID           string = "nsx-op/network_policy_uid"
	TagScopeStaticRouteCRName          string = "nsx-op/static_route_name"
	TagScopeStaticRouteCRUID           string = "nsx-op/static_route_uid"
	TagScopeRuleID                     string = "nsx-op/rule_id"
	TagScopeGroupType                  string = "nsx-op/group_type"
	TagScopeSelectorHash               string = "nsx-op/selector_hash"
	TagScopeNSXServiceAccountCRName    string = "nsx-op/nsx_service_account_name"
	TagScopeNSXServiceAccountCRUID     string = "nsx-op/nsx_service_account_uid"
	TagScopeNSXShareCreatedFor         string = "nsx-op/nsx_share_created_for"
	TagScopeSubnetPortCRName           string = "nsx-op/subnetport_name"
	TagScopeSubnetPortCRUID            string = "nsx-op/subnetport_uid"
	TagScopeIPAddressAllocationCRName  string = "nsx-op/ipaddressallocation_name"
	TagScopeIPAddressAllocationCRUID   string = "nsx-op/ipaddressallocation_uid"
	TagScopeAddressBindingCRName       string = "nsx-op/addressbinding_name"
	TagScopeAddressBindingCRUID        string = "nsx-op/addressbinding_uid"
	TagScopeVMNamespaceUID             string = "nsx-op/vm_namespace_uid"
	TagScopeVMNamespace                string = "nsx-op/vm_namespace"
	TagScopeManagedBy                  string = "nsx/managed-by"
	AutoCreatedTagValue                string = "nsx-op"
	LabelDefaultSubnetSet              string = "nsxoperator.vmware.com/default-subnetset-for"
	LabelImageFetcher                  string = "iaas.vmware.com/image-fetcher"
	LabelDefaultVMSubnetSet            string = "VirtualMachine"
	LabelDefaultPodSubnetSet           string = "Pod"
	LabelLbIngressIpMode               string = "nsx.vmware.com/ingress-ip-mode"
	LabelLbIngressIpModeVipValue       string = "vip"
	LabelLbIngressIpModeProxyValue     string = "proxy"
	DefaultPodSubnetSet                string = "pod-default"
	DefaultVMSubnetSet                 string = "vm-default"
	SystemVPCNetworkConfigurationName  string = "system"
	TagScopeSubnetCRUID                string = "nsx-op/subnet_uid"
	TagScopeSubnetCRName               string = "nsx-op/subnet_name"
	TagScopeSubnetSetCRName            string = "nsx-op/subnetset_name"
	TagScopeSubnetSetCRUID             string = "nsx-op/subnetset_uid"
	TagScopeSubnetBindingCRName        string = "nsx-op/subnetbinding_name"
	TagScopeSubnetBindingCRUID         string = "nsx-op/subnetbinding_uid"
	TagValueGroupScope                 string = "scope"
	TagValueGroupSource                string = "source"
	TagValueGroupDestination           string = "destination"
	TagValueShareCreatedForInfra       string = "infra"
	TagValueShareCreatedForProject     string = "project"
	TagValueShareNotCreated            string = "notShared"
	TagValueDLB                        string = "DLB"
	TagValueSLB                        string = "SLB"
	AnnotationVPCNetworkConfig         string = "nsx.vmware.com/vpc_network_config"
	AnnotationSharedVPCNamespace       string = "nsx.vmware.com/shared_vpc_namespace"
	AnnotationDefaultNetworkConfig     string = "nsx.vmware.com/default"
	AnnotationAttachmentRef            string = "nsx.vmware.com/attachment_ref"
	AnnotationAssociatedResource       string = "nsx.vmware.com/associated-resource"
	AnnotationRestore                  string = "nsx/restore"
	AnnotationPodMAC                   string = "nsx.vmware.com/mac"
	LabelCPVM                          string = "iaas.vmware.com/is-cpvm-subnetport"
	TagScopePodName                    string = "nsx-op/pod_name"
	TagScopePodUID                     string = "nsx-op/pod_uid"
	ValueMajorVersion                  string = "1"
	ValueMinorVersion                  string = "0"
	ValuePatchVersion                  string = "0"
	ConnectorUnderline                 string = "_"

	GCInterval       = 10 * 60 * time.Second
	SubnetGCInterval = 60 * time.Second
	DefaultSNATID    = "DEFAULT"
	AVISubnetLBID    = "_services"

	NSXServiceAccountFinalizerName = "nsxserviceaccount.nsx.vmware.com/finalizer"
	T1SecurityPolicyFinalizerName  = "securitypolicy.nsx.vmware.com/finalizer"
	SubnetFinalizerName            = "subnet.nsx.vmware.com/finalizer"
	SubnetSetFinalizerName         = "subnetset.nsx.vmware.com/finalizer"

	IndexKeySubnetID                  = "IndexKeySubnetID"
	IndexKeyNodeName                  = "IndexKeyNodeName"
	AssociatedResourceIndexKey        = "metadata.annotations." + AnnotationAssociatedResource
	GCValidationInterval       uint16 = 720

	RuleIngress            = "ingress"
	RuleEgress             = "egress"
	RuleActionAllow        = "allow"
	RuleActionDrop         = "isolation"
	RuleActionReject       = "reject"
	RuleAnyPorts           = "all"
	DefaultProject         = "default"
	DefaultVpcAttachmentId = "default"
	SecurityPolicyPrefix   = "sp"
	NetworkPolicyPrefix    = "np"
	TargetGroupSuffix      = "scope"
	SrcGroupSuffix         = "src"
	DstGroupSuffix         = "dst"
	IpSetGroupSuffix       = "ipset"
	ShareSuffix            = "share"

	GatewayInterfaceId = "gateway-interface"
	VPCKey             = "/orgs/%s/projects/%s/vpcs/%s"
)

var (
	TagValueVersion                 = []string{ValueMajorVersion, ValueMinorVersion, ValuePatchVersion}
	TagValueScopeSecurityPolicyName = TagScopeSecurityPolicyCRName
	TagValueScopeSecurityPolicyUID  = TagScopeSecurityPolicyCRUID
)

var (
	ResourceType                                = "resource_type"
	ResourceTypeInfra                           = "Infra"
	ResourceTypeDomain                          = "Domain"
	ResourceTypeSecurityPolicy                  = "SecurityPolicy"
	ResourceTypeNetworkPolicy                   = "NetworkPolicy"
	ResourceTypeGroup                           = "Group"
	ResourceTypeRule                            = "Rule"
	ResourceTypeIPBlock                         = "IpAddressBlock"
	ResourceTypeOrgRoot                         = "OrgRoot"
	ResourceTypeOrg                             = "Org"
	ResourceTypeProject                         = "Project"
	ResourceTypeVpc                             = "Vpc"
	ResourceTypeVpcConnectivityProfile          = "VpcConnectivityProfile"
	ResourceTypeSubnetPort                      = "VpcSubnetPort"
	ResourceTypeVirtualMachine                  = "VirtualMachine"
	ResourceTypeLBService                       = "LBService"
	ResourceTypeVpcAttachment                   = "VpcAttachment"
	ResourceTypeShare                           = "Share"
	ResourceTypeSharedResource                  = "SharedResource"
	ResourceTypeStaticRoutes                    = "StaticRoutes"
	ResourceTypeChildLBPool                     = "ChildLBPool"
	ResourceTypeChildLBService                  = "ChildLBService"
	ResourceTypeChildLBVirtualServer            = "ChildLBVirtualServer"
	ResourceTypeChildSharedResource             = "ChildSharedResource"
	ResourceTypeChildShare                      = "ChildShare"
	ResourceTypeChildRule                       = "ChildRule"
	ResourceTypeChildGroup                      = "ChildGroup"
	ResourceTypeChildSecurityPolicy             = "ChildSecurityPolicy"
	ResourceTypeChildStaticRoutes               = "ChildStaticRoutes"
	ResourceTypeChildSubnetConnectionBindingMap = "ChildSubnetConnectionBindingMap"
	ResourceTypeChildVpcAttachment              = "ChildVpcAttachment"
	ResourceTypeChildVpcIPAddressAllocation     = "ChildVpcIpAddressAllocation"
	ResourceTypeChildVpcSubnet                  = "ChildVpcSubnet"
	ResourceTypeChildVpcSubnetPort              = "ChildVpcSubnetPort"
	ResourceTypeChildResourceReference          = "ChildResourceReference"
	ResourceTypeTlsCertificate                  = "TlsCertificate"
	ResourceTypeLBHttpProfile                   = "LBHttpProfile"
	ResourceTypeLBFastTcpProfile                = "LBFastTcpProfile"
	ResourceTypeLBFastUdpProfile                = "LBFastUdpProfile"
	ResourceTypeLBCookiePersistenceProfile      = "LBCookiePersistenceProfile"
	ResourceTypeLBSourceIpPersistenceProfile    = "LBSourceIpPersistenceProfile"
	ResourceTypeLBHttpMonitorProfile            = "LBHttpMonitorProfile"
	ResourceTypeLBTcpMonitorProfile             = "LBTcpMonitorProfile"
	ResourceTypeLBVirtualServer                 = "LBVirtualServer"
	ResourceTypeLBPool                          = "LBPool"
	ResourceTypeSubnetConnectionBindingMap      = "SubnetConnectionBindingMap"

	// ResourceTypeClusterControlPlane is used by NSXServiceAccountController
	ResourceTypeClusterControlPlane = "clustercontrolplane"
	// ResourceTypePrincipalIdentity is used by NSXServiceAccountController, and it is MP resource type.
	ResourceTypePrincipalIdentity   = "principalidentity"
	ResourceTypeSubnet              = "VpcSubnet"
	ResourceTypeIPPool              = "IpAddressPool"
	ResourceTypeIPAddressAllocation = "VpcIpAddressAllocation"
	ResourceTypeIPPoolBlockSubnet   = "IpAddressPoolBlockSubnet"
	ResourceTypeNode                = "HostTransportNode"

	// Reasons for verification of gateway connection in day0
	ReasonGatewayConnectionNotSet                    = "GatewayConnectionNotSet"
	ReasonServiceClusterNotSet                       = "ServiceClusterNotSet"
	ReasonNoExternalIPBlocksInVPCConnectivityProfile = "ExternalIPBlockMissingInProfile"
)

type Service struct {
	Client    client.Client
	NSXClient *nsx.Client
	NSXConfig *config.NSXOperatorConfig
}

func NewConverter() *bindings.TypeConverter {
	converter := bindings.NewTypeConverter()
	return converter
}

var (
	String = pointy.String // address of string
	Int64  = pointy.Int64  // address of int64
	Bool   = pointy.Bool   // address of bool
)

type VPCResourceInfo struct {
	OrgID     string
	ProjectID string
	VPCID     string
	// 1. For the subnetport with path /orgs/o1/projects/p1/vpcs/v1/subnets/s1/ports/port1,
	//    ID=port1, ParentID=s1;
	// 2. For the subnet with path /orgs/o1/projects/p1/vpcs/v1/subnets/s1,
	//    ID=s1, ParentID=v1 (ParentID==VPCID).
	ID         string
	ParentID   string
	PrivateIps []string
}

func (info *VPCResourceInfo) GetVPCPath() string {
	return fmt.Sprintf(VPCKey, info.OrgID, info.ProjectID, info.VPCID)
}

type VPCConnectionStatus struct {
	GatewayConnectionReady  bool
	GatewayConnectionReason string
	ServiceClusterReady     bool
	ServiceClusterReason    string
}
