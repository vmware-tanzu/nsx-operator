// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package clients

import (
	"net/http"
	"net/url"
	"sync"
)

// VCClient provides methods to interact with vCenter API
type VCClient struct {
	url          *url.URL
	httpClient   *http.Client
	sessionMutex sync.Mutex
	sessionKey   string
}

// SupervisorInfo contains information about a supervisor cluster
type SupervisorInfo struct {
	Name         string `json:"name"`
	ConfigStatus string `json:"config_status"`
	K8sStatus    string `json:"kubernetes_status"`
}

// StoragePolicyInfo contains information about a storage policy
type StoragePolicyInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Policy      string `json:"policy"`
}

// SupervisorSummary contains summary information about a supervisor
type SupervisorSummary struct {
	ID   string         `json:"supervisor"`
	Info SupervisorInfo `json:"info"`
}

// InstancesIpv4Cidr represents an IPv4 CIDR block
type InstancesIpv4Cidr struct {
	Address string `json:"address"`
	Prefix  int64  `json:"prefix"`
}

// InstancesVpcConfig contains VPC configuration
type InstancesVpcConfig struct {
	PrivateCidrs []InstancesIpv4Cidr `json:"private_cidrs"`
}

// InstancesVpcNetworkInfo contains VPC network information
type InstancesVpcNetworkInfo struct {
	VpcConfig         InstancesVpcConfig `json:"vpc_config,omitempty"`
	Vpc               string             `json:"vpc,omitempty"`
	DefaultSubnetSize int64              `json:"default_subnet_size"`
}

// InstancesNetworkConfigInfo contains network configuration information
type InstancesNetworkConfigInfo struct {
	NetworkProvider string                  `json:"network_provider"`
	VpcNetwork      InstancesVpcNetworkInfo `json:"vpc_network"`
}

// InstancesStorageSpec contains storage specification
type InstancesStorageSpec struct {
	Policy string `json:"policy"`
	Limit  *int64 `json:"limit"`
}

// InstancesContentLibrarySpec contains content library specification
type InstancesContentLibrarySpec struct {
	ContentLibrary string `json:"content_library"`
	Writable       *bool  `json:"writable"`
	AllowImport    *bool  `json:"allow_import"`
}

// InstancesVMServiceSpec contains VM service specification
type InstancesVMServiceSpec struct {
	ContentLibraries []string `json:"content_libraries"`
	VmClasses        []string `json:"vm_classes"`
}

// VCNamespaceCreateSpec contains specification for creating a namespace
type VCNamespaceCreateSpec struct {
	Supervisor       string                        `json:"supervisor"`
	Namespace        string                        `json:"namespace"`
	NetworkSpec      InstancesNetworkConfigInfo    `json:"network_spec"`
	StorageSpecs     []InstancesStorageSpec        `json:"storage_specs"`
	ContentLibraries []InstancesContentLibrarySpec `json:"content_libraries"`
	VmServiceSpec    *InstancesVMServiceSpec       `json:"vm_service_spec"`
}

// VCNamespaceGetInfo contains information about a namespace
type VCNamespaceGetInfo struct {
	Supervisor  string                     `json:"supervisor"`
	NetworkSpec InstancesNetworkConfigInfo `json:"network_spec"`
}