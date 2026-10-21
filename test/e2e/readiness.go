package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/test/e2e/internal/readiness"
)

const operatorNamespace = "vmware-system-nsx"

var operatorReadyTimeout = 5 * time.Minute

func (data *TestData) waitForOperatorReady() error {
	if operatorReadyTimeout <= 0 {
		return fmt.Errorf("operator-ready-timeout must be positive")
	}
	cfg := readiness.Config{
		Namespace:         operatorNamespace,
		DeploymentName:    "nsx-ncp",
		WebhookConfigName: "nsx-operator-validating-webhook-configuration",
		WebhookName:       "subnet.validating.crd.nsx.vmware.com",
	}
	probeSpec := v1alpha1.SubnetSpec{IPv4SubnetSize: 16}
	if nsxConfig := data.nsxClient.Client.NsxConfig; nsxConfig != nil && nsxConfig.K8sConfig != nil {
		probeSpec.IPAddressType = nsxConfig.K8sConfig.GetIPAddressType()
	}
	if clusterInfo.podV4NetworkCIDR == "" && clusterInfo.podV6NetworkCIDR != "" {
		probeSpec.IPAddressType = v1alpha1.IPAddressTypeIPv6
	}
	if probeSpec.IPAddressType == v1alpha1.IPAddressTypeIPv6 {
		probeSpec.IPv4SubnetSize = 0
	}
	if probeSpec.IPAddressType == v1alpha1.IPAddressTypeIPv6 || probeSpec.IPAddressType == v1alpha1.IPAddressTypeIPv4IPv6 {
		probeSpec.IPv6PrefixLength = 64
	}
	var lastError error
	var lastMessage string
	err := wait.PollUntilContextTimeout(context.Background(), 2*time.Second, operatorReadyTimeout, true, func(ctx context.Context) (bool, error) {
		lastError = readiness.Check(ctx, data.clientset, cfg)
		if lastError == nil {
			// Exercise API server -> Service -> elected operator -> validator,
			// including TLS. Dry-run leaves no Subnet or NSX resource behind.
			_, lastError = data.crdClientset.CrdV1alpha1().Subnets(operatorNamespace).Create(ctx, &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "e2e-webhook-probe-" + getRandomString(), Namespace: operatorNamespace},
				Spec:       probeSpec,
			}, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		}
		if lastError == nil {
			return true, nil
		}
		if apierrors.IsForbidden(lastError) || apierrors.IsUnauthorized(lastError) || apierrors.IsInvalid(lastError) {
			return false, lastError
		}
		if lastError.Error() != lastMessage {
			lastMessage = lastError.Error()
			log.Info("Waiting for operator prerequisites", "observation", lastMessage)
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("operator rollout/webhook readiness: %w; last observation: %v", err, lastError)
	}
	return nil
}

// collectE2EDiagnostics runs before setup cleanup. Export bounded logs and
// resource status without reading credentials or dumping container env vars.
func collectE2EDiagnostics(reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dir := testOptions.logsExportDir
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "nsx-operator-e2e-")
		if err != nil {
			log.Error(err, "Cannot create diagnostic directory")
			return
		}
	}
	dir = filepath.Join(dir, reason)
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Error(err, "Cannot create diagnostic directory", "path", dir)
		return
	}
	writeJSON := func(name string, obj interface{}) {
		contents, err := json.MarshalIndent(obj, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, name+".json"), contents, 0600)
		}
		if err != nil {
			log.Error(err, "Cannot write diagnostics", "resource", name)
		}
	}
	if dep, err := testData.clientset.AppsV1().Deployments(operatorNamespace).Get(ctx, "nsx-ncp", metav1.GetOptions{}); err == nil {
		log.Info("Operator deployment diagnostics", "generation", dep.Generation, "status", dep.Status)
		writeJSON("deployment", map[string]interface{}{"generation": dep.Generation, "status": dep.Status})
	}
	if endpoints, err := testData.clientset.DiscoveryV1().EndpointSlices(operatorNamespace).List(ctx, metav1.ListOptions{LabelSelector: "kubernetes.io/service-name=vmware-system-nsx-operator-webhook-service"}); err == nil {
		writeJSON("webhook-endpoints", endpoints)
		log.Info("Webhook endpoint diagnostics", "endpointSlices", endpoints.Items)
	}
	pods, err := testData.clientset.CoreV1().Pods(operatorNamespace).List(ctx, metav1.ListOptions{LabelSelector: "component=nsx-ncp"})
	if err == nil {
		for _, pod := range pods.Items {
			log.Info("Operator pod diagnostics", "pod", pod.Name, "status", pod.Status)
			writeJSON(pod.Name, map[string]interface{}{"name": pod.Name, "labels": pod.Labels, "status": pod.Status})
			for _, container := range []string{"nsx-operator", "nsx-ncp"} {
				tail := int64(200)
				body, logErr := testData.clientset.CoreV1().Pods(operatorNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container, TailLines: &tail, Timestamps: true}).DoRaw(ctx)
				if logErr == nil {
					logErr = os.WriteFile(filepath.Join(dir, pod.Name+"-"+container+".log"), body, 0600)
				}
				if logErr != nil {
					log.Error(logErr, "Cannot export container logs", "pod", pod.Name, "container", container)
				}
			}
		}
	}
	for _, ns := range append([]string{operatorNamespace}, allVCNamespaces...) {
		if obj, getErr := testData.clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); getErr == nil {
			log.Info("Namespace diagnostics", "namespace", ns, "status", obj.Status)
			writeJSON(ns+"-namespace", obj.Status)
			if infos, listErr := testData.crdClientset.CrdV1alpha1().NetworkInfos(ns).List(ctx, metav1.ListOptions{}); listErr == nil {
				writeJSON(ns+"-networkinfos", infos)
			}
			if sets, listErr := testData.crdClientset.CrdV1alpha1().SubnetSets(ns).List(ctx, metav1.ListOptions{}); listErr == nil {
				writeJSON(ns+"-subnetsets", sets)
			}
		}
		if events, listErr := testData.clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{Limit: 50}); listErr == nil {
			writeJSON(ns+"-events", events)
		}
	}
	log.Info("Exported e2e diagnostics; archive this directory in CI", "path", dir)
}
