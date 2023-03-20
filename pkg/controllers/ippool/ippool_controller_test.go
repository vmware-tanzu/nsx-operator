/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ippool

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha2"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ippool"
)

func NewFakeIPPoolReconciler() *IPPoolReconciler {
	return &IPPoolReconciler{
		Client:  fake.NewClientBuilder().Build(),
		Scheme:  fake.NewClientBuilder().Build().Scheme(),
		Service: nil,
	}
}

func TestIPPoolController_setReadyStatusTrue(t *testing.T) {
	r := NewFakeIPPoolReconciler()
	ctx := context.TODO()
	dummyIPPool := &v1alpha2.IPPool{}

	// Case: Static Route CRD creation fails
	newConditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionTrue,
			Message: "NSX IPPool has been successfully created/updated",
			Reason:  "",
		},
	}
	r.setReadyStatusTrue(&ctx, dummyIPPool)

	if !reflect.DeepEqual(dummyIPPool.Status.Conditions, newConditions) {
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
func TestIPPoolReconciler_Reconcile(t *testing.T) {

	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)

	service := &ippool.IPPoolService{
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
	r := &IPPoolReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}
	ctx := context.Background()
	req := controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	// not found
	errNotFound := errors.New("not found")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	_, err := r.Reconcile(ctx, req)
	assert.Equal(t, err, errNotFound)

	// DeletionTimestamp.IsZero = ture, client update failed
	sp := &v1alpha2.IPPool{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil)
	err = errors.New("Update failed")
	k8sClient.EXPECT().Update(ctx, gomock.Any(), gomock.Any()).Return(err)
	fakewriter := fakeStatusWriter{}
	k8sClient.EXPECT().Status().Return(fakewriter)
	_, ret := r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	//  DeletionTimestamp.IsZero = false, Finalizers doesn't include util.FinalizerName
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha2.IPPool)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		return nil
	})

	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteIPPool", func(_ *ippool.IPPoolService, uid interface{}) error {
		assert.FailNow(t, "should not be called")
		return nil
	})

	k8sClient.EXPECT().Update(ctx, gomock.Any(), gomock.Any()).Return(nil)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = false, Finalizers include util.FinalizerName
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha2.IPPool)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		v1sp.Finalizers = []string{common.IPPoolFinalizerName}
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteIPPool", func(_ *ippool.IPPoolService, uid interface{}) error {
		return nil
	})
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = false, Finalizers include util.FinalizerName, DeleteIPPool fail
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha2.IPPool)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		v1sp.Finalizers = []string{common.IPPoolFinalizerName}
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteIPPool", func(_ *ippool.IPPoolService,
		uid interface{}) error {
		return errors.New("delete failed")
	})

	k8sClient.EXPECT().Status().Times(2).Return(fakewriter)
	_, ret = r.Reconcile(ctx, req)
	assert.NotEqual(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = true, Finalizers include util.FinalizerName, CreateorUpdateIPPool fail
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha2.IPPool)
		v1sp.ObjectMeta.DeletionTimestamp = nil
		v1sp.Finalizers = []string{common.IPPoolFinalizerName}
		return nil
	})

	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "CreateOrUpdateIPPool", func(_ *ippool.IPPoolService,
		obj *v1alpha2.IPPool) (bool, bool, error) {
		return false, false, errors.New("create failed")
	})
	_, ret = r.Reconcile(ctx, req)
	assert.NotEqual(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = true, Finalizers include util.FinalizerName, CreateorUpdateIPPool succ
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha2.IPPool)
		v1sp.ObjectMeta.DeletionTimestamp = nil
		v1sp.Finalizers = []string{common.IPPoolFinalizerName}
		return nil
	})

	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "CreateOrUpdateIPPool", func(_ *ippool.IPPoolService,
		obj *v1alpha2.IPPool) (bool, bool, error) {
		return false, false, nil
	})
	k8sClient.EXPECT().Status().Times(1).Return(fakewriter)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()
}

func TestReconciler_GarbageCollector(t *testing.T) {
	// gc collect item "2345", local store has more item than k8s cache
	service := &ippool.IPPoolService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "ListIPPoolID", func(_ *ippool.IPPoolService) sets.String {
		a := sets.NewString()
		a.Insert("1234")
		a.Insert("2345")
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteIPPool", func(_ *ippool.IPPoolService, UID interface{}) error {
		return nil
	})
	cancel := make(chan bool)
	defer patch.Reset()
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)

	r := &IPPoolReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}
	ctx := context.Background()
	policyList := &v1alpha2.IPPoolList{}
	k8sClient.EXPECT().List(gomock.Any(), policyList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha2.IPPoolList)
		a.Items = append(a.Items, v1alpha2.IPPool{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})
	go func() {
		time.Sleep(1 * time.Second)
		cancel <- true
	}()
	r.IPPoolGarbageCollector(cancel, time.Second)

	// local store has same item as k8s cache
	patch.Reset()
	patch.ApplyMethod(reflect.TypeOf(service), "ListIPPoolID", func(_ *ippool.IPPoolService) sets.String {
		a := sets.NewString()
		a.Insert("1234")
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteIPPool", func(_ *ippool.IPPoolService, UID interface{}) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(ctx, policyList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha2.IPPoolList)
		a.Items = append(a.Items, v1alpha2.IPPool{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})
	go func() {
		time.Sleep(1 * time.Second)
		cancel <- true
	}()
	r.IPPoolGarbageCollector(cancel, time.Second)

	// local store has no item
	patch.Reset()
	patch.ApplyMethod(reflect.TypeOf(service), "ListIPPoolID", func(_ *ippool.IPPoolService) sets.String {
		a := sets.NewString()
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteIPPool", func(_ *ippool.IPPoolService, UID interface{}) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(ctx, policyList).Return(nil).Times(0)
	go func() {
		time.Sleep(1 * time.Second)
		cancel <- true
	}()
	r.IPPoolGarbageCollector(cancel, time.Second)
}

func TestReconciler_Start(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &ippool.IPPoolService{}
	var mgr controllerruntime.Manager
	r := &IPPoolReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}
	err := r.Start(mgr)
	assert.NotEqual(t, err, nil)
}
