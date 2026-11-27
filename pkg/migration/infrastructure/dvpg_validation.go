/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	stdErrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	nsxPolicy "github.com/vmware/vsphere-automation-sdk-go/services/nsxt"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	nsxWorkflows "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/workflows"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// DVPGValidationAPI is the small NSX workflow surface used by Evaluate.
type DVPGValidationAPI interface {
	Get(string) (*model.DvpgToSubnetWorkflow, error)
	Update(string, *model.DvpgToSubnetWorkflow) error
	ListResults(string) ([]model.DvpgValidationResult, error)
	Delete(string) error
}

// NSXDVPGValidationAPI adapts the generated NSX workflow clients.
type NSXDVPGValidationAPI struct {
	Workflows nsxPolicy.WorkflowsClient
	Results   nsxWorkflows.Dvpg_validation_resultsClient
}

func NewNSXDVPGValidationAPI(client *nsx.Client) (*NSXDVPGValidationAPI, error) {
	if client == nil || client.RestConnector == nil {
		return nil, fmt.Errorf("NSX client is required for DVPG validation")
	}
	return &NSXDVPGValidationAPI{
		Workflows: nsxPolicy.NewWorkflowsClient(client.RestConnector),
		Results:   nsxWorkflows.NewDvpg_validation_resultsClient(client.RestConnector),
	}, nil
}

func (api *NSXDVPGValidationAPI) Get(id string) (*model.DvpgToSubnetWorkflow, error) {
	value, err := api.Workflows.Get(id)
	if err != nil {
		return nil, err
	}
	converted, conversionErrors := common.NewConverter().ConvertToGolang(value, model.DvpgToSubnetWorkflowBindingType())
	if len(conversionErrors) > 0 {
		return nil, conversionErrors[0]
	}
	workflow, ok := converted.(*model.DvpgToSubnetWorkflow)
	if !ok {
		return nil, fmt.Errorf("NSX returned an unexpected DVPG workflow type %T", converted)
	}
	return workflow, nil
}

func (api *NSXDVPGValidationAPI) Update(id string, workflow *model.DvpgToSubnetWorkflow) error {
	value, conversionErrors := common.NewConverter().ConvertToVapi(workflow, model.DvpgToSubnetWorkflowBindingType())
	if len(conversionErrors) > 0 {
		return conversionErrors[0]
	}
	structValue, ok := value.(*data.StructValue)
	if !ok {
		return fmt.Errorf("cannot encode DVPG validation workflow as a struct")
	}
	_, err := api.Workflows.Update(id, structValue)
	return err
}

func (api *NSXDVPGValidationAPI) ListResults(id string) ([]model.DvpgValidationResult, error) {
	pageSize := int64(1000)
	var cursor *string
	seenCursors := make(map[string]struct{})
	var results []model.DvpgValidationResult
	for {
		page, err := api.Results.List(id, nil, cursor, nil, nil, nil, nil, &pageSize, nil, nil, nil)
		if err != nil {
			return nil, err
		}
		results = append(results, page.Results...)
		if page.Cursor == nil || *page.Cursor == "" {
			return results, nil
		}
		if _, found := seenCursors[*page.Cursor]; found {
			return nil, fmt.Errorf("NSX repeated the DVPG validation results cursor")
		}
		seenCursors[*page.Cursor] = struct{}{}
		cursor = page.Cursor
	}
}

func (api *NSXDVPGValidationAPI) Delete(id string) error {
	return api.Workflows.Delete(id)
}

// NSXDVPGCompatibilityValidator runs one validation workflow per VDS. It keeps
// validation workflow IDs deterministic so retries resume the same check.
type NSXDVPGCompatibilityValidator struct {
	API DVPGValidationAPI
}

type dvpgValidationGroup struct {
	vdsRef string
	dvpgs  map[string]struct{}
}

func (validator NSXDVPGCompatibilityValidator) Validate(ctx context.Context, transitionUID string, generation int64, networks []inspection.Network) (DVPGValidationResult, error) {
	if validator.API == nil || transitionUID == "" {
		return DVPGValidationResult{}, fmt.Errorf("DVPG validation API and transition UID are required")
	}
	groups, err := groupDVPGs(networks)
	if err != nil {
		return DVPGValidationResult{}, err
	}
	outcome := DVPGValidationResult{}
	var completedWorkflows []string
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return outcome, err
		}
		workflowID := validationWorkflowID(transitionUID, generation, group)
		workflow, err := validator.API.Get(workflowID)
		if isNSXNotFound(err) {
			if err := validator.API.Update(workflowID, validationWorkflow(workflowID, group)); err != nil {
				return outcome, newEvaluationDiagnostic("could not start the NSX validation workflow", err)
			}
			outcome.InProgress = true
			continue
		}
		if err != nil {
			return outcome, newEvaluationDiagnostic("could not read the NSX validation workflow", err)
		}
		if workflow == nil {
			return outcome, newEvaluationDiagnostic("NSX returned an empty validation workflow", nil)
		}
		if workflow.WorkflowState == nil || workflow.WorkflowState.Status == nil {
			outcome.InProgress = true
			continue
		}
		switch strings.ToUpper(*workflow.WorkflowState.Status) {
		case "IN_PROGRESS":
			outcome.InProgress = true
			continue
		case "FAILED":
			failures := workflowFailure(workflow)
			if len(failures) == 0 {
				failures = []string{fmt.Sprintf("DVPG validation failed for VDS %s", group.vdsRef)}
			}
			outcome.Problems = append(outcome.Problems, failures...)
		case "SUCCESS":
			results, err := validator.API.ListResults(workflowID)
			if err != nil {
				return outcome, newEvaluationDiagnostic("could not read the NSX per-DVPG validation results", err)
			}
			if err := appendValidationFindings(&outcome, group, results); err != nil {
				return outcome, newEvaluationDiagnostic("NSX returned incomplete or invalid per-DVPG validation results", err)
			}
		default:
			return outcome, newEvaluationDiagnostic("NSX returned an unknown DVPG validation workflow status", nil)
		}
		completedWorkflows = append(completedWorkflows, workflowID)
	}
	if !outcome.InProgress {
		outcome.cleanup = func(ctx context.Context) error {
			return validator.deleteWorkflows(ctx, completedWorkflows)
		}
	}
	return outcome, nil
}

func (validator NSXDVPGCompatibilityValidator) deleteWorkflows(ctx context.Context, workflowIDs []string) error {
	for _, workflowID := range workflowIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validator.API.Delete(workflowID); err != nil && !isNSXNotFound(err) {
			return newEvaluationDiagnostic("could not delete a completed NSX validation workflow", err)
		}
	}
	return nil
}

func groupDVPGs(networks []inspection.Network) ([]dvpgValidationGroup, error) {
	groupsByVDS := make(map[string]*dvpgValidationGroup)
	for _, network := range networks {
		if len(network.Source.Topologies) == 0 {
			return nil, fmt.Errorf("VDN %q has no resolved DVPG topology", network.Source.VDNName)
		}
		for _, topology := range network.Source.Topologies {
			if topology.VCenterUUID == "" || topology.SwitchID == "" || topology.PortGroupID == "" {
				return nil, fmt.Errorf("DVPG %q is missing vCenter, VDS or DVPG identity", topology.PortGroupID)
			}
			vdsRef := topology.VCenterUUID + ":" + topology.SwitchID
			group := groupsByVDS[vdsRef]
			if group == nil {
				group = &dvpgValidationGroup{vdsRef: vdsRef, dvpgs: make(map[string]struct{})}
				groupsByVDS[vdsRef] = group
			}
			group.dvpgs[topology.VCenterUUID+":"+topology.PortGroupID] = struct{}{}
		}
	}
	if len(groupsByVDS) == 0 {
		return nil, fmt.Errorf("no source DVPGs are available for conversion validation")
	}
	vdsRefs := make([]string, 0, len(groupsByVDS))
	for vdsRef := range groupsByVDS {
		vdsRefs = append(vdsRefs, vdsRef)
	}
	slices.Sort(vdsRefs)
	groups := make([]dvpgValidationGroup, 0, len(vdsRefs))
	for _, vdsRef := range vdsRefs {
		groups = append(groups, *groupsByVDS[vdsRef])
	}
	return groups, nil
}

func validationWorkflowID(transitionUID string, generation int64, group dvpgValidationGroup) string {
	dvpgRefs := make([]string, 0, len(group.dvpgs))
	for dvpgRef := range group.dvpgs {
		dvpgRefs = append(dvpgRefs, dvpgRef)
	}
	slices.Sort(dvpgRefs)
	identity := fmt.Sprintf("%s:%d:%s:%s", transitionUID, generation, group.vdsRef, strings.Join(dvpgRefs, ","))
	hash := sha256.Sum256([]byte(identity))
	return "migration-eval-" + hex.EncodeToString(hash[:12])
}

func validationWorkflow(id string, group dvpgValidationGroup) *model.DvpgToSubnetWorkflow {
	dvpgRefs := make([]string, 0, len(group.dvpgs))
	for dvpgRef := range group.dvpgs {
		dvpgRefs = append(dvpgRefs, dvpgRef)
	}
	slices.Sort(dvpgRefs)
	dvpgSpecs := make([]model.DvpgToSubnetDvpgSpec, 0, len(dvpgRefs))
	for _, dvpgRef := range dvpgRefs {
		dvpgSpecs = append(dvpgSpecs, model.DvpgToSubnetDvpgSpec{DvpgRef: ptr.To(dvpgRef)})
	}
	displayName := "Migration DVPG compatibility validation"
	desiredState := model.DvpgToSubnetWorkflow_DESIRED_STATE_VALIDATION
	return &model.DvpgToSubnetWorkflow{
		Id:           &id,
		DisplayName:  &displayName,
		ResourceType: model.BaseWorkflowObject_RESOURCE_TYPE_DVPGTOSUBNETWORKFLOW,
		DesiredState: &desiredState,
		VdsRef:       ptr.To(group.vdsRef),
		DvpgSpec:     dvpgSpecs,
	}
}

func appendValidationFindings(outcome *DVPGValidationResult, group dvpgValidationGroup, results []model.DvpgValidationResult) error {
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if result.DvpgRef == nil || *result.DvpgRef == "" {
			return fmt.Errorf("NSX validation result for VDS %s has no DVPG reference", group.vdsRef)
		}
		if _, expected := group.dvpgs[*result.DvpgRef]; !expected {
			return fmt.Errorf("NSX returned an unexpected DVPG validation result %q", *result.DvpgRef)
		}
		if _, duplicate := seen[*result.DvpgRef]; duplicate {
			return fmt.Errorf("NSX returned duplicate DVPG validation result %q", *result.DvpgRef)
		}
		seen[*result.DvpgRef] = struct{}{}
		name := "unknown DVPG"
		if result.DvpgName != nil && *result.DvpgName != "" {
			name = *result.DvpgName
		} else if result.DvpgRef != nil && *result.DvpgRef != "" {
			name = *result.DvpgRef
		}
		compatibility := ""
		if result.Compatibility != nil {
			compatibility = strings.ToUpper(*result.Compatibility)
		}
		switch compatibility {
		case model.DvpgValidationResult_COMPATIBILITY_SUPPORTED,
			model.DvpgValidationResult_COMPATIBILITY_PARTIALLY_SUPPORTED,
			model.DvpgValidationResult_COMPATIBILITY_UNSUPPORTED:
		default:
			return fmt.Errorf("NSX returned unknown compatibility %q for DVPG %s", compatibility, name)
		}
		if compatibility == model.DvpgValidationResult_COMPATIBILITY_UNSUPPORTED {
			outcome.Problems = append(outcome.Problems, fmt.Sprintf("DVPG %s is not supported for conversion", name))
		}
		if compatibility == model.DvpgValidationResult_COMPATIBILITY_PARTIALLY_SUPPORTED {
			outcome.Warnings = append(outcome.Warnings, fmt.Sprintf("DVPG %s is only partially supported for conversion", name))
		}
		for _, issue := range result.Issues {
			message := fmt.Sprintf("DVPG %s: %s", name, optionalString(issue.Summary))
			if issue.Setting != nil && *issue.Setting != "" {
				message += " (" + *issue.Setting + ")"
			}
			if issue.Severity != nil && strings.EqualFold(*issue.Severity, model.DvpgValidationIssue_SEVERITY_ERROR) {
				outcome.Problems = append(outcome.Problems, message)
			} else {
				outcome.Warnings = append(outcome.Warnings, message)
			}
		}
	}
	for dvpgRef := range group.dvpgs {
		if _, found := seen[dvpgRef]; !found {
			return fmt.Errorf("NSX did not return a compatibility result for DVPG %s", dvpgRef)
		}
	}
	return nil
}

func workflowFailure(workflow *model.DvpgToSubnetWorkflow) []string {
	if workflow.WorkflowState == nil {
		return nil
	}
	return append([]string(nil), workflow.WorkflowState.Errors...)
}

func isNSXNotFound(err error) bool {
	var notFound *stdErrors.NotFound
	return errors.As(err, &notFound)
}

func optionalString(value *string) string {
	if value == nil || *value == "" {
		return "conversion compatibility issue"
	}
	return *value
}
