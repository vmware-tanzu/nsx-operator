/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestActionPrerequisites(t *testing.T) {
	for _, resource := range []string{"controlplanenetworktransitions", "namespacenetworktransitions"} {
		t.Run(resource, func(t *testing.T) {
			isNamespace := resource == "namespacenetworktransitions"
			request := func(op admissionv1.Operation, oldAction, action, last, state string, generation, actionGeneration int64, irreversible string) admission.Request {
				t.Helper()
				old := map[string]any{"metadata": map[string]any{"generation": generation}, "spec": map[string]any{"action": oldAction}, "status": map[string]any{"lastAction": last, "actionState": state, "actionGeneration": actionGeneration}}
				if irreversible != "" {
					old["status"].(map[string]any)["irreversibleAction"] = irreversible
				}
				newObj := map[string]any{"metadata": map[string]any{"generation": generation + 1}, "spec": map[string]any{"action": action}}
				oldJSON, err := json.Marshal(old)
				require.NoError(t, err)
				newJSON, err := json.Marshal(newObj)
				require.NoError(t, err)
				return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: op, Resource: metav1.GroupVersionResource{Group: api.GroupVersion.Group, Version: api.GroupVersion.Version, Resource: resource}, OldObject: runtime.RawExtension{Raw: oldJSON}, Object: runtime.RawExtension{Raw: newJSON}}}
			}
			tests := []struct {
				name                     string
				op                       admissionv1.Operation
				old, action, last, state string
				gen, checkpoint          int64
				irreversible             string
				allowed                  bool
			}{
				{"create Evaluate", admissionv1.Create, "", "Evaluate", "", "", 0, 0, "", true},
				{"create Plan", admissionv1.Create, "", "Plan", "", "", 0, 0, "", false},
				{"create Execute", admissionv1.Create, "", "Execute", "", "", 0, 0, "", false},
				{"skip Evaluate to Derive", admissionv1.Update, "Evaluate", "Derive", "Evaluate", "Succeeded", 1, 1, "", false},
				{"skip Plan to Execute", admissionv1.Update, "Plan", "Execute", "Plan", "Succeeded", 2, 2, "", false},
				{"successful Plan to Derive", admissionv1.Update, "Plan", "Derive", "Plan", "Succeeded", 2, 2, "", true},
				{"running Plan cannot Derive", admissionv1.Update, "Plan", "Derive", "Plan", "Running", 2, 2, "", false},
				{"failed Plan cannot Derive", admissionv1.Update, "Plan", "Derive", "Plan", "Failed", 2, 2, "", false},
				{"stale Plan cannot Derive", admissionv1.Update, "Plan", "Derive", "Plan", "Succeeded", 3, 2, "", false},
				{"successful Derive to Execute", admissionv1.Update, "Derive", "Execute", "Derive", "Succeeded", 3, 3, "", true},
				{"unchanged failed Execute retry", admissionv1.Update, "Execute", "Execute", "Execute", "Failed", 4, 4, "", true},
				{"pending Plan cannot reopen Evaluate", admissionv1.Update, "Plan", "Evaluate", "Evaluate", "Succeeded", 2, 1, "", false},
				{"namespace rollback from running Execute", admissionv1.Update, "Execute", "Rollback", "Execute", "Running", 4, 4, "", isNamespace},
				{"namespace rollback from pending Plan", admissionv1.Update, "Plan", "Rollback", "Evaluate", "Succeeded", 2, 1, "", isNamespace},
				{"rollback blocked by irreversible checkpoint", admissionv1.Update, "Execute", "Rollback", "Execute", "Failed", 4, 4, "Commit", false},
				{"namespace successful rollback reopens Evaluate", admissionv1.Update, "Rollback", "Evaluate", "Rollback", "Succeeded", 5, 5, "", isNamespace},
				{"namespace failed rollback cannot reopen Evaluate", admissionv1.Update, "Rollback", "Evaluate", "Rollback", "Failed", 5, 5, "", false},
				{"namespace Execute to Commit", admissionv1.Update, "Execute", "Commit", "Execute", "Succeeded", 4, 4, "", isNamespace},
				{"infrastructure Execute to Finalize", admissionv1.Update, "Execute", "Finalize", "Execute", "Succeeded", 4, 4, "", !isNamespace},
			}
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					res := (TransitionValidator{}).Handle(context.Background(), request(tc.op, tc.old, tc.action, tc.last, tc.state, tc.gen, tc.checkpoint, tc.irreversible))
					require.Equal(t, tc.allowed, res.Allowed, "response: %+v", res.Result)
				})
			}
		})
	}
}

func TestDeleteDuringExecuteOrRollback(t *testing.T) {
	for _, resource := range []string{"controlplanenetworktransitions", "namespacenetworktransitions"} {
		t.Run(resource, func(t *testing.T) {
			completed := "Completed"
			if resource == "controlplanenetworktransitions" {
				completed = "InfraTransitionComplete"
			}
			for _, tc := range []struct {
				action, state, phase string
				generation           int64
				allowed              bool
			}{
				{"Execute", "Running", "Executing", 3, false},
				{"Execute", "Failed", "Failed", 3, false},
				{"Execute", "Succeeded", "Failed", 3, false},
				{"Execute", "Succeeded", completed, 0, false},
				{"Execute", "Succeeded", completed, 3, false},
				{"Execute", "Succeeded", completed, 4, true},
				{"Rollback", "Running", "RollingBack", 4, false},
				{"Rollback", "Failed", "Failed", 4, false},
				{"Rollback", "Succeeded", "RolledBack", 4, true},
				{"Plan", "Succeeded", "PlanReady", 2, true},
			} {
				t.Run(tc.action+"/"+tc.state+"/"+tc.phase, func(t *testing.T) {
					old := map[string]any{"metadata": map[string]any{"generation": 4}, "spec": map[string]any{"action": tc.action}, "status": map[string]any{"lastAction": tc.action, "actionState": tc.state, "actionGeneration": tc.generation, "phase": tc.phase}}
					raw, err := json.Marshal(old)
					require.NoError(t, err)
					req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Delete, Resource: metav1.GroupVersionResource{Group: api.GroupVersion.Group, Version: api.GroupVersion.Version, Resource: resource}, OldObject: runtime.RawExtension{Raw: raw}}}
					require.Equal(t, tc.allowed, (TransitionValidator{}).Handle(context.Background(), req).Allowed)
				})
			}
			for _, status := range []any{nil, map[string]any{"lastAction": "Derive", "actionState": "Succeeded", "actionGeneration": 3}} {
				old := map[string]any{"metadata": map[string]any{"generation": 4}, "spec": map[string]any{"action": "Execute"}, "status": status}
				raw, err := json.Marshal(old)
				require.NoError(t, err)
				req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Delete, Resource: metav1.GroupVersionResource{Group: api.GroupVersion.Group, Version: api.GroupVersion.Version, Resource: resource}, OldObject: runtime.RawExtension{Raw: raw}}}
				require.False(t, (TransitionValidator{}).Handle(context.Background(), req).Allowed, "an Execute request without a current checkpoint must not lose its recovery record")
			}
		})
	}
}
