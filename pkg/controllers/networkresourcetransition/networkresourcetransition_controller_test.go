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
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
			MoveIPAddressAllocation: []migrationv1alpha1.MoveIPAddressAllocationSpec{
				{
					Source: migrationv1alpha1.NamespacedObjectReference{
						Namespace: "vmware-system-net-staging",
						Name:      "test-ipaa",
					},
					Target: migrationv1alpha1.NamespacedObjectReference{
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

	// Since LB provider is not NSXLB, operator must ignore the CR and not modify its status
	updated := &migrationv1alpha1.NetworkResourceTransition{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-transition"}, updated)
	assert.NoError(t, err)
	assert.Empty(t, updated.Status.Phase)
	assert.Empty(t, updated.Status.MoveIPAddressAllocation)
}

func TestReconcile_GetLBProviderError(t *testing.T) {
	scheme := setupScheme()
	transitionCR := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-transition",
		},
		Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
			MoveIPAddressAllocation: []migrationv1alpha1.MoveIPAddressAllocationSpec{
				{
					Source: migrationv1alpha1.NamespacedObjectReference{
						Namespace: "vmware-system-net-staging",
						Name:      "test-ipaa",
					},
					Target: migrationv1alpha1.NamespacedObjectReference{
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
	assert.Error(t, err)
	assert.True(t, res.RequeueAfter > 0)
}

func TestReconcile_SourceNotFound(t *testing.T) {
	scheme := setupScheme()
	transitionCR := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-transition",
		},
		Spec: migrationv1alpha1.NetworkResourceTransitionSpec{
			MoveIPAddressAllocation: []migrationv1alpha1.MoveIPAddressAllocationSpec{
				{
					Source: migrationv1alpha1.NamespacedObjectReference{
						Namespace: "vmware-system-net-staging",
						Name:      "missing-source",
					},
					Target: migrationv1alpha1.NamespacedObjectReference{
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
	assert.Equal(t, migrationv1alpha1.MigrationPhaseFailed, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.ItemPhaseFailed, updated.Status.MoveIPAddressAllocation[0].Phase)
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
			MoveIPAddressAllocation: []migrationv1alpha1.MoveIPAddressAllocationSpec{
				{
					Source: migrationv1alpha1.NamespacedObjectReference{
						Namespace: "vmware-system-net-staging",
						Name:      "ipaa-vip",
					},
					Target: migrationv1alpha1.NamespacedObjectReference{
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
	assert.Equal(t, migrationv1alpha1.MigrationPhaseMigrating, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.ItemPhaseMigrating, updated.Status.MoveIPAddressAllocation[0].Phase)
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
			UsedFor: v1alpha1.UsedForLBFrontend,
			IPBlock: "ipblock-public",
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
			MoveIPAddressAllocation: []migrationv1alpha1.MoveIPAddressAllocationSpec{
				{
					Source: migrationv1alpha1.NamespacedObjectReference{
						Namespace: "vmware-system-net-staging",
						Name:      "ipaa-vip",
					},
					Target: migrationv1alpha1.NamespacedObjectReference{
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
		Id:   stringPtr("alloc-nsx-1"),
		Path: stringPtr("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-nsx-1"),
	}
	mockIPAA.On("GetIPAddressAllocationForTransition", types.UID("source-uid-123"), "vmware-system-net-staging", "ipaa-vip").
		Return(existingAlloc, nil)

	adoptedAlloc := &model.VpcIpAddressAllocation{
		Id:   stringPtr("alloc-nsx-1"),
		Path: stringPtr("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-nsx-1"),
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
	assert.Equal(t, migrationv1alpha1.MigrationPhaseSucceeded, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.ItemPhaseSucceeded, updated.Status.MoveIPAddressAllocation[0].Phase)

	// Check target CR created
	targetCR := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "tenant-ns", Name: "ipaa-vip"}, targetCR)
	assert.NoError(t, err)
	assert.Equal(t, "192.168.10.50", targetCR.Status.AllocationIPs)
	assert.Equal(t, "ipblock-public", targetCR.Spec.IPBlock)
	assert.Equal(t, v1alpha1.UsedForLBFrontend, targetCR.Spec.UsedFor)
	assert.Len(t, targetCR.OwnerReferences, 1)
	assert.Equal(t, types.UID("new-svc-uid-789"), targetCR.OwnerReferences[0].UID)

	// Check source CR reaped
	srcCRCheck := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "vmware-system-net-staging", Name: "ipaa-vip"}, srcCRCheck)
	assert.Error(t, err)
}

func TestReconcile_Idempotent_AlreadyTransitioned(t *testing.T) {
	scheme := setupScheme()
	targetCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipaa-vip",
			Namespace: "tenant-ns",
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
			MoveIPAddressAllocation: []migrationv1alpha1.MoveIPAddressAllocationSpec{
				{
					Source: migrationv1alpha1.NamespacedObjectReference{
						Namespace: "vmware-system-net-staging",
						Name:      "ipaa-vip",
					},
					Target: migrationv1alpha1.NamespacedObjectReference{
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
		WithStatusSubresource(&migrationv1alpha1.NetworkResourceTransition{}).
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
	assert.Zero(t, res.RequeueAfter)

	updated := &migrationv1alpha1.NetworkResourceTransition{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-transition"}, updated)
	assert.NoError(t, err)
	assert.Equal(t, migrationv1alpha1.MigrationPhaseSucceeded, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.ItemPhaseSucceeded, updated.Status.MoveIPAddressAllocation[0].Phase)
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
			MoveIPAddressAllocation: []migrationv1alpha1.MoveIPAddressAllocationSpec{
				{
					Source: migrationv1alpha1.NamespacedObjectReference{
						Namespace: "vmware-system-net-staging",
						Name:      "ipaa-vip",
					},
					Target: migrationv1alpha1.NamespacedObjectReference{
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
	}
	// NSX tags were already adopted before the crash, so searching by target UID returns the adopted allocation
	mockIPAA.On("GetIPAddressAllocationForTransition", types.UID("target-uid-456"), "tenant-ns", "ipaa-vip").
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
	assert.Equal(t, migrationv1alpha1.MigrationPhaseSucceeded, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.ItemPhaseSucceeded, updated.Status.MoveIPAddressAllocation[0].Phase)

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
			Name:      "ipaa-vip",
			Namespace: "tenant-ns",
			UID:       types.UID("target-uid-456"),
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
			MoveIPAddressAllocation: []migrationv1alpha1.MoveIPAddressAllocationSpec{
				{
					Source: migrationv1alpha1.NamespacedObjectReference{
						Namespace: "vmware-system-net-staging",
						Name:      "ipaa-vip",
					},
					Target: migrationv1alpha1.NamespacedObjectReference{
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
		Id:   stringPtr("alloc-nsx-1"),
		Path: stringPtr("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-nsx-1"),
	}
	mockIPAA.On("GetIPAddressAllocationForTransition", types.UID("source-uid-123"), "vmware-system-net-staging", "ipaa-vip").
		Return(existingAlloc, nil)

	adoptedAlloc := &model.VpcIpAddressAllocation{
		Id:   stringPtr("alloc-nsx-1"),
		Path: stringPtr("/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-nsx-1"),
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
	assert.Equal(t, migrationv1alpha1.MigrationPhaseSucceeded, updated.Status.Phase)
	assert.Equal(t, migrationv1alpha1.ItemPhaseSucceeded, updated.Status.MoveIPAddressAllocation[0].Phase)

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
					APIVersion: "v1",
					Kind:       "Service",
					Name:       "svc-frontend",
					UID:        types.UID("old-source-uid"),
					Controller: &isCtrl,
				},
				{
					APIVersion: "v1",
					Kind:       "Pod",
					Name:       "some-pod",
				},
			},
		},
	}

	refs := r.buildTargetOwnerReferences(context.Background(), sourceCR, migrationv1alpha1.NamespacedObjectReference{
		Namespace: "tenant-ns",
		Name:      "vip-target",
	})
	assert.Len(t, refs, 1)
	assert.Equal(t, "svc-frontend", refs[0].Name)
	assert.Equal(t, types.UID("svc-target-uid-1"), refs[0].UID)
	assert.Equal(t, &isCtrl, refs[0].Controller)

	// Fallback to target name match
	sourceCRFallback := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "Service",
					Name:       "legacy-nonexistent-svc",
					UID:        types.UID("legacy-uid"),
					Controller: &isCtrl,
				},
			},
		},
	}
	refsFallback := r.buildTargetOwnerReferences(context.Background(), sourceCRFallback, migrationv1alpha1.NamespacedObjectReference{
		Namespace: "tenant-ns",
		Name:      "vip-by-target-name",
	})
	assert.Len(t, refsFallback, 1)
	assert.Equal(t, "vip-by-target-name", refsFallback[0].Name)
	assert.Equal(t, types.UID("svc-target-uid-2"), refsFallback[0].UID)
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
			UsedFor: v1alpha1.UsedForLBFrontend,
			IPBlock: "ipblock-infra",
		},
	}

	// 1. Returns existing target when targetExists is true
	targetRefExisting := migrationv1alpha1.NamespacedObjectReference{
		Namespace: "tenant-ns",
		Name:      "existing-target",
	}
	gotExisting, err := r.ensureTargetCR(context.Background(), sourceCR, targetRefExisting, true, existingTarget)
	assert.NoError(t, err)
	assert.Equal(t, existingTarget, gotExisting)

	// 2. Creates new target when targetExists is false
	targetRefNew := migrationv1alpha1.NamespacedObjectReference{
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
	assert.Equal(t, "ipblock-infra", gotCreated.Spec.IPBlock)
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

	err := r.adoptNSXAllocationTags(context.Background(), nsxAlloc, migrationv1alpha1.NamespacedObjectReference{
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
	err = r.removeTargetTransitionAnnotation(context.Background(), targetWithoutAnno)
	assert.NoError(t, err)

	// 3. Non-existent target returns nil without error
	nonExistent := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "does-not-exist",
			Namespace: "tenant-ns",
		},
	}
	err = r.removeTargetTransitionAnnotation(context.Background(), nonExistent)
	assert.NoError(t, err)
}

func TestReapSourceCR(t *testing.T) {
	scheme := setupScheme()
	sourceCR := &v1alpha1.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "source-cr",
			Namespace: "staging-ns",
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
	err := r.reapSourceCR(context.Background(), migrationv1alpha1.NamespacedObjectReference{
		Namespace: "staging-ns",
		Name:      "source-cr",
	})
	assert.NoError(t, err)

	check := &v1alpha1.IPAddressAllocation{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "staging-ns", Name: "source-cr"}, check)
	assert.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err))

	// 2. Non-existent source CR does not return error
	err = r.reapSourceCR(context.Background(), migrationv1alpha1.NamespacedObjectReference{
		Namespace: "staging-ns",
		Name:      "already-gone",
	})
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
	res1, err1 := r1.updateTransitionStatus(context.Background(), transitionCR1, []migrationv1alpha1.MoveIPAddressAllocationStatus{{Phase: migrationv1alpha1.ItemPhaseSucceeded}}, 1, 0, false)
	assert.NoError(t, err1)
	assert.Zero(t, res1.RequeueAfter)
	assert.Equal(t, migrationv1alpha1.MigrationPhaseSucceeded, transitionCR1.Status.Phase)
	assert.Equal(t, metav1.ConditionTrue, transitionCR1.Status.Conditions[0].Status)

	// Scenario 2: All failed
	transitionCR2 := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "t2"},
	}
	fakeClient2 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(transitionCR2).WithStatusSubresource(&migrationv1alpha1.NetworkResourceTransition{}).Build()
	r2 := &NetworkResourceTransitionReconciler{Client: fakeClient2, Scheme: scheme}
	res2, err2 := r2.updateTransitionStatus(context.Background(), transitionCR2, []migrationv1alpha1.MoveIPAddressAllocationStatus{{Phase: migrationv1alpha1.ItemPhaseFailed}}, 0, 1, false)
	assert.NoError(t, err2)
	assert.Zero(t, res2.RequeueAfter)
	assert.Equal(t, migrationv1alpha1.MigrationPhaseFailed, transitionCR2.Status.Phase)
	assert.Equal(t, metav1.ConditionFalse, transitionCR2.Status.Conditions[0].Status)

	// Scenario 3: Partially failed
	transitionCR3 := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "t3"},
	}
	fakeClient3 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(transitionCR3).WithStatusSubresource(&migrationv1alpha1.NetworkResourceTransition{}).Build()
	r3 := &NetworkResourceTransitionReconciler{Client: fakeClient3, Scheme: scheme}
	res3, err3 := r3.updateTransitionStatus(context.Background(), transitionCR3, []migrationv1alpha1.MoveIPAddressAllocationStatus{
		{Phase: migrationv1alpha1.ItemPhaseSucceeded},
		{Phase: migrationv1alpha1.ItemPhaseFailed},
	}, 1, 1, false)
	assert.NoError(t, err3)
	assert.Zero(t, res3.RequeueAfter)
	assert.Equal(t, migrationv1alpha1.MigrationPhasePartiallyFailed, transitionCR3.Status.Phase)
	assert.Equal(t, metav1.ConditionFalse, transitionCR3.Status.Conditions[0].Status)

	// Scenario 4: Need requeue (in-progress)
	transitionCR4 := &migrationv1alpha1.NetworkResourceTransition{
		ObjectMeta: metav1.ObjectMeta{Name: "t4"},
	}
	fakeClient4 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(transitionCR4).WithStatusSubresource(&migrationv1alpha1.NetworkResourceTransition{}).Build()
	r4 := &NetworkResourceTransitionReconciler{Client: fakeClient4, Scheme: scheme}
	res4, err4 := r4.updateTransitionStatus(context.Background(), transitionCR4, []migrationv1alpha1.MoveIPAddressAllocationStatus{{Phase: migrationv1alpha1.ItemPhaseMigrating}}, 0, 0, true)
	assert.NoError(t, err4)
	assert.Equal(t, 5*time.Second, res4.RequeueAfter)
	assert.Equal(t, migrationv1alpha1.MigrationPhaseMigrating, transitionCR4.Status.Phase)
}
