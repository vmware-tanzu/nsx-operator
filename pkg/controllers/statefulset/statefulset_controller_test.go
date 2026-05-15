/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package statefulset

import (
	"context"
	"errors"
	"reflect"
	goruntime "runtime"
	"testing"
	"time"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	subnetportservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

type MockManager struct {
	ctrl.Manager
	client client.Client
	scheme *runtime.Scheme
}

func (m *MockManager) GetClient() client.Client {
	return m.client
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *MockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return nil
}

func (m *MockManager) Add(runnable manager.Runnable) error {
	return nil
}

func (m *MockManager) Start(context.Context) error {
	return nil
}

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

// patchNSXClientStatefulSetPodVersion replaces (*nsx.Client).NSXCheckVersion for nsx.StatefulSetPod only.
// Use only in tests that do not combine gomonkey ApplyFunc on SubnetPortService in the same function.
func patchNSXClientStatefulSetPodVersion(t *testing.T, enabled bool) func() {
	t.Helper()
	patches := gomonkey.ApplyMethod(reflect.TypeOf((*nsx.Client)(nil)), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
		if feature == nsx.StatefulSetPod {
			return enabled
		}
		return false
	})
	return func() { patches.Reset() }
}

func testNSXConfigWithStatefulSetPodEnhance() *config.NSXOperatorConfig {
	on := true
	return &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			EnforcementPoint: "vmc-enforcementpoint",
			VpcWcpEnhance:    &on,
		},
	}
}

func TestParseIndexFromPodName(t *testing.T) {
	tests := []struct {
		name    string
		podName string
		want    int
	}{
		{
			name:    "valid index",
			podName: "tea-set-0",
			want:    0,
		},
		{
			name:    "valid index 5",
			podName: "tea-set-5",
			want:    5,
		},
		{
			name:    "no dash",
			podName: "teaset",
			want:    -1,
		},
		{
			name:    "invalid index",
			podName: "tea-set-abc",
			want:    -1,
		},
		{
			name:    "empty string",
			podName: "",
			want:    -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseIndexFromPodName(tt.podName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStsPodOrdinalFromPort(t *testing.T) {
	podIndexScope := servicecommon.TagScopePodIndex
	podNameScope := servicecommon.TagScopePodName
	tests := []struct {
		name string
		port *model.VpcSubnetPort
		want int
	}{
		{
			name: "pod-index tag",
			port: &model.VpcSubnetPort{
				Tags: []model.Tag{
					{Scope: &podIndexScope, Tag: servicecommon.String("9")},
					{Scope: &podNameScope, Tag: servicecommon.String("web-9")},
				},
			},
			want: 9,
		},
		{
			name: "fallback to pod name",
			port: &model.VpcSubnetPort{
				Tags: []model.Tag{
					{Scope: &podNameScope, Tag: servicecommon.String("tea-set-3")},
				},
			},
			want: 3,
		},
		{
			name: "prefer pod-index over misleading name",
			port: &model.VpcSubnetPort{
				Tags: []model.Tag{
					{Scope: &podIndexScope, Tag: servicecommon.String("2")},
					{Scope: &podNameScope, Tag: servicecommon.String("custom-template-9")},
				},
			},
			want: 2,
		},
		{
			name: "invalid pod-index falls back to name",
			port: &model.VpcSubnetPort{
				Tags: []model.Tag{
					{Scope: &podIndexScope, Tag: servicecommon.String("not-a-number")},
					{Scope: &podNameScope, Tag: servicecommon.String("tea-set-1")},
				},
			},
			want: 1,
		},
		{
			name: "nil port",
			port: nil,
			want: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stsPodOrdinalFromPort(tt.port)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPredicateUpdateFunc(t *testing.T) {
	tests := []struct {
		name        string
		oldReplicas *int32
		newReplicas *int32
		want        bool
	}{
		{
			name:        "shrink replicas",
			oldReplicas: func() *int32 { r := int32(5); return &r }(),
			newReplicas: func() *int32 { r := int32(3); return &r }(),
			want:        true,
		},
		{
			name:        "expand replicas",
			oldReplicas: func() *int32 { r := int32(3); return &r }(),
			newReplicas: func() *int32 { r := int32(5); return &r }(),
			want:        false,
		},
		{
			name:        "same replicas",
			oldReplicas: func() *int32 { r := int32(3); return &r }(),
			newReplicas: func() *int32 { r := int32(3); return &r }(),
			want:        false,
		},
		{
			name:        "old nil replicas",
			oldReplicas: nil,
			newReplicas: func() *int32 { r := int32(3); return &r }(),
			want:        false,
		},
		{
			name:        "new nil replicas",
			oldReplicas: func() *int32 { r := int32(3); return &r }(),
			newReplicas: nil,
			want:        false,
		},
		{
			name:        "both nil replicas",
			oldReplicas: nil,
			newReplicas: nil,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldSts := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: tt.oldReplicas,
				},
			}
			newSts := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: tt.newReplicas,
				},
			}

			updateEvent := event.UpdateEvent{
				ObjectOld: oldSts,
				ObjectNew: newSts,
			}
			result := PredicateFuncsForStatefulSet.UpdateFunc(updateEvent)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestPredicateFuncsForStatefulSet(t *testing.T) {
	createEvent := event.CreateEvent{}
	assert.False(t, PredicateFuncsForStatefulSet.CreateFunc(createEvent))

	deleteEvent := event.DeleteEvent{}
	assert.True(t, PredicateFuncsForStatefulSet.DeleteFunc(deleteEvent))

	genericEvent := event.GenericEvent{}
	assert.False(t, PredicateFuncsForStatefulSet.GenericFunc(genericEvent))
}

func TestRestoreReconcile(t *testing.T) {
	reconciler := &StatefulSetReconciler{}
	err := reconciler.RestoreReconcile()
	assert.NoError(t, err)
}

func TestNewStatefulSetReconciler(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			Client: fakeClient,
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	mockMgr := &MockManager{scheme: runtime.NewScheme(), client: fakeClient}
	patches := gomonkey.ApplyFunc((*StatefulSetReconciler).setupWithManager, func(r *StatefulSetReconciler, mgr manager.Manager) error {
		return nil
	})
	defer patches.Reset()
	r := NewStatefulSetReconciler(mockMgr, subnetPortService)
	assert.NotNil(t, r)
}

func TestStatefulSetReconciler_StartController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			Client: fakeClient,
		},
	}
	mockMgr := &MockManager{scheme: runtime.NewScheme(), client: fakeClient}
	patches := gomonkey.ApplyFunc((*StatefulSetReconciler).setupWithManager, func(r *StatefulSetReconciler, mgr manager.Manager) error {
		return nil
	})
	gcEntered := make(chan struct{}, 1)
	patches.ApplyFunc(common.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
		gcEntered <- struct{}{}
	})
	defer patches.Reset()
	r := NewStatefulSetReconciler(mockMgr, subnetPortService)
	err := r.StartController(mockMgr, nil)
	assert.Nil(t, err)
	// Wait until the GC goroutine has entered the patched collector before Reset; otherwise gomonkey
	// can corrupt unrelated patched methods in later tests.
	select {
	case <-gcEntered:
	case <-time.After(2 * time.Second):
		require.Fail(t, "patched GenericGarbageCollector was not invoked")
	}
	goruntime.Gosched()
	time.Sleep(10 * time.Millisecond)
}

func TestStatefulSetReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	r := &StatefulSetReconciler{
		Client: k8sClient,
		SubnetPortService: &subnetportservice.SubnetPortService{
			Service: servicecommon.Service{
				NSXClient: &nsx.Client{},
				NSXConfig: testNSXConfigWithStatefulSetPodEnhance(),
			},
			SubnetPortStore: &subnetportservice.SubnetPortStore{},
		},
		Recorder: fakeRecorder{},
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, r.SubnetPortService.NSXConfig, r.Recorder, MetricResTypeStatefulSet, "SubnetPort", "StatefulSet")
	defer patchNSXClientStatefulSetPodVersion(t, true)()

	tests := []struct {
		name           string
		prepareFunc    func(*testing.T, *StatefulSetReconciler) *gomonkey.Patches
		expectedErr    string
		expectedResult ctrl.Result
	}{
		{
			name: "StatefulSet not found",
			prepareFunc: func(t *testing.T, r *StatefulSetReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("not found"))
				return nil
			},
			expectedErr:    "not found",
			expectedResult: common.ResultRequeue,
		},
		{
			name: "StatefulSet found",
			prepareFunc: func(t *testing.T, r *StatefulSetReconciler) *gomonkey.Patches {
				sts := &appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sts",
						Namespace: "default",
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: func() *int32 { r := int32(3); return &r }(),
					},
					Status: appsv1.StatefulSetStatus{
						Replicas: 3,
					},
				}
				k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Do(
					func(ctx interface{}, key interface{}, obj interface{}, opts ...interface{}) error {
						sts.DeepCopyInto(obj.(*appsv1.StatefulSet))
						return nil
					})
				patches := gomonkey.ApplyFunc((*StatefulSetReconciler).handleReplicaChange,
					func(r *StatefulSetReconciler, ctx context.Context, sts *appsv1.StatefulSet) (ctrl.Result, error) {
						return ctrl.Result{}, nil
					})
				return patches
			},
			expectedResult: common.ResultNormal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(t, r)
			if patches != nil {
				defer patches.Reset()
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-sts",
					Namespace: "default",
				},
			}

			result, err := r.Reconcile(context.Background(), req)
			if tt.expectedErr != "" {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}
		})
	}
}

// TestZStatefulSetReconciler_Reconcile_RequeueAfterOutOfRangePortBlockedByPod is named with a Z prefix so it
// runs after other tests: gomonkey on ListSubnetPortByStsUid here can otherwise break ListSubnetPortByStsName patches.
func TestZStatefulSetReconciler_Reconcile_RequeueAfterOutOfRangePortBlockedByPod(t *testing.T) {
	// Register NSX patch before ListSubnetPortByStsUid patch defers so Reset runs in safe order on exit.
	defer patchNSXClientStatefulSetPodVersion(t, true)()
	scheme := runtime.NewScheme()
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	stsUID := types.UID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sts", Namespace: "default", UID: stsUID,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: func() *int32 { r := int32(2); return &r }(),
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-sts-2", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sts, pod).Build()

	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: testNSXConfigWithStatefulSetPodEnhance(),
		},
		SubnetPortStore: nil,
	}

	scope := servicecommon.TagScopePodName
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsUid,
		func(s *subnetportservice.SubnetPortService, ns string, stsUid string) []*model.VpcSubnetPort {
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port1"),
					Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String("test-sts-2")}}},
			}
		})
	defer patches.Reset()

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
		Recorder:          fakeRecorder{},
	}
	r.StatusUpdater = common.NewStatusUpdater(fakeClient, r.SubnetPortService.NSXConfig, r.Recorder, MetricResTypeStatefulSet, "SubnetPort", "StatefulSet")

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-sts", Namespace: "default"},
	})
	require.NoError(t, err)
	assert.Equal(t, stsSubnetPortPendingRequeueAfter, res.RequeueAfter)
}

func TestStatefulSetReconciler_CollectGarbage(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: testNSXConfigWithStatefulSetPodEnhance(),
		},
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            k8sClient,
		SubnetPortService: subnetPortService,
	}
	defer patchNSXClientStatefulSetPodVersion(t, true)()

	tests := []struct {
		name        string
		prepareFunc func(*testing.T, *StatefulSetReconciler) *gomonkey.Patches
		wantErr     bool
	}{
		{
			name: "list error",
			prepareFunc: func(t *testing.T, r *StatefulSetReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(errors.New("list failed"))
				return nil
			},
			wantErr: true,
		},
		{
			name: "list success with empty items",
			prepareFunc: func(t *testing.T, r *StatefulSetReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(
					func(ctx interface{}, list interface{}, opts ...interface{}) error {
						list.(*appsv1.StatefulSetList).Items = []appsv1.StatefulSet{}
						return nil
					})
				patches := gomonkey.ApplyFunc(
					(*subnetportservice.SubnetPortStore).GetByIndex,
					func(s *subnetportservice.SubnetPortStore, indexKey string, indexValue string) []*model.VpcSubnetPort {
						return []*model.VpcSubnetPort{}
					})
				return patches
			},
			wantErr: false,
		},
		{
			name: "list success with StatefulSet - replicas shrink",
			prepareFunc: func(t *testing.T, r *StatefulSetReconciler) *gomonkey.Patches {
				sts := &appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sts",
						Namespace: "default",
						UID:       "sts-uid-1",
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: func() *int32 { r := int32(2); return &r }(),
					},
					Status: appsv1.StatefulSetStatus{
						Replicas: 5,
					},
				}
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(
					func(ctx interface{}, list interface{}, opts ...interface{}) error {
						list.(*appsv1.StatefulSetList).Items = []appsv1.StatefulSet{*sts}
						return nil
					})
				callCount := 0
				patches := gomonkey.ApplyFunc(
					(*subnetportservice.SubnetPortStore).GetByIndex,
					func(s *subnetportservice.SubnetPortStore, indexKey string, indexValue string) []*model.VpcSubnetPort {
						callCount++
						if callCount == 1 {
							return []*model.VpcSubnetPort{
								{DisplayName: servicecommon.String("test-sts-0")},
								{DisplayName: servicecommon.String("test-sts-1")},
								{DisplayName: servicecommon.String("test-sts-2")},
								{DisplayName: servicecommon.String("test-sts-3")},
								{DisplayName: servicecommon.String("test-sts-4")},
							}
						}
						return []*model.VpcSubnetPort{}
					})
				patches.ApplyFunc(
					(*subnetportservice.SubnetPortService).DeleteSubnetPort,
					func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
						return nil
					})
				return patches
			},
			wantErr: false,
		},
		{
			name: "list success - stateful deleted (orphaned ports)",
			prepareFunc: func(t *testing.T, r *StatefulSetReconciler) *gomonkey.Patches {
				k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(
					func(ctx interface{}, list interface{}, opts ...interface{}) error {
						list.(*appsv1.StatefulSetList).Items = []appsv1.StatefulSet{}
						return nil
					})
				callCount := 0
				patches := gomonkey.ApplyFunc(
					(*subnetportservice.SubnetPortStore).GetByIndex,
					func(s *subnetportservice.SubnetPortStore, indexKey string, indexValue string) []*model.VpcSubnetPort {
						callCount++
						if callCount == 1 {
							return []*model.VpcSubnetPort{}
						}
						portID := servicecommon.String("orphaned-port-1")
						stsUID := servicecommon.String("deleted-sts-uid")
						return []*model.VpcSubnetPort{
							{
								Id:          portID,
								DisplayName: servicecommon.String("test-sts-0"),
								Tags: []model.Tag{
									{Scope: servicecommon.String(servicecommon.TagScopeStatefulSetUID), Tag: stsUID},
								},
							},
						}
					})
				patches.ApplyFunc(
					(*subnetportservice.SubnetPortService).DeleteSubnetPort,
					func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
						return nil
					})
				return patches
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := tt.prepareFunc(t, r)
			if patches != nil {
				defer patches.Reset()
			}

			err := r.CollectGarbage(context.Background())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStatefulSetReconciler_HandleReplicaChange(t *testing.T) {
	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		SubnetPortService: subnetPortService,
	}

	tests := []struct {
		name             string
		sts              *appsv1.StatefulSet
		prepareFunc      func(*testing.T, *StatefulSetReconciler) *gomonkey.Patches
		wantErr          bool
		wantRequeueAfter time.Duration
	}{
		{
			name: "replicas decreased",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sts",
					Namespace: "default",
					UID:       "test-sts-uid",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: func() *int32 { r := int32(2); return &r }(),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas: 5,
				},
			},
			prepareFunc: func(t *testing.T, r *StatefulSetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(
					(*subnetportservice.SubnetPortService).ListSubnetPortByStsUid,
					func(s *subnetportservice.SubnetPortService, ns string, stsUid string) []*model.VpcSubnetPort {
						return []*model.VpcSubnetPort{
							{Id: servicecommon.String("port1"), DisplayName: servicecommon.String("test-sts-0")},
							{Id: servicecommon.String("port2"), DisplayName: servicecommon.String("test-sts-1")},
							{Id: servicecommon.String("port3"), DisplayName: servicecommon.String("test-sts-2")},
							{Id: servicecommon.String("port4"), DisplayName: servicecommon.String("test-sts-3")},
							{Id: servicecommon.String("port5"), DisplayName: servicecommon.String("test-sts-4")},
						}
					})
				patches.ApplyFunc((*StatefulSetReconciler).releaseSubnetPortForPod,
					func(r *StatefulSetReconciler, ctx context.Context, namespace, podName string) (releaseSubnetPortOutcome, error) {
						return releaseSubnetPortReleased, nil
					})
				return patches
			},
			wantErr: false,
		},
		{
			name: "replicas increased",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sts",
					Namespace: "default",
					UID:       "test-sts-uid",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: func() *int32 { r := int32(5); return &r }(),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas: 3,
				},
			},
			prepareFunc: func(t *testing.T, r *StatefulSetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(
					(*subnetportservice.SubnetPortService).ListSubnetPortByStsUid,
					func(s *subnetportservice.SubnetPortService, ns string, stsUid string) []*model.VpcSubnetPort {
						return []*model.VpcSubnetPort{}
					})
				return patches
			},
			wantErr: false,
		},
		{
			name: "nil replicas spec",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sts",
					Namespace: "default",
					UID:       "test-sts-uid",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: nil,
				},
				Status: appsv1.StatefulSetStatus{
					Replicas: 3,
				},
			},
			prepareFunc: func(t *testing.T, r *StatefulSetReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(
					(*subnetportservice.SubnetPortService).ListSubnetPortByStsUid,
					func(s *subnetportservice.SubnetPortService, ns string, stsUid string) []*model.VpcSubnetPort {
						return []*model.VpcSubnetPort{}
					})
				patches.ApplyFunc((*StatefulSetReconciler).releaseSubnetPortForPod,
					func(r *StatefulSetReconciler, ctx context.Context, namespace, podName string) (releaseSubnetPortOutcome, error) {
						return releaseSubnetPortReleased, nil
					})
				return patches
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var patches *gomonkey.Patches
			if tt.prepareFunc != nil {
				patches = tt.prepareFunc(t, r)
				if patches != nil {
					defer patches.Reset()
				}
			}

			res, err := r.handleReplicaChange(context.Background(), tt.sts)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantRequeueAfter, res.RequeueAfter)
			}
		})
	}
}

type releaseSubnetPortsAux struct {
	deleteCalls int
}

func runReleaseSubnetPortsForStatefulSetCase(t *testing.T, prepare func(*testing.T, *subnetportservice.SubnetPortService, *releaseSubnetPortsAux) *gomonkey.Patches, wantErr bool, wantErrSubstring string, wantDeleteCalls int, wantPending *bool) {
	t.Helper()
	fakeClient := fake.NewClientBuilder().Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}
	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}
	aux := &releaseSubnetPortsAux{}
	patches := prepare(t, subnetPortService, aux)
	if patches != nil {
		defer patches.Reset()
	}

	pending, err := r.releaseSubnetPortsForStatefulSet(context.Background(), "default", "test-sts")
	if wantErr {
		require.Error(t, err)
		if wantErrSubstring != "" {
			assert.Contains(t, err.Error(), wantErrSubstring)
		}
	} else {
		require.NoError(t, err)
	}
	if wantPending != nil {
		assert.Equal(t, *wantPending, pending)
	}
	if wantDeleteCalls >= 0 {
		assert.Equal(t, wantDeleteCalls, aux.deleteCalls)
	}
}

func TestStatefulSetReconciler_ReleaseSubnetPortsForStatefulSet_noPorts(t *testing.T) {
	f := false
	runReleaseSubnetPortsForStatefulSetCase(t, func(t *testing.T, sps *subnetportservice.SubnetPortService, aux *releaseSubnetPortsAux) *gomonkey.Patches {
		return gomonkey.ApplyFunc(
			(*subnetportservice.SubnetPortService).ListSubnetPortByStsName,
			func(s *subnetportservice.SubnetPortService, ns string, stsName string) []*model.VpcSubnetPort {
				return []*model.VpcSubnetPort{}
			})
	}, false, "", -1, &f)
}

func TestStatefulSetReconciler_ReleaseSubnetPortsForStatefulSet_withPortsDeleteSuccess(t *testing.T) {
	fp := false
	runReleaseSubnetPortsForStatefulSetCase(t, func(t *testing.T, sps *subnetportservice.SubnetPortService, aux *releaseSubnetPortsAux) *gomonkey.Patches {
		patches := gomonkey.ApplyFunc(
			(*subnetportservice.SubnetPortService).ListSubnetPortByStsName,
			func(s *subnetportservice.SubnetPortService, ns string, stsName string) []*model.VpcSubnetPort {
				podNameScope := "nsx-op/pod_name"
				return []*model.VpcSubnetPort{
					{Id: servicecommon.String("port1"), DisplayName: servicecommon.String("test-sts-0"),
						Tags: []model.Tag{{Scope: &podNameScope, Tag: servicecommon.String("test-sts-0")}}},
					{Id: servicecommon.String("port2"), DisplayName: servicecommon.String("test-sts-1"),
						Tags: []model.Tag{{Scope: &podNameScope, Tag: servicecommon.String("test-sts-1")}}},
				}
			})
		patches.ApplyFunc(
			(*subnetportservice.SubnetPortService).DeleteSubnetPort,
			func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
				return nil
			})
		return patches
	}, false, "", -1, &fp)
}

func TestStatefulSetReconciler_ReleaseSubnetPortsForStatefulSet_portWithoutPodNameTagStillDeletes(t *testing.T) {
	fp := false
	runReleaseSubnetPortsForStatefulSetCase(t, func(t *testing.T, sps *subnetportservice.SubnetPortService, aux *releaseSubnetPortsAux) *gomonkey.Patches {
		patches := gomonkey.ApplyFunc(
			(*subnetportservice.SubnetPortService).ListSubnetPortByStsName,
			func(s *subnetportservice.SubnetPortService, ns, stsName string) []*model.VpcSubnetPort {
				return []*model.VpcSubnetPort{{Id: servicecommon.String("orphan-id")}}
			})
		patches.ApplyFunc(
			(*subnetportservice.SubnetPortService).DeleteSubnetPort,
			func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
				aux.deleteCalls++
				return nil
			})
		return patches
	}, false, "", 1, &fp)
}

func TestStatefulSetReconciler_ReleaseSubnetPortsForStatefulSet_deleteSubnetPortErrorAggregates(t *testing.T) {
	fp := false
	runReleaseSubnetPortsForStatefulSetCase(t, func(t *testing.T, sps *subnetportservice.SubnetPortService, aux *releaseSubnetPortsAux) *gomonkey.Patches {
		patches := gomonkey.ApplyFunc(
			(*subnetportservice.SubnetPortService).ListSubnetPortByStsName,
			func(s *subnetportservice.SubnetPortService, ns, stsName string) []*model.VpcSubnetPort {
				return []*model.VpcSubnetPort{{Id: servicecommon.String("p1")}}
			})
		patches.ApplyFunc(
			(*subnetportservice.SubnetPortService).DeleteSubnetPort,
			func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
				return errors.New("nsx delete failed")
			})
		return patches
	}, true, "errors found in releasing subnet ports", -1, &fp)
}

func runReleaseSubnetPortForPodCase(t *testing.T, prepare func(*testing.T, *subnetportservice.SubnetPortService) *gomonkey.Patches, wantErr bool, wantOutcome releaseSubnetPortOutcome) {
	t.Helper()
	fakeClient := fake.NewClientBuilder().Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}
	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}
	patches := prepare(t, subnetPortService)
	if patches != nil {
		defer patches.Reset()
	}
	outcome, err := r.releaseSubnetPortForPod(context.Background(), "default", "test-sts-0")
	if wantErr {
		assert.Error(t, err)
	} else {
		assert.NoError(t, err)
		assert.Equal(t, wantOutcome, outcome)
	}
}

func TestStatefulSetReconciler_ReleaseSubnetPortForPod_noPorts(t *testing.T) {
	runReleaseSubnetPortForPodCase(t, func(t *testing.T, sps *subnetportservice.SubnetPortService) *gomonkey.Patches {
		return gomonkey.ApplyFunc(
			(*subnetportservice.SubnetPortService).ListSubnetPortByPodName,
			func(s *subnetportservice.SubnetPortService, ns string, name string) []*model.VpcSubnetPort {
				return []*model.VpcSubnetPort{}
			})
	}, false, releaseSubnetPortNoop)
}

func TestStatefulSetReconciler_ReleaseSubnetPortForPod_withPortsDeleteSuccess(t *testing.T) {
	runReleaseSubnetPortForPodCase(t, func(t *testing.T, sps *subnetportservice.SubnetPortService) *gomonkey.Patches {
		patches := gomonkey.ApplyFunc(
			(*subnetportservice.SubnetPortService).ListSubnetPortByPodName,
			func(s *subnetportservice.SubnetPortService, ns string, name string) []*model.VpcSubnetPort {
				return []*model.VpcSubnetPort{
					{Id: servicecommon.String("port1")},
				}
			})
		patches.ApplyFunc(
			(*subnetportservice.SubnetPortService).DeleteSubnetPort,
			func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
				return nil
			})
		return patches
	}, false, releaseSubnetPortReleased)
}

func TestSetupWithManager(t *testing.T) {
	reconciler := &StatefulSetReconciler{}
	err := reconciler.setupWithManager(nil)
	assert.Error(t, err)
}

func TestStart(t *testing.T) {
	reconciler := &StatefulSetReconciler{}
	err := reconciler.Start(nil)
	assert.Error(t, err)
}

func TestGetOrdinalRange(t *testing.T) {
	tests := []struct {
		name          string
		sts           *appsv1.StatefulSet
		expectedStart int
		expectedEnd   int
	}{
		{
			name: "default ordinals",
			sts: &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Replicas: func() *int32 { r := int32(3); return &r }(),
				},
			},
			expectedStart: 0,
			expectedEnd:   2,
		},
		{
			name: "custom ordinals start",
			sts: &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Ordinals: &appsv1.StatefulSetOrdinals{Start: 10},
					Replicas: func() *int32 { r := int32(3); return &r }(),
				},
			},
			expectedStart: 10,
			expectedEnd:   12,
		},
		{
			name: "zero replicas",
			sts: &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Replicas: func() *int32 { r := int32(0); return &r }(),
				},
			},
			expectedStart: -1,
			expectedEnd:   -1,
		},
		{
			name:          "nil replicas",
			sts:           &appsv1.StatefulSet{},
			expectedStart: -1,
			expectedEnd:   -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &StatefulSetReconciler{}
			start, end := r.GetOrdinalRange(tt.sts)
			assert.Equal(t, tt.expectedStart, start)
			assert.Equal(t, tt.expectedEnd, end)
		})
	}
}

func TestHandleReplicaChange_WithOrdinals(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		// Nil store: if releaseSubnetPortForPod is not monkey-patched, ListSubnetPortByPodName returns
		// empty without touching an uninitialized indexer (avoids panic; does not affect patched ListSubnetPortByStsUid).
		SubnetPortStore: nil,
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts",
			Namespace: "default",
			UID:       "test-sts-uid",
		},
		Spec: appsv1.StatefulSetSpec{
			Ordinals: &appsv1.StatefulSetOrdinals{Start: 5},
			Replicas: func() *int32 { r := int32(3); return &r }(),
		},
	}

	scope := servicecommon.TagScopePodName
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsUid,
		func(s *subnetportservice.SubnetPortService, ns string, stsUid string) []*model.VpcSubnetPort {
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port1"), Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String("test-sts-0")}}},
				{Id: servicecommon.String("port2"), Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String("test-sts-1")}}},
				{Id: servicecommon.String("port3"), Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String("test-sts-5")}}},
				{Id: servicecommon.String("port4"), Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String("test-sts-6")}}},
				{Id: servicecommon.String("port5"), Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String("test-sts-7")}}},
			}
		})
	patches.ApplyFunc((*StatefulSetReconciler).releaseSubnetPortForPod,
		func(r *StatefulSetReconciler, ctx context.Context, namespace, podName string) (releaseSubnetPortOutcome, error) {
			return releaseSubnetPortReleased, nil
		})
	defer patches.Reset()

	res, err := r.handleReplicaChange(context.Background(), sts)
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)
}

func TestHandleReplicaChange_WithNilDisplayName(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: nil,
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts",
			Namespace: "default",
			UID:       "test-sts-uid",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: func() *int32 { r := int32(2); return &r }(),
		},
	}

	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsUid,
		func(s *subnetportservice.SubnetPortService, ns string, stsUid string) []*model.VpcSubnetPort {
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port1")},
			}
		})
	defer patches.Reset()

	res, err := r.handleReplicaChange(context.Background(), sts)
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)
}

func TestReleaseSubnetPortForPod_PodExists(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	outcome, err := r.releaseSubnetPortForPod(context.Background(), "default", "existing-pod")
	assert.NoError(t, err)
	assert.Equal(t, releaseSubnetPortSkippedRunningPod, outcome)
}

func TestReleaseSubnetPortsForStatefulSet_PodExists(t *testing.T) {
	livePodUID := types.UID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	fakeClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts-0",
			Namespace: "default",
			UID:       livePodUID,
		},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	var deleteCalls int
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsName,
		func(s *subnetportservice.SubnetPortService, ns string, stsName string) []*model.VpcSubnetPort {
			podNameScope := "nsx-op/pod_name"
			podUIDScope := servicecommon.TagScopePodUID
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port1"), DisplayName: servicecommon.String("test-sts-0"),
					Tags: []model.Tag{
						{Scope: &podNameScope, Tag: servicecommon.String("test-sts-0")},
						{Scope: &podUIDScope, Tag: servicecommon.String(string(livePodUID))},
					}},
			}
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			deleteCalls++
			return nil
		})
	defer patches.Reset()

	pending, err := r.releaseSubnetPortsForStatefulSet(context.Background(), "default", "test-sts")
	assert.NoError(t, err)
	assert.True(t, pending, "caller should requeue while live pod still blocks port deletion")
	assert.Equal(t, 0, deleteCalls, "port should be retained when pod exists and port pod_uid matches pod.UID")
}

func TestReleaseSubnetPortsForStatefulSet_PodSucceededAllowsDeleteWithMatchingUID(t *testing.T) {
	livePodUID := types.UID("cccccccc-cccc-cccc-cccc-cccccccccccc")
	fakeClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts-0",
			Namespace: "default",
			UID:       livePodUID,
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	var deleteCalls int
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsName,
		func(s *subnetportservice.SubnetPortService, ns string, stsName string) []*model.VpcSubnetPort {
			podNameScope := "nsx-op/pod_name"
			podUIDScope := servicecommon.TagScopePodUID
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port1"), DisplayName: servicecommon.String("test-sts-0"),
					Tags: []model.Tag{
						{Scope: &podNameScope, Tag: servicecommon.String("test-sts-0")},
						{Scope: &podUIDScope, Tag: servicecommon.String(string(livePodUID))},
					}},
			}
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			deleteCalls++
			return nil
		})
	defer patches.Reset()

	pending, err := r.releaseSubnetPortsForStatefulSet(context.Background(), "default", "test-sts")
	assert.NoError(t, err)
	assert.False(t, pending)
	assert.Equal(t, 1, deleteCalls, "port should be deleted when pod is Succeeded even if UID still matches")
}

func TestReleaseSubnetPortsForStatefulSet_PodTerminatingSkipsDeleteWithMatchingUID(t *testing.T) {
	livePodUID := types.UID("dddddddd-dddd-dddd-dddd-dddddddddddd")
	now := metav1.Now()
	fakeClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-sts-0",
			Namespace:         "default",
			UID:               livePodUID,
			DeletionTimestamp: &now,
			Finalizers:        []string{"test-finalizer"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	var deleteCalls int
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsName,
		func(s *subnetportservice.SubnetPortService, ns string, stsName string) []*model.VpcSubnetPort {
			podNameScope := "nsx-op/pod_name"
			podUIDScope := servicecommon.TagScopePodUID
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port1"), DisplayName: servicecommon.String("test-sts-0"),
					Tags: []model.Tag{
						{Scope: &podNameScope, Tag: servicecommon.String("test-sts-0")},
						{Scope: &podUIDScope, Tag: servicecommon.String(string(livePodUID))},
					}},
			}
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			deleteCalls++
			return nil
		})
	defer patches.Reset()

	pending, err := r.releaseSubnetPortsForStatefulSet(context.Background(), "default", "test-sts")
	assert.NoError(t, err)
	assert.True(t, pending, "terminating-but-not-terminal pod should defer delete and surface pending for requeue")
	assert.Equal(t, 0, deleteCalls, "PodIsDeleted is terminal phase only: Running+DeletionTimestamp must not release port when UID still matches")
}

func TestReleaseSubnetPortsForStatefulSet_PodUIDMismatchDeletesPort(t *testing.T) {
	livePodUID := types.UID("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	stalePortPodUID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	fakeClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts-0",
			Namespace: "default",
			UID:       livePodUID,
		},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	var deleteCalls int
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsName,
		func(s *subnetportservice.SubnetPortService, ns string, stsName string) []*model.VpcSubnetPort {
			podNameScope := "nsx-op/pod_name"
			podUIDScope := servicecommon.TagScopePodUID
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port1"), DisplayName: servicecommon.String("test-sts-0"),
					Tags: []model.Tag{
						{Scope: &podNameScope, Tag: servicecommon.String("test-sts-0")},
						{Scope: &podUIDScope, Tag: servicecommon.String(stalePortPodUID)},
					}},
			}
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			deleteCalls++
			return nil
		})
	defer patches.Reset()

	pending, err := r.releaseSubnetPortsForStatefulSet(context.Background(), "default", "test-sts")
	assert.NoError(t, err)
	assert.False(t, pending)
	assert.Equal(t, 1, deleteCalls, "stale subnet port for same pod name but different pod UID should be deleted")
}

func TestReleaseSubnetPortForPod_DeleteError(t *testing.T) {
	// Use fake client (empty cluster) so Get returns NotFound without gomock + gomonkey cross-test interference.
	fakeClient := fake.NewClientBuilder().Build()

	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByPodName,
		func(s *subnetportservice.SubnetPortService, ns string, podName string) []*model.VpcSubnetPort {
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port-delete-error"), DisplayName: servicecommon.String("test-sts-0"), Path: servicecommon.String("/path/to/port")},
			}
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			return errors.New("delete subnet port failed")
		})
	defer patches.Reset()

	_, err := r.releaseSubnetPortForPod(context.Background(), "default", "test-pod-0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete subnet port failed")
}

func TestCollectGarbage_OrphanedPortDeleteError(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: testNSXConfigWithStatefulSetPodEnhance(),
		},
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}
	defer patchNSXClientStatefulSetPodVersion(t, true)()

	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortStore).GetByIndex,
		func(s *subnetportservice.SubnetPortStore, indexKey string, indexValue string) []*model.VpcSubnetPort {
			if indexKey == servicecommon.IndexKeyAllStsPorts {
				stsUIDScope := "nsx-op/sts_uid"
				nsScope := "nsx-op/namespace"
				podNameScope := "nsx-op/pod_name"
				return []*model.VpcSubnetPort{
					{Id: servicecommon.String("port-orphan"),
						Tags: []model.Tag{
							{Scope: &stsUIDScope, Tag: servicecommon.String("deleted-sts-uid")},
							{Scope: &nsScope, Tag: servicecommon.String("default")},
							{Scope: &podNameScope, Tag: servicecommon.String("test-pod-0")},
						}},
				}
			}
			return []*model.VpcSubnetPort{}
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			return errors.New("delete orphaned port failed")
		})
	defer patches.Reset()

	err := r.CollectGarbage(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete error(s)")
}

func TestCollectGarbage_MixedErrors(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts",
			Namespace: "default",
			UID:       "test-sts-uid",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: func() *int32 { r := int32(2); return &r }(),
		},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: testNSXConfigWithStatefulSetPodEnhance(),
		},
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}
	defer patchNSXClientStatefulSetPodVersion(t, true)()

	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortStore).GetByIndex,
		func(s *subnetportservice.SubnetPortStore, indexKey string, indexValue string) []*model.VpcSubnetPort {
			if indexKey == servicecommon.TagScopeStatefulSetUID && indexValue == "test-sts-uid" {
				podNameScope := "nsx-op/pod_name"
				return []*model.VpcSubnetPort{
					{Id: servicecommon.String("port-oor"), DisplayName: servicecommon.String("test-sts-5"), Tags: []model.Tag{{Scope: &podNameScope, Tag: servicecommon.String("test-sts-5")}}},
				}
			}
			if indexKey == servicecommon.IndexKeyAllStsPorts {
				stsUIDScope := "nsx-op/sts_uid"
				nsScope := "nsx-op/namespace"
				podNameScope := "nsx-op/pod_name"
				return []*model.VpcSubnetPort{
					{Id: servicecommon.String("port-orphan"),
						Tags: []model.Tag{
							{Scope: &stsUIDScope, Tag: servicecommon.String("deleted-sts-uid")},
							{Scope: &nsScope, Tag: servicecommon.String("default")},
							{Scope: &podNameScope, Tag: servicecommon.String("test-pod-0")},
						}},
				}
			}
			return []*model.VpcSubnetPort{}
		})

	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			if port.Path == nil {
				port.Path = servicecommon.String("/orgs/default/projects/default/vpcs/default/subnets/default/ports/default")
			}
			if port.Id != nil && *port.Id == "port-oor" {
				return errors.New("failed to delete out-of-range port")
			}
			if port.Id != nil && *port.Id == "port-orphan" {
				return errors.New("failed to delete orphaned port")
			}
			return nil
		})
	defer patches.Reset()

	err := r.CollectGarbage(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete out-of-range port")
	assert.Contains(t, err.Error(), "failed to delete orphaned port")
}

func TestCollectGarbage_WithNilDisplayName(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts",
			Namespace: "default",
			UID:       "test-sts-uid",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: func() *int32 { r := int32(2); return &r }(),
		},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: testNSXConfigWithStatefulSetPodEnhance(),
		},
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}
	defer patchNSXClientStatefulSetPodVersion(t, true)()

	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortStore).GetByIndex,
		func(s *subnetportservice.SubnetPortStore, indexKey string, indexValue string) []*model.VpcSubnetPort {
			if indexKey == servicecommon.TagScopeStatefulSetUID && indexValue == "test-sts-uid" {
				return []*model.VpcSubnetPort{
					{Id: servicecommon.String("port1"), DisplayName: nil},
					{Id: servicecommon.String("port2"), DisplayName: servicecommon.String("test-sts-0")},
				}
			}
			if indexKey == servicecommon.IndexKeyAllStsPorts {
				return []*model.VpcSubnetPort{}
			}
			return []*model.VpcSubnetPort{}
		})
	defer patches.Reset()

	err := r.CollectGarbage(context.Background())
	assert.NoError(t, err)
}

func TestCollectGarbage_WithInvalidIndexDisplayName(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts",
			Namespace: "default",
			UID:       "test-sts-uid",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: func() *int32 { r := int32(2); return &r }(),
		},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: testNSXConfigWithStatefulSetPodEnhance(),
		},
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}
	defer patchNSXClientStatefulSetPodVersion(t, true)()

	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortStore).GetByIndex,
		func(s *subnetportservice.SubnetPortStore, indexKey string, indexValue string) []*model.VpcSubnetPort {
			if indexKey == servicecommon.TagScopeStatefulSetUID && indexValue == "test-sts-uid" {
				return []*model.VpcSubnetPort{
					{Id: servicecommon.String("port1"), DisplayName: servicecommon.String("invalid-name")},
					{Id: servicecommon.String("port2"), DisplayName: servicecommon.String("test-sts-0")},
				}
			}
			if indexKey == servicecommon.IndexKeyAllStsPorts {
				return []*model.VpcSubnetPort{}
			}
			return []*model.VpcSubnetPort{}
		})
	defer patches.Reset()

	err := r.CollectGarbage(context.Background())
	assert.NoError(t, err)
}

func TestHandleReplicaChange_WithPodStillExists(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts-2",
			Namespace: "default",
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: nil,
	}

	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts",
			Namespace: "default",
			UID:       "test-sts-uid",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: func() *int32 { r := int32(2); return &r }(),
		},
	}

	scope := servicecommon.TagScopePodName
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsUid,
		func(s *subnetportservice.SubnetPortService, ns string, stsUid string) []*model.VpcSubnetPort {
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port1"),
					Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String("test-sts-2")}}},
			}
		})
	defer patches.Reset()

	res, err := r.handleReplicaChange(context.Background(), sts)
	assert.NoError(t, err)
	assert.Equal(t, stsSubnetPortPendingRequeueAfter, res.RequeueAfter)
}

// TestHandleReplicaChange_ReplicasZeroWithRunningPod covers GetOrdinalRange (-1,-1): every
// parseable ordinal is out of range; a still-running Pod forces requeue instead of deleting.
func TestHandleReplicaChange_ReplicasZeroWithRunningPod(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts-0",
			Namespace: "default",
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{SubnetPortStore: nil}
	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sts", Namespace: "default", UID: "test-sts-uid",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: func() *int32 { z := int32(0); return &z }(),
		},
	}

	scope := servicecommon.TagScopePodName
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsUid,
		func(s *subnetportservice.SubnetPortService, ns string, stsUid string) []*model.VpcSubnetPort {
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port0"),
					Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String("test-sts-0")}}},
			}
		})
	defer patches.Reset()

	res, err := r.handleReplicaChange(context.Background(), sts)
	assert.NoError(t, err)
	assert.Equal(t, stsSubnetPortPendingRequeueAfter, res.RequeueAfter)
}

// TestHandleReplicaChange_ReplicasZeroReleasesWhenPodsGone verifies scale-to-zero releases
// NSX ports once Pods are gone (no requeue when nothing is blocked by a live Pod).
func TestHandleReplicaChange_ReplicasZeroReleasesWhenPodsGone(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()
	subnetPortService := &subnetportservice.SubnetPortService{SubnetPortStore: nil}
	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sts", Namespace: "default", UID: "test-sts-uid",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: func() *int32 { z := int32(0); return &z }(),
		},
	}

	scope := servicecommon.TagScopePodName
	var deleteCalls int
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsUid,
		func(s *subnetportservice.SubnetPortService, ns string, stsUid string) []*model.VpcSubnetPort {
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port0"),
					Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String("test-sts-0")}}},
			}
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByPodName,
		func(s *subnetportservice.SubnetPortService, ns string, name string) []*model.VpcSubnetPort {
			if name == "test-sts-0" {
				return []*model.VpcSubnetPort{{Id: servicecommon.String("port0")}}
			}
			return nil
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			deleteCalls++
			return nil
		})
	defer patches.Reset()

	res, err := r.handleReplicaChange(context.Background(), sts)
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)
	assert.Equal(t, 1, deleteCalls)
}

// TestHandleReplicaChange_MixedReleasedAndSkippedRunningPod ensures pendingRunningPod is OR'd:
// one out-of-range port is released while another is blocked by a running Pod → still requeue.
func TestHandleReplicaChange_MixedReleasedAndSkippedRunningPod(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts-4",
			Namespace: "default",
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{SubnetPortStore: nil}
	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sts", Namespace: "default", UID: "test-sts-uid",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: func() *int32 { r := int32(2); return &r }(),
		},
	}

	scope := servicecommon.TagScopePodName
	var deleteCalls int
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsUid,
		func(s *subnetportservice.SubnetPortService, ns string, stsUid string) []*model.VpcSubnetPort {
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port3"),
					Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String("test-sts-3")}}},
				{Id: servicecommon.String("port4"),
					Tags: []model.Tag{{Scope: &scope, Tag: servicecommon.String("test-sts-4")}}},
			}
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByPodName,
		func(s *subnetportservice.SubnetPortService, ns string, name string) []*model.VpcSubnetPort {
			switch name {
			case "test-sts-3":
				return []*model.VpcSubnetPort{{Id: servicecommon.String("port3")}}
			case "test-sts-4":
				return []*model.VpcSubnetPort{{Id: servicecommon.String("port4")}}
			default:
				return nil
			}
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			deleteCalls++
			return nil
		})
	defer patches.Reset()

	res, err := r.handleReplicaChange(context.Background(), sts)
	assert.NoError(t, err)
	assert.Equal(t, stsSubnetPortPendingRequeueAfter, res.RequeueAfter)
	assert.Equal(t, 1, deleteCalls, "only the out-of-range Pod with no live Pod should be deleted")
}

func TestReleaseSubnetPortForPod_ClientGetError(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)
	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("apiserver timeout"))

	subnetPortService := &subnetportservice.SubnetPortService{SubnetPortStore: nil}
	r := &StatefulSetReconciler{
		Client:            k8sClient,
		SubnetPortService: subnetPortService,
	}

	outcome, err := r.releaseSubnetPortForPod(context.Background(), "default", "any-pod")
	assert.Error(t, err)
	assert.Equal(t, releaseSubnetPortNoop, outcome)
}

func TestReleaseSubnetPortForPod_DeleteSubnetPortFails(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()
	subnetPortService := &subnetportservice.SubnetPortService{SubnetPortStore: nil}
	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}

	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByPodName,
		func(s *subnetportservice.SubnetPortService, ns string, name string) []*model.VpcSubnetPort {
			return []*model.VpcSubnetPort{{Id: servicecommon.String("p1")}}
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			return errors.New("nsx delete failed")
		})
	defer patches.Reset()

	outcome, err := r.releaseSubnetPortForPod(context.Background(), "default", "gone-pod")
	assert.Error(t, err)
	assert.Equal(t, releaseSubnetPortNoop, outcome)
}

func TestReconcile_PodCacheNotReady(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: testNSXConfigWithStatefulSetPodEnhance(),
		},
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}

	r := &StatefulSetReconciler{
		Client:            k8sClient,
		SubnetPortService: subnetPortService,
		Recorder:          fakeRecorder{},
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, r.SubnetPortService.NSXConfig, r.Recorder, MetricResTypeStatefulSet, "SubnetPort", "StatefulSet")
	defer patchNSXClientStatefulSetPodVersion(t, true)()

	// Return an arbitrary error from Get to simulate cache not ready
	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("cache not ready"))

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sts", Namespace: "default"}}
	res, err := r.Reconcile(context.Background(), req)

	assert.Error(t, err)
	assert.Equal(t, "cache not ready", err.Error())
	assert.True(t, res.Requeue) //nolint:staticcheck // SA1019: Requeue is deprecated
}

func TestReconcile_WithDeleteAnnotation(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: testNSXConfigWithStatefulSetPodEnhance(),
		},
		// No store: ListSubnetPortByStsName returns empty without gomonkey (avoids leaking a global patch).
		SubnetPortStore: nil,
	}

	r := &StatefulSetReconciler{
		Client:            k8sClient,
		SubnetPortService: subnetPortService,
		Recorder:          fakeRecorder{},
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, r.SubnetPortService.NSXConfig, r.Recorder, MetricResTypeStatefulSet, "SubnetPort", "StatefulSet")
	defer patchNSXClientStatefulSetPodVersion(t, true)()

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-sts",
			Namespace:         "default",
			DeletionTimestamp: &metav1.Time{Time: time.Now()},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: func() *int32 { r := int32(2); return &r }(),
		},
	}

	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			sts.DeepCopyInto(obj.(*appsv1.StatefulSet))
			return nil
		})

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-sts", Namespace: "default"}}
	res, err := r.Reconcile(context.Background(), req)

	assert.NoError(t, err)
	assert.Equal(t, common.ResultNormal, res)
}

func TestPredicateUpdateFunc_NonStatefulSetObjects(t *testing.T) {
	ev := event.UpdateEvent{
		ObjectOld: &corev1.Pod{},
		ObjectNew: &corev1.Pod{},
	}
	assert.False(t, PredicateFuncsForStatefulSet.UpdateFunc(ev))
}

func TestPredicateUpdateFunc_OrdinalsStartIncreasedShrinksRange(t *testing.T) {
	oldSts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Spec: appsv1.StatefulSetSpec{
			Ordinals: &appsv1.StatefulSetOrdinals{Start: 5},
			Replicas: func() *int32 { r := int32(3); return &r }(),
		},
	}
	newSts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "ns"},
		Spec: appsv1.StatefulSetSpec{
			Ordinals: &appsv1.StatefulSetOrdinals{Start: 7},
			Replicas: func() *int32 { r := int32(3); return &r }(),
		},
	}
	assert.True(t, PredicateFuncsForStatefulSet.UpdateFunc(event.UpdateEvent{ObjectOld: oldSts, ObjectNew: newSts}))
}

func TestReleaseSubnetPortForPod_GetPodError(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)
	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("apiserver timeout"))

	subnetPortService := &subnetportservice.SubnetPortService{SubnetPortStore: &subnetportservice.SubnetPortStore{}}
	r := &StatefulSetReconciler{Client: k8sClient, SubnetPortService: subnetPortService}

	_, err := r.releaseSubnetPortForPod(context.Background(), "default", "pod-0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "apiserver timeout")
}

func TestProcessDelete_ReleaseSubnetPortsError(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{EnforcementPoint: "vmc-enforcementpoint"}},
		},
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}
	r := &StatefulSetReconciler{
		Client:            k8sClient,
		SubnetPortService: subnetPortService,
		Recorder:          fakeRecorder{},
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, r.SubnetPortService.NSXConfig, r.Recorder, MetricResTypeStatefulSet, "SubnetPort", "StatefulSet")

	// Drive releaseSubnetPortsForStatefulSet failure via NSX delete error (more reliable than patching the reconciler method on darwin/arm64).
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsName,
		func(s *subnetportservice.SubnetPortService, ns, stsName string) []*model.VpcSubnetPort {
			return []*model.VpcSubnetPort{{Id: servicecommon.String("p1")}}
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			return errors.New("delete failed")
		})
	defer patches.Reset()

	res, err := r.processDelete(context.Background(), types.NamespacedName{Namespace: "default", Name: "sts"}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "errors found in releasing subnet ports")
	assert.Equal(t, common.ResultRequeue, res)
}

func TestProcessDelete_PendingRunningPodRequeues(t *testing.T) {
	livePodUID := types.UID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	fakeClient := fake.NewClientBuilder().WithObjects(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-sts-0", Namespace: "default", UID: livePodUID},
	}).Build()
	subnetPortService := &subnetportservice.SubnetPortService{
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}
	r := &StatefulSetReconciler{
		Client:            fakeClient,
		SubnetPortService: subnetPortService,
	}
	podNameScope := "nsx-op/pod_name"
	podUIDScope := servicecommon.TagScopePodUID
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortService).ListSubnetPortByStsName,
		func(s *subnetportservice.SubnetPortService, ns, stsName string) []*model.VpcSubnetPort {
			return []*model.VpcSubnetPort{
				{Id: servicecommon.String("port1"),
					Tags: []model.Tag{
						{Scope: &podNameScope, Tag: servicecommon.String("test-sts-0")},
						{Scope: &podUIDScope, Tag: servicecommon.String(string(livePodUID))},
					}},
			}
		})
	defer patches.Reset()

	res, err := r.processDelete(context.Background(), types.NamespacedName{Namespace: "default", Name: "test-sts"}, nil)
	require.NoError(t, err)
	assert.Equal(t, stsSubnetPortPendingRequeueAfter, res.RequeueAfter)
}

func TestReconcile_NoOpWhenFeatureDisabled(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{EnforcementPoint: "vmc-enforcementpoint"}},
		},
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}
	r := &StatefulSetReconciler{
		Client:            k8sClient,
		SubnetPortService: subnetPortService,
		Recorder:          fakeRecorder{},
	}
	r.StatusUpdater = common.NewStatusUpdater(k8sClient, r.SubnetPortService.NSXConfig, r.Recorder, MetricResTypeStatefulSet, "SubnetPort", "StatefulSet")
	defer patchNSXClientStatefulSetPodVersion(t, false)()

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "sts"}}
	res, err := r.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, common.ResultNormal, res)
}

func TestStatefulSetPodFeatureEnabled_NSXCheckVersion(t *testing.T) {
	defer patchNSXClientStatefulSetPodVersion(t, true)()
	s := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: testNSXConfigWithStatefulSetPodEnhance(),
		},
	}
	r := &StatefulSetReconciler{SubnetPortService: s}
	assert.True(t, r.StatefulSetPodFeatureEnabled())
}

func TestStatefulSetPodFeatureEnabled_DisabledWhenVpcWcpEnhanceUnset(t *testing.T) {
	defer patchNSXClientStatefulSetPodVersion(t, true)()
	s := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{EnforcementPoint: "vmc-enforcementpoint"}},
		},
	}
	r := &StatefulSetReconciler{SubnetPortService: s}
	assert.False(t, r.StatefulSetPodFeatureEnabled())
}

func TestStatefulSetPodFeatureEnabled_DisabledWhenVpcWcpEnhanceFalse(t *testing.T) {
	defer patchNSXClientStatefulSetPodVersion(t, true)()
	off := false
	s := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{NsxConfig: &config.NsxConfig{VpcWcpEnhance: &off}},
		},
	}
	r := &StatefulSetReconciler{SubnetPortService: s}
	assert.False(t, r.StatefulSetPodFeatureEnabled())
}

func TestCollectGarbage_GetPodErrorDoesNotSkipDelete(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	subnetPortService := &subnetportservice.SubnetPortService{
		Service: servicecommon.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: testNSXConfigWithStatefulSetPodEnhance(),
		},
		SubnetPortStore: &subnetportservice.SubnetPortStore{},
	}
	r := &StatefulSetReconciler{Client: k8sClient, SubnetPortService: subnetPortService}
	defer patchNSXClientStatefulSetPodVersion(t, true)()

	// List StatefulSets: one STS so orphaned path uses second loop with sts not in set
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			list.(*appsv1.StatefulSetList).Items = []appsv1.StatefulSet{
				{ObjectMeta: metav1.ObjectMeta{Name: "live", Namespace: "ns", UID: "live-uid"}, Spec: appsv1.StatefulSetSpec{Replicas: func() *int32 { r := int32(1); return &r }()}},
			}
			return nil
		})
	// GC orphan branch: Get pod fails → should not skip delete
	k8sClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("temporary")).MinTimes(1)

	var deleteCalls int
	patches := gomonkey.ApplyFunc(
		(*subnetportservice.SubnetPortStore).GetByIndex,
		func(s *subnetportservice.SubnetPortStore, indexKey, indexValue string) []*model.VpcSubnetPort {
			if indexKey == servicecommon.TagScopeStatefulSetUID && indexValue == "live-uid" {
				return []*model.VpcSubnetPort{}
			}
			if indexKey == servicecommon.IndexKeyAllStsPorts {
				nsScope := servicecommon.TagScopeNamespace
				podScope := servicecommon.TagScopePodName
				stsScope := servicecommon.TagScopeStatefulSetUID
				return []*model.VpcSubnetPort{
					{Id: servicecommon.String("orph"),
						Tags: []model.Tag{
							{Scope: &stsScope, Tag: servicecommon.String("gone-sts-uid")},
							{Scope: &nsScope, Tag: servicecommon.String("ns")},
							{Scope: &podScope, Tag: servicecommon.String("pod-0")},
						}},
				}
			}
			return nil
		})
	patches.ApplyFunc(
		(*subnetportservice.SubnetPortService).DeleteSubnetPort,
		func(s *subnetportservice.SubnetPortService, port *model.VpcSubnetPort) error {
			deleteCalls++
			return nil
		})
	defer patches.Reset()

	require.NoError(t, r.CollectGarbage(context.Background()))
	assert.GreaterOrEqual(t, deleteCalls, 1)
}
