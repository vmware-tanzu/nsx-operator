package subnetbinding

import (
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
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
		// SubnetAssociation defaults to TRUNK when unset in the CR.
		SubnetAssociation: String(model.SubnetConnectionBindingMap_SUBNET_ASSOCIATION_TRUNK),
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
		// SubnetAssociation defaults to TRUNK when unset in the CR.
		SubnetAssociation: String(model.SubnetConnectionBindingMap_SUBNET_ASSOCIATION_TRUNK),
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
	// Simulate NSX >= 9.2.0 where the subnet_association field is supported.
	patches := gomonkey.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool { return true })
	defer patches.Reset()

	service := mockService()
	parentSubnetPaths := []string{parentSubnetPath1, parentSubnetPath2}
	bindingMaps := service.buildSubnetBindings(binding1, 201, parentSubnetPaths)
	require.Equal(t, 2, len(bindingMaps))
	expBindingMaps := []*model.SubnetConnectionBindingMap{
		bindingMap1, bindingMap2,
	}
	require.ElementsMatch(t, expBindingMaps, bindingMaps)
}

func TestBuildSubnetBindingsDefaultTrunkAssociation(t *testing.T) {
	// Simulate NSX >= 9.2.0 where the subnet_association field is supported.
	patches := gomonkey.ApplyMethodFunc(&nsx.Client{}, "NSXCheckVersion", func(feature int) bool { return true })
	defer patches.Reset()

	service := mockService()
	parentSubnetPaths := []string{parentSubnetPath1}
	bindingMaps := service.buildSubnetBindings(binding1, 201, parentSubnetPaths)
	require.Len(t, bindingMaps, 1)
	// An unset subnetAssociation in the CR must be defaulted to TRUNK so that the
	// desired object matches the realized NSX object (NSX defaults the omitted
	// field to TRUNK), avoiding redundant PATCH requests on every reconciliation.
	require.NotNil(t, bindingMaps[0].SubnetAssociation)
	assert.Equal(t, model.SubnetConnectionBindingMap_SUBNET_ASSOCIATION_TRUNK, *bindingMaps[0].SubnetAssociation)
}

func TestBuildSubnetBindingsBranch(t *testing.T) {
	service := mockService()
	branchBinding := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      "cross-vpc-binding",
			Namespace: "ns-vpc-b",
			UID:       "uuid-cross",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:        "parent-subnet",
			TargetSubnetName:  "child-subnet",
			SubnetAssociation: v1alpha1.SubnetAssociationBranch,
			VLANTrafficTag:    v1alpha1.VLANTrafficTagPtr(201),
		},
	}
	targetPaths := []string{"/orgs/default/projects/default/vpcs/vpc-b/subnets/child-subnet"}
	bindingMaps := service.buildSubnetBindings(branchBinding, 201, targetPaths)
	require.Len(t, bindingMaps, 1)
	require.NotNil(t, bindingMaps[0].SubnetAssociation)
	assert.Equal(t, model.SubnetConnectionBindingMap_SUBNET_ASSOCIATION_BRANCH, *bindingMaps[0].SubnetAssociation)
	assert.Equal(t, targetPaths[0], *bindingMaps[0].SubnetPath)
}

func TestBuildSubnetBindingsUnsupportedNSXVersion(t *testing.T) {
	// NSXCheckVersion returns false on NSX < 9.2.0 (mockService has no version
	// checker), simulating e.g. NSX 9.1 where subnet_association is unsupported.
	service := mockService()

	// An unset subnetAssociation in the CR must be left unset on NSX versions
	// without the SubnetAssociation feature, so that the request stays
	// compatible with NSX 9.1 (NSX treats an omitted association as TRUNK).
	bindingMaps := service.buildSubnetBindings(binding1, 201, []string{parentSubnetPath1})
	require.Len(t, bindingMaps, 1)
	assert.Nil(t, bindingMaps[0].SubnetAssociation)

	// An explicitly specified subnetAssociation is always sent; the controller
	// rejects such CRs on unsupported NSX versions before reaching the builder.
	branchBinding := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      "cross-vpc-binding",
			Namespace: "ns-vpc-b",
			UID:       "uuid-cross",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:        "parent-subnet",
			TargetSubnetName:  "child-subnet",
			SubnetAssociation: v1alpha1.SubnetAssociationBranch,
			VLANTrafficTag:    v1alpha1.VLANTrafficTagPtr(201),
		},
	}
	bindingMaps = service.buildSubnetBindings(branchBinding, 201, []string{"/orgs/default/projects/default/vpcs/vpc-b/subnets/child-subnet"})
	require.Len(t, bindingMaps, 1)
	require.NotNil(t, bindingMaps[0].SubnetAssociation)
	assert.Equal(t, model.SubnetConnectionBindingMap_SUBNET_ASSOCIATION_BRANCH, *bindingMaps[0].SubnetAssociation)
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
