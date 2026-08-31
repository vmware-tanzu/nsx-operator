/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

// StatefulSetPodSubnetPortFeatureEnabled is true when NSX supports reusing subnet ports for statefulset pod.
//
//go:noinline
func StatefulSetPodSubnetPortFeatureEnabled(client *Client) bool {
	if client == nil {
		return false
	}
	return client.NSXCheckVersion(StatefulSetPod)
}
