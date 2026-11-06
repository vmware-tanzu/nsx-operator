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

func TestBuildSegmentPortContextID(t *testing.T) {
	service := &SubnetPortService{
		Service: common.Service{
			Client:    fake.NewClientBuilder().WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", UID: "ns-uid"}}).Build(),
			NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "cluster"}},
		},
		SegmentPortStore: setupSegmentPortStore(),
	}
	sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{
		Name: "port", Namespace: "ns", UID: "00000000-0000-0000-0000-000000000001",
	}}
	for _, contextID := range []string{"", "transport-node-1"} {
		t.Run(contextID, func(t *testing.T) {
			port, err := service.buildSegmentPort(sp, "/infra/segments/seg-1", contextID, nil, false, false)
			require.NoError(t, err)
			if contextID == "" {
				assert.Nil(t, port.Attachment.ContextId)
			} else {
				assert.Equal(t, common.String(contextID), port.Attachment.ContextId)
			}
		})
	}
}

func TestCreateOrUpdateSegmentPortContextID(t *testing.T) {
	tests := []struct {
		name            string
		newPort         bool
		contextID       string
		existingContext string
		expectedContext string
		patchCount      int
	}{
		{name: "new port", newPort: true, contextID: "host-1", expectedContext: "host-1", patchCount: 1},
		{name: "changed context", contextID: "host-2", existingContext: "host-1", expectedContext: "host-2", patchCount: 1},
		{name: "unchanged context", contextID: "host-1", existingContext: "host-1", expectedContext: "host-1"},
		{name: "cleared context", existingContext: "host-1", patchCount: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{
				Name: "port", Namespace: "ns", UID: "00000000-0000-0000-0000-000000000001",
			}}
			segmentClient := &fakeSegmentPortsClient{}
			service := &SubnetPortService{
				Service: common.Service{
					Client:    fake.NewClientBuilder().WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", UID: "ns-uid"}}).Build(),
					NSXClient: &nsx.Client{SegmentPortsClient: segmentClient},
					NSXConfig: &config.NSXOperatorConfig{
						CoeConfig: &config.CoeConfig{Cluster: "cluster"},
					},
				},
				SegmentPortStore: setupSegmentPortStore(),
			}
			segmentPath := "/infra/segments/seg-1"
			if !tt.newPort {
				port, err := service.buildSegmentPort(sp, segmentPath, "", nil, true, false)
				require.NoError(t, err)
				port.Attachment.ContextId = common.String(tt.existingContext)
				require.NoError(t, service.SegmentPortStore.Apply(port))
				segmentClient.port = *port
			}
			patches := gomonkey.ApplyMethod(reflect.TypeOf(service), "CheckSegmentPortState", func(_ *SubnetPortService, _ *v1alpha1.SubnetPort, _ string) (*model.SegmentPortState, error) {
				return &model.SegmentPortState{}, nil
			})
			defer patches.Reset()

			_, err := service.CreateOrUpdateSegmentPort(sp, segmentPath, tt.contextID, nil, true, false)
			require.NoError(t, err)
			assert.Equal(t, tt.patchCount, segmentClient.patchCount)
			cached, err := service.SegmentPortStore.GetSegmentPortByUID(sp.UID)
			require.NoError(t, err)
			require.NotNil(t, cached)
			if tt.expectedContext == "" {
				assert.Nil(t, cached.Attachment.ContextId)
			} else {
				assert.Equal(t, common.String(tt.expectedContext), cached.Attachment.ContextId)
			}
			assert.Equal(t, cached.Attachment.ContextId, segmentClient.port.Attachment.ContextId)
		})
	}
}
