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
