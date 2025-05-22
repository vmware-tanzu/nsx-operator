package subnetbinding

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	bm1ID       = "binding1_7lupl"
	bm2ID       = "binding1_i6zuf"
	bindingMap1 = &model.SubnetConnectionBindingMap{
		Id:             String(bm1ID),
		DisplayName:    String("binding1"),
		SubnetPath:     String(parentSubnetPath1),
		VlanTrafficTag: Int64(201),
		Tags: []model.Tag{
			{
				Scope: String(common.TagScopeCluster),
				Tag:   String("fake_cluster"),
			},
			{
				Scope: String(common.TagScopeVersion),
				Tag:   String("1.0.0"),
			},
			{
				Scope: String(common.TagScopeNamespace),
				Tag:   String("default"),
			},
			{
				Scope: String(common.TagScopeSubnetBindingCRName),
				Tag:   String("binding1"),
			},
			{
				Scope: String(common.TagScopeSubnetBindingCRUID),
				Tag:   String("uuid-binding1"),
			},
		},
	}
	bindingMap2 = &model.SubnetConnectionBindingMap{
		Id:             String(bm2ID),
		DisplayName:    String("binding1"),
		SubnetPath:     String(parentSubnetPath2),
		VlanTrafficTag: Int64(201),
		Tags: []model.Tag{
			{
				Scope: String(common.TagScopeCluster),
				Tag:   String("fake_cluster"),
			},
			{
				Scope: String(common.TagScopeVersion),
				Tag:   String("1.0.0"),
			},
			{
				Scope: String(common.TagScopeNamespace),
				Tag:   String("default"),
			},
			{
				Scope: String(common.TagScopeSubnetBindingCRName),
				Tag:   String("binding1"),
			},
			{
				Scope: String(common.TagScopeSubnetBindingCRUID),
				Tag:   String("uuid-binding1"),
			},
		},
	}
	parentSubnet1 = &model.VpcSubnet{
		Id:   String("parent1"),
		Path: String(parentSubnetPath1),
	}
	parentSubnet2 = &model.VpcSubnet{
		Id:   String("parent2"),
		Path: String(parentSubnetPath2),
	}
	childSubnet = &model.VpcSubnet{
		Id:   String("child"),
		Path: String(childSubnetPath1),
	}
)

func TestBuildSubnetBindings(t *testing.T) {
	service := mockService()
	parentSubnetPaths := []string{parentSubnetPath1, parentSubnetPath2}
	bindingMaps := service.buildSubnetBindings(binding1, parentSubnetPaths)
	require.Equal(t, 2, len(bindingMaps))
	expBindingMaps := []*model.SubnetConnectionBindingMap{
		bindingMap1, bindingMap2,
	}
	require.ElementsMatch(t, expBindingMaps, bindingMaps)
}

func TestBuildSubnetConnectionBindingMapCR(t *testing.T) {
	expCR := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: v1.ObjectMeta{
			UID:       types.UID("uuid-binding1"),
			Name:      "binding1",
			Namespace: "default",
		},
	}
	cr, err := buildSubnetConnectionBindingMapCR(bindingMap1)
	require.NoError(t, err)
	assert.Equal(t, expCR, cr)
}

func mockService() *BindingService {
	return &BindingService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "fake_cluster",
				},
			},
		},
		BindingStore: SetupStore(),
	}
}
