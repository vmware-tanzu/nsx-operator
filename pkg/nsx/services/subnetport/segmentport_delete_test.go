/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	infra_segments "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/segments"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

type fakeDeleteSegmentPortsClient struct {
	infra_segments.PortsClient
	segmentID string
	portID    string
	deleteErr error
	calls     int
}

func (c *fakeDeleteSegmentPortsClient) Delete(segmentID, portID string) error {
	c.segmentID, c.portID = segmentID, portID
	c.calls++
	return c.deleteErr
}

func segmentPortForDeletion() *model.SegmentPort {
	return &model.SegmentPort{
		Id:         common.String("port-1"),
		ParentPath: common.String("/infra/segments/seg-1"),
		Path:       common.String("/infra/segments/seg-1/ports/port-1"),
		Tags: []model.Tag{
			{Scope: common.String(common.TagScopeSubnetPortCRUID), Tag: common.String("cr-uid")},
			{Scope: common.String(common.TagScopePodUID), Tag: common.String("pod-uid")},
		},
	}
}

func TestDeleteSegmentPortCacheCleanup(t *testing.T) {
	notFound := nsxutil.NewNSXApiError(&model.ApiError{}, apierrors.ErrorType_NOT_FOUND)
	tests := []struct {
		name string
		err  error
		keep bool
	}{
		{name: "success"},
		{name: "SDK NotFound", err: apierrors.NotFound{}},
		{name: "SDK NotFound pointer", err: &apierrors.NotFound{}},
		{name: "wrapped SDK NotFound", err: fmt.Errorf("delete: %w", apierrors.NotFound{})},
		{name: "wrapped SDK NotFound pointer", err: fmt.Errorf("delete: %w", &apierrors.NotFound{})},
		{name: "translated NotFound", err: notFound},
		{name: "wrapped translated NotFound", err: fmt.Errorf("delete: %w", notFound)},
		{name: "HTTP NotFound", err: nsxutil.HttpNotFoundError},
		{name: "wrapped HTTP NotFound", err: fmt.Errorf("delete: %w", nsxutil.HttpNotFoundError)},
		{name: "API failure", err: apierrors.InternalServerError{}, keep: true},
		{name: "operation not found", err: apierrors.OperationNotFound{}, keep: true},
		{name: "translated failure", err: nsxutil.NewNSXApiError(&model.ApiError{}, apierrors.ErrorType_SERVICE_UNAVAILABLE), keep: true},
		{name: "transport failure", err: errors.New("connection refused"), keep: true},
		{name: "untyped 404", err: errors.New("404 NotFound"), keep: true},
	}
	for _, byID := range []bool{false, true} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("byID=%t/%s", byID, tt.name), func(t *testing.T) {
				segmentClient := &fakeDeleteSegmentPortsClient{deleteErr: tt.err}
				service := &SubnetPortService{
					Service:          common.Service{NSXClient: &nsx.Client{SegmentPortsClient: segmentClient}},
					SegmentPortStore: setupSegmentPortStore(),
				}
				port := segmentPortForDeletion()
				require.NoError(t, service.SegmentPortStore.Apply(port))

				var err error
				if byID {
					err = service.DeleteSegmentPortById("", "port-1")
				} else {
					err = service.DeleteSegmentPort(port)
				}
				if tt.keep {
					assert.Error(t, err)
					assert.Same(t, port, service.SegmentPortStore.GetByKey("port-1"))
				} else {
					assert.NoError(t, err)
					assert.Nil(t, service.SegmentPortStore.GetByKey("port-1"))
				}
				assert.Equal(t, 1, segmentClient.calls)
				assert.Equal(t, "seg-1", segmentClient.segmentID)
				assert.Equal(t, "port-1", segmentClient.portID)
				for index, value := range map[string]string{
					common.TagScopeSubnetPortCRUID: "cr-uid",
					common.TagScopePodUID:          "pod-uid",
					common.IndexKeySegmentPath:     "/infra/segments/seg-1",
				} {
					objects, err := service.SegmentPortStore.ByIndex(index, value)
					require.NoError(t, err)
					if tt.keep {
						assert.Len(t, objects, 1)
					} else {
						assert.Empty(t, objects)
					}
				}
			})
		}
	}
}

func TestDeleteSegmentPortPathFallback(t *testing.T) {
	for _, method := range []string{"object", "ID", "GC", "CR UID", "Pod UID"} {
		for _, parent := range []struct {
			name  string
			value *string
		}{{"nil", nil}, {"empty", common.String("")}} {
			t.Run(method+"/"+parent.name, func(t *testing.T) {
				segmentClient := &fakeDeleteSegmentPortsClient{}
				service := &SubnetPortService{
					Service:          common.Service{NSXClient: &nsx.Client{SegmentPortsClient: segmentClient}},
					SegmentPortStore: setupSegmentPortStore(),
				}
				// Simulate an incomplete cached GET response without startup normalization.
				service.SegmentPortStore.Indexer = cache.NewIndexer(keyFunc, cache.Indexers{
					common.TagScopeSubnetPortCRUID: segmentPortIndexByCRUID,
					common.TagScopePodUID:          segmentPortIndexByPodUID,
				})
				port := segmentPortForDeletion()
				port.ParentPath = parent.value
				require.NoError(t, service.SegmentPortStore.Add(port))

				var err error
				switch method {
				case "object":
					err = service.DeleteSegmentPort(port)
				case "ID":
					err = service.DeleteSegmentPortById("", "port-1")
				case "GC":
					err = service.DeletePortById("port-1")
				case "CR UID":
					err = service.DeletePortByUID("cr-uid")
				case "Pod UID":
					err = service.DeletePortByUID("pod-uid")
				}
				require.NoError(t, err)
				assert.Equal(t, 1, segmentClient.calls)
				assert.Equal(t, "seg-1", segmentClient.segmentID)
				assert.Equal(t, "port-1", segmentClient.portID)
				assert.Empty(t, service.SegmentPortStore.List())
			})
		}
	}
}

func TestDeleteSegmentPortInvalidPath(t *testing.T) {
	for _, tt := range []struct {
		name string
		path *string
	}{
		{name: "missing path"},
		{name: "empty path", path: common.String("")},
		{name: "parent only", path: common.String("/infra/segments/seg-1")},
		{name: "different port", path: common.String("/infra/segments/seg-1/ports/other-port")},
		{name: "extra component", path: common.String("/infra/segments/seg-1/ports/port-1/extra")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			segmentClient := &fakeDeleteSegmentPortsClient{}
			service := &SubnetPortService{
				Service:          common.Service{NSXClient: &nsx.Client{SegmentPortsClient: segmentClient}},
				SegmentPortStore: setupSegmentPortStore(),
			}
			port := segmentPortForDeletion()
			port.ParentPath, port.Path = common.String(""), tt.path
			require.NoError(t, service.SegmentPortStore.Add(port))
			assert.Error(t, service.DeleteSegmentPortById("", "port-1"))
			assert.Zero(t, segmentClient.calls)
			assert.Same(t, port, service.SegmentPortStore.GetByKey("port-1"))
		})
	}
}

func TestDeleteSegmentPortWithoutParentOrCache(t *testing.T) {
	segmentClient := &fakeDeleteSegmentPortsClient{}
	service := &SubnetPortService{
		Service:          common.Service{NSXClient: &nsx.Client{SegmentPortsClient: segmentClient}},
		SegmentPortStore: setupSegmentPortStore(),
	}
	port := segmentPortForDeletion()
	port.ParentPath, port.Path = common.String(""), nil
	require.NoError(t, service.SegmentPortStore.Add(port))
	require.NoError(t, service.DeleteSegmentPortById("seg-1", "port-1"))
	assert.Equal(t, "seg-1", segmentClient.segmentID)
	assert.Empty(t, service.SegmentPortStore.List())

	// A missing cached ID is a no-op; object-based deletion can still delete NSX.
	require.NoError(t, service.DeleteSegmentPortById("seg-1", "port-1"))
	assert.Equal(t, 1, segmentClient.calls)
	require.NoError(t, service.DeleteSegmentPort(segmentPortForDeletion()))
	assert.Equal(t, 2, segmentClient.calls)
}

func TestDeleteSegmentPortSegmentIDMismatch(t *testing.T) {
	segmentClient := &fakeDeleteSegmentPortsClient{}
	service := &SubnetPortService{
		Service:          common.Service{NSXClient: &nsx.Client{SegmentPortsClient: segmentClient}},
		SegmentPortStore: setupSegmentPortStore(),
	}
	port := segmentPortForDeletion()
	require.NoError(t, service.SegmentPortStore.Apply(port))
	assert.Error(t, service.DeleteSegmentPortById("another-segment", "port-1"))
	assert.Zero(t, segmentClient.calls)
	assert.Same(t, port, service.SegmentPortStore.GetByKey("port-1"))
}
