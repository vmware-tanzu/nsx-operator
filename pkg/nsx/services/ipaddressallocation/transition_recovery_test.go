/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	migration "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	vpcapi "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestTransitionDurableAdoptionRecoveryControl(t *testing.T) {
	f := transitionSetup(t, "pending", false, false)
	allocation := f.backend["source-alloc"]
	_, err := f.service.AdoptIPAddressAllocationTags(&allocation, "tenant", "tenant-ns-uid", "target", "target-uid")
	require.NoError(t, err)
	f.reconcile(t)
	f.reconcile(t)
	target := &vpcapi.IPAddressAllocation{}
	require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Namespace: "tenant", Name: "target"}, target))
	require.Equal(t, "192.0.2.10", target.Status.AllocationIPs)
	require.Equal(t, "target-uid", nsxutil.FindTag(f.backend["source-alloc"].Tags, common.TagScopeIPAddressAllocationCRUID))
	source := &vpcapi.IPAddressAllocation{}
	require.True(t, apierrors.IsNotFound(f.kube.Get(context.Background(), types.NamespacedName{Namespace: "staging", Name: "source"}, source)))
	require.NoError(t, f.service.DeleteIPAddressAllocationByNamespacedName("staging", "source"))
	require.Len(t, f.backend, 1)
	require.Equal(t, 1, f.patches)
	require.Zero(t, f.deletes)
	t.Log("CONTROL: interrupted status update recovers from durable target ownership, retaining the same VIP without another PATCH")
}

func TestTransitionLookupIdentityControls(t *testing.T) {
	f := transitionSetup(t, "pending", false, false)
	for _, uid := range []types.UID{"source-uid", ""} {
		allocation, err := f.service.GetIPAddressAllocationForTransition(uid, "staging", "source")
		require.NoError(t, err)
		require.Equal(t, "source-alloc", *allocation.Id)
		require.Equal(t, "source-uid", nsxutil.FindTag(allocation.Tags, common.TagScopeIPAddressAllocationCRUID))
	}
	t.Log("CONTROL: exact UID lookup and the documented empty-UID namespace/name lookup both resolve the intended allocation")
}

func TestTransitionCleanupAfterExplicitRetryControl(t *testing.T) {
	f := transitionSetup(t, "pending", false, true)
	f.reconcile(t)
	source := &vpcapi.IPAddressAllocation{}
	require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Namespace: "staging", Name: "source"}, source))
	f.kube.rejectSourceDelete = false
	f.reconcile(t)
	require.True(t, apierrors.IsNotFound(f.kube.Get(context.Background(), types.NamespacedName{Namespace: "staging", Name: "source"}, source)))
	request := &migration.NetworkResourceTransition{}
	require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Name: "move"}, request))
	require.Equal(t, migration.TransitionPhaseSucceeded, request.Status.Phase)
	require.Equal(t, "target-uid", nsxutil.FindTag(f.backend["source-alloc"].Tags, common.TagScopeIPAddressAllocationCRUID))
	require.Equal(t, 1, f.patches)
	t.Log("CONTROL: after DELETE access recovers, an explicitly triggered reconcile removes the source; the false first success is the defect, not an irreversible cleanup failure")
}
