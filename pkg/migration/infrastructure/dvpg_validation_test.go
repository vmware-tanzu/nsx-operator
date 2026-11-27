/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stdErrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	policyclient "github.com/vmware/vsphere-automation-sdk-go/runtime/protocol/client"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

func TestNSXDVPGCompatibilityValidatorCreatesWorkflowAfterNotFound(t *testing.T) {
	workflowID := validationWorkflowID("transition-uid", 7, dvpgValidationGroup{
		vdsRef: "cm-1:dvs-1", dvpgs: map[string]struct{}{"cm-1:dvpg-1": {}},
	})
	var createCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/policy/api/v1/workflows/"+workflowID, request.URL.Path)
		response.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			if createCalls.Load() == 0 {
				response.WriteHeader(http.StatusNotFound)
				_, _ = response.Write([]byte(`{"error_code":600,"error_message":"workflow not found"}`))
				return
			}
		case http.MethodPut:
			var body map[string]any
			assert.NoError(t, json.NewDecoder(request.Body).Decode(&body))
			assert.Equal(t, workflowID, body["id"])
			assert.Equal(t, "VALIDATION", body["desired_state"])
			assert.Equal(t, "cm-1:dvs-1", body["vds_ref"])
			assert.Equal(t, []any{map[string]any{"dvpg_ref": "cm-1:dvpg-1"}}, body["dvpg_spec"])
			createCalls.Add(1)
		default:
			t.Errorf("unexpected HTTP method %s", request.Method)
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_, _ = response.Write([]byte(`{"id":"` + workflowID + `","resource_type":"DvpgToSubnetWorkflow","workflow_state":{"status":"IN_PROGRESS"}}`))
	}))
	t.Cleanup(server.Close)
	connector := policyclient.NewConnector(server.URL, policyclient.UsingRest(nil), policyclient.WithHttpClient(server.Client()))
	api, err := NewNSXDVPGValidationAPI(&nsx.Client{RestConnector: connector})
	require.NoError(t, err)
	validator := NSXDVPGCompatibilityValidator{API: api, ComputeManagerID: "cm-1", VCenterUUID: "vc-1"}
	networks := []inspection.Network{{Source: inspection.Source{Topologies: []inspection.Topology{{
		VCenterUUID: "vc-1", SwitchID: "dvs-1", PortGroupID: "dvpg-1",
	}}}}}

	for range 2 {
		result, err := validator.Validate(context.Background(), "transition-uid", 7, networks)
		require.NoError(t, err)
		require.True(t, result.InProgress)
		require.EqualValues(t, 1, createCalls.Load())
	}
}

type fakeDVPGValidationAPI struct {
	workflows map[string]*model.DvpgToSubnetWorkflow
	results   map[string][]model.DvpgValidationResult
	updated   []*model.DvpgToSubnetWorkflow
	deleted   []string
}

func (api *fakeDVPGValidationAPI) Get(id string) (*model.DvpgToSubnetWorkflow, error) {
	workflow, found := api.workflows[id]
	if !found {
		return nil, stdErrors.NewNotFound()
	}
	return workflow, nil
}

func (api *fakeDVPGValidationAPI) Update(id string, workflow *model.DvpgToSubnetWorkflow) error {
	api.updated = append(api.updated, workflow)
	api.workflows[id] = workflow
	return nil
}

func (api *fakeDVPGValidationAPI) ListResults(id string) ([]model.DvpgValidationResult, error) {
	return api.results[id], nil
}

func (api *fakeDVPGValidationAPI) Delete(id string) error {
	api.deleted = append(api.deleted, id)
	delete(api.workflows, id)
	return nil
}

func TestNSXDVPGCompatibilityValidatorStartsOneValidationPerVDS(t *testing.T) {
	api := &fakeDVPGValidationAPI{workflows: make(map[string]*model.DvpgToSubnetWorkflow)}
	validator := NSXDVPGCompatibilityValidator{API: api, ComputeManagerID: "cm-1", VCenterUUID: "vc-1"}
	networks := []inspection.Network{
		{Source: inspection.Source{Topologies: []inspection.Topology{
			{VCenterUUID: "vc-1", SwitchID: "dvs-1", PortGroupID: "dvpg-1"},
			{VCenterUUID: "vc-1", SwitchID: "dvs-1", PortGroupID: "dvpg-2"},
		}}},
		{Source: inspection.Source{Topologies: []inspection.Topology{
			{VCenterUUID: "vc-1", SwitchID: "dvs-2", PortGroupID: "dvpg-3"},
			{VCenterUUID: "vc-1", SwitchID: "dvs-1", PortGroupID: "dvpg-1"},
		}}},
	}

	result, err := validator.Validate(context.Background(), "transition-uid", 7, networks)
	require.NoError(t, err)
	require.True(t, result.InProgress)
	require.Len(t, api.updated, 2)

	workflow := api.updated[0]
	require.Equal(t, model.DvpgToSubnetWorkflow_DESIRED_STATE_VALIDATION, *workflow.DesiredState)
	require.Equal(t, model.BaseWorkflowObject_RESOURCE_TYPE_DVPGTOSUBNETWORKFLOW, workflow.ResourceType)
	require.Equal(t, "cm-1:dvs-1", *workflow.VdsRef)
	require.Len(t, workflow.DvpgSpec, 2)

	firstID, secondID := *workflow.Id, *api.updated[1].Id
	workflow.WorkflowState = &model.DvpgToSubnetWorkflowState{Status: ptr.To("FAILED")}
	api.updated[1].WorkflowState = &model.DvpgToSubnetWorkflowState{Status: ptr.To("IN_PROGRESS")}
	result, err = validator.Validate(context.Background(), "transition-uid", 7, networks)
	require.NoError(t, err)
	require.True(t, result.InProgress)
	require.Equal(t, []string{"DVPG validation failed for VDS cm-1:dvs-1"}, result.Problems)
	require.Nil(t, result.cleanup)
	require.Empty(t, api.deleted)

	api.updated[1].WorkflowState.Status = ptr.To("FAILED")
	result, err = validator.Validate(context.Background(), "transition-uid", 7, networks)
	require.NoError(t, err)
	require.False(t, result.InProgress)
	require.Equal(t, []string{
		"DVPG validation failed for VDS cm-1:dvs-1",
		"DVPG validation failed for VDS cm-1:dvs-2",
	}, result.Problems)
	require.Len(t, api.updated, 2)
	require.Empty(t, api.deleted)
	require.NoError(t, result.deleteWorkflows(context.Background()))
	require.ElementsMatch(t, []string{firstID, secondID}, api.deleted)
}

func TestNSXDVPGCompatibilityValidatorRejectsUnresolvedTopology(t *testing.T) {
	api := &fakeDVPGValidationAPI{workflows: make(map[string]*model.DvpgToSubnetWorkflow)}
	validator := NSXDVPGCompatibilityValidator{API: api, ComputeManagerID: "cm-1", VCenterUUID: "vc-1"}

	_, err := validator.Validate(context.Background(), "transition-uid", 7, []inspection.Network{{
		Source: inspection.Source{VDNName: "workload-network"},
	}})
	require.ErrorContains(t, err, `VDN "workload-network" has no resolved DVPG topology`)
	require.Empty(t, api.updated)
}

func TestNSXDVPGCompatibilityValidatorReportsUnsupportedDVPG(t *testing.T) {
	api := &fakeDVPGValidationAPI{workflows: make(map[string]*model.DvpgToSubnetWorkflow), results: make(map[string][]model.DvpgValidationResult)}
	validator := NSXDVPGCompatibilityValidator{API: api, ComputeManagerID: "cm-1", VCenterUUID: "vc-1"}
	networks := []inspection.Network{{Source: inspection.Source{Topologies: []inspection.Topology{{
		VCenterUUID: "vc-1", SwitchID: "dvs-1", PortGroupID: "dvpg-1",
	}}}}}

	first, err := validator.Validate(context.Background(), "transition-uid", 7, networks)
	require.NoError(t, err)
	require.True(t, first.InProgress)
	workflowID := validationWorkflowID("transition-uid", 7, dvpgValidationGroup{
		vdsRef: "cm-1:dvs-1", dvpgs: map[string]struct{}{"cm-1:dvpg-1": {}},
	})
	api.workflows[workflowID] = &model.DvpgToSubnetWorkflow{WorkflowState: &model.DvpgToSubnetWorkflowState{Status: ptr.To("SUCCESS")}}
	api.results[workflowID] = []model.DvpgValidationResult{{
		Compatibility: ptr.To(model.DvpgValidationResult_COMPATIBILITY_UNSUPPORTED),
		DvpgName:      ptr.To("workload-network"),
		DvpgRef:       ptr.To("cm-1:dvpg-1"),
	}}

	result, err := validator.Validate(context.Background(), "transition-uid", 7, networks)
	require.NoError(t, err)
	require.False(t, result.InProgress)
	require.Equal(t, []string{"DVPG workload-network is not supported for conversion"}, result.Problems)
	require.NoError(t, result.deleteWorkflows(context.Background()))
	require.Equal(t, []string{workflowID}, api.deleted)
}

func TestValidationWorkflowIDChangesWithGeneration(t *testing.T) {
	group := dvpgValidationGroup{vdsRef: "cm-1:dvs-1", dvpgs: map[string]struct{}{"cm-1:dvpg-1": {}}}
	require.NotEqual(t, validationWorkflowID("transition-uid", 1, group), validationWorkflowID("transition-uid", 2, group))
}
