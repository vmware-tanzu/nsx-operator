package subnet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/crd.nsx.vmware.com/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestBuildSubnetName(t *testing.T) {
	svc := &SubnetService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "cluster1",
				},
			},
		},
	}
	subnet := &v1alpha1.Subnet{
		ObjectMeta: v1.ObjectMeta{
			UID:  "uuid1",
			Name: "subnet1",
		},
	}
	name := svc.buildSubnetName(subnet)
	expName := "subnet1-uuid1"
	assert.Equal(t, expName, name)
	id := svc.BuildSubnetID(subnet)
	expId := "subnet1-uuid1"
	assert.Equal(t, expId, id)
}

func TestBuildSubnetSetName(t *testing.T) {
	svc := &SubnetService{
		Service: common.Service{
			NSXConfig: &config.NSXOperatorConfig{
				CoeConfig: &config.CoeConfig{
					Cluster: "b720ee2c-5788-4680-9796-0f93db33d8a9",
				},
			},
		},
	}
	subnetset := &v1alpha1.SubnetSet{
		ObjectMeta: v1.ObjectMeta{
			UID:  "28e85c0b-21e4-4cab-b1c3-597639dfe752",
			Name: "pod-default",
		},
	}
	index := "0c5d588b"
	name := svc.buildSubnetSetName(subnetset, index)
	expName := "pod-default-28e85c0b-21e4-4cab-b1c3-597639dfe752-0c5d588b"
	assert.Equal(t, expName, name)
	assert.True(t, len(name) <= 80)
	id := svc.buildSubnetSetID(subnetset, index)
	expId := "pod-default-28e85c0b-21e4-4cab-b1c3-597639dfe752_0c5d588b"
	assert.Equal(t, expId, id)
}
