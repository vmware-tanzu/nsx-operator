/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestPlanRetainsMultipleSubnets(t *testing.T) {
	resource := func(name string) *data.StructValue {
		return data.NewStructValue("VpcSubnet", map[string]data.DataValue{
			"resource_type": data.NewStringValue("VpcSubnet"),
			"id":            data.NewStringValue(name),
			"path":          data.NewStringValue("/orgs/o/projects/p/vpcs/v/subnets/" + name),
		})
	}
	root := &model.OrgRoot{Children: []*data.StructValue{resource("a"), resource("b")}}
	resources, err := plannedNamespaceResources(root)
	require.NoError(t, err)
	require.Len(t, resources, 2)
	current := api.NamespaceNetworkPreview{TargetResources: []api.NamespaceTargetResourcePreview{{Kind: "Subnet", Decision: "Pending"}}}
	for _, item := range resources {
		upsertTargetResource(&current, item)
	}
	require.Len(t, current.TargetResources, 2)
	require.Equal(t, "a", current.TargetResources[0].Name)
	require.Equal(t, "b", current.TargetResources[1].Name)
}

func TestCompareNamespaceTargetResourcesRetainsMultipleSubnets(t *testing.T) {
	subnet := func(name string) api.NamespaceTargetResourcePreview {
		return api.NamespaceTargetResourcePreview{Kind: "Subnet", Decision: "Create", Name: name}
	}
	before := []api.NamespaceNetworkPreview{{VDNName: "vdn", TargetResources: []api.NamespaceTargetResourcePreview{subnet("a"), subnet("b")}}}
	reordered := []api.NamespaceNetworkPreview{{VDNName: "vdn", TargetResources: []api.NamespaceTargetResourcePreview{subnet("b"), subnet("a")}}}
	require.Empty(t, CompareNamespaceTargetResources(before, reordered))
	after := []api.NamespaceNetworkPreview{{VDNName: "vdn", TargetResources: []api.NamespaceTargetResourcePreview{subnet("changed-a"), subnet("b")}}}
	changes := CompareNamespaceTargetResources(before, after)
	require.Len(t, changes, 1)
	require.Equal(t, "a", changes[0].Previous.Name)
	require.Equal(t, "changed-a", changes[0].Current.Name)
	after[0].TargetResources = []api.NamespaceTargetResourcePreview{subnet("b")}
	changes = CompareNamespaceTargetResources(before, after)
	require.Len(t, changes, 1)
	require.Equal(t, "a", changes[0].Previous.Name)
	require.Nil(t, changes[0].Current)
}

func TestEvaluateExecuteReviewAndResume(t *testing.T) {
	batch := &api.NamespaceNetworkTransition{
		ObjectMeta: metav1.ObjectMeta{Generation: 2},
		Spec:       api.NamespaceNetworkTransitionSpec{TransitionSpec: api.TransitionSpec{Action: api.ActionExecute}},
		Status: &api.NamespaceNetworkTransitionStatus{
			TransitionStatus:  api.TransitionStatus{LastAction: api.ActionEvaluate, ActionState: api.ActionStateSucceeded, ActionGeneration: 1, ObservedGeneration: 1},
			EvaluationPreview: &api.NamespaceEvaluationPreview{Generation: 1, ReviewRevision: 1, Networks: []api.NamespaceNetworkPreview{{VDNName: "vdn", TargetResources: []api.NamespaceTargetResourcePreview{{Kind: "VPC", Decision: "Pending"}}}}},
		},
	}
	calls := 0
	engine := Engine{Enabled: true, Handlers: map[Key]Handler{{Namespaces, api.ActionExecute}: handlerFunc(func(_ context.Context, req Request) (Result, error) {
		calls++
		actual := batch.Status.EvaluationPreview.DeepCopy()
		actual.Networks[0].TargetResources[0] = api.NamespaceTargetResourcePreview{Kind: "VPC", Decision: "Create", Name: "vpc"}
		changed, err := PublishNamespaceExecutionPreview(batch, req.Generation, actual)
		if err != nil {
			return Result{}, err
		}
		if changed {
			return Result{Outcome: OutcomeNeedsReview}, nil
		}
		return Result{Outcome: OutcomeInProgress}, nil
	})}}
	req := Request{Stage: Namespaces, Object: batch, Action: api.ActionExecute, Generation: 2, Status: &batch.Status.TransitionStatus}
	result, err := engine.Reconcile(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, OutcomeInProgress, result.Outcome)
	require.Zero(t, calls)
	result, err = engine.Reconcile(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, OutcomeNeedsReview, result.Outcome)
	require.Equal(t, api.PhaseReviewRequired, batch.Status.Phase)
	require.Equal(t, int64(2), batch.Status.EvaluationPreview.ReviewRevision)
	result, err = engine.Reconcile(context.Background(), req)
	require.NoError(t, err)
	require.Empty(t, result.Outcome)
	require.Equal(t, 1, calls)
	require.NoError(t, AcknowledgeNamespacePreviewReview(batch))
	result, err = engine.Reconcile(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, OutcomeInProgress, result.Outcome)
	require.False(t, batch.Status.EvaluationPreview.ReviewRequired)
	require.Equal(t, 2, calls)
}

func TestExecuteRejectsStalePreview(t *testing.T) {
	batch := &api.NamespaceNetworkTransition{Status: &api.NamespaceNetworkTransitionStatus{EvaluationPreview: &api.NamespaceEvaluationPreview{Generation: 1, ReviewRevision: 1}}}
	stage1 := &api.ControlPlaneNetworkTransition{Status: &api.ControlPlaneNetworkTransitionStatus{TargetNSXProjectPath: "/orgs/o/projects/p"}}
	_, err := (NamespaceExecute{}).Reconcile(context.Background(), Request{Object: batch, Generation: 3, Stage1Transition: stage1})
	require.ErrorContains(t, err, "requires a current preview")
	_, err = (NamespaceExecute{}).Reconcile(context.Background(), Request{Object: batch, Generation: 2, Stage1Transition: stage1})
	require.ErrorContains(t, err, "topology is unavailable")
}
