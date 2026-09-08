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
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
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
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
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

	// Case: With DNSHostName set
	dnsIPAddressAllocation := &v1alpha1.IPAddressAllocation{
		Spec: v1alpha1.IPAddressAllocationSpec{
			DNSHostName: "db.qe",
		},
	}
	setReadyStatusTrue(r.Client, ctx, dnsIPAddressAllocation, transitionTime)
	assert.Equal(t, 2, len(dnsIPAddressAllocation.Status.Conditions))
	assert.Equal(t, v1alpha1.ConditionType("DNSRecordReady"), dnsIPAddressAllocation.Status.Conditions[1].Type)
	assert.Equal(t, v1.ConditionTrue, dnsIPAddressAllocation.Status.Conditions[1].Status)

	// Case: With DNSHostName set but VPC DNS not configured (dnsActive == false)
	dnsDisabledIPAddressAllocation := &v1alpha1.IPAddressAllocation{
		Spec: v1alpha1.IPAddressAllocationSpec{
			DNSHostName: "db.qe",
		},
	}
	setReadyStatusTrue(r.Client, ctx, dnsDisabledIPAddressAllocation, transitionTime, false)
	assert.Equal(t, 2, len(dnsDisabledIPAddressAllocation.Status.Conditions))
	assert.Equal(t, v1alpha1.ConditionType("DNSRecordReady"), dnsDisabledIPAddressAllocation.Status.Conditions[1].Type)
	assert.Equal(t, v1.ConditionFalse, dnsDisabledIPAddressAllocation.Status.Conditions[1].Status)
	assert.Equal(t, "VPCDNSNotConfigured", dnsDisabledIPAddressAllocation.Status.Conditions[1].Reason)

	// Case: Clear DNSHostName from an IPAddressAllocation that previously had DNSRecordReady
	dnsIPAddressAllocation.Spec.DNSHostName = ""
	setReadyStatusTrue(r.Client, ctx, dnsIPAddressAllocation, transitionTime)
	assert.Equal(t, 1, len(dnsIPAddressAllocation.Status.Conditions))
	assert.Equal(t, v1alpha1.Ready, dnsIPAddressAllocation.Status.Conditions[0].Type)
	assert.Equal(t, v1.ConditionTrue, dnsIPAddressAllocation.Status.Conditions[0].Status)
}

func TestIPAddressAllocationController_updateIPAddressAllocationStatusConditions(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)
	ctx := context.TODO()

	transitionTime := metav1.Now()

	// Case 1: Matched condition found with no change
	dummyIPAddressAllocation1 := &v1alpha1.IPAddressAllocation{
		Status: v1alpha1.IPAddressAllocationStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:               v1alpha1.Ready,
					Status:             v1.ConditionTrue,
					Message:            "NSX IPAddressAllocation has been successfully created/updated",
					Reason:             "IPAddressAllocationReady",
					LastTransitionTime: transitionTime,
				},
			},
		},
	}
	newConditions1 := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX IPAddressAllocation has been successfully created/updated",
			Reason:             "IPAddressAllocationReady",
			LastTransitionTime: transitionTime,
		},
	}

	// EXPECT Status() should not be called because conditionsUpdated should be false
	k8sClient.EXPECT().Status().Times(0)
	updateIPAddressAllocationStatusConditions(k8sClient, ctx, dummyIPAddressAllocation1, newConditions1)

	// Case 2: Matched condition found with change
	dummyIPAddressAllocation2 := &v1alpha1.IPAddressAllocation{
		Status: v1alpha1.IPAddressAllocationStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:               v1alpha1.Ready,
					Status:             v1.ConditionTrue,
					Message:            "NSX IPAddressAllocation has been successfully created/updated",
					Reason:             "IPAddressAllocationReady",
					LastTransitionTime: transitionTime,
				},
			},
		},
	}
	newConditions2 := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionFalse,
			Message:            "NSX IPAddressAllocation failed",
			Reason:             "IPAddressAllocationNotReady",
			LastTransitionTime: transitionTime,
		},
	}

	fakewriter := fakeStatusWriter{}
	k8sClient.EXPECT().Status().Return(fakewriter).Times(1)
	updateIPAddressAllocationStatusConditions(k8sClient, ctx, dummyIPAddressAllocation2, newConditions2)

	assert.Equal(t, v1.ConditionFalse, dummyIPAddressAllocation2.Status.Conditions[0].Status)
	assert.Equal(t, "NSX IPAddressAllocation failed", dummyIPAddressAllocation2.Status.Conditions[0].Message)
	assert.Equal(t, "IPAddressAllocationNotReady", dummyIPAddressAllocation2.Status.Conditions[0].Reason)
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

func (writer fakeStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
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

	k8sClient.EXPECT().Status().Times(1).Return(fakewriter)
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
	res, ret := r.Reconcile(ctx, req)
	assert.Equal(t, res, resultRequeue)
	assert.NotEqual(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = true, CreateOrUpdateIPAddressAllocation success
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.IPAddressAllocation)
		v1sp.ObjectMeta.DeletionTimestamp = nil
		v1sp.Status.Conditions = []v1alpha1.Condition{
			{
				Type:    v1alpha1.Ready,
				Status:  v1.ConditionTrue,
				Reason:  "IPAddressAllocationReady",
				Message: "NSX IPAddressAllocation has been successfully created/updated",
			},
		}
		return nil
	})

	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "CreateOrUpdateIPAddressAllocation", func(_ *ipaddressallocation.IPAddressAllocationService,
		obj *v1alpha1.IPAddressAllocation) (bool, error) {
		return false, nil
	})
	k8sClient.EXPECT().Status().Times(0).Return(fakewriter)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()
	pat.Reset()
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
	service.SetIPAddressAllocationStore(ipaddressallocation.BuildIPAddressAllocationStore())

	r := &IPAddressAllocationReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}

	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListIPAddressAllocationID",
		func(_ *ipaddressallocation.IPAddressAllocationService) sets.Set[string] {
			return sets.New[string]("ipa-uid-1")
		})
	defer patches.Reset()
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
	patches.ApplyMethod(reflect.TypeOf(r), "Reconcile", func(_ *IPAddressAllocationReconciler, ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
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
	store := ipaddressallocation.BuildIPAddressAllocationStore()
	service.SetIPAddressAllocationStore(store)

	// Add items to store with tagScope common.TagScopeIPAddressAllocationCRUID
	tag1 := common.TagScopeIPAddressAllocationCRUID
	alloc1 := &model.VpcIpAddressAllocation{
		Id:         common.String("alloc1"),
		Path:       common.String("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc1"),
		ParentPath: common.String("/orgs/default/projects/p1/vpcs/v1"),
		Tags:       []model.Tag{{Scope: &tag1, Tag: common.String("1234")}},
	}
	alloc2 := &model.VpcIpAddressAllocation{
		Id:         common.String("alloc2"),
		Path:       common.String("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc2"),
		ParentPath: common.String("/orgs/default/projects/p1/vpcs/v1"),
		Tags:       []model.Tag{{Scope: &tag1, Tag: common.String("2345")}},
	}
	_ = store.Add(alloc1)
	_ = store.Add(alloc2)

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &IPAddressAllocationReconciler{
		Client:  k8sClient,
		Scheme:  scheme,
		Service: service,
	}

	deleted := make([]string, 0)
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteIPAddressAllocation", func(_ *ipaddressallocation.IPAddressAllocationService, UID interface{}) error {
		deleted = append(deleted, string(UID.(types.UID)))
		return nil
	})
	defer patch.Reset()

	ipa1 := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipa1",
			Namespace: "default",
			UID:       "1234",
		},
	}
	_ = k8sClient.Create(context.Background(), ipa1)
	err := r.CollectGarbage(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, []string{"2345"}, deleted)

	// local store has same item as k8s cache: remove alloc2
	deleted = nil
	_ = store.Delete(alloc2)
	err = r.CollectGarbage(context.Background())
	assert.NoError(t, err)
	assert.Empty(t, deleted)

	// local store has no item: remove alloc1
	_ = store.Delete(alloc1)
	err = r.CollectGarbage(context.Background())
	assert.NoError(t, err)
	assert.Empty(t, deleted)
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

func TestIPAddressAllocationReconciler_handleUpdate(t *testing.T) {
	r := &IPAddressAllocationReconciler{
		StatusUpdater: ctlcommon.StatusUpdater{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
		Service: &ipaddressallocation.IPAddressAllocationService{},
	}
	ctx := context.TODO()

	tests := []struct {
		name         string
		updated      bool
		updateErr    error
		initialConds []v1alpha1.Condition
		expectConds  bool
		expectErr    bool
	}{
		{
			name:         "update true, clear conditions",
			updated:      true,
			updateErr:    nil,
			initialConds: []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: v1.ConditionTrue}},
			expectConds:  false,
			expectErr:    false,
		},
		{
			name:         "update false, keep conditions",
			updated:      false,
			updateErr:    nil,
			initialConds: []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: v1.ConditionTrue}},
			expectConds:  true,
			expectErr:    false,
		},
		{
			name:         "update error",
			updated:      false,
			updateErr:    errors.New("update err"),
			initialConds: []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: v1.ConditionTrue}},
			expectConds:  true,
			expectErr:    true,
		},
		{
			name:         "DNS validation failure",
			updated:      false,
			updateErr:    nil,
			initialConds: nil,
			expectConds:  true,
			expectErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &v1alpha1.IPAddressAllocation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ipa",
					Namespace: "default",
				},
				Status: v1alpha1.IPAddressAllocationStatus{
					Conditions: tt.initialConds,
				},
			}
			if tt.name == "DNS validation failure" {
				obj.Spec.DNSHostName = "invalid..hostname"
			}

			patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateIPAddressAllocation",
				func(_ *ipaddressallocation.IPAddressAllocationService, _ *v1alpha1.IPAddressAllocation, _ bool) (bool, error) {
					return tt.updated, tt.updateErr
				})
			defer patches.Reset()

			patches2 := gomonkey.ApplyMethod(reflect.TypeOf(&r.StatusUpdater), "UpdateSuccess",
				func(_ *ctlcommon.StatusUpdater, _ context.Context, _ client.Object, _ ctlcommon.UpdateSuccessStatusFn, _ ...interface{}) {
				})
			defer patches2.Reset()

			patches3 := gomonkey.ApplyMethod(reflect.TypeOf(&r.StatusUpdater), "UpdateFail",
				func(_ *ctlcommon.StatusUpdater, _ context.Context, _ client.Object, _ error, _ string, _ ctlcommon.UpdateFailStatusFn, _ ...interface{}) {
				})
			defer patches3.Reset()

			patches4 := gomonkey.ApplyMethod(reflect.TypeOf(&r.StatusUpdater), "IncreaseUpdateTotal",
				func(_ *ctlcommon.StatusUpdater) {})
			defer patches4.Reset()

			_, err := r.handleUpdate(ctx, obj)
			if tt.expectErr {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}

			if tt.expectConds {
				assert.NotNil(t, obj.Status.Conditions)
			} else {
				assert.Nil(t, obj.Status.Conditions)
			}
		})
	}
}
