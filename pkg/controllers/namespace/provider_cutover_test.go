/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestCoordinatedProviderCutoverResumesSharedDefaults(t *testing.T) {
	ctx := context.Background()
	config.SetMixedModeStateForTest(false, true)
	t.Cleanup(func() { config.SetMixedModeStateForTest(false, false) })
	const vpcPath = "/orgs/default/projects/p/vpcs/v"
	nc := &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "target"},
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			VPC: vpcPath, NSXProject: "/orgs/default/projects/p",
			Subnets: []v1alpha1.SharedSubnet{{Path: vpcPath + "/subnets/s", PodDefault: true, VMDefault: true}},
		},
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team", Labels: map[string]string{netop.NamespaceNetworkLabelKey: "desired"}, Annotations: map[string]string{config.VPCNetworkConfigAnnotation: nc.Name}}}
	shared := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "retained", Namespace: ns.Name, UID: "shared-uid", Annotations: map[string]string{servicecommon.AnnotationAssociatedResource: "p:v:s"}}}
	r := createTestNamespaceReconciler([]client.Object{nc, ns, shared})
	r.NSXConfig.K8sConfig = &config.K8sConfig{}
	settingsScheme := runtime.NewScheme()
	require.NoError(t, netop.AddToScheme(settingsScheme))
	settings := &netop.NetworkSettings{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: ns.Name}, Provider: netop.NetworkProviderVSphereDistributed}
	settingsClient := fake.NewClientBuilder().WithScheme(settingsScheme).WithObjects(settings).Build()
	r.networkSettings = settingsClient
	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: ns.Name}}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	infos := &v1alpha1.NetworkInfoList{}
	require.NoError(t, r.Client.List(ctx, infos))
	require.Empty(t, infos.Items)

	require.NoError(t, settingsClient.Get(ctx, client.ObjectKeyFromObject(settings), settings))
	settings.Provider, settings.LegacyProvider = netop.NetworkProviderVPC, netop.NetworkProviderVSphereDistributed
	require.NoError(t, settingsClient.Update(ctx, settings))
	patch := gomonkey.ApplyMethod(reflect.TypeOf(r.VPCService), "GetNetworkStackFromNC", func(*vpc.VPCService, *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
		return v1alpha1.VLANBackedVPC, nil
	})
	defer patch.Reset()
	api := r.Client
	writeErr := errors.New("vm-default create unavailable")
	r.Client = &CustomClient{Client: api, CreateFunc: func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
		if obj.GetName() == servicecommon.DefaultVMSubnetSet {
			return writeErr
		}
		return api.Create(ctx, obj, opts...)
	}}
	_, err = r.Reconcile(ctx, req)
	require.ErrorIs(t, err, writeErr)
	pod := &v1alpha1.SubnetSet{}
	require.NoError(t, api.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: servicecommon.DefaultPodSubnetSet}, pod))
	podVersion := pod.ResourceVersion
	require.NoError(t, api.List(ctx, infos))
	require.Len(t, infos.Items, 1)
	infoVersion := infos.Items[0].ResourceVersion

	// Discard controller state while retaining the same API objects.
	restarted := createTestNamespaceReconciler(nil)
	restarted.NSXConfig.K8sConfig = r.NSXConfig.K8sConfig
	restarted.Client, restarted.APIReader, restarted.networkSettings = api, api, settingsClient
	restarted.VPCService.(*vpc.VPCService).Client = api
	for range 2 {
		_, err = restarted.Reconcile(ctx, req)
		require.NoError(t, err)
	}
	sets := &v1alpha1.SubnetSetList{}
	require.NoError(t, api.List(ctx, sets))
	require.Len(t, sets.Items, 2)
	for _, ss := range sets.Items {
		require.Equal(t, []string{shared.Name}, *ss.Spec.SubnetNames)
	}
	require.NoError(t, api.Get(ctx, client.ObjectKeyFromObject(pod), pod))
	require.Equal(t, podVersion, pod.ResourceVersion)
	require.NoError(t, api.List(ctx, infos))
	require.Len(t, infos.Items, 1)
	require.Equal(t, infoVersion, infos.Items[0].ResourceVersion)
	subnets := &v1alpha1.SubnetList{}
	require.NoError(t, api.List(ctx, subnets))
	require.Len(t, subnets.Items, 1)
	require.Equal(t, shared.UID, subnets.Items[0].UID)
}

func TestSharedSubnetAlreadyCreatedForSamePath(t *testing.T) {
	subnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "retained", Namespace: "team", UID: "existing", Annotations: map[string]string{servicecommon.AnnotationAssociatedResource: "p:v:s"}}}
	r := createTestNamespaceReconciler([]client.Object{subnet})
	attempt := subnet.DeepCopy()
	attempt.UID, attempt.ResourceVersion = "", ""
	require.NoError(t, r.createSubnetCRInK8s(context.Background(), attempt))
	require.Equal(t, subnet.UID, attempt.UID)
	list := &v1alpha1.SubnetList{}
	require.NoError(t, r.Client.List(context.Background(), list))
	require.Len(t, list.Items, 1)
}
