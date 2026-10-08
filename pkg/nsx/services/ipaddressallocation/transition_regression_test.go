/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	migration "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
	vpcapi "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	nrt "github.com/vmware-tanzu/nsx-operator/pkg/controllers/networkresourcetransition"
	mockservices "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// Only API-server behavior is substituted: UID assignment and a rejected DELETE.
type transitionClient struct {
	client.Client
	rejectSourceDelete bool
}

func (c *transitionClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*vpcapi.IPAddressAllocation); ok && obj.GetUID() == "" {
		obj.SetUID("target-uid")
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c *transitionClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if c.rejectSourceDelete && obj.GetNamespace() == "staging" && obj.GetName() == "source" {
		return apierrors.NewForbidden(schema.GroupResource{Group: "crd.nsx.vmware.com", Resource: "ipaddressallocations"}, obj.GetName(), fmt.Errorf("injected source DELETE rejection"))
	}
	return c.Client.Delete(ctx, obj, opts...)
}

type transitionFixture struct {
	service      *IPAddressAllocationService
	kube         *transitionClient
	r            *nrt.NetworkResourceTransitionReconciler
	backend      map[string]model.VpcIpAddressAllocation
	patches      int
	deletes      int
	beforePatch  func()
	statusWrites map[types.NamespacedName]int
}

func transitionScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, vpcapi.AddToScheme(scheme))
	require.NoError(t, migration.AddToScheme(scheme))
	return scheme
}

func transitionSetup(t *testing.T, targetState string, rejectFirstPatch, rejectDelete bool) *transitionFixture {
	t.Helper()
	service, _, api := createIPAddressAllocationService(t)
	scheme := transitionScheme(t)
	spec := vpcapi.IPAddressAllocationSpec{UsedFor: vpcapi.UsedForLBFrontend, IPAddressBlockVisibility: vpcapi.IPAddressVisibilityExternal, AllocationSize: 1, IPAddressType: vpcapi.IPAllocationIPAddressTypeIPv4, IPBlock: ":pool"}
	source := &vpcapi.IPAddressAllocation{ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "staging", UID: "source-uid"}, Spec: spec, Status: vpcapi.IPAddressAllocationStatus{AllocationIPs: "192.0.2.10"}}
	target := &vpcapi.IPAddressAllocation{ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "tenant", UID: "target-uid", Annotations: map[string]string{common.AnnotationTransitionTarget: "true"}}, Spec: spec}
	request := &migration.NetworkResourceTransition{ObjectMeta: metav1.ObjectMeta{Name: "move", UID: "move-uid"}, Spec: migration.NetworkResourceTransitionSpec{MoveIPAddressAllocation: []migration.MoveIPAddressAllocationSpec{{Source: migration.NamespacedObjectReference{Namespace: "staging", Name: "source"}, Target: migration.NamespacedObjectReference{Namespace: "tenant", Name: "target"}}}}}
	objects := []client.Object{source, request, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging", UID: "staging-ns-uid"}}, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: "tenant-ns-uid"}}}
	if targetState == "ready" {
		target.Status.AllocationIPs = "192.0.2.11"
	}
	if targetState != "absent" {
		objects = append(objects, target)
	}
	statusWrites := make(map[types.NamespacedName]int)
	kube := &transitionClient{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&vpcapi.IPAddressAllocation{}, &migration.NetworkResourceTransition{}).WithInterceptorFuncs(interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if subResourceName == "status" {
				statusWrites[client.ObjectKeyFromObject(obj)]++
			}
			return c.SubResource(subResourceName).Update(ctx, obj, opts...)
		},
		SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if subResourceName == "status" {
				statusWrites[client.ObjectKeyFromObject(obj)]++
			}
			return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
		},
	}).Build(), rejectSourceDelete: rejectDelete}
	provider := &mockservices.MockVPCServiceProvider{}
	provider.On("GetLBProvider").Return(common.NSXLB, nil)
	service.Client = kube
	service.VPCService = provider
	f := &transitionFixture{service: service, kube: kube, backend: make(map[string]model.VpcIpAddressAllocation), statusWrites: statusWrites}
	f.r = &nrt.NetworkResourceTransitionReconciler{Client: kube, Scheme: scheme, Service: service, VPCService: provider}
	allocation := func(id, ns, name, uid, ip string) model.VpcIpAddressAllocation {
		parent := "/orgs/default/projects/project/vpcs/shared-lb"
		return model.VpcIpAddressAllocation{Id: String(id), DisplayName: String(name), Path: String(parent + "/ip-address-allocations/" + id), ParentPath: String(parent), AllocationIps: String(ip), AllocationSize: Int64(1), IpAddressBlockVisibility: String("EXTERNAL"), IpAddressType: String("IPV4"), IpBlock: String("/infra/ip-blocks/pool"), Tags: []model.Tag{{Scope: String(common.TagScopeCluster), Tag: String(service.NSXConfig.Cluster)}, {Scope: String(common.TagScopeNamespace), Tag: String(ns)}, {Scope: String(common.TagScopeIPAddressAllocationCRName), Tag: String(name)}, {Scope: String(common.TagScopeIPAddressAllocationCRUID), Tag: String(uid)}, {Scope: String(common.TagScopeIPAllocLB), Tag: String("true")}}}
	}
	sourceAlloc := allocation("source-alloc", "staging", "source", "source-uid", "192.0.2.10")
	require.NoError(t, service.ipAddressAllocationStore.Apply(&sourceAlloc))
	f.backend["source-alloc"] = sourceAlloc
	if targetState == "ready" || targetState == "stale" {
		uid := "target-uid"
		if targetState == "stale" {
			uid = "deleted-target-uid"
		}
		other := allocation("other-alloc", "tenant", "target", uid, "192.0.2.11")
		require.NoError(t, service.ipAddressAllocationStore.Apply(&other))
		f.backend["other-alloc"] = other
	}
	api.EXPECT().Patch("default", "project", "shared-lb", gomock.Any(), gomock.Any()).DoAndReturn(func(_, _, _, id string, value model.VpcIpAddressAllocation) error {
		if f.beforePatch != nil {
			f.beforePatch()
		}
		f.patches++
		if rejectFirstPatch && f.patches == 1 {
			return fmt.Errorf("injected NSX PATCH rejection before persistence")
		}
		f.backend[id] = value
		return nil
	}).AnyTimes()
	api.EXPECT().Get("default", "project", "shared-lb", gomock.Any()).DoAndReturn(func(_, _, _, id string) (model.VpcIpAddressAllocation, error) {
		value, ok := f.backend[id]
		if !ok {
			return model.VpcIpAddressAllocation{}, fmt.Errorf("allocation absent")
		}
		return value, nil
	}).AnyTimes()
	api.EXPECT().Delete("default", "project", "shared-lb", gomock.Any()).DoAndReturn(func(_, _, _, id string) error {
		f.deletes++
		delete(f.backend, id)
		return nil
	}).AnyTimes()
	return f
}

func (f *transitionFixture) reconcile(t *testing.T) ctrl.Result {
	t.Helper()
	result, err := f.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "move"}})
	require.NoError(t, err)
	return result
}

func TestTransitionControl(t *testing.T) {
	f := transitionSetup(t, "absent", false, false)
	f.reconcile(t)
	f.reconcile(t)
	got := &vpcapi.IPAddressAllocation{}
	require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Namespace: "tenant", Name: "target"}, got))
	require.Equal(t, "192.0.2.10", got.Status.AllocationIPs)
	require.Equal(t, "target-uid", nsxutil.FindTag(f.backend["source-alloc"].Tags, common.TagScopeIPAddressAllocationCRUID))
	require.Equal(t, 1, f.patches)
	require.Len(t, f.backend, 1)
	// The normal source deletion handler must leave the adopted backend intact.
	require.NoError(t, f.service.DeleteIPAddressAllocation(types.UID("source-uid")))
	require.Zero(t, f.deletes)
	t.Log("CONTROL: new target adopts the exact VIP; repeat reconcile and source deletion preserve one backend allocation")
}

func TestTransitionRejectsDeletingTarget(t *testing.T) {
	f := transitionSetup(t, "pending", false, false)
	ctx := context.Background()
	targetKey := types.NamespacedName{Namespace: "tenant", Name: "target"}
	target := &vpcapi.IPAddressAllocation{}
	require.NoError(t, f.kube.Get(ctx, targetKey, target))
	target.Finalizers = []string{"test.nsx.vmware.com/hold-deletion"}
	require.NoError(t, f.kube.Update(ctx, target))
	require.NoError(t, f.kube.Delete(ctx, target))
	require.NoError(t, f.kube.Get(ctx, targetKey, target))
	require.False(t, target.DeletionTimestamp.IsZero())

	f.reconcile(t)
	require.Zero(t, f.patches, "a deleting target must not receive the source allocation")
	require.Zero(t, f.deletes)
	source := &vpcapi.IPAddressAllocation{}
	require.NoError(t, f.kube.Get(ctx, types.NamespacedName{Namespace: "staging", Name: "source"}, source))
	require.Equal(t, "source-uid", nsxutil.FindTag(f.backend["source-alloc"].Tags, common.TagScopeIPAddressAllocationCRUID))
	request := &migration.NetworkResourceTransition{}
	require.NoError(t, f.kube.Get(ctx, types.NamespacedName{Name: "move"}, request))
	require.Equal(t, migration.ItemPhaseFailed, request.Status.MoveIPAddressAllocation[0].Phase)
	require.Contains(t, request.Status.MoveIPAddressAllocation[0].Message, "being deleted")
}

func TestTransitionRequestExtensionUpdatesReady(t *testing.T) {
	f := transitionSetup(t, "pending", false, false)
	ctx := context.Background()
	f.reconcile(t)
	request := &migration.NetworkResourceTransition{}
	require.NoError(t, f.kube.Get(ctx, types.NamespacedName{Name: "move"}, request))
	require.True(t, meta.IsStatusConditionTrue(request.Status.Conditions, "Ready"))
	target := &vpcapi.IPAddressAllocation{}
	require.NoError(t, f.kube.Get(ctx, types.NamespacedName{Namespace: "tenant", Name: "target"}, target))
	waiting := &vpcapi.IPAddressAllocation{
		ObjectMeta: metav1.ObjectMeta{Namespace: "staging", Name: "waiting", UID: "waiting-uid"},
		Spec:       target.Spec,
	}
	require.NoError(t, f.kube.Create(ctx, waiting))
	request.Spec.MoveIPAddressAllocation = append(request.Spec.MoveIPAddressAllocation, migration.MoveIPAddressAllocationSpec{
		Source: migration.NamespacedObjectReference{Namespace: "staging", Name: "waiting"},
		Target: migration.NamespacedObjectReference{Namespace: "tenant", Name: "waiting"},
	})
	require.NoError(t, f.kube.Update(ctx, request))

	result := f.reconcile(t)
	require.Positive(t, result.RequeueAfter)
	require.NoError(t, f.kube.Get(ctx, types.NamespacedName{Name: "move"}, request))
	require.Equal(t, migration.MigrationPhaseMigrating, request.Status.Phase)
	require.Equal(t, 2, request.Status.TotalResources)
	require.Equal(t, 1, request.Status.SucceededResources)
	require.True(t, meta.IsStatusConditionFalse(request.Status.Conditions, "Ready"))
}

func TestTransitionRepeatedReconcileSkipsStatusWrites(t *testing.T) {
	for _, phase := range []migration.MigrationPhase{
		migration.MigrationPhaseSucceeded,
		migration.MigrationPhaseFailed,
		migration.MigrationPhasePartiallyFailed,
		migration.MigrationPhaseMigrating,
	} {
		t.Run(string(phase), func(t *testing.T) {
			f := transitionSetup(t, "pending", false, false)
			ctx := context.Background()
			key := types.NamespacedName{Name: "move"}
			request := &migration.NetworkResourceTransition{}
			switch phase {
			case migration.MigrationPhaseFailed:
				require.NoError(t, f.kube.Get(ctx, key, request))
				request.Spec.MoveIPAddressAllocation[0].Target = request.Spec.MoveIPAddressAllocation[0].Source
				require.NoError(t, f.kube.Update(ctx, request))
			case migration.MigrationPhasePartiallyFailed:
				require.NoError(t, f.kube.Get(ctx, key, request))
				invalid := request.Spec.MoveIPAddressAllocation[0]
				invalid.Target = invalid.Source
				request.Spec.MoveIPAddressAllocation = append(request.Spec.MoveIPAddressAllocation, invalid)
				require.NoError(t, f.kube.Update(ctx, request))
			case migration.MigrationPhaseMigrating:
				source := &vpcapi.IPAddressAllocation{}
				require.NoError(t, f.kube.Get(ctx, types.NamespacedName{Namespace: "staging", Name: "source"}, source))
				source.Status.AllocationIPs = ""
				require.NoError(t, f.kube.Status().Update(ctx, source))
			}
			f.reconcile(t)
			require.NoError(t, f.kube.Get(ctx, key, request))
			require.Equal(t, phase, request.Status.Phase)
			before := request.DeepCopy()
			writes := make(map[types.NamespacedName]int, len(f.statusWrites))
			for key, count := range f.statusWrites {
				writes[key] = count
			}
			patches := f.patches
			for i := 0; i < 10; i++ {
				result := f.reconcile(t)
				if phase == migration.MigrationPhaseMigrating {
					require.Positive(t, result.RequeueAfter)
				} else {
					require.Zero(t, result.RequeueAfter)
					require.False(t, result.Requeue)
				}
			}
			require.NoError(t, f.kube.Get(ctx, key, request))
			assert.Equal(t, before.ResourceVersion, request.ResourceVersion)
			assert.Equal(t, before.Status, request.Status)
			assert.Equal(t, writes, f.statusWrites, "repeated reconciles must not issue status writes for NRT or IPAA")
			assert.Equal(t, patches, f.patches, "repeated reconciles must not patch the adopted allocation again")
		})
	}
}

func TestTransitionReadyTargetConflict(t *testing.T) {
	f := transitionSetup(t, "ready", false, false)
	f.reconcile(t)
	source := &vpcapi.IPAddressAllocation{}
	err := f.kube.Get(context.Background(), types.NamespacedName{Namespace: "staging", Name: "source"}, source)
	assert.NoError(t, err, "a different ready target must not cause source deletion")
	request := &migration.NetworkResourceTransition{}
	require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Name: "move"}, request))
	assert.NotEqual(t, migration.MigrationPhaseSucceeded, request.Status.Phase, "unrelated ready VIP must not count as successful adoption")
	if apierrors.IsNotFound(err) {
		require.NoError(t, f.service.DeleteIPAddressAllocation(types.UID("source-uid")))
	}
	_, stillReserved := f.backend["source-alloc"]
	t.Logf("OBSERVED: phase=%s patches=%d sourceVIPReserved=%t backendCount=%d", request.Status.Phase, f.patches, stillReserved, len(f.backend))
}

func TestTransitionRejectedPatchRetry(t *testing.T) {
	f := transitionSetup(t, "pending", true, false)
	f.reconcile(t)
	ownerAfterFailure := nsxutil.FindTag(f.backend["source-alloc"].Tags, common.TagScopeIPAddressAllocationCRUID)
	require.Equal(t, "source-uid", ownerAfterFailure)
	f.reconcile(t)
	request := &migration.NetworkResourceTransition{}
	require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Name: "move"}, request))
	backendOwner := nsxutil.FindTag(f.backend["source-alloc"].Tags, common.TagScopeIPAddressAllocationCRUID)
	t.Logf("OBSERVED: phase=%s patches=%d durableBackendOwner=%s", request.Status.Phase, f.patches, backendOwner)
	assert.Equal(t, "target-uid", backendOwner, "Succeeded requires a durable ownership transfer, even after a rejected PATCH")
	assert.Equal(t, 2, f.patches, "the retry must perform the failed NSX write")
}

func TestTransitionSourceDeleteFailure(t *testing.T) {
	f := transitionSetup(t, "pending", false, true)
	result := f.reconcile(t)
	source := &vpcapi.IPAddressAllocation{}
	require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Namespace: "staging", Name: "source"}, source))
	request := &migration.NetworkResourceTransition{}
	require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Name: "move"}, request))
	t.Logf("OBSERVED: phase=%s sourceStillPresent=true requeue=%t requeueAfter=%s", request.Status.Phase, result.Requeue, result.RequeueAfter)
	assert.NotEqual(t, migration.MigrationPhaseSucceeded, request.Status.Phase, "source cleanup rejection must not be reported as completed")
	assert.True(t, result.Requeue || result.RequeueAfter > 0, "unfinished source cleanup needs an explicit retry")
}

func TestTransitionRecreatedTargetUID(t *testing.T) {
	f := transitionSetup(t, "stale", false, false)
	f.reconcile(t)
	target := &vpcapi.IPAddressAllocation{}
	require.NoError(t, f.kube.Get(context.Background(), types.NamespacedName{Namespace: "tenant", Name: "target"}, target))
	t.Logf("OBSERVED: currentUID=%s assignedIP=%s staleAllocationOwner=%s patches=%d", target.UID, target.Status.AllocationIPs, nsxutil.FindTag(f.backend["other-alloc"].Tags, common.TagScopeIPAddressAllocationCRUID), f.patches)
	assert.Equal(t, "192.0.2.10", target.Status.AllocationIPs, "a recreated target must receive the requested source VIP, not an old same-name object's VIP")
	assert.Equal(t, "target-uid", nsxutil.FindTag(f.backend["source-alloc"].Tags, common.TagScopeIPAddressAllocationCRUID), "source must be durably adopted by the current UID before reaping")
}
