/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctlcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipaddressallocation"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func NewFakeIPAddressAllocationReconciler() *IPAddressAllocationReconciler {
	return &IPAddressAllocationReconciler{
		Client:  fake.NewClientBuilder().Build(),
		Scheme:  fake.NewClientBuilder().Build().Scheme(),
		Service: nil,
	}
}

func TestIPAddressAllocationController_setReadyStatusTrue(t *testing.T) {
	r := NewFakeIPAddressAllocationReconciler()
	ctx := context.TODO()
	dummyIPAddressAllocation := &v1alpha1.IPAddressAllocation{}
	transitionTime := metav1.Now()

	// Case: Static Route CRD creation fails
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX IPAddressAllocation has been successfully created/updated",
			Reason:             "IPAddressAllocationReady",
			LastTransitionTime: transitionTime,
		},
	}
	setReadyStatusTrue(r.Client, ctx, dummyIPAddressAllocation, transitionTime)

	if !reflect.DeepEqual(dummyIPAddressAllocation.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}
}

type fakeStatusWriter struct {
}

func (writer fakeStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}
func (writer fakeStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return nil
}
func (writer fakeStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return nil
}

type fakeRecorder struct {
}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}
func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}
func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func TestIPAddressAllocationReconciler_Reconcile(t *testing.T) {

	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)

	service := &ipaddressallocation.IPAddressAllocationService{
		Service: common.Service{
			NSXClient: &nsx.Client{},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	service.NSXConfig.CoeConfig = &config.CoeConfig{}
	service.NSXConfig.Cluster = "k8s_cluster"
	r := &IPAddressAllocationReconciler{
		Client:   k8sClient,
		Scheme:   nil,
		Service:  service,
		Recorder: fakeRecorder{},
	}
	r.StatusUpdater = ctlcommon.NewStatusUpdater(r.Client, r.Service.NSXConfig, r.Recorder, MetricResType, "IPAddressAllocation", "IPAddressAllocation")

	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	// common.GcOnce do nothing
	var once sync.Once
	pat := gomonkey.ApplyMethod(reflect.TypeOf(&once), "Do", func(_ *sync.Once, _ func()) {})
	defer pat.Reset()

	// not found
	errNotFound := errors.New("not found")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	_, err := r.Reconcile(ctx, req)
	assert.Equal(t, err, errNotFound)

	sp := &v1alpha1.IPAddressAllocation{}
	fakewriter := fakeStatusWriter{}

	//  DeletionTimestamp.IsZero = false, DeleteIPAddressAllocation success
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.IPAddressAllocation)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		return nil
	})
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteIPAddressAllocation",
		func(_ *ipaddressallocation.IPAddressAllocationService, uid interface{}) error {
			return nil
		})
	_, ret := r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = false, DeleteIPAddressAllocation fail
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.IPAddressAllocation)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteIPAddressAllocation", func(_ *ipaddressallocation.IPAddressAllocationService,
		uid interface{}) error {
		return errors.New("delete failed")
	})
	patch.ApplyPrivateMethod(reflect.TypeOf(r), "setIPAddressBlockVisibilityDefaultValue", func(_ *IPAddressAllocationReconciler,
		obj *v1alpha1.IPAddressAllocation) error {
		return nil
	})

	k8sClient.EXPECT().Status().Times(2).Return(fakewriter)
	_, ret = r.Reconcile(ctx, req)
	assert.NotEqual(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = true, CreateOrUpdateIPAddressAllocation fail
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.IPAddressAllocation)
		v1sp.ObjectMeta.DeletionTimestamp = nil
		return nil
	})

	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "CreateOrUpdateIPAddressAllocation", func(_ *ipaddressallocation.IPAddressAllocationService,
		obj *v1alpha1.IPAddressAllocation) (bool, error) {
		return false, errors.New("create failed")
	})
	patch.ApplyPrivateMethod(reflect.TypeOf(r), "setIPAddressBlockVisibilityDefaultValue", func(_ *IPAddressAllocationReconciler,
		obj *v1alpha1.IPAddressAllocation) error {
		return nil
	})
	res, ret := r.Reconcile(ctx, req)
	assert.Equal(t, res, resultRequeue)
	assert.NotEqual(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = true, CreateOrUpdateIPAddressAllocation success
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.IPAddressAllocation)
		v1sp.ObjectMeta.DeletionTimestamp = nil
		return nil
	})

	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "CreateOrUpdateIPAddressAllocation", func(_ *ipaddressallocation.IPAddressAllocationService,
		obj *v1alpha1.IPAddressAllocation) (bool, error) {
		return true, nil
	})
	patch.ApplyPrivateMethod(reflect.TypeOf(r), "setIPAddressBlockVisibilityDefaultValue", func(_ *IPAddressAllocationReconciler,
		obj *v1alpha1.IPAddressAllocation) error {
		return nil
	})
	k8sClient.EXPECT().Status().Times(3).Return(fakewriter)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()
}

func TestIPAddressAllocationReconciler_RestoreReconcile(t *testing.T) {

	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()

	service := &ipaddressallocation.IPAddressAllocationService{
		Service: common.Service{
			NSXClient: &nsx.Client{},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}

	r := &IPAddressAllocationReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(service), "ListIPAddressAllocationID",
		func(_ *ipaddressallocation.IPAddressAllocationService) sets.Set[string] {
			return sets.New[string]("ipa-uid-1")
		})
	// defer patches.Reset()
	ipAddressAllocationList := &v1alpha1.IPAddressAllocationList{}
	k8sClient.EXPECT().List(gomock.Any(), ipAddressAllocationList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.IPAddressAllocationList)
		a.Items = []v1alpha1.IPAddressAllocation{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ipa-1",
					Namespace: "ns-1",
					UID:       "ipa-uid-1",
				},
				Status: v1alpha1.IPAddressAllocationStatus{
					AllocationIPs: "1.2.3.4/28",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ipa-2",
					Namespace: "ns-1",
					UID:       "ipa-uid-2",
				},
				Status: v1alpha1.IPAddressAllocationStatus{
					AllocationIPs: "5.6.7.8",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ipa-3",
					Namespace: "ns-1",
					UID:       "ipa-uid-2",
				},
			},
		}
		return nil
	})
	patches.ApplyFunc((*IPAddressAllocationReconciler).Reconcile, func(r *IPAddressAllocationReconciler, ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		assert.Equal(t, "ipa-2", req.Name)
		assert.Equal(t, "ns-1", req.Namespace)
		return ctlcommon.ResultNormal, nil
	})
	err := r.RestoreReconcile()
	assert.Nil(t, err)

}

func TestReconciler_GarbageCollector(t *testing.T) {
	// gc collect item "2345", local store has more item than k8s cache
	service := &ipaddressallocation.IPAddressAllocationService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "ListIPAddressAllocationID",
		func(_ *ipaddressallocation.IPAddressAllocationService) sets.Set[string] {
			a := sets.New[string]()
			a.Insert("1234")
			a.Insert("2345")
			return a
		})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteIPAddressAllocation", func(_ *ipaddressallocation.IPAddressAllocationService, UID interface{}) error {
		return nil
	})
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)

	r := &IPAddressAllocationReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}
	ctx := context.Background()
	ipAddressAllocationList := &v1alpha1.IPAddressAllocationList{}
	k8sClient.EXPECT().List(gomock.Any(), ipAddressAllocationList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.IPAddressAllocationList)
		a.Items = append(a.Items, v1alpha1.IPAddressAllocation{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})
	r.CollectGarbage(context.Background())

	// local store has same item as k8s cache
	patch.Reset()

	patch.ApplyMethod(reflect.TypeOf(service), "ListIPAddressAllocationID", func(_ *ipaddressallocation.IPAddressAllocationService) sets.Set[string] {
		a := sets.New[string]()
		a.Insert("1234")
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteIPAddressAllocation", func(_ *ipaddressallocation.IPAddressAllocationService, UID interface{}) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(ctx, ipAddressAllocationList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.IPAddressAllocationList)
		a.Items = append(a.Items, v1alpha1.IPAddressAllocation{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})
	r.CollectGarbage(context.Background())

	// local store has no item
	patch.Reset()

	patch.ApplyMethod(reflect.TypeOf(service), "ListIPAddressAllocationID", func(_ *ipaddressallocation.IPAddressAllocationService) sets.Set[string] {
		a := sets.New[string]()
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteIPAddressAllocation", func(_ *ipaddressallocation.IPAddressAllocationService, UID interface{}) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(ctx, ipAddressAllocationList).Return(nil).Times(0)
	r.CollectGarbage(context.Background())

	patch.Reset()
}

func TestIPAddressAllocationReconciler_StartController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	vpcService := &vpc.VPCService{
		Service: common.Service{
			Client: fakeClient,
		},
	}
	ipAddressAllocationService := &ipaddressallocation.IPAddressAllocationService{
		Service: common.Service{
			Client: fakeClient,
		},
	}
	mockMgr := &MockManager{scheme: runtime.NewScheme()}
	patches := gomonkey.ApplyFunc((*IPAddressAllocationReconciler).setupWithManager, func(r *IPAddressAllocationReconciler, mgr manager.Manager) error {
		return nil
	})
	patches.ApplyFunc(ctlcommon.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
		return
	})
	defer patches.Reset()
	r := NewIPAddressAllocationReconciler(mockMgr, ipAddressAllocationService, vpcService)
	err := r.StartController(mockMgr, nil)
	assert.Nil(t, err)
}

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
