package vpc

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type unavailableConfigOwnerClient struct{ client.Client }

func (c unavailableConfigOwnerClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if key.Name == "owner" {
		return context.DeadlineExceeded
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestInheritedConfigReverseLookupUsesNamespaceSnapshot(t *testing.T) {
	child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging", Annotations: map[string]string{common.AnnotationSharedVPCNamespace: "owner"}}}
	owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "owner", Annotations: map[string]string{common.AnnotationVPCNetworkConfig: "shared"}}}
	api := fake.NewClientBuilder().WithObjects(child, owner).Build()
	svc := &VPCService{Service: common.Service{NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{EnableNetworkMigration: true}}, Client: unavailableConfigOwnerClient{api}}}
	names, err := svc.GetNamespacesByNetworkconfigName("shared")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"owner", "staging"}, names, "an extra owner Get must not lose an inherited namespace already resolvable from the list")
}

func TestInheritedConfigReverseLookup(t *testing.T) {
	child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "vmware-system-net-staging", Annotations: map[string]string{common.AnnotationSharedVPCNamespace: "kube-system"}}}
	owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", Annotations: map[string]string{common.AnnotationVPCNetworkConfig: "system"}}}
	service := &VPCService{Service: common.Service{NSXConfig: &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{EnableNetworkMigration: true}}, Client: fake.NewClientBuilder().WithObjects(child, owner).Build()}}
	patches := gomonkey.ApplyMethod(service, "GetDefaultNetworkConfig", func(_ *VPCService) (*v1alpha1.VPCNetworkConfiguration, error) {
		return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "default"}}, nil
	})
	defer patches.Reset()
	effective, err := service.GetNetworkconfigNameFromNS(context.Background(), child.Name)
	require.NoError(t, err)
	users, err := service.GetNamespacesByNetworkconfigName(effective)
	require.NoError(t, err)
	t.Logf("effective config=%s; namespaces returned for its update fanout=%v", effective, users)
	require.ElementsMatch(t, []string{child.Name, owner.Name}, users)
	// Both queries must follow an owner configuration change.
	owner.Annotations[common.AnnotationVPCNetworkConfig] = "updated"
	require.NoError(t, service.Client.Update(context.Background(), owner))
	effective, err = service.GetNetworkconfigNameFromNS(context.Background(), child.Name)
	require.NoError(t, err)
	require.Equal(t, "updated", effective)
	users, err = service.GetNamespacesByNetworkconfigName("updated")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{child.Name, owner.Name}, users)
	users, err = service.GetNamespacesByNetworkconfigName("system")
	require.NoError(t, err)
	require.Empty(t, users)
	// Explicit child configuration takes precedence over the shared owner.
	child.Annotations[common.AnnotationVPCNetworkConfig] = "local"
	require.NoError(t, service.Client.Update(context.Background(), child))
	effective, err = service.GetNetworkconfigNameFromNS(context.Background(), child.Name)
	require.NoError(t, err)
	require.Equal(t, "local", effective)
	users, err = service.GetNamespacesByNetworkconfigName("updated")
	require.NoError(t, err)
	require.Equal(t, []string{owner.Name}, users)
	users, err = service.GetNamespacesByNetworkconfigName("local")
	require.NoError(t, err)
	require.Equal(t, []string{child.Name}, users)
}

func TestDisabledMigrationSkipsInheritedConfigHelpers(t *testing.T) {
	child := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging", Annotations: map[string]string{common.AnnotationSharedVPCNamespace: "owner"}}}
	local := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "local", Annotations: map[string]string{common.AnnotationVPCNetworkConfig: "local-config"}}}
	service := &VPCService{Service: common.Service{NSXConfig: &config.NSXOperatorConfig{}, Client: fake.NewClientBuilder().WithObjects(child, local).Build()}}
	patches := gomonkey.ApplyFunc((*VPCService).getEffectiveNetworkconfigName, func(*VPCService, context.Context, *corev1.Namespace) (string, error) {
		t.Error("disabled migration entered inherited configuration resolution")
		return "unexpected", nil
	})
	defer patches.Reset()
	patches.ApplyFunc(networkconfigSourceNamespace, func(*corev1.Namespace) string {
		t.Error("disabled migration entered owner selection")
		return "unexpected"
	})
	patches.ApplyMethod(service, "GetDefaultNetworkConfig", func(*VPCService) (*v1alpha1.VPCNetworkConfiguration, error) {
		return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "default"}}, nil
	})
	for _, tc := range []struct{ name, expected string }{{child.Name, "default"}, {local.Name, "local-config"}} {
		name, err := service.GetNetworkconfigNameFromNS(context.Background(), tc.name)
		require.NoError(t, err)
		require.Equal(t, tc.expected, name)
		names, err := service.GetNamespacesByNetworkconfigName(tc.expected)
		require.NoError(t, err)
		require.Equal(t, []string{tc.name}, names)
	}
}
