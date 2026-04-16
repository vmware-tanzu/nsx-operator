/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

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

func TestPatchAPIServiceCABundle(t *testing.T) {
	// patchAPIServiceCABundle uses a MergePatch with raw JSON — the caBundle
	// must not appear in desiredAPIServiceUnstructured at all.
	t.Setenv(envEASAPIServiceInsecureTLS, "true")

	u, err := desiredAPIServiceUnstructured()
	require.NoError(t, err)

	// caBundle must NOT be set in the desired struct (it's patched separately).
	_, found, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "caBundle")
	assert.False(t, found, "caBundle must not be set in desiredAPIServiceUnstructured")
}

func TestEnvBool_ParseError(t *testing.T) {
	t.Setenv("_TEST_BOOL_PARSE", "not-a-bool")
	assert.True(t, envBool("_TEST_BOOL_PARSE", true))
	assert.False(t, envBool("_TEST_BOOL_PARSE", false))
}

func TestEnvInt32_ParseError(t *testing.T) {
	t.Setenv("_TEST_INT32_PARSE", "not-a-number")
	assert.Equal(t, int32(99), envInt32("_TEST_INT32_PARSE", 99))
}

func TestRegisterExtensionAPIService_SkipWhenEnvFalse(t *testing.T) {
	t.Setenv(envEASRegisterAPIService, "false")
	s := &EASServer{restConfig: nil} // restConfig nil but env disables
	// With nil restConfig, returns nil regardless
	err := s.registerExtensionAPIService(context.Background())
	assert.NoError(t, err)
}

func TestRegisterExtensionAPIService_SkipWhenEnvFalseNonNilConfig(t *testing.T) {
	// env=false takes priority even when restConfig is non-nil.
	t.Setenv(envEASRegisterAPIService, "false")
	s := &EASServer{
		restConfig: &rest.Config{Host: "https://localhost:9999"},
	}
	err := s.registerExtensionAPIService(context.Background())
	assert.NoError(t, err)
}

func TestMergeAPIServiceSpec_NoSpec(t *testing.T) {
	// 'from' has no spec → into should be left unchanged.
	into := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"uid": "abc"},
	}}
	from := &unstructured.Unstructured{Object: map[string]interface{}{}}
	mergeAPIServiceSpec(into, from) // should be a no-op
	_, found, err := unstructured.NestedMap(into.Object, "spec")
	require.NoError(t, err)
	assert.False(t, found, "into must not gain a spec when from has none")
}

// ── patchAPIServiceCABundle ───────────────────────────────────────────────────

// fakeDynamicRI returns a fake dynamic.ResourceInterface backed by a fake
// dynamic client, with the given reactor pre-pended for "patch" actions.
func fakeDynamicRIWithPatchReactor(t *testing.T, patchErr error) *fake.FakeDynamicClient {
	t.Helper()
	fakeClient := fake.NewSimpleDynamicClient(runtime.NewScheme())
	fakeClient.PrependReactor("patch", "apiservices",
		func(_ k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{}, patchErr
		})
	return fakeClient
}

func TestPatchAPIServiceCABundle_Success(t *testing.T) {
	s := &EASServer{caCert: []byte("pem-cert-bytes")}
	fc := fakeDynamicRIWithPatchReactor(t, nil)
	err := s.patchAPIServiceCABundle(context.Background(), fc.Resource(apiserviceGVR), defaultAPIServiceName)
	assert.NoError(t, err)
}

func TestPatchAPIServiceCABundle_WCPErrorTolereted(t *testing.T) {
	// WCP/Tanzu returns an error containing "may not have a caBundle"; the
	// function must swallow it and return nil.
	s := &EASServer{caCert: []byte("pem-cert-bytes")}
	fc := fakeDynamicRIWithPatchReactor(t, fmt.Errorf("may not have a caBundle set alongside a service"))
	err := s.patchAPIServiceCABundle(context.Background(), fc.Resource(apiserviceGVR), defaultAPIServiceName)
	assert.NoError(t, err)
}

func TestPatchAPIServiceCABundle_InsecureTLSErrorTolereted(t *testing.T) {
	s := &EASServer{caCert: []byte("pem-cert-bytes")}
	fc := fakeDynamicRIWithPatchReactor(t, fmt.Errorf("may not have insecureSkipTLSVerify and caBundle set"))
	err := s.patchAPIServiceCABundle(context.Background(), fc.Resource(apiserviceGVR), defaultAPIServiceName)
	assert.NoError(t, err)
}

func TestPatchAPIServiceCABundle_GenericError(t *testing.T) {
	s := &EASServer{caCert: []byte("pem-cert-bytes")}
	fc := fakeDynamicRIWithPatchReactor(t, fmt.Errorf("unexpected server error"))
	err := s.patchAPIServiceCABundle(context.Background(), fc.Resource(apiserviceGVR), defaultAPIServiceName)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "patch APIService caBundle")
}

// ── registerExtensionAPIService body beyond early-exit guards ─────────────────

func TestRegisterExtensionAPIService_GetAPIServiceError(t *testing.T) {
	// dynamic.NewForConfig succeeds with any *rest.Config; the subsequent
	// ri.Get call fails with "connection refused" since port 9 is closed.
	// This exercises lines 51-70 (dynamic client creation through the getErr branch).
	t.Setenv(envEASRegisterAPIService, "")
	s := &EASServer{
		restConfig: &rest.Config{Host: "https://127.0.0.1:9"},
	}
	err := s.registerExtensionAPIService(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get APIService")
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
