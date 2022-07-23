/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func NewFakeSecurityPolicyReconciler() *SecurityPolicyReconciler {
	return &SecurityPolicyReconciler{
		Client:  fake.NewClientBuilder().Build(),
		Scheme:  fake.NewClientBuilder().Build().Scheme(),
		Service: nil,
	}
}

func TestSecurityPolicyController_updateSecurityPolicyStatusConditions(t *testing.T) {
	r := NewFakeSecurityPolicyReconciler()
	ctx := context.TODO()
	dummySP := &v1alpha1.SecurityPolicy{}

	// Case: Security Policy CRD creation fails
	newConditions := []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionFalse,
			Message: "NSX Security Policy could not be created/updated",
			Reason:  "Error occurred while processing the Security Policy CRD. Please check the config and try again",
		},
	}
	r.updateSecurityPolicyStatusConditions(&ctx, dummySP, newConditions)

	if !reflect.DeepEqual(dummySP.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}

	// Case: No change in Conditions
	dummyConditions := []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionFalse,
			Message: "NSX Security Policy could not be created/updated",
			Reason:  "Error occurred while processing the Security Policy CRD. Please check the config and try again",
		},
	}
	dummySP.Status.Conditions = dummyConditions

	newConditions = []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionFalse,
			Message: "NSX Security Policy could not be created/updated",
			Reason:  "Error occurred while processing the Security Policy CRD. Please check the config and try again",
		},
	}

	r.updateSecurityPolicyStatusConditions(&ctx, dummySP, newConditions)

	if !reflect.DeepEqual(dummySP.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}

	// Case: SP CRD Creation succeeds after failure
	newConditions = []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionTrue,
			Message: "NSX Security Policy has been successfully created/updated",
			Reason:  "NSX API returned 200 response code for PATCH",
		},
	}

	r.updateSecurityPolicyStatusConditions(&ctx, dummySP, newConditions)

	if !reflect.DeepEqual(dummySP.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}

	// Case: SP CRD Update failed
	newConditions = []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionFalse,
			Message: "NSX Security Policy could not be created/updated",
			Reason:  "Error occurred while processing the Security Policy CRD. Please check the config and try again",
		},
	}

	r.updateSecurityPolicyStatusConditions(&ctx, dummySP, newConditions)

	if !reflect.DeepEqual(dummySP.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}
}

func TestSecurityPolicyController_isCRRequestedInSystemNamespace(t *testing.T) {
	r := NewFakeSecurityPolicyReconciler()
	ctx := context.TODO()
	dummyNs := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "dummy"}}
	r.Client.Create(ctx, dummyNs)
	req := &controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	isCRInSysNs, err := r.isCRRequestedInSystemNamespace(&ctx, req)
	if err != nil {
		t.Fatalf(err.Error())
	}
	if isCRInSysNs {
		t.Fatalf("Non-system namespace identied as a system namespace")
	}
	r.Client.Delete(ctx, dummyNs)

	sysNs := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "sys-ns",
			Namespace:   "sys-ns",
			Annotations: map[string]string{"vmware-system-shared-t1": "true"},
		},
	}
	r.Client.Create(ctx, sysNs)
	req = &ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "sys-ns", Name: "dummy"}}

	isCRInSysNs, err = r.isCRRequestedInSystemNamespace(&ctx, req)

	if err != nil {
		t.Fatalf(err.Error())
	}
	if !isCRInSysNs {
		t.Fatalf("System namespace not identied as a system namespace")
	}
	r.Client.Delete(ctx, sysNs)
}

func TestSecurityPolicyReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &services.SecurityPolicyService{
		NSXConfig: &config.NSXOperatorConfig{
			NsxConfig: &config.NsxConfig{
				EnforcementPoint: "vmc-enforcementpoint",
			},
		},
	}
	r := &SecurityPolicyReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	// not found
	errNotFound := errors.New("not found")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	_, err := r.Reconcile(ctx, req)
	assert.Equal(t, err, errNotFound)

	// DeletionTimestamp.IsZero = ture, client update failed
	sp := &v1alpha1.SecurityPolicy{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil)
	err = errors.New("Update failed")
	k8sClient.EXPECT().Update(ctx, gomock.Any()).Return(err)

	patches := gomonkey.ApplyFunc(updateFail,
		func(r *SecurityPolicyReconciler, c *context.Context, o *v1alpha1.SecurityPolicy, e *error) {
		})
	defer patches.Reset()
	_, ret := r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	//  DeletionTimestamp.IsZero = false, Finalizers doesn't include util.FinalizerName
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object) error {
		v1sp := obj.(*v1alpha1.SecurityPolicy)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		return nil
	})
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *services.SecurityPolicyService, UID types.UID) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = false, Finalizers include util.FinalizerName
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object) error {
		v1sp := obj.(*v1alpha1.SecurityPolicy)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		v1sp.Finalizers = []string{util.FinalizerName}
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *services.SecurityPolicyService, UID types.UID) error {
		return nil
	})
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()
}

func TestSecurityPolicyReconciler_GarbageCollector(t *testing.T) {
	// gc collect item "2345", local store has more item than k8s cache
	service := &services.SecurityPolicyService{
		NSXConfig: &config.NSXOperatorConfig{
			NsxConfig: &config.NsxConfig{
				EnforcementPoint: "vmc-enforcementpoint",
			},
		},
	}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "ListSecurityPolicyID", func(_ *services.SecurityPolicyService) sets.String {
		a := sets.NewString()
		a.Insert("1234")
		a.Insert("2345")
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *services.SecurityPolicyService, UID types.UID) error {
		assert.Equal(t, string(UID), "2345")
		return nil
	})
	cancel := make(chan bool)
	defer patch.Reset()
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)

	r := &SecurityPolicyReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}
	ctx := context.Background()
	policyList := &v1alpha1.SecurityPolicyList{}
	k8sClient.EXPECT().List(ctx, policyList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SecurityPolicyList)
		a.Items = append(a.Items, v1alpha1.SecurityPolicy{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})
	go func() {
		time.Sleep(1 * time.Second)
		cancel <- true
	}()
	r.GarbageCollector(cancel, time.Second)

	// local store has same item as k8s cache
	patch.Reset()
	patch.ApplyMethod(reflect.TypeOf(service), "ListSecurityPolicyID", func(_ *services.SecurityPolicyService) sets.String {
		a := sets.NewString()
		a.Insert("1234")
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *services.SecurityPolicyService, UID types.UID) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(gomock.Any(), policyList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SecurityPolicyList)
		a.Items = append(a.Items, v1alpha1.SecurityPolicy{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})
	go func() {
		time.Sleep(1 * time.Second)
		cancel <- true
	}()
	r.GarbageCollector(cancel, time.Second)

	// local store has no item
	patch.Reset()
	patch.ApplyMethod(reflect.TypeOf(service), "ListSecurityPolicyID", func(_ *services.SecurityPolicyService) sets.String {
		a := sets.NewString()
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *services.SecurityPolicyService, UID types.UID) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(ctx, policyList).Return(nil).Times(0)
	go func() {
		time.Sleep(1 * time.Second)
		cancel <- true
	}()
	r.GarbageCollector(cancel, time.Second)
}

func TestSecurityPolicyReconciler_Start(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &services.SecurityPolicyService{}
	var mgr ctrl.Manager
	r := &SecurityPolicyReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}
	err := r.Start(mgr)
	assert.NotEqual(t, err, nil)
}
