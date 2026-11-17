/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkresourcetransition

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	migrationv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	mockservices "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func setupScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	_ = migrationv1alpha1.AddToScheme(scheme)
	return scheme
}

func TestShouldRetryTransitionCompletionError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantRetry bool
	}{
		{
			name:      "forbidden API error is retried",
			err:       apierrors.NewForbidden(schema.GroupResource{Resource: "ipaddressallocations"}, "target", errors.New("forbidden")),
			wantRetry: true,
		},
		{
			name:      "other API error is retried",
			err:       apierrors.NewServiceUnavailable("temporarily unavailable"),
			wantRetry: true,
		},
		{
			name:      "permanent transition validation error is not retried",
			err:       &permanentTransitionValidationError{err: errors.New("target UID changed")},
			wantRetry: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantRetry, shouldRetryTransitionCompletionError(tc.err))
		})
	}
}

func stringPtr(s string) *string {
	return &s
}

func TestReconcile_DeletedTransitionCR(t *testing.T) {
	scheme := setupScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &NetworkResourceTransitionReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Service:    &mockservices.MockIPAddressAllocationProvider{},
		VPCService: &mockservices.MockVPCServiceProvider{},
		Recorder:   record.NewFakeRecorder(10),
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "non-existent"},
	})
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)
}

func TestReconcile_NonNSXLB(t *testing.T) {
	scheme := setupScheme()
	transitionCR := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-transition",
		},
		Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
			IPAddressAllocationMoves: []migrationv1alpha1.IPAddressAllocationMoveSpec{
				{
					Source: migrationv1alpha1.NamespacedName{
						Namespace: "vmware-system-net-staging",
						Name:      "test-ipaa",
					},
					Target: migrationv1alpha1.NamespacedName{
						Namespace: "tenant-ns",
						Name:      "test-ipaa",
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(transitionCR).WithStatusSubresource(&migrationv1alpha1.NetworkResourceTransition{}).Build()
	mockVPC := &mockservices.MockVPCServiceProvider{}
	mockVPC.On("GetLBProvider").Return(servicecommon.AVILB, nil)

	r := &NetworkResourceTransitionReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Service:    &mockservices.MockIPAddressAllocationProvider{},
		VPCService: mockVPC,
		Recorder:   record.NewFakeRecorder(10),
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-transition"},
	})
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)

	// Unsupported LB providers must fail the transition instead of leaving it pending.
	updated := &migrationv1alpha1.NetworkResourceTransition{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-transition"}, updated)
	assert.NoError(t, err)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseFailed, updated.Status.Phase)
	assert.Equal(t, 1, updated.Status.FailedResources)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseFailed, updated.Status.IPAddressAllocationMoves[0].Phase)
	assert.Contains(t, updated.Status.IPAddressAllocationMoves[0].Message, string(servicecommon.AVILB))
}

func TestReconcile_GetLBProviderError(t *testing.T) {
	scheme := setupScheme()
	transitionCR := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-transition",
		},
		Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
			IPAddressAllocationMoves: []migrationv1alpha1.IPAddressAllocationMoveSpec{
				{
					Source: migrationv1alpha1.NamespacedName{
						Namespace: "vmware-system-net-staging",
						Name:      "test-ipaa",
					},
					Target: migrationv1alpha1.NamespacedName{
						Namespace: "tenant-ns",
						Name:      "test-ipaa",
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(transitionCR).WithStatusSubresource(&migrationv1alpha1.NetworkResourceTransition{}).Build()
	mockVPC := &mockservices.MockVPCServiceProvider{}
	mockVPC.On("GetLBProvider").Return(servicecommon.LBProvider(""), errors.New("failed to query provider"))

	r := &NetworkResourceTransitionReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Service:    &mockservices.MockIPAddressAllocationProvider{},
		VPCService: mockVPC,
		Recorder:   record.NewFakeRecorder(10),
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-transition"},
	})
	assert.NoError(t, err)
	assert.True(t, res.RequeueAfter > 0)
	updated := &migrationv1alpha1.NetworkResourceTransition{}
	assert.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-transition"}, updated))
	assert.Equal(t, migrationv1alpha1.TransitionPhaseInProgress, updated.Status.Phase)
	assert.Equal(t, "WaitingForLBProvider", updated.Status.Conditions[0].Reason)
	assert.Contains(t, updated.Status.IPAddressAllocationMoves[0].Message, "failed to query provider")
}

func TestReconcile_SourceNotFound(t *testing.T) {
	scheme := setupScheme()
	transitionCR := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-transition",
		},
		Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
			IPAddressAllocationMoves: []migrationv1alpha1.IPAddressAllocationMoveSpec{
				{
					Source: migrationv1alpha1.NamespacedName{
						Namespace: "vmware-system-net-staging",
						Name:      "missing-source",
					},
					Target: migrationv1alpha1.NamespacedName{
						Namespace: "tenant-ns",
						Name:      "target-ipaa",
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(transitionCR).WithStatusSubresource(&migrationv1alpha1.NetworkResourceTransition{}).Build()
	mockVPC := &mockservices.MockVPCServiceProvider{}
	mockVPC.On("GetLBProvider").Return(servicecommon.NSXLB, nil)

	r := &NetworkResourceTransitionReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Service:    &mockservices.MockIPAddressAllocationProvider{},
		VPCService: mockVPC,
		Recorder:   record.NewFakeRecorder(10),
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-transition"},
	})
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)

	updated := &migrationv1alpha1.NetworkResourceTransition{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-transition"}, updated)
	assert.NoError(t, err)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseFailed, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseFailed, updated.Status.IPAddressAllocationMoves[0].Phase)
}

func TestReconcile_WaitingSourceAllocation(t *testing.T) {
	scheme := setupScheme()
	sourceCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipaa-vip",
			Namespace: "vmware-system-net-staging",
			UID:       types.UID("source-uid-123"),
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			UsedFor: v1alpha1.UsedForLBFrontend,
		},
		// No allocation IPs yet
	}

	transitionCR := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-transition",
		},
		Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
			IPAddressAllocationMoves: []migrationv1alpha1.IPAddressAllocationMoveSpec{
				{
					Source: migrationv1alpha1.NamespacedName{
						Namespace: "vmware-system-net-staging",
						Name:      "ipaa-vip",
					},
					Target: migrationv1alpha1.NamespacedName{
						Namespace: "tenant-ns",
						Name:      "ipaa-vip",
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sourceCR, transitionCR).
		WithStatusSubresource(&v1alpha1.IPAddressAllocation{}, &migrationv1alpha1.NetworkResourceTransition{}).
		Build()

	mockVPC := &mockservices.MockVPCServiceProvider{}
	mockVPC.On("GetLBProvider").Return(servicecommon.NSXLB, nil)

	r := &NetworkResourceTransitionReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Service:    &mockservices.MockIPAddressAllocationProvider{},
		VPCService: mockVPC,
		Recorder:   record.NewFakeRecorder(10),
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-transition"},
	})
	assert.NoError(t, err)
	assert.True(t, res.RequeueAfter > 0)

	updated := &migrationv1alpha1.NetworkResourceTransition{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-transition"}, updated)
	assert.NoError(t, err)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseInProgress, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseInProgress, updated.Status.IPAddressAllocationMoves[0].Phase)
}

func TestReconcile_Success_InPlaceTagAdoption(t *testing.T) {
	scheme := setupScheme()
	sourceCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipaa-vip",
			Namespace: "vmware-system-net-staging",
			UID:       types.UID("source-uid-123"),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "Service",
					Name:       "svc-lb",
					UID:        types.UID("old-svc-uid"),
				},
			},
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			UsedFor:     v1alpha1.UsedForLBFrontend,
			IPBlockName: "ipblock-public",
		},
		Status: v1alpha1.IPAddressAllocationStatus{
			AllocationIPs: "192.168.10.50",
		},
	}

	targetNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-ns",
			UID:  types.UID("tenant-ns-uid-456"),
		},
	}

	targetService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-lb",
			Namespace: "tenant-ns",
			UID:       types.UID("new-svc-uid-789"),
		},
	}

	transitionCR := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-transition",
		},
		Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
			IPAddressAllocationMoves: []migrationv1alpha1.IPAddressAllocationMoveSpec{
				{
					Source: migrationv1alpha1.NamespacedName{
						Namespace: "vmware-system-net-staging",
						Name:      "ipaa-vip",
					},
					Target: migrationv1alpha1.NamespacedName{
						Namespace: "tenant-ns",
						Name:      "ipaa-vip",
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sourceCR, targetNamespace, targetService, transitionCR).
		WithStatusSubresource(&v1alpha1.IPAddressAllocation{}, &migrationv1alpha1.NetworkResourceTransition{}).
		Build()

	mockVPC := &mockservices.MockVPCServiceProvider{}
	mockVPC.On("GetLBProvider").Return(servicecommon.NSXLB, nil)

	mockIPAA := &mockservices.MockIPAddressAllocationProvider{}
	existingAlloc := &model.VpcIpAddressAllocation{
		AllocationIps: stringPtr("192.168.10.50"),
		Id:            stringPtr("alloc-nsx-1"),
		Path:          stringPtr("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-nsx-1"),
	}
	mockIPAA.On("GetIPAddressAllocationForTransition", types.UID("source-uid-123"), "vmware-system-net-staging", "ipaa-vip").
		Return(existingAlloc, nil)

	adoptedAlloc := &model.VpcIpAddressAllocation{
		AllocationIps: stringPtr("192.168.10.50"),
		Id:            stringPtr("alloc-nsx-1"),
		Path:          stringPtr("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-nsx-1"),
	}
	mockIPAA.On("AdoptIPAddressAllocationTags", existingAlloc, "tenant-ns", "tenant-ns-uid-456", "ipaa-vip", mock.Anything).
		Return(adoptedAlloc, nil)

	r := &NetworkResourceTransitionReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Service:    mockIPAA,
		VPCService: mockVPC,
		Recorder:   record.NewFakeRecorder(10),
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-transition"},
	})
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)

	// Check transition status
	updated := &migrationv1alpha1.NetworkResourceTransition{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-transition"}, updated)
	assert.NoError(t, err)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseSucceeded, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseSucceeded, updated.Status.IPAddressAllocationMoves[0].Phase)
	assert.Contains(t, updated.Finalizers, servicecommon.NetworkResourceTransitionFinalizerName)

	// Check target CR created
	targetCR := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "tenant-ns", Name: "ipaa-vip"}, targetCR)
	assert.NoError(t, err)
	assert.Equal(t, "192.168.10.50", targetCR.Status.AllocationIPs)
	assert.Equal(t, "ipblock-public", targetCR.Spec.IPBlockName)
	assert.Equal(t, v1alpha1.UsedForLBFrontend, targetCR.Spec.UsedFor)
	assert.Len(t, targetCR.OwnerReferences, 1)
	assert.Equal(t, types.UID("new-svc-uid-789"), targetCR.OwnerReferences[0].UID)

	// Check source CR reaped
	srcCRCheck := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "vmware-system-net-staging", Name: "ipaa-vip"}, srcCRCheck)
	assert.Error(t, err)
}

func TestReconcile_DeletionRequestedCompletesTransition(t *testing.T) {
	scheme := setupScheme()
	deletedAt := metav1.Now()
	spec := v1alpha1.IPAddressAllocationSpec{
		UsedFor:     v1alpha1.UsedForLBFrontend,
		IPBlockName: "ipblock-public",
	}
	sourceCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipaa-vip",
			Namespace: "vmware-system-net-staging",
			UID:       types.UID("source-uid-123"),
		},
		Spec:   spec,
		Status: v1alpha1.IPAddressAllocationStatus{AllocationIPs: "192.168.10.50"},
	}
	targetCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "ipaa-vip",
			Namespace:   "tenant-ns",
			UID:         types.UID("target-uid-456"),
			Annotations: map[string]string{servicecommon.AnnotationTransitionTarget: "true"},
		},
		Spec: spec,
	}
	transitionCR := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-transition",
			DeletionTimestamp: &deletedAt,
			Finalizers:        []string{servicecommon.NetworkResourceTransitionFinalizerName},
		},
		Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
			IPAddressAllocationMoves: []migrationv1alpha1.IPAddressAllocationMoveSpec{{
				Source: migrationv1alpha1.NamespacedName{Namespace: sourceCR.Namespace, Name: sourceCR.Name},
				Target: migrationv1alpha1.NamespacedName{Namespace: targetCR.Namespace, Name: targetCR.Name},
			}},
		},
	}
	targetNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant-ns", UID: types.UID("tenant-ns-uid-456")}}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sourceCR, targetCR, targetNamespace, transitionCR).
		WithStatusSubresource(&v1alpha1.IPAddressAllocation{}, &migrationv1alpha1.NetworkResourceTransition{}).
		Build()

	mockVPC := &mockservices.MockVPCServiceProvider{}
	mockVPC.On("GetLBProvider").Return(servicecommon.NSXLB, nil)
	mockIPAA := &mockservices.MockIPAddressAllocationProvider{}
	existingAlloc := &model.VpcIpAddressAllocation{
		AllocationIps: stringPtr("192.168.10.50"),
		Id:            stringPtr("alloc-nsx-1"),
		Path:          stringPtr("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-nsx-1"),
	}
	mockIPAA.On("GetIPAddressAllocationForTransition", types.UID("target-uid-456"), "tenant-ns", "ipaa-vip").
		Return(nil, nil)
	mockIPAA.On("GetIPAddressAllocationForTransition", types.UID("source-uid-123"), "vmware-system-net-staging", "ipaa-vip").
		Return(existingAlloc, nil)
	mockIPAA.On("AdoptIPAddressAllocationTags", existingAlloc, "tenant-ns", "tenant-ns-uid-456", "ipaa-vip", types.UID("target-uid-456")).
		Return(existingAlloc, nil)

	r := &NetworkResourceTransitionReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Service:    mockIPAA,
		VPCService: mockVPC,
		Recorder:   record.NewFakeRecorder(10),
	}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: transitionCR.Name}})
	assert.NoError(t, err)
	assert.Zero(t, result.RequeueAfter)
	mockIPAA.AssertExpectations(t)

	updatedTarget := &v1alpha1.IPAddressAllocation{}
	assert.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Namespace: targetCR.Namespace, Name: targetCR.Name}, updatedTarget))
	assert.Equal(t, "192.168.10.50", updatedTarget.Status.AllocationIPs)
	assert.NotContains(t, updatedTarget.Annotations, servicecommon.AnnotationTransitionTarget)
	updatedSource := &v1alpha1.IPAddressAllocation{}
	sourceErr := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: sourceCR.Namespace, Name: sourceCR.Name}, updatedSource)
	assert.True(t, apierrors.IsNotFound(sourceErr))

	updatedTransition := &migrationv1alpha1.NetworkResourceTransition{}
	getErr := fakeClient.Get(context.Background(), types.NamespacedName{Name: transitionCR.Name}, updatedTransition)
	if getErr == nil {
		assert.Equal(t, migrationv1alpha1.TransitionPhaseSucceeded, updatedTransition.Status.Phase)
		assert.NotContains(t, updatedTransition.Finalizers, servicecommon.NetworkResourceTransitionFinalizerName)
	} else {
		assert.True(t, apierrors.IsNotFound(getErr))
	}
}

func TestReconcile_Idempotent_AlreadyTransitioned(t *testing.T) {
	scheme := setupScheme()
	targetCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipaa-vip",
			Namespace: "tenant-ns",
			UID:       types.UID("target-uid-456"),
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			UsedFor: v1alpha1.UsedForLBFrontend,
		},
		Status: v1alpha1.IPAddressAllocationStatus{
			AllocationIPs: "192.168.10.50",
		},
	}

	transitionCR := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-transition",
		},
		Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
			IPAddressAllocationMoves: []migrationv1alpha1.IPAddressAllocationMoveSpec{
				{
					Source: migrationv1alpha1.NamespacedName{
						Namespace: "vmware-system-net-staging",
						Name:      "ipaa-vip",
					},
					Target: migrationv1alpha1.NamespacedName{
						Namespace: "tenant-ns",
						Name:      "ipaa-vip",
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(targetCR, transitionCR).
		WithStatusSubresource(&v1alpha1.IPAddressAllocation{}, &migrationv1alpha1.NetworkResourceTransition{}).
		Build()

	mockVPC := &mockservices.MockVPCServiceProvider{}
	mockVPC.On("GetLBProvider").Return(servicecommon.NSXLB, nil)

	mockIPAA := &mockservices.MockIPAddressAllocationProvider{}
	mockIPAA.On("GetIPAddressAllocationForTransition", targetCR.UID, targetCR.Namespace, targetCR.Name).Return(&model.VpcIpAddressAllocation{
		Id:            stringPtr("alloc-nsx-1"),
		Path:          stringPtr("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-nsx-1"),
		AllocationIps: stringPtr("192.168.10.50"),
		Tags: []model.Tag{
			{Scope: stringPtr(servicecommon.TagScopeIPAddressAllocationCRUID), Tag: stringPtr(string(targetCR.UID))},
			{Scope: stringPtr(servicecommon.TagScopeNamespace), Tag: stringPtr(targetCR.Namespace)},
			{Scope: stringPtr(servicecommon.TagScopeIPAddressAllocationCRName), Tag: stringPtr(targetCR.Name)},
		},
	}, nil)
	r := &NetworkResourceTransitionReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Service:    mockIPAA,
		VPCService: mockVPC,
		Recorder:   record.NewFakeRecorder(10),
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-transition"},
	})
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)

	updated := &migrationv1alpha1.NetworkResourceTransition{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-transition"}, updated)
	assert.NoError(t, err)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseSucceeded, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseSucceeded, updated.Status.IPAddressAllocationMoves[0].Phase)
}

func TestReconcile_Recovery_RestartDuringStatusUpdate(t *testing.T) {
	scheme := setupScheme()
	sourceCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipaa-vip",
			Namespace: "vmware-system-net-staging",
			UID:       types.UID("source-uid-123"),
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			UsedFor: v1alpha1.UsedForLBFrontend,
		},
		Status: v1alpha1.IPAddressAllocationStatus{
			AllocationIPs: "192.168.10.50",
		},
	}

	targetCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipaa-vip",
			Namespace: "tenant-ns",
			UID:       types.UID("target-uid-456"),
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			UsedFor: v1alpha1.UsedForLBFrontend,
		},
		// Status is empty because operator crashed right before updating target status
		Status: v1alpha1.IPAddressAllocationStatus{},
	}

	transitionCR := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-transition",
		},
		Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
			IPAddressAllocationMoves: []migrationv1alpha1.IPAddressAllocationMoveSpec{
				{
					Source: migrationv1alpha1.NamespacedName{
						Namespace: "vmware-system-net-staging",
						Name:      "ipaa-vip",
					},
					Target: migrationv1alpha1.NamespacedName{
						Namespace: "tenant-ns",
						Name:      "ipaa-vip",
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sourceCR, targetCR, transitionCR).
		WithStatusSubresource(&v1alpha1.IPAddressAllocation{}, &migrationv1alpha1.NetworkResourceTransition{}).
		Build()

	mockVPC := &mockservices.MockVPCServiceProvider{}
	mockVPC.On("GetLBProvider").Return(servicecommon.NSXLB, nil)

	mockIPAA := &mockservices.MockIPAddressAllocationProvider{}
	adoptedAlloc := &model.VpcIpAddressAllocation{
		Id:            stringPtr("alloc-nsx-1"),
		Path:          stringPtr("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-nsx-1"),
		AllocationIps: stringPtr("192.168.10.50"),
		Tags: []model.Tag{
			{Scope: stringPtr(servicecommon.TagScopeIPAddressAllocationCRUID), Tag: stringPtr(string(targetCR.UID))},
			{Scope: stringPtr(servicecommon.TagScopeNamespace), Tag: stringPtr(targetCR.Namespace)},
			{Scope: stringPtr(servicecommon.TagScopeIPAddressAllocationCRName), Tag: stringPtr(targetCR.Name)},
		},
	}
	// NSX tags were already adopted before the crash, so searching by target UID returns the adopted allocation
	mockIPAA.On("GetIPAddressAllocationForTransition", types.UID("target-uid-456"), "tenant-ns", "ipaa-vip").
		Return(adoptedAlloc, nil)

	mockIPAA.On("GetIPAddressAllocationForTransition", sourceCR.UID, sourceCR.Namespace, sourceCR.Name).Return(nil, nil)
	r := &NetworkResourceTransitionReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Service:    mockIPAA,
		VPCService: mockVPC,
		Recorder:   record.NewFakeRecorder(10),
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-transition"},
	})
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)

	// Verify transition succeeded
	updated := &migrationv1alpha1.NetworkResourceTransition{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-transition"}, updated)
	assert.NoError(t, err)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseSucceeded, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseSucceeded, updated.Status.IPAddressAllocationMoves[0].Phase)

	// Verify target CR status was recovered
	updatedTarget := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "tenant-ns", Name: "ipaa-vip"}, updatedTarget)
	assert.NoError(t, err)
	assert.Equal(t, "192.168.10.50", updatedTarget.Status.AllocationIPs)

	// Verify source CR was reaped
	reapedSource := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "vmware-system-net-staging", Name: "ipaa-vip"}, reapedSource)
	assert.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestReconcile_Recovery_RestartBeforeNSXAdoption(t *testing.T) {
	scheme := setupScheme()
	sourceCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipaa-vip",
			Namespace: "vmware-system-net-staging",
			UID:       types.UID("source-uid-123"),
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			UsedFor: v1alpha1.UsedForLBFrontend,
		},
		Status: v1alpha1.IPAddressAllocationStatus{
			AllocationIPs: "192.168.10.50",
		},
	}

	targetCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "ipaa-vip",
			Namespace:   "tenant-ns",
			UID:         types.UID("target-uid-456"),
			Annotations: map[string]string{servicecommon.AnnotationTransitionTarget: "true"},
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			UsedFor: v1alpha1.UsedForLBFrontend,
		},
		Status: v1alpha1.IPAddressAllocationStatus{},
	}

	targetNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-ns",
			UID:  types.UID("tenant-ns-uid-456"),
		},
	}

	transitionCR := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-transition",
		},
		Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
			IPAddressAllocationMoves: []migrationv1alpha1.IPAddressAllocationMoveSpec{
				{
					Source: migrationv1alpha1.NamespacedName{
						Namespace: "vmware-system-net-staging",
						Name:      "ipaa-vip",
					},
					Target: migrationv1alpha1.NamespacedName{
						Namespace: "tenant-ns",
						Name:      "ipaa-vip",
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sourceCR, targetCR, targetNamespace, transitionCR).
		WithStatusSubresource(&v1alpha1.IPAddressAllocation{}, &migrationv1alpha1.NetworkResourceTransition{}).
		Build()

	mockVPC := &mockservices.MockVPCServiceProvider{}
	mockVPC.On("GetLBProvider").Return(servicecommon.NSXLB, nil)

	mockIPAA := &mockservices.MockIPAddressAllocationProvider{}
	// NSX tags were NOT adopted yet, so searching by target UID returns nil
	mockIPAA.On("GetIPAddressAllocationForTransition", types.UID("target-uid-456"), "tenant-ns", "ipaa-vip").
		Return(nil, nil)

	existingAlloc := &model.VpcIpAddressAllocation{
		AllocationIps: stringPtr("192.168.10.50"),
		Id:            stringPtr("alloc-nsx-1"),
		Path:          stringPtr("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-nsx-1"),
	}
	mockIPAA.On("GetIPAddressAllocationForTransition", types.UID("source-uid-123"), "vmware-system-net-staging", "ipaa-vip").
		Return(existingAlloc, nil)

	adoptedAlloc := &model.VpcIpAddressAllocation{
		AllocationIps: stringPtr("192.168.10.50"),
		Id:            stringPtr("alloc-nsx-1"),
		Path:          stringPtr("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-nsx-1"),
	}
	// Should adopt tags onto existing targetCR.UID without re-creating targetCR
	mockIPAA.On("AdoptIPAddressAllocationTags", existingAlloc, "tenant-ns", "tenant-ns-uid-456", "ipaa-vip", types.UID("target-uid-456")).
		Return(adoptedAlloc, nil)

	r := &NetworkResourceTransitionReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Service:    mockIPAA,
		VPCService: mockVPC,
		Recorder:   record.NewFakeRecorder(10),
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-transition"},
	})
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)

	// Verify transition succeeded
	updated := &migrationv1alpha1.NetworkResourceTransition{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-transition"}, updated)
	assert.NoError(t, err)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseSucceeded, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseSucceeded, updated.Status.IPAddressAllocationMoves[0].Phase)

	// Verify target CR status was updated
	updatedTarget := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "tenant-ns", Name: "ipaa-vip"}, updatedTarget)
	assert.NoError(t, err)
	assert.Equal(t, "192.168.10.50", updatedTarget.Status.AllocationIPs)

	// Verify source CR was reaped
	reapedSource := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "vmware-system-net-staging", Name: "ipaa-vip"}, reapedSource)
	assert.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestBuildTargetOwnerReferences(t *testing.T) {
	scheme := setupScheme()
	targetSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-frontend",
			Namespace: "tenant-ns",
			UID:       types.UID("svc-target-uid-1"),
		},
	}
	targetSvcByTargetName := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vip-by-target-name",
			Namespace: "tenant-ns",
			UID:       types.UID("svc-target-uid-2"),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(targetSvc, targetSvcByTargetName).
		Build()

	r := &NetworkResourceTransitionReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	isCtrl := true
	sourceCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "v1",
					Kind:               "Service",
					Name:               "svc-frontend",
					UID:                types.UID("old-source-uid"),
					Controller:         &isCtrl,
					BlockOwnerDeletion: &isCtrl,
				},
				{
					APIVersion: "v1",
					Kind:       "Pod",
					Name:       "some-pod",
				},
			},
		},
	}

	refs, err := r.buildTargetOwnerReferences(context.Background(), sourceCR, migrationv1alpha1.NamespacedName{
		Namespace: "tenant-ns",
		Name:      "vip-target",
	})
	assert.NoError(t, err)
	assert.Len(t, refs, 1)
	assert.Equal(t, "svc-frontend", refs[0].Name)
	assert.Equal(t, types.UID("svc-target-uid-1"), refs[0].UID)
	assert.Equal(t, &isCtrl, refs[0].Controller)
	assert.Equal(t, &isCtrl, refs[0].BlockOwnerDeletion)

	// Fallback to target name match
	sourceCRFallback := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "v1",
					Kind:               "Service",
					Name:               "legacy-nonexistent-svc",
					UID:                types.UID("legacy-uid"),
					Controller:         &isCtrl,
					BlockOwnerDeletion: &isCtrl,
				},
			},
		},
	}
	refsFallback, err := r.buildTargetOwnerReferences(context.Background(), sourceCRFallback, migrationv1alpha1.NamespacedName{
		Namespace: "tenant-ns",
		Name:      "vip-by-target-name",
	})
	assert.NoError(t, err)
	assert.Len(t, refsFallback, 1)
	assert.Equal(t, "vip-by-target-name", refsFallback[0].Name)
	assert.Equal(t, types.UID("svc-target-uid-2"), refsFallback[0].UID)
	assert.Equal(t, &isCtrl, refsFallback[0].Controller)
	assert.Equal(t, &isCtrl, refsFallback[0].BlockOwnerDeletion)
}

func TestEnsureTargetCR(t *testing.T) {
	scheme := setupScheme()
	existingTarget := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-target",
			Namespace: "tenant-ns",
			UID:       types.UID("existing-uid"),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existingTarget).
		Build()

	r := &NetworkResourceTransitionReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	sourceCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"env": "prod"},
		},
		Spec: v1alpha1.IPAddressAllocationSpec{
			UsedFor:     v1alpha1.UsedForLBFrontend,
			IPBlockName: "ipblock-infra",
		},
	}

	existingTarget.Spec = sourceCR.Spec
	existingTarget.Annotations = map[string]string{servicecommon.AnnotationTransitionTarget: "true"}
	// 1. Returns existing target when targetExists is true
	targetRefExisting := migrationv1alpha1.NamespacedName{
		Namespace: "tenant-ns",
		Name:      "existing-target",
	}
	gotExisting, err := r.ensureTargetCR(context.Background(), sourceCR, targetRefExisting, true, existingTarget)
	assert.NoError(t, err)
	assert.Equal(t, existingTarget, gotExisting)

	// 2. Creates new target when targetExists is false
	targetRefNew := migrationv1alpha1.NamespacedName{
		Namespace: "tenant-ns",
		Name:      "new-target-cr",
	}
	gotCreated, err := r.ensureTargetCR(context.Background(), sourceCR, targetRefNew, false, nil)
	assert.NoError(t, err)
	assert.Equal(t, "new-target-cr", gotCreated.Name)
	assert.Equal(t, "tenant-ns", gotCreated.Namespace)
	assert.Equal(t, "prod", gotCreated.Labels["env"])
	assert.Equal(t, "true", gotCreated.Annotations[servicecommon.AnnotationTransitionTarget])
	assert.Equal(t, v1alpha1.UsedForLBFrontend, gotCreated.Spec.UsedFor)
	assert.Equal(t, "ipblock-infra", gotCreated.Spec.IPBlockName)

	// 3. Reject a target that entered deletion before it was returned to adoption.
	deletingTarget := existingTarget.DeepCopy()
	now := metav1.Now()
	deletingTarget.DeletionTimestamp = &now
	gotDeleting, err := r.ensureTargetCR(context.Background(), sourceCR, targetRefExisting, true, deletingTarget)
	assert.ErrorContains(t, err, "being deleted")
	assert.Nil(t, gotDeleting)
}

func TestEnsureTargetCR_ValidatesPersistedTarget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(*v1alpha1.IPAddressAllocation)
		wantError string
	}{
		{name: "awaiting_adoption"},
		{name: "missing_annotation", mutate: func(target *v1alpha1.IPAddressAllocation) {
			target.Annotations = nil
		}, wantError: "not awaiting transition adoption"},
		{name: "conflicting_spec", mutate: func(target *v1alpha1.IPAddressAllocation) {
			target.Spec.AllocationSize++
		}, wantError: "spec conflicts with source"},
		{name: "deleting", mutate: func(target *v1alpha1.IPAddressAllocation) {
			now := metav1.Now()
			target.DeletionTimestamp = &now
			target.Finalizers = []string{"test.vmware.com/hold"}
		}, wantError: "being deleted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &v1alpha1.IPAddressAllocation{Spec: v1alpha1.IPAddressAllocationSpec{UsedFor: v1alpha1.UsedForLBFrontend, AllocationSize: 1}}
			target := &v1alpha1.IPAddressAllocation{
				ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "target", UID: "target-uid", Annotations: map[string]string{servicecommon.AnnotationTransitionTarget: "true"}},
				Spec:       source.Spec,
			}
			if tc.mutate != nil {
				tc.mutate(target)
			}
			kube := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(target).Build()
			r := &NetworkResourceTransitionReconciler{Client: kube}
			// Simulate another writer creating the target after our initial GET.
			got, err := r.ensureTargetCR(context.Background(), source, migrationv1alpha1.NamespacedName{Namespace: target.Namespace, Name: target.Name}, false, nil)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, target.UID, got.UID)
		})
	}
}

func TestAdoptNSXAllocationTags(t *testing.T) {
	scheme := setupScheme()
	targetNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-ns",
			UID:  types.UID("ns-uid-999"),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(targetNs).
		Build()

	mockIPAA := &mockservices.MockIPAddressAllocationProvider{}
	nsxAlloc := &model.VpcIpAddressAllocation{
		Id: stringPtr("alloc-1"),
	}
	mockIPAA.On("AdoptIPAddressAllocationTags", nsxAlloc, "tenant-ns", "ns-uid-999", "target-cr", types.UID("target-uid-888")).
		Return(&model.VpcIpAddressAllocation{}, nil)

	r := &NetworkResourceTransitionReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Service: mockIPAA,
	}

	targetCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID("target-uid-888"),
		},
	}

	err := r.adoptNSXAllocationTags(context.Background(), nsxAlloc, migrationv1alpha1.NamespacedName{
		Namespace: "tenant-ns",
		Name:      "target-cr",
	}, targetCR)
	assert.NoError(t, err)
}

func TestUpdateTargetStatus(t *testing.T) {
	scheme := setupScheme()
	targetCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target-cr",
			Namespace: "tenant-ns",
			Annotations: map[string]string{
				servicecommon.AnnotationTransitionTarget: "true",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(targetCR).
		WithStatusSubresource(&v1alpha1.IPAddressAllocation{}).
		Build()

	r := &NetworkResourceTransitionReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	err := r.updateTargetStatus(context.Background(), targetCR, "10.0.0.123")
	assert.NoError(t, err)

	updated := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "tenant-ns", Name: "target-cr"}, updated)
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.123", updated.Status.AllocationIPs)
	assert.Len(t, updated.Status.Conditions, 1)
	assert.Equal(t, v1alpha1.Ready, updated.Status.Conditions[0].Type)
	assert.Equal(t, corev1.ConditionTrue, updated.Status.Conditions[0].Status)
	assert.Equal(t, "IPAddressAllocationReady", updated.Status.Conditions[0].Reason)
	// Verify that the transient transition annotation was stripped upon status realization
	assert.NotContains(t, updated.Annotations, servicecommon.AnnotationTransitionTarget)
}

func TestRemoveTargetTransitionAnnotation(t *testing.T) {
	scheme := setupScheme()

	// 1. Target with annotation gets it removed
	targetWithAnno := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "with-anno",
			Namespace: "tenant-ns",
			Annotations: map[string]string{
				servicecommon.AnnotationTransitionTarget: "true",
				"other-key":                              "other-val",
			},
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(targetWithAnno).
		Build()

	r := &NetworkResourceTransitionReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	err := r.removeTargetTransitionAnnotation(context.Background(), targetWithAnno)
	assert.NoError(t, err)

	updated := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "tenant-ns", Name: "with-anno"}, updated)
	assert.NoError(t, err)
	assert.NotContains(t, updated.Annotations, servicecommon.AnnotationTransitionTarget)
	assert.Equal(t, "other-val", updated.Annotations["other-key"])

	// 2. Target without annotation is a no-op
	targetWithoutAnno := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "without-anno",
			Namespace: "tenant-ns",
		},
	}
	assert.NoError(t, fakeClient.Create(context.Background(), targetWithoutAnno))
	err = r.removeTargetTransitionAnnotation(context.Background(), targetWithoutAnno)
	assert.NoError(t, err)

	// 3. A missing target must not allow source cleanup.
	nonExistent := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "does-not-exist",
			Namespace: "tenant-ns",
		},
	}
	err = r.removeTargetTransitionAnnotation(context.Background(), nonExistent)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestReapSourceCR(t *testing.T) {
	scheme := setupScheme()
	sourceCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "source-cr",
			Namespace: "staging-ns",
			UID:       types.UID("source-uid"),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sourceCR).
		Build()

	r := &NetworkResourceTransitionReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// 1. Existing source CR is deleted
	err := r.reapSourceCR(context.Background(), migrationv1alpha1.NamespacedName{
		Namespace: "staging-ns",
		Name:      "source-cr",
	}, types.UID("source-uid"))
	assert.NoError(t, err)

	check := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "staging-ns", Name: "source-cr"}, check)
	assert.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err))

	// 2. Non-existent source CR does not return error
	err = r.reapSourceCR(context.Background(), migrationv1alpha1.NamespacedName{
		Namespace: "staging-ns",
		Name:      "already-gone",
	}, types.UID("source-uid"))
	assert.NoError(t, err)
}

func TestUpdateTransitionStatus_Scenarios(t *testing.T) {
	scheme := setupScheme()

	// Scenario 1: All succeeded
	transitionCR1 := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "t1"},
	}
	fakeClient1 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(transitionCR1).WithStatusSubresource(&migrationv1alpha1.NetworkResourceTransition{}).Build()
	r1 := &NetworkResourceTransitionReconciler{Client: fakeClient1, Scheme: scheme}
	res1, err1 := r1.updateTransitionStatus(context.Background(), transitionCR1, []migrationv1alpha1.IPAddressAllocationMoveStatus{{Phase: migrationv1alpha1.TransitionPhaseSucceeded}}, false)
	assert.NoError(t, err1)
	assert.Zero(t, res1.RequeueAfter)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseSucceeded, transitionCR1.Status.Phase)
	assert.Equal(t, corev1.ConditionTrue, transitionCR1.Status.Conditions[0].Status)

	// Scenario 2: All failed
	transitionCR2 := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "t2"},
	}
	fakeClient2 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(transitionCR2).WithStatusSubresource(&migrationv1alpha1.NetworkResourceTransition{}).Build()
	r2 := &NetworkResourceTransitionReconciler{Client: fakeClient2, Scheme: scheme}
	res2, err2 := r2.updateTransitionStatus(context.Background(), transitionCR2, []migrationv1alpha1.IPAddressAllocationMoveStatus{{Phase: migrationv1alpha1.TransitionPhaseFailed}}, false)
	assert.NoError(t, err2)
	assert.Zero(t, res2.RequeueAfter)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseFailed, transitionCR2.Status.Phase)
	assert.Equal(t, corev1.ConditionFalse, transitionCR2.Status.Conditions[0].Status)

	// Scenario 3: Partially failed
	transitionCR3 := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "t3"},
	}
	fakeClient3 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(transitionCR3).WithStatusSubresource(&migrationv1alpha1.NetworkResourceTransition{}).Build()
	r3 := &NetworkResourceTransitionReconciler{Client: fakeClient3, Scheme: scheme}
	res3, err3 := r3.updateTransitionStatus(context.Background(), transitionCR3, []migrationv1alpha1.IPAddressAllocationMoveStatus{
		{Phase: migrationv1alpha1.TransitionPhaseSucceeded},
		{Phase: migrationv1alpha1.TransitionPhaseFailed},
	}, false)
	assert.NoError(t, err3)
	assert.Zero(t, res3.RequeueAfter)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseFailed, transitionCR3.Status.Phase)
	assert.Equal(t, corev1.ConditionFalse, transitionCR3.Status.Conditions[0].Status)

	// Scenario 4: Need requeue (in-progress)
	transitionCR4 := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "t4"},
	}
	fakeClient4 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(transitionCR4).WithStatusSubresource(&migrationv1alpha1.NetworkResourceTransition{}).Build()
	r4 := &NetworkResourceTransitionReconciler{Client: fakeClient4, Scheme: scheme}
	res4, err4 := r4.updateTransitionStatus(context.Background(), transitionCR4, []migrationv1alpha1.IPAddressAllocationMoveStatus{{Phase: migrationv1alpha1.TransitionPhaseInProgress}}, true)
	assert.NoError(t, err4)
	assert.Equal(t, 5*time.Second, res4.RequeueAfter)
	assert.Equal(t, migrationv1alpha1.TransitionPhaseInProgress, transitionCR4.Status.Phase)
}

func TestUpdateTransitionStatus_ReadyTransitions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		oldReady  corev1.ConditionStatus
		items     []migrationv1alpha1.IPAddressAllocationMoveStatus
		succeeded int
		failed    int
		requeue   bool
		phase     migrationv1alpha1.TransitionPhase
		ready     corev1.ConditionStatus
		reason    string
	}{
		{"success_to_migrating", corev1.ConditionTrue, []migrationv1alpha1.IPAddressAllocationMoveStatus{{Phase: migrationv1alpha1.TransitionPhaseInProgress}}, 0, 0, true, migrationv1alpha1.TransitionPhaseInProgress, corev1.ConditionFalse, "MigrationInProgress"},
		{"failure_to_migrating", corev1.ConditionFalse, []migrationv1alpha1.IPAddressAllocationMoveStatus{{Phase: migrationv1alpha1.TransitionPhaseInProgress}}, 0, 0, true, migrationv1alpha1.TransitionPhaseInProgress, corev1.ConditionFalse, "MigrationInProgress"},
		{"success_to_failed", corev1.ConditionTrue, []migrationv1alpha1.IPAddressAllocationMoveStatus{{Phase: migrationv1alpha1.TransitionPhaseFailed}}, 0, 1, false, migrationv1alpha1.TransitionPhaseFailed, corev1.ConditionFalse, "MigrationFailed"},
		{"success_to_partially_failed", corev1.ConditionTrue, []migrationv1alpha1.IPAddressAllocationMoveStatus{{Phase: migrationv1alpha1.TransitionPhaseSucceeded}, {Phase: migrationv1alpha1.TransitionPhaseFailed}}, 1, 1, false, migrationv1alpha1.TransitionPhaseFailed, corev1.ConditionFalse, "MigrationPartiallyFailed"},
		{"failure_to_success", corev1.ConditionFalse, []migrationv1alpha1.IPAddressAllocationMoveStatus{{Phase: migrationv1alpha1.TransitionPhaseSucceeded}}, 1, 0, false, migrationv1alpha1.TransitionPhaseSucceeded, corev1.ConditionTrue, "AllResourcesMigrated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldTime := metav1.NewTime(time.Unix(1000, 0).UTC())
			other := v1alpha1.Condition{Type: "Other", Status: corev1.ConditionTrue, Reason: "Preserved", LastTransitionTime: oldTime}
			transition := &migrationv1alpha1.NetworkResourceTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "transition", Generation: 2},
				Status: migrationv1alpha1.NetworkResourceTransitionStatus{Conditions: []v1alpha1.Condition{
					{Type: "Ready", Status: tc.oldReady, Reason: "PreviousResult", Message: "previous result", LastTransitionTime: oldTime}, other,
				}},
			}
			kube := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(transition).WithStatusSubresource(transition).Build()
			r := &NetworkResourceTransitionReconciler{Client: kube}
			_, err := r.updateTransitionStatus(context.Background(), transition, tc.items, tc.requeue)
			require.NoError(t, err)
			updated := &migrationv1alpha1.NetworkResourceTransition{}
			require.NoError(t, kube.Get(context.Background(), types.NamespacedName{Name: transition.Name}, updated))
			assert.Equal(t, tc.phase, updated.Status.Phase)
			assert.Equal(t, tc.succeeded, updated.Status.SucceededResources)
			assert.Equal(t, tc.failed, updated.Status.FailedResources)
			ready := findTransitionCondition(updated.Status.Conditions, "Ready")
			require.NotNil(t, ready)
			assert.Equal(t, tc.ready, ready.Status)
			assert.Equal(t, tc.reason, ready.Reason)
			assert.NotEqual(t, "previous result", ready.Message)
			assert.Equal(t, int64(2), updated.Status.ObservedGeneration)
			assert.True(t, apiequality.Semantic.DeepEqual(&other, findTransitionCondition(updated.Status.Conditions, "Other")))
			if tc.oldReady == tc.ready {
				assert.True(t, oldTime.Equal(&ready.LastTransitionTime))
			} else {
				assert.True(t, ready.LastTransitionTime.After(oldTime.Time))
			}

			// Identical status must neither write again nor change the transition time.
			previous := updated.DeepCopy()
			_, err = r.updateTransitionStatus(context.Background(), updated, tc.items, tc.requeue)
			require.NoError(t, err)
			require.NoError(t, kube.Get(context.Background(), types.NamespacedName{Name: transition.Name}, updated))
			assert.Equal(t, previous.ResourceVersion, updated.ResourceVersion)
			assert.Equal(t, previous.Status, updated.Status)

			// Item diagnostics must still be saved when Ready itself is unchanged.
			items := append([]migrationv1alpha1.IPAddressAllocationMoveStatus(nil), tc.items...)
			items[0].Message = "updated item diagnostics"
			_, err = r.updateTransitionStatus(context.Background(), updated, items, tc.requeue)
			require.NoError(t, err)
			require.NoError(t, kube.Get(context.Background(), types.NamespacedName{Name: transition.Name}, updated))
			assert.Equal(t, "updated item diagnostics", updated.Status.IPAddressAllocationMoves[0].Message)
		})
	}
}

func TestUpdateTransitionStatus_MixedProgressWithFailure(t *testing.T) {
	transition := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "mixed-transition", Generation: 1},
	}
	kube := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(transition).
		WithStatusSubresource(transition).Build()
	r := &NetworkResourceTransitionReconciler{Client: kube}
	moves := []migrationv1alpha1.IPAddressAllocationMoveStatus{
		{Phase: migrationv1alpha1.TransitionPhaseSucceeded},
		{Phase: migrationv1alpha1.TransitionPhaseFailed},
		{Phase: migrationv1alpha1.TransitionPhaseInProgress},
	}

	result, err := r.updateTransitionStatus(context.Background(), transition, moves, true)
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, result.RequeueAfter)

	updated := &migrationv1alpha1.NetworkResourceTransition{}
	require.NoError(t, kube.Get(context.Background(), types.NamespacedName{Name: transition.Name}, updated))
	assert.Equal(t, migrationv1alpha1.TransitionPhaseInProgress, updated.Status.Phase)
	assert.Equal(t, 1, updated.Status.SucceededResources)
	assert.Equal(t, 1, updated.Status.FailedResources)
	ready := findTransitionCondition(updated.Status.Conditions, "Ready")
	require.NotNil(t, ready)
	assert.Equal(t, corev1.ConditionFalse, ready.Status)
	assert.Equal(t, "MigrationInProgress", ready.Reason)
	assert.Equal(t, "Migration is still in progress; 1 succeeded, 1 failed out of 3 resources", ready.Message)
}

func TestReconcileMoveItem_SourceMissingQuarantinesPendingTarget(t *testing.T) {
	for _, allocationIP := range []string{"", "192.0.2.10"} {
		t.Run("target status allocationIP="+allocationIP, func(t *testing.T) {
			target := &v1alpha1.IPAddressAllocation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "target",
					Namespace: "tenant",
					UID:       types.UID("target-uid"),
					Annotations: map[string]string{
						servicecommon.AnnotationTransitionTarget: "true",
					},
				},
				Status: v1alpha1.IPAddressAllocationStatus{AllocationIPs: allocationIP},
			}
			kube := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(target).
				WithStatusSubresource(target).Build()
			mockIPAA := &mockservices.MockIPAddressAllocationProvider{}
			mockIPAA.On("GetIPAddressAllocationForTransition", target.UID, target.Namespace, target.Name).Return(nil, nil)
			r := &NetworkResourceTransitionReconciler{Client: kube, Service: mockIPAA}
			move := migrationv1alpha1.IPAddressAllocationMoveSpec{
				Source: migrationv1alpha1.NamespacedName{Namespace: "staging", Name: "source"},
				Target: migrationv1alpha1.NamespacedName{Namespace: target.Namespace, Name: target.Name},
			}

			status, requeue := r.reconcileMoveItem(context.Background(), move)

			assert.False(t, requeue)
			assert.Equal(t, migrationv1alpha1.TransitionPhaseFailed, status.Phase)
			assert.Contains(t, status.Message, "Source IPAddressAllocation staging/source not found")
			updated := &v1alpha1.IPAddressAllocation{}
			require.NoError(t, kube.Get(context.Background(), types.NamespacedName{Namespace: target.Namespace, Name: target.Name}, updated))
			assert.Equal(t, "true", updated.Annotations[servicecommon.AnnotationTransitionTarget])
			assert.Empty(t, updated.Status.AllocationIPs)
			condition := findTransitionCondition(updated.Status.Conditions, v1alpha1.Ready)
			require.NotNil(t, condition)
			assert.Equal(t, corev1.ConditionFalse, condition.Status)
			assert.Equal(t, "TransitionSourceMissing", condition.Reason)
			assert.Contains(t, condition.Message, "target was not adopted and remains quarantined")
			mockIPAA.AssertExpectations(t)
		})
	}
}

func TestUpdateTransitionStatus_PreservesPortOperations(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		name := "cutover"
		if reverse {
			name = "revert"
		}
		t.Run(name, func(t *testing.T) {
			reservation := migrationv1alpha1.NamespacedName{Namespace: "staging", Name: "reservation"}
			port := migrationv1alpha1.NamespacedName{Namespace: "tenant", Name: "port"}
			transition := &migrationv1alpha1.NetworkResourceTransition{
				ObjectMeta: metav1.ObjectMeta{Name: "transition", Generation: 3},
				Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
					IPAddressAllocationMoves: []migrationv1alpha1.IPAddressAllocationMoveSpec{{
						Source: migrationv1alpha1.NamespacedName{Namespace: "staging", Name: "vip"},
						Target: migrationv1alpha1.NamespacedName{Namespace: "tenant", Name: "vip"},
					}},
				},
			}
			if reverse {
				transition.Spec.SubnetPortReverts = []migrationv1alpha1.SubnetPortRevertSpec{{SubnetPort: port, SubnetIPReservation: reservation, ReservedIPs: []string{"192.0.2.10"}}}
			} else {
				transition.Spec.SubnetPortCutovers = []migrationv1alpha1.SubnetPortCutoverSpec{{SubnetPort: port, SubnetIPReservation: reservation}}
			}
			kube := fake.NewClientBuilder().WithScheme(setupScheme()).WithObjects(transition).WithStatusSubresource(transition).Build()
			r := &NetworkResourceTransitionReconciler{Client: kube}
			move := migrationv1alpha1.IPAddressAllocationMoveStatus{
				Source: transition.Spec.IPAddressAllocationMoves[0].Source,
				Target: transition.Spec.IPAddressAllocationMoves[0].Target,
				Phase:  migrationv1alpha1.TransitionPhaseSucceeded,
			}
			result, err := r.updateTransitionStatus(context.Background(), transition, []migrationv1alpha1.IPAddressAllocationMoveStatus{move}, false)
			require.NoError(t, err)
			assert.Equal(t, 5*time.Second, result.RequeueAfter)
			assert.Equal(t, migrationv1alpha1.TransitionPhaseInProgress, transition.Status.Phase)
			assert.Equal(t, 2, transition.Status.TotalResources)
			assert.Equal(t, 1, transition.Status.SucceededResources)
			assert.Equal(t, int64(3), transition.Status.ObservedGeneration)
			assert.Equal(t, corev1.ConditionFalse, findTransitionCondition(transition.Status.Conditions, v1alpha1.Ready).Status)

			if reverse {
				transition.Status.SubnetPortReverts = []migrationv1alpha1.SubnetPortRevertStatus{{SubnetPort: port, SubnetIPReservation: &reservation, ReservedIPs: []string{"192.0.2.10"}, Phase: migrationv1alpha1.TransitionPhaseSucceeded}}
			} else {
				transition.Status.SubnetPortCutovers = []migrationv1alpha1.SubnetPortCutoverStatus{{SubnetPort: port, SubnetIPReservation: reservation, Phase: migrationv1alpha1.TransitionPhaseSucceeded}}
			}
			require.NoError(t, kube.Status().Update(context.Background(), transition))
			portStatus := transition.Status.DeepCopy()
			result, err = r.updateTransitionStatus(context.Background(), transition, []migrationv1alpha1.IPAddressAllocationMoveStatus{move}, false)
			require.NoError(t, err)
			assert.Zero(t, result.RequeueAfter)
			assert.Equal(t, migrationv1alpha1.TransitionPhaseSucceeded, transition.Status.Phase)
			assert.Equal(t, 2, transition.Status.SucceededResources)
			assert.Equal(t, portStatus.SubnetPortCutovers, transition.Status.SubnetPortCutovers)
			assert.Equal(t, portStatus.SubnetPortReverts, transition.Status.SubnetPortReverts)
			assert.Equal(t, corev1.ConditionTrue, findTransitionCondition(transition.Status.Conditions, v1alpha1.Ready).Status)
		})
	}
}

func findTransitionCondition(conditions []v1alpha1.Condition, conditionType v1alpha1.ConditionType) *v1alpha1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
