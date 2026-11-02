/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package nsxworkflow adds migration polling and target checks to the NSX SDK.
// Workflow requests, responses and DVPG configuration use SDK models directly.
package nsxworkflow

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	nsxt "github.com/vmware/vsphere-automation-sdk-go/services/nsxt"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/workflows"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

const (
	defaultPollInterval = 2 * time.Second
	maxValidationPages  = 1000
)

// DesiredState is a migration phase supported by the SDK workflow model.
type DesiredState = string

const (
	DesiredStateValidation         = model.DvpgToSubnetWorkflow_DESIRED_STATE_VALIDATION
	DesiredStatePlan               = model.DvpgToSubnetWorkflow_DESIRED_STATE_PLAN
	DesiredStateConversion         = model.DvpgToSubnetWorkflow_DESIRED_STATE_CONVERSION
	DesiredStateRollbackConversion = model.DvpgToSubnetWorkflow_DESIRED_STATE_ROLLBACK_CONVERSION
)

// Client uses the shared SDK clients for all network calls. Context cancellation
// stops polling; in-flight SDK calls use the standard NSX transport timeout.
type Client struct {
	workflows         nsxt.WorkflowsClient
	validationResults workflows.Dvpg_validation_resultsClient
	PollInterval      time.Duration
}

// NewClient wraps an NSX client initialized by nsx.NewMigrationClient.
func NewClient(client *nsx.Client) *Client {
	return &Client{workflows: client.WorkflowsClient, validationResults: client.WorkflowValidationResultsClient}
}

// Put creates or advances the workflow using the SDK's typed request and binding.
// NSX validates request configuration. Auto-commit is disabled so the migration
// operator retains control of Commit/Finalize and the rollback boundary.
func (client *Client) Put(ctx context.Context, request model.DvpgToSubnetWorkflow) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ptr.Deref(request.AutoCommit, false) {
		return errors.New("workflow auto-commit would bypass migration Commit/Finalize")
	}
	request.ResourceType = model.DvpgToSubnetWorkflow__TYPE_IDENTIFIER
	request.AutoCommit = ptr.To(false)
	value, bindingErrors := request.GetDataValue__()
	if len(bindingErrors) != 0 {
		return bindings.VAPIerrorsToError(bindingErrors)
	}
	_, err := client.workflows.Update(ptr.Deref(request.Id, ""), value.(*data.StructValue))
	return err
}

// GetStatus reads the workflow and its nested workflow_state using SDK Get.
func (client *Client) GetStatus(ctx context.Context, workflowID string) (model.DvpgToSubnetWorkflow, error) {
	var workflow model.DvpgToSubnetWorkflow
	if err := ctx.Err(); err != nil {
		return workflow, err
	}
	value, err := client.workflows.Get(workflowID)
	if err != nil {
		return workflow, err
	}
	if value == nil {
		return workflow, errors.New("NSX returned an empty workflow")
	}
	decoded, bindingErrors := bindings.NewTypeConverter().ConvertToGolang(value, model.DvpgToSubnetWorkflowBindingType())
	if len(bindingErrors) != 0 {
		return workflow, bindings.VAPIerrorsToError(bindingErrors)
	}
	workflow = decoded.(model.DvpgToSubnetWorkflow)
	if ptr.Deref(workflow.Id, "") != workflowID || workflow.ResourceType != model.DvpgToSubnetWorkflow__TYPE_IDENTIFIER {
		return workflow, fmt.Errorf("NSX returned an unexpected workflow identity or type for %q", workflowID)
	}
	return workflow, ctx.Err()
}

func (client *Client) GetValidationResults(ctx context.Context, workflowID, cursor string, pageSize int) (model.DvpgValidationResultsList, error) {
	if err := ctx.Err(); err != nil {
		return model.DvpgValidationResultsList{}, err
	}
	if client.validationResults == nil {
		return model.DvpgValidationResultsList{}, errors.New("NSX workflow validation client is required")
	}
	if pageSize <= 0 || pageSize > 1000 {
		return model.DvpgValidationResultsList{}, errors.New("validation result page size must be between 1 and 1000")
	}
	var pageCursor *string
	if cursor != "" {
		pageCursor = ptr.To(cursor)
	}
	return client.validationResults.List(workflowID, nil, pageCursor, nil, nil, nil, nil, ptr.To(int64(pageSize)), nil, nil, nil)
}

func (client *Client) ListValidationResults(ctx context.Context, workflowID string, pageSize int) ([]model.DvpgValidationResult, error) {
	var results []model.DvpgValidationResult
	seen := make(map[string]bool)
	cursor := ""
	for pageNumber := 0; pageNumber < maxValidationPages; pageNumber++ {
		page, err := client.GetValidationResults(ctx, workflowID, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		results = append(results, page.Results...)
		cursor = ptr.Deref(page.Cursor, "")
		if cursor == "" {
			return results, nil
		}
		if seen[cursor] {
			return nil, fmt.Errorf("NSX validation results repeated pagination cursor %q", cursor)
		}
		seen[cursor] = true
	}
	return nil, fmt.Errorf("NSX validation results exceeded %d pages", maxValidationPages)
}

// Delete preserves SDK errors, including NotFound, for the owning action handler.
func (client *Client) Delete(ctx context.Context, workflowID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return client.workflows.Delete(workflowID)
}

// WaitForCompletion polls until the requested workflow state succeeds and every
// reported resource finishes. Validation and planning may report no resources.
func (client *Client) WaitForCompletion(ctx context.Context, workflowID, expectedVPCPath string, expectedState DesiredState) (model.DvpgToSubnetWorkflow, error) {
	if err := ctx.Err(); err != nil {
		return model.DvpgToSubnetWorkflow{}, err
	}
	if !validPolicyPath(expectedVPCPath) {
		return model.DvpgToSubnetWorkflow{}, fmt.Errorf("expected target VPC path %q is invalid", expectedVPCPath)
	}
	switch expectedState {
	case DesiredStateValidation, DesiredStatePlan, DesiredStateConversion, DesiredStateRollbackConversion:
	default:
		return model.DvpgToSubnetWorkflow{}, fmt.Errorf("unsupported expected NSX workflow state %q", expectedState)
	}
	interval := client.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		workflowStatus, err := client.GetStatus(ctx, workflowID)
		if err != nil {
			return workflowStatus, err
		}
		if err := ValidateTargetScope(workflowStatus, expectedVPCPath); err != nil {
			return workflowStatus, err
		}
		complete, err := resourcesComplete(workflowStatus)
		if err != nil {
			return workflowStatus, err
		}
		if (expectedState == DesiredStateValidation || expectedState == DesiredStatePlan) &&
			(workflowStatus.WorkflowState == nil || workflowStatus.WorkflowState.MigrationStatus == nil || len(workflowStatus.WorkflowState.MigrationStatus.NsxObject) == 0) {
			complete = true
		}
		switch strings.ToUpper(workflowExecutionStatus(workflowStatus)) {
		case "FAILED", "ERROR":
			return workflowStatus, fmt.Errorf("NSX workflow %q failed", workflowID)
		case "SUCCESS":
			if ptr.Deref(workflowStatus.DesiredState, "") == expectedState && complete {
				return workflowStatus, nil
			}
		case "PENDING", "IN_PROGRESS", "RUNNING", "":
		default:
			return workflowStatus, fmt.Errorf("NSX workflow %q has unknown status %q", workflowID, workflowExecutionStatus(workflowStatus))
		}
		select {
		case <-ctx.Done():
			return workflowStatus, ctx.Err()
		case <-ticker.C:
		}
	}
}

// ValidateTargetScope ensures the workflow and its NSX-reported paths belong to the requested VPC.
func ValidateTargetScope(workflowStatus model.DvpgToSubnetWorkflow, expectedVPCPath string) error {
	if !validPolicyPath(expectedVPCPath) {
		return fmt.Errorf("expected target VPC path %q is invalid", expectedVPCPath)
	}
	if ptr.Deref(workflowStatus.VpcPath, "") != expectedVPCPath {
		return fmt.Errorf("NSX workflow %q targets VPC %q, expected %q", ptr.Deref(workflowStatus.Id, ""), ptr.Deref(workflowStatus.VpcPath, ""), expectedVPCPath)
	}
	if workflowStatus.WorkflowState == nil || workflowStatus.WorkflowState.MigrationStatus == nil {
		return nil
	}
	for _, resource := range workflowStatus.WorkflowState.MigrationStatus.NsxObject {
		if ptr.Deref(resource.ResourceType, "") == "" || !validPolicyPath(ptr.Deref(resource.PolicyPath, "")) {
			return fmt.Errorf("NSX workflow %q reports an invalid resource path or type", ptr.Deref(workflowStatus.Id, ""))
		}
		switch ptr.Deref(resource.ResourceType, "") {
		case "VpcSubnet", "PortConfig":
			if !pathIsWithin(ptr.Deref(resource.PolicyPath, ""), expectedVPCPath) {
				return fmt.Errorf("NSX workflow %q resource %q is outside target VPC %q", ptr.Deref(workflowStatus.Id, ""), ptr.Deref(resource.PolicyPath, ""), expectedVPCPath)
			}
		case "DistributedVlanConnection":
			if !pathIsWithin(ptr.Deref(resource.PolicyPath, ""), "/infra/distributed-vlan-connections") {
				return fmt.Errorf("NSX workflow %q DVC path %q is outside the infrastructure scope", ptr.Deref(workflowStatus.Id, ""), ptr.Deref(resource.PolicyPath, ""))
			}
		case "IpBlock", "IpAddressBlock":
			if !pathIsWithin(ptr.Deref(resource.PolicyPath, ""), "/infra/ip-blocks") {
				return fmt.Errorf("NSX workflow %q IP block path %q is outside the infrastructure scope", ptr.Deref(workflowStatus.Id, ""), ptr.Deref(resource.PolicyPath, ""))
			}
		default:
			return fmt.Errorf("NSX workflow %q reports unsupported resource type %q", ptr.Deref(workflowStatus.Id, ""), ptr.Deref(resource.ResourceType, ""))
		}
	}
	return nil
}

func resourcesComplete(workflowStatus model.DvpgToSubnetWorkflow) (bool, error) {
	if workflowStatus.WorkflowState == nil || workflowStatus.WorkflowState.MigrationStatus == nil || len(workflowStatus.WorkflowState.MigrationStatus.NsxObject) == 0 {
		return false, nil
	}
	allResourcesCompleted := true
	for _, resource := range workflowStatus.WorkflowState.MigrationStatus.NsxObject {
		switch strings.ToUpper(ptr.Deref(resource.State, "")) {
		case "COMPLETED":
		case "PENDING", "IN_PROGRESS", "RUNNING":
			allResourcesCompleted = false
		case "FAILED", "ERROR":
			return false, fmt.Errorf("NSX workflow %q resource %q failed with state %q", ptr.Deref(workflowStatus.Id, ""), ptr.Deref(resource.PolicyPath, ""), ptr.Deref(resource.State, ""))
		default:
			return false, fmt.Errorf("NSX workflow %q resource %q has unknown state %q", ptr.Deref(workflowStatus.Id, ""), ptr.Deref(resource.PolicyPath, ""), ptr.Deref(resource.State, ""))
		}
	}
	return allResourcesCompleted, nil
}

func validPolicyPath(policyPath string) bool {
	if policyPath == "" || !strings.HasPrefix(policyPath, "/") || strings.HasPrefix(policyPath, "//") ||
		strings.ContainsAny(policyPath, "?#\\\x00") || path.Clean(policyPath) != policyPath {
		return false
	}
	decodedPath, err := url.PathUnescape(policyPath)
	return err == nil && path.Clean(decodedPath) == decodedPath && !strings.Contains(decodedPath, "\\")
}

func pathIsWithin(resourcePath, parentPath string) bool {
	return strings.HasPrefix(resourcePath, strings.TrimRight(parentPath, "/")+"/")
}

func workflowExecutionStatus(workflow model.DvpgToSubnetWorkflow) string {
	if workflow.WorkflowState == nil {
		return ""
	}
	return ptr.Deref(workflow.WorkflowState.Status, "")
}
