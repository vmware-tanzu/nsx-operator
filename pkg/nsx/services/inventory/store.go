package inventory

import (
	"errors"

	"github.com/vmware/go-vmware-nsxt/containerinventory"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

type ApplicationInstanceStore struct {
	common.ResourceStore
}
type ApplicationStore struct {
	common.ResourceStore
}
type ProjectStore struct {
	common.ResourceStore
}
type ClusterNodeStore struct {
	common.ResourceStore
}
type NetworkPolicyStore struct {
	common.ResourceStore
}
type IngressPolicyStore struct {
	common.ResourceStore
}
type ClusterStore struct {
	common.ResourceStore
}

func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *containerinventory.ContainerApplicationInstance:
		return v.ExternalId, nil
	case *containerinventory.ContainerCluster:
		return v.ExternalId, nil
	case *containerinventory.ContainerApplication:
		return v.ExternalId, nil
	case *containerinventory.ContainerProject:
		return v.ExternalId, nil
	case *containerinventory.ContainerClusterNode:
		return v.ExternalId, nil
	case *containerinventory.ContainerNetworkPolicy:
		return v.ExternalId, nil
	case *containerinventory.ContainerIngressPolicy:
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
	case *containerinventory.ContainerApplication:
		return []string{v.ExternalId}, nil
	case *containerinventory.ContainerProject:
		return []string{v.ExternalId}, nil
	case *containerinventory.ContainerClusterNode:
		return []string{v.ExternalId}, nil
	case *containerinventory.ContainerNetworkPolicy:
		return []string{v.ExternalId}, nil
	case *containerinventory.ContainerIngressPolicy:
		return []string{v.ExternalId}, nil
	default:
		break
	}
	return res, nil
}
