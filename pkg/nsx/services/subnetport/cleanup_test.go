/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestCleanupInfraResources(t *testing.T) {
	deleteErr := errors.New("NSX unavailable")
	for _, tt := range []struct {
		name      string
		ports     int
		deleteErr error
		canceled  bool
		wantErr   error
		calls     int
	}{
		{name: "empty store"},
		{name: "multiple ports", ports: 2, calls: 2},
		{name: "already deleted", ports: 1, deleteErr: apierrors.NotFound{}, calls: 1},
		{name: "deletion failure", ports: 1, deleteErr: deleteErr, wantErr: deleteErr, calls: 1},
		{name: "canceled", ports: 1, canceled: true, wantErr: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			segmentClient := &fakeDeleteSegmentPortsClient{deleteErr: tt.deleteErr}
			service := &SubnetPortService{
				Service:          common.Service{NSXClient: &nsx.Client{SegmentPortsClient: segmentClient}},
				SegmentPortStore: setupSegmentPortStore(),
			}
			for i := 0; i < tt.ports; i++ {
				port := segmentPortForDeletion()
				port.Id = common.String(fmt.Sprintf("port-%d", i))
				port.Path = common.String("/infra/segments/seg-1/ports/" + *port.Id)
				require.NoError(t, service.SegmentPortStore.Apply(port))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.canceled {
				cancel()
			}

			err := service.CleanupInfraResources(ctx)
			assert.Equal(t, tt.calls, segmentClient.calls)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Len(t, service.SegmentPortStore.List(), tt.ports)
				// Retained records can be deleted on the next cleanup attempt.
				segmentClient.deleteErr = nil
				require.NoError(t, service.CleanupInfraResources(context.Background()))
			} else {
				require.NoError(t, err)
			}
			assert.Empty(t, service.SegmentPortStore.List())
			calls := segmentClient.calls
			require.NoError(t, service.CleanupInfraResources(context.Background()))
			assert.Equal(t, calls, segmentClient.calls)
		})
	}
}
