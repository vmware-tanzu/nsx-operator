/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestNormalizeServer(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{name: "URL with scheme and port", input: " HTTPS://VCENTER.EXAMPLE.COM:443/sdk ", want: "vcenter.example.com"},
		{name: "host and port", input: "VCENTER.EXAMPLE.COM:443", want: "vcenter.example.com"},
		{name: "trailing dot", input: "VCENTER.EXAMPLE.COM.", want: "vcenter.example.com"},
		{name: "bare host", input: "vcenter.example.com", want: "vcenter.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, normalizeServer(tc.input)) })
	}
}

func TestNSXComputeManagersPagination(t *testing.T) {
	t.Run("follows cursors", func(t *testing.T) {
		var cursors []string
		reader := NSXComputeManagers{listPage: func(cursor *string, pageSize int64) (computeManagerPage, error) {
			require.EqualValues(t, 1000, pageSize)
			if cursor == nil {
				cursors = append(cursors, "")
				return computeManagerPage{Managers: []ComputeManager{{ID: "one"}}, Cursor: stringPointer("next")}, nil
			}
			cursors = append(cursors, *cursor)
			return computeManagerPage{Managers: []ComputeManager{{ID: "two"}}}, nil
		}}

		managers, err := reader.ListComputeManagers(context.Background())
		require.NoError(t, err)
		require.Equal(t, []string{"", "next"}, cursors)
		require.Equal(t, []string{"one", "two"}, []string{managers[0].ID, managers[1].ID})
	})

	t.Run("rejects repeated cursor", func(t *testing.T) {
		calls := 0
		reader := NSXComputeManagers{listPage: func(cursor *string, _ int64) (computeManagerPage, error) {
			calls++
			return computeManagerPage{Cursor: stringPointer("same")}, nil
		}}
		_, err := reader.ListComputeManagers(context.Background())
		require.ErrorContains(t, err, "repeated a compute-manager pagination cursor")
		require.Equal(t, 2, calls)
	})

	t.Run("propagates page error", func(t *testing.T) {
		wantErr := errors.New("NSX unavailable")
		reader := NSXComputeManagers{listPage: func(*string, int64) (computeManagerPage, error) {
			return computeManagerPage{}, wantErr
		}}
		_, err := reader.ListComputeManagers(context.Background())
		require.ErrorIs(t, err, wantErr)
	})
}

func TestValidateReuse(t *testing.T) {
	const generation = int64(7)
	validSource := &api.ResolvedSourceContext{
		EvaluatedGeneration: generation,
		VCenterInstanceUUID: "vc-uuid",
		NSXComputeManagerID: "cm-id",
	}
	validConditions := []metav1.Condition{
		{Type: "NetworkInputsValidated", Status: metav1.ConditionTrue, ObservedGeneration: generation},
		{Type: "DVPGConversionValidated", Status: metav1.ConditionTrue, ObservedGeneration: generation},
	}
	validStatus := &api.ControlPlaneNetworkTransitionStatus{TransitionStatus: api.TransitionStatus{Conditions: validConditions}}
	require.NoError(t, RecordResolvedSourceContext(validStatus, *validSource, generation))
	wrongVCenterStatus := validStatus.DeepCopy()
	wrongVCenterStatus.ResolvedSourceContext.VCenterInstanceUUID = "other"
	staleConditionStatus := validStatus.DeepCopy()
	staleConditionStatus.Conditions[0].ObservedGeneration = generation - 1
	falseConditionStatus := validStatus.DeepCopy()
	falseConditionStatus.Conditions[0].Status = metav1.ConditionFalse

	tests := []struct {
		name              string
		status            *api.ControlPlaneNetworkTransitionStatus
		action            api.Action
		vCenter           string
		currentGeneration int64
		wantErr           string
	}{
		{name: "valid persisted Evaluate", status: validStatus, action: api.ActionExecute, vCenter: "vc-uuid", currentGeneration: generation + 1},
		{name: "missing source", action: api.ActionExecute, vCenter: "vc-uuid", currentGeneration: generation + 1, wantErr: "resolved source context is missing"},
		{name: "wrong vCenter", status: wrongVCenterStatus, action: api.ActionExecute, vCenter: "vc-uuid", currentGeneration: generation + 1, wantErr: "different vCenter"},
		{name: "wrong action", status: validStatus, action: api.ActionEvaluate, vCenter: "vc-uuid", currentGeneration: generation + 1, wantErr: "cannot be reused"},
		{name: "missing source checkpoint", status: &api.ControlPlaneNetworkTransitionStatus{}, action: api.ActionExecute, vCenter: "vc-uuid", currentGeneration: generation + 1, wantErr: "resolved source context is missing"},
		{name: "inputs changed after last Evaluate", status: validStatus, action: api.ActionExecute, vCenter: "vc-uuid", currentGeneration: generation + 2, wantErr: "does not match the Evaluate generation"},
		{name: "stale condition generation", status: staleConditionStatus, action: api.ActionExecute, vCenter: "vc-uuid", currentGeneration: generation + 1, wantErr: "not backed by successful Evaluate checks"},
		{name: "false condition", status: falseConditionStatus, action: api.ActionExecute, vCenter: "vc-uuid", currentGeneration: generation + 1, wantErr: "not backed by successful Evaluate checks"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateReuse(tc.status, tc.action, tc.vCenter, tc.currentGeneration)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestRecordResolvedSourceContext(t *testing.T) {
	source := api.ResolvedSourceContext{EvaluatedGeneration: 5, VCenterInstanceUUID: "vc-uuid", NSXComputeManagerID: "cm-id"}
	status := &api.ControlPlaneNetworkTransitionStatus{}
	require.NoError(t, RecordResolvedSourceContext(status, source, 5))
	require.Equal(t, &source, status.ResolvedSourceContext)

	require.ErrorContains(t, RecordResolvedSourceContext(status, source, 6), "does not match Evaluate generation")
	require.ErrorContains(t, RecordResolvedSourceContext(status, api.ResolvedSourceContext{EvaluatedGeneration: 5}, 5), "must include vCenter UUID")
	require.ErrorContains(t, RecordResolvedSourceContext(nil, source, 5), "status is required")
}

func stringPointer(value string) *string { return &value }
