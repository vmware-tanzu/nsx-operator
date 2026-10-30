package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Collect status and events before callers remove the failed test's resources.
// Do not include Pod specs, which may contain credentials or unrelated inputs.
func (data *TestData) logNamespaceDiagnostics(namespace string) {
	ctx, cancel := context.WithTimeout(context.Background(), apiRequestTimeout)
	defer cancel()
	status := make(map[string]interface{})
	if ns, err := data.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{}); err == nil {
		status["namespace"] = ns.Status
	} else {
		log.Info("Unable to collect Namespace status", "namespace", namespace, "error", err)
	}
	if pods, err := data.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{}); err == nil {
		podStatus := make(map[string]interface{}, len(pods.Items))
		for _, pod := range pods.Items {
			podStatus[pod.Name] = pod.Status
		}
		status["pods"] = podStatus
	} else {
		log.Info("Unable to collect Pod status", "namespace", namespace, "error", err)
	}
	if events, err := data.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{Limit: 100}); err == nil {
		status["events"] = events.Items
	} else {
		log.Info("Unable to collect Namespace events", "namespace", namespace, "error", err)
	}
	content, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		log.Info("Unable to encode failure diagnostics", "namespace", namespace, "error", err)
		return
	}
	log.Info("Namespace failure diagnostics", "namespace", namespace, "status", string(content))
	if testOptions.logsExportDir == "" {
		return
	}
	if err := os.MkdirAll(testOptions.logsExportDir, 0700); err != nil {
		log.Info("Unable to create diagnostics directory", "error", err)
		return
	}
	name := fmt.Sprintf("%s-%d.json", namespace, time.Now().UnixNano())
	if err := os.WriteFile(filepath.Join(testOptions.logsExportDir, name), content, 0600); err != nil {
		log.Info("Unable to save failure diagnostics", "namespace", namespace, "error", err)
	}
}
