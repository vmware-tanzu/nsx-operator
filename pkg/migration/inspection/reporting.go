/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package inspection

import (
	"context"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	migrationstatus "github.com/vmware-tanzu/nsx-operator/pkg/migration/status"
)

// +kubebuilder:rbac:groups=migration.network.vmware.com,resources=controlplanenetworktransitions/status;namespacenetworktransitions/status,verbs=patch

// EvaluateControlPlane inspects WCP-resolved source VDN identities and persists
// diagnostics against this exact CR snapshot. The handler must resolve CPVM and
// Supervisor Service network membership before calling; networkConfiguration is not scope.
// workloadTopologies may be supplied from namespace inspection to allow the SNAT
// policy to recognize an FLB backend that is also used by a workload.
func (service Service) EvaluateControlPlane(ctx context.Context, snapshot *api.ControlPlaneNetworkTransition, sourceVDNs []string, workloadTopologies ...Topology) (Result, error) {
	if snapshot == nil || snapshot.Spec.Action != api.ActionEvaluate {
		return Result{}, fmt.Errorf("Evaluate snapshot is required")
	}
	if service.StatusClient == nil {
		return Result{}, fmt.Errorf("status client is required")
	}
	var dhcpInputs []api.DHCPNetworkConfiguration
	var resourceNames []api.VDNResourceNaming
	if snapshot.Spec.NetworkConfiguration != nil {
		dhcpInputs = snapshot.Spec.NetworkConfiguration.ControlPlaneAndServiceDHCPNetworks
		if snapshot.Spec.NetworkConfiguration.ResourceNaming != nil {
			resourceNames = snapshot.Spec.NetworkConfiguration.ResourceNaming.Networks
		}
	}
	inspectionResult, discoveryErr := service.inspectForEvaluate(ctx, sourceVDNs, dhcpInputs, resourceNames)
	if discoveryErr == nil {
		// The control-plane transition owns the Supervisor's single LB VPC and
		// its single SNAT IP. Reuse its accepted allocation on reevaluation;
		// namespace evaluations never choose or allocate another SNAT IP.
		var snatDecision LBSNATDecision
		var acceptedAllocations []api.IPAllocationStatus
		if snapshot.Status != nil {
			acceptedAllocations = snapshot.Status.LBSNATIPAllocations
		}
		snatDecision, discoveryErr = acceptedLBSNATDecision(acceptedAllocations)
		if discoveryErr == nil && snatDecision.Selection == "" {
			if service.FLBNetworkInventory == nil {
				discoveryErr = fmt.Errorf("source load-balancer inventory is required for Evaluate")
			} else {
				var flbNetworks FLBNetworks
				flbNetworks, discoveryErr = service.FLBNetworkInventory.LoadBalancerNetworks(ctx)
				if discoveryErr == nil {
					workloadTopologies = append(workloadTopologies, inspectionResult.SourceTopologies...)
					var configuredIPs []string
					if snapshot.Spec.NetworkConfiguration != nil {
						configuredIPs = snapshot.Spec.NetworkConfiguration.LBSNATIPs
					}
					snatDecision, discoveryErr = SelectLBSNATSource(flbNetworks, workloadTopologies, configuredIPs)
				}
			}
		}
		if discoveryErr == nil && snatDecision.Selection != "" {
			inspectionResult.LBSNAT = &snatDecision
			if snatDecision.Warning != "" {
				inspectionResult.Warnings = append(inspectionResult.Warnings, snatDecision.Warning)
			}
			if snatDecision.Selection == AllocateFromVIPRange {
				inspectionResult.OptionalInputs = append(inspectionResult.OptionalInputs, api.OptionalInput{
					FieldName:   "spec.networkConfiguration.lbSNATIPs",
					Description: "Optionally supply an IPv4 LB SNAT IP; if omitted, the migrator allocates one from the FLB VIP range. Verify routing from the FLB backend to that address.",
				})
			}
		}
	}
	inspectionResult.qualifyInputs("controlPlaneAndServiceDHCPNetworks")
	_, err := migrationstatus.UpdateControlPlane(ctx, service.StatusClient, snapshot, func(target *api.ControlPlaneNetworkTransitionStatus) error {
		return report(inspectionResult, discoveryErr, &target.TransitionStatus, snapshot.Generation)
	})
	return inspectionResult, errors.Join(discoveryErr, err)
}

func acceptedLBSNATDecision(allocations []api.IPAllocationStatus) (LBSNATDecision, error) {
	if len(allocations) == 0 {
		return LBSNATDecision{}, nil
	}
	if len(allocations) != 1 {
		return LBSNATDecision{}, fmt.Errorf("expected at most one accepted LB SNAT allocation, found %d", len(allocations))
	}
	if _, err := ipv4(allocations[0].IPAddress); err != nil {
		return LBSNATDecision{}, fmt.Errorf("accepted LB SNAT IP must be IPv4: %w", err)
	}
	return LBSNATDecision{
		Selection:     ReuseAcceptedLBSNATIP,
		IPAddress:     allocations[0].IPAddress,
		AllocationRef: allocations[0].AllocationRef,
	}, nil
}

// EvaluateNamespaces discovers namespace associations and reports the shared
// inspection milestone. It leaves action checkpoints and namespace execution
// progress to the stage handler. LB SNAT selection belongs to the single
// control-plane transition, so namespace evaluation never selects an address.
// Conflicts require fresh discovery on retry.
func (service Service) EvaluateNamespaces(ctx context.Context, snapshot *api.NamespaceNetworkTransition) (Result, error) {
	if snapshot == nil || snapshot.Spec.Action != api.ActionEvaluate {
		return Result{}, fmt.Errorf("Evaluate snapshot is required")
	}
	if service.StatusClient == nil {
		return Result{}, fmt.Errorf("status client is required")
	}
	var dhcpInputs []api.DHCPNetworkConfiguration
	var resourceNames []api.VDNResourceNaming
	if snapshot.Spec.NetworkConfiguration != nil {
		dhcpInputs = snapshot.Spec.NetworkConfiguration.NamespaceDHCPNetworks
		if snapshot.Spec.NetworkConfiguration.ResourceNaming != nil {
			resourceNames = snapshot.Spec.NetworkConfiguration.ResourceNaming.Networks
		}
	}
	var inspectionResult Result
	var discoveryErr error
	if service.Inventory == nil {
		discoveryErr = fmt.Errorf("source network inventory is required for Evaluate")
	} else if service.TargetInventory == nil {
		discoveryErr = fmt.Errorf("target topology inventory is required for Evaluate")
	} else {
		var sourceVDNs []string
		sourceVDNs, discoveryErr = service.Inventory.NamespaceVDNs(ctx, snapshot.Spec.Namespaces)
		if discoveryErr == nil {
			inspectionResult, discoveryErr = service.inspectForEvaluate(ctx, sourceVDNs, dhcpInputs, resourceNames)
		}
	}
	inspectionResult.qualifyInputs("namespaceDHCPNetworks")
	_, err := migrationstatus.UpdateNamespace(ctx, service.StatusClient, snapshot, func(target *api.NamespaceNetworkTransitionStatus) error {
		return report(inspectionResult, discoveryErr, &target.TransitionStatus, snapshot.Generation)
	})
	return inspectionResult, errors.Join(discoveryErr, err)
}

func (service Service) inspectForEvaluate(ctx context.Context, sourceVDNs []string, dhcpInputs []api.DHCPNetworkConfiguration, resourceNames []api.VDNResourceNaming) (Result, error) {
	if service.TargetInventory == nil {
		return Result{}, fmt.Errorf("target topology inventory is required for Evaluate")
	}
	return service.InspectWithNaming(ctx, sourceVDNs, dhcpInputs, resourceNames)
}

// qualifyInputs points each diagnostic to the corresponding VDN in the CR spec.
func (result *Result) qualifyInputs(inputField string) {
	for inputIndex := range result.RequiredInputs {
		requiredInput := &result.RequiredInputs[inputIndex]
		vdnName := strings.TrimPrefix(requiredInput.FieldName, inputPrefix)
		requiredInput.FieldName = fmt.Sprintf("spec.networkConfiguration.%s[vdnName=%s]", inputField, vdnName)
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
	clearInspectionInputs(target)
	return migrationstatus.Observe(target, generation, api.PhaseBlocked, metav1.Condition{Type: ConditionNetworkInputsValidated, Status: metav1.ConditionUnknown, Reason: "NetworkDiscoveryUnavailable", Message: "Source network discovery is unavailable; retry inspection"})
}

func clearInspectionInputs(target *api.TransitionStatus) {
	retainedRequiredInputs := target.RequiredInputs[:0]
	for _, input := range target.RequiredInputs {
		if strings.HasPrefix(input.FieldName, inputPrefix) || strings.HasPrefix(input.FieldName, "namespaceDHCPNetworks/") || strings.HasPrefix(input.FieldName, "controlPlaneAndServiceDHCPNetworks/") || strings.HasPrefix(input.FieldName, "spec.networkConfiguration.namespaceDHCPNetworks[") || strings.HasPrefix(input.FieldName, "spec.networkConfiguration.controlPlaneAndServiceDHCPNetworks[") {
			continue
		}
		retainedRequiredInputs = append(retainedRequiredInputs, input)
	}
	target.RequiredInputs = retainedRequiredInputs
	retainedOptionalInputs := target.OptionalInputs[:0]
	for _, input := range target.OptionalInputs {
		if strings.HasPrefix(input.FieldName, "spec.networkConfiguration.resourceNaming.networks[") || input.FieldName == "spec.networkConfiguration.lbSNATIPs" {
			continue
		}
		retainedOptionalInputs = append(retainedOptionalInputs, input)
	}
	target.OptionalInputs = retainedOptionalInputs
}
