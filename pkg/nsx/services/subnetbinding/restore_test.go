package subnetbinding

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestValidateRestoreBindings(t *testing.T) {
	for _, name := range []string{"target swap", "VLAN swap", "terminating owner", "unplanned conflict", "desired conflict", "unplanned trunk role", "unplanned branch role", "query failure", "unchanged", "branch association", "branch conflict"} {
		t.Run(name, func(t *testing.T) {
			svc := &BindingService{BindingStore: SetupStore()}
			plan := func(uid, parent string, vlan int64) RestoreBindingPlan {
				return RestoreBindingPlan{
					Binding: &v1alpha1.SubnetConnectionBindingMap{
						ObjectMeta: metav1.ObjectMeta{Name: uid, UID: types.UID(uid)},
						Status:     v1alpha1.SubnetConnectionBindingMapStatus{VLANTrafficTag: Int64(vlan)},
					},
					SubnetPath:        "/child-" + uid,
					TargetSubnetPaths: []string{parent},
				}
			}
			add := func(uid, parent string, vlan int64) {
				require.NoError(t, svc.BindingStore.Apply(&model.SubnetConnectionBindingMap{
					Id:             String(uid),
					Path:           String("/orgs/default/projects/default/vpcs/vpc-1/subnets/child/subnet-connection-binding-maps/" + uid),
					ParentPath:     String("/child-" + uid),
					SubnetPath:     String(parent),
					VlanTrafficTag: Int64(vlan),
					Tags:           []model.Tag{{Scope: String(common.TagScopeSubnetBindingCRUID), Tag: String(uid)}},
				}))
			}
			deleting := sets.New[string]()
			expectedDelete := sets.New[string]()
			plans := []RestoreBindingPlan{plan("a", "/parent-1", 1)}
			wantErr := false
			switch name {
			case "branch association", "branch conflict":
				plans[0].Binding.Spec.SubnetAssociation = v1alpha1.SubnetAssociationBranch
				if name == "branch conflict" {
					add("external", plans[0].SubnetPath, 1)
					// Retain an existing resource to prove failure is non-destructive.
					add("a", "/old-parent", 2)
					wantErr = true
				}
			case "target swap":
				add("a", "/parent-2", 1)
				add("b", "/parent-1", 1)
				plans = append(plans, plan("b", "/parent-2", 1))
				expectedDelete.Insert("a", "b")
			case "VLAN swap":
				add("a", "/parent-1", 2)
				add("b", "/parent-1", 1)
				plans = append(plans, plan("b", "/parent-1", 2))
				expectedDelete.Insert("a", "b")
			case "terminating owner":
				add("old", "/parent-1", 1)
				deleting.Insert("old")
				expectedDelete.Insert("old")
			case "unplanned conflict":
				add("a", "/parent-1", 2)
				add("external", "/parent-1", 1)
				wantErr = true
			case "desired conflict":
				add("a", "/parent-1", 2)
				plans = append(plans, plan("b", "/parent-1", 1))
				wantErr = true
			case "unplanned trunk role":
				add("a", "/parent-1", 2)
				add("external", "/child-a", 3)
				wantErr = true
			case "unplanned branch role":
				add("a", "/parent-1", 2)
				require.NoError(t, svc.BindingStore.Apply(&model.SubnetConnectionBindingMap{
					Id:         String("external"),
					Path:       String("/orgs/default/projects/default/vpcs/vpc-1/subnets/parent/subnet-connection-binding-maps/external"),
					ParentPath: String("/parent-1"), SubnetPath: String("/elsewhere"), VlanTrafficTag: Int64(3),
				}))
				wantErr = true
			case "query failure":
				add("a", "/parent-1", 2)
				wantErr = true
			case "unchanged":
				add("a", "/parent-1", 1)
			}
			patches := gomonkey.ApplyPrivateMethod(reflect.TypeOf(svc), "listBindingMapsByParentSubnetPath",
				func(s *BindingService, parent string) ([]*model.SubnetConnectionBindingMap, error) {
					if name == "query failure" {
						return nil, fmt.Errorf("NSX unavailable")
					}
					return s.GetSubnetConnectionBindingMapsByParentSubnet(parent), nil
				})
			patches.ApplyPrivateMethod(reflect.TypeOf(svc), "listBindingMapsByChildSubnetPath",
				func(s *BindingService, child string) ([]*model.SubnetConnectionBindingMap, error) {
					return s.GetSubnetConnectionBindingMapsByChildSubnet(child), nil
				})
			defer patches.Reset()
			deleted := false
			patches.ApplyMethod(reflect.TypeOf(svc), "DeleteMultiSubnetConnectionBindingMapsByCRs",
				func(s *BindingService, uids sets.Set[string]) error {
					deleted = true
					assert.Equal(t, expectedDelete, uids)
					for uid := range uids {
						s.BindingStore.DeleteMultipleObjects(s.BindingStore.GetBindingsByBindingMapCRUID(uid))
					}
					return nil
				})
			outdated, err := svc.ValidateRestoreBindings(plans, deleting)
			assert.False(t, deleted, "validation must not mutate resources")
			if wantErr {
				require.Error(t, err)
				assert.False(t, deleted, "preflight failure must preserve existing resources")
				assert.Len(t, svc.BindingStore.GetBindingsByBindingMapCRUID("a"), 1)
				return
			}
			require.NoError(t, err)
			require.NoError(t, svc.DeleteMultiSubnetConnectionBindingMapsByCRs(outdated))
			svc.SetRestoreMode(true)
			for _, p := range plans {
				parents := p.TargetSubnetPaths
				if p.Binding.Spec.IsBranchAssociation() {
					parents = []string{p.SubnetPath}
				}
				got, err := svc.ReconcileVlanTrafficTag(p.Binding, -1, parents, false)
				require.NoError(t, err)
				assert.Equal(t, *p.Binding.Status.VLANTrafficTag, got)
			}
		})
	}
}
