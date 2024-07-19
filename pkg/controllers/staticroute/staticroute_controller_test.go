/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package staticroute

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/openlyinc/pointy"
	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	mocks "github.com/vmware-tanzu/nsx-operator/pkg/mock/staticrouteclient"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/staticroute"
)

func NewFakeStaticRouteReconciler() *StaticRouteReconciler {
	return &StaticRouteReconciler{
		Client:  fake.NewClientBuilder().Build(),
		Scheme:  fake.NewClientBuilder().Build().Scheme(),
		Service: nil,
	}
}

func TestStaticRouteController_updateStaticRouteStatusConditions(t *testing.T) {
	r := NewFakeStaticRouteReconciler()
	ctx := context.TODO()
	dummySR := &v1alpha1.StaticRoute{}

	// Case: Static Route CRD creation fails
	newConditions := []v1alpha1.StaticRouteCondition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX Static Route could not be created/updated",
			Reason:  "Error occurred while processing the Static Route CRD. Please check the config and try again",
		},
	}
	r.updateStaticRouteStatusConditions(&ctx, dummySR, newConditions)

	if !reflect.DeepEqual(dummySR.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}

	// Case: No change in Conditions
	dummyConditions := []v1alpha1.StaticRouteCondition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX Static Route could not be created/updated",
			Reason:  "Error occurred while processing the Static Route CRD. Please check the config and try again",
		},
	}
	dummySR.Status.Conditions = dummyConditions

	newConditions = []v1alpha1.StaticRouteCondition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX Static Route could not be created/updated",
			Reason:  "Error occurred while processing the Static Route CRD. Please check the config and try again",
		},
	}

	r.updateStaticRouteStatusConditions(&ctx, dummySR, newConditions)

	if !reflect.DeepEqual(dummySR.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}

	// Case: SP CRD Creation succeeds after failure
	newConditions = []v1alpha1.StaticRouteCondition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionTrue,
			Message: "NSX Static Route has been successfully created/updated",
			Reason:  "NSX API returned 200 response code for PATCH",
		},
	}

	r.updateStaticRouteStatusConditions(&ctx, dummySR, newConditions)

	if !reflect.DeepEqual(dummySR.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}

	// Case: SP CRD Update failed
	newConditions = []v1alpha1.StaticRouteCondition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX Static Route could not be created/updated",
			Reason:  "Error occurred while processing the Static Route CRD. Please check the config and try again",
		},
	}

	r.updateStaticRouteStatusConditions(&ctx, dummySR, newConditions)

	if !reflect.DeepEqual(dummySR.Status.Conditions, newConditions) {
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

func TestStaticRouteReconciler_Reconcile(t *testing.T) {

	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)

	mockCtrl := gomock.NewController(t)
	mockStaticRouteclient := mocks.NewMockStaticRoutesClient(mockCtrl)

	service := &staticroute.StaticRouteService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				StaticRouteClient: mockStaticRouteclient,
			},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	service.NSXConfig.CoeConfig = &config.CoeConfig{}
	service.NSXConfig.Cluster = "k8s_cluster"
	r := &StaticRouteReconciler{
		Client:   k8sClient,
		Scheme:   nil,
		Service:  service,
		Recorder: fakeRecorder{},
	}
	ctx := context.Background()
	req := controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	// common.GcOnce do nothing
	var once sync.Once
	pat := gomonkey.ApplyMethod(reflect.TypeOf(&once), "Do", func(_ *sync.Once, _ func()) {})
	defer pat.Reset()

	// not found
	errNotFound := errors.New("not found")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	_, err := r.Reconcile(ctx, req)
	assert.Equal(t, err, errNotFound)

	// DeletionTimestamp.IsZero = ture, client update failed
	sp := &v1alpha1.StaticRoute{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil)
	err = errors.New("Update failed")
	k8sClient.EXPECT().Update(ctx, gomock.Any(), gomock.Any()).Return(err)
	fakewriter := fakeStatusWriter{}
	k8sClient.EXPECT().Status().Return(fakewriter)
	_, ret := r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	//  DeletionTimestamp.IsZero = false, Finalizers doesn't include util.FinalizerName
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.StaticRoute)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		return nil
	})

	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteStaticRoute", func(_ *staticroute.StaticRouteService, obj *v1alpha1.StaticRoute) error {
		assert.FailNow(t, "should not be called")
		return nil
	})

	k8sClient.EXPECT().Update(ctx, gomock.Any(), gomock.Any()).Return(nil)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = false, Finalizers include util.FinalizerName
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.StaticRoute)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		v1sp.Finalizers = []string{common.StaticRouteFinalizerName}
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteStaticRoute", func(_ *staticroute.StaticRouteService, obj *v1alpha1.StaticRoute) error {
		return nil
	})
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = false, Finalizers include util.FinalizerName, DeleteStaticRoute fail
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.StaticRoute)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		v1sp.Finalizers = []string{common.StaticRouteFinalizerName}
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteStaticRoute", func(_ *staticroute.StaticRouteService, obj *v1alpha1.StaticRoute) error {
		return errors.New("delete failed")
	})

	k8sClient.EXPECT().Status().Times(2).Return(fakewriter)
	_, ret = r.Reconcile(ctx, req)
	assert.NotEqual(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = true, Finalizers include util.FinalizerName, CreateorUpdateStaticRoute fail
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.StaticRoute)
		v1sp.ObjectMeta.DeletionTimestamp = nil
		v1sp.Finalizers = []string{common.StaticRouteFinalizerName}
		return nil
	})

	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "CreateOrUpdateStaticRoute", func(_ *staticroute.StaticRouteService, namespace string, obj *v1alpha1.StaticRoute) error {
		return errors.New("create failed")
	})
	_, ret = r.Reconcile(ctx, req)
	assert.NotEqual(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = true, Finalizers include util.FinalizerName, CreateorUpdateStaticRoute succ
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.StaticRoute)
		v1sp.ObjectMeta.DeletionTimestamp = nil
		v1sp.Finalizers = []string{common.StaticRouteFinalizerName}
		return nil
	})

	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "CreateOrUpdateStaticRoute", func(_ *staticroute.StaticRouteService, namespace string, obj *v1alpha1.StaticRoute) error {
		return nil
	})
	k8sClient.EXPECT().Status().Times(1).Return(fakewriter)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()
}

func TestStaticRouteReconciler_GarbageCollector(t *testing.T) {
	// gc collect item "2345", local store has more item than k8s cache
	service := &staticroute.StaticRouteService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "ListStaticRoute", func(_ *staticroute.StaticRouteService) []model.StaticRoutes {
		a := []model.StaticRoutes{}
		id1 := "2345"
		path := "/orgs/org123/projects/pro123/vpcs/vpc123/static-routes/123"
		tag1 := []model.Tag{{Scope: pointy.String(common.TagScopeStaticRouteCRUID), Tag: pointy.String("2345")}}
		a = append(a, model.StaticRoutes{Id: &id1, Path: &path, Tags: tag1})
		id2 := "1234"
		tag2 := []model.Tag{{Scope: pointy.String(common.TagScopeStaticRouteCRUID), Tag: pointy.String("1234")}}
		a = append(a, model.StaticRoutes{Id: &id2, Path: &path, Tags: tag2})
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteStaticRouteByPath", func(_ *staticroute.StaticRouteService, orgId string, projectId string, vpcId string, uid string) error {
		return nil
	})
	defer patch.Reset()
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)

	r := &StaticRouteReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}
	ctx := context.Background()
	srList := &v1alpha1.StaticRouteList{}
	k8sClient.EXPECT().List(ctx, srList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.StaticRouteList)
		a.Items = append(a.Items, v1alpha1.StaticRoute{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})
	r.CollectGarbage(ctx)

	// local store has same item as k8s cache
	patch.Reset()
	patch.ApplyMethod(reflect.TypeOf(service), "ListStaticRoute", func(_ *staticroute.StaticRouteService) []model.StaticRoutes {
		a := []model.StaticRoutes{}
		id := "1234"
		tag2 := []model.Tag{{Scope: pointy.String(common.TagScopeStaticRouteCRUID), Tag: pointy.String(id)}}
		a = append(a, model.StaticRoutes{Id: &id, Tags: tag2})
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteStaticRoute", func(_ *staticroute.StaticRouteService, obj *v1alpha1.StaticRoute) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(ctx, srList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.StaticRouteList)
		a.Items = append(a.Items, v1alpha1.StaticRoute{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})
	r.CollectGarbage(ctx)

	// local store has no item
	patch.Reset()
	patch.ApplyMethod(reflect.TypeOf(service), "ListStaticRoute", func(_ *staticroute.StaticRouteService) []model.StaticRoutes {
		return []model.StaticRoutes{}
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteStaticRoute", func(_ *staticroute.StaticRouteService, obj *v1alpha1.StaticRoute) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(ctx, srList).Return(nil).Times(0)
	r.CollectGarbage(ctx)
}

func TestStaticRouteReconciler_Start(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &staticroute.StaticRouteService{}
	var mgr controllerruntime.Manager
	r := &StaticRouteReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}
	err := r.Start(mgr)
	assert.NotEqual(t, err, nil)
}
