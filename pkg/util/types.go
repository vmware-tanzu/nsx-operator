/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import "time"

const (
	HashLength                   int    = 8
	MaxTagLength                 int    = 256
	TagScopeCluster              string = "nsx-op/cluster"
	TagScopeNamespace            string = "nsx-op/namespace"
	TagScopeSecurityPolicyCRName string = "nsx-op/security_policy_cr_name"
	TagScopeSecurityPolicyCRUID  string = "nsx-op/security_policy_cr_uid"
	TagScopeRuleID               string = "nsx-op/rule_id"
	TagScopeGroupType            string = "nsx-op/group_type"
	TagScopeSelectorHash         string = "nsx-op/selector_hash"
	TagScopeNCPCluster           string = "ncp/cluster"
	TagScopeNCPProject           string = "ncp/project"
	TagScopeNCPVIFProject        string = "ncp/vif_project"
	TagScopeNCPPod               string = "ncp/pod"
	TagScopeNCPVNETInterface     string = "ncp/vnet_interface"

	GCInterval    = 60 * time.Second
	FinalizerName = "securitypolicy.nsx.vmware.com/finalizer"
)

// Address is used when named port is specified.
type Address struct {
	// Port is the port number.
	Port int `json:"port"`
	// IPs is a list of IPs.
	IPs []string `json:"ips"`
}
