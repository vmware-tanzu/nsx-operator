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
	staticroutes, err := service.buildStaticRoute(obj)
	assert.Equal(t, err, nil)
	assert.Equal(t, len(staticroutes.NextHops), 2)
	expName := "teststaticroute"
	assert.Equal(t, expName, *staticroutes.DisplayName)
	expId := "teststaticroute_du8nz"
	assert.Equal(t, expId, *staticroutes.Id)
}
