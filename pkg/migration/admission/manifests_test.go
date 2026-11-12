/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

func TestTransitionValidationDeploymentManifests(t *testing.T) {
	data, err := os.ReadFile("../../../build/yaml/migration/admission/webhook.yaml")
	require.NoError(t, err)
	var config admissionv1.ValidatingWebhookConfiguration
	require.NoError(t, yaml.Unmarshal(data, &config))
	require.Len(t, config.Webhooks, 1)
	hook := config.Webhooks[0]
	require.Equal(t, admissionv1.Fail, *hook.FailurePolicy)
	require.Equal(t, admissionv1.SideEffectClassNone, *hook.SideEffects)
	require.Equal(t, TransitionValidationPath, *hook.ClientConfig.Service.Path)
	require.Equal(t, "vmware-system-nsx", hook.ClientConfig.Service.Namespace)
	require.Len(t, hook.Rules, 1)
	require.Equal(t, []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, hook.Rules[0].Operations)
	require.ElementsMatch(t, []string{"controlplanenetworktransitions", "namespacenetworktransitions"}, hook.Rules[0].Resources)
	data, err = os.ReadFile("../../../build/yaml/migration/admission/service.yaml")
	require.NoError(t, err)
	var svc corev1.Service
	require.NoError(t, yaml.Unmarshal(data, &svc))
	require.Equal(t, hook.ClientConfig.Service.Name, svc.Name)
	require.Equal(t, int32(9982), svc.Spec.Ports[0].TargetPort.IntVal)
}
