/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"time"

	"github.com/openlyinc/pointy"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

const (
	HashLength                         int    = 8
	MaxTagScopeLength                  int    = 128
	MaxTagValueLength                  int    = 256
	MaxIdLength                        int    = 255
	MaxNameLength                      int    = 255
	MaxSubnetNameLength                int    = 80
	PriorityNetworkPolicyAllowRule     int    = 2010
	PriorityNetworkPolicyIsolationRule int    = 2090
	TagScopeNCPCluster                 string = "ncp/cluster"
	TagScopeNCPProjectUID              string = "ncp/project_uid"
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
	TagScopeGoupID                     string = "nsx-op/group_id"
	TagScopeGroupType                  string = "nsx-op/group_type"
	TagScopeSelectorHash               string = "nsx-op/selector_hash"
	TagScopeNSXServiceAccountCRName    string = "nsx-op/nsx_service_account_name"
	TagScopeNSXServiceAccountCRUID     string = "nsx-op/nsx_service_account_uid"
	TagScopeNSXProjectID               string = "nsx-op/nsx_project_id"
	TagScopeProjectGroupShared         string = "nsx-op/is_nsx_project_shared"
	TagScopeSubnetPortCRName           string = "nsx-op/subnetport_name"
	TagScopeSubnetPortCRUID            string = "nsx-op/subnetport_uid"
	TagScopeIPPoolCRName               string = "nsx-op/ippool_name"
	TagScopeIPPoolCRUID                string = "nsx-op/ippool_uid"
	TagScopeIPPoolCRType               string = "nsx-op/ippool_type"
	TagScopeIPAddressAllocationCRName  string = "nsx-op/ipaddressallocation_name"
	TagScopeIPAddressAllocationCRUID   string = "nsx-op/ipaddressallocation_uid"
	TagScopeIPSubnetName               string = "nsx-op/ipsubnet_name"
	TagScopeVMNamespaceUID             string = "nsx-op/vm_namespace_uid"
	TagScopeVMNamespace                string = "nsx-op/vm_namespace"
	TagScopeVPCManagedBy               string = "nsx/managed-by"
	AutoCreatedVPCTagValue             string = "nsx-op"
	LabelDefaultSubnetSet              string = "nsxoperator.vmware.com/default-subnetset-for"
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
	TagValueGroupScope                 string = "scope"
	TagValueGroupSource                string = "source"
	TagValueGroupDestination           string = "destination"
	TagValueGroupAvi                   string = "avi"
	TagValueSLB                        string = "SLB"
	AnnotationVPCNetworkConfig         string = "nsx.vmware.com/vpc_network_config"
	AnnotationSharedVPCNamespace       string = "nsx.vmware.com/shared_vpc_namespace"
	AnnotationDefaultNetworkConfig     string = "nsx.vmware.com/default"
	AnnotationAttachmentRef            string = "nsx.vmware.com/attachment_ref"
	AnnotationPodMAC                   string = "nsx.vmware.com/mac"
	AnnotationPodAttachment            string = "nsx.vmware.com/attachment"
	TagScopePodName                    string = "nsx-op/pod_name"
	TagScopePodUID                     string = "nsx-op/pod_uid"
	ValueMajorVersion                  string = "1"
	ValueMinorVersion                  string = "0"
	ValuePatchVersion                  string = "0"

	GCInterval        = 60 * time.Second
	RealizeTimeout    = 2 * time.Minute
	RealizeMaxRetries = 3
	DefaultSNATID     = "DEFAULT"
	AVISubnetLBID     = "_AVI_SUBNET--LB"
	IPPoolTypePublic  = "Public"
	IPPoolTypePrivate = "Private"

	NSXServiceAccountFinalizerName = "nsxserviceaccount.nsx.vmware.com/finalizer"
	T1SecurityPolicyFinalizerName  = "securitypolicy.nsx.vmware.com/finalizer"

	SecurityPolicyFinalizerName      = "securitypolicy.crd.nsx.vmware.com/finalizer"
	NetworkPolicyFinalizerName       = "networkpolicy.crd.nsx.vmware.com/finalizer"
	StaticRouteFinalizerName         = "staticroute.crd.nsx.vmware.com/finalizer"
	SubnetFinalizerName              = "subnet.crd.nsx.vmware.com/finalizer"
	SubnetSetFinalizerName           = "subnetset.crd.nsx.vmware.com/finalizer"
	SubnetPortFinalizerName          = "subnetport.crd.nsx.vmware.com/finalizer"
	NetworkInfoFinalizerName         = "networkinfo.crd.nsx.vmware.com/finalizer"
	PodFinalizerName                 = "pod.crd.nsx.vmware.com/finalizer"
	IPPoolFinalizerName              = "ippool.crd.nsx.vmware.com/finalizer"
	IPAddressAllocationFinalizerName = "ipaddressallocation.crd.nsx.vmware.com/finalizer"

	IndexKeySubnetID            = "IndexKeySubnetID"
	IndexKeyPathPath            = "Path"
	IndexKeyNodeName            = "IndexKeyNodeName"
	GCValidationInterval uint16 = 720

	RuleSuffixIngressAllow  = "ingress-allow"
	RuleSuffixEgressAllow   = "egress-allow"
	RuleSuffixIngressDrop   = "ingress-isolation"
	RuleSuffixEgressDrop    = "egress-isolation"
	RuleSuffixIngressReject = "ingress-reject"
	RuleSuffixEgressReject  = "egress-reject"
	SecurityPolicyPrefix    = "sp"
	NetworkPolicyPrefix     = "np"
	TargetGroupSuffix       = "scope"
	SrcGroupSuffix          = "src"
	DstGroupSuffix          = "dst"
	IpSetGroupSuffix        = "ipset"
	SharePrefix             = "share"
)

var (
	TagValueVersion                 = []string{ValueMajorVersion, ValueMinorVersion, ValuePatchVersion}
	TagValueScopeSecurityPolicyName = TagScopeSecurityPolicyCRName
	TagValueScopeSecurityPolicyUID  = TagScopeSecurityPolicyCRUID
)

var (
	ResourceType                       = "resource_type"
	ResourceTypeInfra                  = "Infra"
	ResourceTypeDomain                 = "Domain"
	ResourceTypeSecurityPolicy         = "SecurityPolicy"
	ResourceTypeNetworkPolicy          = "NetworkPolicy"
	ResourceTypeGroup                  = "Group"
	ResourceTypeRule                   = "Rule"
	ResourceTypeIPBlock                = "IpAddressBlock"
	ResourceTypeOrgRoot                = "OrgRoot"
	ResourceTypeOrg                    = "Org"
	ResourceTypeProject                = "Project"
	ResourceTypeVpc                    = "Vpc"
	ResourceTypeSubnetPort             = "VpcSubnetPort"
	ResourceTypeVirtualMachine         = "VirtualMachine"
	ResourceTypeLBService              = "LBService"
	ResourceTypeShare                  = "Share"
	ResourceTypeSharedResource         = "SharedResource"
	ResourceTypeChildSharedResource    = "ChildSharedResource"
	ResourceTypeChildShare             = "ChildShare"
	ResourceTypeChildRule              = "ChildRule"
	ResourceTypeChildGroup             = "ChildGroup"
	ResourceTypeChildSecurityPolicy    = "ChildSecurityPolicy"
	ResourceTypeChildResourceReference = "ChildResourceReference"

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
	ReasonEdgeMissingInProject                     = "EdgeMissingInProject"
	ReasonDistributedGatewayConnectionNotSupported = "DistributedGatewayConnectionNotSupported"
	ReasonGatewayConnectionNotSet                  = "GatewayConnectionNotSet"
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
	ID                string
	ParentID          string
	PrivateIpv4Blocks []string
}

type VPCNetworkConfigInfo struct {
	IsDefault              bool
	Org                    string
	Name                   string
	VPCConnectivityProfile string
	NSXProject             string
	PrivateIPs             []string
	DefaultSubnetSize      int
	PodSubnetAccessMode    string
	ShortID                string
	VPCPath                string
}
