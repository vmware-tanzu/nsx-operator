/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/ini.v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const ncpConfigMapName = "nsx-ncp-config"

// +kubebuilder:rbac:groups="",resources=configmaps,resourceNames=nsx-ncp-config,verbs=get,namespace=vmware-system-nsx

// ReadSupervisorIDCandidateFromNCPConfig reads the NCP adaptor's cluster
// identifier from [coe].cluster. The caller must verify it against vCenter
// before treating it as the Supervisor ID.
func ReadSupervisorIDCandidateFromNCPConfig(ctx context.Context, reader client.Reader, namespace string) (string, error) {
	if reader == nil || namespace == "" {
		return "", fmt.Errorf("Kubernetes reader and NCP configuration namespace are required")
	}

	var configMap corev1.ConfigMap
	if err := reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ncpConfigMapName}, &configMap); err != nil {
		return "", fmt.Errorf("read ConfigMap %s/%s: %w", namespace, ncpConfigMapName, err)
	}

	configText := configMap.Data["ncp.ini"]
	if strings.TrimSpace(configText) == "" {
		return "", fmt.Errorf("ConfigMap %s/%s has no ncp.ini data", namespace, ncpConfigMapName)
	}

	config, err := ini.Load([]byte(configText))
	if err != nil {
		return "", fmt.Errorf("parse ncp.ini in ConfigMap %s/%s: %w", namespace, ncpConfigMapName, err)
	}
	supervisorID := strings.TrimSpace(config.Section("coe").Key("cluster").String())
	if supervisorID == "" {
		return "", fmt.Errorf("ncp.ini in ConfigMap %s/%s has no [coe] cluster value", namespace, ncpConfigMapName)
	}
	return supervisorID, nil
}
