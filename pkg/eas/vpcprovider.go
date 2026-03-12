/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package eas

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

const (
	annotationVPCNetworkConfig = "nsx.vmware.com/vpc_network_config"
	annotationDefaultConfig    = "nsx.vmware.com/default"
)

// VPCInfoProvider is a minimal interface for resolving namespace to VPC info.
// This is a subset of common.VPCServiceProvider, used by EAS storage layer.
type VPCInfoProvider interface {
	ListVPCInfo(ns string) []common.VPCResourceInfo
	// ListAllVPCNamespaces returns all namespaces that have an associated VPC.
	ListAllVPCNamespaces() []string
}

// K8sVPCInfoProvider resolves namespace→VPC mapping by reading
// VPCNetworkConfiguration CRDs from the Kubernetes API.
type K8sVPCInfoProvider struct {
	client client.Client
}

// NewK8sVPCInfoProvider creates a new provider with the given k8s client.
func NewK8sVPCInfoProvider(c client.Client) *K8sVPCInfoProvider {
	return &K8sVPCInfoProvider{client: c}
}

// ListVPCInfo resolves a namespace to its associated VPC(s).
// Flow: namespace annotation → VPCNetworkConfiguration → VPC path → VPCResourceInfo
func (p *K8sVPCInfoProvider) ListVPCInfo(ns string) []common.VPCResourceInfo {
	log := logger.Log
	ctx := context.Background()

	// Step 1: Get namespace annotation to find the VPCNetworkConfiguration name.
	namespace := &corev1.Namespace{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: ns}, namespace); err != nil {
		log.Debug("Failed to get namespace", "namespace", ns, "error", err)
		return nil
	}

	ncName := namespace.Annotations[annotationVPCNetworkConfig]
	if ncName == "" {
		// Fallback to default VPCNetworkConfiguration.
		log.Debug("No VPC network config annotation, using default", "namespace", ns)
		nc, err := p.findDefaultNetworkConfig(ctx)
		if err != nil || nc == nil {
			log.Debug("No default VPCNetworkConfiguration found", "namespace", ns, "error", err)
			return nil
		}
		return p.extractVPCInfoFromNC(nc)
	}

	// Step 2: Get the VPCNetworkConfiguration.
	log.Debug("Resolving VPC info", "namespace", ns, "networkConfig", ncName)
	nc := &vpcv1alpha1.VPCNetworkConfiguration{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: ncName}, nc); err != nil {
		log.Warn("Failed to get VPCNetworkConfiguration", "name", ncName, "error", err)
		return nil
	}

	result := p.extractVPCInfoFromNC(nc)
	log.Debug("Resolved VPC info", "namespace", ns, "vpcCount", len(result))
	return result
}

// findDefaultNetworkConfig finds the VPCNetworkConfiguration annotated as default.
func (p *K8sVPCInfoProvider) findDefaultNetworkConfig(ctx context.Context) (*vpcv1alpha1.VPCNetworkConfiguration, error) {
	ncList := &vpcv1alpha1.VPCNetworkConfigurationList{}
	if err := p.client.List(ctx, ncList); err != nil {
		return nil, fmt.Errorf("failed to list VPCNetworkConfigurations: %w", err)
	}
	for i := range ncList.Items {
		nc := &ncList.Items[i]
		if nc.Annotations[annotationDefaultConfig] == "true" {
			return nc, nil
		}
	}
	return nil, nil
}

// ListAllVPCNamespaces returns all namespace names that have an associated VPC.
func (p *K8sVPCInfoProvider) ListAllVPCNamespaces() []string {
	log := logger.Log
	ctx := context.Background()
	nsList := &corev1.NamespaceList{}
	if err := p.client.List(ctx, nsList); err != nil {
		log.Warn("Failed to list namespaces", "error", err)
		return nil
	}
	var result []string
	for _, ns := range nsList.Items {
		if infos := p.ListVPCInfo(ns.Name); len(infos) > 0 {
			result = append(result, ns.Name)
		}
	}
	log.Debug("Listed all VPC namespaces", "totalNamespaces", len(nsList.Items), "vpcNamespaces", len(result))
	return result
}

// extractVPCInfoFromNC extracts VPCResourceInfo from a VPCNetworkConfiguration.
func (p *K8sVPCInfoProvider) extractVPCInfoFromNC(nc *vpcv1alpha1.VPCNetworkConfiguration) []common.VPCResourceInfo {
	var result []common.VPCResourceInfo

	// Pre-created VPC: parse from spec.vpc directly.
	if nc.Spec.VPC != "" {
		info, err := common.ParseVPCResourcePath(nc.Spec.VPC)
		if err == nil {
			result = append(result, info)
		}
		return result
	}

	// Auto-created VPCs: parse from status.vpcs[].vpcPath.
	for _, vpc := range nc.Status.VPCs {
		info, err := common.ParseVPCResourcePath(vpc.VPCPath)
		if err == nil {
			result = append(result, info)
		}
	}
	return result
}
