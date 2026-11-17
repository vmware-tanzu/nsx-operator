/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/types"

	vpcapi "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	mockservices "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func transitionAllocation(id, uid string) *model.VpcIpAddressAllocation {
	parent := "/orgs/default/projects/project/vpcs/shared-lb"
	return &model.VpcIpAddressAllocation{
		Id: String(id), Path: String(parent + "/ip-address-allocations/" + id), ParentPath: String(parent),
		AllocationIps: String("192.0.2.10"),
		Tags: []model.Tag{
			{Scope: String(common.TagScopeIPAddressAllocationCRUID), Tag: String(uid)},
			{Scope: String(common.TagScopeNamespace), Tag: String("staging")},
			{Scope: String(common.TagScopeIPAddressAllocationCRName), Tag: String(id)},
			{Scope: String(common.TagScopeIPAllocLB), Tag: String("true")},
		},
	}
}

func transitionReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for transition operation")
		var zero T
		return zero
	}
}

func TestTransitionConcurrentOwners(t *testing.T) {
	for _, sameTarget := range []bool{false, true} {
		name := "one_source_two_targets"
		if sameTarget {
			name = "two_sources_one_target"
		}
		t.Run(name, func(t *testing.T) {
			service, _, api := createIPAddressAllocationService(t)
			first := transitionAllocation("first", "source-1")
			second, secondTarget := first, types.UID("target-2")
			if sameTarget {
				second = transitionAllocation("second", "source-2")
				second.AllocationIps = String("192.0.2.11")
				secondTarget = "target-1"
				require.NoError(t, service.ipAddressAllocationStore.Apply(second))
			}
			require.NoError(t, service.ipAddressAllocationStore.Apply(first))
			entered, release := make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			t.Cleanup(func() { unblock.Do(func() { close(release) }) })
			var persisted model.VpcIpAddressAllocation
			api.EXPECT().Patch("default", "project", "shared-lb", "first", gomock.Any()).DoAndReturn(
				func(_, _, _, _ string, allocation model.VpcIpAddressAllocation) error {
					close(entered)
					<-release
					persisted = allocation
					return nil
				}).Times(1)
			api.EXPECT().Get("default", "project", "shared-lb", "first").DoAndReturn(
				func(_, _, _, _ string) (model.VpcIpAddressAllocation, error) { return persisted, nil }).Times(1)
			firstDone, secondDone := make(chan error, 1), make(chan error, 1)
			go func() {
				_, err := service.AdoptIPAddressAllocationTags(first, "tenant", "tenant-uid", "target", "target-1")
				firstDone <- err
			}()
			transitionReceive(t, entered)
			go func() {
				_, err := service.AdoptIPAddressAllocationTags(second, "tenant", "tenant-uid", "target", secondTarget)
				secondDone <- err
			}()
			// The first PATCH is in flight while the competing transfer starts.
			unblock.Do(func() { close(release) })
			require.NoError(t, transitionReceive(t, firstDone))
			require.Error(t, transitionReceive(t, secondDone))
			owner, err := service.GetIPAddressAllocationForTransition("target-1", "tenant", "target")
			require.NoError(t, err)
			require.Equal(t, "first", *owner.Id)
		})
	}
}

func TestTransitionReadersWaitForConfirmation(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "rejected"}[reject], func(t *testing.T) {
			service, _, api := createIPAddressAllocationService(t)
			source := transitionAllocation("first", "source-uid")
			require.NoError(t, service.ipAddressAllocationStore.Apply(source))
			entered, release := make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			t.Cleanup(func() { unblock.Do(func() { close(release) }) })
			persisted := *source
			api.EXPECT().Patch("default", "project", "shared-lb", "first", gomock.Any()).DoAndReturn(
				func(_, _, _, _ string, allocation model.VpcIpAddressAllocation) error {
					close(entered)
					<-release
					if reject {
						return errors.New("PATCH rejected")
					}
					persisted = allocation
					return nil
				})
			if !reject {
				api.EXPECT().Get("default", "project", "shared-lb", "first").DoAndReturn(
					func(_, _, _, _ string) (model.VpcIpAddressAllocation, error) { return persisted, nil })
			}
			done := make(chan error, 1)
			go func() {
				_, err := service.AdoptIPAddressAllocationTags(source, "tenant", "tenant-uid", "target", "target-uid")
				done <- err
			}()
			transitionReceive(t, entered)
			// No speculative target entry is visible even to a raw cache reader.
			require.Equal(t, "source-uid", nsxutil.FindTag(service.ipAddressAllocationStore.GetByKey("first").(*model.VpcIpAddressAllocation).Tags, common.TagScopeIPAddressAllocationCRUID))
			type lookupResult struct {
				allocation *model.VpcIpAddressAllocation
				err        error
			}
			lookup := make(chan lookupResult, 1)
			started := make(chan struct{})
			go func() {
				close(started)
				allocation, err := service.GetIPAddressAllocationForTransition("target-uid", "tenant", "target")
				lookup <- lookupResult{allocation, err}
			}()
			transitionReceive(t, started)
			select {
			case <-lookup:
				t.Fatal("reader returned before durable ownership was confirmed")
			case <-time.After(50 * time.Millisecond):
			}
			unblock.Do(func() { close(release) })
			err := transitionReceive(t, done)
			result := transitionReceive(t, lookup)
			require.NoError(t, result.err)
			if reject {
				require.Error(t, err)
				require.Nil(t, result.allocation)
			} else {
				require.NoError(t, err)
				require.Equal(t, "192.0.2.10", *result.allocation.AllocationIps)
			}
		})
	}
}

func TestTransitionStaleSourceOperations(t *testing.T) {
	f := transitionSetup(t, "pending", false, true)
	staleSourceAllocation := f.backend["source-alloc"]
	f.reconcile(t)
	// A deletion selected by another controller before the transfer is stale.
	require.NoError(t, f.service.DeleteIPAddressAllocationByNSXResource(&staleSourceAllocation))
	require.Zero(t, f.deletes)
	// The source CR is still present because its DELETE was rejected. A queued
	// ordinary reconcile must not create a fresh source reservation.
	source := &vpcapi.IPAddressAllocation{}
	require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Namespace: "staging", Name: "source"}, source))
	provider := f.service.VPCService.(*mockservices.MockVPCServiceProvider)
	provider.On("GetVPCNetworkConfigByNamespace", "staging").Return(&vpcapi.VPCNetworkConfiguration{
		Spec: vpcapi.VPCNetworkConfigurationSpec{LoadBalancerVPC: "/orgs/default/projects/project/vpcs/shared-lb"},
	}, nil)
	_, err := f.service.CreateOrUpdateIPAddressAllocation(source, false)
	require.ErrorContains(t, err, "refusing to recreate source")
	require.Equal(t, 1, f.patches)
	require.Len(t, f.backend, 1)
}

func TestAllocationConflictIsScopedToVPC(t *testing.T) {
	for _, tc := range []struct {
		name, vpcPath, ip, owner string
		conflict                 bool
	}{
		{"same_vpc_and_ip", "/orgs/default/projects/project/vpcs/shared-lb", "192.0.2.10", "target-uid", true},
		{"different_vpc", "/orgs/default/projects/project/vpcs/other", "192.0.2.10", "target-uid", false},
		{"different_ip", "/orgs/default/projects/project/vpcs/shared-lb", "192.0.2.11", "target-uid", false},
		{"same_owner", "/orgs/default/projects/project/vpcs/shared-lb", "192.0.2.10", "source-uid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &IPAddressAllocationService{ipAddressAllocationStore: buildIPAddressAllocationStore()}
			allocation := transitionAllocation("allocation", tc.owner)
			allocation.ParentPath = String(tc.vpcPath)
			allocation.AllocationIps = String(tc.ip)
			require.NoError(t, service.ipAddressAllocationStore.Apply(allocation))
			unlock := service.lockAllocationUIDs("source-uid")
			defer unlock()
			err := service.validateNoConflictingAllocation("192.0.2.10", "/orgs/default/projects/project/vpcs/shared-lb", "source-uid")
			if tc.conflict {
				require.ErrorContains(t, err, "refusing to recreate source")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTransitionSerializesSourceReconcileAndDelete(t *testing.T) {
	f := transitionSetup(t, "pending", false, false)
	source := &vpcapi.IPAddressAllocation{}
	require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Namespace: "staging", Name: "source"}, source))
	provider := f.service.VPCService.(*mockservices.MockVPCServiceProvider)
	provider.On("GetVPCNetworkConfigByNamespace", "staging").Return(&vpcapi.VPCNetworkConfiguration{
		Spec: vpcapi.VPCNetworkConfigurationSpec{LoadBalancerVPC: "/orgs/default/projects/project/vpcs/shared-lb"},
	}, nil)
	staleAllocation := f.backend["source-alloc"]
	entered, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	f.beforePatch = func() { close(entered); <-release }
	adoptionDone, reconcileDone, deleteDone := make(chan error, 1), make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := f.service.AdoptIPAddressAllocationTags(&staleAllocation, "tenant", "tenant-ns-uid", "target", "target-uid")
		adoptionDone <- err
	}()
	transitionReceive(t, entered)
	go func() {
		_, err := f.service.CreateOrUpdateIPAddressAllocation(source, false)
		reconcileDone <- err
	}()
	go func() { deleteDone <- f.service.DeleteIPAddressAllocationByNSXResource(&staleAllocation) }()
	select {
	case <-reconcileDone:
		t.Fatal("source reconcile bypassed an in-flight adoption")
	case <-deleteDone:
		t.Fatal("source deletion bypassed an in-flight adoption")
	case <-time.After(50 * time.Millisecond):
	}
	unblock.Do(func() { close(release) })
	require.NoError(t, transitionReceive(t, adoptionDone))
	require.NoError(t, transitionReceive(t, deleteDone))
	require.ErrorContains(t, transitionReceive(t, reconcileDone), "refusing to recreate source")
	require.Equal(t, 1, f.patches)
	require.Zero(t, f.deletes)
	require.Len(t, f.backend, 1)
}
