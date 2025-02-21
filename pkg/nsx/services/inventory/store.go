package inventory

import (
	"errors"

	"github.com/vmware/go-vmware-nsxt/containerinventory"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// PodInventoryStore is a store for pod inventory
type ApplicationInstanceStore struct {
	common.ResourceStore
}

// keyFunc is used to get the key of a resource, usually, which is the ID of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *containerinventory.ContainerApplicationInstance:
		return v.ContainerApplicationIds[0], nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

// indexFunc is used to get index of a resource, usually, which is the UID of the CR controller reconciles,
// index is used to filter out resources which are related to the CR
func indexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch v := obj.(type) {
	case *containerinventory.ContainerApplicationInstance:
		return v.ContainerApplicationIds, nil
	default:
		break
	}
	return res, nil
}
