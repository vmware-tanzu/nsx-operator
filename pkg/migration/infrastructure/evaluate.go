/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

const ConditionDVPGConversionValidated = "DVPGConversionValidated"

// ControlPlaneScope discovers the VDNs used by CPVMs and Supervisor Services.
// It must read their actual membership; spec.networkConfiguration is only input.
type ControlPlaneScope interface {
	SourceNetworks(context.Context) ([]string, []inspection.Topology, error)
}

type ControlPlaneInspector interface {
	InspectControlPlane(context.Context, *api.ControlPlaneNetworkTransition, []string, ...inspection.Topology) (inspection.Result, error)
}

// DVPGCompatibilityValidator runs NSX's read-only DVPG VALIDATION phase.
type DVPGCompatibilityValidator interface {
	Validate(context.Context, string, int64, []inspection.Network) (DVPGValidationResult, error)
}

type DVPGValidationResult struct {
	InProgress bool
	Problems   []string
	Warnings   []string
}

// SourceSNAT ensures the one source reservation selected by inspection. The
// accepted status is checked on each call, including after process restart.
type SourceSNAT interface {
	Ensure(context.Context, *api.ControlPlaneNetworkTransition, inspection.LBSNATDecision) (api.IPAllocationStatus, bool, error)
}

// EvaluateHandler runs Stage 1 source checks. It never prepares target fabric.
type EvaluateHandler struct {
	Scope         ControlPlaneScope
	Inspector     ControlPlaneInspector
	DVPGValidator DVPGCompatibilityValidator
	SNAT          SourceSNAT
}

func (handler EvaluateHandler) Reconcile(ctx context.Context, request transition.Request) (transition.Result, error) {
	resource, ok := request.Object.(*api.ControlPlaneNetworkTransition)
	if !ok || request.Stage != transition.Infrastructure || request.Action != api.ActionEvaluate || resource.Status == nil || request.Status == nil {
		return transition.Result{}, fmt.Errorf("control-plane Evaluate transition is required")
	}
	if handler.Scope == nil || handler.Inspector == nil || handler.DVPGValidator == nil || handler.SNAT == nil {
		return transition.Result{}, fmt.Errorf("control-plane Evaluate services are not configured")
	}
	vDNs, workloadTopologies, err := handler.Scope.SourceNetworks(ctx)
	if err != nil {
		if applyErr := inspection.ApplyEvaluation(inspection.Result{}, err, request.Status, request.Generation); applyErr != nil {
			return transition.Result{}, applyErr
		}
		reportDVPGValidation(DVPGValidationResult{}, err, false, &request.Status.Conditions, request.Generation)
		meta.SetStatusCondition(&request.Status.Conditions, metav1.Condition{
			Type: "EvaluationPreview", Status: metav1.ConditionUnknown,
			Reason: "SourcePreviewUnavailable", Message: "Source network preview is unavailable until discovery succeeds",
			ObservedGeneration: request.Generation,
		})
		return transition.Result{}, fmt.Errorf("CPVM and Service network discovery is unavailable; retry Evaluate: %w", err)
	}
	findings, inspectionErr := handler.Inspector.InspectControlPlane(ctx, resource, vDNs, workloadTopologies...)
	var validation DVPGValidationResult
	var validationErr error
	if inspectionErr == nil && len(findings.Networks) > 0 {
		validation, validationErr = handler.DVPGValidator.Validate(ctx, string(resource.UID), request.Generation, findings.Networks)
		findings.Problems = append(findings.Problems, validation.Problems...)
		findings.Warnings = append(findings.Warnings, validation.Warnings...)
	}
	if err := inspection.ApplyEvaluation(findings, inspectionErr, request.Status, request.Generation); err != nil {
		return transition.Result{}, err
	}
	if inspectionErr != nil {
		reportDVPGValidation(DVPGValidationResult{}, inspectionErr, false, &request.Status.Conditions, request.Generation)
		return transition.Result{}, fmt.Errorf("source network inspection failed; inspect NetworkInputsValidated: %w", inspectionErr)
	}
	reportDVPGValidation(validation, validationErr, len(findings.Networks) > 0, &request.Status.Conditions, request.Generation)
	if validationErr != nil {
		return transition.Result{}, fmt.Errorf("DVPG conversion compatibility validation failed; retry Evaluate: %w", validationErr)
	}
	if findings.LBSNAT != nil {
		resource.Status.SourceLBProvider = api.LoadBalancerProvider("VSPHERE_FOUNDATION")
	}
	if len(findings.Networks) > 0 {
		meta.SetStatusCondition(&request.Status.Conditions, metav1.Condition{
			Type: "EvaluationPreview", Status: metav1.ConditionTrue,
			Reason: "SourcePreviewReady", Message: evaluationPreview(findings),
			ObservedGeneration: request.Generation,
		})
	} else {
		meta.SetStatusCondition(&request.Status.Conditions, metav1.Condition{
			Type: "EvaluationPreview", Status: metav1.ConditionUnknown,
			Reason: "SourcePreviewUnavailable", Message: "Source network preview is unavailable until discovery succeeds",
			ObservedGeneration: request.Generation,
		})
	}
	if len(findings.Problems) > 0 {
		return transition.Result{}, fmt.Errorf("source network configuration is incompatible; inspect NetworkInputsValidated")
	}
	if len(findings.RequiredInputs) > 0 {
		if validation.InProgress {
			return transition.Result{Outcome: transition.OutcomeInProgress, RequeueAfter: 5 * time.Second}, nil
		}
		return transition.Result{Outcome: transition.OutcomeNeedsInput}, nil
	}
	if validation.InProgress {
		return transition.Result{Outcome: transition.OutcomeInProgress, RequeueAfter: 5 * time.Second}, nil
	}
	if len(findings.MissingZones) > 0 {
		return transition.Result{Outcome: transition.OutcomeInProgress, RequeueAfter: 30 * time.Second}, nil
	}
	if !findings.Ready() || findings.LBSNAT == nil {
		return transition.Result{}, fmt.Errorf("source evaluation returned no usable network or LB SNAT decision")
	}
	allocation, ready, err := handler.SNAT.Ensure(ctx, resource, *findings.LBSNAT)
	if err != nil {
		return transition.Result{}, fmt.Errorf("LB SNAT source reservation failed; retry Evaluate: %w", err)
	}
	if !ready {
		return transition.Result{Outcome: transition.OutcomeInProgress, RequeueAfter: 5 * time.Second}, nil
	}
	resource.Status.LBSNATIPAllocations = []api.IPAllocationStatus{allocation}
	return transition.Result{Outcome: transition.OutcomeSucceeded}, nil
}

func reportDVPGValidation(result DVPGValidationResult, err error, foundNetworks bool, conditions *[]metav1.Condition, generation int64) {
	condition := metav1.Condition{
		Type:               ConditionDVPGConversionValidated,
		ObservedGeneration: generation,
	}
	switch {
	case err != nil:
		condition.Status = metav1.ConditionUnknown
		condition.Reason = "ValidationUnavailable"
		condition.Message = "DVPG conversion compatibility validation is unavailable; Evaluate will retry"
	case !foundNetworks:
		condition.Status = metav1.ConditionUnknown
		condition.Reason = "NoSourceDVPGs"
		condition.Message = "No source DVPGs were available for conversion validation"
	case result.InProgress:
		condition.Status = metav1.ConditionUnknown
		condition.Reason = "ValidationInProgress"
		condition.Message = "NSX DVPG conversion compatibility validation is still running"
	case len(result.Problems) > 0:
		condition.Status = metav1.ConditionFalse
		condition.Reason = "IncompatibleDVPGs"
		condition.Message = strings.Join(result.Problems, "; ")
	default:
		condition.Status = metav1.ConditionTrue
		condition.Reason = "DVPGsCompatible"
		condition.Message = "All discovered DVPGs are supported for conversion"
	}
	if len(condition.Message) > 4096 {
		condition.Message = condition.Message[:4093] + "..."
	}
	meta.SetStatusCondition(conditions, condition)
}

func evaluationPreview(findings inspection.Result) string {
	parts := make([]string, 0, len(findings.Networks)+1)
	for _, network := range findings.Networks {
		target := "reuse target TGW/VPC"
		if network.Target.CreateTGW || network.Target.CreateVPC {
			target = "prepare target TGW/VPC during Execute"
		}
		parts = append(parts, fmt.Sprintf("VDN %s: %s, %s", network.Source.VDNName, network.Addressing.CIDR, target))
	}
	if findings.LBSNAT != nil {
		parts = append(parts, "LB SNAT: "+string(findings.LBSNAT.Selection))
	}
	preview := strings.Join(parts, "; ")
	if len(preview) > 4096 {
		return preview[:4093] + "..."
	}
	return preview
}
