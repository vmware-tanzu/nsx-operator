/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
	stdErrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/utils/ptr"
)

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
	validator := NSXDVPGCompatibilityValidator{API: api}
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
	require.Equal(t, "vc-1:dvs-1", *workflow.VdsRef)
	require.Len(t, workflow.DvpgSpec, 2)
}

func TestNSXDVPGCompatibilityValidatorRejectsUnresolvedTopology(t *testing.T) {
	api := &fakeDVPGValidationAPI{workflows: make(map[string]*model.DvpgToSubnetWorkflow)}
	validator := NSXDVPGCompatibilityValidator{API: api}

	_, err := validator.Validate(context.Background(), "transition-uid", 7, []inspection.Network{{
		Source: inspection.Source{VDNName: "workload-network"},
	}})
	require.ErrorContains(t, err, `VDN "workload-network" has no resolved DVPG topology`)
	require.Empty(t, api.updated)
}

func TestNSXDVPGCompatibilityValidatorReportsUnsupportedDVPG(t *testing.T) {
	api := &fakeDVPGValidationAPI{workflows: make(map[string]*model.DvpgToSubnetWorkflow), results: make(map[string][]model.DvpgValidationResult)}
	validator := NSXDVPGCompatibilityValidator{API: api}
	networks := []inspection.Network{{Source: inspection.Source{Topologies: []inspection.Topology{{
		VCenterUUID: "vc-1", SwitchID: "dvs-1", PortGroupID: "dvpg-1",
	}}}}}

	first, err := validator.Validate(context.Background(), "transition-uid", 7, networks)
	require.NoError(t, err)
	require.True(t, first.InProgress)
	workflowID := validationWorkflowID("transition-uid", 7, dvpgValidationGroup{
		vdsRef: "vc-1:dvs-1", dvpgs: map[string]struct{}{"vc-1:dvpg-1": {}},
	})
	api.workflows[workflowID] = &model.DvpgToSubnetWorkflow{WorkflowState: &model.DvpgToSubnetWorkflowState{Status: ptr.To("SUCCESS")}}
	api.results[workflowID] = []model.DvpgValidationResult{{
		Compatibility: ptr.To(model.DvpgValidationResult_COMPATIBILITY_UNSUPPORTED),
		DvpgName:      ptr.To("workload-network"),
		DvpgRef:       ptr.To("vc-1:dvpg-1"),
	}}

	result, err := validator.Validate(context.Background(), "transition-uid", 7, networks)
	require.NoError(t, err)
	require.False(t, result.InProgress)
	require.Equal(t, []string{"DVPG workload-network is not supported for conversion"}, result.Problems)
	require.Equal(t, []string{workflowID}, api.deleted)
}

func TestValidationWorkflowIDChangesWithGeneration(t *testing.T) {
	group := dvpgValidationGroup{vdsRef: "vc-1:dvs-1", dvpgs: map[string]struct{}{"vc-1:dvpg-1": {}}}
	require.NotEqual(t, validationWorkflowID("transition-uid", 1, group), validationWorkflowID("transition-uid", 2, group))
}
