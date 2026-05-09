/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import "github.com/vmware-tanzu/nsx-operator/pkg/config"

// StatefulSetPodSubnetPortFeatureEnabled is true when NSX supports StatefulSet pod SubnetPorts
// and operator nsx_v3 sets vpc_wcp_enhance to true (omitted or false keeps the feature off).
//
//go:noinline
func StatefulSetPodSubnetPortFeatureEnabled(client *Client, operatorConfig *config.NSXOperatorConfig) bool {
	if client == nil || !client.NSXCheckVersion(StatefulSetPod) {
		return false
	}
	if operatorConfig == nil || operatorConfig.NsxConfig == nil {
		return false
	}
	return operatorConfig.NsxConfig.VpcWcpEnhanceEnabled()
}

// RestoreVifFeatureEnabled is true when NSX supports restoring SubnetPort vif and
// operator config set restore_vif to true.
func RestoreVifFeatureEnabled(client *Client, operatorConfig *config.NSXOperatorConfig) bool {
	if client == nil || !client.NSXCheckVersion(RestoreVIF) {
		return false
	}
	if operatorConfig == nil || operatorConfig.NsxConfig == nil {
		return false
	}
	return operatorConfig.NsxConfig.RestoreVifEnabled()
}
