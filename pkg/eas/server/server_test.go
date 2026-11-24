/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	apirest "k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
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
	assert.NotNil(t, s.vpcEndpointStats)
	assert.NotNil(t, s.vpcServiceEndpointStats)
	assert.NotNil(t, s.subnetDHCPLeases)
	assert.NotNil(t, s.subnetStatuses)
	assert.NotNil(t, s.vpcServiceProfiles)
	assert.NotNil(t, s.vpcIPBlockAllocationStates)
}

func TestReadOnlyResources(t *testing.T) {
	resources := newTestEASServer(t).restStorage()
	require.Len(t, resources, 10)
	for _, tc := range []struct {
		name    string
		object  runtime.Object
		canList bool
	}{
		{"vpcserviceprofiles", &easv1alpha1.VPCServiceProfile{}, true},
		{"subnetstatuses", &easv1alpha1.SubnetStatus{}, true},
		{"vpcipblockallocationstates", &easv1alpha1.VPCIPBlockAllocationState{}, false},
		{"subnetdhcpserverleases", &easv1alpha1.SubnetDHCPServerLeases{}, false},
		{"vpcendpointstatistics", &easv1alpha1.VPCEndpointStatistics{}, false},
		{"serviceendpointstatistics", &easv1alpha1.ServiceEndpointStatistics{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := resources[tc.name]
			require.NotNil(t, store)
			assert.IsType(t, tc.object, store.New())
			require.Implements(t, (*apirest.Getter)(nil), store)
			_, canList := store.(apirest.Lister)
			assert.Equal(t, tc.canList, canList)
			require.Implements(t, (*apirest.Scoper)(nil), store)
			assert.True(t, store.(apirest.Scoper).NamespaceScoped())
			_, canCreate := store.(apirest.Creater) //nolint:misspell // This is the Kubernetes interface name.
			_, canUpdate := store.(apirest.Updater)
			_, canDelete := store.(apirest.GracefulDeleter)
			assert.False(t, canCreate)
			assert.False(t, canUpdate)
			assert.False(t, canDelete)
		})
	}
}

func TestResourceOpenAPIDependencies(t *testing.T) {
	definitions := easv1alpha1.GetOpenAPIDefinitions(func(name string) spec.Ref {
		return spec.MustCreateRef("#/definitions/" + name)
	})
	seen := make(map[string]bool)
	var check func(string)
	check = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		definition, exists := definitions[name]
		require.True(t, exists, "missing OpenAPI definition for %s", name)
		for _, dependency := range definition.Dependencies {
			check(dependency)
		}
	}
	check("github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1.VPCServiceProfile")
	check("github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1.VPCServiceProfileList")
	check("github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1.SubnetStatus")
	check("github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1.SubnetStatusList")
	check("github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1.VPCIPBlockAllocationState")
	check("github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1.SubnetDHCPServerLeases")
	check("github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1.VPCEndpointStatistics")
	check("github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1.ServiceEndpointStatistics")
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
