/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespace

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func TestServiceNamespaceManagedDefaultsRespectProvider(t *testing.T) {
	for _, tc := range []struct {
		name                                         string
		enabled, explicit, perNamespace, wantManaged bool
	}{
		{"inherited_without_shared_defaults", true, false, true, false},
		{"explicit_vpc", true, true, true, true},
		{"disabled_inherited", false, false, true, false},
		{"disabled_explicit_vpc", false, true, true, true},
		{"legacy_provider", true, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			patches := gomonkey.ApplyFunc(config.IsPerNamespaceProvidersSupported, func() bool { return tc.perNamespace })
			defer patches.Reset()
			patches.ApplyFunc(config.HasVPCNamespaces, func() bool { return true })
			child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "service-ns", Labels: map[string]string{common.SupervisorServiceIDLabel: "test-service", "managedBy": common.VsphereAppPlatformLabel}, Annotations: map[string]string{servicecommon.AnnotationSharedVPCNamespace: "owner"}}}
			if tc.explicit {
				child.Annotations[servicecommon.AnnotationVPCNetworkConfig] = "network"
			}
			owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "owner", Annotations: map[string]string{servicecommon.AnnotationVPCNetworkConfig: "network"}}}
			r := createNameSpaceReconciler([]client.Object{child, owner})
			r.APIReader = r.Client
			r.NSXConfig.CoeConfig = &config.CoeConfig{EnableNetworkMigration: tc.enabled}
			svc := r.VPCService.(*vpc.VPCService)
			nc := &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "network"}, Spec: v1alpha1.VPCNetworkConfigurationSpec{DefaultSubnetSize: 32}}
			patches.ApplyMethod(svc, "GetDefaultNetworkConfig", func(*vpc.VPCService) (*v1alpha1.VPCNetworkConfiguration, error) { return nc, nil })
			patches.ApplyMethod(svc, "GetVPCNetworkConfig", func(*vpc.VPCService, string) (*v1alpha1.VPCNetworkConfiguration, bool, error) { return nc, true, nil })
			patches.ApplyMethod(svc, "ValidateNetworkConfig", func(*vpc.VPCService, *v1alpha1.VPCNetworkConfiguration) error { return nil })
			patches.ApplyMethod(svc, "GetNetworkStackFromNC", func(*vpc.VPCService, *v1alpha1.VPCNetworkConfiguration) (v1alpha1.NetworkStackType, error) {
				return v1alpha1.FullStackVPC, nil
			})
			for retry := 0; retry < 2; retry++ {
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(child)})
				require.NoError(t, err)
			}
			sets := &v1alpha1.SubnetSetList{}
			require.NoError(t, r.Client.List(ctx, sets, client.InNamespace(child.Name)))
			if tc.wantManaged {
				require.Len(t, sets.Items, 1)
				require.Nil(t, sets.Items[0].Spec.SubnetNames)
				require.Equal(t, servicecommon.DefaultPodSubnetSet, sets.Items[0].Name)
				require.Equal(t, 32, sets.Items[0].Spec.IPv4SubnetSize)
			} else {
				require.Empty(t, sets.Items)
			}
		})
	}
}
