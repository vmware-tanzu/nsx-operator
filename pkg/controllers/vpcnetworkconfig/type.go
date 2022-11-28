/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package vpcnetworkconfig

type VPCNetworkConfigInfo struct {
	name                    string
	namespace               string
	defaultGatewayPath      string
	edgeClusterPath         string
	nsxtProject             string
	publicIPv4Blocks        []string
	privateIPv4CIDRs        []string
	defaultIPv4SubnetSize   int
	defaultSubnetAccessMode string
}

type VPCNetworkConfigInfoStore interface {
	AddVPCNetworkConfigInfo(networkConfig *VPCNetworkConfigInfo)
	DeleteAddVPCNetworkConfigInfo(networkConfig *VPCNetworkConfigInfo)
	GetVPCNetworkConfigInfoPerNamespace(namespace string) *VPCNetworkConfigInfo
}
