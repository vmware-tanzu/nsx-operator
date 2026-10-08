/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	migration "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	vpcapi "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

type transitionReadFailure struct {
	client.Client
	key       client.ObjectKey
	failOn    int
	reads     int
	recovered bool
}

func (c *transitionReadFailure) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if key == c.key {
		c.reads++
		if !c.recovered && c.reads == c.failOn {
			return apierrors.NewServiceUnavailable("injected Kubernetes read failure")
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestTransitionReadFailuresRetry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     client.ObjectKey
		failOn  int
		patches int
	}{
		{name: "target_lookup", key: client.ObjectKey{Namespace: "tenant", Name: "target"}, failOn: 1},
		{name: "source_lookup", key: client.ObjectKey{Namespace: "staging", Name: "source"}, failOn: 1},
		{name: "namespace_lookup", key: client.ObjectKey{Name: "tenant"}, failOn: 1},
		{name: "source_cleanup_lookup", key: client.ObjectKey{Namespace: "staging", Name: "source"}, failOn: 2, patches: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := transitionSetup(t, "pending", false, false)
			reader := &transitionReadFailure{Client: f.kube, key: tc.key, failOn: tc.failOn}
			f.r.Client = reader
			result := f.reconcile(t)
			require.Positive(t, result.RequeueAfter)
			require.Equal(t, tc.patches, f.patches)
			source := &vpcapi.IPAddressAllocation{}
			require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Namespace: "staging", Name: "source"}, source))
			request := &migration.NetworkResourceTransition{}
			require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Name: "move"}, request))
			require.NotEqual(t, migration.MigrationPhaseSucceeded, request.Status.Phase)
			reader.recovered = true
			f.reconcile(t)
			require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Name: "move"}, request))
			require.Equal(t, migration.MigrationPhaseSucceeded, request.Status.Phase)
			require.Equal(t, 1, f.patches)
		})
	}
}
