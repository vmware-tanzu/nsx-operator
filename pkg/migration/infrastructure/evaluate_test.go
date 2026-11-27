/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

type evaluationScope struct{}

func (evaluationScope) SourceNetworks(context.Context) ([]string, []inspection.Topology, error) {
	return []string{"cp-network"}, nil, nil
}

type evaluationInspector struct{ result inspection.Result }

func (inspector evaluationInspector) InspectControlPlane(_ context.Context, _ *api.ControlPlaneNetworkTransition, vdns []string, _ ...inspection.Topology) (inspection.Result, error) {
	if len(vdns) != 1 || vdns[0] != "cp-network" {
		return inspection.Result{}, nil
	}
	return inspector.result, nil
}

type evaluationSNAT struct {
	calls   int
	pending bool
}

func (snat *evaluationSNAT) Ensure(_ context.Context, _ *api.ControlPlaneNetworkTransition, _ inspection.LBSNATDecision) (api.IPAllocationStatus, bool, error) {
	snat.calls++
	if snat.pending {
		return api.IPAllocationStatus{}, false, nil
	}
	return api.IPAllocationStatus{IPAddress: "192.0.2.10", AllocationRef: &api.NamespacedName{Namespace: "vmware-system-netop", Name: "migration-snat"}}, true, nil
}

type evaluationDVPGValidator struct {
	calls  int
	result DVPGValidationResult
	err    error
}

func (validator *evaluationDVPGValidator) Validate(context.Context, string, int64, []inspection.Network) (DVPGValidationResult, error) {
	validator.calls++
	return validator.result, validator.err
}

func TestEvaluateHandlerWaitsForDHCPInputBeforeReservingSNAT(t *testing.T) {
	resource := evaluationTransition()
	snat := &evaluationSNAT{}
	handler := EvaluateHandler{Scope: evaluationScope{}, Inspector: evaluationInspector{result: inspection.Result{
		RequiredInputs: []api.RequiredInput{{FieldName: "spec.networkConfiguration.controlPlaneAndServiceDHCPNetworks[vdnName=cp-network]", Description: "Supply DHCP network details"}},
	}}, DVPGValidator: &evaluationDVPGValidator{}, SNAT: snat}

	result, err := handler.Reconcile(context.Background(), evaluationRequest(resource))
	require.NoError(t, err)
	require.Equal(t, transition.OutcomeNeedsInput, result.Outcome)
	require.Equal(t, 0, snat.calls)
	require.Len(t, resource.Status.RequiredInputs, 1)
	require.Empty(t, resource.Status.LBSNATIPAllocations)
}

func TestEvaluateHandlerClearsOldFindingsWhenScopeDiscoveryFails(t *testing.T) {
	resource := evaluationTransition()
	resource.Status.RequiredInputs = []api.RequiredInput{{FieldName: "spec.networkConfiguration.controlPlaneAndServiceDHCPNetworks[vdnName=old]"}}
	meta.SetStatusCondition(&resource.Status.Conditions, metav1.Condition{
		Type: inspection.ConditionNetworkInputsValidated, Status: metav1.ConditionTrue,
		Reason: "InputsValidated", Message: "previous successful inspection", ObservedGeneration: 1,
	})
	meta.SetStatusCondition(&resource.Status.Conditions, metav1.Condition{
		Type: "EvaluationPreview", Status: metav1.ConditionTrue,
		Reason: "SourcePreviewReady", Message: "previous preview", ObservedGeneration: 1,
	})
	meta.SetStatusCondition(&resource.Status.Conditions, metav1.Condition{
		Type: ConditionDVPGConversionValidated, Status: metav1.ConditionTrue,
		Reason: "DVPGsCompatible", Message: "previously validated DVPGs", ObservedGeneration: 1,
	})
	handler := EvaluateHandler{
		Scope: failingEvaluationScope{}, Inspector: evaluationInspector{}, DVPGValidator: &evaluationDVPGValidator{}, SNAT: &evaluationSNAT{},
	}

	_, err := handler.Reconcile(context.Background(), evaluationRequest(resource))
	require.ErrorContains(t, err, "network discovery is unavailable")
	require.ErrorContains(t, err, "could not list Net Operator VDNs")
	require.NotContains(t, err.Error(), "injected scope failure")
	require.Empty(t, resource.Status.RequiredInputs)
	require.Equal(t, metav1.ConditionUnknown, meta.FindStatusCondition(resource.Status.Conditions, inspection.ConditionNetworkInputsValidated).Status)
	require.Equal(t, "NetworkDiscoveryUnavailable", meta.FindStatusCondition(resource.Status.Conditions, inspection.ConditionNetworkInputsValidated).Reason)
	require.Equal(t, metav1.ConditionUnknown, meta.FindStatusCondition(resource.Status.Conditions, "EvaluationPreview").Status)
	validation := meta.FindStatusCondition(resource.Status.Conditions, ConditionDVPGConversionValidated)
	require.Equal(t, metav1.ConditionUnknown, validation.Status)
	require.Equal(t, "ValidationUnavailable", validation.Reason)
	require.Contains(t, validation.Message, "could not list Net Operator VDNs")
	require.NotContains(t, validation.Message, "injected scope failure")
}

type failingEvaluationScope struct{}

func (failingEvaluationScope) SourceNetworks(context.Context) ([]string, []inspection.Topology, error) {
	return nil, nil, newEvaluationDiagnostic("could not list Net Operator VDNs", fmt.Errorf("injected scope failure"))
}

type failingEvaluationInspector struct{}

func (failingEvaluationInspector) InspectControlPlane(context.Context, *api.ControlPlaneNetworkTransition, []string, ...inspection.Topology) (inspection.Result, error) {
	return inspection.Result{}, fmt.Errorf("injected inspection failure")
}

func TestEvaluateHandlerMarksDVPGValidationUnknownWhenInspectionFails(t *testing.T) {
	resource := evaluationTransition()
	meta.SetStatusCondition(&resource.Status.Conditions, metav1.Condition{
		Type: "EvaluationPreview", Status: metav1.ConditionTrue,
		Reason: "SourcePreviewReady", Message: "previous preview", ObservedGeneration: 1,
	})
	meta.SetStatusCondition(&resource.Status.Conditions, metav1.Condition{
		Type: ConditionDVPGConversionValidated, Status: metav1.ConditionTrue,
		Reason: "DVPGsCompatible", Message: "previously validated DVPGs", ObservedGeneration: 1,
	})
	handler := EvaluateHandler{
		Scope: evaluationScope{}, Inspector: failingEvaluationInspector{}, DVPGValidator: &evaluationDVPGValidator{}, SNAT: &evaluationSNAT{},
	}

	_, err := handler.Reconcile(context.Background(), evaluationRequest(resource))
	require.ErrorContains(t, err, "source network inspection failed")
	validation := meta.FindStatusCondition(resource.Status.Conditions, ConditionDVPGConversionValidated)
	require.Equal(t, metav1.ConditionUnknown, validation.Status)
	require.Equal(t, "ValidationUnavailable", validation.Reason)
	require.Equal(t, metav1.ConditionUnknown, meta.FindStatusCondition(resource.Status.Conditions, "EvaluationPreview").Status)
}

func TestEvaluateHandlerRecordsAcceptedSourceReservation(t *testing.T) {
	resource := evaluationTransition()
	snat := &evaluationSNAT{}
	handler := EvaluateHandler{Scope: evaluationScope{}, Inspector: evaluationInspector{result: inspection.Result{
		Networks: []inspection.Network{{Source: inspection.Source{VDNName: "cp-network"}}},
		LBSNAT:   &inspection.LBSNATDecision{Selection: inspection.AllocateFromBackendNetwork, AllocationSource: "dvportgroup-1"},
	}}, DVPGValidator: &evaluationDVPGValidator{}, SNAT: snat}

	result, err := handler.Reconcile(context.Background(), evaluationRequest(resource))
	require.NoError(t, err)
	require.Equal(t, transition.OutcomeSucceeded, result.Outcome)
	require.Equal(t, 1, snat.calls)
	require.Equal(t, "192.0.2.10", resource.Status.LBSNATIPAllocations[0].IPAddress)
	require.Equal(t, "migration-snat", resource.Status.LBSNATIPAllocations[0].AllocationRef.Name)
	require.Equal(t, api.LoadBalancerProvider("VSPHERE_FOUNDATION"), resource.Status.SourceLBProvider)
	require.Contains(t, meta.FindStatusCondition(resource.Status.Conditions, "EvaluationPreview").Message, "VDN cp-network")
}

func TestEvaluateHandlerWaitsForDVPGCompatibilityBeforeSNAT(t *testing.T) {
	resource := evaluationTransition()
	snat := &evaluationSNAT{}
	validator := &evaluationDVPGValidator{result: DVPGValidationResult{InProgress: true}}
	handler := EvaluateHandler{
		Scope: evaluationScope{},
		Inspector: evaluationInspector{result: inspection.Result{
			Networks: []inspection.Network{{Source: inspection.Source{VDNName: "cp-network"}}},
			LBSNAT:   &inspection.LBSNATDecision{Selection: inspection.AllocateFromBackendNetwork, AllocationSource: "dvportgroup-1"},
		}},
		DVPGValidator: validator,
		SNAT:          snat,
	}

	result, err := handler.Reconcile(context.Background(), evaluationRequest(resource))
	require.NoError(t, err)
	require.Equal(t, transition.OutcomeInProgress, result.Outcome)
	require.Equal(t, 0, snat.calls)
	require.Equal(t, metav1.ConditionUnknown, meta.FindStatusCondition(resource.Status.Conditions, ConditionDVPGConversionValidated).Status)
}

func TestEvaluateHandlerReusesFinishedDVPGValidationWhileSNATIsPending(t *testing.T) {
	ctx := context.Background()
	resource := evaluationTransition()
	snat := &evaluationSNAT{pending: true}
	validationAPI := &fakeDVPGValidationAPI{
		workflows: make(map[string]*model.DvpgToSubnetWorkflow),
		results:   make(map[string][]model.DvpgValidationResult),
	}
	handler := EvaluateHandler{
		Scope: evaluationScope{},
		Inspector: evaluationInspector{result: inspection.Result{
			Networks: []inspection.Network{{Source: inspection.Source{
				VDNName: "cp-network", Topologies: []inspection.Topology{{
					VCenterUUID: "vc-1", SwitchID: "dvs-1", PortGroupID: "dvpg-1",
				}},
			}}},
			LBSNAT: &inspection.LBSNATDecision{Selection: inspection.AllocateFromBackendNetwork, AllocationSource: "dvpg-1"},
		}},
		DVPGValidator: NSXDVPGCompatibilityValidator{API: validationAPI, ComputeManagerID: "cm-1", VCenterUUID: "vc-1"},
		SNAT:          snat,
	}

	result, err := handler.Reconcile(ctx, evaluationRequest(resource))
	require.NoError(t, err)
	require.Equal(t, transition.OutcomeInProgress, result.Outcome)
	require.Len(t, validationAPI.updated, 1)
	workflow := validationAPI.updated[0]
	workflow.WorkflowState = &model.DvpgToSubnetWorkflowState{Status: ptr.To("SUCCESS")}
	validationAPI.results[*workflow.Id] = []model.DvpgValidationResult{{
		DvpgRef: ptr.To("cm-1:dvpg-1"), Compatibility: ptr.To(model.DvpgValidationResult_COMPATIBILITY_SUPPORTED),
	}}

	result, err = handler.Reconcile(ctx, evaluationRequest(resource))
	require.NoError(t, err)
	require.Equal(t, transition.OutcomeInProgress, result.Outcome)
	require.Equal(t, 1, snat.calls)
	require.Empty(t, validationAPI.deleted)

	snat.pending = false
	result, err = handler.Reconcile(ctx, evaluationRequest(resource))
	require.NoError(t, err)
	require.Equal(t, transition.OutcomeSucceeded, result.Outcome)
	require.Len(t, validationAPI.updated, 1)
	require.Equal(t, []string{*workflow.Id}, validationAPI.deleted)
}

func TestEvaluateHandlerRejectsIncompatibleDVPGBeforeSNAT(t *testing.T) {
	resource := evaluationTransition()
	snat := &evaluationSNAT{}
	handler := EvaluateHandler{
		Scope: evaluationScope{},
		Inspector: evaluationInspector{result: inspection.Result{
			Networks: []inspection.Network{{Source: inspection.Source{VDNName: "cp-network"}}},
			LBSNAT:   &inspection.LBSNATDecision{Selection: inspection.AllocateFromBackendNetwork, AllocationSource: "dvportgroup-1"},
		}},
		DVPGValidator: &evaluationDVPGValidator{result: DVPGValidationResult{Problems: []string{"DVPG workload is unsupported"}}},
		SNAT:          snat,
	}

	_, err := handler.Reconcile(context.Background(), evaluationRequest(resource))
	require.ErrorContains(t, err, "incompatible")
	require.Equal(t, 0, snat.calls)
	require.Equal(t, metav1.ConditionFalse, meta.FindStatusCondition(resource.Status.Conditions, ConditionDVPGConversionValidated).Status)
}

func TestEvaluateHandlerRunsAfterDurableActionCheckpoint(t *testing.T) {
	resource := evaluationTransition()
	resource.Status = &api.ControlPlaneNetworkTransitionStatus{}
	snat := &evaluationSNAT{}
	handler := EvaluateHandler{Scope: evaluationScope{}, Inspector: evaluationInspector{result: inspection.Result{
		Networks: []inspection.Network{{Source: inspection.Source{VDNName: "cp-network"}}},
		LBSNAT:   &inspection.LBSNATDecision{Selection: inspection.AllocateFromBackendNetwork, AllocationSource: "dvportgroup-1"},
	}}, DVPGValidator: &evaluationDVPGValidator{}, SNAT: snat}
	engine := &transition.Engine{Enabled: true, Handlers: map[transition.Key]transition.Handler{{Stage: transition.Infrastructure, Action: api.ActionEvaluate}: handler}}

	result, err := engine.Reconcile(context.Background(), evaluationRequest(resource))
	require.NoError(t, err)
	require.Equal(t, transition.OutcomeInProgress, result.Outcome)
	require.Equal(t, 0, snat.calls, "persist the action checkpoint before reserving an IP")
	result, err = engine.Reconcile(context.Background(), evaluationRequest(resource))
	require.NoError(t, err)
	require.Equal(t, transition.OutcomeSucceeded, result.Outcome)
	require.Equal(t, "Evaluated", resource.Status.Phase)
	require.Equal(t, 1, snat.calls)
}

func evaluationTransition() *api.ControlPlaneNetworkTransition {
	return &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, UID: "cp-transition", Generation: 1},
		Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}},
		Status:     &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{LastAction: api.ActionEvaluate, ActionState: api.ActionStateRunning, ActionGeneration: 1}},
	}
}

func evaluationRequest(resource *api.ControlPlaneNetworkTransition) transition.Request {
	return transition.Request{Stage: transition.Infrastructure, Object: resource, Action: api.ActionEvaluate, Generation: resource.Generation, Status: &resource.Status.TransitionStatus}
}
