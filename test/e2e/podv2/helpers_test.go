package podv2

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/ini.v1"
)

func TestConfigPreservesUnrelatedSettings(t *testing.T) {
	original := []byte("[nsx_v3]\nnsx_api_managers=example.invalid\npod_v2=false\n[k8s]\ncluster=fixture\n")
	b, err := Config(original, true, false, true, true)
	require.NoError(t, err)
	f, err := ini.Load(b)
	require.NoError(t, err)
	require.Equal(t, "example.invalid", f.Section("nsx_v3").Key("nsx_api_managers").String())
	require.Equal(t, "fixture", f.Section("k8s").Key("cluster").String())
	require.True(t, f.Section("nsx_v3").Key("pod_v2").MustBool())
	require.True(t, f.Section("k8s").Key("enable_restore").MustBool())
	require.Contains(t, string(original), "pod_v2=false")
}

func TestPortPathRejectsOtherResources(t *testing.T) {
	for _, path := range []string{"", "/orgs/a/projects/b/vpcs/c/subnets/d", "/orgs/a/projects/b/vpcs/c/subnets/d/ports/", "/orgs/a/projects/b/vpcs/c/subnets/d/ports/../x", "/orgs/a/projects/b/vpcs/c/subnets/d/groups/e"} {
		_, err := PortPath(path)
		require.Error(t, err, path)
	}
	p, err := PortPath("/orgs/a/projects/b/vpcs/c/subnets/d/ports/e")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c", "d", "e"}, p)
}

func TestIPsComparePodAndCRFormats(t *testing.T) {
	a, err := IPs([]string{"2001:db8::1/64", "192.0.2.1/24", ""})
	require.NoError(t, err)
	b, err := IPs([]string{"192.0.2.1", "2001:db8:0:0::1"})
	require.NoError(t, err)
	require.Equal(t, a, b)
	_, err = IPs([]string{"not-an-ip"})
	require.Error(t, err)
}
