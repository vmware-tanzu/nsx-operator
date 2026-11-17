/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package admission

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

// The webhook endpoint is deliberately unavailable so only API-server-side
// match conditions can allow a controller to recreate a Pod.
func TestWorkloadWebhookControllerRecoveryOnAPIServer(t *testing.T) {
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		t.Skip("KUBEBUILDER_ASSETS with Kubernetes 1.33 is required")
	}
	assetRoot, err := os.OpenRoot(assets)
	if err != nil {
		t.Skipf("KUBEBUILDER_ASSETS=%q is unavailable: %v", assets, err)
	}
	t.Cleanup(func() { require.NoError(t, assetRoot.Close()) })
	for _, binary := range []string{"etcd", "kube-apiserver"} {
		if _, err := assetRoot.Stat(binary); err != nil {
			t.Skipf("KUBEBUILDER_ASSETS=%q is missing %s: %v", assets, binary, err)
		}
	}
	env := &envtest.Environment{BinaryAssetsDirectory: assets}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, admissionv1.AddToScheme(scheme))
	admin, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, admin.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "migration-test"}}))
	data, err := os.ReadFile("../../../build/yaml/migration/admission/workload-webhook.yaml")
	require.NoError(t, err)
	var config admissionv1.ValidatingWebhookConfiguration
	require.NoError(t, yaml.Unmarshal(data, &config))
	url := "https://127.0.0.1:1/unavailable"
	for i := range config.Webhooks {
		config.Webhooks[i].ClientConfig = admissionv1.WebhookClientConfig{URL: &url}
	}
	require.NoError(t, admin.Create(ctx, &config))

	impersonate := func(username string) client.Client {
		userConfig := rest.CopyConfig(cfg)
		userConfig.Impersonate = rest.ImpersonationConfig{UserName: username, Groups: []string{"system:masters"}}
		c, err := client.New(userConfig, client.Options{Scheme: scheme})
		require.NoError(t, err)
		return c
	}
	pod := func(name string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "migration-test"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Image: "example.invalid/test"}}}}
	}
	controller := impersonate("system:serviceaccount:kube-system:replicaset-controller")
	user := impersonate("ordinary-user")
	require.NoError(t, controller.Create(ctx, pod("controller-pod")))
	require.ErrorContains(t, user.Create(ctx, pod("user-pod")), "failed calling webhook")

	lb := func(name string) *corev1.Service {
		return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "migration-test"},
			Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Ports: []corev1.ServicePort{{Port: 80}}}}
	}
	require.NoError(t, impersonate(NCPControllerUser).Create(ctx, lb("controller-lb")))
	require.ErrorContains(t, user.Create(ctx, lb("user-lb")), "failed calling webhook")
}
