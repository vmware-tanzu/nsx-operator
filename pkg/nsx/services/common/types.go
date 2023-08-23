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
	HashLength                      int    = 8
	MaxTagLength                    int    = 256
	MaxIdLength                     int    = 255
	MaxNameLength                   int    = 255
	MaxSubnetNameLength             int    = 80
	TagScopeNCPCluster              string = "ncp/cluster"
	TagScopeNCPProjectUID           string = "ncp/project_uid"
	TagScopeNCPVIFProjectUID        string = "ncp/vif_project_uid"
	TagScopeNCPPod                  string = "ncp/pod"
	TagScopeNCPVNETInterface        string = "ncp/vnet_interface"
	TagScopeVersion                 string = "nsx-op/version"
	TagScopeCluster                 string = "nsx-op/cluster"
	TagScopeNamespace               string = "nsx-op/namespace"
	TagScopeNamespaceUID            string = "nsx-op/namespace_uid"
	TagScopeSecurityPolicyCRName    string = "nsx-op/security_policy_cr_name"
	TagScopeSecurityPolicyCRUID     string = "nsx-op/security_policy_cr_uid"
	TagScopeStaticRouteCRName       string = "nsx-op/static_route_cr_name"
	TagScopeStaticRouteCRUID        string = "nsx-op/static_route_cr_uid"
	TagScopeRuleID                  string = "nsx-op/rule_id"
	TagScopeGoupID                  string = "nsx-op/group_id"
	TagScopeGroupType               string = "nsx-op/group_type"
	TagScopeSelectorHash            string = "nsx-op/selector_hash"
	TagScopeNSXServiceAccountCRName string = "nsx-op/nsx_service_account_name"
	TagScopeNSXServiceAccountCRUID  string = "nsx-op/nsx_service_account_uid"
	TagScopeNSXProjectID            string = "nsx-op/nsx_project_id"
	TagScopeProjectGroupShared      string = "nsx-op/is_nsx_project_shared"
	TagScopeVPCCRName               string = "nsx-op/vpc_cr_name"
	TagScopeVPCCRUID                string = "nsx-op/vpc_cr_uid"
	TagScopeSubnetPortCRName        string = "nsx-op/subnetport_cr_name"
	TagScopeSubnetPortCRUID         string = "nsx-op/subnetport_cr_uid"
	TagScopeIPPoolCRName            string = "nsx-op/ippool_cr_name"
	TagScopeIPPoolCRUID             string = "nsx-op/ippool_cr_uid"
	TagScopeIPPoolCRType            string = "nsx-op/ippool_cr_type"
	TagScopeIPSubnetName            string = "nsx-op/ipsubnet_cr_name"
	TagScopeVMNamespaceUID          string = "nsx-op/vm_namespace_uid"
	TagScopeVMNamespace             string = "nsx-op/vm_namespace"
	LabelDefaultSubnetSet           string = "nsxoperator.vmware.com/default-subnetset-for"
	LabelDefaultVMSubnetSet         string = "VirtualMachine"
	LabelDefaultPodSubnetSet        string = "Pod"
	TagScopeSubnetCRType            string = "nsx-op/subnet_cr_type"
	TagScopeSubnetCRUID             string = "nsx-op/subnet_cr_uid"
	TagScopeSubnetCRName            string = "nsx-op/subnet_cr_name"
	TagScopeSubnetSetCRName         string = "nsx-op/subnetset_cr_name"
	TagScopeSubnetSetCRUID          string = "nsx-op/subnetset_cr_uid"
	TagValueGroupScope              string = "scope"
	TagValueGroupSrc                string = "source"
	TagValueGroupDst                string = "destination"
	AnnotationVPCNetworkConfig      string = "nsx.vmware.com/vpc_network_config"
	AnnotationVPCName               string = "nsx.vmware.com/vpc_name"
	AnnotationPodMAC                string = "nsx.vmware.com/mac"
	AnnotationPodAttachment         string = "nsx.vmware.com/attachment"
	DefaultNetworkConfigName        string = "default"
	TagScopePodName                 string = "nsx-op/pod_name"
	TagScopePodUID                  string = "nsx-op/pod_uid"
	ValueMajorVersion               string = "1"
	ValueMinorVersion               string = "0"
	ValuePatchVersion               string = "0"

	GCInterval          = 60 * time.Second
	RealizeTimeout      = 2 * time.Minute
	RealizeMaxRetries   = 3
	IPPoolFinalizerName = "ippool.nsx.vmware.com/finalizer"
	DefaultSNATID       = "DEFAULT"
	AVISubnetLBID       = "_AVI_SUBNET--LB"
	IPPoolTypePublic    = "Public"
	IPPoolTypePrivate   = "Private"

	SecurityPolicyFinalizerName    = "securitypolicy.nsx.vmware.com/finalizer"
	StaticRouteFinalizerName       = "staticroute.nsx.vmware.com/finalizer"
	NSXServiceAccountFinalizerName = "nsxserviceaccount.nsx.vmware.com/finalizer"
	SubnetFinalizerName            = "subnet.nsx.vmware.com/finalizer"
	SubnetSetFinalizerName         = "subnetset.nsx.vmware.com/finalizer"
	SubnetPortFinalizerName        = "subnetport.nsx.vmware.com/finalizer"
	VPCFinalizerName               = "vpc.nsx.vmware.com/finalizer"
	PodFinalizerName               = "pod.nsx.vmware.com/finalizer"

	IndexKeySubnetID            = "IndexKeySubnetID"
	IndexKeyPathPath            = "Path"
	IndexKeyNodeName            = "IndexKeyNodeName"
	GCValidationInterval uint16 = 720
)

var TagValueVersion = []string{ValueMajorVersion, ValueMinorVersion, ValuePatchVersion}

var (
	ResourceType                    = "resource_type"
	ResourceTypeSecurityPolicy      = "SecurityPolicy"
	ResourceTypeGroup               = "Group"
	ResourceTypeRule                = "Rule"
	ResourceTypeIPBlock             = "IpAddressBlock"
	ResourceTypeOrgRoot             = "OrgRoot"
	ResourceTypeOrg                 = "Org"
	ResourceTypeProject             = "Project"
	ResourceTypeVpc                 = "Vpc"
	ResourceTypeSubnetPort          = "VpcSubnetPort"
	ResourceTypeVirtualMachine      = "VirtualMachine"
	ResourceTypeShare               = "Share"
	ResourceTypeSharedResource      = "SharedResource"
	ResourceTypeChildSharedResource = "ChildSharedResource"
	ResourceTypeChildShare          = "ChildShare"

	// ResourceTypeClusterControlPlane is used by NSXServiceAccountController
	ResourceTypeClusterControlPlane = "clustercontrolplane"
	// ResourceTypePrincipalIdentity is used by NSXServiceAccountController, and it is MP resource type.
	ResourceTypePrincipalIdentity = "principalidentity"
	ResourceTypeSubnet            = "VpcSubnet"
	ResourceTypeIPPool            = "IpAddressPool"
	ResourceTypeIPPoolBlockSubnet = "IpAddressPoolBlockSubnet"
	ResourceTypeNode              = "HostTransportNode"
)

type Service struct {
	Client    client.Client
	NSXClient *nsx.Client
	NSXConfig *config.NSXOperatorConfig
}

func NewConverter() *bindings.TypeConverter {
	converter := bindings.NewTypeConverter()
	converter.SetMode(bindings.REST)
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
	ID       string
	ParentID string
}
