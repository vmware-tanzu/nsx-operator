/*
Copyright © 2026 Broadcom, Inc. All Rights Reserved.

	SPDX-License-Identifier: Apache-2.0
*/
package infrastructure

import (
	"context"
	"strings"

	netapi "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const ncpNamespace = "vmware-system-nsx"

// KubernetesReadiness reads the platform-published provider configuration and
// the normal NSX operator's controller-ready role/container. It never treats the
// migration sidecar's readiness or existence of VPC namespaces as NCP readiness.
type KubernetesReadiness struct{ Reader client.Reader }

func (r *KubernetesReadiness) Check(ctx context.Context, resuming bool, projectPath string) error {
	var config netapi.WorkloadNetworkConfiguration
	if err := r.Reader.Get(ctx, client.ObjectKey{Name: netapi.WorkloadNetworkConfigurationName}, &config); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return issue("ProviderNotRegistered", "NSX_VPC is not published in WorkloadNetworkConfiguration/default; complete vCenter provider registration and platform configuration propagation")
		}
		return readFailure("Cannot read Supervisor provider registration", err)
	}
	if !config.DeletionTimestamp.IsZero() {
		return issue("ProviderNotReady", "Supervisor provider configuration is being deleted")
	}
	active := config.Spec.ActiveSystemProvider
	if active != netapi.NetworkProviderVSphereDistributed && (!resuming || active != netapi.NetworkProviderVPC) {
		return issue("UnexpectedActiveProvider", "A new migration requires the VDS system provider; an existing Execute/Finalize checkpoint is required to resume on VPC")
	}
	vpcProviders := 0
	for _, provider := range config.Spec.Providers {
		if provider.Type != netapi.NetworkProviderVPC {
			continue
		}
		vpcProviders++
		if provider.SystemConfiguration == nil {
			return issue("ProviderNotReady", "The registered VPC provider has no system configuration")
		}
		vpc := provider.SystemConfiguration.VPCConfig
		configuredProject := vpc.AutoCreateConfig.NSXProject
		if vpc.VPC != "" {
			parts := strings.Split(vpc.VPC, "/")
			if len(parts) != 7 || parts[1] != "orgs" || parts[3] != "projects" || parts[5] != "vpcs" || parts[6] == "" {
				return issue("ProviderNotReady", "The registered VPC provider has an invalid existing VPC path")
			}
			configuredProject = strings.Join(parts[:5], "/")
		}
		if configuredProject != projectPath {
			return issue("ProviderProjectMismatch", "The registered VPC provider and transition select different NSX projects; align provider registration before starting migration")
		}
	}
	if vpcProviders != 1 {
		return issue("ProviderNotRegistered", "Exactly one VPC provider must be registered in WorkloadNetworkConfiguration/default")
	}
	if config.Status == nil {
		return issue("ProviderNotReady", "Supervisor provider configuration has not been reconciled")
	}
	ready := meta.FindStatusCondition(config.Status.Conditions, netapi.WorkloadNetworkConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue || ready.ObservedGeneration != config.Generation {
		return issue("ProviderNotReady", "Wait for the current Supervisor provider configuration to become Ready")
	}
	var pods corev1.PodList
	if err := r.Reader.List(ctx, &pods, client.InNamespace(ncpNamespace), client.MatchingLabels{"nsx-operator-role": "master"}); err != nil {
		return readFailure("Cannot read NCP controller readiness", err)
	}
	var masters []corev1.Pod
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp.IsZero() {
			masters = append(masters, pod)
		}
	}
	if len(masters) != 1 {
		return issue("NCPNotReady", "Wait for exactly one active NSX operator controller in vmware-system-nsx")
	}
	master := masters[0]
	if master.Status.Phase != corev1.PodRunning {
		return issue("NCPNotReady", "The active NCP Pod must have a running, ready nsx-operator container; migration sidecar readiness is insufficient")
	}
	for _, container := range master.Status.ContainerStatuses {
		if container.Name == "nsx-operator" && container.Ready && container.State.Running != nil && container.ContainerID != "" {
			return nil
		}
	}
	return issue("NCPNotReady", "The active NCP Pod must have a running, ready nsx-operator container; migration sidecar readiness is insufficient")
}
