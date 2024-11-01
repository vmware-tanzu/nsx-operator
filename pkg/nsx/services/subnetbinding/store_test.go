package subnetbinding

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	childSubnetPath1  = "/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1"
	childSubnetPath2  = "/orgs/default/projects/default/vpcs/vpc1/subnets/subnet2"
	parentSubnetPath1 = "/orgs/default/projects/default/vpcs/vpc1/subnets/parent1"
	parentSubnetPath2 = "/orgs/default/projects/default/vpcs/vpc1/subnets/parent2"
	binding1          = &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      "binding1",
			Namespace: "default",
			UID:       "uuid-binding1",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			VLANTrafficTag:      201,
			TargetSubnetSetName: "parent",
		},
	}
	binding2 = &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      "binding2",
			Namespace: "ns1",
			UID:       "uuid-binding2",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child2",
			VLANTrafficTag:      202,
			TargetSubnetSetName: "parent2",
		},
	}
	incompleteBindingMap = &model.SubnetConnectionBindingMap{
		Id:             String("incomplete"),
		DisplayName:    String("binding1"),
		SubnetPath:     String(parentSubnetPath1),
		ParentPath:     childSubnet.Path,
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
				Scope: String(common.TagScopeSubnetBindingCRName),
				Tag:   String("binding1"),
			},
		},
	}
)

func TestStore(t *testing.T) {
	store := SetupStore()
	bm1 := &model.SubnetConnectionBindingMap{
		Id:             String("binding1-parent1"),
		DisplayName:    String("binding1"),
		Path:           String("/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1/subnet-connection-binding-maps/binding1-parent1"),
		ParentPath:     String(childSubnetPath1),
		SubnetPath:     String(parentSubnetPath1),
		VlanTrafficTag: Int64(201),
		Tags: []model.Tag{
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
	store.Apply(bm1)
	bm2 := &model.SubnetConnectionBindingMap{
		Id:             String("binding1-parent2"),
		DisplayName:    String("binding1"),
		Path:           String("/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1/subnet-connection-binding-maps/binding1-parent2"),
		ParentPath:     String(childSubnetPath1),
		SubnetPath:     String(parentSubnetPath2),
		VlanTrafficTag: Int64(201),
		Tags: []model.Tag{
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
	store.Apply(bm2)

	bindings := store.getBindingsByChildSubnet(childSubnetPath1)
	require.Equal(t, 2, len(bindings))
	require.ElementsMatch(t, []*model.SubnetConnectionBindingMap{bm1, bm2}, bindings)

	bindings = store.getBindingsByParentSubnet(parentSubnetPath1)
	require.Equal(t, 1, len(bindings))
	require.Equal(t, bm1, bindings[0])

	bindings = store.getBindingsByParentSubnet(parentSubnetPath2)
	require.Equal(t, 1, len(bindings))
	require.Equal(t, bm2, bindings[0])

	bindings = store.getBindingsByBindingMapCRUID(string(binding1.UID))
	require.Equal(t, 2, len(bindings))
	require.ElementsMatch(t, []*model.SubnetConnectionBindingMap{bm1, bm2}, bindings)

	bindings = store.getBindingsByBindingMapCRName(binding1.Name, binding1.Namespace)
	require.Equal(t, 2, len(bindings))
	require.ElementsMatch(t, []*model.SubnetConnectionBindingMap{bm1, bm2}, bindings)

	bindingMap := store.GetByKey(*bm1.Id)
	require.NotNil(t, bindingMap)
	require.Equal(t, bm1, bindingMap)

	delBindingMap1 := *bm1
	delBindingMap1.MarkedForDelete = Bool(true)
	delBindingMap2 := *bm2
	delBindingMap2.MarkedForDelete = Bool(true)
	store.Apply(&delBindingMap1)
	store.Apply(&delBindingMap2)

	bindings = store.getBindingsByBindingMapCRUID(string(binding1.UID))
	require.Equal(t, 0, len(bindings))
}
