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

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/manager"

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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
)

func fakeService() *securitypolicy.SecurityPolicyService {
	c := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(c)
	rc, _ := cluster.NewRestConnector()

	service := &securitypolicy.SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{
				QueryClient:            nil,
				RestConnector:          rc,
				RealizedEntitiesClient: nil,
				ProjectInfraClient:     nil,
				NsxConfig: &config.NSXOperatorConfig{
					CoeConfig: &config.CoeConfig{
						Cluster: "k8scl-one:test",
					},
				},
			},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster:          "k8scl-one:test",
					EnableVPCNetwork: false,
				},
			},
		},
	}
	return service
}

func NewFakeSecurityPolicyReconciler() *SecurityPolicyReconciler {
	return &SecurityPolicyReconciler{
		Client:  fake.NewClientBuilder().Build(),
		Scheme:  fake.NewClientBuilder().Build().Scheme(),
		Service: fakeService(),
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

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func TestSecurityPolicyReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &securitypolicy.SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					EnableVPCNetwork: false,
				},
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	r := &SecurityPolicyReconciler{
		Client:   k8sClient,
		Scheme:   nil,
		Service:  service,
		Recorder: fakeRecorder{},
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
	checkNsxVersionPatch := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
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
	checkNsxVersionPatch = gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
		return true
	})
	defer checkNsxVersionPatch.Reset()

	// DeletionTimestamp.IsZero = ture, client update failed
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil)
	err = errors.New("Update failed")
	k8sClient.EXPECT().Update(ctx, gomock.Any(), gomock.Any()).Return(err)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, err, ret)

	//  DeletionTimestamp.IsZero = false, Finalizers doesn't include util.SecurityPolicyFinalizerName
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.SecurityPolicy)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		return nil
	})
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, UID interface{}, isVpcCleanup bool) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()

	//  DeletionTimestamp.IsZero = false, Finalizers include util.SecurityPolicyFinalizerName
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.SecurityPolicy)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		v1sp.Finalizers = []string{common.T1SecurityPolicyFinalizerName}
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, UID interface{}, isVpcCleanup bool) error {
		return nil
	})
	k8sClient.EXPECT().Update(ctx, gomock.Any(), gomock.Any()).Return(nil)
	_, ret = r.Reconcile(ctx, req)
	assert.Equal(t, ret, nil)
	patch.Reset()
}

func TestSecurityPolicyReconciler_GarbageCollector(t *testing.T) {
	// gc collect item "2345", local store has more item than k8s cache
	service := &securitypolicy.SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
				CoeConfig: &config.CoeConfig{
					EnableVPCNetwork: false,
				},
			},
		},
	}
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "ListSecurityPolicyID", func(_ *securitypolicy.SecurityPolicyService) sets.Set[string] {
		a := sets.New[string]()
		a.Insert("1234")
		a.Insert("2345")
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, UID interface{}, isVpcCleanup bool) error {
		return nil
	})
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
	k8sClient.EXPECT().List(gomock.Any(), policyList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SecurityPolicyList)
		a.Items = append(a.Items, v1alpha1.SecurityPolicy{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})
	r.CollectGarbage(ctx)

	// local store has same item as k8s cache
	patch.Reset()
	patch.ApplyMethod(reflect.TypeOf(service), "ListSecurityPolicyID", func(_ *securitypolicy.SecurityPolicyService) sets.Set[string] {
		a := sets.New[string]()
		a.Insert("1234")
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, UID interface{}, isVpcCleanup bool) error {
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
	r.CollectGarbage(ctx)

	// local store has no item
	patch.Reset()
	patch.ApplyMethod(reflect.TypeOf(service), "ListSecurityPolicyID", func(_ *securitypolicy.SecurityPolicyService) sets.Set[string] {
		a := sets.New[string]()
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, UID interface{}, isVpcCleanup bool) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(ctx, policyList).Return(nil).Times(0)
	r.CollectGarbage(ctx)
}

func TestSecurityPolicyReconciler_Start(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &securitypolicy.SecurityPolicyService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
				CoeConfig: &config.CoeConfig{
					EnableVPCNetwork: false,
				},
			},
		},
	}
	mgr, _ := controllerruntime.NewManager(&rest.Config{}, manager.Options{})
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

	r := NewFakeSecurityPolicyReconciler()
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
			tt.wantErr(t, reconcileSecurityPolicy(r, tt.args.client, tt.args.pods, tt.args.q),
				fmt.Sprintf("reconcileSecurityPolicy(%v, %v, %v)", tt.args.client, tt.args.pods, tt.args.q))
		})
	}
}
