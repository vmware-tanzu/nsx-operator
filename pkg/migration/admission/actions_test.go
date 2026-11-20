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
			request := func(op admissionv1.Operation, oldAction, action, last, state string, generation, actionGeneration int64, irreversible string, rollbackComplete bool) admission.Request {
				t.Helper()
				phase := ""
				if state == "Succeeded" {
					switch last {
					case "Evaluate":
						phase = "Evaluated"
					case "Execute":
						phase = "Completed"
						if !isNamespace {
							phase = "InfraTransitionComplete"
						}
					case "Rollback":
						phase = "RolledBack"
					case "Commit":
						phase = "Committed"
					case "Finalize":
						phase = "Finalized"
					}
				}
				old := map[string]any{"metadata": map[string]any{"uid": "current-uid", "generation": generation}, "spec": map[string]any{"action": oldAction}, "status": map[string]any{"lastAction": last, "actionState": state, "actionGeneration": actionGeneration, "observedGeneration": actionGeneration, "phase": phase}}
				if rollbackComplete {
					old["status"].(map[string]any)["rollbackComplete"] = true
				}
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
				rollbackComplete         bool
				allowed                  bool
			}{
				{"create Evaluate", admissionv1.Create, "", "Evaluate", "", "", 0, 0, "", false, true},
				{"create legacy Plan", admissionv1.Create, "", "Plan", "", "", 0, 0, "", false, false},
				{"create Execute", admissionv1.Create, "", "Execute", "", "", 0, 0, "", false, false},
				{"Evaluate requires fresh successful checkpoint", admissionv1.Update, "Evaluate", "Execute", "Evaluate", "Succeeded", 2, 1, "", false, false},
				{"current Evaluate permits Execute", admissionv1.Update, "Evaluate", "Execute", "Evaluate", "Succeeded", 2, 2, "", false, true},
				{"changed inputs require reevaluation", admissionv1.Update, "Evaluate", "Execute", "Evaluate", "Succeeded", 3, 2, "", false, false},
				{"illegal direct Finalize", admissionv1.Update, "Evaluate", "Finalize", "Evaluate", "Succeeded", 2, 2, "", false, false},
				{"unchanged failed Execute retry", admissionv1.Update, "Execute", "Execute", "Execute", "Failed", 4, 4, "", false, true},
				{"namespace rollback from running Execute", admissionv1.Update, "Execute", "Rollback", "Execute", "Running", 4, 4, "", false, isNamespace},
				{"namespace rollback requires Execute checkpoint", admissionv1.Update, "Execute", "Rollback", "Evaluate", "Succeeded", 2, 2, "", false, false},
				{"rollback blocked by irreversible checkpoint", admissionv1.Update, "Execute", "Rollback", "Execute", "Failed", 4, 4, "Commit", false, false},
				{"namespace successful rollback reopens Evaluate", admissionv1.Update, "Rollback", "Evaluate", "Rollback", "Succeeded", 5, 5, "", true, isNamespace},
				{"namespace unverified rollback cannot reopen Evaluate", admissionv1.Update, "Rollback", "Evaluate", "Rollback", "Succeeded", 5, 5, "", false, false},
				{"namespace failed rollback cannot reopen Evaluate", admissionv1.Update, "Rollback", "Evaluate", "Rollback", "Failed", 5, 5, "", false, false},
				{"namespace Execute to Commit", admissionv1.Update, "Execute", "Commit", "Execute", "Succeeded", 4, 4, "", false, isNamespace},
				{"infrastructure Execute to Finalize", admissionv1.Update, "Execute", "Finalize", "Execute", "Succeeded", 4, 4, "", false, !isNamespace},
			}
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					validator := newTestTransitionValidator(t)
					if isNamespace {
						validator = newTestTransitionValidator(t, readyInfrastructureTransition())
					}
					res := validator.Handle(context.Background(), request(tc.op, tc.old, tc.action, tc.last, tc.state, tc.gen, tc.checkpoint, tc.irreversible, tc.rollbackComplete))
					require.Equal(t, tc.allowed, res.Allowed, "response: %+v", res.Result)
				})
			}
		})
	}
}

func TestDeleteDuringExecuteOrRollback(t *testing.T) {
	for _, resource := range []string{"controlplanenetworktransitions", "namespacenetworktransitions"} {
		t.Run(resource, func(t *testing.T) {
			for _, tc := range []struct {
				action, state, phase string
				irreversible         string
				generation           int64
				allowed              bool
			}{
				{"Execute", "Running", "Executing", "", 3, false},
				{"Execute", "Failed", "Failed", "", 3, false},
				{"Execute", "Succeeded", "Failed", "", 3, false},
				{"Execute", "Succeeded", "Completed", "", 4, false},
				{"Rollback", "Running", "RollingBack", "", 4, false},
				{"Rollback", "Failed", "Failed", "", 4, false},
				{"Rollback", "Succeeded", "RolledBack", "", 4, resource == "namespacenetworktransitions"},
				{"Commit", "Succeeded", "Committed", "Commit", 4, resource == "namespacenetworktransitions"},
				{"Finalize", "Succeeded", "Finalized", "Finalize", 4, resource == "controlplanenetworktransitions"},
			} {
				t.Run(tc.action+"/"+tc.state+"/"+tc.phase, func(t *testing.T) {
					old := map[string]any{"metadata": map[string]any{"generation": 4}, "spec": map[string]any{"action": tc.action}, "status": map[string]any{"lastAction": tc.action, "actionState": tc.state, "actionGeneration": tc.generation, "observedGeneration": tc.generation, "phase": tc.phase}}
					if tc.irreversible != "" {
						old["status"].(map[string]any)["irreversibleAction"] = tc.irreversible
					}
					raw, err := json.Marshal(old)
					require.NoError(t, err)
					req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Delete, Resource: metav1.GroupVersionResource{Group: api.GroupVersion.Group, Version: api.GroupVersion.Version, Resource: resource}, OldObject: runtime.RawExtension{Raw: raw}}}
					require.Equal(t, tc.allowed, newTestTransitionValidator(t).Handle(context.Background(), req).Allowed)
				})
			}
			for _, status := range []any{nil, map[string]any{"lastAction": "Execute", "actionState": "Succeeded", "actionGeneration": 3}} {
				old := map[string]any{"metadata": map[string]any{"generation": 4}, "spec": map[string]any{"action": "Execute"}, "status": status}
				raw, err := json.Marshal(old)
				require.NoError(t, err)
				req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Delete, Resource: metav1.GroupVersionResource{Group: api.GroupVersion.Group, Version: api.GroupVersion.Version, Resource: resource}, OldObject: runtime.RawExtension{Raw: raw}}}
				require.False(t, (TransitionValidator{}).Handle(context.Background(), req).Allowed, "an Execute request without a current checkpoint must not lose its recovery record")
			}
		})
	}
}
