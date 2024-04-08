package networkinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	types "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestNsxtProjectPathToId(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		org     string
		project string
		err     interface{}
	}{
		{"1", "/orgs/default/projects/nsx_operator_e2e_test", "default", "nsx_operator_e2e_test", nil},
		{"2", "", "", "", "dummy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, p, e := nsxtProjectPathToId(tt.path)
			if tt.err != nil {
				assert.NotNil(t, e)
			} else {
				assert.Nil(t, e)
			}
			assert.Equal(t, tt.org, o)
			assert.Equal(t, tt.project, p)
		})
	}
}

func TestIsDefaultNetworkConfigCR(t *testing.T) {
	testCRD1 := v1alpha1.VPCNetworkConfiguration{}
	testCRD1.Name = "test-1"
	testCRD2 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				types.AnnotationDefaultNetworkConfig: "invalid",
			},
		},
	}
	testCRD2.Name = "test-2"
	testCRD3 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				types.AnnotationDefaultNetworkConfig: "true",
			},
		},
	}
	testCRD3.Name = "test-3"
	assert.Equal(t, isDefaultNetworkConfigCR(testCRD1), false)
	assert.Equal(t, isDefaultNetworkConfigCR(testCRD2), false)
	assert.Equal(t, isDefaultNetworkConfigCR(testCRD3), true)

}

func TestBuildNetworkConfigInfo(t *testing.T) {
	emptyCRD := &v1alpha1.VPCNetworkConfiguration{}
	emptyCRD2 := &v1alpha1.VPCNetworkConfiguration{
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			NSXTProject: "/invalid/path",
		},
	}
	_, e := buildNetworkConfigInfo(*emptyCRD)
	assert.NotNil(t, e)
	_, e = buildNetworkConfigInfo(*emptyCRD2)
	assert.NotNil(t, e)

	spec1 := v1alpha1.VPCNetworkConfigurationSpec{
		DefaultGatewayPath:         "test-gw-path-1",
		EdgeClusterPath:            "test-edge-path-1",
		ExternalIPv4Blocks:         []string{"external-ipb-1", "external-ipb-2"},
		PrivateIPv4CIDRs:           []string{"private-ipb-1", "private-ipb-2"},
		DefaultIPv4SubnetSize:      64,
		DefaultPodSubnetAccessMode: "Public",
		NSXTProject:                "/orgs/default/projects/nsx_operator_e2e_test",
	}
	spec2 := v1alpha1.VPCNetworkConfigurationSpec{
		DefaultGatewayPath:         "test-gw-path-2",
		EdgeClusterPath:            "test-edge-path-2",
		ExternalIPv4Blocks:         []string{"external-ipb-1", "external-ipb-2"},
		PrivateIPv4CIDRs:           []string{"private-ipb-1", "private-ipb-2"},
		DefaultIPv4SubnetSize:      32,
		DefaultPodSubnetAccessMode: "Private",
		NSXTProject:                "/orgs/anotherOrg/projects/anotherProject",
	}
	testCRD1 := v1alpha1.VPCNetworkConfiguration{
		Spec: spec1,
	}
	testCRD1.Name = "test-1"
	testCRD2 := v1alpha1.VPCNetworkConfiguration{
		Spec: spec2,
	}
	testCRD2.Name = "test-2"

	testCRD3 := v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				types.AnnotationDefaultNetworkConfig: "true",
			},
		},
		Spec: spec2,
	}
	testCRD3.Name = "test-3"

	tests := []struct {
		name       string
		nc         v1alpha1.VPCNetworkConfiguration
		gw         string
		edge       string
		org        string
		project    string
		subnetSize int
		accessMode string
		isDefault  bool
	}{
		{"1", testCRD1, "test-gw-path-1", "test-edge-path-1", "default", "nsx_operator_e2e_test", 64, "Public", false},
		{"2", testCRD2, "test-gw-path-2", "test-edge-path-2", "anotherOrg", "anotherProject", 32, "Private", false},
		{"3", testCRD3, "test-gw-path-2", "test-edge-path-2", "anotherOrg", "anotherProject", 32, "Private", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nc, e := buildNetworkConfigInfo(tt.nc)
			assert.Nil(t, e)
			assert.Equal(t, tt.gw, nc.DefaultGatewayPath)
			assert.Equal(t, tt.edge, nc.EdgeClusterPath)
			assert.Equal(t, tt.org, nc.Org)
			assert.Equal(t, tt.project, nc.NsxtProject)
			assert.Equal(t, tt.subnetSize, nc.DefaultIPv4SubnetSize)
			assert.Equal(t, tt.accessMode, nc.DefaultPodSubnetAccessMode)
			assert.Equal(t, tt.isDefault, nc.IsDefault)
		})
	}

}
