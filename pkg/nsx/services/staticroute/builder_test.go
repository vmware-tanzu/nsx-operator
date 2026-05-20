package staticroute

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// TestBuildStaticRoute_NetworkIPAllocationPath verifies that buildStaticRoute sets
// NetworkIpAllocationPath (not Network) when a non-empty networkIPAllocationPath is
// provided, and sets Network (not NetworkIpAllocationPath) when it is empty.
func TestBuildStaticRoute_NetworkIPAllocationPath(t *testing.T) {
	service := &StaticRouteService{Service: common.Service{}, StaticRouteStore: buildStaticRouteStore()}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID",
		func(_ *common.Service, _ string) types.UID { return types.UID("nsUUID") })
	defer patches.Reset()

	service.NSXConfig = &config.NSXOperatorConfig{CoeConfig: &config.CoeConfig{Cluster: "test_1"}}

	obj := &v1alpha1.StaticRoute{}
	obj.Name = "testroute"
	obj.Namespace = "ns1"
	obj.UID = "uid-abc"
	obj.Spec.NextHops = []v1alpha1.NextHop{{IPAddress: "192.168.1.1"}}

	allocPath := "/orgs/default/projects/p1/vpcs/v1/ip-address-allocations/alloc-1"

	t.Run("networkIPAllocationPath sets NetworkIpAllocationPath only", func(t *testing.T) {
		sr, err := service.buildStaticRoute(obj, allocPath)
		assert.NoError(t, err)
		assert.NotNil(t, sr.NetworkIpAllocationPath)
		assert.Equal(t, allocPath, *sr.NetworkIpAllocationPath)
		assert.Nil(t, sr.Network, "Network must be nil when NetworkIpAllocationPath is used")
	})

	t.Run("empty path falls back to spec.network", func(t *testing.T) {
		obj.Spec.Network = "10.0.0.0/24"
		sr, err := service.buildStaticRoute(obj, "")
		assert.NoError(t, err)
		assert.NotNil(t, sr.Network)
		assert.Equal(t, "10.0.0.0/24", *sr.Network)
		assert.Nil(t, sr.NetworkIpAllocationPath, "NetworkIpAllocationPath must be nil when Network CIDR is used")
	})
}

func TestValidateStaticRoute(t *testing.T) {
	obj := &v1alpha1.StaticRoute{}
	err := validateStaticRoute(obj)
	assert.Equal(t, err, nil)

	ip1 := "10.0.0.1"
	obj.Spec.NextHops = []v1alpha1.NextHop{{IPAddress: ip1}, {IPAddress: ip1}}
	err = validateStaticRoute(obj)
	assert.Equal(t, err, fmt.Errorf("duplicate ip address %s", ip1))

	ip2 := "10.0.0.0.1"
	obj.Spec.NextHops = []v1alpha1.NextHop{{IPAddress: ip1}, {IPAddress: ip2}}
	err = validateStaticRoute(obj)
	assert.Equal(t, err, fmt.Errorf("invalid IP address: %s", ip2))
}

func TestBuildStaticRoute(t *testing.T) {
	obj := &v1alpha1.StaticRoute{}
	ip1 := "10.0.0.1"
	ip2 := "10.0.0.2"
	obj.Spec.NextHops = []v1alpha1.NextHop{{IPAddress: ip1}, {IPAddress: ip2}}
	obj.ObjectMeta.Name = "teststaticroute"
	obj.ObjectMeta.Namespace = "qe"
	obj.ObjectMeta.UID = "uuid1"

	service := &StaticRouteService{Service: common.Service{}, StaticRouteStore: buildStaticRouteStore()}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(&service.Service), "GetNamespaceUID",
		func(s *common.Service, ns string) types.UID {
			return types.UID("nsUUID")
		})
	defer patches.Reset()

	service.NSXConfig = &config.NSXOperatorConfig{}
	service.NSXConfig.CoeConfig = &config.CoeConfig{}
	service.NSXConfig.Cluster = "test_1"
	staticroutes, err := service.buildStaticRoute(obj, "")
	assert.Equal(t, err, nil)
	assert.Equal(t, len(staticroutes.NextHops), 2)
	expName := "teststaticroute"
	assert.Equal(t, expName, *staticroutes.DisplayName)
	expId := "teststaticroute_du8nz"
	assert.Equal(t, expId, *staticroutes.Id)
}
