package ipblocksinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	fakeVpcPath           = "vpc-path"
	fakeVpcProfilePath    = "vpc-connectivity-profile-path"
	fakeIpBlockPath       = "ip-block-path"
	fakeVpcAttachmentPath = fakeVpcPath + "/attachments/defaults"
	fakeDeleted           = true
)

func Test_KeyFunc(t *testing.T) {
	vpc := model.Vpc{Path: &fakeVpcPath}
	vpcProfile := model.VpcConnectivityProfile{Path: &fakeVpcProfilePath}
	ipBlock := model.IpAddressBlock{Path: &fakeIpBlockPath}
	notSupported := struct{}{}

	type args struct {
		obj interface{}
	}

	tests := []struct {
		name        string
		expectedKey string
		item        args
		expectedErr bool
	}{
		{
			name:        "Vpc",
			item:        args{obj: &vpc},
			expectedKey: fakeVpcPath,
		},
		{
			name:        "VpcConnectivityProfile",
			item:        args{obj: &vpcProfile},
			expectedKey: fakeVpcProfilePath,
		},
		{
			name:        "IpBlock",
			item:        args{obj: &ipBlock},
			expectedKey: fakeIpBlockPath,
		},
		{
			name:        "NotSupported",
			item:        args{obj: &notSupported},
			expectedErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := keyFunc(tt.item.obj)
			if !tt.expectedErr {
				assert.Nil(t, err)
				if got != tt.expectedKey {
					t.Errorf("keyFunc() = %v, want %v", got, tt.expectedKey)
				}
			} else {
				assert.NotNil(t, err)
			}

		})
	}

}

func TestVPCConnectivityProfileStore_Apply(t *testing.T) {
	vpcConnectivityProfileStore := &VPCConnectivityProfileStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.VpcConnectivityProfileBindingType(),
	}}

	profile1 := model.VpcConnectivityProfile{
		Path: &fakeVpcProfilePath,
	}
	profile2 := model.VpcConnectivityProfile{
		Path:            &fakeVpcProfilePath,
		MarkedForDelete: &fakeDeleted,
	}

	type args struct {
		i interface{}
	}
	tests := []struct {
		name string
		args args
	}{
		{"Add", args{i: &profile1}},
		{"Delete", args{i: &profile2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := vpcConnectivityProfileStore.Apply(tt.args.i)
			assert.Nil(t, err)
		})
	}
}

func TestIPBlockStore_Apply(t *testing.T) {
	ipBlockStore := &IPBlockStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.IpAddressBlockBindingType(),
	}}

	ipblock1 := model.IpAddressBlock{
		Path: &fakeIpBlockPath,
	}
	ipblock2 := model.IpAddressBlock{
		Path:            &fakeIpBlockPath,
		MarkedForDelete: &fakeDeleted,
	}

	type args struct {
		i interface{}
	}
	tests := []struct {
		name string
		args args
	}{
		{"Add", args{i: &ipblock1}},
		{"Delete", args{i: &ipblock2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ipBlockStore.Apply(tt.args.i)
			assert.Nil(t, err)
		})
	}
}
func TestVpcAttachmentStore_Apply(t *testing.T) {
	vpcAttachmentStore := NewVpcAttachmentStore()

	attachment1 := model.VpcAttachment{
		Path:       &fakeVpcAttachmentPath,
		ParentPath: &fakeVpcPath,
	}
	attachment2 := model.VpcAttachment{
		Path:            &fakeVpcAttachmentPath,
		ParentPath:      &fakeVpcPath,
		MarkedForDelete: &fakeDeleted,
	}

	type args struct {
		i interface{}
	}
	tests := []struct {
		name string
		args args
	}{
		{"Add", args{i: &attachment1}},
		{"Delete", args{i: &attachment2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := vpcAttachmentStore.Apply(tt.args.i)
			assert.Nil(t, err)
		})
	}
}
func TestVpcAttachmentStore_GetByKey(t *testing.T) {
	vpcAttachmentStore := NewVpcAttachmentStore()

	attachment := model.VpcAttachment{
		Path:       &fakeVpcAttachmentPath,
		ParentPath: &fakeVpcPath,
	}

	err := vpcAttachmentStore.Apply(&attachment)
	assert.Nil(t, err)

	t.Run("GetByKey", func(t *testing.T) {
		got := vpcAttachmentStore.GetByKey(fakeVpcAttachmentPath)
		assert.NotNil(t, got)
		assert.Equal(t, &attachment, got)
	})
}

func TestVpcAttachmentStore_GetByVpcPath(t *testing.T) {
	vpcAttachmentStore := NewVpcAttachmentStore()

	attachment1 := model.VpcAttachment{
		Path:       &fakeVpcAttachmentPath,
		ParentPath: &fakeVpcPath,
	}
	attachment2 := model.VpcAttachment{
		Path:       common.String(fakeVpcAttachmentPath + "2"),
		ParentPath: &fakeVpcPath,
	}

	err := vpcAttachmentStore.Apply(&attachment1)
	assert.Nil(t, err)
	err = vpcAttachmentStore.Apply(&attachment2)
	assert.Nil(t, err)

	t.Run("GetByVpcPath", func(t *testing.T) {
		got := vpcAttachmentStore.GetByVpcPath(fakeVpcPath)
		assert.NotNil(t, got)
		assert.Equal(t, 2, len(got))
		assert.Contains(t, got, &attachment1)
		assert.Contains(t, got, &attachment2)
	})
}
