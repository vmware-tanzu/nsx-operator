/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package transition

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestIrreversibleCheckpointIsEngineOwned(t *testing.T) {
	for _, tc := range []struct {
		stage  Stage
		action api.Action
	}{{Namespaces, api.ActionCommit}, {Infrastructure, api.ActionFinalize}} {
		for _, previous := range []string{"", string(tc.action)} {
			for _, replacement := range []string{"", "Commit", "Finalize"} {
				if previous == replacement {
					continue
				}
				for _, outcome := range []Outcome{OutcomeSucceeded, OutcomeInProgress, OutcomeCheckpointIrreversible} {
					t.Run(string(tc.action)+"/"+previous+"/"+replacement+"/"+string(outcome), func(t *testing.T) {
						failure := errors.New("handler failure")
						status := &api.TransitionStatus{LastAction: tc.action, ActionState: api.ActionStateRunning, ActionGeneration: 2, IrreversibleAction: previous}
						engine := Engine{Enabled: true, Handlers: map[Key]Handler{{tc.stage, tc.action}: handlerFunc(func(_ context.Context, req Request) (Result, error) {
							req.Status.IrreversibleAction = replacement
							// Exercise restoration both with and without another handler error.
							if outcome == OutcomeInProgress {
								return Result{Outcome: outcome}, failure
							}
							return Result{Outcome: outcome}, nil
						})}}
						_, err := engine.Reconcile(context.Background(), Request{Stage: tc.stage, Action: tc.action, Generation: 2, Status: status})
						require.ErrorContains(t, err, "must not mutate irreversibleAction")
						if outcome == OutcomeInProgress {
							require.ErrorIs(t, err, failure)
						}
						require.Equal(t, previous, status.IrreversibleAction)
						require.Equal(t, api.ActionStateFailed, status.ActionState)
						if tc.action == api.ActionCommit {
							require.Equal(t, "Committing", status.Phase)
							require.False(t, meta.IsStatusConditionTrue(status.Conditions, "NamespaceMigrationCommitted"))
						}
					})
				}
			}
		}
	}
}

func TestCompletedIrreversibleActionRequiresDurableCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		stage  Stage
		action api.Action
	}{{Namespaces, api.ActionCommit}, {Infrastructure, api.ActionFinalize}} {
		for _, generation := range []int64{2, 3} {
			t.Run(fmt.Sprintf("%s/generation-%d", tc.action, generation), func(t *testing.T) {
				status := &api.TransitionStatus{LastAction: tc.action, ActionState: api.ActionStateSucceeded, ActionGeneration: 2}
				engine := Engine{Enabled: true, Handlers: map[Key]Handler{{tc.stage, tc.action}: handlerFunc(func(context.Context, Request) (Result, error) {
					t.Fatal("must not replay unverified destructive work")
					return Result{}, nil
				})}}
				result, err := engine.Reconcile(context.Background(), Request{Stage: tc.stage, Action: tc.action, Generation: generation, Status: status})
				require.NoError(t, err)
				require.Empty(t, result.Outcome)
				require.Empty(t, status.IrreversibleAction)
				require.Equal(t, "Blocked", status.Phase)
				require.Equal(t, "MissingIrreversibleCheckpoint", meta.FindStatusCondition(status.Conditions, "Ready").Reason)
				require.False(t, meta.IsStatusConditionTrue(status.Conditions, "NamespaceMigrationCommitted"))
			})
		}
	}
}

func TestCompletedCheckpointCannotBypassActionScope(t *testing.T) {
	for _, tc := range []struct {
		stage  Stage
		action api.Action
	}{{Infrastructure, api.ActionCommit}, {Namespaces, api.ActionFinalize}} {
		status := &api.TransitionStatus{LastAction: tc.action, ActionState: api.ActionStateSucceeded, ActionGeneration: 2, IrreversibleAction: string(tc.action)}
		engine := Engine{Enabled: true}
		result, err := engine.Reconcile(context.Background(), Request{Stage: tc.stage, Action: tc.action, Generation: 2, Status: status})
		require.NoError(t, err)
		require.Empty(t, result.Outcome)
		require.Equal(t, "InvalidAction", meta.FindStatusCondition(status.Conditions, "Ready").Reason)
	}
}
