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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
