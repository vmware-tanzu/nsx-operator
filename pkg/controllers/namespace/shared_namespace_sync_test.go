/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

// Exercise the real update handler, queue, provider gate and resource writes.
// Only network configuration/backend queries are stubbed; shared Subnet and
// default SubnetSet synchronization must actually converge in the fake API.
func TestSharedNamespaceConfigUpdateSync(t *testing.T) {
	for _, localConfig := range []bool{false, true} {
		name := "inherited"
		if localConfig {
			name = "explicit local configuration"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "vmware-system-net-staging", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: "kube-system"}}}
			if localConfig {
				child.Annotations[config.VPCNetworkConfigAnnotation] = "system"
			}
			owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", Annotations: map[string]string{config.VPCNetworkConfigAnnotation: "system"}}}
			oldSubnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "old", Namespace: child.Name, Annotations: map[string]string{servicecommon.AnnotationAssociatedResource: "default:vpc:old"}}}
			objects := []client.Object{child, owner, oldSubnet}
			for _, network := range []string{servicecommon.DefaultVMNetwork, servicecommon.DefaultPodNetwork} {
				members := []string{"old"}
				objects = append(objects, &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "default-" + network, Namespace: child.Name, Labels: map[string]string{servicecommon.LabelDefaultNetwork: network}}, Spec: v1alpha1.SubnetSetSpec{SubnetNames: &members}})
			}
			r := createNameSpaceReconciler(objects)
			api := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(objects...).
				WithIndex(&v1alpha1.SubnetPort{}, "spec.subnet", func(obj client.Object) []string { return []string{obj.(*v1alpha1.SubnetPort).Spec.Subnet} }).
				WithIndex(&v1alpha1.SubnetIPReservation{}, "spec.subnet", func(obj client.Object) []string { return []string{obj.(*v1alpha1.SubnetIPReservation).Spec.Subnet} }).Build()
			failures := &namespaceFailureClient{Client: api, listFailures: 1}
			r.Client, r.APIReader = failures, failures
			r.namespaceEvents = make(chan event.GenericEvent, 10)
			svc := r.VPCService.(*vpc.VPCService)
			svc.Client = failures
			r.SubnetService.Client = api
			r.SubnetService.SharedSubnetResourceMap = make(map[string]sets.Set[types.NamespacedName])
			r.SubnetPortService = &pkgmock.MockSubnetPortServiceProvider{}
			r.SubnetStatusUpdater = controllercommon.NewStatusUpdater(api, r.NSXConfig, record.NewFakeRecorder(10), "Subnet", "Subnet", "Subnet")
			patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return true })
			defer patches.Reset()
			nc := &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "system"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{Subnets: []v1alpha1.SharedSubnet{{Path: "/orgs/default/projects/default/vpcs/vpc/subnets/new", VMDefault: true, PodDefault: true}}}}
			patches.ApplyMethod(svc, "GetVPCNetworkConfig", func(_ *vpc.VPCService, name string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
				require.Equal(t, "system", name)
				return nc, true, nil
			})
			patches.ApplyMethod(svc, "ValidateNetworkConfig", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) error { return nil })
			patches.ApplyMethod(svc, "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
				return v1alpha1.FullStackVPC, nil
			})
			patches.ApplyMethod(svc, "IsDefaultNSXProject", func(_ *vpc.VPCService, _, _ string) (bool, error) { return true, nil })
			backendErr := errors.New("temporary subnet lookup failure")
			patches.ApplyMethod(r.SubnetService, "GetNSXSubnetFromCacheOrAPI", func(_ *subnet.SubnetService, associated string, _ bool) (*model.VpcSubnet, error) {
				require.Equal(t, "default:vpc:new", associated)
				return &model.VpcSubnet{IpAddresses: []string{"192.0.2.0/24"}}, backendErr
			})
			enqueueChild := func(oldNC *v1alpha1.VPCNetworkConfiguration) reconcile.Request {
				q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
				defer q.ShutDown()
				e := event.UpdateEvent{ObjectOld: oldNC, ObjectNew: nc}
				require.True(t, PredicateFuncsVPCNetworkConfig.Update(e))
				(&EnqueueRequestForVPCNetworkConfiguration{Reconciler: r}).Update(ctx, e, q)
				require.Equal(t, 1, q.Len())
				dependency, _ := q.Get()
				q.Done(dependency)
				require.Equal(t, networkConfigDependency, dependency.Namespace)
				_, err := r.Reconcile(ctx, dependency)
				if err != nil {
					require.ErrorIs(t, err, context.DeadlineExceeded)
					require.Empty(t, r.namespaceEvents)
					_, err = r.Reconcile(ctx, dependency)
				}
				require.NoError(t, err)
				var childRequest reconcile.Request
				for len(r.namespaceEvents) > 0 {
					ev := <-r.namespaceEvents
					if ev.Object.GetName() == child.Name {
						childRequest = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(ev.Object)}
					}
				}
				require.Equal(t, child.Name, childRequest.Name, "the inherited namespace must be enqueued")
				return childRequest
			}
			oldNC := nc.DeepCopy()
			oldNC.Spec.Subnets[0].Path = "/orgs/default/projects/default/vpcs/vpc/subnets/old"
			request := enqueueChild(oldNC)
			if !localConfig {
				failures.ownerFailures = 1
				_, err := r.Reconcile(ctx, request)
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(oldSubnet), &v1alpha1.Subnet{}))
			}
			result, err := r.Reconcile(ctx, request)
			require.ErrorIs(t, err, backendErr)
			require.Equal(t, controllercommon.ResultRequeue, result)
			require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(oldSubnet), &v1alpha1.Subnet{}))
			backendErr = nil
			result, err = r.Reconcile(ctx, request)
			require.NoError(t, err)
			require.Equal(t, controllercommon.ResultNormal, result)
			created := &v1alpha1.Subnet{}
			require.NoError(t, api.Get(ctx, types.NamespacedName{Namespace: child.Name, Name: "new"}, created))
			require.Equal(t, "default:vpc:new", created.Annotations[servicecommon.AnnotationAssociatedResource])
			require.True(t, apierrors.IsNotFound(api.Get(ctx, client.ObjectKeyFromObject(oldSubnet), &v1alpha1.Subnet{})))
			for _, network := range []string{servicecommon.DefaultVMNetwork, servicecommon.DefaultPodNetwork} {
				set, err := controllercommon.ListDefaultSubnetSet(ctx, api, child.Name, network)
				require.NoError(t, err)
				require.NotNil(t, set)
				require.Equal(t, []string{"new"}, *set.Spec.SubnetNames)
			}
			// An update removing all shared entries must also remove stale references.
			oldNC = nc.DeepCopy()
			nc.Spec.Subnets = nil
			result, err = r.Reconcile(ctx, enqueueChild(oldNC))
			require.NoError(t, err)
			require.Equal(t, controllercommon.ResultNormal, result)
			subnets, subnetSets := &v1alpha1.SubnetList{}, &v1alpha1.SubnetSetList{}
			require.NoError(t, api.List(ctx, subnets, client.InNamespace(child.Name)))
			require.NoError(t, api.List(ctx, subnetSets, client.InNamespace(child.Name)))
			require.Empty(t, subnets.Items)
			require.Empty(t, subnetSets.Items)
			actual := &corev1.Namespace{}
			require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(child), actual))
			require.Equal(t, child.Annotations[config.VPCNetworkConfigAnnotation], actual.Annotations[config.VPCNetworkConfigAnnotation], "provider inheritance must not rewrite local annotations")
		})
	}
}

func TestSharedNamespaceProviderBoundaries(t *testing.T) {
	patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return true })
	defer patches.Reset()
	for _, tc := range []struct {
		name     string
		shared   string
		local    map[string]string
		owner    map[string]string
		missing  bool
		deleting bool
	}{
		{name: "namespace name alone"},
		{name: "self reference", shared: "vmware-system-net-staging"},
		{name: "unmarked owner", shared: "owner"},
		{name: "T1 owner", shared: "owner", owner: map[string]string{config.T1DefaultConfigAnnotation: "true"}},
		{name: "conflicting owner", shared: "owner", owner: map[string]string{config.VPCNetworkConfigAnnotation: "system", config.T1DefaultConfigAnnotation: "true"}},
		{name: "local T1 default", shared: "owner", owner: map[string]string{config.VPCNetworkConfigAnnotation: "system"}, local: map[string]string{config.T1DefaultConfigAnnotation: "true"}},
		{name: "local shared T1", shared: "owner", owner: map[string]string{config.VPCNetworkConfigAnnotation: "system"}, local: map[string]string{config.T1VmwareSystemSharedAnnotation: "true"}},
		{name: "local T1 config", shared: "owner", owner: map[string]string{config.VPCNetworkConfigAnnotation: "system"}, local: map[string]string{config.T1NsxNetworkConfigAnnotation: "legacy"}},
		{name: "missing owner retries", shared: "owner", missing: true},
		{name: "deletion bypasses missing owner", shared: "owner", missing: true, deleting: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "vmware-system-net-staging", Annotations: map[string]string{}}}
			for k, v := range tc.local {
				child.Annotations[k] = v
			}
			if tc.shared != "" {
				child.Annotations[servicecommon.AnnotationSharedVPCNamespace] = tc.shared
			}
			if tc.deleting {
				now := metav1.Now()
				child.DeletionTimestamp = &now
				child.Finalizers = []string{"test-cleanup"}
			}
			objects := []client.Object{child}
			if !tc.missing {
				objects = append(objects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "owner", Annotations: tc.owner}})
			}
			r := createNameSpaceReconciler(objects)
			result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: child.Name}})
			if tc.missing && !tc.deleting {
				require.True(t, apierrors.IsNotFound(err))
				require.Equal(t, controllercommon.ResultRequeueAfter10sec, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, controllercommon.ResultNormal, result)
			}
			infos := &v1alpha1.NetworkInfoList{}
			require.NoError(t, r.Client.List(context.Background(), infos))
			require.Empty(t, infos.Items, "rejected namespaces must not create VPC resources")
		})
	}
}
