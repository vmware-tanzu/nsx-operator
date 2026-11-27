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
	WorkflowID       string
	VPCPath          string
	DVCPaths         []string
	SubnetPaths      []string
	WorkflowComplete bool
	Ready            bool
}

// WorkflowExpectation pins the source DVPG and exact target resources for one
// conversion. PortSettings are verified separately through live inventory.
type WorkflowExpectation struct {
	VCenterUUID, PortGroupID     string
	VPCPath, DVCPath, SubnetPath string
}

type WorkflowPortSettingVerifier interface {
	VerifyPortSetting(context.Context, WorkflowExpectation) (bool, error)
}

// PortSettingInventory verifies conversion bindings from current NSX state.
type PortSettingInventory interface {
	VerifyPortSetting(context.Context, string, string, string, string, string) (bool, error)
}

// InventoryPortSettingVerifier adapts inspection's live target inventory to
// the conversion tracker without accepting workflow output as inventory proof.
type InventoryPortSettingVerifier struct {
	Inventory PortSettingInventory
}

func (verifier InventoryPortSettingVerifier) VerifyPortSetting(ctx context.Context, expected WorkflowExpectation) (bool, error) {
	if isNil(verifier.Inventory) {
		return false, fmt.Errorf("PortSetting inventory is required")
	}
	return verifier.Inventory.VerifyPortSetting(ctx, expected.VCenterUUID, expected.PortGroupID, expected.VPCPath, expected.DVCPath, expected.SubnetPath)
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
	outputsComplete := workflowComplete
	hasDVC, hasSubnet := false, false
	for _, resource := range resources {
		resourcePath := ptr.Deref(resource.PolicyPath, "")
		resourceState := strings.ToUpper(ptr.Deref(resource.State, ""))
		switch ptr.Deref(resource.ResourceType, "") {
		case "VpcSubnet":
			hasSubnet = true
			tracked.SubnetPaths = append(tracked.SubnetPaths, resourcePath)
		case "DistributedVlanConnection":
			hasDVC = true
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
			outputsComplete = false
		case "FAILED", "ERROR":
			return tracked, fmt.Errorf("NSX workflow %q resource %s failed", workflowID, resourcePath)
		default:
			return tracked, fmt.Errorf("NSX workflow %q resource %s has unknown state %q", workflowID, resourcePath, resourceState)
		}
	}
	tracked.WorkflowComplete = outputsComplete
	if workflowComplete && outputsComplete && (!hasDVC || !hasSubnet || len(tracked.DVCPaths) != 1 || len(tracked.SubnetPaths) != 1) {
		return tracked, fmt.Errorf("NSX workflow %q succeeded without exactly one DVC and VLAN subnet", workflowID)
	}
	if workflowComplete && (!hasDVC || !hasSubnet || len(tracked.DVCPaths) != 1 || len(tracked.SubnetPaths) != 1) {
		complete = false
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

// TrackConversionWorkflow verifies the expected target paths and matching live
// PortSetting for one source DVPG. Generic workflow outputs alone do not prove
// that this conversion is ready.
func TrackConversionWorkflow(ctx context.Context, reader WorkflowStatusReader, realizer Realizer, verifier WorkflowPortSettingVerifier, workflowID string, expected WorkflowExpectation) (WorkflowResources, error) {
	tracked := WorkflowResources{WorkflowID: workflowID, VPCPath: expected.VPCPath}
	if expected.VCenterUUID == "" || expected.PortGroupID == "" || expected.VPCPath == "" || expected.DVCPath == "" || expected.SubnetPath == "" || isNil(verifier) {
		return tracked, fmt.Errorf("source DVPG identity, target VPC/DVC/subnet paths and PortSetting verifier are required")
	}
	tracked, err := TrackWorkflow(ctx, reader, realizer, workflowID, expected.VPCPath)
	if err != nil {
		return tracked, err
	}
	if !tracked.WorkflowComplete {
		return tracked, nil
	}
	if len(tracked.DVCPaths) != 1 || len(tracked.SubnetPaths) != 1 ||
		tracked.DVCPaths[0] != expected.DVCPath || tracked.SubnetPaths[0] != expected.SubnetPath {
		return tracked, fmt.Errorf("NSX workflow %q returned target paths that conflict with the DVPG conversion plan", workflowID)
	}
	for _, targetPath := range []string{expected.DVCPath, expected.SubnetPath} {
		ready, err := realizer.Ready(ctx, targetPath)
		if err != nil {
			return tracked, fmt.Errorf("realize expected workflow resource %s: %w", targetPath, err)
		}
		if !ready {
			tracked.Ready = false
			return tracked, nil
		}
	}
	portSettingReady, err := verifier.VerifyPortSetting(ctx, expected)
	if err != nil {
		return tracked, fmt.Errorf("verify PortSetting for DVPG %s: %w", expected.PortGroupID, err)
	}
	tracked.Ready = portSettingReady
	return tracked, nil
}
