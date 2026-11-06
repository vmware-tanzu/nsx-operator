package subnetport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/mock"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

func TestCheckAndGetSubnetPathForExistingSegmentPort(t *testing.T) {
	const segmentPath = "/infra/segments/segment-1"
	const trackingSubnetPath = "/orgs/default/projects/default/vpcs/vpc-1/subnets/tracking-1"
	tests := []struct {
		name     string
		parent   string
		podOwner bool
		pathOnly bool
		segments []string
		wantErr  bool
	}{
		{name: "Subnet CR UID", parent: "Subnet", segments: []string{segmentPath}},
		{name: "owner Pod UID", parent: "Subnet", podOwner: true, segments: []string{segmentPath}},
		{name: "parent recovered from port path", parent: "Subnet", pathOnly: true, segments: []string{segmentPath}},
		{name: "explicit SubnetSet", parent: "SubnetSet", segments: []string{"/infra/segments/other", segmentPath}},
		{name: "default SubnetSet", segments: []string{segmentPath}},
		{name: "different segment", parent: "Subnet", segments: []string{"/infra/segments/other"}, wantErr: true},
		{name: "no tracking tag", parent: "Subnet", segments: []string{""}, wantErr: true},
		{name: "no candidate subnet", parent: "SubnetSet", wantErr: true},
		{name: "ambiguous tracking subnets", parent: "SubnetSet", segments: []string{segmentPath, segmentPath}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{
				Name: "port-1", Namespace: "ns-1", UID: "port-uid",
			}}
			switch tt.parent {
			case "Subnet":
				sp.Spec.Subnet = "subnet-1"
			case "SubnetSet":
				sp.Spec.SubnetSet = "subnetset-1"
			}
			uid, scope := sp.UID, servicecommon.TagScopeSubnetPortCRUID
			if tt.podOwner {
				uid, scope = "pod-uid", servicecommon.TagScopePodUID
				sp.OwnerReferences = []metav1.OwnerReference{{Kind: "Pod", Name: "pod-1", UID: uid}}
			}
			indexers := cache.Indexers{}
			for _, scope := range []string{servicecommon.TagScopeSubnetPortCRUID, servicecommon.TagScopePodUID} {
				indexers[scope] = func(obj interface{}) ([]string, error) {
					for _, tag := range obj.(*model.SegmentPort).Tags {
						if *tag.Scope == scope {
							return []string{*tag.Tag}, nil
						}
					}
					return nil, nil
				}
			}
			keyFunc := func(obj interface{}) (string, error) { return *obj.(*model.SegmentPort).Id, nil }
			service := &subnetport.SubnetPortService{
				SubnetPortStore: &subnetport.SubnetPortStore{ResourceStore: servicecommon.ResourceStore{
					Indexer: cache.NewIndexer(keyFunc, indexers),
				}},
				SegmentPortStore: &subnetport.SegmentPortStore{ResourceStore: servicecommon.ResourceStore{
					Indexer: cache.NewIndexer(keyFunc, indexers),
				}},
			}
			port := &model.SegmentPort{
				Id: servicecommon.String("existing-port"), Path: servicecommon.String(segmentPath + "/ports/existing-port"),
				Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String(string(uid))}},
			}
			if !tt.pathOnly {
				port.ParentPath = servicecommon.String(segmentPath)
			}
			require.NoError(t, service.SegmentPortStore.Apply(port))

			names := make([]string, len(tt.segments))
			nsxSubnets := make(map[string]*model.VpcSubnet)
			for i, segment := range tt.segments {
				name := fmt.Sprintf("subnet-%d", i+1)
				names[i] = name
				require.NoError(t, k8sClient.Create(context.Background(), &v1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: sp.Namespace},
				}))
				path := trackingSubnetPath
				if i != len(tt.segments)-1 {
					path += "-other"
				}
				nsxSubnet := &model.VpcSubnet{Path: &path}
				if segment != "" {
					nsxSubnet.Tags = []model.Tag{{
						Scope: servicecommon.String(servicecommon.TagScopeWCPSegmentTrackingSubnet), Tag: servicecommon.String(segment),
					}}
				}
				nsxSubnets[name] = nsxSubnet
			}
			if tt.parent != "Subnet" {
				require.NoError(t, k8sClient.Create(context.Background(), &v1alpha1.SubnetSet{
					ObjectMeta: metav1.ObjectMeta{
						Name: "subnetset-1", Namespace: sp.Namespace,
						Labels: map[string]string{servicecommon.LabelDefaultNetwork: servicecommon.DefaultVMNetwork},
					},
					Spec: v1alpha1.SubnetSetSpec{SubnetNames: &names},
				}))
			}
			patches := gomonkey.ApplyFunc((*subnet.SubnetService).GetSubnetByCR,
				func(_ *subnet.SubnetService, cr *v1alpha1.Subnet) (*model.VpcSubnet, error) {
					return nsxSubnets[cr.Name], nil
				})
			defer patches.Reset()
			r := &SubnetPortReconciler{Client: k8sClient, SubnetService: &subnet.SubnetService{}, SubnetPortService: service}
			existing, stale, path, subnetSetUID, subnetSetLock, ipType, allocationType, err := r.CheckAndGetSubnetPathForSubnetPort(context.Background(), sp)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "tracking Subnet")
				assert.False(t, existing)
			} else {
				require.NoError(t, err)
				assert.True(t, existing)
				assert.Equal(t, trackingSubnetPath, path)
			}
			assert.False(t, stale)
			assert.Nil(t, subnetSetUID)
			assert.Nil(t, subnetSetLock)
			assert.Empty(t, ipType)
			assert.Empty(t, allocationType)
			assert.Same(t, port, service.SegmentPortStore.GetByKey("existing-port"))
		})
	}
}

func TestReconcileSegmentPortWithoutVPCNetworkStatus(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	sp := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{Name: "port-1", Namespace: "ns-1", UID: "port-uid"},
		Spec: v1alpha1.SubnetPortSpec{
			InterfaceIPType: v1alpha1.IPAddressTypeIPv4, StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeNone,
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(sp).WithObjects(sp).Build()
	service := &subnetport.SubnetPortService{Service: servicecommon.Service{
		NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}},
	}}
	vpcService := &mock.MockVPCServiceProvider{}
	vpcService.On("IsRADeactivatedByVPCPath", "/tracking-subnet").Return(false, errors.New("RA configuration is unavailable"))
	r := &SubnetPortReconciler{
		Client: k8sClient, SubnetPortService: service, SubnetService: &subnet.SubnetService{}, VPCService: vpcService,
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, service.NSXConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
	patches := gomonkey.ApplyFunc((*SubnetPortReconciler).CheckAndGetSubnetPathForSubnetPort,
		func(_ *SubnetPortReconciler, _ context.Context, _ *v1alpha1.SubnetPort) (bool, bool, string, *types.UID, *sync.RWMutex, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, error) {
			return true, false, "/tracking-subnet", nil, nil, v1alpha1.IPAddressTypeIPv4, "", nil
		})
	defer patches.Reset()
	patches.ApplyFunc(common.IsSharedSubnetPath, func(context.Context, client.Client, string, string) (bool, error) {
		return true, nil
	})
	patches.ApplyFunc((*subnet.SubnetService).GetSubnetByPath,
		func(_ *subnet.SubnetService, _ string, _ bool) (*model.VpcSubnet, error) {
			return &model.VpcSubnet{RealizationId: servicecommon.String("switch-uuid"), Tags: []model.Tag{{
				Scope: servicecommon.String(servicecommon.TagScopeWCPSegmentTrackingSubnet), Tag: servicecommon.String("/infra/segments/segment-1"),
			}}}, nil
		})
	patches.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSegmentPort,
		func(_ *subnetport.SubnetPortService, _ *v1alpha1.SubnetPort, segmentPath, _ string, _ *map[string]string, _ bool, _ bool) (*model.SegmentPortState, error) {
			assert.Equal(t, "/infra/segments/segment-1", segmentPath)
			return &model.SegmentPortState{Attachment: &model.SegmentPortAttachmentState{Id: servicecommon.String("existing-attachment")}}, nil
		})
	patches.ApplyFunc(setAddressBindingStatusBySubnetPort,
		func(client.Client, context.Context, *v1alpha1.SubnetPort, *subnetport.SubnetPortService, metav1.Time, error, bool) {
		})

	key := types.NamespacedName{Namespace: sp.Namespace, Name: sp.Name}
	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, common.ResultNormal, result)
	vpcService.AssertNotCalled(t, "IsRADeactivatedByVPCPath", "/tracking-subnet")
	require.NoError(t, k8sClient.Get(ctx, key, sp))
	assert.Equal(t, "existing-attachment", sp.Status.Attachment.ID)
	assert.Equal(t, v1alpha1.NetworkInterfaceConfig{LogicalSwitchUUID: "switch-uuid"}, sp.Status.NetworkInterfaceConfig)
}

func TestReconcileSegmentPortSwitchUUIDWithoutPortState(t *testing.T) {
	for _, tt := range []struct {
		name          string
		realizationID *string
		wantUUID      string
	}{
		{name: "backfill unchanged port", realizationID: servicecommon.String("switch-uuid"), wantUUID: "switch-uuid"},
		{name: "missing realization ID", wantUUID: "old-switch-uuid"},
		{name: "empty realization ID", realizationID: servicecommon.String(""), wantUUID: "old-switch-uuid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			sp := &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{Name: "port-1", Namespace: "ns-1", UID: "port-uid"},
				Status: v1alpha1.SubnetPortStatus{
					Attachment: v1alpha1.PortAttachment{ID: "attachment"},
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{
						MACAddress:  "02:50:56:00:00:01",
						IPAddresses: []v1alpha1.NetworkInterfaceIPAddress{{IPAddress: "10.0.0.2/24"}},
					},
				},
			}
			if tt.realizationID == nil || *tt.realizationID == "" {
				sp.Status.NetworkInterfaceConfig.LogicalSwitchUUID = "old-switch-uuid"
			}
			oldStatus := sp.Status.DeepCopy()
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(sp).WithObjects(sp).Build()
			service := &subnetport.SubnetPortService{Service: servicecommon.Service{
				NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{}},
			}}
			r := &SubnetPortReconciler{Client: k8sClient, SubnetPortService: service}
			r.StatusUpdater = common.NewStatusUpdater(k8sClient, service.NSXConfig, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
			patches := gomonkey.ApplyFunc((*subnetport.SubnetPortService).CreateOrUpdateSegmentPort,
				func(_ *subnetport.SubnetPortService, _ *v1alpha1.SubnetPort, _, _ string, _ *map[string]string, _, _ bool) (*model.SegmentPortState, error) {
					return nil, nil
				})
			defer patches.Reset()
			patches.ApplyFunc(setAddressBindingStatusBySubnetPort,
				func(client.Client, context.Context, *v1alpha1.SubnetPort, *subnetport.SubnetPortService, metav1.Time, error, bool) {
				})
			key := types.NamespacedName{Namespace: sp.Namespace, Name: sp.Name}
			result, err := r.reconcileSubnetPort(ctx, ctrl.Request{NamespacedName: key}, sp, oldStatus,
				&model.VpcSubnet{RealizationId: tt.realizationID}, "/tracking-subnet", "/infra/segments/segment-1", true,
				nil, true, v1alpha1.IPAddressTypeIPv4, nil, "", false, false, "", nil, k8sClient)
			require.NoError(t, err)
			assert.Equal(t, common.ResultNormal, result)
			require.NoError(t, k8sClient.Get(ctx, key, sp))
			wantConfig := oldStatus.NetworkInterfaceConfig
			wantConfig.LogicalSwitchUUID = tt.wantUUID
			assert.Equal(t, wantConfig, sp.Status.NetworkInterfaceConfig)
			assert.Equal(t, oldStatus.Attachment, sp.Status.Attachment)
		})
	}
}

func newSegmentRestorePortStore() *subnetport.SegmentPortStore {
	indexers := cache.Indexers{}
	for _, scope := range []string{servicecommon.TagScopeSubnetPortCRUID, servicecommon.TagScopePodUID} {
		indexers[scope] = func(obj interface{}) ([]string, error) {
			var values []string
			for _, tag := range obj.(*model.SegmentPort).Tags {
				if *tag.Scope == scope {
					values = append(values, *tag.Tag)
				}
			}
			return values, nil
		}
	}
	return &subnetport.SegmentPortStore{ResourceStore: servicecommon.ResourceStore{
		Indexer: cache.NewIndexer(func(obj interface{}) (string, error) {
			return *obj.(*model.SegmentPort).Id, nil
		}, indexers),
	}}
}

func TestGetRestoreListSegmentTrackingSubnet(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	for _, tt := range []struct {
		name          string
		segment       bool
		cached        bool
		attachmentID  string
		logicalSwitch string
		hasIP         bool
		podOwned      bool
		missingParent bool
		wantRestore   bool
	}{
		{name: "missing segment port", segment: true, attachmentID: "attachment", logicalSwitch: "switch", wantRestore: true},
		{name: "cached segment port", segment: true, cached: true, attachmentID: "attachment", logicalSwitch: "switch", hasIP: true},
		{name: "missing attachment", segment: true, logicalSwitch: "switch", hasIP: true},
		{name: "missing logical switch", segment: true, attachmentID: "attachment", hasIP: true},
		{name: "missing status", segment: true, hasIP: true},
		{name: "segment port skips Pod handling", segment: true, attachmentID: "attachment", logicalSwitch: "switch", podOwned: true, wantRestore: true},
		{name: "normal port uses saved IP", hasIP: true, wantRestore: true},
		{name: "normal port without saved IP", attachmentID: "attachment", logicalSwitch: "switch"},
		{name: "missing parent", missingParent: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sp := &v1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: "ns", UID: "port-uid"},
				Spec:       v1alpha1.SubnetPortSpec{Subnet: "parent"},
				Status: v1alpha1.SubnetPortStatus{
					Attachment:             v1alpha1.PortAttachment{ID: tt.attachmentID},
					NetworkInterfaceConfig: v1alpha1.NetworkInterfaceConfig{LogicalSwitchUUID: tt.logicalSwitch},
				},
			}
			if tt.hasIP {
				sp.Status.NetworkInterfaceConfig.IPAddresses = []v1alpha1.NetworkInterfaceIPAddress{{IPAddress: "10.0.0.2/24"}}
			}
			if tt.podOwned {
				sp.OwnerReferences = []metav1.OwnerReference{{Kind: "Pod", Name: "pod", UID: "pod-uid"}}
			}
			parent := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "parent", Namespace: sp.Namespace}}
			if tt.segment {
				parent.Annotations = map[string]string{servicecommon.AnnotationSegmentPath: "/infra/segments/segment-1"}
			}
			builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sp)
			if !tt.missingParent {
				builder = builder.WithObjects(parent)
			}
			k8sClient := builder.Build()
			segmentStore := newSegmentRestorePortStore()
			if tt.cached {
				require.NoError(t, segmentStore.Add(&model.SegmentPort{
					Id: servicecommon.String("nsx-port"),
					Tags: []model.Tag{{
						Scope: servicecommon.String(servicecommon.TagScopeSubnetPortCRUID),
						Tag:   servicecommon.String(string(sp.UID)),
					}},
				}))
			}
			r := &SubnetPortReconciler{
				Client: k8sClient, APIReader: k8sClient,
				SubnetPortService: &subnetport.SubnetPortService{
					Service:          servicecommon.Service{Client: k8sClient},
					SubnetPortStore:  newPodRestorePortStore(),
					SegmentPortStore: segmentStore,
				},
			}
			restoreList, err := r.getRestoreList()
			if tt.missingParent {
				require.Error(t, err)
				assert.Empty(t, restoreList)
				return
			}
			require.NoError(t, err)
			if tt.wantRestore {
				assert.Equal(t, []types.NamespacedName{client.ObjectKeyFromObject(sp)}, restoreList)
			} else {
				assert.Empty(t, restoreList)
			}
		})
	}
}
