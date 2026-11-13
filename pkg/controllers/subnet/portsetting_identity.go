package subnet

import (
	"context"
	"fmt"
	"slices"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
)

// NetworkPortSettingAssociation is an explicit original Network -> VDN -> DVPG
// chain supplied by a future migration source, never inferred from NSX names/tags.
type NetworkPortSettingAssociation struct {
	Network types.NamespacedName
	VDNName string
	DVPGID  string
}

// NetworkPortSettingAssociationSource must return complete associations for the
// exact namespace, Subnet source and PortSetting ID. No production source is
// installed yet; a nil source leaves Network-derived naming disabled.
type NetworkPortSettingAssociationSource interface {
	ListNetworkAssociations(ctx context.Context, namespace, associatedResource, portSettingID string) ([]NetworkPortSettingAssociation, error)
}

func generateValidPortSettingName(ps model.VpcSubnetPortSetting) (string, error) {
	if ps.Id == nil || *ps.Id == "" {
		return "", fmt.Errorf("PortSetting is missing its NSX ID")
	}
	if len(validation.IsDNS1123Subdomain(*ps.Id)) == 0 {
		return *ps.Id, nil
	}
	if ps.UniqueId == nil || *ps.UniqueId == "" {
		return "", fmt.Errorf("PortSetting %q has an invalid Kubernetes ID and is missing unique_id", *ps.Id)
	}
	if problems := validation.IsDNS1123Subdomain(*ps.UniqueId); len(problems) != 0 {
		return "", fmt.Errorf("PortSetting %q has an unusable unique_id %q: %v", *ps.Id, *ps.UniqueId, problems)
	}
	return *ps.UniqueId, nil
}

func (r *SubnetReconciler) portSettingName(ctx context.Context, namespace, associatedResource string, ps model.VpcSubnetPortSetting) (string, error) {
	if ps.Id == nil || *ps.Id == "" {
		return "", fmt.Errorf("PortSetting is missing its NSX ID")
	}
	if r.NetworkAssociations != nil {
		associations, err := r.NetworkAssociations.ListNetworkAssociations(ctx, namespace, associatedResource, *ps.Id)
		if err != nil {
			return "", fmt.Errorf("failed to resolve Networks for PortSetting %q: %w", *ps.Id, err)
		}
		names := make([]string, 0, len(associations))
		for _, association := range associations {
			if association.Network.Namespace != namespace || association.VDNName == "" || association.DVPGID == "" ||
				len(validation.IsDNS1123Subdomain(association.Network.Name)) != 0 {
				return "", fmt.Errorf("invalid Network-to-VDN-to-DVPG association for PortSetting %q", *ps.Id)
			}
			names = append(names, association.Network.Name)
		}
		if len(names) != 0 {
			// The original metadata.name is preserved exactly. Sorting is independent
			// of the source's enumeration order and duplicate references.
			slices.Sort(names)
			return names[0], nil
		}
	}
	return generateValidPortSettingName(ps)
}
