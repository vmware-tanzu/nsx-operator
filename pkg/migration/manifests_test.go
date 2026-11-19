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
	    "spec":{"hostNetwork":true,"serviceAccountName":"ncp-svc-account","containers":[
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
	require.Equal(t, "ncp-svc-account", pod.Spec.ServiceAccountName)
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
		var resource struct {
			Kind     string
			Metadata struct {
				Name      string
				Namespace string
			} `json:"metadata"`
		}
		require.NoError(t, json.Unmarshal(doc, &resource))
		switch resource.Kind {
		case "ClusterRole":
			var role rbacv1.ClusterRole
			require.NoError(t, json.Unmarshal(doc, &role))
			for _, rule := range role.Rules {
				require.NotContains(t, rule.Resources, "leases")
				require.NotContains(t, rule.Resources, "configmaps")
				require.NotContains(t, rule.Resources, "controlplanenetworktransitions/finalizers")
				require.NotContains(t, rule.Resources, "namespacenetworktransitions/finalizers")
			}
		case "Role":
			if resource.Metadata.Namespace == "vmware-system-nsx" {
				require.NoError(t, json.Unmarshal(doc, &leaseRole))
			}
		}
	}
	require.Equal(t, "vmware-system-nsx", leaseRole.Namespace)
	require.Len(t, leaseRole.Rules, 1)
	require.Equal(t, []string{"leases"}, leaseRole.Rules[0].Resources)
	require.ElementsMatch(t, []string{"get", "create", "update"}, leaseRole.Rules[0].Verbs)
	bindings := 0
	for _, doc := range readManifestDocuments(t, "../../build/yaml/migration/rbac/bindings.yaml") {
		var resource struct {
			Kind     string
			Metadata struct{ Name string } `json:"metadata"`
		}
		require.NoError(t, json.Unmarshal(doc, &resource))
		if resource.Kind == "RoleBinding" && resource.Metadata.Name == "supervisor-network-migration-operator-leader-election" {
			var binding rbacv1.RoleBinding
			require.NoError(t, json.Unmarshal(doc, &binding))
			require.Equal(t, leaseRole.Namespace, binding.Namespace)
			require.Equal(t, leaseRole.Name, binding.RoleRef.Name)
			require.Equal(t, "Role", binding.RoleRef.Kind)
			require.Contains(t, binding.Subjects, rbacv1.Subject{Kind: "ServiceAccount", Name: "ncp-svc-account", Namespace: "vmware-system-nsx"})
			bindings++
		}
	}
	require.Equal(t, 1, bindings)
}

func TestBatchPreflightRBAC(t *testing.T) {
	var clusterRole rbacv1.ClusterRole
	var configReaderRole rbacv1.Role
	for _, doc := range readManifestDocuments(t, "../../build/yaml/migration/rbac/role.yaml") {
		var resource struct {
			Kind     string
			Metadata struct {
				Name      string
				Namespace string
			} `json:"metadata"`
		}
		require.NoError(t, json.Unmarshal(doc, &resource))
		if resource.Kind == "ClusterRole" {
			require.NoError(t, json.Unmarshal(doc, &clusterRole))
		} else if resource.Kind == "Role" && resource.Metadata.Namespace == "vmware-system-vks-public" {
			require.NoError(t, json.Unmarshal(doc, &configReaderRole))
		}
	}
	for _, expected := range []struct {
		apiGroup string
		resource string
		verbs    []string
	}{
		{apiGroup: "", resource: "namespaces", verbs: []string{"get", "list"}},
		{apiGroup: "iaas.vmware.com", resource: "supervisornamespaces", verbs: []string{"get"}},
		{apiGroup: "netoperator.vmware.com", resource: "networksettings", verbs: []string{"get"}},
		{apiGroup: "migration.network.vmware.com", resource: "namespacenetworktransitions/status", verbs: []string{"get", "patch", "update"}},
	} {
		found := false
		for _, rule := range clusterRole.Rules {
			if !containsString(rule.APIGroups, expected.apiGroup) || !containsString(rule.Resources, expected.resource) {
				continue
			}
			for _, verb := range expected.verbs {
				require.Contains(t, rule.Verbs, verb)
			}
			found = true
			break
		}
		require.True(t, found, "missing RBAC rule for %s/%s", expected.apiGroup, expected.resource)
	}
	require.Equal(t, "supervisor-network-migration-operator", configReaderRole.Name)
	require.Equal(t, "vmware-system-vks-public", configReaderRole.Namespace)
	require.Len(t, configReaderRole.Rules, 1)
	require.Equal(t, []string{""}, configReaderRole.Rules[0].APIGroups)
	require.Equal(t, []string{"configmaps"}, configReaderRole.Rules[0].Resources)
	require.Equal(t, []string{"supervisor-properties"}, configReaderRole.Rules[0].ResourceNames)
	require.Equal(t, []string{"get"}, configReaderRole.Rules[0].Verbs)

	var configReaderBinding rbacv1.RoleBinding
	for _, doc := range readManifestDocuments(t, "../../build/yaml/migration/rbac/bindings.yaml") {
		var resource struct {
			Kind     string
			Metadata struct{ Name string } `json:"metadata"`
		}
		require.NoError(t, json.Unmarshal(doc, &resource))
		if resource.Kind == "RoleBinding" && resource.Metadata.Name == "supervisor-network-migration-operator-config-reader" {
			require.NoError(t, json.Unmarshal(doc, &configReaderBinding))
		}
	}
	require.Equal(t, "vmware-system-vks-public", configReaderBinding.Namespace)
	require.Equal(t, "Role", configReaderBinding.RoleRef.Kind)
	require.Equal(t, "supervisor-network-migration-operator", configReaderBinding.RoleRef.Name)
	require.Contains(t, configReaderBinding.Subjects, rbacv1.Subject{Kind: "ServiceAccount", Name: "ncp-svc-account", Namespace: "vmware-system-nsx"})
}

func containsString(values []string, value string) bool {
	for _, entry := range values {
		if entry == value {
			return true
		}
	}
	return false
}
