/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package migration

import (
	"context"
	"errors"

	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/migration/infrastructure"
)

// GetProject resolves only a pre-existing project through the standard NSX client.
// No create, display-name search or fallback to another project is performed.
func (s *Service) GetProject(ctx context.Context, id string) (infrastructure.Project, error) {
	if err := ctx.Err(); err != nil {
		return infrastructure.Project{}, err
	}
	client, err := s.Client()
	if err != nil {
		return infrastructure.Project{}, &infrastructure.Issue{Reason: "NSXUnavailable", Message: "Cannot initialize the NSX client; verify the mounted NSX configuration and connectivity", Unknown: true, Cause: err}
	}
	if client.NsxConfig == nil || client.NsxConfig.CoeConfig == nil || !client.NsxConfig.EnableVPCNetwork {
		return infrastructure.Project{}, &infrastructure.Issue{Reason: "VPCProviderDisabled", Message: "NCP configuration must enable VPC networking before migration"}
	}
	if client.ProjectClient == nil {
		return infrastructure.Project{}, &infrastructure.Issue{Reason: "NSXUnavailable", Message: "The NSX Project client is unavailable", Unknown: true}
	}
	project, err := client.ProjectClient.Get("default", id, nil)
	if err != nil {
		var missing vapierrors.NotFound
		var missingPointer *vapierrors.NotFound
		if errors.As(err, &missing) || errors.As(err, &missingPointer) {
			return infrastructure.Project{}, &infrastructure.Issue{Reason: "NSXProjectNotFound", Message: "The selected NSX Project does not exist; pre-provision it before migration", Cause: err}
		}
		return infrastructure.Project{}, &infrastructure.Issue{Reason: "NSXProjectReadFailed", Message: "Cannot read the selected NSX Project; verify NSX access and retry", Unknown: true, Cause: err}
	}
	if err = ctx.Err(); err != nil {
		return infrastructure.Project{}, err
	}
	return resolvedProject(project)
}

func resolvedProject(project model.Project) (infrastructure.Project, error) {
	if project.MarkedForDelete != nil && *project.MarkedForDelete {
		return infrastructure.Project{}, &infrastructure.Issue{Reason: "NSXProjectDeleting", Message: "The selected NSX Project is being deleted"}
	}
	if project.Id == nil || project.Path == nil {
		return infrastructure.Project{}, &infrastructure.Issue{Reason: "InvalidNSXProject", Message: "NSX returned an incomplete project identity"}
	}
	return infrastructure.Project{ID: *project.Id, Path: *project.Path}, nil
}
