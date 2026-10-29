/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package infrastructure checks Stage 1 prerequisites before action dispatch.
package infrastructure

import (
	"context"
	"errors"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

const ConditionReady = "InfrastructurePrerequisitesReady"

// Issue distinguishes a missing prerequisite from an inability to observe it.
// Messages are safe for CR status; transport error payloads are not exposed there.
type Issue struct {
	Reason  string
	Message string
	Unknown bool
	Cause   error
}

func (i *Issue) Error() string { return i.Message }

func (i *Issue) Unwrap() error { return i.Cause }

func issue(reason, message string) error {
	return &Issue{Reason: reason, Message: message}
}

func readFailure(message string, err error) error {
	return &Issue{Reason: "PrerequisiteReadFailed", Message: message, Unknown: true, Cause: err}
}

// Project identifies an existing NSX Project. The service never creates one.
type Project struct {
	ID   string
	Path string
}

type Projects interface {
	GetProject(context.Context, string) (Project, error)
}

type Readiness interface {
	Check(context.Context, bool, string) error
}

// Service validates prerequisites without creating resources or completing Evaluate.
type Service struct {
	Readiness Readiness
	Projects  Projects
}

// ProjectID accepts an ID, not a path, URL or display-name lookup. Omission alone
// selects default. Do not use the normal operator's default_project setting here.
func ProjectID(value string) (string, error) {
	if value == "" {
		return "default", nil
	}
	if strings.TrimSpace(value) != value || value == "." || value == ".." {
		return "", issue("InvalidNSXProject", "spec.nsxProjectId must be an NSX project ID, not a path or URL")
	}
	if strings.ContainsAny(value, "/\\?#%") || len(value) > 256 {
		return "", issue("InvalidNSXProject", "spec.nsxProjectId must be an NSX project ID, not a path or URL")
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return "", issue("InvalidNSXProject", "spec.nsxProjectId contains a control character")
		}
	}
	return value, nil
}

func hasRecordedInfrastructureState(status *api.ControlPlaneNetworkTransitionStatus) bool {
	return status != nil && (status.PrimaryVPCPath != "" ||
		status.LoadBalancerVPCPath != "" || len(status.NSXWorkflows) > 0 ||
		len(status.LBSNATIPAllocations) > 0 || status.IrreversibleAction != "")
}

func (s *Service) resolveProject(ctx context.Context, obj *api.ControlPlaneNetworkTransition) (Project, error) {
	if obj.UID == "" || obj.Generation <= 0 || obj.Name != api.ControlPlaneNetworkTransitionName {
		return Project{}, issue("InvalidTransitionIdentity", "Infrastructure start requires the persisted singleton transition identity")
	}
	if obj.Spec.SourceMode != "VSPHERE_VDS" || obj.Spec.TargetMode != "NSX_VPC" {
		return Project{}, issue("UnsupportedNetworkTransition", "Only VSPHERE_VDS to NSX_VPC infrastructure migration is supported")
	}
	id, err := ProjectID(obj.Spec.NSXProjectID)
	if err != nil {
		return Project{}, err
	}
	path := "/orgs/default/projects/" + id
	if s.Readiness == nil || s.Projects == nil {
		return Project{}, issue("PrerequisiteServiceUnavailable", "Infrastructure prerequisite services are not configured")
	}
	resume := obj.Status != nil && (obj.Status.LastAction == api.ActionExecute || obj.Status.LastAction == api.ActionFinalize)
	if err = s.Readiness.Check(ctx, resume, path); err != nil {
		return Project{}, err
	}
	project, err := s.Projects.GetProject(ctx, id)
	if err != nil {
		return Project{}, err
	}
	if project.ID != id || project.Path != path {
		return Project{}, issue("InvalidNSXProject", "NSX returned an incomplete or different project identity")
	}
	projectChanged := obj.Status != nil && obj.Status.TargetNSXProjectPath != "" && obj.Status.TargetNSXProjectPath != path
	if projectChanged && (hasRecordedInfrastructureState(obj.Status) || obj.Spec.Action != api.ActionEvaluate) {
		return Project{}, issue("ProjectIdentityConflict", "Resolved project differs from recorded infrastructure; resume using its original project")
	}
	if hasRecordedInfrastructureState(obj.Status) && obj.Status.TargetNSXProjectPath == "" {
		return Project{}, issue("ProjectIdentityMissing", "Existing infrastructure resources have no recorded NSX project; verify adoption before resuming")
	}
	return project, nil
}

// Check refreshes prerequisite status without advancing action checkpoints.
func (s *Service) Check(ctx context.Context, obj *api.ControlPlaneNetworkTransition) error {
	project, err := s.resolveProject(ctx, obj)
	report(obj, project, err)
	return err
}

func report(obj *api.ControlPlaneNetworkTransition, project Project, err error) {
	if obj.Status == nil {
		obj.Status = &api.ControlPlaneNetworkTransitionStatus{}
	}
	condition := metav1.Condition{Type: ConditionReady, ObservedGeneration: obj.Generation, Status: metav1.ConditionTrue, Reason: "PrerequisitesReady", Message: "VPC provider and NCP are ready; the pre-existing NSX Project is resolved"}
	if err != nil {
		condition.Status = metav1.ConditionUnknown
		condition.Reason = "PrerequisiteCheckFailed"
		condition.Message = "Infrastructure prerequisites could not be validated"
		var problem *Issue
		if errors.As(err, &problem) {
			condition.Status = metav1.ConditionFalse
			condition.Reason = problem.Reason
			condition.Message = problem.Message
			if problem.Unknown {
				condition.Status = metav1.ConditionUnknown
			}
		}
	} else {
		obj.Status.TargetNSXProjectPath = project.Path
	}
	meta.SetStatusCondition(&obj.Status.Conditions, condition)
}
