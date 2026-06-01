/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
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

// VPCEntry pairs the VPC display name with the parsed NSX resource info.
type VPCEntry struct {
	// DisplayName is the NSX VPC display name, set from VPCNetworkConfiguration.Status.VPCs[].Name.
	// This is the k8s VPC CR name stored in NSX as the display name.
	// When empty, callers should fall back to matching by Info.VPCID.
	DisplayName string
	// Info contains the parsed NSX path components (OrgID, ProjectID, VPCID).
	Info common.VPCResourceInfo
}

// VPCInfoProvider is a minimal interface for resolving namespace to VPC info.
// This is a subset of common.VPCServiceProvider, used by EAS storage layer.
type VPCInfoProvider interface {
	ListVPCInfo(ns string) []VPCEntry
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

// ListVPCInfo resolves a namespace to its associated VPCs by following a two-step
// annotation-driven lookup:
//
//  1. The Namespace may carry the annotation
//     nsx.vmware.com/vpc_network_config=<VPCNetworkConfiguration name>
//     which explicitly names the VPCNetworkConfiguration CR that governs it.
//     When present, that CR is fetched directly.
//
//  2. If the annotation is absent the namespace has no explicit binding, so the
//     operator falls back to the cluster-wide default: it scans all
//     VPCNetworkConfiguration CRs for the one that carries the annotation
//     nsx.vmware.com/default=true (see findDefaultNetworkConfig).
//     Exactly one CR should carry this marker; it represents the network config
//     applied to any namespace that has not been explicitly assigned one.
//
// In both cases the resolved VPCNetworkConfiguration is passed to
// extractVPCInfoFromNC, which turns the CR's VPC path(s) into VPCEntry values.
func (p *K8sVPCInfoProvider) ListVPCInfo(ns string) []VPCEntry {
	log := logger.Log
	ctx := context.Background()

	// Step 1: read nsx.vmware.com/vpc_network_config from the Namespace.
	// Its value is the name of the VPCNetworkConfiguration CR to use.
	namespace := &corev1.Namespace{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: ns}, namespace); err != nil {
		log.Debug("Failed to get namespace", "namespace", ns, "error", err)
		return nil
	}

	ncName := namespace.Annotations[annotationVPCNetworkConfig]
	if ncName == "" {
		// Annotation absent: fall back to the VPCNetworkConfiguration that has
		// nsx.vmware.com/default=true on the CR itself.
		log.Debug("No VPC network config annotation, using default", "namespace", ns)
		nc, err := p.findDefaultNetworkConfig(ctx)
		if err != nil || nc == nil {
			log.Debug("No default VPCNetworkConfiguration found", "namespace", ns, "error", err)
			return nil
		}
		return p.extractVPCInfoFromNC(nc)
	}

	// Step 2: annotation present — fetch the named VPCNetworkConfiguration CR.
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

// findDefaultNetworkConfig scans all VPCNetworkConfiguration CRs and returns the
// one annotated with nsx.vmware.com/default=true.  This CR acts as the fallback
// network config for any namespace that does not carry the vpc_network_config annotation.
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

// extractVPCInfoFromNC extracts VPCEntry values from a VPCNetworkConfiguration.
// Each entry includes the NSX VPC display name alongside the parsed path components.
func (p *K8sVPCInfoProvider) extractVPCInfoFromNC(nc *vpcv1alpha1.VPCNetworkConfiguration) []VPCEntry {
	var result []VPCEntry

	// Pre-created VPC: parse from spec.vpc directly; use the VPCID as display name.
	if nc.Spec.VPC != "" {
		info, err := common.ParseVPCResourcePath(nc.Spec.VPC)
		if err == nil {
			result = append(result, VPCEntry{DisplayName: info.VPCID, Info: info})
		}
		return result
	}

	// Auto-created VPCs: parse from status.vpcs[].vpcPath.
	// VPCInfo.Name is the NSX VPC display name (set from k8s VPC CR name).
	for _, vpc := range nc.Status.VPCs {
		info, err := common.ParseVPCResourcePath(vpc.VPCPath)
		if err == nil {
			result = append(result, VPCEntry{DisplayName: vpc.Name, Info: info})
		}
	}
	return result
}
