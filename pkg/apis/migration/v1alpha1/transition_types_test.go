/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package v1alpha1

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestReviewedExamplesRetainDocumentedFields(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resource any
	}{
		{"controlplane", &ControlPlaneNetworkTransition{}},
		{"controlplane-nsx-lb", &ControlPlaneNetworkTransition{}},
		{"controlplane-nsx-lb-reserved", &ControlPlaneNetworkTransition{}},
		{"namespaces", &NamespaceNetworkTransition{}},
		{"design-controlplane", &ControlPlaneNetworkTransition{}},
		{"design-controlplane-naming", &ControlPlaneNetworkTransition{}},
		{"design-controlplane-needs-dhcp", &ControlPlaneNetworkTransition{}},
		{"design-controlplane-two-vdns", &ControlPlaneNetworkTransition{}},
		{"design-controlplane-two-vdns-inputs", &ControlPlaneNetworkTransition{}},
		{"design-namespaces-naming", &NamespaceNetworkTransition{}},
		{"design-namespaces-success", &NamespaceNetworkTransition{}},
		{"design-namespaces-failure", &NamespaceNetworkTransition{}},
		{"design-namespaces-incompatible", &NamespaceNetworkTransition{}},
		{"design-namespaces-plan", &NamespaceNetworkTransition{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + tc.name + ".yaml")
			require.NoError(t, err)
			source, err := yaml.YAMLToJSON(data)
			require.NoError(t, err)
			decoder := json.NewDecoder(bytes.NewReader(source))
			decoder.DisallowUnknownFields()
			require.NoError(t, decoder.Decode(tc.resource))
			encoded, err := json.Marshal(tc.resource)
			require.NoError(t, err)
			var expected, actual map[string]any
			require.NoError(t, json.Unmarshal(source, &expected))
			require.NoError(t, json.Unmarshal(encoded, &actual))
			requireFieldsPreserved(t, expected, actual)
		})
	}
}

func requireFieldsPreserved(t *testing.T, expected, actual map[string]any) {
	t.Helper()
	for key, want := range expected {
		got, exists := actual[key]
		require.True(t, exists, "documented field %s was lost", key)
		if nested, ok := want.(map[string]any); ok {
			actualNested, ok := got.(map[string]any)
			require.True(t, ok, "field %s must remain an object", key)
			requireFieldsPreserved(t, nested, actualNested)
			continue
		}
		// Kubernetes conditions include mandatory fields omitted by the illustrative
		// example; compare only the documented keys in those list elements.
		if list, ok := want.([]any); ok {
			gotList, ok := got.([]any)
			require.True(t, ok)
			require.Len(t, gotList, len(list))
			for i, item := range list {
				if nested, ok := item.(map[string]any); ok {
					actualNested, ok := gotList[i].(map[string]any)
					require.True(t, ok)
					requireFieldsPreserved(t, nested, actualNested)
				} else {
					require.Equal(t, item, gotList[i])
				}
			}
			continue
		}
		require.Equal(t, want, got, "field %s changed", key)
	}
}

func TestRemovedInputFieldsAreRejectedByStrictDecoding(t *testing.T) {
	for _, data := range []string{
		`{"spec":{"supervisorId":"domain-c8"}}`,
		`{"spec":{"userInputs":{}}}`,
		`{"spec":{"networkConfiguration":{"cpVmServiceSubnets":[]}}}`,
		`{"spec":{"networkConfiguration":{"aviConfig":{}}}}`,
		`{"spec":{"networkConfiguration":{"controlPlaneAndServiceDHCPNetworks":[{"vdnName":"network","subnetCidr":"192.168.10.0/24"}]}}}`,
		`{"status":{"lbSnatReservations":[{"allocationRef":{"namespace":"netop","name":"snat","uid":"removed"}}]}}`,
		`{"spec":{"networkConfiguration":{"controlPlaneAndServiceDHCPNetworks":[{"vdn":"old-name"}]}}}`,
		`{"status":{"nsxWorkflows":[{"vdn":"old-name","workflowId":"workflow-1"}]}}`,
		`{"spec":{"networkConfiguration":{"tenantNetworks":[]}}}`,
		`{"spec":{"networkConfiguration":{"controlPlaneAndServiceDHCPNetworks":[{"dvpgId":"dvpg-1"}]}}}`,
		`{"status":{"nsxWorkflows":[{"dvsUUID":"dvs-1","workflowId":"workflow-1"}]}}`,
	} {
		decoder := json.NewDecoder(bytes.NewBufferString(data))
		decoder.DisallowUnknownFields()
		var transition ControlPlaneNetworkTransition
		require.Error(t, decoder.Decode(&transition))
	}
}

func TestNamespaceConfigurationUsesReviewedNames(t *testing.T) {
	for _, payload := range []string{
		`{"spec":{"userInputs":{"tenantSubnets":[]}}}`,
		`{"spec":{"networkConfiguration":{"tenantSubnets":[]}}}`,
		`{"spec":{"networkConfiguration":{"namespaceSubnets":[]}}}`,
		`{"spec":{"networkConfiguration":{"namespaceDHCPNetworks":[{"vdnName":"network","subnetCidr":"192.168.10.0/24"}]}}}`,
		`{"spec":{"networkConfiguration":{"namespaceDHCPNetworks":[{"vdn":"old-name"}]}}}`,
	} {
		var resource NamespaceNetworkTransition
		decoder := json.NewDecoder(bytes.NewBufferString(payload))
		decoder.DisallowUnknownFields()
		require.Error(t, decoder.Decode(&resource))
	}
}

func TestResourceNamingDeepCopy(t *testing.T) {
	cp := &ControlPlaneNetworkTransition{Spec: ControlPlaneNetworkTransitionSpec{NetworkConfiguration: &ControlPlaneNetworkConfiguration{ResourceNaming: &ControlPlaneResourceNaming{Networks: []VDNResourceNaming{{VDNName: "vdn-cp-services-1", VPCName: "primary"}}}}}}
	ns := &NamespaceNetworkTransition{Spec: NamespaceNetworkTransitionSpec{NetworkConfiguration: &NamespaceNetworkConfiguration{ResourceNaming: &NamespaceResourceNaming{Networks: []VDNResourceNaming{{VDNName: "vdn-tenant-1", VPCName: "tenant"}}}}}}
	cpCopy, nsCopy := cp.DeepCopy(), ns.DeepCopy()
	cpCopy.Spec.NetworkConfiguration.ResourceNaming.Networks[0].VPCName = "changed"
	nsCopy.Spec.NetworkConfiguration.ResourceNaming.Networks[0].VDNName = "changed"
	require.Equal(t, NSXResourceName("primary"), cp.Spec.NetworkConfiguration.ResourceNaming.Networks[0].VPCName)
	require.Equal(t, "vdn-tenant-1", ns.Spec.NetworkConfiguration.ResourceNaming.Networks[0].VDNName)
}

func TestVDNSubnetNamingDeepCopyAndRoundTrip(t *testing.T) {
	naming := VDNResourceNaming{VDNName: "vdn-source", DVCName: "source-dvc", SubnetName: "source-subnet"}
	cp := &ControlPlaneNetworkTransition{Spec: ControlPlaneNetworkTransitionSpec{NetworkConfiguration: &ControlPlaneNetworkConfiguration{ResourceNaming: &ControlPlaneResourceNaming{Networks: []VDNResourceNaming{naming}}}}}
	ns := &NamespaceNetworkTransition{Spec: NamespaceNetworkTransitionSpec{NetworkConfiguration: &NamespaceNetworkConfiguration{ResourceNaming: &NamespaceResourceNaming{Networks: []VDNResourceNaming{naming}}}}}
	cp.DeepCopy().Spec.NetworkConfiguration.ResourceNaming.Networks[0].DVCName = "changed"
	ns.DeepCopy().Spec.NetworkConfiguration.ResourceNaming.Networks[0].SubnetName = "changed"
	require.Equal(t, NSXResourceName("source-dvc"), naming.DVCName)
	require.Equal(t, NSXResourceName("source-subnet"), naming.SubnetName)
	raw, err := json.Marshal(naming)
	require.NoError(t, err)
	var restored VDNResourceNaming
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.Equal(t, naming, restored)
}

func TestPerVLANResourceNamingIsRejectedByStrictDecoding(t *testing.T) {
	for _, resource := range []any{&ControlPlaneNetworkTransition{}, &NamespaceNetworkTransition{}} {
		for _, removed := range []string{`"vlanId":100`, `"vlanSubnets":[{"vlanId":100,"dvcName":"dvc","subnetName":"subnet"}]`} {
			payload := `{"spec":{"networkConfiguration":{"resourceNaming":{"networks":[{"vdnName":"source",` + removed + `}]}}}}`
			decoder := json.NewDecoder(bytes.NewBufferString(payload))
			decoder.DisallowUnknownFields()
			require.Error(t, decoder.Decode(resource))
		}
	}
}

func TestOptionalInputsRoundTripAndDeepCopy(t *testing.T) {
	hints := []OptionalInput{{Key: "spec.networkConfiguration.resourceNaming.networks[vdnName=vdn-source].tgwName", Description: "Optional TGW name; omitted names are generated."}}
	cp := &ControlPlaneNetworkTransition{Status: &ControlPlaneNetworkTransitionStatus{TransitionStatus: TransitionStatus{OptionalInputs: hints}}}
	ns := &NamespaceNetworkTransition{Status: &NamespaceNetworkTransitionStatus{TransitionStatus: TransitionStatus{OptionalInputs: hints}}}
	cp.DeepCopy().Status.OptionalInputs[0].Description = "changed"
	ns.DeepCopy().Status.OptionalInputs[0].Key = "changed"
	require.Equal(t, "Optional TGW name; omitted names are generated.", hints[0].Description)
	require.Equal(t, "spec.networkConfiguration.resourceNaming.networks[vdnName=vdn-source].tgwName", hints[0].Key)
	raw, err := json.Marshal(cp)
	require.NoError(t, err)
	var restored ControlPlaneNetworkTransition
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.Equal(t, cp, &restored)
	require.Empty(t, restored.Status.RequiredInputs, "optional hints are not required inputs")
}
