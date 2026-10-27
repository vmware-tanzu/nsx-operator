/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package migration

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

func TestNCPPatchPreservesExistingNetworkingContainers(t *testing.T) {
	original := []byte(`{
	  "apiVersion":"apps/v1","kind":"Deployment",
	  "metadata":{"name":"nsx-ncp","namespace":"vmware-system-nsx"},
	  "spec":{"template":{"metadata":{"labels":{"component":"nsx-ncp","nsx-operator-role":"master"}},
	    "spec":{"hostNetwork":true,"serviceAccountName":"nsx-ncp","containers":[
	      {"name":"nsx-operator","image":"existing-networking-image","args":["--nsxconfig=/etc/nsx-operator/nsxop.ini"]},
	      {"name":"nsx-eas","image":"existing-eas-image"}
	    ]}}}}`)
	patch, err := os.ReadFile("../../build/yaml/migration/ncp-container.patch.yaml")
	require.NoError(t, err)
	patchJSON, err := yaml.YAMLToJSON(patch)
	require.NoError(t, err)
	merged, err := strategicpatch.StrategicMergePatch(original, patchJSON, appsv1.Deployment{})
	require.NoError(t, err)
	var deployment appsv1.Deployment
	require.NoError(t, json.Unmarshal(merged, &deployment))
	pod := deployment.Spec.Template
	require.True(t, pod.Spec.HostNetwork)
	require.Equal(t, "nsx-ncp", pod.Spec.ServiceAccountName)
	require.Equal(t, "master", pod.Labels["nsx-operator-role"])
	require.Len(t, pod.Spec.Containers, 3)
	for _, container := range pod.Spec.Containers {
		switch container.Name {
		case "nsx-operator":
			require.Equal(t, "existing-networking-image", container.Image)
			require.Equal(t, []string{"--nsxconfig=/etc/nsx-operator/nsxop.ini"}, container.Args)
		case "nsx-eas":
			require.Equal(t, "existing-eas-image", container.Image)
		case "supervisor-network-migration-operator":
			require.Equal(t, "supervisor-network-migration-operator:latest", container.Image)
			require.Equal(t, []string{"/usr/local/bin/supervisor-network-migration-operator"}, container.Command)
			require.NotNil(t, container.ReadinessProbe)
			require.NotNil(t, container.LivenessProbe)
			require.NotContains(t, container.Args, "--enable-network-migration")
			require.Equal(t, int32(9924), container.Ports[0].ContainerPort)
		default:
			t.Fatalf("unexpected container %q", container.Name)
		}
	}
}

func readManifestDocuments(t *testing.T, path string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var documents [][]byte
	for {
		var doc json.RawMessage
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if len(doc) != 0 {
			documents = append(documents, doc)
		}
	}
	return documents
}

func TestLeaseRBACMatchesSharedNCPServiceAccount(t *testing.T) {
	var leaseRole rbacv1.Role
	for _, doc := range readManifestDocuments(t, "../../build/yaml/migration/rbac/role.yaml") {
		var resource struct{ Kind string }
		require.NoError(t, json.Unmarshal(doc, &resource))
		switch resource.Kind {
		case "ClusterRole":
			var role rbacv1.ClusterRole
			require.NoError(t, json.Unmarshal(doc, &role))
			for _, rule := range role.Rules {
				require.NotContains(t, rule.Resources, "leases")
			}
		case "Role":
			require.NoError(t, json.Unmarshal(doc, &leaseRole))
		}
	}
	require.Equal(t, "vmware-system-nsx", leaseRole.Namespace)
	require.Len(t, leaseRole.Rules, 1)
	require.Contains(t, leaseRole.Rules[0].Resources, "leases")
	for _, verb := range []string{"get", "create", "update"} {
		require.Contains(t, leaseRole.Rules[0].Verbs, verb)
	}
	bindings := 0
	for _, doc := range readManifestDocuments(t, "../../build/yaml/migration/rbac/bindings.yaml") {
		var resource struct{ Kind string }
		require.NoError(t, json.Unmarshal(doc, &resource))
		if resource.Kind == "RoleBinding" {
			var binding rbacv1.RoleBinding
			require.NoError(t, json.Unmarshal(doc, &binding))
			require.Equal(t, leaseRole.Namespace, binding.Namespace)
			require.Equal(t, leaseRole.Name, binding.RoleRef.Name)
			require.Equal(t, "Role", binding.RoleRef.Kind)
			require.Contains(t, binding.Subjects, rbacv1.Subject{Kind: "ServiceAccount", Name: "nsx-ncp", Namespace: "vmware-system-nsx"})
			bindings++
		}
	}
	require.Equal(t, 1, bindings)
}

func TestNetworkInspectionRBACIsReadOnly(t *testing.T) {
	var role rbacv1.ClusterRole
	for _, doc := range readManifestDocuments(t, "../../build/yaml/migration/rbac/role.yaml") {
		var resource struct{ Kind string }
		require.NoError(t, json.Unmarshal(doc, &resource))
		if resource.Kind == "ClusterRole" {
			require.NoError(t, json.Unmarshal(doc, &role))
		}
	}
	sourceReads, statusPatches := false, false
	for _, rule := range role.Rules {
		for _, group := range rule.APIGroups {
			if group == "netoperator.vmware.com" {
				sourceReads = true
				require.ElementsMatch(t, []string{"get", "list"}, rule.Verbs)
				require.ElementsMatch(t, []string{"networks", "vspheredistributednetworks", "ippools"}, rule.Resources)
			}
		}
		for _, resource := range rule.Resources {
			if resource == "controlplanenetworktransitions/status" {
				statusPatches = true
				require.Equal(t, []string{"patch"}, rule.Verbs)
				require.Contains(t, rule.Resources, "namespacenetworktransitions/status")
			}
		}
	}
	require.True(t, sourceReads)
	require.True(t, statusPatches)
}
