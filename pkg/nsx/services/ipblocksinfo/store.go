package ipblocksinfo

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// keyFunc is used to get the key of a resource, usually, which is the Path of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.VpcConnectivityProfile:
		return *v.Path, nil
	case *model.Vpc:
		return *v.Path, nil
	case *model.IpAddressBlock:
		return *v.Path, nil
	case *model.VpcAttachment:
		return *v.Path, nil
	default:
		return "", fmt.Errorf("keyFunc doesn't support unknown type %s", v)
	}
}

type VPCConnectivityProfileStore struct {
	common.ResourceStore
}

func (vs *VPCConnectivityProfileStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	vpcProfile := i.(*model.VpcConnectivityProfile)
	if vpcProfile.MarkedForDelete != nil && *vpcProfile.MarkedForDelete {
		err := vs.Delete(vpcProfile)
		log.V(1).Info("delete VpcConnectivityProfile from store", "VpcConnectivityProfile", vpcProfile)
		if err != nil {
			return err
		}
	} else {
		err := vs.Add(vpcProfile)
		log.V(1).Info("add VpcConnectivityProfile to store", "VpcConnectivityProfile", vpcProfile)
		if err != nil {
			return err
		}
	}
	return nil
}

type IPBlockStore struct {
	common.ResourceStore
}

func (is *IPBlockStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	ipblock := i.(*model.IpAddressBlock)
	if ipblock.MarkedForDelete != nil && *ipblock.MarkedForDelete {
		err := is.Delete(ipblock)
		log.V(1).Info("delete ipblock from store", "IPBlock", ipblock)
		if err != nil {
			return err
		}
	} else {
		err := is.Add(ipblock)
		log.V(1).Info("add IPBlock to store", "IPBlock", ipblock)
		if err != nil {
			return err
		}
	}
	return nil
}

type VpcAttachmentStore struct {
	common.ResourceStore
}

func NewVpcAttachmentStore() *VpcAttachmentStore {
	return &VpcAttachmentStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{}),
		BindingType: model.VpcAttachmentBindingType(),
	}}
}

func (vas *VpcAttachmentStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	attachment := i.(*model.VpcAttachment)
	if attachment.MarkedForDelete != nil && *attachment.MarkedForDelete {
		err := vas.Delete(attachment)
		log.V(1).Info("delete VPC attachment from store", "VpcAttachment", attachment)
		if err != nil {
			return err
		}
	} else {
		err := vas.Add(attachment)
		log.V(1).Info("add VPC attachment to store", "VpcAttachment", attachment)
		if err != nil {
			return err
		}
	}
	return nil
}

func (vas *VpcAttachmentStore) GetByKey(path string) *model.VpcAttachment {
	obj := vas.ResourceStore.GetByKey(path)
	if obj != nil {
		attachment := obj.(*model.VpcAttachment)
		return attachment
	}
	return nil
}

func (vas *VpcAttachmentStore) GetByVpcPath(vpcPath string) []*model.VpcAttachment {
	result := []*model.VpcAttachment{}
	attachments := vas.ResourceStore.List()
	for _, item := range attachments {
		attachment := item.(*model.VpcAttachment)
		if attachment != nil && *attachment.ParentPath == vpcPath {
			result = append(result, attachment)
		}
	}
	return result
}
