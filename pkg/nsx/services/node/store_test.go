package node

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func Test_KeyFunc(t *testing.T) {
	id := "test_id"
	node := model.HostTransportNode{Id: &id}
	t.Run("1", func(t *testing.T) {
		got, _ := keyFunc(&node)
		if got != "test_id" {
			t.Errorf("keyFunc() = %v, want %v", got, "test_id")
		}
	})
}

func TestSubnetStore_Apply(t *testing.T) {
	resourceStore := common.ResourceStore{
		Indexer: cache.NewIndexer(
			keyFunc,
			cache.Indexers{
				common.IndexKeyNodeName: nodeIndexByNodeName,
			},
		),
		BindingType: model.HostTransportNodeBindingType(),
	}
	nodeStore := &NodeStore{ResourceStore: resourceStore}
	fakeNode := model.HostTransportNode{
		Id: common.String("node_id"),
		NodeDeploymentInfo: &model.FabricHostNode{
			Fqdn: common.String("node_name"),
		},
	}
	type args struct {
		i interface{}
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{"1", args{i: &fakeNode}, assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, nodeStore.Apply(tt.args.i), fmt.Sprintf("Apply(%v)", tt.args.i))
		})
	}
}
