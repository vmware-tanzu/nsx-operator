package inventory

import (
	"errors"

	"github.com/vmware/go-vmware-nsxt/containerinventory"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// ApplicationInstanceStore is a store for pod inventory
type ApplicationInstanceStore struct {
	common.ResourceStore
}

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *containerinventory.ContainerApplicationInstance:
		return v.ExternalId, nil
	case *containerinventory.ContainerCluster:
		return v.ExternalId, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func indexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch v := obj.(type) {
	case *containerinventory.ContainerApplicationInstance:
		return []string{v.ExternalId}, nil
	case *containerinventory.ContainerCluster:
		return []string{v.ExternalId}, nil
	default:
		break
	}
	return res, nil
}

type ClusterStore struct {
	common.ResourceStore
}
