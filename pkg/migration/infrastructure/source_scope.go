/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	"github.com/vmware/govmomi/vapi/rest"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
)

const (
	managedByLabel     = "managedBy"
	vsphereAppPlatform = "vSphere-AppPlatform"
	vsphereNetworkType = "VSPHERE_NETWORK"
)

// SupervisorNetworkAPI returns the DVPG backings used by CPVMs and
// Supervisor Service Namespaces.
type SupervisorNetworkAPI interface {
	PortGroupIDs(context.Context) ([]string, error)
}

// VCenterSupervisorNetworks discovers VDS backings from vCenter and Kubernetes.
type VCenterSupervisorNetworks struct {
	Client       *rest.Client
	SupervisorID string
	Reader       client.Reader
}

type vcenterNetwork struct {
	Network         string `json:"network"`
	NetworkProvider string `json:"network_provider"`
	VSphereNetwork  *struct {
		PortGroup string `json:"portgroup"`
	} `json:"vsphere_network"`
}

type networkReference struct {
	ID      string `json:"network"`
	VSphere *struct {
		PortGroup string `json:"portgroup"`
	} `json:"vsphere_network"`
}

// PortGroupIDs uses the Supervisor topology to find enabled clusters, reads
// each cluster's primary CPVM network, then resolves networks used by
// AppPlatform-managed Supervisor Namespaces.
func (reader VCenterSupervisorNetworks) PortGroupIDs(ctx context.Context) ([]string, error) {
	if reader.Client == nil || reader.SupervisorID == "" || reader.Reader == nil {
		return nil, fmt.Errorf("vCenter REST client, Supervisor ID and Kubernetes reader are required")
	}
	clusterIDs, err := reader.supervisorClusterIDs(ctx)
	if err != nil {
		return nil, err
	}

	portGroups := make(map[string]struct{})
	networksByID := make(map[string]string)
	for _, clusterID := range clusterIDs {
		clusterPath := "/api/vcenter/namespace-management/clusters/" + url.PathEscape(clusterID)
		var cluster struct {
			WorkloadNetworks struct {
				SupervisorPrimaryWorkloadNetwork json.RawMessage `json:"supervisor_primary_workload_network"`
			} `json:"workload_networks"`
			SupervisorPrimaryWorkloadNetwork json.RawMessage `json:"supervisor_primary_workload_network"`
		}
		if err := reader.get(ctx, clusterPath, &cluster); err != nil {
			return nil, fmt.Errorf("get Supervisor cluster %q: %w", clusterID, err)
		}

		var networkList []vcenterNetwork
		if err := reader.get(ctx, clusterPath+"/networks", &networkList); err != nil {
			return nil, fmt.Errorf("list networks for Supervisor cluster %q: %w", clusterID, err)
		}
		for _, network := range networkList {
			if network.NetworkProvider != vsphereNetworkType || network.VSphereNetwork == nil {
				continue
			}
			if network.Network != "" && network.VSphereNetwork.PortGroup != "" {
				networksByID[network.Network] = network.VSphereNetwork.PortGroup
			}
		}

		primaryNetwork := cluster.WorkloadNetworks.SupervisorPrimaryWorkloadNetwork
		if len(primaryNetwork) == 0 {
			primaryNetwork = cluster.SupervisorPrimaryWorkloadNetwork
		}
		backing, networkID, err := parseNetworkReference(primaryNetwork)
		if err != nil {
			return nil, fmt.Errorf("read CPVM network for cluster %q: %w", clusterID, err)
		}
		if backing == "" {
			backing = networksByID[networkID]
		}
		if backing == "" {
			return nil, fmt.Errorf("CPVM network for cluster %q has no VDS port-group backing", clusterID)
		}
		portGroups[backing] = struct{}{}
	}

	var namespaces []struct {
		Name string `json:"namespace"`
	}
	if err := reader.get(ctx, "/api/vcenter/namespaces/instances/v2", &namespaces); err != nil {
		return nil, fmt.Errorf("list Supervisor Namespaces: %w", err)
	}
	var kubernetesNamespaces corev1.NamespaceList
	if err := reader.Reader.List(ctx, &kubernetesNamespaces); err != nil {
		return nil, fmt.Errorf("list Kubernetes Namespace labels: %w", err)
	}
	labelsByNamespace := make(map[string]map[string]string, len(kubernetesNamespaces.Items))
	for _, namespace := range kubernetesNamespaces.Items {
		labelsByNamespace[namespace.Name] = namespace.Labels
	}
	for _, namespace := range namespaces {
		if namespace.Name == "" {
			return nil, fmt.Errorf("Supervisor Namespace list contains an empty name")
		}
		if labelsByNamespace[namespace.Name][managedByLabel] != vsphereAppPlatform {
			continue
		}
		networkID, provider, err := reader.namespaceDefaultNetwork(ctx, namespace.Name)
		if err != nil {
			return nil, err
		}
		if provider != "" && provider != vsphereNetworkType {
			continue
		}
		if networkID == "" {
			// A Namespace without an override inherits the Supervisor primary
			// workload network, which was already added above.
			continue
		}
		backing := networksByID[networkID]
		if backing == "" {
			return nil, fmt.Errorf("Namespace %q default workload network %q has no VDS port-group backing", namespace.Name, networkID)
		}
		portGroups[backing] = struct{}{}
	}

	result := make([]string, 0, len(portGroups))
	for portGroupID := range portGroups {
		result = append(result, portGroupID)
	}
	slices.Sort(result)
	if len(result) == 0 {
		return nil, fmt.Errorf("Supervisor APIs returned no VDS port-group backings")
	}
	return result, nil
}

func (reader VCenterSupervisorNetworks) supervisorClusterIDs(ctx context.Context) ([]string, error) {
	path := "/api/vcenter/namespace-management/supervisors/" + url.PathEscape(reader.SupervisorID) + "/topology"
	var zones []struct {
		Clusters []string `json:"clusters"`
	}
	if err := reader.get(ctx, path, &zones); err != nil {
		return nil, fmt.Errorf("read Supervisor topology: %w", err)
	}
	clusterSet := make(map[string]struct{})
	for _, zone := range zones {
		for _, clusterID := range zone.Clusters {
			if clusterID != "" {
				clusterSet[clusterID] = struct{}{}
			}
		}
	}
	clusterIDs := make([]string, 0, len(clusterSet))
	for clusterID := range clusterSet {
		clusterIDs = append(clusterIDs, clusterID)
	}
	slices.Sort(clusterIDs)
	if len(clusterIDs) == 0 {
		return nil, fmt.Errorf("Supervisor topology contains no clusters")
	}
	return clusterIDs, nil
}

func (reader VCenterSupervisorNetworks) namespaceDefaultNetwork(ctx context.Context, namespace string) (string, string, error) {
	path := "/api/vcenter/namespaces/instances/v2/" + url.PathEscape(namespace)
	var result struct {
		NetworkSpec *struct {
			NetworkProvider string `json:"network_provider"`
			VSphereNetwork  *struct {
				DefaultWorkloadNetwork string `json:"default_workload_network"`
			} `json:"vsphere_network"`
		} `json:"network_spec"`
	}
	if err := reader.get(ctx, path, &result); err != nil {
		return "", "", fmt.Errorf("get network specification for Namespace %q: %w", namespace, err)
	}
	if result.NetworkSpec == nil || result.NetworkSpec.VSphereNetwork == nil {
		return "", "", nil
	}
	return result.NetworkSpec.VSphereNetwork.DefaultWorkloadNetwork, result.NetworkSpec.NetworkProvider, nil
}

func parseNetworkReference(raw json.RawMessage) (portGroup, networkID string, err error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", fmt.Errorf("supervisor_primary_workload_network is missing")
	}
	if err := json.Unmarshal(raw, &networkID); err == nil {
		if networkID == "" {
			return "", "", fmt.Errorf("supervisor_primary_workload_network is empty")
		}
		return "", networkID, nil
	}
	var reference networkReference
	if err := json.Unmarshal(raw, &reference); err != nil {
		return "", "", fmt.Errorf("decode supervisor_primary_workload_network: %w", err)
	}
	if reference.VSphere != nil && reference.VSphere.PortGroup != "" {
		return reference.VSphere.PortGroup, reference.ID, nil
	}
	if reference.ID == "" {
		return "", "", fmt.Errorf("supervisor_primary_workload_network has no network ID or VDS port-group")
	}
	return "", reference.ID, nil
}

func (reader VCenterSupervisorNetworks) get(ctx context.Context, path string, response any) error {
	request := reader.Client.Resource(path).Request(http.MethodGet)
	return reader.Client.Do(ctx, request, response)
}

// VCenterControlPlaneScope maps WCP-reported DVPG backings to Net Operator
// VDNs and resolves their source topology for SNAT network comparisons.
type VCenterControlPlaneScope struct {
	Networks  SupervisorNetworkAPI
	Reader    client.Reader
	Inventory inspection.Inventory
}

func (scope VCenterControlPlaneScope) SourceNetworks(ctx context.Context) ([]string, []inspection.Topology, error) {
	if scope.Networks == nil || scope.Reader == nil || scope.Inventory == nil {
		return nil, nil, fmt.Errorf("Supervisor network API, Net Operator reader and source inventory are required")
	}
	portGroupIDs, err := scope.Networks.PortGroupIDs(ctx)
	if err != nil {
		return nil, nil, err
	}
	vdnNames, err := sourceVDNsForPortGroups(ctx, scope.Reader, portGroupIDs)
	if err != nil {
		return nil, nil, err
	}

	var workloadTopologies []inspection.Topology
	for _, vdnName := range vdnNames {
		discovered, err := scope.Inventory.Resolve(ctx, vdnName)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve Supervisor VDN %q: %w", vdnName, err)
		}
		workloadTopologies = append(workloadTopologies, discovered.Topologies...)
	}
	return vdnNames, workloadTopologies, nil
}

func sourceVDNsForPortGroups(ctx context.Context, reader client.Reader, portGroupIDs []string) ([]string, error) {
	vdnNames, err := listVDNsByPortGroup(ctx, reader)
	if err != nil {
		return nil, err
	}
	vdnSet := make(map[string]struct{})
	for _, portGroupID := range portGroupIDs {
		matched := vdnNames[portGroupID]
		if len(matched) == 0 {
			return nil, fmt.Errorf("Supervisor DVPG %q has no Net Operator VDN", portGroupID)
		}
		for _, vdnName := range matched {
			vdnSet[vdnName] = struct{}{}
		}
	}
	result := make([]string, 0, len(vdnSet))
	for name := range vdnSet {
		result = append(result, name)
	}
	slices.Sort(result)
	return result, nil
}

func listVDNsByPortGroup(ctx context.Context, reader client.Reader) (map[string][]string, error) {
	var vdns netop.VSphereDistributedNetworkList
	if err := reader.List(ctx, &vdns); err != nil {
		return nil, fmt.Errorf("list Net Operator VDNs: %w", err)
	}
	backings := make(map[string][]string)
	for _, vdn := range vdns.Items {
		if vdn.Name != "" && vdn.Spec.PortGroupID != "" && vdn.DeletionTimestamp.IsZero() {
			backings[vdn.Spec.PortGroupID] = append(backings[vdn.Spec.PortGroupID], vdn.Name)
		}
	}
	return backings, nil
}
