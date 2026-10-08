/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSegmentPortStore(t *testing.T) {
	store := setupSegmentPortStore()
	assert.NotNil(t, store)

	portID := "segment-port-1"
	segmentPath := "/infra/segments/seg-1"
	cruid := "uid-12345"

	sp := &model.SegmentPort{
		Id:         common.String(portID),
		ParentPath: common.String(segmentPath),
		Tags: []model.Tag{
			{
				Scope: common.String(common.TagScopeSubnetPortCRUID),
				Tag:   common.String(cruid),
			},
		},
	}

	err := store.Apply(sp)
	assert.NoError(t, err)

	byKey := store.GetByKey(portID)
	assert.NotNil(t, byKey)
	assert.Equal(t, portID, *byKey.Id)

	byUID, err := store.GetSegmentPortByUID(types.UID(cruid))
	assert.NoError(t, err)
	assert.NotNil(t, byUID)
	assert.Equal(t, portID, *byUID.Id)

	byIndex := store.GetByIndex(common.IndexKeySegmentPath, segmentPath)
	assert.Len(t, byIndex, 1)
	assert.Equal(t, portID, *byIndex[0].Id)

	store.DeleteMultipleObjects([]*model.SegmentPort{sp})
	assert.Nil(t, store.GetByKey(portID))
}

func TestGetSegmentTrackingPath(t *testing.T) {
	service := &SubnetPortService{}

	// nil subnet
	path, isTracking := service.GetSegmentTrackingPath(nil)
	assert.False(t, isTracking)
	assert.Empty(t, path)

	// Subnet without tracking tag
	subnetNormal := &model.VpcSubnet{
		Tags: []model.Tag{
			{Scope: common.String("other"), Tag: common.String("val")},
		},
	}
	path, isTracking = service.GetSegmentTrackingPath(subnetNormal)
	assert.False(t, isTracking)
	assert.Empty(t, path)

	// Subnet with tracking tag
	segmentPathVal := "/infra/segments/opaque-seg"
	subnetTracking := &model.VpcSubnet{
		Tags: []model.Tag{
			{Scope: common.String(common.TagScopeWCPSegmentTrackingSubnet), Tag: common.String(segmentPathVal)},
		},
	}
	path, isTracking = service.GetSegmentTrackingPath(subnetTracking)
	assert.True(t, isTracking)
	assert.Equal(t, segmentPathVal, path)
}

func TestDeletePortByUID(t *testing.T) {
	service := &SubnetPortService{
		SegmentPortStore: setupSegmentPortStore(),
		SubnetPortStore:  setupStore(),
	}

	uid := types.UID("test-port-uid")

	// Empty stores - should return nil without panic
	err := service.DeletePortByUID(uid)
	assert.NoError(t, err)

	// With SegmentPort in store
	sp := &model.SegmentPort{
		Id:         common.String("sp-1"),
		ParentPath: common.String("/infra/segments/seg1"),
		Tags: []model.Tag{
			{Scope: common.String(common.TagScopeSubnetPortCRUID), Tag: common.String(string(uid))},
		},
	}
	_ = service.SegmentPortStore.Apply(sp)

	// Mock failure or success depending on client - here client is nil so calling DeleteSegmentPortById would panic on NSXClient if not guarded
	// Let's verify SegmentPortStore lookup works safely
	found, err := service.SegmentPortStore.GetSegmentPortByUID(uid)
	assert.NoError(t, err)
	assert.NotNil(t, found)
}

func TestSegmentPortComparable(t *testing.T) {
	sp := &model.SegmentPort{
		Id:          common.String("port-1"),
		DisplayName: common.String("Port 1"),
	}

	comp := SegmentPortToComparable(sp)
	assert.Equal(t, "port-1", comp.Key())

	converted := ComparableToSegmentPort(comp)
	assert.Equal(t, "port-1", *converted.Id)
}

func TestBuildSegmentPortIdAndName(t *testing.T) {
	service := &SubnetPortService{
		SegmentPortStore: setupSegmentPortStore(),
	}

	meta := &metav1.ObjectMeta{
		Name:      "test-sp",
		Namespace: "test-ns",
		UID:       types.UID("sp-uid-123"),
	}

	id, name := service.BuildSegmentPortIdAndName(meta, types.UID("ns-uid-456"))
	assert.NotEmpty(t, id)
	assert.NotEmpty(t, name)
}

func TestCheckSegmentPortState_NotFound(t *testing.T) {
	service := &SubnetPortService{
		SegmentPortStore: setupSegmentPortStore(),
	}

	spCR := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID("non-existing-uid"),
		},
	}

	_, err := service.CheckSegmentPortState(spCR, "/infra/segments/seg1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get segment port from store")
}
