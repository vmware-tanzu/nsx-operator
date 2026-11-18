/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestEvaluateToExecuteCheckpoint(t *testing.T) {
	for _, resource := range []string{"controlplanenetworktransitions", "namespacenetworktransitions"} {
		t.Run(resource, func(t *testing.T) {
			request := func(oldGen, observedGen, checkpoint int64, last, state, oldAction, newAction string) admission.Request {
				t.Helper()
				oldObj := map[string]any{"metadata": map[string]any{"uid": "current-uid", "generation": oldGen}, "spec": map[string]any{"action": oldAction}, "status": map[string]any{"lastAction": last, "actionState": state, "actionGeneration": checkpoint, "observedGeneration": observedGen}}
				newObj := map[string]any{"metadata": map[string]any{"generation": oldGen + 1}, "spec": map[string]any{"action": newAction}}
				oldRaw, err := json.Marshal(oldObj)
				require.NoError(t, err)
				newRaw, err := json.Marshal(newObj)
				require.NoError(t, err)
				return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Group: api.GroupVersion.Group, Version: api.GroupVersion.Version, Resource: resource}, OldObject: runtime.RawExtension{Raw: oldRaw}, Object: runtime.RawExtension{Raw: newRaw}}}
			}
			tests := []struct {
				name                              string
				oldGen, observedGen, checkpoint   int64
				last, state, oldAction, newAction string
				allowed                           bool
			}{
				{"input changed after success", 2, 2, 1, "Evaluate", "Succeeded", "Evaluate", "Execute", false},
				{"stale observed generation", 2, 1, 2, "Evaluate", "Succeeded", "Evaluate", "Execute", false},
				{"reevaluated current inputs", 2, 2, 2, "Evaluate", "Succeeded", "Evaluate", "Execute", true},
				{"missing generation", 2, 2, 0, "Evaluate", "Succeeded", "Evaluate", "Execute", false},
				{"missing action", 2, 2, 2, "", "Succeeded", "Evaluate", "Execute", false},
				{"missing state", 2, 2, 2, "Evaluate", "", "Evaluate", "Execute", false},
				{"running", 2, 2, 2, "Evaluate", "Running", "Evaluate", "Execute", false},
				{"failed", 2, 2, 2, "Evaluate", "Failed", "Evaluate", "Execute", false},
				{"different action", 2, 2, 2, "Execute", "Succeeded", "Evaluate", "Execute", false},
				{"unchanged plan recovery", 3, 3, 1, "Evaluate", "Succeeded", "Execute", "Execute", true},
				{"evaluation update", 2, 2, 1, "Evaluate", "Succeeded", "Evaluate", "Evaluate", true},
			}
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					validator := newTestTransitionValidator(t)
					if resource == "namespacenetworktransitions" {
						validator = newTestTransitionValidator(t, readyInfrastructureTransition())
					}
					res := validator.Handle(context.Background(), request(tc.oldGen, tc.observedGen, tc.checkpoint, tc.last, tc.state, tc.oldAction, tc.newAction))
					require.Equal(t, tc.allowed, res.Allowed)
					if !tc.allowed {
						require.Contains(t, res.Result.Message, "Complete Evaluate successfully for the current inputs")
					}
				})
			}
			req := request(2, 2, 1, "Evaluate", "Succeeded", "Evaluate", "Execute")
			req.SubResource = "status"
			require.True(t, newTestTransitionValidator(t).Handle(context.Background(), req).Allowed)
			req.SubResource = ""
			req.OldObject = runtime.RawExtension{}
			require.False(t, newTestTransitionValidator(t).Handle(context.Background(), req).Allowed)

			req = request(2, 2, 2, "Evaluate", "Succeeded", "Evaluate", "Execute")
			var old map[string]any
			require.NoError(t, json.Unmarshal(req.OldObject.Raw, &old))
			delete(old, "status")
			req.OldObject.Raw, _ = json.Marshal(old)
			require.False(t, newTestTransitionValidator(t).Handle(context.Background(), req).Allowed, "missing status must not satisfy the prerequisite")
		})
	}
}

func TestNamespaceExecuteRequiresStageOneCompletion(t *testing.T) {
	ready := readyInfrastructureTransition()
	finalizing := ready.DeepCopy()
	finalizing.Generation++
	finalizing.Status.Phase = api.PhaseFinalizing
	finalizing.Status.Conditions = []metav1.Condition{{Type: api.ConditionInfraTransitionComplete, Status: metav1.ConditionTrue, ObservedGeneration: ready.Generation}}
	finalized := finalizing.DeepCopy()
	finalized.Status.Phase = api.PhaseFinalized

	tests := []struct {
		name      string
		infra     *api.ControlPlaneNetworkTransition
		wantAllow bool
	}{
		{name: "missing Stage 1"},
		{name: "Stage 1 only evaluated", infra: &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 2}, Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: api.PhaseEvaluated}}}},
		{name: "Stage 1 completed", infra: ready, wantAllow: true},
		{name: "Stage 1 finalizing with completion condition", infra: finalizing, wantAllow: true},
		{name: "Stage 1 finalized with completion condition", infra: finalized, wantAllow: true},
		{name: "finalizing without completion condition", infra: &api.ControlPlaneNetworkTransition{ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 2}, Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Phase: api.PhaseFinalizing}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			validator := newTestTransitionValidator(t)
			if tc.infra != nil {
				validator = newTestTransitionValidator(t, tc.infra)
			}
			response := validator.Handle(context.Background(), validExecuteRequest("namespacenetworktransitions", "current-uid"))
			require.Equal(t, tc.wantAllow, response.Allowed)
			if !tc.wantAllow {
				require.Contains(t, response.Result.Message, "Stage 1 infrastructure migration must complete")
			}
		})
	}
}

func TestNamespaceExecuteStageOneReadFailureFailsClosed(t *testing.T) {
	reader := getErrorReader{Reader: newTestTransitionValidator(t).Reader, err: errors.New("API unavailable")}
	response := (TransitionValidator{Reader: reader}).Handle(context.Background(), validExecuteRequest("namespacenetworktransitions", "current-uid"))
	require.False(t, response.Allowed)
	require.Equal(t, int32(503), response.Result.Code)
	require.Contains(t, response.Result.Message, "API unavailable")
}

func readyInfrastructureTransition() *api.ControlPlaneNetworkTransition {
	return &api.ControlPlaneNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Name: api.ControlPlaneNetworkTransitionName, Generation: 4},
		Spec:       api.ControlPlaneNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}},
		Status: &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{
			LastAction: api.ActionExecute, ActionState: api.ActionStateSucceeded,
			ActionGeneration: 4, ObservedGeneration: 4, Phase: api.PhaseInfraTransitionComplete,
		}},
	}
}

type getErrorReader struct {
	client.Reader
	err error
}

func (r getErrorReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return r.err
}

func TestAdmissionReviewHTTP(t *testing.T) {
	request := admissionv1.AdmissionReview{TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"}, Request: &admissionv1.AdmissionRequest{
		UID: "request-1", Operation: admissionv1.Update,
		Resource:  metav1.GroupVersionResource{Group: api.GroupVersion.Group, Version: api.GroupVersion.Version, Resource: "controlplanenetworktransitions"},
		OldObject: runtime.RawExtension{Raw: []byte(`{"metadata":{"generation":2},"spec":{"action":"Evaluate"},"status":{"lastAction":"Evaluate","actionState":"Succeeded","actionGeneration":1}}`)},
		Object:    runtime.RawExtension{Raw: []byte(`{"metadata":{"generation":3},"spec":{"action":"Execute"}}`)},
	}}
	body, err := json.Marshal(request)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, TransitionValidationPath, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	(&admission.Webhook{Handler: newTestTransitionValidator(t)}).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var result admissionv1.AdmissionReview
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.NotNil(t, result.Response)
	require.Equal(t, request.Request.UID, result.Response.UID)
	require.False(t, result.Response.Allowed)
}

func newTestTransitionValidator(t *testing.T, objects ...client.Object) TransitionValidator {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	return TransitionValidator{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()}
}

func TestTransitionValidatorRejectsUnexpectedResource(t *testing.T) {
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Group: api.GroupVersion.Group, Version: api.GroupVersion.Version, Resource: "unknown"}}}
	require.False(t, (TransitionValidator{}).Handle(context.Background(), req).Allowed)
}
