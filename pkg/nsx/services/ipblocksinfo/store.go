package ipblocksinfo

import (
	"fmt"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

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

type VPCStore struct {
	common.ResourceStore
}

func (vs *VPCStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	vpc := i.(*model.Vpc)
	if vpc.MarkedForDelete != nil && *vpc.MarkedForDelete {
		err := vs.Delete(vpc)
		log.V(1).Info("delete VPC from store", "VPC", vpc)
		if err != nil {
			return err
		}
	} else {
		err := vs.Add(vpc)
		log.V(1).Info("add VPC to store", "VPC", vpc)
		if err != nil {
			return err
		}
	}
	return nil
}
