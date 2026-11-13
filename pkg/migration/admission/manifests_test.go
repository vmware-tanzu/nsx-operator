/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
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
	data, err = os.ReadFile("../../../build/yaml/migration/ncp-container.patch.yaml")
	require.NoError(t, err)
	var deployment appsv1.Deployment
	require.NoError(t, yaml.Unmarshal(data, &deployment))
	var migrationContainer *corev1.Container
	for i := range deployment.Spec.Template.Spec.Containers {
		if deployment.Spec.Template.Spec.Containers[i].Name == "supervisor-network-migration-operator" {
			migrationContainer = &deployment.Spec.Template.Spec.Containers[i]
			break
		}
	}
	require.NotNil(t, migrationContainer)
	require.Contains(t, migrationContainer.Args, "--enable-migration-admission")
	require.Contains(t, migrationContainer.Args, "--migration-admission-port=9982")
	require.Contains(t, migrationContainer.Args,
		"--migration-admission-cert-dir=/etc/nsx-operator/migration-webhook-certs")
	require.Contains(t, migrationContainer.Ports, corev1.ContainerPort{
		Name: "migration-admission", ContainerPort: 9982, Protocol: corev1.ProtocolTCP,
	})
	require.Contains(t, migrationContainer.VolumeMounts, corev1.VolumeMount{
		Name:      "migration-admission-certs",
		MountPath: "/etc/nsx-operator/migration-webhook-certs", ReadOnly: true,
	})
	require.Len(t, deployment.Spec.Template.Spec.Volumes, 1)
	data, err = os.ReadFile("../../../build/yaml/migration/admission/certificate.yaml")
	require.NoError(t, err)
	var certificate struct {
		Spec struct {
			SecretName string `yaml:"secretName"`
		} `yaml:"spec"`
	}
	certificateDocs := strings.SplitN(string(data), "---", 2)
	require.Len(t, certificateDocs, 2)
	require.NoError(t, yaml.Unmarshal([]byte(certificateDocs[1]), &certificate))
	require.Equal(t, certificate.Spec.SecretName,
		deployment.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	data, err = os.ReadFile("../../../build/yaml/migration/admission/kustomization.yaml")
	require.NoError(t, err)
	var kustomization struct {
		Resources []string `yaml:"resources"`
	}
	require.NoError(t, yaml.Unmarshal(data, &kustomization))
	require.NotContains(t, kustomization.Resources, "webhook.yaml",
		"the fail-closed webhook must be installed only after serving Pods are ready")
}
