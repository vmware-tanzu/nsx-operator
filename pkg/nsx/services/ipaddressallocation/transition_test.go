/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
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

	// 2. Found by NS and Name tags fallback (when UID is not indexed or empty)
	foundByName, err := service.GetIPAddressAllocationForTransition("", "ns-staging", "cr-1")
	assert.NoError(t, err)
	assert.Equal(t, "alloc-1", *foundByName.Id)

	// 3. Not found
	notFound, err := service.GetIPAddressAllocationForTransition("nonexistent", "ns-other", "cr-other")
	assert.Error(t, err)
	assert.Nil(t, notFound)
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

	// 4. Patch error
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
