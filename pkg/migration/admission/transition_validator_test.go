/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestEvaluateToPlanCheckpoint(t *testing.T) {
	for _, resource := range []string{"controlplanenetworktransitions", "namespacenetworktransitions"} {
		t.Run(resource, func(t *testing.T) {
			request := func(oldGen, observedGen, checkpoint int64, last, state, oldAction, newAction string) admission.Request {
				t.Helper()
				oldObj := map[string]any{"metadata": map[string]any{"generation": oldGen}, "spec": map[string]any{"action": oldAction}, "status": map[string]any{"lastAction": last, "actionState": state, "actionGeneration": checkpoint, "observedGeneration": observedGen}}
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
				{"input changed after success", 2, 2, 1, "Evaluate", "Succeeded", "Evaluate", "Plan", false},
				{"stale observed generation", 2, 1, 2, "Evaluate", "Succeeded", "Evaluate", "Plan", false},
				{"reevaluated current inputs", 2, 2, 2, "Evaluate", "Succeeded", "Evaluate", "Plan", true},
				{"missing generation", 2, 2, 0, "Evaluate", "Succeeded", "Evaluate", "Plan", false},
				{"missing action", 2, 2, 2, "", "Succeeded", "Evaluate", "Plan", false},
				{"missing state", 2, 2, 2, "Evaluate", "", "Evaluate", "Plan", false},
				{"running", 2, 2, 2, "Evaluate", "Running", "Evaluate", "Plan", false},
				{"failed", 2, 2, 2, "Evaluate", "Failed", "Evaluate", "Plan", false},
				{"different action", 2, 2, 2, "Plan", "Succeeded", "Evaluate", "Plan", false},
				{"unchanged plan recovery", 3, 3, 1, "Evaluate", "Succeeded", "Plan", "Plan", true},
				{"evaluation update", 2, 2, 1, "Evaluate", "Succeeded", "Evaluate", "Evaluate", true},
			}
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					res := (TransitionValidator{}).Handle(context.Background(), request(tc.oldGen, tc.observedGen, tc.checkpoint, tc.last, tc.state, tc.oldAction, tc.newAction))
					require.Equal(t, tc.allowed, res.Allowed)
					if !tc.allowed {
						require.Contains(t, res.Result.Message, "Complete Evaluate successfully for the current inputs")
					}
				})
			}
			req := request(2, 2, 1, "Evaluate", "Succeeded", "Evaluate", "Plan")
			req.SubResource = "status"
			require.True(t, (TransitionValidator{}).Handle(context.Background(), req).Allowed)
			req.SubResource = ""
			req.OldObject = runtime.RawExtension{}
			require.False(t, (TransitionValidator{}).Handle(context.Background(), req).Allowed)

			req = request(2, 2, 2, "Evaluate", "Succeeded", "Evaluate", "Plan")
			var old map[string]any
			require.NoError(t, json.Unmarshal(req.OldObject.Raw, &old))
			delete(old, "status")
			req.OldObject.Raw, _ = json.Marshal(old)
			require.False(t, (TransitionValidator{}).Handle(context.Background(), req).Allowed, "missing status must not satisfy the prerequisite")
		})
	}
}

func TestAdmissionReviewHTTP(t *testing.T) {
	request := admissionv1.AdmissionReview{TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"}, Request: &admissionv1.AdmissionRequest{
		UID: "request-1", Operation: admissionv1.Update,
		Resource:  metav1.GroupVersionResource{Group: api.GroupVersion.Group, Version: api.GroupVersion.Version, Resource: "controlplanenetworktransitions"},
		OldObject: runtime.RawExtension{Raw: []byte(`{"metadata":{"generation":2},"spec":{"action":"Evaluate"},"status":{"lastAction":"Evaluate","actionState":"Succeeded","actionGeneration":1}}`)},
		Object:    runtime.RawExtension{Raw: []byte(`{"metadata":{"generation":3},"spec":{"action":"Plan"}}`)},
	}}
	body, err := json.Marshal(request)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, TransitionValidationPath, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	(&admission.Webhook{Handler: TransitionValidator{}}).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var result admissionv1.AdmissionReview
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.NotNil(t, result.Response)
	require.Equal(t, request.Request.UID, result.Response.UID)
	require.False(t, result.Response.Allowed)
}

func TestTransitionValidatorRejectsUnexpectedResource(t *testing.T) {
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Group: api.GroupVersion.Group, Version: api.GroupVersion.Version, Resource: "unknown"}}}
	require.False(t, (TransitionValidator{}).Handle(context.Background(), req).Allowed)
}
