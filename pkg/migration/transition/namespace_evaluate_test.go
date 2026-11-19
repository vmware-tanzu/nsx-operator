/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIncompatibleVMReasonsUsesNetworkInterfaceCardType(t *testing.T) {
	vm := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec": map[string]interface{}{
			"networkInterfaces": []interface{}{
				map[string]interface{}{"ethernetCardType": "SR-IOV"},
				map[string]interface{}{"ethernetCardType": "Dynamic.DirectPath-IO"},
				map[string]interface{}{"ethernetCardType": "Virtual_PCI_Passthrough"},
			},
		},
	}}

	require.Equal(t, []string{
		"DynamicDirectPath I/O network adapter",
		"PCI passthrough network device",
		"SR-IOV virtual NIC",
	}, incompatibleVMReasons(vm))
}
