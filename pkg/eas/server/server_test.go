/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

// fakeVPCInfoProvider is a test double for eas.VPCInfoProvider.
type fakeVPCInfoProvider struct {
	namespaces []string
}

func (f fakeVPCInfoProvider) ListVPCInfo(string) []eas.VPCEntry { return nil }
func (f fakeVPCInfoProvider) ListAllVPCNamespaces() []string    { return f.namespaces }

func newTestFakeK8sClient() *fake.ClientBuilder {
	s := runtime.NewScheme()
	_ = vpcv1alpha1.AddToScheme(s)
	return fake.NewClientBuilder().WithScheme(s)
}

func newTestEASServer(t *testing.T) *EASServer {
	t.Helper()
	return NewEASServer(
		&nsx.Client{},
		fakeVPCInfoProvider{namespaces: []string{"ns1"}},
		newTestFakeK8sClient().Build(),
		nil,
		"",
		nil,
	)
}

func TestNewEASServer(t *testing.T) {
	s := newTestEASServer(t)
	require.NotNil(t, s)
	assert.Nil(t, s.restConfig)
	assert.NotNil(t, s.vpcIPUsage)
	assert.NotNil(t, s.ipBlockUsage)
	assert.NotNil(t, s.subnetIPPools)
	assert.NotNil(t, s.subnetDHCPStats)
}

func TestBuildGenericAPIServer_ErrorsWithoutCert(t *testing.T) {
	// buildGenericAPIServer will fail because cert files referenced by listenerConfig
	// (EAS_PORT / EAS_BIND_ADDRESS defaulting to non-existent paths) do not exist.
	// The test exercises all setup statements before the first I/O-related failure.
	t.Setenv(easPortEnv, "")
	t.Setenv(easBindAddressEnv, "")
	s := &EASServer{}
	_, err := s.buildGenericAPIServer()
	assert.Error(t, err, "buildGenericAPIServer must fail without valid TLS cert files")
	t.Logf("buildGenericAPIServer error: %v", err)
}

func TestListenerConfig(t *testing.T) {
	t.Run("default port", func(t *testing.T) {
		t.Setenv(easPortEnv, "")
		t.Setenv(easBindAddressEnv, "")
		port, bindAddr, certFile, keyFile := listenerConfig()
		assert.Equal(t, 9553, port)
		assert.Nil(t, bindAddr)
		assert.Contains(t, certFile, "eas.crt")
		assert.Contains(t, keyFile, "eas.key")
	})

	t.Run("explicit port", func(t *testing.T) {
		t.Setenv(easPortEnv, "19443")
		port, _, _, _ := listenerConfig()
		assert.Equal(t, 19443, port)
	})

	t.Run("explicit bind address", func(t *testing.T) {
		t.Setenv(easBindAddressEnv, "10.0.0.1")
		_, bindAddr, _, _ := listenerConfig()
		require.NotNil(t, bindAddr)
		assert.Equal(t, "10.0.0.1", bindAddr.String())
	})

	t.Run("invalid port falls back to 9553", func(t *testing.T) {
		t.Setenv(easPortEnv, "not-a-port")
		port, _, _, _ := listenerConfig()
		assert.Equal(t, 9553, port)
	})
}
