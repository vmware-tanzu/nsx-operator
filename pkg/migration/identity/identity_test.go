/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package identity

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/fabric"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

type topologyReaderFunc func(context.Context, string) ([]SupervisorZone, error)

func (reader topologyReaderFunc) ReadTopology(ctx context.Context, supervisorID string) ([]SupervisorZone, error) {
	return reader(ctx, supervisorID)
}

type computeManagerReaderFunc func(context.Context) ([]ComputeManager, error)

func (reader computeManagerReaderFunc) ListComputeManagers(ctx context.Context) ([]ComputeManager, error) {
	return reader(ctx)
}

type computeManagersClientStub struct {
	fabric.ComputeManagersClient
	results []model.ComputeManager
}

func (stub computeManagersClientStub) List(*string, *string, *string, *int64, *string, *bool, *string) (model.ComputeManagerListResult, error) {
	return model.ComputeManagerListResult{Results: stub.results}, nil
}

func TestResolverResolve(t *testing.T) {
	const supervisorID = "123e4567-e89b-12d3-a456-426614174000"
	const vCenterUUID = "abcdef12-3456-789a-bcde-f0123456789a"
	zones := []SupervisorZone{{Zone: "zone-1", Clusters: []string{"cluster-1"}}}
	resolver := func(topology []SupervisorZone, topologyErr error, managers []ComputeManager, managersErr error) Resolver {
		return Resolver{
			SupervisorTopology: topologyReaderFunc(func(context.Context, string) ([]SupervisorZone, error) { return topology, topologyErr }),
			ComputeManagers:    computeManagerReaderFunc(func(context.Context) ([]ComputeManager, error) { return managers, managersErr }),
			VCenterUUID:        vCenterUUID,
		}
	}

	t.Run("matches vCenter by instance UUID regardless of server", func(t *testing.T) {
		reader := resolver(zones, nil, nil, nil)
		reader.ComputeManagers = NSXComputeManagers{Client: computeManagersClientStub{results: []model.ComputeManager{
			{Id: stringPointer("wrong-uuid"), OriginType: stringPointer("VCENTER"), Server: stringPointer("vcenter.example.com"),
				OriginProperties: []model.KeyValuePair{
					{Key: stringPointer("otherProperty"), Value: stringPointer(vCenterUUID)},
					{Key: stringPointer("instanceUuid"), Value: stringPointer("abcdef12-3456-789a-bcde-f0123456789b")},
				}},
			{Id: stringPointer("missing-uuid"), OriginType: stringPointer("VCENTER"), Server: stringPointer("vcenter.example.com")},
			{Id: stringPointer("wrong-origin"), OriginType: stringPointer("NSX"),
				OriginProperties: []model.KeyValuePair{{Key: stringPointer("instanceUuid"), Value: stringPointer(vCenterUUID)}}},
			{Id: stringPointer("cm-id"), OriginType: stringPointer("vCenter"), Server: stringPointer("192.0.2.10"),
				OriginProperties: []model.KeyValuePair{{Key: stringPointer("instanceUuid"), Value: stringPointer("ABCDEF12-3456-789A-BCDE-F0123456789A")}}},
		}}}
		got, err := reader.Resolve(context.Background(), supervisorID, 6)
		require.NoError(t, err)
		require.Equal(t, api.ResolvedSourceContext{EvaluatedGeneration: 6, VCenterInstanceUUID: vCenterUUID, NSXComputeManagerID: "cm-id"}, got)
	})

	t.Run("rejects invalid Supervisor ID before lookups", func(t *testing.T) {
		var topologyCalls, managerCalls int
		reader := resolver(zones, nil, nil, nil)
		reader.SupervisorTopology = topologyReaderFunc(func(context.Context, string) ([]SupervisorZone, error) {
			topologyCalls++
			return zones, nil
		})
		reader.ComputeManagers = computeManagerReaderFunc(func(context.Context) ([]ComputeManager, error) {
			managerCalls++
			return nil, nil
		})
		_, err := reader.Resolve(context.Background(), "not-a-uuid", 6)
		require.ErrorContains(t, err, "Supervisor ID, and Evaluate generation are required")
		require.Zero(t, topologyCalls)
		require.Zero(t, managerCalls)
	})

	t.Run("rejects missing prerequisites", func(t *testing.T) {
		_, err := (Resolver{}).Resolve(context.Background(), supervisorID, 6)
		require.ErrorContains(t, err, "authenticated vCenter and NSX clients")
		reader := resolver(zones, nil, nil, nil)
		_, err = reader.Resolve(context.Background(), supervisorID, 0)
		require.ErrorContains(t, err, "Evaluate generation are required")
	})

	t.Run("wraps topology read error", func(t *testing.T) {
		wantErr := errors.New("vCenter unavailable")
		_, err := resolver(nil, wantErr, nil, nil).Resolve(context.Background(), supervisorID, 6)
		require.ErrorIs(t, err, wantErr)
		require.ErrorContains(t, err, "validate Supervisor ID against vCenter topology")
	})

	t.Run("rejects invalid topology", func(t *testing.T) {
		_, err := resolver(nil, nil, nil, nil).Resolve(context.Background(), supervisorID, 6)
		require.ErrorContains(t, err, "no enabled zones")
	})

	t.Run("wraps NSX list error", func(t *testing.T) {
		wantErr := errors.New("NSX unavailable")
		_, err := resolver(zones, nil, nil, wantErr).Resolve(context.Background(), supervisorID, 6)
		require.ErrorIs(t, err, wantErr)
		require.ErrorContains(t, err, "list NSX compute managers")
	})

	t.Run("rejects missing, duplicate, and empty manager identities", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			managers []ComputeManager
			message  string
		}{
			{name: "no match", managers: []ComputeManager{{ID: "other", OriginType: "VCENTER"}}, message: "found 0"},
			{name: "duplicate matches", managers: []ComputeManager{{ID: "one", OriginType: "VCENTER", InstanceUUID: vCenterUUID}, {ID: "two", OriginType: "vcenter", InstanceUUID: vCenterUUID}}, message: "found 2"},
			{name: "empty manager ID", managers: []ComputeManager{{OriginType: "VCENTER", InstanceUUID: vCenterUUID}}, message: "has an empty ID"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := resolver(zones, nil, tc.managers, nil).Resolve(context.Background(), supervisorID, 6)
				require.ErrorContains(t, err, tc.message)
			})
		}
	})
}

func TestValidateTopology(t *testing.T) {
	for _, tc := range []struct {
		name  string
		zones []SupervisorZone
		want  string
	}{
		{name: "valid topology", zones: []SupervisorZone{{Zone: "zone", Clusters: []string{"cluster"}}}},
		{name: "no zones", want: "no enabled zones"},
		{name: "missing zone ID", zones: []SupervisorZone{{Clusters: []string{"cluster"}}}, want: "zone without an ID"},
		{name: "missing cluster ID", zones: []SupervisorZone{{Zone: "zone", Clusters: []string{""}}}, want: "empty cluster ID"},
		{name: "no clusters", zones: []SupervisorZone{{Zone: "zone"}}, want: "no enabled clusters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTopology(tc.zones)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestNewResolverRequiresAuthenticatedClients(t *testing.T) {
	_, err := NewResolver(nil)
	require.ErrorContains(t, err, "authenticated vCenter SOAP/REST and NSX clients are required")
	_, err = NewResolver(&AuthenticatedClients{})
	require.ErrorContains(t, err, "authenticated vCenter SOAP/REST and NSX clients are required")
	_, err = NewResolver(&AuthenticatedClients{VCenter: &govmomi.Client{}})
	require.ErrorContains(t, err, "authenticated vCenter identity is incomplete")
}

func TestVCenterSupervisorTopologyRefreshesExpiredSessions(t *testing.T) {
	const supervisorID = "123e4567-e89b-12d3-a456-426614174000"
	zones := []SupervisorZone{{Zone: "zone", Clusters: []string{"cluster"}}}
	t.Run("refresh before read", func(t *testing.T) {
		manager := &fakeRESTSessionManager{}
		reader := VCenterSupervisorTopology{sessionManager: manager, credentials: url.UserPassword("user", "secret")}
		reader.readTopologyFn = func(context.Context, string) ([]SupervisorZone, error) { return zones, nil }
		got, err := reader.ReadTopology(context.Background(), supervisorID)
		require.NoError(t, err)
		require.Equal(t, zones, got)
		require.Equal(t, 1, manager.logins)
	})

	t.Run("refreshes and retries after session expires during read", func(t *testing.T) {
		manager := &fakeRESTSessionManager{session: &rest.Session{User: "user"}}
		reader := VCenterSupervisorTopology{sessionManager: manager, credentials: url.UserPassword("user", "secret")}
		readCalls := 0
		reader.readTopologyFn = func(context.Context, string) ([]SupervisorZone, error) {
			readCalls++
			if readCalls == 1 {
				manager.session = nil
				return nil, errors.New("401 Unauthorized")
			}
			return zones, nil
		}
		got, err := reader.ReadTopology(context.Background(), supervisorID)
		require.NoError(t, err)
		require.Equal(t, zones, got)
		require.Equal(t, 2, readCalls)
		require.Equal(t, 1, manager.logins)
	})

	t.Run("does not retry unrelated read errors with a live session", func(t *testing.T) {
		manager := &fakeRESTSessionManager{session: &rest.Session{User: "user"}}
		reader := VCenterSupervisorTopology{sessionManager: manager, credentials: url.UserPassword("user", "secret")}
		wantErr := errors.New("vCenter topology unavailable")
		readCalls := 0
		reader.readTopologyFn = func(context.Context, string) ([]SupervisorZone, error) {
			readCalls++
			return nil, wantErr
		}
		_, err := reader.ReadTopology(context.Background(), supervisorID)
		require.ErrorIs(t, err, wantErr)
		require.Equal(t, 1, readCalls)
	})

	t.Run("rejects invalid ID without session calls", func(t *testing.T) {
		manager := &fakeRESTSessionManager{}
		reader := VCenterSupervisorTopology{sessionManager: manager, credentials: url.UserPassword("user", "secret")}
		_, err := reader.ReadTopology(context.Background(), "invalid")
		require.ErrorContains(t, err, "authenticated vCenter REST client and Supervisor ID are required")
		require.Zero(t, manager.logins)
	})
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

func TestStringValue(t *testing.T) {
	value := "value"
	require.Equal(t, "value", stringValue(&value))
	require.Empty(t, stringValue(nil))
}
