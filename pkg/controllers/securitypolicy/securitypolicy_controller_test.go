/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	controllerruntime "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
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
	newConditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
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
	dummyConditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX Security Policy could not be created/updated",
			Reason:  "Error occurred while processing the Security Policy CRD. Please check the config and try again",
		},
	}
	dummySP.Status.Conditions = dummyConditions

	newConditions = []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
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
	newConditions = []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
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
	newConditions = []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
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

func TestSecurityPolicyReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &services.SecurityPolicyService{
		NSXClient: &nsx.Client{},
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
	req := controllerruntime.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	// not found
	errNotFound := errors.New("not found")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	_, err := r.Reconcile(ctx, req)
	assert.Equal(t, err, errNotFound)

	// NSX version check failed case
	sp := &v1alpha1.SecurityPolicy{}
	checkNsxVersionPatch := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient), "NSXCheckVersionForSecurityPolicy", func(_ *nsx.Client) bool {
		return false
	})
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil)
	patches := gomonkey.ApplyFunc(updateFail,
		func(r *SecurityPolicyReconciler, c *context.Context, o *v1alpha1.SecurityPolicy, e *error) {
		})
	defer patches.Reset()
	result, ret := r.Reconcile(ctx, req)
	resultRequeueAfter5mins := controllerruntime.Result{Requeue: true, RequeueAfter: 5 * time.Minute}
	assert.Equal(t, nil, ret)
	assert.Equal(t, resultRequeueAfter5mins, result)

	checkNsxVersionPatch.Reset()
	checkNsxVersionPatch = gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient), "NSXCheckVersionForSecurityPolicy", func(_ *nsx.Client) bool {
		return true
	})
	defer checkNsxVersionPatch.Reset()

	// DeletionTimestamp.IsZero = ture, client update failed
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil)
	err = errors.New("Update failed")
	k8sClient.EXPECT().Update(ctx, gomock.Any()).Return(err)
	_, ret = r.Reconcile(ctx, req)
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
	var mgr controllerruntime.Manager
	r := &SecurityPolicyReconciler{
		Client:  k8sClient,
		Scheme:  nil,
		Service: service,
	}
	err := r.Start(mgr)
	assert.NotEqual(t, err, nil)
}

func TestReconcileSecurityPolicy(t *testing.T) {
	rule := v1alpha1.SecurityPolicyRule{
		Name: "rule-with-pod-selector",
		AppliedTo: []v1alpha1.SecurityPolicyTarget{
			{},
		},
		Sources: []v1alpha1.SecurityPolicyPeer{
			{
				PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"pod_selector_1": "pod_value_1"},
				},
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"ns1": "spA"},
				},
			},
		},
		Ports: []v1alpha1.SecurityPolicyPort{
			{
				Protocol: v1.ProtocolUDP,
				Port:     intstr.IntOrString{Type: intstr.String, StrVal: "named-port"},
			},
		},
	}
	spList := &v1alpha1.SecurityPolicyList{
		Items: []v1alpha1.SecurityPolicy{
			{
				Spec: v1alpha1.SecurityPolicySpec{
					Rules: []v1alpha1.SecurityPolicyRule{
						rule,
					},
				},
			},
		},
	}
	pods := []v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-1",
				Namespace: "spA",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name: "test-container-1",
						Ports: []v1.ContainerPort{
							{
								Name: "named-port",
							},
						},
					},
				},
			},
		},
	}

	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	ctx := context.Background()
	policyList := &v1alpha1.SecurityPolicyList{}
	k8sClient.EXPECT().List(ctx, policyList).Return(nil).Do(func(_ context.Context, list client.ObjectList,
		_ ...client.ListOption,
	) error {
		a := list.(*v1alpha1.SecurityPolicyList)
		a.Items = spList.Items
		return nil
	})

	mockQueue := mock_client.NewMockInterface(mockCtl)

	type args struct {
		client client.Client
		pods   []v1.Pod
		q      workqueue.RateLimitingInterface
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{k8sClient, pods, mockQueue}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, reconcileSecurityPolicy(tt.args.client, tt.args.pods, tt.args.q),
				fmt.Sprintf("reconcileSecurityPolicy(%v, %v, %v)", tt.args.client, tt.args.pods, tt.args.q))
		})
	}
}
