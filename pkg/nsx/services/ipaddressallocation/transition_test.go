/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestGetIPAddressAllocationForTransition(t *testing.T) {
	service, mockCtrl, _ := createIPAddressAllocationService(t)
	defer mockCtrl.Finish()

	uid := types.UID("uid-123")
	alloc1 := &model.VpcIpAddressAllocation{
		Id:   String("alloc-1"),
		Path: String("/orgs/default/projects/proj-1/vpcs/vpc-1/ip-address-allocations/alloc-1"),
		Tags: []model.Tag{
			{
				Scope: String(common.TagScopeIPAddressAllocationCRUID),
				Tag:   String(string(uid)),
			},
			{
				Scope: String(common.TagScopeNamespace),
				Tag:   String("ns-staging"),
			},
			{
				Scope: String(common.TagScopeIPAddressAllocationCRName),
				Tag:   String("cr-1"),
			},
		},
	}
	err := service.ipAddressAllocationStore.Add(alloc1)
	assert.NoError(t, err)

	// 1. Found by UID
	found, err := service.GetIPAddressAllocationForTransition(uid, "ns-staging", "cr-1")
	assert.NoError(t, err)
	assert.Equal(t, "alloc-1", *found.Id)

	// 2. Name fallback is available only when no UID is supplied.
	foundByName, err := service.GetIPAddressAllocationForTransition("", "ns-staging", "cr-1")
	assert.NoError(t, err)
	assert.Equal(t, "alloc-1", *foundByName.Id)

	// 3. Not found
	notFound, err := service.GetIPAddressAllocationForTransition("nonexistent", "ns-other", "cr-other")
	assert.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestTransitionAmbiguousWriteReadback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		patchError bool
		committed  bool
		getError   bool
	}{
		{name: "committed_write_with_lost_response", patchError: true, committed: true},
		{name: "rejected_write", patchError: true},
		{name: "successful_patch", committed: true},
		{name: "successful_patch_with_stale_readback"},
		{name: "successful_patch_with_failed_readback", committed: true, getError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _, api := createIPAddressAllocationService(t)
			source := transitionAllocation("allocation", "source-uid")
			require.NoError(t, service.ipAddressAllocationStore.Apply(source))
			persisted := *source
			patchErr := errors.New("PATCH response failed")
			api.EXPECT().Patch("default", "project", "shared-lb", "allocation", gomock.Any()).DoAndReturn(
				func(_, _, _, _ string, allocation model.VpcIpAddressAllocation) error {
					if tc.committed {
						persisted = allocation
					}
					if tc.patchError {
						return patchErr
					}
					return nil
				})
			if !tc.patchError {
				api.EXPECT().Get("default", "project", "shared-lb", "allocation").DoAndReturn(
					func(_, _, _, _ string) (model.VpcIpAddressAllocation, error) {
						if tc.getError {
							return model.VpcIpAddressAllocation{}, errors.New("GET failed")
						}
						return persisted, nil
					})
			}
			_, err := service.AdoptIPAddressAllocationTags(source, "tenant", "tenant-uid", "target", "target-uid")
			owner := nsxutil.FindTag(service.ipAddressAllocationStore.GetByKey("allocation").(*model.VpcIpAddressAllocation).Tags, common.TagScopeIPAddressAllocationCRUID)
			if !tc.patchError && tc.committed && !tc.getError {
				require.NoError(t, err)
				require.Equal(t, "target-uid", owner)
			} else {
				require.Error(t, err)
				require.Equal(t, "source-uid", owner)
			}
			if tc.patchError {
				require.ErrorIs(t, err, patchErr)
			}
		})
	}
}

func TestTransitionReadbackFailureRetriesPatch(t *testing.T) {
	service, _, api := createIPAddressAllocationService(t)
	source := transitionAllocation("allocation", "source-uid")
	require.NoError(t, service.ipAddressAllocationStore.Apply(source))
	var persisted model.VpcIpAddressAllocation
	patch := func(_, _, _, _ string, allocation model.VpcIpAddressAllocation) error {
		persisted = allocation
		return nil
	}
	readbackErr := errors.New("GET failed after committed PATCH")
	gomock.InOrder(
		api.EXPECT().Patch("default", "project", "shared-lb", "allocation", gomock.Any()).DoAndReturn(patch),
		api.EXPECT().Get("default", "project", "shared-lb", "allocation").Return(model.VpcIpAddressAllocation{}, readbackErr),
		api.EXPECT().Patch("default", "project", "shared-lb", "allocation", gomock.Any()).DoAndReturn(patch),
		api.EXPECT().Get("default", "project", "shared-lb", "allocation").DoAndReturn(
			func(_, _, _, _ string) (model.VpcIpAddressAllocation, error) { return persisted, nil }),
	)

	_, err := service.AdoptIPAddressAllocationTags(source, "tenant", "tenant-uid", "target", "target-uid")
	require.ErrorIs(t, err, readbackErr)
	target, err := service.GetIPAddressAllocationForTransition("target-uid", "tenant", "target")
	require.NoError(t, err)
	require.Nil(t, target, "failed readback must not publish target ownership")

	adopted, err := service.AdoptIPAddressAllocationTags(source, "tenant", "tenant-uid", "target", "target-uid")
	require.NoError(t, err)
	require.Equal(t, source.Path, adopted.Path)
	require.Equal(t, source.AllocationIps, adopted.AllocationIps)
	target, err = service.GetIPAddressAllocationForTransition("target-uid", "tenant", "target")
	require.NoError(t, err)
	require.Equal(t, adopted, target)
}

func TestAdoptIPAddressAllocationTags(t *testing.T) {
	service, mockCtrl, mockClient := createIPAddressAllocationService(t)
	defer mockCtrl.Finish()

	path := "/orgs/default/projects/proj-1/vpcs/vpc-1/ip-address-allocations/alloc-1"
	existingAlloc := &model.VpcIpAddressAllocation{
		Id:            String("alloc-1"),
		Path:          String(path),
		AllocationIps: String("192.168.10.50"),
		Tags: []model.Tag{
			{Scope: String(common.TagScopeCluster), Tag: String("k8scl-one:test")},
			{Scope: String(common.TagScopeNamespace), Tag: String("vmware-system-net-staging")},
			{Scope: String(common.TagScopeIPAddressAllocationCRName), Tag: String("src-cr")},
			{Scope: String(common.TagScopeIPAddressAllocationCRUID), Tag: String("src-uid")},
			{Scope: String(common.TagScopeIPAllocLB), Tag: String("true")},
		},
	}

	// 1. Nil or invalid existingAlloc
	_, err := service.AdoptIPAddressAllocationTags(nil, "target-ns", "target-ns-uid", "target-cr", "target-uid")
	assert.Error(t, err)

	invalidAlloc := &model.VpcIpAddressAllocation{Id: String("alloc-1")}
	_, err = service.AdoptIPAddressAllocationTags(invalidAlloc, "target-ns", "target-ns-uid", "target-cr", "target-uid")
	assert.Error(t, err)

	// 2. Invalid path
	badPathAlloc := &model.VpcIpAddressAllocation{
		Id:   String("alloc-1"),
		Path: String("invalid-path"),
	}
	_, err = service.AdoptIPAddressAllocationTags(badPathAlloc, "target-ns", "target-ns-uid", "target-cr", "target-uid")
	assert.Error(t, err)

	assert.NoError(t, service.ipAddressAllocationStore.Apply(existingAlloc))
	// 3. Success case
	targetAllocResponse := *existingAlloc
	targetAllocResponse.Tags = []model.Tag{
		{Scope: String(common.TagScopeCluster), Tag: String("k8scl-one:test")},
		{Scope: String(common.TagScopeNamespace), Tag: String("target-ns")},
		{Scope: String(common.TagScopeNamespaceUID), Tag: String("target-ns-uid")},
		{Scope: String(common.TagScopeIPAddressAllocationCRName), Tag: String("target-cr")},
		{Scope: String(common.TagScopeIPAddressAllocationCRUID), Tag: String("target-uid")},
		{Scope: String(common.TagScopeIPAllocLB), Tag: String("true")},
	}

	mockClient.EXPECT().Patch(
		"default",
		"proj-1",
		"vpc-1",
		"alloc-1",
		gomock.Any(),
	).Return(nil)

	mockClient.EXPECT().Get(
		"default",
		"proj-1",
		"vpc-1",
		"alloc-1",
	).Return(targetAllocResponse, nil)

	adopted, err := service.AdoptIPAddressAllocationTags(
		existingAlloc,
		"target-ns",
		"target-ns-uid",
		"target-cr",
		types.UID("target-uid"),
	)
	assert.NoError(t, err)
	assert.NotNil(t, adopted)
	assert.Equal(t, "target-ns", nsxutil.FindTag(adopted.Tags, common.TagScopeNamespace))
	assert.Equal(t, "target-cr", nsxutil.FindTag(adopted.Tags, common.TagScopeIPAddressAllocationCRName))
	assert.Equal(t, "target-uid", nsxutil.FindTag(adopted.Tags, common.TagScopeIPAddressAllocationCRUID))
	assert.Equal(t, "true", nsxutil.FindTag(adopted.Tags, common.TagScopeIPAllocLB))

	// Verify store updated
	storeAlloc, errStore := service.GetIPAddressAllocationForTransition("target-uid", "target-ns", "target-cr")
	assert.NoError(t, errStore)
	assert.NotNil(t, storeAlloc)

	// 4. Patch error returns without GET or publishing target ownership.
	assert.NoError(t, service.ipAddressAllocationStore.Apply(existingAlloc))
	mockClient.EXPECT().Patch(
		"default",
		"proj-1",
		"vpc-1",
		"alloc-1",
		gomock.Any(),
	).Return(errors.New("patch failed"))

	_, err = service.AdoptIPAddressAllocationTags(
		existingAlloc,
		"target-ns",
		"target-ns-uid",
		"target-cr",
		types.UID("target-uid"),
	)
	assert.Error(t, err)

	// 5. Get error
	mockClient.EXPECT().Patch(
		"default",
		"proj-1",
		"vpc-1",
		"alloc-1",
		gomock.Any(),
	).Return(nil)

	mockClient.EXPECT().Get(
		"default",
		"proj-1",
		"vpc-1",
		"alloc-1",
	).Return(model.VpcIpAddressAllocation{}, errors.New("get failed"))

	_, err = service.AdoptIPAddressAllocationTags(
		existingAlloc,
		"target-ns",
		"target-ns-uid",
		"target-cr",
		types.UID("target-uid"),
	)
	assert.Error(t, err)
}
