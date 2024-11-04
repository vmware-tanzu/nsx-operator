/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	crdv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func Test_T1ToVPC(t *testing.T) {
	// Initialize input SecurityPolicy
	input := &v1alpha1.SecurityPolicy{
		Spec: v1alpha1.SecurityPolicySpec{
			Rules: []v1alpha1.SecurityPolicyRule{
				{
					Name: "ingress_isolation",
				},
			},
		},
	}

	output := T1ToVPC(input)

	// Verify the output
	assert.Equal(t, "crd.nsx.vmware.com/v1alpha1", output.APIVersion, "APIVersion should be set correctly")
	assert.Equal(t, (*crdv1alpha1.SecurityPolicy)(unsafe.Pointer(input)), output, "Conversion should produce the correct type")
	assert.Equal(t, input.Spec.Rules[0].Name, output.Spec.Rules[0].Name, "Field values should match after conversion")
}

func Test_VPCToT1(t *testing.T) {
	// Initialize input SecurityPolicy
	input := &crdv1alpha1.SecurityPolicy{
		Spec: crdv1alpha1.SecurityPolicySpec{
			Rules: []crdv1alpha1.SecurityPolicyRule{
				{
					Name: "egress_isolation",
				},
			},
		},
	}

	output := VPCToT1(input)

	// Verify the output
	assert.Equal(t, "nsx.vmware.com/v1alpha1", output.APIVersion, "APIVersion should be set correctly")
	assert.Equal(t, (*v1alpha1.SecurityPolicy)(unsafe.Pointer(input)), output, "Conversion should produce the correct type")
	assert.Equal(t, input.Spec.Rules[0].Name, output.Spec.Rules[0].Name, "Field values should match after conversion")
}
