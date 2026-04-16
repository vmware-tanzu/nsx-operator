/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
)

func TestRegisterExtensionAPIService_NilRestConfig(t *testing.T) {
	s := &EASServer{}
	err := s.registerExtensionAPIService(context.Background())
	require.NoError(t, err)
}

func TestMergeAPIServiceSpec(t *testing.T) {
	into := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"uid": "abc"},
		"spec":     map[string]interface{}{"version": "old"},
	}}
	from := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec": map[string]interface{}{
			"group":                 easv1alpha1.GroupVersion.Group,
			"version":               easv1alpha1.GroupVersion.Version,
			"groupPriorityMinimum":  int64(1000),
			"versionPriority":       int64(15),
			"insecureSkipTLSVerify": true,
		},
	}}
	mergeAPIServiceSpec(into, from)
	spec, found, err := unstructured.NestedMap(into.Object, "spec")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, easv1alpha1.GroupVersion.Group, spec["group"])
	meta, ok := into.Object["metadata"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "abc", meta["uid"])
}

func TestDesiredAPIServiceUnstructured_Defaults(t *testing.T) {
	t.Setenv(envEASAPIServiceServiceName, "")
	t.Setenv(envEASAPIServiceServiceNamespace, "")
	t.Setenv(envEASAPIServiceInsecureTLS, "")
	t.Setenv(envEASAPIServiceGroupPriority, "")
	t.Setenv(envEASAPIServiceVersionPriority, "")

	u, err := desiredAPIServiceUnstructured()
	require.NoError(t, err)
	assert.Equal(t, "apiregistration.k8s.io/v1", u.GetAPIVersion())
	assert.Equal(t, "APIService", u.GetKind())
	assert.Equal(t, defaultAPIServiceName, u.GetName())

	svc, found, err := unstructured.NestedMap(u.Object, "spec", "service")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "nsx-eas", svc["name"])
	assert.Equal(t, "vmware-system-nsx", svc["namespace"])

	g, _, _ := unstructured.NestedString(u.Object, "spec", "group")
	assert.Equal(t, easv1alpha1.GroupVersion.Group, g)
	v, _, _ := unstructured.NestedString(u.Object, "spec", "version")
	assert.Equal(t, easv1alpha1.GroupVersion.Version, v)
}

func TestDesiredAPIServiceUnstructured_EnvOverrides(t *testing.T) {
	t.Setenv(envEASAPIServiceServiceName, "custom-svc")
	t.Setenv(envEASAPIServiceServiceNamespace, "custom-ns")
	t.Setenv(envEASAPIServiceInsecureTLS, "false")
	t.Setenv(envEASAPIServiceGroupPriority, "2000")
	t.Setenv(envEASAPIServiceVersionPriority, "99")

	u, err := desiredAPIServiceUnstructured()
	require.NoError(t, err)
	svc, _, _ := unstructured.NestedMap(u.Object, "spec", "service")
	assert.Equal(t, "custom-svc", svc["name"])
	assert.Equal(t, "custom-ns", svc["namespace"])
	insecure, _, _ := unstructured.NestedBool(u.Object, "spec", "insecureSkipTLSVerify")
	assert.False(t, insecure)
	gp, _, _ := unstructured.NestedInt64(u.Object, "spec", "groupPriorityMinimum")
	assert.Equal(t, int64(2000), gp)
	vp, _, _ := unstructured.NestedInt64(u.Object, "spec", "versionPriority")
	assert.Equal(t, int64(99), vp)
}
