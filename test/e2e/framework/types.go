// Copyright © 2019-2021 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

// Package framework provides the core functionality for e2e tests
package framework

import (
	"net"
	"time"

	corev1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"

	"github.com/vmware-tanzu/nsx-operator/pkg/client/clientset/versioned"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/test/e2e/clients"
)

var Log = &logger.Log

const (
	// DefaultTimeout is the default timeout for operations
	DefaultTimeout = 600 * time.Second
	// PolicyAPI is the NSX policy API path
	PolicyAPI = "policy/api/v1"
)

// Status represents the status of a resource
type Status int

const (
	// Ready indicates the resource is ready
	Ready Status = iota
	// Deleted indicates the resource is deleted
	Deleted
)

// ClusterNode represents a node in the Kubernetes cluster
type ClusterNode struct {
	// Idx is the index of the node (0 for master node)
	Idx int
	// Name is the name of the node
	Name string
	// UID is the UID of the node
	UID string
}

// ClusterInfo stores information about the Kubernetes cluster
type ClusterInfo struct {
	// NumWorkerNodes is the number of worker nodes in the cluster
	NumWorkerNodes int
	// NumNodes is the total number of nodes in the cluster
	NumNodes int
	// PodV4NetworkCIDR is the IPv4 CIDR for pods
	PodV4NetworkCIDR string
	// PodV6NetworkCIDR is the IPv6 CIDR for pods
	PodV6NetworkCIDR string
	// MasterNodeName is the name of the master node
	MasterNodeName string
	// Nodes is a map of node index to ClusterNode
	Nodes map[int]ClusterNode
	// K8sServerVersion is the Kubernetes server version
	K8sServerVersion string
}

// ClusterInfoData is the global cluster information
var ClusterInfoData ClusterInfo

// TestOptions stores command-line options for e2e tests
type TestOptions struct {
	// ProviderName is the name of the provider
	ProviderName string
	// ProviderConfigPath is the path to the provider configuration
	ProviderConfigPath string
	// LogsExportDir is the directory to export logs to
	LogsExportDir string
	// OperatorConfigPath is the path to the operator configuration
	OperatorConfigPath string
	// VCUser is the vCenter user
	VCUser string
	// VCPassword is the vCenter password
	VCPassword string
	// LogsExportOnSuccess indicates whether to export logs on success
	LogsExportOnSuccess bool
	// DebugLog indicates whether to enable debug logging
	DebugLog bool
}

// TestData stores the state required for each test case
type TestData struct {
	// KubeConfig is the Kubernetes client configuration
	KubeConfig *restclient.Config
	// ClientSet is the Kubernetes client
	ClientSet clientset.Interface
	// CRDClientset is the CRD client
	CRDClientset versioned.Interface
	// NSXClient is the NSX client
	NSXClient *clients.NSXClient
	// VCClient is the vCenter client
	VCClient *clients.VCClient
}

// PodCondition is a function type that evaluates a Pod and returns whether a condition is met
type PodCondition func(*corev1.Pod) (bool, error)

// PodIPs stores the IP addresses assigned to a Pod
type PodIPs struct {
	// IPv4 is the IPv4 address of the Pod
	IPv4 *net.IP
	// IPv6 is the IPv6 address of the Pod
	IPv6 *net.IP
	// IPStrings is a list of IP address strings
	IPStrings []string
}
