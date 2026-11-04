/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	controllercommon "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	subnetcontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnet"
	portcontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnetport"
	setcontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnetset"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnet"
	portservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

// Start with only namespaces. Let the real Namespace reconciler create the
// shared Subnet/default SubnetSet, then dispatch the SubnetSet event through its
// real predicate and exercise the real SubnetPort selection path.
func TestFreshSharedNamespacePortUsable(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "inherited_only"
		if explicit {
			name = "explicit_local_control"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "vmware-system-net-staging", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: "kube-system"}}}
			if explicit {
				child.Annotations[servicecommon.AnnotationVPCNetworkConfig] = "shared-config"
			}
			owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", Annotations: map[string]string{servicecommon.AnnotationVPCNetworkConfig: "shared-config"}}}
			r := createNameSpaceReconciler([]client.Object{child, owner})
			r.Client = fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(child, owner).WithStatusSubresource(&v1alpha1.Subnet{}, &v1alpha1.SubnetSet{}).
				WithIndex(&v1alpha1.SubnetPort{}, "spec.subnet", func(obj client.Object) []string { return []string{obj.(*v1alpha1.SubnetPort).Spec.Subnet} }).
				WithIndex(&v1alpha1.SubnetIPReservation{}, "spec.subnet", func(obj client.Object) []string { return []string{obj.(*v1alpha1.SubnetIPReservation).Spec.Subnet} }).Build()
			r.APIReader = r.Client
			r.SubnetPortService = &pkgmock.MockSubnetPortServiceProvider{}
			r.namespaceEvents = make(chan event.GenericEvent, 10)
			r.SubnetService.Client = r.Client
			r.SubnetStatusUpdater = controllercommon.NewStatusUpdater(r.Client, r.SubnetService.NSXConfig, record.NewFakeRecorder(20), "Subnet", "Subnet", "Subnet")
			r.SubnetService.SharedSubnetResourceMap = make(map[string]sets.Set[types.NamespacedName])
			svc := r.VPCService.(*vpc.VPCService)
			svc.Client = r.Client
			patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return true })
			defer patches.Reset()
			path := "/orgs/default/projects/default/vpcs/vpc/subnets/new"
			backend := &model.VpcSubnet{Id: servicecommon.String("new"), Path: &path, IpAddresses: []string{"192.0.2.0/24"}}
			nc := &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "shared-config"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{Subnets: []v1alpha1.SharedSubnet{{Path: path, VMDefault: true}}}}
			patches.ApplyMethod(svc, "GetVPCNetworkConfig", func(_ *vpc.VPCService, name string) (*v1alpha1.VPCNetworkConfiguration, bool, error) {
				require.Equal(t, nc.Name, name)
				return nc, true, nil
			})
			patches.ApplyMethod(svc, "ValidateNetworkConfig", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) error { return nil })
			patches.ApplyMethod(svc, "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
				return v1alpha1.FullStackVPC, nil
			})
			patches.ApplyMethod(svc, "IsDefaultNSXProject", func(_ *vpc.VPCService, _, _ string) (bool, error) { return true, nil })
			patches.ApplyMethod(r.SubnetService, "GetNSXSubnetFromCacheOrAPI", func(_ *subnet.SubnetService, _ string, _ bool) (*model.VpcSubnet, error) { return backend, nil })
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: child.Name}})
			require.NoError(t, err)
			set, err := controllercommon.ListDefaultSubnetSet(ctx, r.Client, child.Name, servicecommon.DefaultVMNetwork)
			require.NoError(t, err)
			require.NotNil(t, set)
			require.Equal(t, []string{"new"}, *set.Spec.SubnetNames)
			initialType := set.Spec.IPAddressType
			admitted := controllercommon.SubnetResourceNamespacePredicate(r.Client).Create(event.CreateEvent{Object: set})
			require.True(t, admitted)
			// Exercise actual controllers, including the retry while the member is
			// not realized. No type, gateway or Ready status is filled by the test.
			sr := &setcontroller.SubnetSetReconciler{Client: r.Client, SubnetService: r.SubnetService, StatusUpdater: r.SubnetStatusUpdater}
			setRequest := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}
			_, err = sr.Reconcile(ctx, setRequest)
			require.ErrorContains(t, err, "not realized")
			member := &v1alpha1.Subnet{}
			require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Namespace: child.Name, Name: "new"}, member))
			require.True(t, controllercommon.SubnetResourceNamespacePredicate(r.Client).Create(event.CreateEvent{Object: member}))
			patches.ApplyMethod(r.SubnetService, "GetSubnetStatusFromCacheOrAPI", func(_ *subnet.SubnetService, _ *model.VpcSubnet, _ string) ([]model.VpcSubnetStatus, error) {
				return []model.VpcSubnetStatus{{NetworkAddress: servicecommon.String("192.0.2.0/24"), GatewayAddress: servicecommon.String("192.0.2.1")}}, nil
			})
			patches.ApplyFunc(nsx.SubnetPortSettingFeatureEnabled, func(_ *nsx.Client, _ *config.NSXOperatorConfig) bool { return false })
			subr := &subnetcontroller.SubnetReconciler{Client: r.Client, APIReader: r.Client, SubnetService: r.SubnetService, VPCService: r.VPCService, StatusUpdater: r.SubnetStatusUpdater}
			_, err = subr.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(member)})
			require.NoError(t, err)
			require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(member), member))
			require.True(t, controllercommon.IsObjectReady(member.Status.Conditions))
			require.NotEmpty(t, member.Status.GatewayAddresses)
			_, err = sr.Reconcile(ctx, setRequest)
			require.NoError(t, err)
			require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(set), set))
			require.Equal(t, v1alpha1.IPAddressTypeIPv4, set.Spec.IPAddressType)
			require.True(t, controllercommon.IsObjectReady(set.Status.Conditions))

			emptyIndex := func(interface{}) ([]string, error) { return nil, nil }
			store := &portservice.SubnetPortStore{ResourceStore: servicecommon.ResourceStore{Indexer: cache.NewIndexer(func(obj interface{}) (string, error) { return *obj.(*model.VpcSubnetPort).Id, nil }, cache.Indexers{servicecommon.TagScopeSubnetPortCRUID: emptyIndex, servicecommon.TagScopePodUID: emptyIndex})}}
			ps := &portservice.SubnetPortService{SubnetPortStore: store}
			allocations := 0
			patches.ApplyMethod(ps, "AllocatePortFromSubnet", func(_ *portservice.SubnetPortService, selected *model.VpcSubnet, shared bool, family v1alpha1.IPAddressType, _ v1alpha1.StaticIPAllocationType, addresses []v1alpha1.PortAddressBinding, _ ...types.UID) (bool, error) {
				require.Equal(t, path, *selected.Path)
				require.True(t, shared)
				require.Equal(t, v1alpha1.IPAddressTypeIPv4, family)
				require.NotEmpty(t, addresses)
				allocations++
				return true, nil
			})
			pr := &portcontroller.SubnetPortReconciler{Client: r.Client, APIReader: r.Client, SubnetService: r.SubnetService, VPCService: r.VPCService, SubnetPortService: ps}
			for _, named := range []bool{false, true} {
				for _, multi := range []bool{false, true} {
					for _, ipType := range []v1alpha1.IPAddressType{"", v1alpha1.IPAddressTypeIPv4} {
						sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "requested", Namespace: child.Name, UID: "requested-port"}, Spec: v1alpha1.SubnetPortSpec{InterfaceIPType: ipType, StaticIPAllocationType: v1alpha1.StaticIPAllocationTypeIPv4, AddressBindings: []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10"}}}}
						if named {
							sp.Spec.SubnetSet = set.Name
						}
						if multi {
							sp.Spec.AddressBindings = append(sp.Spec.AddressBindings, v1alpha1.PortAddressBinding{IPAddress: "192.0.2.11"})
						}
						requested := append([]v1alpha1.PortAddressBinding(nil), sp.Spec.AddressBindings...)

						_, _, selected, uid, lock, _, _, err := pr.CheckAndGetSubnetPathForSubnetPort(ctx, sp)
						if lock != nil {
							controllercommon.RUnlockSubnetSet(*uid, lock)
						}
						t.Logf("initial parent IPAddressType=%q; SubnetSet event admitted=%v; requested InterfaceIPType=%q; selected=%q; error=%v", initialType, admitted, ipType, selected, err)
						require.Equal(t, requested, sp.Spec.AddressBindings)
						if err != nil {
							t.Errorf("synchronized shared SubnetSet cannot serve SubnetPort: %v", err)
						}
						if selected != path {
							t.Errorf("expected matching member %s, got %q", path, selected)
						}
					}
				}
			}
			require.Equal(t, 8, allocations, "default/named, inferred/explicit IPv4 and single/multiple addresses must reach allocation")
			// Change only the owner's config annotation. No config/SubnetSet
			// update event may be used to wake the child in this phase.
			before := &corev1.Namespace{}
			require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(owner), before))
			after := before.DeepCopy()
			after.Annotations[config.VPCNetworkConfigAnnotation] = "config-b"
			require.NoError(t, r.Client.Update(ctx, after))
			update := event.UpdateEvent{ObjectOld: before, ObjectNew: after}
			require.True(t, sharedOwnerPredicate.Update(update))
			q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			defer q.ShutDown()
			handler.EnqueueRequestsFromMapFunc(sharedOwnerRequests).Update(ctx, update, q)
			require.Equal(t, 1, q.Len())
			dependency, _ := q.Get()
			q.Done(dependency)
			failures := &namespaceFailureClient{Client: r.Client, listFailures: 1}
			r.Client = failures
			_, err = r.Reconcile(ctx, dependency)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Empty(t, r.namespaceEvents)
			_, err = r.Reconcile(ctx, dependency)
			require.NoError(t, err)
			if explicit {
				require.Empty(t, r.namespaceEvents, "an explicit child configuration must not follow owner changes")
				effective, err := svc.GetNetworkconfigNameFromNS(ctx, child.Name)
				require.NoError(t, err)
				require.Equal(t, "shared-config", effective)
				require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(set), set))
				require.Equal(t, []string{"new"}, *set.Spec.SubnetNames)
				return
			}
			require.Len(t, r.namespaceEvents, 1)
			(&handler.EnqueueRequestForObject{}).Generic(ctx, <-r.namespaceEvents, q)
			request, _ := q.Get()
			defer q.Done(request)
			require.Equal(t, child.Name, request.Name)
			require.Empty(t, request.Namespace)
			// Reuse the real resource controllers for the new subnet in the same VPC.
			path = "/orgs/default/projects/default/vpcs/vpc/subnets/next"
			backend = &model.VpcSubnet{Id: servicecommon.String("next"), Path: &path, IpAddresses: []string{"198.51.100.0/24"}}
			nc = &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "config-b"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{Subnets: []v1alpha1.SharedSubnet{{Path: path, VMDefault: true}}}}
			_, err = r.Reconcile(ctx, request)
			require.NoError(t, err)
			require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(set), set))
			require.Equal(t, []string{"next"}, *set.Spec.SubnetNames)
			require.True(t, apierrors.IsNotFound(r.Client.Get(ctx, client.ObjectKeyFromObject(member), &v1alpha1.Subnet{})))
			patches.ApplyMethod(r.SubnetService, "GetSubnetStatusFromCacheOrAPI", func(_ *subnet.SubnetService, _ *model.VpcSubnet, _ string) ([]model.VpcSubnetStatus, error) {
				return []model.VpcSubnetStatus{{NetworkAddress: servicecommon.String("198.51.100.0/24"), GatewayAddress: servicecommon.String("198.51.100.1")}}, nil
			})
			_, err = subr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: child.Name, Name: "next"}})
			require.NoError(t, err)
			_, err = sr.Reconcile(ctx, setRequest)
			require.NoError(t, err)
			sp := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "after-switch", Namespace: child.Name, UID: "after-switch"}, Spec: v1alpha1.SubnetPortSpec{AddressBindings: []v1alpha1.PortAddressBinding{{IPAddress: "198.51.100.10"}}}}
			_, _, selected, uid, lock, _, _, err := pr.CheckAndGetSubnetPathForSubnetPort(ctx, sp)
			if lock != nil {
				controllercommon.RUnlockSubnetSet(*uid, lock)
			}
			require.NoError(t, err)
			require.Equal(t, path, selected)
			require.Equal(t, 9, allocations, "owner change must make config B addresses allocatable")

		})
	}
}

// Recheck eligibility after dequeue as well as at the watch predicate: namespace
// annotations may change while a resource event waits in the controller queue.
func TestSharedResourceProviderGuards(t *testing.T) {
	patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return true })
	defer patches.Reset()
	for _, tc := range []struct {
		name    string
		localT1 string
		ownerT1 bool
		missing bool
		revoke  bool
		managed bool
	}{
		{name: "local T1 default", localT1: config.T1DefaultConfigAnnotation},
		{name: "local T1 shared", localT1: config.T1VmwareSystemSharedAnnotation},
		{name: "local T1 configuration", localT1: config.T1NsxNetworkConfigAnnotation},
		{name: "T1 owner", ownerT1: true},
		{name: "missing owner retries", missing: true},
		{name: "owner revoked after enqueue", revoke: true},
		{name: "managed resources stay excluded", managed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: "owner"}}}
			if tc.localT1 != "" {
				child.Annotations[tc.localT1] = "true"
			}
			owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "owner", Annotations: map[string]string{config.VPCNetworkConfigAnnotation: "shared-config"}}}
			if tc.ownerT1 {
				owner.Annotations = map[string]string{config.T1DefaultConfigAnnotation: "true"}
			}
			member := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "member", Namespace: child.Name}}
			set := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: child.Name}}
			if !tc.managed {
				member.Annotations = map[string]string{servicecommon.AnnotationAssociatedResource: "default:vpc:member"}
				members := []string{member.Name}
				set.Spec.SubnetNames = &members
			}
			objects := []client.Object{child, member, set}
			if !tc.missing {
				objects = append(objects, owner)
			}
			r := createNameSpaceReconciler(objects)
			updater := controllercommon.NewStatusUpdater(r.Client, r.NSXConfig, record.NewFakeRecorder(10), "Subnet", "Subnet", "Subnet")
			pred := controllercommon.SubnetResourceNamespacePredicate(r.Client)
			for _, resource := range []client.Object{member, set} {
				want := tc.missing || tc.revoke
				require.Equal(t, want, pred.Create(event.CreateEvent{Object: resource}))
				require.Equal(t, want, pred.Update(event.UpdateEvent{ObjectNew: resource}))
				require.Equal(t, want, pred.Generic(event.GenericEvent{Object: resource}))
				require.True(t, pred.Delete(event.DeleteEvent{Object: resource}), "cleanup events must not be lost")
				require.False(t, controllercommon.VPCNamespacePredicate(r.Client).Create(event.CreateEvent{Object: resource}), "unrelated controller gates remain unchanged")
			}
			if tc.revoke {
				owner.Annotations = map[string]string{config.T1DefaultConfigAnnotation: "true"}
				require.NoError(t, r.Client.Update(ctx, owner))
			}
			subr := &subnetcontroller.SubnetReconciler{Client: r.Client, SubnetService: r.SubnetService, StatusUpdater: updater}
			setr := &setcontroller.SubnetSetReconciler{Client: r.Client, SubnetService: r.SubnetService, StatusUpdater: updater}
			for _, call := range []struct {
				resource  client.Object
				reconcile func(context.Context, ctrl.Request) (ctrl.Result, error)
			}{{member, subr.Reconcile}, {set, setr.Reconcile}} {
				result, err := call.reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(call.resource)})
				if tc.missing {
					require.True(t, apierrors.IsNotFound(err))
					require.Equal(t, controllercommon.ResultRequeue, result)
				} else {
					require.NoError(t, err)
					require.Equal(t, controllercommon.ResultNormal, result)
				}
			}
			require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(member), member))
			require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(set), set))
			require.Empty(t, member.Status.Conditions)
			require.Empty(t, set.Status.Conditions)
			require.Empty(t, set.Spec.IPAddressType, "unauthorized or unverified resources must not be initialized")
		})
	}
}
