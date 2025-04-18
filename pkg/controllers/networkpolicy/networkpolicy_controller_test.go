/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkpolicy

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/openlyinc/pointy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctrcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
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
					EnableVPCNetwork: true,
				},
				NsxConfig: &config.NsxConfig{
					EnforcementPoint: "vmc-enforcementpoint",
				},
			},
		},
	}
	return service
}

func createFakeNetworkPolicyReconciler(objs []client.Object) *NetworkPolicyReconciler {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...).Build()

	r := &NetworkPolicyReconciler{
		Client:   fakeClient,
		Scheme:   fake.NewClientBuilder().Build().Scheme(),
		Service:  fakeService(),
		Recorder: fakeRecorder{},
	}
	r.StatusUpdater = ctrcommon.NewStatusUpdater(r.Client, r.Service.NSXConfig, r.Recorder, MetricResType, "NetworkPolicy", "NetworkPolicy")
	return r
}

func Test_setNetworkPolicyErrorAnnotation(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	ctx := context.TODO()
	info := ctrcommon.ErrorNoDFWLicense

	// Create a sample NetworkPolicy without annotations
	networkPolicy := &networkingv1.NetworkPolicy{}

	// Mock the Update call with gomock for the case when info is being added
	k8sClient.EXPECT().
		Update(ctx, networkPolicy).
		Return(nil)

	// Call the function under test
	setNetworkPolicyErrorAnnotation(ctx, networkPolicy, k8sClient, info)

	// Check that the annotation was set correctly
	require.NotNil(t, networkPolicy.Annotations)
	assert.Equal(t, info, networkPolicy.Annotations[ctrcommon.NSXOperatorError])

	// Call the function again with the same info; Update should not be called
	setNetworkPolicyErrorAnnotation(ctx, networkPolicy, k8sClient, info)
}

func Test_cleanNetworkPolicyErrorAnnotation(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)

	ctx := context.TODO()
	info := ctrcommon.ErrorNoDFWLicense

	// Test case 1: Annotation exists, should be removed
	t.Run("Annotation exists", func(t *testing.T) {
		networkPolicy := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					ctrcommon.NSXOperatorError: info,
				},
			},
		}

		// Expect Update to be called once since we are removing the annotation
		k8sClient.EXPECT().
			Update(ctx, networkPolicy).
			Return(nil).
			Times(1)

		// Call the function under test
		cleanNetworkPolicyErrorAnnotation(ctx, networkPolicy, k8sClient)

		// Check that the annotation was removed
		assert.NotContains(t, networkPolicy.Annotations, ctrcommon.NSXOperatorError)
	})

	// Test case 2: Annotation does not exist, Update should not be called
	t.Run("Annotation does not exist", func(t *testing.T) {
		networkPolicy := &networkingv1.NetworkPolicy{}

		// Update should not be called since there's no annotation to remove
		k8sClient.EXPECT().Update(ctx, networkPolicy).Times(0)

		// Call the function under test
		cleanNetworkPolicyErrorAnnotation(ctx, networkPolicy, k8sClient)
	})
}

func TestNetworkPolicyReconciler_Reconcile(t *testing.T) {
	npName := "test-np"
	npID := "fake-np-uid"
	ns := "default"

	createNewNetworkPolicy := func(specs ...bool) *networkingv1.NetworkPolicy {
		networkPolicyCR := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      npName,
				Namespace: ns,
				UID:       types.UID(npID),
			},
			Spec: networkingv1.NetworkPolicySpec{},
		}
		if len(specs) > 0 && specs[0] {
			// Finalizers and DeletionTimestamp must be set together
			networkPolicyCR.Finalizers = []string{"test-Finalizers"}
			networkPolicyCR.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		}
		return networkPolicyCR
	}

	testCases := []struct {
		name                    string
		req                     ctrl.Request
		expectRes               ctrl.Result
		expectErrStr            string
		patches                 func(r *NetworkPolicyReconciler) *gomonkey.Patches
		existingNetworkPolicyCR *networkingv1.NetworkPolicy
		expectNetworkPolicyCR   *networkingv1.NetworkPolicy
	}{
		{
			name: "NetworkPolicy CR not found",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: npName}},
			patches: func(r *NetworkPolicyReconciler) *gomonkey.Patches {
				return gomonkey.ApplyPrivateMethod(reflect.TypeOf(r), "deleteNetworkPolicyByName", func(_ *NetworkPolicyReconciler, ns, name string) error {
					return nil
				})
			},
			expectRes:               ResultNormal,
			existingNetworkPolicyCR: nil,
		},
		{
			name: "Get NetworkPolicy return other error should retry",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: npName}},
			patches: func(r *NetworkPolicyReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Client), "Get", func(_ client.Client, _ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
					return errors.New("get NetworkPolicy CR error")
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(r), "deleteNetworkPolicyByName", func(_ *NetworkPolicyReconciler, ns, name string) error {
					return nil
				})
				return patches
			},
			expectErrStr:            "get NetworkPolicy CR error",
			expectRes:               ResultRequeue,
			existingNetworkPolicyCR: nil,
		},
		{
			name: "NetworkPolicy with DeletionTimestamp not zero and delete success",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: npName}},
			patches: func(r *NetworkPolicyReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}, isGc bool, createdFor string) error {
					return nil
				})
				return patches
			},
			expectRes:               ResultNormal,
			existingNetworkPolicyCR: createNewNetworkPolicy(true),
		},
		{
			name: "NetworkPolicy with DeletionTimestamp not zero and delete fail",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: npName}},
			patches: func(r *NetworkPolicyReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}, isGc bool, createdFor string) error {
					return errors.New("delete networkpolicy failed")
				})
				return patches
			},
			expectErrStr:            "delete networkpolicy failed",
			expectRes:               ResultRequeue,
			existingNetworkPolicyCR: createNewNetworkPolicy(true),
		},
		{
			name: "NetworkPolicy with DeletionTimestamp zero and create/update success",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: npName}},
			patches: func(r *NetworkPolicyReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}) error {
					return nil
				})
				return patches
			},
			expectRes:               ResultNormal,
			existingNetworkPolicyCR: createNewNetworkPolicy(),
		},
		{
			name: "NetworkPolicy with DeletionTimestamp zero and create/update fail",
			req:  ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: npName}},
			patches: func(r *NetworkPolicyReconciler) *gomonkey.Patches {
				patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}) error {
					return errors.New("create or update networkpolicy failed")
				})
				return patches
			},
			expectErrStr:            "create or update networkpolicy failed",
			expectRes:               ResultRequeue,
			existingNetworkPolicyCR: createNewNetworkPolicy(),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			objs := []client.Object{}
			if testCase.existingNetworkPolicyCR != nil {
				objs = append(objs, testCase.existingNetworkPolicyCR)
			}
			reconciler := createFakeNetworkPolicyReconciler(objs)
			ctx := context.Background()

			v1alpha1.AddToScheme(reconciler.Scheme)
			patches := testCase.patches(reconciler)
			defer patches.Reset()

			result, err := reconciler.Reconcile(ctx, testCase.req)
			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, testCase.expectRes, result)

			if testCase.expectNetworkPolicyCR != nil {
				actualNetworkPolicyCR := &networkingv1.NetworkPolicy{}
				assert.NoError(t, reconciler.Client.Get(ctx, testCase.req.NamespacedName, actualNetworkPolicyCR))
				assert.Equal(t, testCase.expectNetworkPolicyCR.Spec, actualNetworkPolicyCR.Spec)
			}
		})
	}
}

func TestNetworkPolicyReconciler_GarbageCollector(t *testing.T) {
	testCases := []struct {
		name                    string
		patches                 func(r *NetworkPolicyReconciler) *gomonkey.Patches
		existingNetworkPolicyCR *networkingv1.NetworkPolicy
	}{
		{
			name: "Delete stale NetworkPolicy success",
			patches: func(r *NetworkPolicyReconciler) *gomonkey.Patches {
				patch := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListNetworkPolicyID", func(_ *securitypolicy.SecurityPolicyService) sets.Set[string] {
					res := sets.New[string]("1234_ingress", "1234_isolation")
					return res
				})
				patch.ApplyMethod(reflect.TypeOf(r.Service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}, isGc bool, createdFor string) error {
					return nil
				})
				return patch
			},
		},
		{
			name: "Should not delete NSX corresponding SecurityPolicies when the NetworkPolicy CR exists",
			patches: func(r *NetworkPolicyReconciler) *gomonkey.Patches {
				// local store has same item as k8s cache
				patch := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListNetworkPolicyID", func(_ *securitypolicy.SecurityPolicyService) sets.Set[string] {
					res := sets.New[string]("1234_allow", "1234_isolation")
					return res
				})
				patch.ApplyMethod(reflect.TypeOf(r.Service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}, isGc bool, createdFor string) error {
					assert.FailNow(t, "should not be called")
					return nil
				})
				return patch
			},
			existingNetworkPolicyCR: &networkingv1.NetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "np-1",
					Namespace: "default",
					UID:       types.UID("1234"),
				},
			},
		},
		{
			name: "Delete NSX corresponding SecurityPolicies error",
			patches: func(r *NetworkPolicyReconciler) *gomonkey.Patches {
				patch := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListNetworkPolicyID", func(_ *securitypolicy.SecurityPolicyService) sets.Set[string] {
					res := sets.New[string]("1234_allow", "1234_isolation")
					return res
				})
				patch.ApplyMethod(reflect.TypeOf(r.Service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}, isGc bool, createdFor string) error {
					return errors.New("delete failed")
				})
				return patch
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			objs := []client.Object{}
			if testCase.existingNetworkPolicyCR != nil {
				objs = append(objs, testCase.existingNetworkPolicyCR)
			}
			r := createFakeNetworkPolicyReconciler(objs)
			ctx := context.Background()

			patches := testCase.patches(r)
			defer patches.Reset()

			r.CollectGarbage(ctx)
		})
	}
}

func TestNetworkPolicyReconciler_listNetworkPolciyCRIDs(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)
	r := &NetworkPolicyReconciler{
		Client: k8sClient,
		Scheme: nil,
	}
	ctx := context.Background()

	// list returns an error
	errList := errors.New("list error")
	k8sClient.EXPECT().List(ctx, gomock.Any()).Return(errList)
	_, err := r.listNetworkPolciyCRIDs()
	assert.Equal(t, err, errList)

	// list returns no error, but no items
	k8sClient.EXPECT().List(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		networkPolicyList := list.(*networkingv1.NetworkPolicyList)
		networkPolicyList.Items = []networkingv1.NetworkPolicy{}
		return nil
	})
	crIDs, err := r.listNetworkPolciyCRIDs()
	assert.NoError(t, err)
	assert.Equal(t, 0, crIDs.Len())

	// list returns items
	k8sClient.EXPECT().List(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		networkPolicyList := list.(*networkingv1.NetworkPolicyList)
		networkPolicyList.Items = []networkingv1.NetworkPolicy{
			{ObjectMeta: metav1.ObjectMeta{UID: "uid1"}},
		}
		return nil
	})
	crIDs, err = r.listNetworkPolciyCRIDs()
	assert.NoError(t, err)
	assert.Equal(t, 2, crIDs.Len())
	assert.True(t, crIDs.Has("uid1_allow"))
	assert.True(t, crIDs.Has("uid1_isolation"))
}

func TestNetworkPolicyReconciler_deleteNetworkPolicyByName(t *testing.T) {
	objs := []client.Object{}
	r := createFakeNetworkPolicyReconciler(objs)

	// deletion fails
	patch := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "ListNetworkPolicyByName", func(_ *securitypolicy.SecurityPolicyService, _ string, _ string) []*model.SecurityPolicy {
		return []*model.SecurityPolicy{
			{
				Id:   pointy.String("sp-id-1"),
				Tags: []model.Tag{{Scope: pointy.String(common.TagScopeNetworkPolicyUID), Tag: pointy.String("uid1")}},
			},
			{
				Id:   pointy.String("sp-id-2"),
				Tags: []model.Tag{{Scope: pointy.String(common.TagScopeNetworkPolicyUID), Tag: pointy.String("uid2")}},
			},
		}
	})

	patch.ApplyMethod(reflect.TypeOf(r.Service), "DeleteSecurityPolicy", func(_ *securitypolicy.SecurityPolicyService, obj interface{}, isGc bool, createdFor string) error {
		switch sp := obj.(type) {
		case types.UID:
			if sp == "uid2" {
				return errors.New("delete failed")
			}
		}
		return nil
	})

	err := r.deleteNetworkPolicyByName("dummy-ns", "dummy-name")
	assert.Error(t, err)
	patch.Reset()
}

func TestStartNetworkPolicyController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	vpcService := &vpc.VPCService{
		Service: common.Service{
			Client: fakeClient,
		},
	}
	commonService := common.Service{
		Client: fakeClient,
	}
	mockMgr := &MockManager{scheme: runtime.NewScheme()}

	testCases := []struct {
		name         string
		expectErrStr string
		patches      func() *gomonkey.Patches
	}{
		// expected no error when starting the NetworkPolicy controller
		{
			name: "Start NetworkPolicy Controller",
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
				patches.ApplyMethod(reflect.TypeOf(&NetworkPolicyReconciler{}), "Start", func(_ *NetworkPolicyReconciler, r ctrl.Manager) error {
					return nil
				})
				return patches
			},
		},
		{
			name:         "Start NetworkPolicy controller return error",
			expectErrStr: "failed to setupWithManager",
			patches: func() *gomonkey.Patches {
				patches := gomonkey.ApplyFunc(ctrcommon.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
					return
				})
				patches.ApplyFunc(securitypolicy.GetSecurityService, func(service common.Service, vpcService common.VPCServiceProvider) *securitypolicy.SecurityPolicyService {
					return fakeService()
				})
				patches.ApplyPrivateMethod(reflect.TypeOf(&NetworkPolicyReconciler{}), "setupWithManager", func(_ *NetworkPolicyReconciler, mgr ctrl.Manager) error {
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

			r := NewNetworkPolicyReconciler(mockMgr, commonService, vpcService)
			err := r.StartController(mockMgr, nil)

			if testCase.expectErrStr != "" {
				assert.ErrorContains(t, err, testCase.expectErrStr)
			} else {
				assert.NoError(t, err, "expected no error when starting the NetworkPolicy controller")
			}
		})
	}
}
