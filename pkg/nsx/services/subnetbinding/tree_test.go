package subnetbinding

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/data"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type orgRootMatcher struct {
	expectedRoot *model.OrgRoot
}

func (m *orgRootMatcher) Matches(obj interface{}) bool {
	dst, ok := obj.(model.OrgRoot)
	if !ok {
		return false
	}
	return m.matches(&dst)
}

func (m *orgRootMatcher) matches(dst *model.OrgRoot) bool {
	return stringEquals(m.expectedRoot.ResourceType, dst.ResourceType) &&
		childrenResourceEquals(m.expectedRoot.Children, dst.Children)
}

func (m *orgRootMatcher) String() string {
	return fmt.Sprintf("%v", m.expectedRoot)
}

func childrenResourceEquals(children []*data.StructValue, children2 []*data.StructValue) bool {
	if len(children) != len(children2) {
		return false
	}
	for _, child := range children {
		if !childExists(children2, child) {
			return false
		}
	}
	return true
}

func childExists(children2 []*data.StructValue, child *data.StructValue) bool {
	for _, cn := range children2 {
		if dataStructEqual(cn, child) {
			return true
		}
	}
	return false
}

func dataStructEqual(v1, v2 *data.StructValue) bool {
	if v1.Name() != v2.Name() {
		return false
	}
	if strings.Contains(v1.Name(), "child_resource_reference") {
		v1Obj, err := convertToChildResourceReference(v1)
		if err != nil {
			return false
		}
		v2Obj, err := convertToChildResourceReference(v2)
		if err != nil {
			return false
		}
		return childResourceReferenceEquals(v1Obj, v2Obj)
	} else if strings.Contains(v1.Name(), "child_subnet_connection_binding_map") {
		v1Obj, err := convertToSubnetConnectionBindingMap(v1)
		if err != nil {
			return false
		}
		v2Obj, err := convertToSubnetConnectionBindingMap(v2)
		if err != nil {
			return false
		}

		return childSubnetConnectionBindingMapEquals(v1Obj, v2Obj)
	}
	return false
}

func convertToSubnetConnectionBindingMap(v *data.StructValue) (model.ChildSubnetConnectionBindingMap, error) {
	res, err := common.NewConverter().ConvertToGolang(v, model.ChildSubnetConnectionBindingMapBindingType())
	if err != nil {
		return model.ChildSubnetConnectionBindingMap{}, err[0]
	}
	obj := res.(model.ChildSubnetConnectionBindingMap)
	return obj, nil
}

func convertToChildResourceReference(v *data.StructValue) (model.ChildResourceReference, error) {
	res, err := common.NewConverter().ConvertToGolang(v, model.ChildResourceReferenceBindingType())
	if err != nil {
		return model.ChildResourceReference{}, err[0]
	}
	obj := res.(model.ChildResourceReference)
	return obj, nil
}

func childResourceReferenceEquals(v1, v2 model.ChildResourceReference) bool {
	return stringEquals(v1.Id, v2.Id) && stringEquals(v1.TargetType, v2.TargetType) &&
		v1.ResourceType == v2.ResourceType && childrenResourceEquals(v1.Children, v2.Children)
}

func childSubnetConnectionBindingMapEquals(v1, v2 model.ChildSubnetConnectionBindingMap) bool {
	return stringEquals(v1.Id, v2.Id) && boolEquals(v1.MarkedForDelete, v2.MarkedForDelete) &&
		v1.ResourceType == v2.ResourceType && segmentConnectionBindingMapEquals(v1.SubnetConnectionBindingMap, v2.SubnetConnectionBindingMap)
}

func segmentConnectionBindingMapEquals(bm1, bm2 *model.SubnetConnectionBindingMap) bool {
	if bm1 == nil && bm2 == nil {
		return true
	}
	if bm1 == nil && bm2 != nil {
		return false
	}
	if bm1 != nil && bm2 == nil {
		return false
	}
	return stringEquals(bm1.Id, bm2.Id) && stringEquals(bm1.SubnetPath, bm2.SubnetPath) &&
		stringEquals(bm1.ParentPath, bm2.ParentPath) && int64Equals(bm1.VlanTrafficTag, bm2.VlanTrafficTag) &&
		stringEquals(bm1.Path, bm2.Path) && boolEquals(bm1.MarkedForDelete, bm2.MarkedForDelete)
}

func int64Equals(i1, i2 *int64) bool {
	if i1 == nil && i2 == nil {
		return true
	}
	if i1 == nil && i2 != nil {
		return false
	}
	if i1 != nil && i2 == nil {
		return false
	}
	return *i1 == *i2
}

func boolEquals(b1 *bool, b2 *bool) bool {
	if b1 == nil && b2 == nil {
		return true
	}
	if b1 == nil && b2 != nil {
		return false
	}
	if b1 != nil && b2 == nil {
		return false
	}
	return *b1 == *b2
}

func stringEquals(s1, s2 *string) bool {
	if s1 == nil && s2 == nil {
		return true
	}
	if s1 == nil && s2 != nil {
		return false
	}
	if s1 != nil && s2 == nil {
		return false
	}
	return *s1 == *s2
}

func TestBuildHNodeFromSubnetConnectionBindingMap(t *testing.T) {
	for _, tc := range []struct {
		name             string
		subnetBindings   []*model.SubnetConnectionBindingMap
		expOrgRootConfig map[string]map[string]map[string]map[string][]string
	}{
		{
			name: "bindings under same subnets",
			subnetBindings: []*model.SubnetConnectionBindingMap{
				genSubnetConnectionBindingMap(bm1ID, "binding1", parentSubnetPath1, "/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1", 201),
				genSubnetConnectionBindingMap(bm2ID, "binding2", parentSubnetPath2, "/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1", 202),
			},
			expOrgRootConfig: map[string]map[string]map[string]map[string][]string{
				"default": {
					"default": {
						"vpc1": {
							"subnet1": []string{bm1ID, bm2ID},
						},
					},
				},
			},
		},
		{
			name: "bindings under different subnets",
			subnetBindings: []*model.SubnetConnectionBindingMap{
				genSubnetConnectionBindingMap(bm1ID, "binding1", parentSubnetPath1, "/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1", 201),
				genSubnetConnectionBindingMap(bm2ID, "binding2", parentSubnetPath2, "/orgs/default/projects/default/vpcs/vpc1/subnets/subnet2", 202),
			},
			expOrgRootConfig: map[string]map[string]map[string]map[string][]string{
				"default": {
					"default": {
						"vpc1": {
							"subnet1": []string{bm1ID},
							"subnet2": []string{bm2ID},
						},
					},
				},
			},
		}, {
			name: "bindings under different VPCs",
			subnetBindings: []*model.SubnetConnectionBindingMap{
				genSubnetConnectionBindingMap(bm1ID, "binding1", parentSubnetPath1, "/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1", 201),
				genSubnetConnectionBindingMap(bm2ID, "binding2", parentSubnetPath2, "/orgs/default/projects/default/vpcs/vpc2/subnets/subnet1", 202),
			},
			expOrgRootConfig: map[string]map[string]map[string]map[string][]string{
				"default": {
					"default": {
						"vpc1": {
							"subnet1": []string{bm1ID},
						},
						"vpc2": {
							"subnet1": []string{bm2ID},
						},
					},
				},
			},
		}, {
			name: "bindings under different projects",
			subnetBindings: []*model.SubnetConnectionBindingMap{
				genSubnetConnectionBindingMap(bm1ID, "binding1", parentSubnetPath1, "/orgs/default/projects/default/vpcs/vpc1/subnets/subnet1", 201),
				genSubnetConnectionBindingMap(bm2ID, "binding2", parentSubnetPath2, "/orgs/default/projects/project1/vpcs/vpc2/subnets/subnet1", 202),
			},
			expOrgRootConfig: map[string]map[string]map[string]map[string][]string{
				"default": {
					"default": {
						"vpc1": {
							"subnet1": []string{bm1ID},
						},
					},
					"project1": {
						"vpc2": {
							"subnet1": []string{bm2ID},
						},
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bmMappings := make(map[string]*model.SubnetConnectionBindingMap)
			for _, bm := range tc.subnetBindings {
				bmMappings[*bm.Id] = bm
			}
			orgRootConfig := convertToOrgConfig(tc.expOrgRootConfig, bmMappings)
			expOrgRoot, err := wrapOrgRoot(orgRootConfig)
			require.Nil(t, err)

			orgRoot, err := buildOrgRootBySubnetConnectionBindingMaps(tc.subnetBindings, "")
			require.NoError(t, err)

			expRoot := orgRootMatcher{expOrgRoot}
			assert.True(t, expRoot.matches(orgRoot))
		})
	}
}

func convertToOrgConfig(testCfg map[string]map[string]map[string]map[string][]string, subnetBindings map[string]*model.SubnetConnectionBindingMap) map[string]map[string]map[string]map[string][]*model.SubnetConnectionBindingMap {
	out := make(map[string]map[string]map[string]map[string][]*model.SubnetConnectionBindingMap)
	for k1, v1 := range testCfg {
		out[k1] = make(map[string]map[string]map[string][]*model.SubnetConnectionBindingMap)
		for k2, v2 := range v1 {
			out[k1][k2] = make(map[string]map[string][]*model.SubnetConnectionBindingMap)
			for k3, v3 := range v2 {
				out[k1][k2][k3] = make(map[string][]*model.SubnetConnectionBindingMap)
				for k4, v4 := range v3 {
					bms := make([]*model.SubnetConnectionBindingMap, len(v4))
					for i, bmID := range v4 {
						bms[i] = subnetBindings[bmID]
					}
					out[k1][k2][k3][k4] = bms
				}
			}
		}
	}
	return out
}

func wrapOrgRoot(orgConfigs map[string]map[string]map[string]map[string][]*model.SubnetConnectionBindingMap) (*model.OrgRoot, error) {
	// This is the outermost layer of the hierarchy SubnetConnectionBindingMaps.
	// It doesn't need ID field.
	resourceType := "OrgRoot"
	children := make([]*data.StructValue, 0)
	for orgID, orgConfig := range orgConfigs {
		child, err := wrapOrg(orgID, orgConfig)
		if err != nil {
			return nil, err
		}
		children = append(children, child...)
	}
	orgRoot := model.OrgRoot{
		Children:     children,
		ResourceType: &resourceType,
	}
	return &orgRoot, nil
}

func wrapOrg(orgID string, orgConfig map[string]map[string]map[string][]*model.SubnetConnectionBindingMap) ([]*data.StructValue, error) {
	children := make([]*data.StructValue, 0)
	for projectID, projectConfig := range orgConfig {
		child, err := wrapProject(projectID, projectConfig)
		if err != nil {
			return nil, err
		}
		children = append(children, child...)
	}
	return wrapChildResourceReference("Org", orgID, children)
}

func wrapProject(projectID string, projectConfig map[string]map[string][]*model.SubnetConnectionBindingMap) ([]*data.StructValue, error) {
	children := make([]*data.StructValue, 0)
	for vpcID, vpcConfig := range projectConfig {
		child, err := wrapVPC(vpcID, vpcConfig)
		if err != nil {
			return nil, err
		}
		children = append(children, child...)
	}
	return wrapChildResourceReference("Project", projectID, children)
}

func wrapVPC(vpcID string, vpcConfig map[string][]*model.SubnetConnectionBindingMap) ([]*data.StructValue, error) {
	children := make([]*data.StructValue, 0)
	for subnetID, subnetConfig := range vpcConfig {
		child, err := wrapSubnet(subnetID, subnetConfig)
		if err != nil {
			return nil, err
		}
		children = append(children, child...)
	}
	return wrapChildResourceReference("Vpc", vpcID, children)
}

func wrapSubnet(subnetId string, bindingMaps []*model.SubnetConnectionBindingMap) ([]*data.StructValue, error) {
	children, err := wrapSubnetBindingMaps(bindingMaps)
	if err != nil {
		return nil, err
	}
	return wrapChildResourceReference("VpcSubnet", subnetId, children)
}

func wrapSubnetBindingMaps(bindingMaps []*model.SubnetConnectionBindingMap) ([]*data.StructValue, error) {
	dataValues := make([]*data.StructValue, 0)
	for _, bindingMap := range bindingMaps {
		dataValue, err := wrapSubnetBindingMap(bindingMap)
		if err != nil {
			return nil, err
		}
		dataValues = append(dataValues, dataValue)
	}
	return dataValues, nil
}

func TestBuildRootNodePerformance(t *testing.T) {
	orgPrefix, orgCount := "org", 1
	projectPrefix, projectCount := "proj", 10
	vpcPrefix, vpcCount := "vpc", 20
	subnetPrefix, subnetCount := "subnet", 100
	bindingPrefix, bindingCount := "binding", 5

	bindings := make([]*model.SubnetConnectionBindingMap, 0)
	for i := 1; i <= orgCount; i++ {
		orgID := fmt.Sprintf("%s%d", orgPrefix, i)
		for j := 1; j <= projectCount; j++ {
			projID := fmt.Sprintf("%s%d", projectPrefix, j)
			for k := 1; k <= vpcCount; k++ {
				vpcID := fmt.Sprintf("%s%d", vpcPrefix, k)
				for l := 1; l <= subnetCount; l++ {
					subnetID := fmt.Sprintf("%s%d", subnetPrefix, l)
					subnetPath := fmt.Sprintf("/orgs/%s/projects/%s/vpcs/%s/subnets/%s", orgID, projID, vpcID, subnetID)
					for m := 0; m <= bindingCount; m++ {
						bindingID := fmt.Sprintf("%s%d", bindingPrefix, m)
						bindingPath := fmt.Sprintf("%s/subnet-connection-binding-maps/%s", subnetPath, bindingID)
						binding := &model.SubnetConnectionBindingMap{
							Id:           common.String(bindingID),
							Path:         common.String(bindingPath),
							ParentPath:   common.String(subnetPath),
							ResourceType: common.String(leafType),
						}
						bindings = append(bindings, binding)
					}
				}
			}
		}
	}

	// The total time to build OrgRoot with 10W SubnetConnectionBindngMaps is supposed to less than 10s.
	start := time.Now()
	buildRootNode(bindings, "")
	assert.True(t, time.Now().Sub(start).Seconds() < 5)
}
