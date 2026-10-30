/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	infra_segments "github.com/vmware/vsphere-automation-sdk-go/services/nsxt/infra/segments"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

type segmentPortCleanupClient struct {
	infra_segments.PortsClient
	deleted []string
	err     error
}

func (c *segmentPortCleanupClient) Delete(segmentID, portID string) error {
	c.deleted = append(c.deleted, segmentID+"/"+portID)
	return c.err
}

func TestDeleteSubnetPortByNameSegmentPort(t *testing.T) {
	deleteErr := errors.New("NSX deletion failed")
	tests := []struct {
		name       string
		scope      string
		stateful   bool
		feature    bool
		noStore    bool
		deleteErr  error
		wantDelete bool
	}{
		{name: "VM namespace", scope: servicecommon.TagScopeVMNamespace, wantDelete: true},
		{name: "Pod namespace", scope: servicecommon.TagScopeNamespace, wantDelete: true},
		{name: "StatefulSet retained", scope: servicecommon.TagScopeNamespace, stateful: true, feature: true},
		{name: "StatefulSet feature disabled", scope: servicecommon.TagScopeNamespace, stateful: true, wantDelete: true},
		{name: "deletion failure", scope: servicecommon.TagScopeVMNamespace, deleteErr: deleteErr, wantDelete: true},
		{name: "segment store absent", noStore: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segmentClient := &segmentPortCleanupClient{err: tt.deleteErr}
			service := &subnetport.SubnetPortService{
				Service: servicecommon.Service{NSXClient: &nsx.Client{SegmentPortsClient: segmentClient}},
				SubnetPortStore: &subnetport.SubnetPortStore{ResourceStore: servicecommon.ResourceStore{
					Indexer: cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
						servicecommon.TagScopeVMNamespace: func(interface{}) ([]string, error) { return nil, nil },
						servicecommon.TagScopeNamespace:   func(interface{}) ([]string, error) { return nil, nil },
					}),
				}},
			}
			patches := gomonkey.ApplyFunc(nsx.StatefulSetPodSubnetPortFeatureEnabled,
				func(*nsx.Client, *config.NSXOperatorConfig) bool { return tt.feature })
			defer patches.Reset()

			if !tt.noStore {
				service.SegmentPortStore = &subnetport.SegmentPortStore{ResourceStore: servicecommon.ResourceStore{
					Indexer: cache.NewIndexer(func(obj interface{}) (string, error) {
						if id, ok := obj.(string); ok {
							return id, nil
						}
						return *obj.(*model.SegmentPort).Id, nil
					}, cache.Indexers{}),
				}}
				for _, port := range []struct{ id, namespace, name string }{
					{"matching", "ns", "port"},
					{"other-namespace", "other", "port"},
					{"other-name", "ns", "other"},
				} {
					tags := []model.Tag{
						{Scope: servicecommon.String(tt.scope), Tag: servicecommon.String(port.namespace)},
						{Scope: servicecommon.String(servicecommon.TagScopeSubnetPortCRName), Tag: servicecommon.String(port.name)},
					}
					if tt.stateful {
						tags = append(tags, model.Tag{Scope: servicecommon.String(servicecommon.TagScopeStatefulSetUID), Tag: servicecommon.String("sts-uid")})
					}
					require.NoError(t, service.SegmentPortStore.Apply(&model.SegmentPort{
						Id:         servicecommon.String(port.id),
						ParentPath: servicecommon.String("/infra/segments/segment-1"),
						Tags:       tags,
					}))
				}
			}

			r := &SubnetPortReconciler{SubnetPortService: service}
			err := r.deleteSubnetPortByName(context.Background(), "ns", "port")
			if tt.deleteErr != nil {
				assert.ErrorIs(t, err, tt.deleteErr)
				require.NotNil(t, service.SegmentPortStore.GetByKey("matching"))
			} else {
				require.NoError(t, err)
				// Repeated reconciliation after deletion is a no-op.
				require.NoError(t, r.deleteSubnetPortByName(context.Background(), "ns", "port"))
			}
			if tt.wantDelete {
				assert.Equal(t, []string{"segment-1/matching"}, segmentClient.deleted)
			} else {
				assert.Empty(t, segmentClient.deleted)
			}
			if !tt.noStore {
				assert.NotNil(t, service.SegmentPortStore.GetByKey("other-namespace"))
				assert.NotNil(t, service.SegmentPortStore.GetByKey("other-name"))
				if tt.wantDelete && tt.deleteErr == nil {
					assert.Nil(t, service.SegmentPortStore.GetByKey("matching"))
				} else {
					assert.NotNil(t, service.SegmentPortStore.GetByKey("matching"))
				}
			}
		})
	}
}
