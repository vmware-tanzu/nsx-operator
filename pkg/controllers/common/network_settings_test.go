/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

func TestNamespaceDefaultNetworking(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, netop.AddToScheme(scheme))
	for _, tc := range []struct {
		name                                   string
		provider, legacy                       netop.NetworkProvider
		associated, bound, provision, migrated bool
	}{
		{name: "old native namespace", provision: true},
		{name: "coordinated but not yet propagated", associated: true, bound: true},
		{name: "VDS with prepared target", provider: netop.NetworkProviderVSphereDistributed, bound: true},
		{name: "native VPC", provider: netop.NetworkProviderVPC, provision: true},
		{name: "VDS cutover waiting for target", provider: netop.NetworkProviderVPC, legacy: netop.NetworkProviderVSphereDistributed, migrated: true},
		{name: "VDS cutover", provider: netop.NetworkProviderVPC, legacy: netop.NetworkProviderVSphereDistributed, bound: true, provision: true, migrated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team"}}
			if tc.associated {
				ns.Labels = map[string]string{netop.NamespaceNetworkLabelKey: "config"}
			}
			if tc.bound {
				ns.Annotations = map[string]string{config.VPCNetworkConfigAnnotation: "target"}
			}
			b := fake.NewClientBuilder().WithScheme(scheme)
			if tc.provider != "" {
				b = b.WithObjects(&netop.NetworkSettings{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: ns.Name}, Provider: tc.provider, LegacyProvider: tc.legacy})
			}
			provision, migrated, err := NamespaceDefaultNetworking(context.Background(), b.Build(), ns)
			require.NoError(t, err)
			require.Equal(t, tc.provision, provision)
			require.Equal(t, tc.migrated, migrated)
		})
	}
}
