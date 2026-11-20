/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestExecuteExclusivityAcrossTransitionKinds(t *testing.T) {
	tests := []struct {
		name     string
		other    client.Object
		allowed  bool
		wantName string
	}{
		{
			name: "active control plane Execute blocks namespace Execute",
			other: &api.ControlPlaneNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "infra", UID: "infra-uid"},
				Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}},
				Status:     &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: api.PhaseExecuting, LastAction: api.ActionExecute, ActionState: api.ActionStateRunning}},
			},
			wantName: "controlplanenetworktransitions/infra",
		},
		{
			name: "failed namespace Execute remains occupied while action stays Execute",
			other: &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "tenant-batch", UID: "tenant-uid"},
				Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant"}},
				Status:     &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: api.PhaseFailed, LastAction: api.ActionExecute, ActionState: api.ActionStateFailed}},
			},
			wantName: "namespacenetworktransitions/tenant-batch",
		},
		{
			name: "rollback request remains occupied until restoration is verified",
			other: &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "rolling-back", UID: "rollback-uid"},
				Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionRollback}, Namespaces: []string{"tenant"}},
				Status:     &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: api.PhaseFailed, LastAction: api.ActionExecute, ActionState: api.ActionStateFailed}},
			},
			wantName: "namespacenetworktransitions/rolling-back",
		},
		{
			name: "running Execute checkpoint blocks even if phase is terminal",
			other: &api.ControlPlaneNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "running-checkpoint", UID: "running-uid"},
				Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionEvaluate}},
				Status:     &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: api.PhaseCompleted, LastAction: api.ActionExecute, ActionState: api.ActionStateRunning}},
			},
			wantName: "controlplanenetworktransitions/running-checkpoint",
		},
		{
			name: "terminal phase does not block",
			other: &api.NamespaceNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "done", UID: "done-uid"},
				Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}, Namespaces: []string{"tenant"}},
				Status:     &api.NamespaceNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: api.PhaseCompleted, LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded}},
			},
			allowed: true,
		},
		{
			name: "same UID is excluded",
			other: &api.ControlPlaneNetworkTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "current", UID: "current-uid"},
				Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}},
				Status:     &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: api.PhaseExecuting}},
			},
			allowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := newTestTransitionValidator(t, readyInfrastructureTransition(), tt.other)
			response := validator.Handle(context.Background(), validExecuteRequest("namespacenetworktransitions", "current-uid"))
			require.Equal(t, tt.allowed, response.Allowed)
			if tt.wantName != "" {
				require.Contains(t, response.Result.Message, tt.wantName)
			}
		})
	}
}

func TestExecuteExclusivityFailsClosedWhenReaderUnavailableOrFails(t *testing.T) {
	req := validExecuteRequest("controlplanenetworktransitions", "current-uid")
	response := (TransitionValidator{}).Handle(context.Background(), req)
	require.False(t, response.Allowed)
	require.Equal(t, int32(503), response.Result.Code)
	require.Contains(t, response.Result.Message, "reader is not configured")

	reader := errorListReader{Client: newTestTransitionValidator(t).Reader.(client.Client), err: errors.New("API unavailable")}
	response = (TransitionValidator{Reader: reader}).Handle(context.Background(), req)
	require.False(t, response.Allowed)
	require.Equal(t, int32(503), response.Result.Code)
	require.Contains(t, response.Result.Message, "API unavailable")
}

func TestExecuteExclusivityRequiresCurrentUID(t *testing.T) {
	req := validExecuteRequest("controlplanenetworktransitions", "current-uid")
	var oldObject map[string]any
	require.NoError(t, json.Unmarshal(req.OldObject.Raw, &oldObject))
	delete(oldObject["metadata"].(map[string]any), "uid")
	req.OldObject.Raw, _ = json.Marshal(oldObject)

	response := newTestTransitionValidator(t).Handle(context.Background(), req)
	require.False(t, response.Allowed)
	require.Equal(t, int32(400), response.Result.Code)
	require.Contains(t, response.Result.Message, "has no UID")
}

func validExecuteRequest(resource string, uid types.UID) admission.Request {
	oldRaw, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"uid": uid, "generation": 2},
		"spec":     map[string]any{"action": "Evaluate"},
		"status": map[string]any{
			"lastAction": "Evaluate", "actionState": "Succeeded", "actionGeneration": 2,
			"observedGeneration": 2, "phase": "Evaluated",
		},
	})
	newRaw, _ := json.Marshal(map[string]any{"metadata": map[string]any{"generation": 3}, "spec": map[string]any{"action": "Execute"}})
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Update,
		Resource:  metav1.GroupVersionResource{Group: api.GroupVersion.Group, Version: api.GroupVersion.Version, Resource: resource},
		OldObject: runtime.RawExtension{Raw: oldRaw}, Object: runtime.RawExtension{Raw: newRaw},
	}}
}

type errorListReader struct {
	client.Client
	err error
}

func (r errorListReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return r.err
}
