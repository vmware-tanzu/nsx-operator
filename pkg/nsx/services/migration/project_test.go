/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package migration

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	vapierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/migration/infrastructure"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

type projectClient struct {
	orgs.ProjectsClient
	get func(string, string, *bool) (model.Project, error)
}

func (p projectClient) Get(org, id string, short *bool) (model.Project, error) {
	return p.get(org, id, short)
}

func TestGetExistingProject(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		reason  string
		unknown bool
	}{
		{name: "found"}, {name: "not found", err: vapierrors.NotFound{}, reason: "NSXProjectNotFound"},
		{name: "not found pointer", err: &vapierrors.NotFound{}, reason: "NSXProjectNotFound"},
		{name: "unauthorized", err: vapierrors.Unauthorized{}, reason: "NSXProjectReadFailed", unknown: true},
		{name: "transport", err: errors.New("connection failed"), reason: "NSXProjectReadFailed", unknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := &Service{client: &nsx.Client{NsxConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{EnableVPCNetwork: true}}, ProjectClient: projectClient{get: func(org, id string, short *bool) (model.Project, error) {
				calls++
				require.Equal(t, "default", org)
				require.Equal(t, "tenant-project", id)
				require.Nil(t, short)
				return model.Project{Id: ptr.To(id), Path: ptr.To("/orgs/default/projects/" + id), UniqueId: ptr.To("project-uid")}, tc.err
			}}}}
			project, err := s.GetProject(context.Background(), "tenant-project")
			require.Equal(t, 1, calls, "do not retry default on missing/custom-project failures")
			if tc.reason == "" {
				require.NoError(t, err)
				require.Equal(t, "tenant-project", project.ID)
				require.Equal(t, "/orgs/default/projects/tenant-project", project.Path)
			} else {
				var issue *infrastructure.Issue
				require.ErrorAs(t, err, &issue)
				require.Equal(t, tc.reason, issue.Reason)
				require.Equal(t, tc.unknown, issue.Unknown)
			}
		})
	}
}

func TestProjectGatesAndCancellation(t *testing.T) {
	s := &Service{client: &nsx.Client{NsxConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{EnableVPCNetwork: false}}}}
	_, err := s.GetProject(context.Background(), "default")
	var problem *infrastructure.Issue
	require.ErrorAs(t, err, &problem)
	require.Equal(t, "VPCProviderDisabled", problem.Reason)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = NewService().GetProject(ctx, "default")
	require.ErrorIs(t, err, context.Canceled)
	_, err = resolvedProject(model.Project{Id: ptr.To("default"), Path: ptr.To("/orgs/default/projects/default"), UniqueId: ptr.To("uid"), MarkedForDelete: ptr.To(true)})
	require.ErrorAs(t, err, &problem)
	require.Equal(t, "NSXProjectDeleting", problem.Reason)
	_, err = resolvedProject(model.Project{Id: ptr.To("default")})
	require.ErrorAs(t, err, &problem)
	require.Equal(t, "InvalidNSXProject", problem.Reason)
}
