/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	migrationstatus "github.com/vmware-tanzu/nsx-operator/pkg/migration/status"
)

// +kubebuilder:rbac:groups=migration.network.vmware.com,resources=controlplanenetworktransitions/status;namespacenetworktransitions/status,verbs=patch

// EvaluateControlPlane inspects WCP-resolved source VDN identities and persists
// diagnostics against this exact CR snapshot. The handler must resolve CPVM and
// Supervisor Service network membership before calling; networkConfiguration is not scope.
func (service Service) EvaluateControlPlane(ctx context.Context, kubeClient client.Client, snapshot *api.ControlPlaneNetworkTransition, sourceVDNs []string) (Result, error) {
	if snapshot == nil || snapshot.Spec.Action != api.ActionEvaluate {
		return Result{}, fmt.Errorf("Evaluate snapshot is required")
	}
	var dhcpInputs []api.DHCPNetworkConfiguration
	if snapshot.Spec.NetworkConfiguration != nil {
		dhcpInputs = snapshot.Spec.NetworkConfiguration.ControlPlaneAndServiceDHCPNetworks
	}
	inspectionResult, discoveryErr := service.Inspect(ctx, sourceVDNs, dhcpInputs)
	inspectionResult.qualifyInputs("controlPlaneAndServiceDHCPNetworks")
	_, err := migrationstatus.UpdateControlPlane(ctx, kubeClient, snapshot, func(target *api.ControlPlaneNetworkTransitionStatus) error {
		return report(inspectionResult, discoveryErr, &target.TransitionStatus, snapshot.Generation)
	})
	return inspectionResult, errors.Join(discoveryErr, err)
}

// EvaluateNamespaces discovers namespace associations and reports the shared
// inspection milestone. It leaves action checkpoints and namespace execution
// progress to the stage handler. Conflicts require fresh discovery on retry.
func (service Service) EvaluateNamespaces(ctx context.Context, kubeClient client.Client, snapshot *api.NamespaceNetworkTransition) (Result, error) {
	if snapshot == nil || snapshot.Spec.Action != api.ActionEvaluate {
		return Result{}, fmt.Errorf("Evaluate snapshot is required")
	}
	var dhcpInputs []api.DHCPNetworkConfiguration
	if snapshot.Spec.NetworkConfiguration != nil {
		dhcpInputs = snapshot.Spec.NetworkConfiguration.NamespaceDHCPNetworks
	}
	inspectionResult, discoveryErr := service.InspectNamespaces(ctx, snapshot.Spec.Namespaces, dhcpInputs)
	inspectionResult.qualifyInputs("namespaceDHCPNetworks")
	_, err := migrationstatus.UpdateNamespace(ctx, kubeClient, snapshot, func(target *api.NamespaceNetworkTransitionStatus) error {
		return report(inspectionResult, discoveryErr, &target.TransitionStatus, snapshot.Generation)
	})
	return inspectionResult, errors.Join(discoveryErr, err)
}

// qualifyInputs anchors stable per-VDN diagnostic keys to the CR's input field.
// Hash suffixes keep keys bounded even for maximum-length VDN names; descriptions
// retain the actual VDN name and missing fields for administrators.
func (result *Result) qualifyInputs(inputField string) {
	for inputIndex := range result.RequiredInputs {
		requiredInput := &result.RequiredInputs[inputIndex]
		requiredInput.Key = inputField + "/" + strings.TrimPrefix(requiredInput.Key, inputPrefix)
		requiredInput.Description += " at spec.networkConfiguration." + inputField
	}
}

func report(result Result, discoveryErr error, target *api.TransitionStatus, generation int64) error {
	if discoveryErr == nil {
		return result.Apply(target, generation)
	}
	var configuration *ConfigurationError
	if errors.As(discoveryErr, &configuration) {
		return (Result{Problems: []string{discoveryErr.Error()}}).Apply(target, generation)
	}
	// Do not echo transport errors: they can contain credentials or very large
	// server responses. The returned error is available to the handler's logger.
	return migrationstatus.Observe(target, generation, api.PhaseBlocked, metav1.Condition{Type: ConditionNetworkInputsValidated, Status: metav1.ConditionUnknown, Reason: "NetworkDiscoveryUnavailable", Message: "Source network discovery is unavailable; retry inspection"})
}
