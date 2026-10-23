package subnetbinding

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// RestoreBindingPlan describes the final placement of a binding during restore.
type RestoreBindingPlan struct {
	Binding           *v1alpha1.SubnetConnectionBindingMap
	SubnetPath        string
	TargetSubnetPaths []string
}

// ValidateRestoreBindings checks the final topology and returns CR UIDs whose
// old placements must be removed. It does not mutate NSX or the local cache.
func (s *BindingService) ValidateRestoreBindings(plans []RestoreBindingPlan, deleting sets.Set[string]) (sets.Set[string], error) {
	owners := map[string]map[int64]string{}
	planned := sets.New[string]()
	outdated := deleting.Clone()
	parents := sets.New[string]()
	trunks, branches := sets.New[string](), sets.New[string]()
	for _, plan := range plans {
		binding := plan.Binding
		uid := string(binding.UID)
		if uid == "" || deleting.Has(uid) || planned.Has(uid) {
			return nil, fmt.Errorf("invalid or duplicate restore UID %q", uid)
		}
		planned.Insert(uid)
		vlan := binding.Status.VLANTrafficTag
		if binding.Spec.HasVlanTrafficTag() {
			vlan = binding.Spec.VLANTrafficTag
		}
		if vlan == nil || *vlan < 1 || *vlan > 4094 || plan.SubnetPath == "" || len(plan.TargetSubnetPaths) == 0 {
			return nil, fmt.Errorf("invalid restore allocation for %s/%s", binding.Namespace, binding.Name)
		}
		parentPaths := plan.TargetSubnetPaths
		if binding.Spec.IsBranchAssociation() {
			parentPaths = []string{plan.SubnetPath}
			branches.Insert(plan.TargetSubnetPaths...)
		} else {
			branches.Insert(plan.SubnetPath)
		}
		trunks.Insert(parentPaths...)
		for _, parent := range parentPaths {
			if parent == "" {
				return nil, fmt.Errorf("empty parent path for %s", uid)
			}
			parents.Insert(parent)
			if owners[parent] == nil {
				owners[parent] = map[int64]string{}
			}
			if other, exists := owners[parent][*vlan]; exists && other != uid {
				return nil, fmt.Errorf("restore VLAN %d on %s requested by both %s and %s", *vlan, parent, other, uid)
			}
			owners[parent][*vlan] = uid
		}
		// A change of host or target also releases a VLAN on the old trunk,
		// even if the VLAN number itself has not changed.
		targets := sets.New[string](plan.TargetSubnetPaths...)
		existingBMs := s.BindingStore.GetBindingsByBindingMapCRUID(uid)
		if len(existingBMs) > 0 {
			if len(existingBMs) != len(plan.TargetSubnetPaths) {
				outdated.Insert(uid)
			} else {
				for _, bm := range existingBMs {
					if bm.VlanTrafficTag == nil || *bm.VlanTrafficTag != *vlan || bm.ParentPath == nil || *bm.ParentPath != plan.SubnetPath || bm.SubnetPath == nil || !targets.Has(*bm.SubnetPath) {
						outdated.Insert(uid)
						break
					}
				}
			}
		}
	}
	if overlap := trunks.Intersection(branches); overlap.Len() > 0 {
		return nil, fmt.Errorf("restore topology uses subnets as both trunk and branch: %v", overlap.UnsortedList())
	}
	// Include NSX resources outside the operator cache. Planned old placements
	// are movable; all other owners must retain their VLANs.
	for parent := range parents.Union(branches) {
		bindings, err := s.listBindingMapsByParentSubnetPath(parent)
		if err != nil {
			return nil, fmt.Errorf("cannot validate restore VLANs on %s: %w", parent, err)
		}
		bindings = append(bindings, s.GetSubnetConnectionBindingMapsByParentSubnet(parent)...)
		for _, bm := range bindings {
			uid := bindingMapCRUID(bm)
			if planned.Has(uid) || deleting.Has(uid) {
				continue
			}
			if branches.Has(parent) {
				return nil, fmt.Errorf("restore branch %s is a trunk in an unplanned binding", parent)
			}
			if bm.VlanTrafficTag == nil {
				continue
			}
			if owner, conflict := owners[parent][*bm.VlanTrafficTag]; conflict {
				return nil, fmt.Errorf("restore VLAN %d on %s for %s is occupied by an unplanned binding", *bm.VlanTrafficTag, parent, owner)
			}
		}
	}
	for trunk := range trunks {
		bindings, err := s.listBindingMapsByChildSubnetPath(trunk)
		if err != nil {
			return nil, fmt.Errorf("cannot validate restore topology on %s: %w", trunk, err)
		}
		bindings = append(bindings, s.GetSubnetConnectionBindingMapsByChildSubnet(trunk)...)
		for _, bm := range bindings {
			uid := bindingMapCRUID(bm)
			if !planned.Has(uid) && !deleting.Has(uid) {
				return nil, fmt.Errorf("restore trunk %s is a branch in an unplanned binding", trunk)
			}
		}
	}
	return outdated, nil
}
