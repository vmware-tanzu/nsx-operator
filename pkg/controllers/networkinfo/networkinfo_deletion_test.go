/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkinfo

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	mockvpc "github.com/vmware-tanzu/nsx-operator/pkg/mock/vpcclient"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func deletionTestVPC(namespace, id string) *model.Vpc {
	return &model.Vpc{
		Id:          servicecommon.String(id),
		DisplayName: servicecommon.String(id),
		Path:        servicecommon.String("/orgs/default/projects/default/vpcs/" + id),
		Tags: []model.Tag{
			{Scope: servicecommon.String(servicecommon.TagScopeNamespace), Tag: servicecommon.String(namespace)},
			{Scope: servicecommon.String(servicecommon.TagScopeNamespaceUID), Tag: servicecommon.String(namespace + "-uid")},
		},
	}
}

func newDeletionTestReconciler(t *testing.T, funcs interceptor.Funcs, objects []client.Object, vpcs ...*model.Vpc) (*NetworkInfoReconciler, *mockvpc.MockVpcsClient) {
	t.Helper()
	r := createNetworkInfoReconciler(nil)
	k8sClient := fake.NewClientBuilder().WithScheme(r.Client.Scheme()).WithStatusSubresource(&v1alpha1.VPCNetworkConfiguration{}).
		WithObjects(objects...).WithInterceptorFuncs(funcs).Build()
	r.Client = k8sClient
	r.Service.Client = k8sClient
	r.Service.VpcStore.Indexer = cache.NewIndexer(func(obj interface{}) (string, error) {
		return *obj.(*model.Vpc).Id, nil
	}, cache.Indexers{servicecommon.TagScopeNamespace: func(obj interface{}) ([]string, error) {
		return []string{filterTagFromNSXVPC(obj.(*model.Vpc), servicecommon.TagScopeNamespace)}, nil
	}})
	for _, vpc := range vpcs {
		require.NoError(t, r.Service.VpcStore.Add(vpc))
	}
	vpcClient := mockvpc.NewMockVpcsClient(gomock.NewController(t))
	r.Service.NSXClient.VPCClient = vpcClient
	return r, vpcClient
}

func TestDeleteVPCsUpdatesConfigurationAfterNamespaceDeletion(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "last namespace", true: "shared configuration"}[shared], func(t *testing.T) {
			stale := deletionTestVPC("deleted-ns", "stale-vpc")
			alive := deletionTestVPC("alive-ns", "alive-vpc")
			staleInfo := v1alpha1.VPCInfo{Name: *stale.DisplayName, VPCPath: *stale.Path}
			aliveInfo := v1alpha1.VPCInfo{Name: *alive.DisplayName, VPCPath: *alive.Path}
			nc := &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "shared-config"},
				Status:     v1alpha1.VPCNetworkConfigurationStatus{VPCs: []v1alpha1.VPCInfo{staleInfo}},
			}
			// The same display name in another project must not associate an unrelated config.
			unrelatedInfo := v1alpha1.VPCInfo{Name: *stale.DisplayName, VPCPath: "/orgs/default/projects/unrelated/vpcs/stale-vpc"}
			unrelated := &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "unrelated-config"},
				Status:     v1alpha1.VPCNetworkConfigurationStatus{VPCs: []v1alpha1.VPCInfo{unrelatedInfo}},
			}
			objects := []client.Object{nc, unrelated}
			vpcs := []*model.Vpc{stale}
			if shared {
				nc.Status.VPCs = append(nc.Status.VPCs, aliveInfo)
				objects = append(objects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
					Name: "alive-ns", UID: types.UID("alive-ns-uid"),
					Annotations: map[string]string{servicecommon.AnnotationVPCNetworkConfig: nc.Name},
				}})
				vpcs = append(vpcs, alive)
			}
			r, nsxClient := newDeletionTestReconciler(t, interceptor.Funcs{}, objects, vpcs...)
			nsxClient.EXPECT().Delete("default", "default", "stale-vpc").Return(nil)

			require.NoError(t, r.deleteVPCsByNamespace(context.Background(), "deleted-ns"))
			require.Empty(t, r.Service.GetVPCsByNamespace("deleted-ns"))
			got := &v1alpha1.VPCNetworkConfiguration{}
			require.NoError(t, r.Client.Get(context.Background(), types.NamespacedName{Name: nc.Name}, got))
			if shared {
				require.Equal(t, []v1alpha1.VPCInfo{aliveInfo}, got.Status.VPCs)
			} else {
				require.Empty(t, got.Status.VPCs)
			}
			require.NoError(t, r.Client.Get(context.Background(), types.NamespacedName{Name: unrelated.Name}, got))
			require.Equal(t, []v1alpha1.VPCInfo{unrelatedInfo}, got.Status.VPCs)
		})
	}
}

func TestDeleteVPCsRetriesConfigurationLookupErrors(t *testing.T) {
	for _, operation := range []string{"namespace get", "configuration list", "alive namespace list"} {
		t.Run(operation, func(t *testing.T) {
			stale := deletionTestVPC("deleted-ns", "stale-vpc")
			staleInfo := v1alpha1.VPCInfo{Name: *stale.DisplayName, VPCPath: *stale.Path}
			nc := &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "config"},
				Status:     v1alpha1.VPCNetworkConfigurationStatus{VPCs: []v1alpha1.VPCInfo{staleInfo}},
			}
			fail := true
			listCalls := 0
			lookupErr := errors.New("temporary API failure")
			funcs := interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*corev1.Namespace); ok && operation == "namespace get" && fail {
						return lookupErr
					}
					return c.Get(ctx, key, obj, opts...)
				},
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if _, ok := list.(*v1alpha1.VPCNetworkConfigurationList); ok && operation == "configuration list" && fail {
						return lookupErr
					}
					if _, ok := list.(*corev1.NamespaceList); ok {
						listCalls++
						// The first list checks namespace UIDs; the second resolves live VPCs.
						if operation == "alive namespace list" && listCalls == 2 && fail {
							return lookupErr
						}
					}
					return c.List(ctx, list, opts...)
				},
			}
			r, nsxClient := newDeletionTestReconciler(t, funcs, []client.Object{nc}, stale)
			// Only the retry may delete the VPC; a failed lookup must preserve its association.
			require.ErrorContains(t, r.deleteVPCsByNamespace(context.Background(), "deleted-ns"), lookupErr.Error())
			require.Len(t, r.Service.GetVPCsByNamespace("deleted-ns"), 1)
			got := &v1alpha1.VPCNetworkConfiguration{}
			require.NoError(t, r.Client.Get(context.Background(), types.NamespacedName{Name: nc.Name}, got))
			require.Equal(t, []v1alpha1.VPCInfo{staleInfo}, got.Status.VPCs)

			fail = false
			nsxClient.EXPECT().Delete("default", "default", "stale-vpc").Return(nil)
			require.NoError(t, r.deleteVPCsByNamespace(context.Background(), "deleted-ns"))
			require.NoError(t, r.Client.Get(context.Background(), types.NamespacedName{Name: nc.Name}, got))
			require.Empty(t, got.Status.VPCs)
		})
	}
}
