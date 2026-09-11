/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package serviceendpoint

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	ctrcommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/serviceendpoint"
)

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

func (writer fakeStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return nil
}

type fakeRecorder struct{}

func (recorder fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {}

func (recorder fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (recorder fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func fakeServiceEndpointService() *serviceendpoint.ServiceEndpointService {
	return &serviceendpoint.ServiceEndpointService{
		Service: servicecommon.Service{
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
}

func newFakeServiceEndpointReconciler(k8sClient client.Client, service *serviceendpoint.ServiceEndpointService) *ServiceEndpointReconciler {
	r := &ServiceEndpointReconciler{
		Client:   k8sClient,
		Service:  service,
		Recorder: fakeRecorder{},
	}
	r.StatusUpdater = ctrcommon.NewStatusUpdater(r.Client, service.NSXConfig, fakeRecorder{}, MetricResType, "ServiceEndpoint", "ServiceEndpoint")
	return r
}

func TestServiceEndpointReconciler_Reconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := fakeServiceEndpointService()
	r := newFakeServiceEndpointReconciler(k8sClient, service)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "dummy", Name: "dummy"}}

	// fail to get CR
	errFailToGet := errors.New("failed to get CR")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errFailToGet)
	result, retErr := r.Reconcile(ctx, req)
	assert.Equal(t, errFailToGet, retErr)
	assert.Equal(t, resultRequeue, result)

	// not found, NSX-side deletion fails
	errNotFound := apierrors.NewNotFound(v1alpha1.Resource("ServiceEndpoint"), "dummy")
	errDelete := errors.New("failed to delete by namespaced name")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteServiceEndpointByNamespacedName", func(_ *serviceendpoint.ServiceEndpointService, namespace, name string) error {
		return errDelete
	})
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, errDelete, retErr)
	assert.Equal(t, resultRequeue, result)
	patch.Reset()

	// not found, NSX-side deletion succeeds
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(errNotFound)
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteServiceEndpointByNamespacedName", func(_ *serviceendpoint.ServiceEndpointService, namespace, name string) error {
		return nil
	})
	result, retErr = r.Reconcile(ctx, req)
	assert.NoError(t, retErr)
	assert.Equal(t, resultNormal, result)
	patch.Reset()

	// found, no deletion timestamp, create/update succeeds
	fakewriter := fakeStatusWriter{}
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil)
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "CreateOrUpdateServiceEndpoint", func(_ *serviceendpoint.ServiceEndpointService, obj *v1alpha1.ServiceEndpoint) error {
		return nil
	})
	k8sClient.EXPECT().Status().Times(1).Return(fakewriter)
	result, retErr = r.Reconcile(ctx, req)
	assert.NoError(t, retErr)
	assert.Equal(t, resultNormal, result)
	patch.Reset()

	// found, no deletion timestamp, create/update fails
	errUpdate := errors.New("create or update failed")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil)
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "CreateOrUpdateServiceEndpoint", func(_ *serviceendpoint.ServiceEndpointService, obj *v1alpha1.ServiceEndpoint) error {
		return errUpdate
	})
	k8sClient.EXPECT().Status().Times(1).Return(fakewriter)
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, errUpdate, retErr)
	assert.Equal(t, resultRequeue, result)
	patch.Reset()

	// found, deletion timestamp set, no finalizer blocking: NSX delete succeeds
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
		se := obj.(*v1alpha1.ServiceEndpoint)
		now := metav1.Now()
		se.ObjectMeta.DeletionTimestamp = &now
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteServiceEndpoint", func(_ *serviceendpoint.ServiceEndpointService, obj interface{}) error {
		return nil
	})
	result, retErr = r.Reconcile(ctx, req)
	assert.NoError(t, retErr)
	assert.Equal(t, resultNormal, result)
	patch.Reset()

	// found, deletion timestamp set, NSX delete fails: DeleteFailure status condition should be set
	errNSXDelete := errors.New("nsx delete failed")
	k8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
		se := obj.(*v1alpha1.ServiceEndpoint)
		now := metav1.Now()
		se.ObjectMeta.DeletionTimestamp = &now
		return nil
	})
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "DeleteServiceEndpoint", func(_ *serviceendpoint.ServiceEndpointService, obj interface{}) error {
		return errNSXDelete
	})
	k8sClient.EXPECT().Status().Times(1).Return(fakewriter)
	result, retErr = r.Reconcile(ctx, req)
	assert.Equal(t, errNSXDelete, retErr)
	assert.Equal(t, resultRequeue, result)
	patch.Reset()
}

func TestSetDeleteFailedStatus(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)
	k8sClient.EXPECT().Status().Return(fakeStatusWriter{}).AnyTimes()

	se := &v1alpha1.ServiceEndpoint{}
	setDeleteFailedStatus(k8sClient, context.TODO(), se, metav1.Now(), errors.New("boom"))
	cond := apimeta.FindStatusCondition(se.Status.Conditions, "DeleteFailure")
	assert.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "ServiceEndpointInUse", cond.Reason)
}

func TestServiceEndpointReconciler_CollectGarbage(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := fakeServiceEndpointService()
	r := newFakeServiceEndpointReconciler(k8sClient, service)
	ctx := context.Background()
	seList := &v1alpha1.ServiceEndpointList{}

	// local store has an orphaned item, gets deleted
	patch := gomonkey.ApplyMethod(reflect.TypeOf(service), "ListServiceEndpointID", func(_ *serviceendpoint.ServiceEndpointService) sets.Set[string] {
		s := sets.New[string]()
		s.Insert("1234")
		s.Insert("2345")
		return s
	})
	patch.ApplyMethod(reflect.TypeOf(service), "DeleteServiceEndpoint", func(_ *serviceendpoint.ServiceEndpointService, obj interface{}) error {
		assert.Equal(t, types.UID("2345"), obj)
		return nil
	})
	k8sClient.EXPECT().List(ctx, seList).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		l := list.(*v1alpha1.ServiceEndpointList)
		l.Items = append(l.Items, v1alpha1.ServiceEndpoint{ObjectMeta: metav1.ObjectMeta{UID: "1234"}})
		return nil
	})
	err := r.CollectGarbage(ctx)
	assert.NoError(t, err)
	patch.Reset()

	// local store empty, GC is a no-op and does not list CRs
	patch = gomonkey.ApplyMethod(reflect.TypeOf(service), "ListServiceEndpointID", func(_ *serviceendpoint.ServiceEndpointService) sets.Set[string] {
		return sets.New[string]()
	})
	k8sClient.EXPECT().List(ctx, seList).Times(0)
	err = r.CollectGarbage(ctx)
	assert.NoError(t, err)
	patch.Reset()
}

func TestServiceEndpointReconciler_RestoreReconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()
	k8sClient := mock_client.NewMockClient(mockCtl)
	service := fakeServiceEndpointService()
	r := newFakeServiceEndpointReconciler(k8sClient, service)

	assert.NoError(t, r.RestoreReconcile())
}
