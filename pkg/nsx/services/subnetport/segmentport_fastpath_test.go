/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type countingSegmentPortsClient struct {
	*fakeSegmentPortsClient
	getCount int
}

func (c *countingSegmentPortsClient) Get(segmentID, portID string) (model.SegmentPort, error) {
	c.getCount++
	return c.fakeSegmentPortsClient.Get(segmentID, portID)
}

func TestCreateOrUpdateSegmentPortAlreadyRealized(t *testing.T) {
	const attachmentID = "00000000-0000-0000-0000-000000000002"
	tests := []struct {
		name         string
		ready        corev1.ConditionStatus
		attachmentID string
		contextID    string
		updateTags   bool
		restoreMode  bool
		newPort      bool
		stateCalls   int
		patchCount   int
	}{
		{name: "ready L2 port without IP or gateway", ready: corev1.ConditionTrue, attachmentID: attachmentID},
		{name: "missing Ready condition", attachmentID: attachmentID, stateCalls: 1},
		{name: "not ready", ready: corev1.ConditionFalse, attachmentID: attachmentID, stateCalls: 1},
		{name: "missing status attachment", ready: corev1.ConditionTrue, stateCalls: 1},
		{name: "mismatched status attachment", ready: corev1.ConditionTrue, attachmentID: "different-attachment", stateCalls: 1},
		{name: "changed context", ready: corev1.ConditionTrue, attachmentID: attachmentID, contextID: "host-1", stateCalls: 1, patchCount: 1},
		{name: "changed tags", ready: corev1.ConditionTrue, attachmentID: attachmentID, updateTags: true, stateCalls: 1, patchCount: 1},
		{name: "restore unchanged port", ready: corev1.ConditionTrue, attachmentID: attachmentID, restoreMode: true, stateCalls: 1},
		{name: "new port with stale ready status", ready: corev1.ConditionTrue, attachmentID: attachmentID, newPort: true, stateCalls: 1, patchCount: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{
				Name: "port", Namespace: "ns", UID: "00000000-0000-0000-0000-000000000001",
			}}
			sp.Status.Attachment.ID = tt.attachmentID
			if tt.ready != "" {
				sp.Status.Conditions = []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: tt.ready}}
			}
			segmentClient := &countingSegmentPortsClient{fakeSegmentPortsClient: &fakeSegmentPortsClient{}}
			service := &SubnetPortService{
				Service: common.Service{
					Client:    fake.NewClientBuilder().WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", UID: "ns-uid"}}).Build(),
					NSXClient: &nsx.Client{SegmentPortsClient: segmentClient},
					NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster"}},
				},
				SegmentPortStore: setupSegmentPortStore(),
			}
			segmentPath := "/infra/segments/seg-1"
			if !tt.newPort {
				port, err := service.buildSegmentPort(sp, segmentPath, "", nil, true, false)
				require.NoError(t, err)
				port.Attachment.Id = common.String(attachmentID)
				require.NoError(t, service.SegmentPortStore.Apply(port))
				segmentClient.port = *port
			}
			stateCalls := 0
			patches := gomonkey.ApplyMethod(reflect.TypeOf(service), "CheckSegmentPortState", func(_ *SubnetPortService, _ *v1alpha1.SubnetPort, _ string) (*model.SegmentPortState, error) {
				stateCalls++
				return &model.SegmentPortState{}, nil
			})
			defer patches.Reset()
			patches.ApplyFunc(nsx.RestoreVifFeatureEnabled, func(_ *nsx.Client, _ *config.NSXOperatorConfig) bool { return true })
			var tags *map[string]string
			if tt.updateTags {
				tags = &map[string]string{"app": "updated"}
			}

			state, err := service.CreateOrUpdateSegmentPort(sp, segmentPath, tt.contextID, tags, true, tt.restoreMode)
			require.NoError(t, err)
			assert.Equal(t, tt.patchCount, segmentClient.patchCount)
			assert.Equal(t, tt.stateCalls, stateCalls)
			assert.Equal(t, tt.stateCalls, segmentClient.getCount)
			if tt.stateCalls == 0 {
				assert.Nil(t, state)
			} else {
				assert.NotNil(t, state)
			}
		})
	}
}
