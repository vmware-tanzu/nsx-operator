/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package pod

import (
	"context"
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

func TestPodReconciler_CollectGarbage_SegmentPorts(t *testing.T) {
	for _, tt := range []struct {
		name         string
		phase        v1.PodPhase
		statefulSet  bool
		retainSTS    bool
		vpcLookupErr bool
		segmentErr   bool
		wantDeleted  sets.Set[string]
	}{
		{name: "live Pod", wantDeleted: sets.New("orphan")},
		{name: "terminal Pod", phase: v1.PodSucceeded, wantDeleted: sets.New("live", "orphan")},
		{name: "VPC lookup error", vpcLookupErr: true, wantDeleted: sets.New("orphan")},
		{name: "StatefulSet replacement", statefulSet: true, retainSTS: true, wantDeleted: sets.New[string]()},
		{name: "StatefulSet feature disabled", statefulSet: true, wantDeleted: sets.New("orphan")},
		{name: "segment lookup error", segmentErr: true, wantDeleted: sets.New[string]()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "live", Namespace: "ns", UID: "live-uid"},
				Status:     v1.PodStatus{Phase: tt.phase},
			}
			k8sClient := fake.NewClientBuilder().WithObjects(pod).Build()
			indexers := cache.Indexers{}
			for _, scope := range []string{servicecommon.TagScopeSubnetPortCRUID, servicecommon.TagScopePodUID} {
				indexers[scope] = func(obj interface{}) ([]string, error) {
					value := nsxutil.FindTag(obj.(*model.SegmentPort).Tags, scope)
					if value == "" {
						return nil, nil
					}
					return []string{value}, nil
				}
			}
			store := &subnetport.SegmentPortStore{ResourceStore: servicecommon.ResourceStore{
				Indexer: cache.NewIndexer(func(obj interface{}) (string, error) {
					return *obj.(*model.SegmentPort).Id, nil
				}, indexers),
			}}
			for _, name := range []string{"live", "orphan"} {
				port := &model.SegmentPort{
					Id: servicecommon.String(name),
					Tags: []model.Tag{{
						Scope: servicecommon.String(servicecommon.TagScopePodUID),
						Tag:   servicecommon.String(name + "-uid"),
					}},
				}
				if tt.statefulSet && name == "orphan" {
					port.Tags = append(port.Tags, model.Tag{
						Scope: servicecommon.String(servicecommon.TagScopeStatefulSetUID),
						Tag:   servicecommon.String("sts-uid"),
					})
				}
				require.NoError(t, store.Add(port))
			}
			service := &subnetport.SubnetPortService{
				Service: servicecommon.Service{
					Client: k8sClient, NSXClient: &nsx.Client{},
					NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}},
				},
				SegmentPortStore: store,
			}
			r := &PodReconciler{Client: k8sClient, SubnetPortService: service}
			r.StatusUpdater = common.NewStatusUpdater(k8sClient, service.NSXConfig, nil, MetricResTypePod, "SubnetPort", "Pod")
			patches := gomonkey.NewPatches()
			defer patches.Reset()
			patches.ApplyFunc((*subnetport.SubnetPortStore).GetVpcSubnetPortByUID,
				func(_ *subnetport.SubnetPortStore, _ types.UID) (*model.VpcSubnetPort, error) {
					if tt.vpcLookupErr {
						return nil, errors.New("VPC lookup failed")
					}
					return nil, nil
				})
			patches.ApplyFunc(nsx.StatefulSetPodSubnetPortFeatureEnabled,
				func(_ *nsx.Client, _ *config.NSXOperatorConfig) bool { return tt.retainSTS })
			if tt.segmentErr {
				patches.ApplyFunc((*subnetport.SegmentPortStore).GetSegmentPortByUID,
					func(_ *subnetport.SegmentPortStore, _ types.UID) (*model.SegmentPort, error) {
						return nil, errors.New("segment lookup failed")
					})
			}
			deleted := sets.New[string]()
			patches.ApplyFunc((*subnetport.SubnetPortService).DeleteSubnetPortById,
				func(_ *subnetport.SubnetPortService, id string) error {
					deleted.Insert(id)
					return nil
				})
			err := r.CollectGarbage(context.Background())
			if tt.segmentErr {
				require.EqualError(t, err, "segment lookup failed")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantDeleted, deleted)
		})
	}
}
