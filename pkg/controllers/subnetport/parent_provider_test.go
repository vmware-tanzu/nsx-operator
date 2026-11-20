/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package subnetport

import (
	"context"
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	pkgmock "github.com/vmware-tanzu/nsx-operator/pkg/mock"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	portservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

// Exercise Reconcile and real selection/allocation, replacing only backend calls.
// An admitted port event must not grant permission to create a rejected parent.
func TestSubnetPortParentProviderAdmission(t *testing.T) {
	for _, selection := range []string{"subnet", "subnetset", "default"} {
		for _, scenario := range []string{"inherited_managed", "inherited_shared", "explicit_managed", "disabled", "disabled_explicit", "legacy_provider", "shared_provider_retry", "inherited_managed_member", "inherited_managed_member_static", "explicit_managed_member", "disabled_managed_member", "legacy_managed_member"} {
			t.Run(selection+"/"+scenario, func(t *testing.T) {
				ctx := context.Background()
				enabled := scenario != "disabled" && scenario != "disabled_explicit" && scenario != "disabled_managed_member"
				inheritedManagedMember := scenario == "inherited_managed_member" || scenario == "inherited_managed_member_static"
				precreated := inheritedManagedMember || scenario == "explicit_managed_member" || scenario == "disabled_managed_member" || scenario == "legacy_managed_member" || scenario == "inherited_shared" || scenario == "shared_provider_retry"
				patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool {
					return scenario != "legacy_provider" && scenario != "legacy_managed_member"
				})
				defer patches.Reset()
				scheme := runtime.NewScheme()
				require.NoError(t, corev1.AddToScheme(scheme))
				require.NoError(t, v1alpha1.AddToScheme(scheme))
				child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging", Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: "owner"}}}
				owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "owner", Annotations: map[string]string{servicecommon.AnnotationVPCNetworkConfig: "network"}}}
				if scenario == "explicit_managed" || scenario == "disabled_explicit" || scenario == "explicit_managed_member" || inheritedManagedMember {
					child.Annotations[servicecommon.AnnotationVPCNetworkConfig] = "network"
				}
				path := "/orgs/default/projects/default/vpcs/vpc/subnets/member"
				member := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "member", Namespace: child.Name}, Spec: v1alpha1.SubnetSpec{IPAddressType: v1alpha1.IPAddressTypeIPv4}}
				set := &v1alpha1.SubnetSet{ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: child.Name, UID: "set-uid", Labels: map[string]string{servicecommon.LabelDefaultNetwork: servicecommon.DefaultVMNetwork}}, Spec: v1alpha1.SubnetSetSpec{IPAddressType: v1alpha1.IPAddressTypeIPv4}}
				if scenario == "inherited_shared" || scenario == "shared_provider_retry" {
					member.Annotations = map[string]string{servicecommon.AnnotationAssociatedResource: path}
				}
				if precreated {
					names := []string{member.Name}
					set.Spec.SubnetNames = &names
				}
				port := &v1alpha1.SubnetPort{ObjectMeta: metav1.ObjectMeta{Name: "port", Namespace: child.Name, UID: "port-uid"}, Spec: v1alpha1.SubnetPortSpec{InterfaceIPType: v1alpha1.IPAddressTypeIPv4}}
				if scenario == "inherited_managed_member_static" {
					port.Spec.AddressBindings = []v1alpha1.PortAddressBinding{{IPAddress: "192.0.2.10"}}
				}
				var parent client.Object = set
				if selection == "subnet" {
					port.Spec.Subnet = member.Name
					parent = member
				}
				if selection == "subnetset" {
					port.Spec.SubnetSet = set.Name
				}
				api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(child, owner, member, set, port).WithStatusSubresource(port).Build()
				if inheritedManagedMember {
					// The realized managed member was eligible under the old local
					// provider. Switching to inheritance must invalidate both references.
					allowed, err := common.CanReconcileSubnetResource(ctx, api, member)
					require.NoError(t, err)
					require.True(t, allowed)
					require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(child), child))
					delete(child.Annotations, servicecommon.AnnotationVPCNetworkConfig)
					require.NoError(t, api.Update(ctx, child))
					allowed, err = common.CanReconcileSubnetResource(ctx, api, member)
					require.NoError(t, err)
					require.False(t, allowed)
				}
				cfg := &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{EnableNetworkMigration: enabled}, NsxConfig: &config.NsxConfig{}}
				ps := &portservice.SubnetPortService{Service: servicecommon.Service{NSXConfig: cfg}, SubnetPortStore: &portservice.SubnetPortStore{}}
				ss := &pkgmock.MockSubnetServiceProvider{}
				vs := &pkgmock.MockVPCServiceProvider{}
				backend := &model.VpcSubnet{Id: servicecommon.String("member"), Path: &path, IpAddresses: []string{"192.0.2.0/24"}}
				created, allocations := 0, 0
				reached := errors.New("backend allocation reached")
				ss.On("GetSubnetsByIndex", testifymock.Anything, testifymock.Anything).Return([]*model.VpcSubnet{}).Maybe()
				ss.On("GenerateSubnetNSTags", testifymock.Anything).Return([]model.Tag{}).Maybe()
				ss.On("CreateOrUpdateSubnet", testifymock.Anything, testifymock.Anything, testifymock.Anything).Run(func(testifymock.Arguments) { created++ }).Return(backend, nil).Maybe()
				ss.On("GetSubnetByCR", testifymock.Anything).Return(backend, nil).Maybe()
				vs.On("ListVPCInfo", child.Name).Return([]servicecommon.VPCResourceInfo{{}}).Maybe()
				nc := &v1alpha1.VPCNetworkConfiguration{Spec: v1alpha1.VPCNetworkConfigurationSpec{Subnets: []v1alpha1.SharedSubnet{{Path: path, VMDefault: true}}}}
				patches.ApplyFunc(common.GetVpcNetworkConfig, func(servicecommon.VPCServiceProvider, string) (*v1alpha1.VPCNetworkConfiguration, error) {
					return nc, nil
				})
				patches.ApplyMethod(ps.SubnetPortStore, "GetVpcSubnetPortByUID", func(*portservice.SubnetPortStore, types.UID) (*model.VpcSubnetPort, error) { return nil, nil })
				patches.ApplyMethod(ps, "AllocatePortFromSubnet", func(*portservice.SubnetPortService, *model.VpcSubnet, bool, v1alpha1.IPAddressType, v1alpha1.StaticIPAllocationType, []v1alpha1.PortAddressBinding, ...types.UID) (bool, error) {
					allocations++
					return false, reached
				})
				r := &SubnetPortReconciler{Client: api, APIReader: api, SubnetService: ss, VPCService: vs, SubnetPortService: ps}
				r.StatusUpdater = common.NewStatusUpdater(api, cfg, fakeRecorder{}, MetricResTypeSubnetPort, "SubnetPort", "SubnetPort")
				var allowed bool
				var err error
				if enabled {
					allowed, err = common.CanReconcileSubnetResource(ctx, api, parent)
					require.NoError(t, err)
				} else {
					patches.ApplyFunc(common.IsVPCNamespaceOrShared, func(context.Context, client.Reader, *corev1.Namespace) (bool, error) {
						t.Error("disabled migration entered shared namespace admission")
						return true, nil
					})
					patches.ApplyFunc(common.CanReconcileSubnetResource, func(context.Context, client.Reader, client.Object) (bool, error) {
						t.Error("disabled migration entered resource admission")
						return true, nil
					})
					patches.ApplyFunc((*SubnetPortReconciler).validateSubnetParent, func(*SubnetPortReconciler, context.Context, client.Object) error {
						t.Error("disabled migration entered new parent admission")
						return nil
					})
				}
				if scenario == "shared_provider_retry" {
					// The event's provider check succeeds; only the parent-side live read fails.
					reader := &parentProviderReader{Client: api}
					r.APIReader = reader
					_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(port)})
					require.ErrorIs(t, err, context.DeadlineExceeded)
					require.Zero(t, created)
					require.Zero(t, allocations)
					r.APIReader = api
				}
				_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(port)})
				if scenario == "inherited_managed" || inheritedManagedMember {
					require.Equal(t, inheritedManagedMember && selection != "subnet", allowed, "collection eligibility must not authorize an ineligible member")
					require.ErrorContains(t, err, "not eligible")
					require.Zero(t, created, "a rejected parent must not create an NSX subnet")
					require.Zero(t, allocations, "a rejected parent must not reserve capacity")
					current := &v1alpha1.SubnetPort{}
					require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(port), current))
					require.NotEmpty(t, current.Status.Conditions)
					require.False(t, common.IsObjectReady(current.Status.Conditions))
				} else {
					if enabled {
						require.True(t, allowed)
					}
					require.ErrorIs(t, err, reached, "valid and legacy parents must still reach real allocation")
					require.Equal(t, 1, allocations)
					if selection != "subnet" && !precreated {
						require.Equal(t, 1, created)
					}
				}
			})
		}
	}
}

// The cached namespace check succeeds; the live owner read used by parent admission fails.
type parentProviderReader struct {
	client.Client
	ownerReads int
}

func (r *parentProviderReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, namespace := obj.(*corev1.Namespace); namespace && key.Name == "owner" {
		r.ownerReads++
		if r.ownerReads == 1 {
			return context.DeadlineExceeded
		}
	}
	return r.Client.Get(ctx, key, obj, opts...)
}
