/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package infrastructure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	netop "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/migration/inspection"
)

func TestVCenterSupervisorNetworksReturnsUniqueDVPGIDs(t *testing.T) {
	requestedNamespace := false
	requestedAppPlatformNamespace := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/vcenter/namespace-management/supervisors/supervisor-1/topology":
			_, _ = w.Write([]byte(`[{"zone":"zone-a","clusters":["domain-c52"]}]`))
		case "/api/vcenter/namespace-management/clusters/domain-c52":
			_, _ = w.Write([]byte(`{"workload_networks":{"supervisor_primary_workload_network":{"network":"network-cp","network_provider":"VSPHERE_NETWORK","vsphere_network":{"portgroup":"dvpg-cp"}}}}`))
		case "/api/vcenter/namespace-management/clusters/domain-c52/networks":
			_, _ = w.Write([]byte(`[{"network":"network-cp","network_provider":"VSPHERE_NETWORK","vsphere_network":{"portgroup":"dvpg-cp"}},{"network":"network-1","network_provider":"VSPHERE_NETWORK","vsphere_network":{"portgroup":"dvpg-workload"}},{"network":"network-2","network_provider":"VSPHERE_NETWORK","vsphere_network":{"portgroup":"dvpg-appplatform"}},{"network":"nsx-network","network_provider":"NSXT_CONTAINER_PLUGIN"}]`))
		case "/api/vcenter/namespaces/instances/v2":
			_, _ = w.Write([]byte(`[{"namespace":"ns1"},{"namespace":"appplatform-ns"}]`))
		case "/api/vcenter/namespaces/instances/v2/ns1":
			requestedNamespace = true
			_, _ = w.Write([]byte(`{"network_spec":{"network_provider":"VSPHERE_NETWORK","vsphere_network":{"default_workload_network":"network-1"}}}`))
		case "/api/vcenter/namespaces/instances/v2/appplatform-ns":
			requestedAppPlatformNamespace = true
			_, _ = w.Write([]byte(`{"network_spec":{"network_provider":"VSPHERE_NETWORK","vsphere_network":{"default_workload_network":"network-2"}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := rest.NewClient(&vim25.Client{Client: soap.NewClient(endpoint, false)})
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	kubeReader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "appplatform-ns", Labels: map[string]string{managedByLabel: vsphereAppPlatform}}},
	).Build()
	reader := VCenterSupervisorNetworks{Client: client, SupervisorID: "supervisor-1", Reader: kubeReader}

	portGroupIDs, err := reader.PortGroupIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"dvpg-appplatform", "dvpg-cp"}, portGroupIDs)
	require.False(t, requestedNamespace)
	require.True(t, requestedAppPlatformNamespace)
}

type scopeInventoryStub struct{}

func (scopeInventoryStub) NamespaceVDNs(context.Context, []string) ([]string, error) {
	return nil, nil
}

func (scopeInventoryStub) Resolve(_ context.Context, vdnName string) (inspection.DiscoveredSource, error) {
	return inspection.DiscoveredSource{Source: inspection.Source{VDNName: vdnName, Topologies: []inspection.Topology{{PortGroupID: "dvpg-" + vdnName}}}}, nil
}

type scopeNetworksStub struct{ portGroupIDs []string }

func (stub scopeNetworksStub) PortGroupIDs(context.Context) ([]string, error) {
	return stub.portGroupIDs, nil
}

func TestVCenterControlPlaneScopeMapsBackingsToVDNs(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, netop.AddToScheme(scheme))
	vdnA := &netop.VSphereDistributedNetwork{ObjectMeta: metav1.ObjectMeta{Name: "network-a"}, Spec: netop.VSphereDistributedNetworkSpec{PortGroupID: "dvpg-a"}}
	vdnB := &netop.VSphereDistributedNetwork{ObjectMeta: metav1.ObjectMeta{Name: "network-b"}, Spec: netop.VSphereDistributedNetworkSpec{PortGroupID: "dvpg-b"}}
	kubeReader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vdnA, vdnB).Build()
	scope := VCenterControlPlaneScope{
		Networks:  scopeNetworksStub{portGroupIDs: []string{"dvpg-b", "dvpg-a"}},
		Reader:    kubeReader,
		Inventory: scopeInventoryStub{},
	}

	vdnNames, topologies, err := scope.SourceNetworks(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"network-a", "network-b"}, vdnNames)
	require.Equal(t, []inspection.Topology{{PortGroupID: "dvpg-network-a"}, {PortGroupID: "dvpg-network-b"}}, topologies)
}
