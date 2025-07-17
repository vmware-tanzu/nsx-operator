/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/openlyinc/pointy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	crdv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctrcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

func fakeService() *securitypolicy.SecurityPolicyService {
	c := nsx.NewConfig("localhost", "1", "1", []string{}, 10, 3, 20, 20, true, true, true, ratelimiter.AIMD, nil, nil, []string{})
	cluster, _ := nsx.NewCluster(c)
	rc := cluster.NewRestConnector()

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
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
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
	updateSecurityPolicyStatusConditions(r.Client, ctx, dummySP, newConditions, r.Service)

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

	updateSecurityPolicyStatusConditions(r.Client, ctx, dummySP, newConditions, r.Service)

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

	updateSecurityPolicyStatusConditions(r.Client, ctx, dummySP, newConditions, r.Service)

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

	updateSecurityPolicyStatusConditions(r.Client, ctx, dummySP, newConditions, r.Service)

	if !reflect.DeepEqual(dummySP.Status.Conditions, newConditions) {
		t.Fatalf("Failed to correctly update Status Conditions when conditions haven't changed")
	}
}

type fakeStatusWriter struct{}

func (writer fakeStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}

func (writer fakeStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return nil
}

func (writer fakeStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return nil
}

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func Test_setSecurityPolicyErrorAnnotation(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	ctx := context.TODO()
	info := ctrcommon.ErrorNoDFWLicense

	// Test case with isVPCEnabled = false
	isVPCEnabled := false
	securityPolicy := &v1alpha1.SecurityPolicy{}
	securityPolicy.Annotations = make(map[string]string)
	k8sClient.EXPECT().
		Update(ctx, gomock.AssignableToTypeOf(&v1alpha1.SecurityPolicy{})).
		Return(nil)
	// Call the function under test
	setSecurityPolicyErrorAnnotation(ctx, securityPolicy, isVPCEnabled, k8sClient, info)
	// Assert that the annotation was set correctly
	require.NotNil(t, securityPolicy.Annotations)
	assert.Equal(t, info, securityPolicy.Annotations[ctrcommon.NSXOperatorError])

	// Test case with isVPCEnabled = true
	isVPCEnabled = true
	k8sClient.EXPECT().
		Update(ctx, gomock.AssignableToTypeOf(&crdv1alpha1.SecurityPolicy{})).
		Return(nil).AnyTimes()
	// Call the function under test again with isVPCEnabled = true
	setSecurityPolicyErrorAnnotation(ctx, securityPolicy, isVPCEnabled, k8sClient, info)
	// Assert that the annotation remains the same
	assert.Equal(t, info, securityPolicy.Annotations[ctrcommon.NSXOperatorError])
}

func Test_cleanSecurityPolicyErrorAnnotation(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	ctx := context.TODO()
	info := ctrcommon.ErrorNoDFWLicense

	// Define a SecurityPolicy with an annotation
	securityPolicy := &v1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ctrcommon.NSXOperatorError: info,
			},
		},
	}

	// Expected updated SecurityPolicy after annotation removal
	expectedSecurityPolicy := &v1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{}, // Annotation should be removed
		},
	}

	// Test case with isVPCEnabled = false
	isVPCEnabled := false
	k8sClient.EXPECT().Update(ctx, expectedSecurityPolicy).Return(nil).Times(1)
	// Run the function
	cleanSecurityPolicyErrorAnnotation(ctx, securityPolicy, isVPCEnabled, k8sClient)
	// Assertions annotation removed
	assert.NotContains(t, securityPolicy.Annotations, ctrcommon.NSXOperatorError)

	// Test case with isVPCEnabled = true
	isVPCEnabled = true
	k8sClient.EXPECT().
		Update(ctx, gomock.AssignableToTypeOf(&crdv1alpha1.SecurityPolicy{})).
		Return(nil).AnyTimes()
	securityPolicy.Annotations[ctrcommon.NSXOperatorError] = info
	// Assert that the annotation was set correctly
	require.NotNil(t, securityPolicy.Annotations)
	// Call the function under test again with isVPCEnabled = true
	cleanSecurityPolicyErrorAnnotation(ctx, securityPolicy, isVPCEnabled, k8sClient)
	// Assertions annotation removed
	assert.NotContains(t, securityPolicy.Annotations, ctrcommon.NSXOperatorError)
}

func TestSecurityPolicyReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
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
		Client:        k8sClient,
		Scheme:        nil,
		Service:       service,
		Recorder:      fakeRecorder{},
		StatusUpdater: ctrcommon.NewStatusUpdater(k8sClient, service.NSXConfig, fakeRecorder{}, MetricResTypeSecurityPolicy, "SecurityPolicy", "SecurityPolicy"),
	}
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	// fail to get CR
	errFailToGet := errors.New("failed to get CR")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errFailToGet)
	result, retErr := r.Reconcile(ctx, req)
	assert.Equal(t, retErr, errFailToGet)
	assert.Equal(t, ResultRequeue, result)

	// not found and deletion success
	errNotFound := apierrors.NewNotFound(v1alpha1.Resource("SecurityPolicy"), "")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	deleteSecurityPolicyByNamePatch := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "deleteSecurityPolicyByName", func(_ *SecurityPolicyReconciler, name, ns string) error {
		return nil
	})
	defer deleteSecurityPolicyByNamePatch.Reset()
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, retErr, nil)
	assert.Equal(t, ResultNormal, result)

	// NSX version check failed case
	sp := &v1alpha1.SecurityPolicy{}
	fakewriter := fakeStatusWriter{}
	checkNsxVersionPatch := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
		return false
	})
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil)
	k8sClient.EXPECT().Status().Times(1).Return(fakewriter)
	result, retErr = r.Reconcile(ctx, req)
	resultRequeueAfter5mins := ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Minute}
	assert.Equal(t, retErr, nil)
	assert.Equal(t, resultRequeueAfter5mins, result)
	checkNsxVersionPatch.Reset()
	checkNsxVersionPatch = gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
		return true
	})
	defer checkNsxVersionPatch.Reset()

	// DeletionTimestamp.IsZero = ture, create security policy in SystemNamespace
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil)
	err := errors.New("fetch namespace associated with security policy CR failed")
	IsSystemNamespacePatch := gomonkey.ApplyFunc(util.IsSystemNamespace, func(client client.Client, ns string, obj *v1.Namespace,
	) (bool, error) {
		return true, errors.New("fetch namespace associated with security policy CR failed")
	})
	k8sClient.EXPECT().Status().Times(1).Return(fakewriter)
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, retErr, err)
	assert.Equal(t, ResultRequeue, result)
	IsSystemNamespacePatch.Reset()

	// DeletionTimestamp.IsZero = ture, create security policy fail
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil)
	IsSystemNamespacePatch = gomonkey.ApplyFunc(util.IsSystemNamespace, func(client client.Client, ns string, obj *v1.Namespace,
	) (bool, error) {
		return false, nil
	})
	err = errors.New("create or update security policy failed")
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "CreateOrUpdateSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}) error {
		return errors.New("create or update security policy failed")
	})
	k8sClient.EXPECT().Status().Times(1).Return(fakewriter)
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, retErr, err)
	assert.Equal(t, ResultRequeue, result)
	patch.Reset()

	// DeletionTimestamp.IsZero = true, Finalizers include util.SecurityPolicyFinalizerName and update success
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil)
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "CreateOrUpdateSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}) error {
		return nil
	})
	k8sClient.EXPECT().Status().Times(1).Return(fakewriter)
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, retErr, nil)
	assert.Equal(t, ResultNormal, result)
	IsSystemNamespacePatch.Reset()
	patch.Reset()

	// DeletionTimestamp.IsZero = false, Finalizers include util.SecurityPolicyFinalizerName and update fails
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.SecurityPolicy)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		v1sp.Finalizers = []string{common.T1SecurityPolicyFinalizerName}
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}, isGc bool, createdFor string) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	err = errors.New("finalizer remove failed, would retry exponentially")
	k8sClient.EXPECT().Update(ctx, gomock.Any()).Return(err)
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, retErr, err)
	assert.Equal(t, ResultRequeue, result)
	patch.Reset()

	// DeletionTimestamp.IsZero = false, Finalizers doesn't include util.SecurityPolicyFinalizerName
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.SecurityPolicy)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}, isGc bool, createdFor string) error {
		return nil
	})
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, retErr, nil)
	assert.Equal(t, ResultNormal, result)
	patch.Reset()

	// DeletionTimestamp.IsZero = false, Finalizers include util.SecurityPolicyFinalizerName and delete fail
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.SecurityPolicy)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		v1sp.Finalizers = []string{common.T1SecurityPolicyFinalizerName}
		return nil
	})
	err = errors.New("delete security policy failed")
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, UID interface{}, isGc bool, createdFor string) error {
		return errors.New("delete security policy failed")
	})
	k8sClient.EXPECT().Update(ctx, gomock.Any(), gomock.Any()).Return(nil)
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, retErr, err)
	assert.Equal(t, ResultRequeue, result)
	patch.Reset()

	// DeletionTimestamp.IsZero = false, Finalizers include util.SecurityPolicyFinalizerName and delete success
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, option ...client.GetOption) error {
		v1sp := obj.(*v1alpha1.SecurityPolicy)
		time := metav1.Now()
		v1sp.ObjectMeta.DeletionTimestamp = &time
		v1sp.Finalizers = []string{common.T1SecurityPolicyFinalizerName}
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}, isGc bool, createdFor string) error {
		return nil
	})
	k8sClient.EXPECT().Update(ctx, gomock.Any(), gomock.Any()).Return(nil)
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, retErr, nil)
	assert.Equal(t, ResultNormal, result)
	patch.Reset()
}

func TestSecurityPolicyReconciler_GarbageCollector(t *testing.T) {
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
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)
	r := &SecurityPolicyReconciler{
		Client:        k8sClient,
		Scheme:        nil,
		Service:       service,
		StatusUpdater: ctrcommon.NewStatusUpdater(k8sClient, service.NSXConfig, fakeRecorder{}, MetricResTypeSecurityPolicy, "SecurityPolicy", "SecurityPolicy"),
	}
	ctx := context.Background()
	policyList := &v1alpha1.SecurityPolicyList{}

	// gc collect item "2345", local store has more item than k8s cache
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}, isGc bool, createdFor string) error {
		return nil
	})
	patch.ApplyMethod(reflect.TypeOf(service), "ListSecurityPolicyID", func(_ *securitypolicy.SecurityPolicyService) sets.Set[string] {
		a := sets.New[string]()
		a.Insert("1234")
		a.Insert("2345")
		return a
	})
	k8sClient.EXPECT().List(ctx, policyList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		a := list.(*v1alpha1.SecurityPolicyList)
		a.Items = append(a.Items, v1alpha1.SecurityPolicy{})
		a.Items[0].ObjectMeta = metav1.ObjectMeta{}
		a.Items[0].UID = "1234"
		return nil
	})
	r.CollectGarbage(ctx)

	// local store has same item as k8s cache
	patch.Reset()
	patch = gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListSecurityPolicyID", func(_ *securitypolicy.SecurityPolicyService) sets.Set[string] {
		a := sets.New[string]()
		a.Insert("1234")
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(r.Service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}, isGc bool, createdFor string) error {
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
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "ListSecurityPolicyID", func(_ *securitypolicy.SecurityPolicyService) sets.Set[string] {
		a := sets.New[string]()
		return a
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj types.UID, isGc bool, createdFor string) error {
		assert.FailNow(t, "should not be called")
		return nil
	})
	k8sClient.EXPECT().List(ctx, policyList).Return(nil).Times(0)
	r.CollectGarbage(ctx)
	patch.Reset()
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
	defer mockCtl.Finish()
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
		q      workqueue.TypedRateLimitingInterface[reconcile.Request]
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

func TestSecurityPolicyReconciler_listSecurityPolciyCRIDsForVPC(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &securitypolicy.SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					EnableVPCNetwork: true,
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

	// list returns an error
	errList := errors.New("list error")
	k8sClient.EXPECT().List(ctx, gomock.Any()).Return(errList)
	_, err := r.listSecurityPolicyCRIDs()
	assert.Equal(t, err, errList)

	// list returns no error, but no items
	k8sClient.EXPECT().List(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		networkPolicyList := list.(*crdv1alpha1.SecurityPolicyList)
		networkPolicyList.Items = []crdv1alpha1.SecurityPolicy{}
		return nil
	})
	crIDs, err := r.listSecurityPolicyCRIDs()
	assert.NoError(t, err)
	assert.Equal(t, 0, crIDs.Len())

	// list returns items
	k8sClient.EXPECT().List(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		networkPolicyList := list.(*crdv1alpha1.SecurityPolicyList)
		networkPolicyList.Items = []crdv1alpha1.SecurityPolicy{
			{ObjectMeta: metav1.ObjectMeta{UID: "uid1"}},
			{ObjectMeta: metav1.ObjectMeta{UID: "uid2"}},
		}
		return nil
	})
	crIDs, err = r.listSecurityPolicyCRIDs()
	assert.NoError(t, err)
	assert.Equal(t, 2, crIDs.Len())
	assert.True(t, crIDs.Has("uid1"))
	assert.True(t, crIDs.Has("uid2"))
}

func TestSecurityPolicyReconciler_deleteSecuritypolicyByName(t *testing.T) {
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
		Scheme:   nil,
		Service:  service,
		Recorder: fakeRecorder{},
	}

	// deletion fails
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "ListSecurityPolicyByName", func(_ *securitypolicy.SecurityPolicyService, _ string, _ string) []*model.SecurityPolicy {
		return []*model.SecurityPolicy{
			{
				Id:   pointy.String("sp-id-1"),
				Tags: []model.Tag{{Scope: pointy.String(common.TagValueScopeSecurityPolicyUID), Tag: pointy.String("uid1")}},
			},
			{
				Id:   pointy.String("sp-id-2"),
				Tags: []model.Tag{{Scope: pointy.String(common.TagValueScopeSecurityPolicyUID), Tag: pointy.String("uid2")}},
			},
		}
	})

	patch.ApplyMethod(reflect.TypeOf(service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj types.UID, isGc bool, createdFor string) error {
		if obj == "uid2" {
			return errors.New("delete failed")
		}
		return nil
	})

	err := r.deleteSecurityPolicyByName("dummy-ns", "dummy-name")
	assert.Error(t, err)
	patch.Reset()
}

func TestSecurityPolicyReconcilerForVPC_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	k8sClient := mock_client.NewMockClient(mockCtl)
	service := &securitypolicy.SecurityPolicyService{
		Service: common.Service{
			NSXClient: &nsx.Client{},
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					EnableVPCNetwork: true,
				},
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	r := &SecurityPolicyReconciler{
		Client:        k8sClient,
		Scheme:        nil,
		Service:       service,
		Recorder:      fakeRecorder{},
		StatusUpdater: ctrcommon.NewStatusUpdater(k8sClient, service.NSXConfig, fakeRecorder{}, MetricResTypeSecurityPolicy, "SecurityPolicy", "SecurityPolicy"),
	}
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	// fail to get CR
	errFailToGet := errors.New("failed to get CR")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errFailToGet)
	result, retErr := r.Reconcile(ctx, req)
	assert.Equal(t, retErr, errFailToGet)
	assert.Equal(t, ResultRequeue, result)

	// not found and deletion failed
	errNotFound := apierrors.NewNotFound(crdv1alpha1.Resource("SecurityPolicy"), "")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	err := errors.New("delete security policy failed")
	deleteSecurityPolicyByNamePatch := gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "deleteSecurityPolicyByName", func(_ *SecurityPolicyReconciler, name, ns string) error {
		return errors.New("delete security policy failed")
	})
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, retErr, err)
	assert.Equal(t, ResultRequeue, result)

	// not found and deletion success
	deleteSecurityPolicyByNamePatch.Reset()
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	deleteSecurityPolicyByNamePatch = gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "deleteSecurityPolicyByName", func(_ *SecurityPolicyReconciler, name, ns string) error {
		return nil
	})
	defer deleteSecurityPolicyByNamePatch.Reset()
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, retErr, nil)
	assert.Equal(t, ResultNormal, result)

	// NSX version check failed case
	sp := &crdv1alpha1.SecurityPolicy{}
	fakewriter := fakeStatusWriter{}
	checkNsxVersionPatch := gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
		return false
	})
	k8sClient.EXPECT().Get(ctx, gomock.Any(), sp).Return(nil)
	k8sClient.EXPECT().Status().Times(1).Return(fakewriter)
	result, retErr = r.Reconcile(ctx, req)
	resultRequeueAfter5mins := ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Minute}
	assert.Equal(t, retErr, nil)
	assert.Equal(t, resultRequeueAfter5mins, result)
	checkNsxVersionPatch.Reset()
	checkNsxVersionPatch = gomonkey.ApplyMethod(reflect.TypeOf(service.NSXClient), "NSXCheckVersion", func(_ *nsx.Client, feature int) bool {
		return true
	})
	defer checkNsxVersionPatch.Reset()
}

func TestReconcileSecurityPolicyForVPC(t *testing.T) {
	rule := crdv1alpha1.SecurityPolicyRule{
		Name: "rule-with-pod-selector",
		AppliedTo: []crdv1alpha1.SecurityPolicyTarget{
			{},
		},
		Sources: []crdv1alpha1.SecurityPolicyPeer{
			{
				PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"pod_selector_1": "pod_value_1"},
				},
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"ns1": "spA"},
				},
			},
		},
		Ports: []crdv1alpha1.SecurityPolicyPort{
			{
				Protocol: v1.ProtocolUDP,
				Port:     intstr.IntOrString{Type: intstr.String, StrVal: "named-port"},
			},
		},
	}
	spList := &crdv1alpha1.SecurityPolicyList{
		Items: []crdv1alpha1.SecurityPolicy{
			{
				Spec: crdv1alpha1.SecurityPolicySpec{
					Rules: []crdv1alpha1.SecurityPolicyRule{
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
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)
	ctx := context.Background()
	policyList := &crdv1alpha1.SecurityPolicyList{}
	k8sClient.EXPECT().List(ctx, policyList).Return(nil).Do(func(_ context.Context, list client.ObjectList,
		_ ...client.ListOption,
	) error {
		a := list.(*crdv1alpha1.SecurityPolicyList)
		a.Items = spList.Items
		return nil
	})

	r := NewFakeSecurityPolicyReconciler()
	// Enable VPC network
	r.Service.NSXConfig.EnableVPCNetwork = true
	mockQueue := mock_client.NewMockInterface(mockCtl)

	type args struct {
		client client.Client
		pods   []v1.Pod
		q      workqueue.TypedRateLimitingInterface[reconcile.Request]
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

func TestStartSecurityPolicyController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	vpcService := &vpc.VPCService{
		Service: common.Service{
			Client: fakeClient,
		},
	}
	commonService := common.Service{
		Client: fakeClient,
	}
	mgr, _ := ctrl.NewManager(&rest.Config{}, manager.Options{})

	testCases := []struct {
		name         string
		expectErrStr string
		patches      func() *gomonkey.Patches
	}{
		// expected no error when starting the SecurityPolicy controller
		{
			name: "Start SecurityPolicy Controller",
			patches: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(ctrcommon.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
					return
				})
				patches.ApplyFunc(os.Exit, func(code int) {
					assert.FailNow(t, "os.Exit should not be called")
					return
				})
				patches.ApplyFunc(securitypolicy.GetSecurityService, func(service common.Service, vpcService common.VPCServiceProvider) *securitypolicy.SecurityPolicyService {
					return fakeService()
				})
				patches.ApplyMethod(reflect.TypeOf(&SecurityPolicyReconciler{}), "Start", func(_ *SecurityPolicyReconciler, r ctrl.Manager) error {
					return nil
				})
				return patches
			},
		},
		{
			name:         "Start SecurityPolicy controller return error",
			expectErrStr: "failed to setupWithManager",
			patches: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(ctrcommon.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
					return
				})
				patches.ApplyFunc(securitypolicy.GetSecurityService, func(service common.Service, vpcService common.VPCServiceProvider) *securitypolicy.SecurityPolicyService {
					return fakeService()
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(&SecurityPolicyReconciler{}), "setupWithManager", func(_ *SecurityPolicyReconciler, mgr ctrl.Manager) error {
					return errors.New("failed to setupWithManager")
				})
				return patches
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			patches := testCase.patches()
			defer patches.Reset()

			r := NewSecurityPolicyReconciler(mgr, commonService, vpcService)
			err := r.StartController(mgr, nil)

			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			} else {
				assert.NoError(t, err, "expected no error when starting the SecurityPolicy controller")
			}
		})
	}
}
