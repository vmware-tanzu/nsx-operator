/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/identity"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/transition"
)

// RuntimeEvaluateHandler builds Evaluate services from the manager's
// authenticated clients and the current transition's project scope.
type RuntimeEvaluateHandler struct {
	Clients              *identity.AuthenticatedClients
	Identity             identity.Resolver
	SupervisorID         string
	KubernetesClient     client.Client
	KubernetesReader     client.Reader
	DefaultProject       string
	NetOperatorNamespace string
}

func (handler RuntimeEvaluateHandler) Reconcile(ctx context.Context, request transition.Request) (transition.Result, error) {
	resource, ok := request.Object.(*api.ControlPlaneNetworkTransition)
	if !ok || request.Stage != transition.Infrastructure || request.Action != api.ActionEvaluate || resource.Status == nil || request.Status == nil {
		return transition.Result{}, fmt.Errorf("control-plane Evaluate transition is required")
	}
	if handler.Clients == nil || handler.Clients.VCenter == nil || handler.Clients.VCenterREST == nil || handler.Clients.NSX == nil || handler.KubernetesClient == nil || handler.KubernetesReader == nil || handler.SupervisorID == "" || handler.NetOperatorNamespace == "" {
		return transition.Result{}, fmt.Errorf("authenticated migration clients, Supervisor ID and Kubernetes clients are required")
	}
	if handler.Identity.SupervisorTopology == nil || handler.Identity.ComputeManagers == nil {
		return transition.Result{}, fmt.Errorf("source identity resolver is not configured")
	}
	// A new Evaluate is authoritative only after every check succeeds. Clear an
	// earlier checkpoint first so a failed retry cannot leave stale identity
	// data available to a later Execute.
	resource.Status.ResolvedSourceContext = nil

	resolvedSource, err := handler.Identity.Resolve(ctx, handler.SupervisorID, request.Generation)
	if err != nil {
		setIdentityCondition(request.Status, request.Generation, metav1.ConditionUnknown, "IdentityResolutionUnavailable", "Source identity could not be verified; retry Evaluate")
		if statusErr := inspection.ApplyEvaluation(inspection.Result{}, err, request.Status, request.Generation); statusErr != nil {
			return transition.Result{}, statusErr
		}
		return transition.Result{}, fmt.Errorf("resolve source identity before infrastructure Evaluate: %w", err)
	}
	setIdentityCondition(request.Status, request.Generation, metav1.ConditionTrue, "IdentityResolved", "Supervisor and vCenter/NSX identities are verified")

	projectID := resource.Spec.NSXProjectID
	if projectID == "" {
		projectID = handler.DefaultProject
	}
	if projectID == "" {
		projectID = "default"
	}
	service, err := inspection.NewService(inspection.ServiceConfig{
		KubernetesReader: handler.KubernetesReader,
		KubernetesClient: handler.KubernetesClient,
		VCenter:          handler.Clients.VCenter.Client,
		VCenterREST:      handler.Clients.VCenterREST,
		NSX:              handler.Clients.NSX,
		ComputeManagerID: resolvedSource.NSXComputeManagerID,
		SupervisorID:     handler.SupervisorID,
		OrgID:            "default",
		ProjectID:        projectID,
	})
	if err != nil {
		if statusErr := inspection.ApplyEvaluation(inspection.Result{}, err, request.Status, request.Generation); statusErr != nil {
			return transition.Result{}, statusErr
		}
		return transition.Result{}, fmt.Errorf("initialize infrastructure inspection services")
	}
	validationAPI, err := NewNSXDVPGValidationAPI(handler.Clients.NSX)
	if err != nil {
		if statusErr := inspection.ApplyEvaluation(inspection.Result{}, err, request.Status, request.Generation); statusErr != nil {
			return transition.Result{}, statusErr
		}
		return transition.Result{}, fmt.Errorf("initialize NSX DVPG validation service")
	}

	scope := VCenterControlPlaneScope{
		Networks: VCenterSupervisorNetworks{
			Client:       handler.Clients.VCenterREST,
			SupervisorID: handler.SupervisorID,
			Reader:       handler.KubernetesReader,
		},
		Reader:    handler.KubernetesReader,
		Inventory: service.Inventory,
	}
	evaluator := EvaluateHandler{
		Scope:         scope,
		Inspector:     service,
		DVPGValidator: NSXDVPGCompatibilityValidator{API: validationAPI},
		SNAT: NetOperatorSNAT{
			Client:   handler.KubernetesClient,
			Reader:   handler.KubernetesReader,
			Networks: KubernetesSourceNetworks{Reader: handler.KubernetesReader, Namespace: handler.NetOperatorNamespace},
		},
	}
	result, err := evaluator.Reconcile(ctx, request)
	if err == nil && result.Outcome == transition.OutcomeSucceeded {
		resource.Status.ResolvedSourceContext = &resolvedSource
	}
	return result, err
}

func setIdentityCondition(status *api.TransitionStatus, generation int64, conditionStatus metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type: identity.ConditionSourceIdentityResolved, Status: conditionStatus,
		Reason: reason, Message: message, ObservedGeneration: generation,
	})
}
