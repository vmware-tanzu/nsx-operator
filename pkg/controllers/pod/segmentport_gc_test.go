/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package pod

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestPodGarbageCollectionIgnoresSegmentPorts(t *testing.T) {
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "live", Namespace: "ns", UID: "live-uid"}}
	api := fake.NewClientBuilder().WithObjects(pod).Build()
	indexers := cache.Indexers{}
	for _, scope := range []string{servicecommon.TagScopeSubnetPortCRUID, servicecommon.TagScopePodUID} {
		indexers[scope] = func(obj interface{}) ([]string, error) {
			var tags []model.Tag
			switch port := obj.(type) {
			case *model.VpcSubnetPort:
				tags = port.Tags
			case *model.SegmentPort:
				tags = port.Tags
			}
			value := nsxutil.FindTag(tags, scope)
			if value == "" {
				return nil, nil
			}
			return []string{value}, nil
		}
	}
	vpcStore := &subnetport.SubnetPortStore{ResourceStore: servicecommon.ResourceStore{
		Indexer: cache.NewIndexer(func(obj interface{}) (string, error) { return *obj.(*model.VpcSubnetPort).Id, nil }, indexers),
	}}
	for _, name := range []string{"live", "orphan"} {
		require.NoError(t, vpcStore.Add(&model.VpcSubnetPort{
			Id:   servicecommon.String(name),
			Tags: []model.Tag{{Scope: servicecommon.String(servicecommon.TagScopePodUID), Tag: servicecommon.String(name + "-uid")}},
		}))
	}
	segmentStore := &subnetport.SegmentPortStore{ResourceStore: servicecommon.ResourceStore{
		Indexer: cache.NewIndexer(func(obj interface{}) (string, error) { return *obj.(*model.SegmentPort).Id, nil }, indexers),
	}}
	// Even a cached segment carrying a Pod tag is outside the Pod controller's ownership.
	segment := &model.SegmentPort{Id: servicecommon.String("segment-port"), Tags: []model.Tag{
		{Scope: servicecommon.String(servicecommon.TagScopePodUID), Tag: servicecommon.String("deleted-pod")},
		{Scope: servicecommon.String(servicecommon.TagScopeSubnetPortCRUID), Tag: servicecommon.String("vm-cr")},
	}}
	require.NoError(t, segmentStore.Add(segment))
	service := &subnetport.SubnetPortService{
		Service:         servicecommon.Service{Client: api, NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}}},
		SubnetPortStore: vpcStore, SegmentPortStore: segmentStore,
	}
	r := &PodReconciler{Client: api, SubnetPortService: service}
	r.StatusUpdater = common.NewStatusUpdater(api, service.NSXConfig, nil, MetricResTypePod, "SubnetPort", "Pod")
	var deleted []string
	patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPortById, func(_ *subnetport.SubnetPortService, id string) error {
		deleted = append(deleted, id)
		return nil
	})
	defer patches.Reset()
	require.NoError(t, r.CollectGarbage(context.Background()))
	assert.Equal(t, []string{"orphan"}, deleted)
	assert.Same(t, segment, segmentStore.GetByKey("segment-port"))
}
