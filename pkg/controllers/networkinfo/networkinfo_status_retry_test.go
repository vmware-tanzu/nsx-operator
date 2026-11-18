/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestSetVPCNetworkConfigurationStatusWithLBSErrors(t *testing.T) {
	wantErr := fmt.Errorf("API unavailable")
	for _, tc := range []struct {
		name      string
		getErr    error
		updateErr error
	}{
		{name: "get fails", getErr: wantErr},
		{name: "status update fails", updateErr: wantErr},
		{name: "success"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			nc := &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "system"},
				Status:     v1alpha1.VPCNetworkConfigurationStatus{VPCs: []v1alpha1.VPCInfo{{Name: "system-vpc", AVISESubnetPath: "/old-avi-path"}}},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc).WithObjects(nc).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if tc.getErr != nil {
						return tc.getErr
					}
					return c.Get(ctx, key, obj, opts...)
				},
				SubResourceUpdate: func(ctx context.Context, c client.Client, name string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					if tc.updateErr != nil {
						return tc.updateErr
					}
					return c.SubResource(name).Update(ctx, obj, opts...)
				},
			}).Build()
			err := setVPCNetworkConfigurationStatusWithLBS(ctx, c, "system", "system-vpc", "/new-avi-path", "", "/vpc")
			if tc.getErr != nil || tc.updateErr != nil {
				require.ErrorIs(t, err, wantErr)
			} else {
				require.NoError(t, err)
			}
			if tc.getErr == nil {
				actual := &v1alpha1.VPCNetworkConfiguration{}
				require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "system"}, actual))
				expected := "/new-avi-path"
				if tc.updateErr != nil {
					expected = "/old-avi-path"
				}
				require.Equal(t, expected, actual.Status.VPCs[0].AVISESubnetPath)
			}
		})
	}
}

func TestSystemAVIStatusWriteRetriesUntilRecovery(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", UID: "system-uid", Annotations: map[string]string{servicecommon.AnnotationVPCNetworkConfig: "system"}}}
	nc := &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "system"},
		Spec:       v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/p", VPCConnectivityProfile: "/orgs/default/projects/p/vpc-connectivity-profiles/default"},
		Status:     v1alpha1.VPCNetworkConfigurationStatus{Conditions: []v1alpha1.Condition{{Type: v1alpha1.GatewayConnectionReady, Status: corev1.ConditionTrue}}},
	}
	ni := &v1alpha1.NetworkInfo{ObjectMeta: metav1.ObjectMeta{Name: "system-ni", Namespace: "kube-system"}}
	writeErr := apierrors.NewConflict(schema.GroupResource{Group: "crd.nsx.vmware.com", Resource: "vpcnetworkconfigurations"}, "system", fmt.Errorf("concurrent update"))
	var statusErr error = writeErr
	pathWrites, networkInfoUpdates := 0, 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc, ns).WithObjects(ns, nc, ni).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*v1alpha1.NetworkInfo); ok {
				networkInfoUpdates++
			}
			return c.Update(ctx, obj, opts...)
		},
		SubResourceUpdate: func(ctx context.Context, c client.Client, name string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if cfg, ok := obj.(*v1alpha1.VPCNetworkConfiguration); ok && len(cfg.Status.VPCs) > 0 && cfg.Status.VPCs[0].AVISESubnetPath != "" {
				pathWrites++
				if statusErr != nil {
					return statusErr
				}
			}
			return c.SubResource(name).Update(ctx, obj, opts...)
		},
	}).Build()
	r := createNetworkInfoReconciler(nil)
	r.Client, r.Service.Client = c, c
	r.StatusUpdater = common.NewStatusUpdater(c, r.Service.NSXConfig, r.Recorder, MetricResType, "VPC", "NetworkInfo")
	created := &model.Vpc{Id: servicecommon.String("system-vpc"), DisplayName: servicecommon.String("system-vpc"), Path: servicecommon.String("/orgs/default/projects/p/vpcs/system-vpc"), LoadBalancerVpcEndpoint: &model.LoadBalancerVPCEndpoint{Enabled: servicecommon.Bool(true)}}
	profile := &model.VpcConnectivityProfile{ExternalIpBlocks: []string{"external"}, ServiceGateway: &model.VpcServiceGatewayConfig{Enable: servicecommon.Bool(true), EdgeClusterPaths: []string{"/infra/sites/default/enforcement-points/default/edge-clusters/ec"}, NatConfig: &model.VpcNatConfig{EnableDefaultSnat: servicecommon.Bool(true)}}}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(r.Service), "GetLBProvider", func(_ *vpc.VPCService) (vpc.LBProvider, error) { return vpc.AVILB, nil })
	defer patches.Reset()
	patches.ApplyMethod(reflect.TypeOf(r.Service), "CreateOrUpdateVPC", func(_ *vpc.VPCService, _ context.Context, _ *v1alpha1.NetworkInfo, _ *v1alpha1.VPCNetworkConfiguration, _ vpc.LBProvider, _ bool, _ bool) (*model.Vpc, error) {
		return created, nil
	})
	patches.ApplyMethod(reflect.TypeOf(r.Service), "GetVpcConnectivityProfile", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration, _ string) (*model.VpcConnectivityProfile, error) {
		return profile, nil
	})
	patches.ApplyMethod(reflect.TypeOf(r.Service), "GetDefaultSNATIP", func(_ *vpc.VPCService, _ model.Vpc) (string, error) { return "192.0.2.1", nil })
	patches.ApplyMethod(reflect.TypeOf(r.Service), "GetNetworkStackFromNC", func(_ *vpc.VPCService, _ *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
		return v1alpha1.FullStackVPC, nil
	})
	const subnetPath = "/orgs/default/projects/p/vpcs/system-vpc/subnets/_services"
	patches.ApplyMethod(reflect.TypeOf(r.Service), "GetAVISubnetInfo", func(_ *vpc.VPCService, _ model.Vpc) (string, []string, error) {
		return subnetPath, []string{"100.64.0.0/28"}, nil
	})
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "kube-system", Name: "system-ni"}}
	// Every failure must request another reconcile, without relying on a NetworkInfo update event.
	for attempt := 1; attempt <= 3; attempt++ {
		result, err := r.Reconcile(ctx, req)
		require.ErrorIs(t, err, writeErr)
		require.Equal(t, common.ResultRequeue, result)
		require.Equal(t, attempt, pathWrites)
		require.Zero(t, networkInfoUpdates)
		actual := &v1alpha1.VPCNetworkConfiguration{}
		require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "system"}, actual))
		require.Empty(t, actual.Status.VPCs)
	}
	statusErr = nil
	result, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, common.ResultNormal, result)
	require.Equal(t, 4, pathWrites)
	require.Equal(t, 1, networkInfoUpdates)
	actual := &v1alpha1.VPCNetworkConfiguration{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "system"}, actual))
	require.Equal(t, subnetPath, actual.Status.VPCs[0].AVISESubnetPath)
}
