/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	infra_segments "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/segments"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestGetSegmentLogicalSwitchID(t *testing.T) {
	const segmentPath = "/infra/segments/segment-1"
	switchEntity := model.GenericPolicyRealizedResource{
		EntityType:                    common.String("RealizedLogicalSwitch"),
		State:                         common.String(model.GenericPolicyRealizedResource_STATE_REALIZED),
		RealizationSpecificIdentifier: common.String("segment-switch-uuid"),
	}
	for _, tt := range []struct {
		name    string
		modify  func(*model.GenericPolicyRealizedResource)
		missing bool
		badPath bool
		apiErr  error
		wantID  string
	}{
		{name: "realized switch", wantID: "segment-switch-uuid"},
		{name: "missing switch", missing: true},
		{name: "unrealized switch", modify: func(e *model.GenericPolicyRealizedResource) { e.State = common.String("UNREALIZED") }},
		{name: "switch realization error", modify: func(e *model.GenericPolicyRealizedResource) { e.State = common.String("ERROR") }},
		{name: "missing state", modify: func(e *model.GenericPolicyRealizedResource) { e.State = nil }},
		{name: "missing identifier", modify: func(e *model.GenericPolicyRealizedResource) { e.RealizationSpecificIdentifier = nil }},
		{name: "empty identifier", modify: func(e *model.GenericPolicyRealizedResource) { e.RealizationSpecificIdentifier = common.String("") }},
		{name: "API error", apiErr: errors.New("NSX unavailable")},
		{name: "invalid path", badPath: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entity := switchEntity
			if tt.modify != nil {
				tt.modify(&entity)
			}
			called := false
			entitiesClient := &fakeRealizedEntitiesClient{listFn: func(path string, sitePath *string) (model.GenericPolicyRealizedResourceListResult, error) {
				called = true
				assert.Equal(t, segmentPath, path)
				assert.Nil(t, sitePath)
				// Ignore other realized resources and entities with missing fields.
				entities := []model.GenericPolicyRealizedResource{{}, {
					EntityType: common.String("RealizedLogicalPort"), State: common.String("REALIZED"),
					RealizationSpecificIdentifier: common.String("port-uuid"),
				}}
				if !tt.missing {
					entities = append(entities, entity)
				}
				return model.GenericPolicyRealizedResourceListResult{Results: entities}, tt.apiErr
			}}
			service := &SubnetPortService{Service: common.Service{NSXClient: &nsx.Client{RealizedEntitiesClient: entitiesClient}}}
			path := segmentPath
			if tt.badPath {
				path = "/tracking-subnet"
			}
			got, err := service.GetSegmentLogicalSwitchID(path)
			assert.Equal(t, tt.wantID, got)
			assert.Equal(t, !tt.badPath, called)
			if tt.wantID == "" {
				require.Error(t, err)
				if tt.apiErr != nil {
					assert.ErrorIs(t, err, tt.apiErr)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSegmentPortPathValidation(t *testing.T) {
	for _, tt := range []struct {
		path      string
		segmentID string
	}{
		{path: "/infra/segments/seg-1", segmentID: "seg-1"},
		{path: "infra/segments/seg-1/", segmentID: "seg-1"},
		{path: ""},
		{path: "/segments/seg-1"},
		{path: "/wrong/segments/seg-1"},
		{path: "/infra/wrong/seg-1"},
		{path: "/infra/segments/"},
		{path: "/infra/segments//seg-1"},
		{path: "/infra/segments/seg-1/ports/port-1"},
		{path: "/infra/segments/."},
		{path: "/infra/segments/.."},
	} {
		t.Run(tt.path, func(t *testing.T) {
			segmentID, err := parseSegmentPath(tt.path)
			if tt.segmentID != "" {
				require.NoError(t, err)
				assert.Equal(t, tt.segmentID, segmentID)
				return
			}
			require.Error(t, err)

			segmentClient := &fakeDeleteSegmentPortsClient{}
			service := &SubnetPortService{
				Service:          common.Service{NSXClient: &nsx.Client{SegmentPortsClient: segmentClient}},
				SegmentPortStore: setupSegmentPortStore(),
			}
			subnetPort := &v1alpha1.SubnetPort{}
			_, err = service.CreateOrUpdateSegmentPort(subnetPort, tt.path, "", nil, false, false)
			assert.Error(t, err)
			_, err = service.CheckSegmentPortState(subnetPort, tt.path)
			assert.Error(t, err)
			port := segmentPortForDeletion()
			port.ParentPath = common.String(tt.path)
			port.Path = common.String(tt.path + "/ports/" + *port.Id)
			assert.Error(t, service.DeleteSegmentPort(port))
			assert.Zero(t, segmentClient.calls)
			assert.Empty(t, service.SegmentPortStore.List())
		})
	}
}

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

	id, name, err := service.BuildSegmentPortIdAndName(meta, types.UID("ns-uid-456"))
	assert.NoError(t, err)
	assert.NotEmpty(t, id)
	assert.NotEmpty(t, name)
}

func TestSegmentPortStoreMissingParentPath(t *testing.T) {
	for _, parentPath := range []*string{nil, common.String("")} {
		store := setupSegmentPortStore()
		port := &model.SegmentPort{
			Id:         common.String("port-1"),
			Path:       common.String("/infra/segments/seg-1/ports/port-1"),
			ParentPath: parentPath,
		}
		require.NoError(t, store.Apply(port))
		assert.Equal(t, "/infra/segments/seg-1", *port.ParentPath)
		assert.Len(t, store.GetByIndex(common.IndexKeySegmentPath, "/infra/segments/seg-1"), 1)
	}

	for _, path := range []*string{nil, common.String(""), common.String("/infra/segments/seg-1"), common.String("/infra/segments/seg-1/ports/")} {
		store := setupSegmentPortStore()
		port := &model.SegmentPort{Id: common.String("port-1"), Path: path}
		assert.Error(t, store.Apply(port))
		assert.Empty(t, store.List())
	}

	// Deletion records need no parent information.
	store := setupSegmentPortStore()
	require.NoError(t, store.Apply(&model.SegmentPort{
		Id:              common.String("port-1"),
		MarkedForDelete: common.Bool(true),
	}))
}

func TestGetSegmentPathForSubnetPortFromStore(t *testing.T) {
	const segmentPath = "/infra/segments/segment-1"
	for _, tt := range []struct {
		name       string
		scope      string
		parentPath *string
		want       string
	}{
		{name: "CR UID", scope: common.TagScopeSubnetPortCRUID, parentPath: common.String(segmentPath), want: segmentPath},
		{name: "not found"},
		{name: "nil parent", scope: common.TagScopeSubnetPortCRUID},
		{name: "empty parent", scope: common.TagScopeSubnetPortCRUID, parentPath: common.String("")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := setupSegmentPortStore()
			// Allow incomplete cached records to exercise the missing-parent check.
			store.Indexer = cache.NewIndexer(keyFunc, cache.Indexers{
				common.TagScopeSubnetPortCRUID: segmentPortIndexByCRUID,
			})
			require.NoError(t, store.Add(&model.SegmentPort{
				Id: common.String("port-1"), ParentPath: tt.parentPath,
				Tags: []model.Tag{{Scope: common.String(tt.scope), Tag: common.String("uid-1")}},
			}))
			service := &SubnetPortService{SegmentPortStore: store}
			path := service.GetSegmentPathForSubnetPortFromStore("uid-1")
			assert.Equal(t, tt.want, path)
		})
	}
}

func TestBuildSegmentPortIdAndNameExisting(t *testing.T) {
	service := &SubnetPortService{SegmentPortStore: setupSegmentPortStore()}
	meta := &metav1.ObjectMeta{Name: "port", UID: "cr-uid"}
	port := &model.SegmentPort{
		Id: common.String("existing-port"), DisplayName: common.String("existing-name"),
		Path: common.String("/infra/segments/seg-1/ports/existing-port"),
		Tags: []model.Tag{{Scope: common.String(common.TagScopeSubnetPortCRUID), Tag: common.String(string(meta.UID))}},
	}
	require.NoError(t, service.SegmentPortStore.Apply(port))
	id, name, err := service.BuildSegmentPortIdAndName(meta, "ns-uid")
	require.NoError(t, err)
	assert.Equal(t, "existing-port", id)
	assert.Equal(t, "existing-name", name)
}

func TestBuildSegmentPortIdAndNameLookupError(t *testing.T) {
	service := &SubnetPortService{SegmentPortStore: setupSegmentPortStore()}
	service.SegmentPortStore.Indexer = cache.NewIndexer(keyFunc, cache.Indexers{})
	id, name, err := service.BuildSegmentPortIdAndName(&metav1.ObjectMeta{UID: "cr-uid"}, "ns-uid")
	assert.Error(t, err)
	assert.Empty(t, id)
	assert.Empty(t, name)
}

type fakeSegmentPortsClient struct {
	infra_segments.PortsClient
	port       model.SegmentPort
	patchCount int
}

func (c *fakeSegmentPortsClient) Get(segmentID, portID string) (model.SegmentPort, error) {
	return c.port, nil
}

func (c *fakeSegmentPortsClient) Patch(segmentID, portID string, port model.SegmentPort) error {
	c.patchCount++
	c.port = port
	return nil
}

func TestCreateOrUpdateSegmentPortExisting(t *testing.T) {
	attachmentID := "00000000-0000-0000-0000-000000000002"
	tests := []struct {
		name               string
		restoreMode        bool
		restoreVifEnabled  bool
		attachmentID       string
		existingAttachment string
		patchCount         int
	}{
		{name: "normal reconcile preserves attachment", attachmentID: attachmentID, existingAttachment: "existing-attachment"},
		{name: "restore updates attachment", restoreMode: true, restoreVifEnabled: true, attachmentID: attachmentID, existingAttachment: "existing-attachment", patchCount: 1},
		{name: "restore unchanged attachment", restoreMode: true, restoreVifEnabled: true, attachmentID: attachmentID, existingAttachment: attachmentID},
		{name: "restore feature disabled", restoreMode: true, attachmentID: attachmentID, existingAttachment: "existing-attachment"},
		{name: "restore empty attachment", restoreMode: true, restoreVifEnabled: true, existingAttachment: "existing-attachment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{
				Name: "port", Namespace: "ns", UID: "00000000-0000-0000-0000-000000000001",
			}}
			sp.Status.Attachment.ID = tt.attachmentID
			segmentClient := &fakeSegmentPortsClient{}
			service := &SubnetPortService{
				Service: common.Service{
					Client:    fake.NewClientBuilder().WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", UID: "ns-uid"}}).Build(),
					NSXClient: &nsx.Client{SegmentPortsClient: segmentClient},
					NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster"}},
				},
				SegmentPortStore: setupSegmentPortStore(),
			}
			segmentPath := "/infra/segments/seg-1"
			port, err := service.buildSegmentPort(sp, segmentPath, "", nil, true, false)
			require.NoError(t, err)
			port.Attachment.Id = common.String(tt.existingAttachment)
			require.NoError(t, service.SegmentPortStore.Apply(port))
			segmentClient.port = *port
			segmentClient.port.ParentPath = nil
			patches := gomonkey.ApplyMethod(reflect.TypeOf(service), "CheckSegmentPortState", func(_ *SubnetPortService, _ *v1alpha1.SubnetPort, _ string) (*model.SegmentPortState, error) {
				return &model.SegmentPortState{}, nil
			})
			defer patches.Reset()
			patches.ApplyFunc(nsx.RestoreVifFeatureEnabled, func(_ *nsx.Client, _ *config.NSXOperatorConfig) bool {
				return tt.restoreVifEnabled
			})

			_, err = service.CreateOrUpdateSegmentPort(sp, segmentPath, "", nil, true, tt.restoreMode)
			require.NoError(t, err)
			assert.Equal(t, tt.patchCount, segmentClient.patchCount)
			assert.Len(t, service.SegmentPortStore.List(), 1)
			cached := service.SegmentPortStore.GetByKey(*port.Id)
			require.NotNil(t, cached)
			expectedAttachment := tt.existingAttachment
			if tt.patchCount > 0 {
				expectedAttachment = tt.attachmentID
			}
			assert.Equal(t, expectedAttachment, *cached.Attachment.Id)
			assert.Equal(t, segmentPath, *cached.ParentPath)
		})
	}
}

func TestListSubnetPortIDsFromCRsSegmentPort(t *testing.T) {
	sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{
		Name: "port", Namespace: "ns", UID: "cr-uid",
	}}
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	service := &SubnetPortService{
		Service:          common.Service{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(sp).Build()},
		SubnetPortStore:  setupStore(),
		SegmentPortStore: setupSegmentPortStore(),
	}
	require.NoError(t, service.SegmentPortStore.Apply(&model.SegmentPort{
		Id:   common.String("existing-port"),
		Path: common.String("/infra/segments/seg-1/ports/existing-port"),
		Tags: []model.Tag{{Scope: common.String(common.TagScopeSubnetPortCRUID), Tag: common.String(string(sp.UID))}},
	}))
	ids, err := service.ListSubnetPortIDsFromCRs(context.Background())
	require.NoError(t, err)
	assert.True(t, ids.Has("existing-port"))
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
