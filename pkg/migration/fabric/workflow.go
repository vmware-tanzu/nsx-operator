/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package fabric

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/migration/nsxworkflow"
)

// WorkflowStatusReader reads an already checkpointed NSX conversion workflow.
// The workflow ID is persisted by the transition status before conversion is
// submitted, so this interface can recover its outputs after a process restart.
type WorkflowStatusReader interface {
	GetStatus(context.Context, string) (model.DvpgToSubnetWorkflow, error)
}

// WorkflowResources contains the DVCs and Subnets NSX reports as workflow
// outputs. Callers should use these paths when building conversion plans and
// never submit separate creates for the same resources.
type WorkflowResources struct {
	WorkflowID  string
	VPCPath     string
	DVCPaths    []string
	SubnetPaths []string
	Ready       bool
}

// TrackWorkflow reconstructs workflow-created resources from NSX on every
// reconcile. It is non-blocking: incomplete workflow execution returns Ready
// false so the caller can persist the outputs seen so far and requeue.
func TrackWorkflow(ctx context.Context, reader WorkflowStatusReader, realizer Realizer, workflowID, vpcPath string) (WorkflowResources, error) {
	tracked := WorkflowResources{WorkflowID: workflowID, VPCPath: vpcPath}
	if err := ctx.Err(); err != nil {
		return tracked, err
	}
	if isNil(reader) || isNil(realizer) || workflowID == "" || vpcPath == "" {
		return tracked, fmt.Errorf("workflow reader, realizer, workflow ID and target VPC path are required")
	}
	if !validResourceID(workflowID) {
		return tracked, fmt.Errorf("invalid workflow ID %q", workflowID)
	}
	workflow, err := reader.GetStatus(ctx, workflowID)
	if err != nil {
		return tracked, fmt.Errorf("read NSX workflow %q: %w", workflowID, err)
	}
	if err := nsxworkflow.ValidateTargetScope(workflow, vpcPath); err != nil {
		return tracked, err
	}
	if ptr.Deref(workflow.DesiredState, "") != nsxworkflow.DesiredStateConversion {
		return tracked, nil
	}
	if workflow.WorkflowState == nil {
		return tracked, nil
	}
	status := strings.ToUpper(ptr.Deref(workflow.WorkflowState.Status, ""))
	workflowComplete := false
	switch status {
	case "FAILED", "ERROR":
		return tracked, fmt.Errorf("NSX workflow %q failed", workflowID)
	case "SUCCESS":
		workflowComplete = true
	case "", "PENDING", "IN_PROGRESS", "RUNNING":
	default:
		return tracked, fmt.Errorf("NSX workflow %q has unknown status %q", workflowID, status)
	}
	if workflow.WorkflowState.MigrationStatus == nil {
		if workflowComplete {
			return tracked, fmt.Errorf("NSX workflow %q succeeded without reporting output resources", workflowID)
		}
		return tracked, nil
	}
	resources := workflow.WorkflowState.MigrationStatus.NsxObject
	if len(resources) == 0 {
		if workflowComplete {
			return tracked, fmt.Errorf("NSX workflow %q succeeded without reporting output resources", workflowID)
		}
		return tracked, nil
	}
	complete := workflowComplete
	for _, resource := range resources {
		resourcePath := ptr.Deref(resource.PolicyPath, "")
		resourceState := strings.ToUpper(ptr.Deref(resource.State, ""))
		switch ptr.Deref(resource.ResourceType, "") {
		case "VpcSubnet":
			tracked.SubnetPaths = append(tracked.SubnetPaths, resourcePath)
		case "DistributedVlanConnection":
			tracked.DVCPaths = append(tracked.DVCPaths, resourcePath)
		case "PortConfig", "IpBlock", "IpAddressBlock":
			// These workflow outputs are validated for scope and readiness but
			// are not separate DVC/Subnet resources to register here.
		default:
			return tracked, fmt.Errorf("workflow %q returned unsupported resource type %q", workflowID, ptr.Deref(resource.ResourceType, ""))
		}
		switch resourceState {
		case "COMPLETED":
		case "PENDING", "IN_PROGRESS", "RUNNING":
			complete = false
		case "FAILED", "ERROR":
			return tracked, fmt.Errorf("NSX workflow %q resource %s failed", workflowID, resourcePath)
		default:
			return tracked, fmt.Errorf("NSX workflow %q resource %s has unknown state %q", workflowID, resourcePath, resourceState)
		}
	}
	if complete {
		for _, path := range append(append([]string(nil), tracked.DVCPaths...), tracked.SubnetPaths...) {
			ready, err := realizer.Ready(ctx, path)
			if err != nil {
				return tracked, fmt.Errorf("realize workflow resource %s: %w", path, err)
			}
			if !ready {
				complete = false
			}
		}
	}
	sort.Strings(tracked.DVCPaths)
	sort.Strings(tracked.SubnetPaths)
	tracked.Ready = complete
	return tracked, nil
}
