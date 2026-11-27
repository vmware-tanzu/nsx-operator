/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package namespaces

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/vmware-tanzu/nsx-operator/pkg/apis/migration/v1alpha1"
)

func TestValidateNamespaceMembers(t *testing.T) {
	for _, tc := range []struct {
		name              string
		missingNamespace  bool
		missingSupervisor bool
		terminating       string
		supervisorVersion string
		wantError         string
	}{
		{name: "live members", supervisorVersion: "v1alpha2"},
		{name: "legacy Supervisor API", supervisorVersion: "v1alpha1"},
		{name: "missing Namespace", missingNamespace: true, wantError: `reading Kubernetes Namespace "tenant"`},
		{name: "missing SupervisorNamespace", missingSupervisor: true, wantError: `reading SupervisorNamespace "tenant"`},
		{name: "Namespace already deleting", terminating: "Namespace", wantError: `Kubernetes Namespace "tenant" is terminating`},
		{name: "SupervisorNamespace already deleting", terminating: "SupervisorNamespace", wantError: `SupervisorNamespace "tenant" is terminating`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			for _, version := range []string{"v1alpha1", "v1alpha2"} {
				gv := schema.GroupVersion{Group: "iaas.vmware.com", Version: version}
				scheme.AddKnownTypeWithName(gv.WithKind("SupervisorNamespace"), &unstructured.Unstructured{})
				scheme.AddKnownTypeWithName(gv.WithKind("SupervisorNamespaceList"), &unstructured.UnstructuredList{})
			}
			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant"}}
			version := tc.supervisorVersion
			if version == "" {
				version = "v1alpha2"
			}
			supervisor := &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "iaas.vmware.com/" + version, "kind": "SupervisorNamespace",
				"metadata": map[string]interface{}{"name": "tenant"},
			}}
			now := metav1.Now()
			if tc.terminating == "Namespace" {
				namespace.DeletionTimestamp = &now
				namespace.Finalizers = []string{"test.example/retain"}
			}
			if tc.terminating == "SupervisorNamespace" {
				supervisor.SetDeletionTimestamp(&now)
				supervisor.SetFinalizers([]string{"test.example/retain"})
			}
			var objects []client.Object
			if !tc.missingNamespace {
				objects = append(objects, namespace)
			}
			if !tc.missingSupervisor {
				objects = append(objects, supervisor)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			err := Validate(context.Background(), reader, []string{"tenant"})
			if tc.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantError)
			}
		})
	}
}

func TestValidateRejectsMalformedNamespaceLists(t *testing.T) {
	for _, tc := range []struct {
		name    string
		names   []string
		wantErr string
	}{
		{name: "empty namespace name", names: []string{""}, wantErr: "empty namespace name"},
		{name: "duplicate namespace", names: []string{"tenant", "tenant"}, wantErr: `namespace "tenant" appears more than once`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := client.Reader(nil)
			if tc.name == "duplicate namespace" {
				reader = liveNamespaceReader()
			}
			err := Validate(context.Background(), reader, tc.names)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
	require.NoError(t, Validate(context.Background(), nil, nil), "empty input is a no-op; the CRD independently requires at least one namespace")
	_, err := Snapshot(context.Background(), nil, nil)
	require.ErrorContains(t, err, "at least one namespace")
	require.ErrorContains(t, ValidateSnapshot(context.Background(), nil, nil, nil), "at least one namespace")
}

func TestValidateSnapshotRejectsNamespaceObjectReplacement(t *testing.T) {
	ctx := context.Background()
	expected := []api.NamespaceIdentityStatus{{
		Namespace: "tenant", NamespaceUID: "namespace-original", SupervisorNamespaceUID: "supervisor-original",
	}}
	for _, tc := range []struct {
		name               string
		namespaceUID       string
		supervisorUID      string
		wantError          string
		expectedIdentities []api.NamespaceIdentityStatus
	}{
		{name: "Kubernetes Namespace replacement", namespaceUID: "namespace-replacement", supervisorUID: "supervisor-original", wantError: "object UID changed"},
		{name: "SupervisorNamespace replacement", namespaceUID: "namespace-original", supervisorUID: "supervisor-replacement", wantError: "object UID changed"},
		{name: "snapshot contains a different namespace", namespaceUID: "namespace-original", supervisorUID: "supervisor-original", expectedIdentities: []api.NamespaceIdentityStatus{{Namespace: "other", NamespaceUID: "ns", SupervisorNamespaceUID: "sn"}}, wantError: `namespace "tenant" is missing`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectedIdentities := tc.expectedIdentities
			if expectedIdentities == nil {
				expectedIdentities = expected
			}
			err := ValidateSnapshot(ctx, namespaceIdentityReader(tc.namespaceUID, tc.supervisorUID), []string{"tenant"}, expectedIdentities)
			require.ErrorContains(t, err, tc.wantError)
		})
	}
}

func liveNamespaceReader() client.Reader {
	return namespaceIdentityReader("namespace-uid", "supervisor-uid")
}

func namespaceIdentityReader(namespaceUID, supervisorUID string) client.Reader {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	gv := schema.GroupVersion{Group: "iaas.vmware.com", Version: "v1alpha2"}
	scheme.AddKnownTypeWithName(gv.WithKind("SupervisorNamespace"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(gv.WithKind("SupervisorNamespaceList"), &unstructured.UnstructuredList{})
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: types.UID(namespaceUID)}}
	supervisor := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "iaas.vmware.com/v1alpha2", "kind": "SupervisorNamespace",
		"metadata": map[string]interface{}{"name": "tenant", "uid": supervisorUID},
	}}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespace, supervisor).Build()
}
