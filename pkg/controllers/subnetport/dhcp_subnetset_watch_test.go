package subnetport

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSubnetSetEventsEnqueueDependentPodPorts(t *testing.T) {
	base := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{Name: "default-pod", Namespace: "ns", Generation: 1,
			Labels: map[string]string{servicecommon.LabelDefaultNetwork: servicecommon.DefaultPodNetwork}},
		Spec: v1alpha1.SubnetSetSpec{SubnetNames: &[]string{"vlan-a"}},
	}
	for _, tc := range []struct {
		name   string
		event  string
		change func(old, current *v1alpha1.SubnetSet)
		want   []string
	}{
		{name: "create default set", event: "create", want: []string{"by-set", "by-old-subnet"}},
		{name: "delete default set", event: "delete", want: []string{"by-set", "by-old-subnet"}},
		{name: "remove default label without changing generation", event: "update", change: func(_, current *v1alpha1.SubnetSet) {
			current.Labels = nil
		}, want: []string{"by-set", "by-old-subnet"}},
		{name: "add default label without changing generation", event: "update", change: func(old, _ *v1alpha1.SubnetSet) {
			old.Labels = nil
		}, want: []string{"by-set", "by-old-subnet"}},
		{name: "change legacy default label", event: "update", change: func(old, current *v1alpha1.SubnetSet) {
			old.Labels = map[string]string{servicecommon.LabelDefaultSubnetSet: servicecommon.LabelDefaultPodSubnetSet}
			current.Labels = nil
		}, want: []string{"by-set"}},
		{name: "replace members includes old and new ports", event: "update", change: func(_, current *v1alpha1.SubnetSet) {
			current.Generation++
			current.Spec.SubnetNames = &[]string{"vlan-b"}
		}, want: []string{"by-set", "by-old-subnet", "by-new-subnet"}},
		{name: "remove all members", event: "update", change: func(_, current *v1alpha1.SubnetSet) {
			current.Generation++
			current.Spec.SubnetNames = nil
		}, want: []string{"by-set", "by-old-subnet"}},
		{name: "create non-default referenced set", event: "create", change: func(_, current *v1alpha1.SubnetSet) {
			current.Labels = nil
		}, want: []string{"by-set"}},
		{name: "status-only update", event: "update", change: func(_, current *v1alpha1.SubnetSet) {
			current.Status.Conditions = []v1alpha1.Condition{{Type: v1alpha1.Ready, Status: v1.ConditionTrue}}
		}},
		{name: "unchanged update", event: "update"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			var objects []client.Object
			for _, fixture := range []struct {
				name, subnetSet, subnet, namespace string
				pod                                bool
			}{
				{"by-set", "default-pod", "", "ns", true},
				{"by-old-subnet", "", "vlan-a", "ns", true},
				{"by-new-subnet", "", "vlan-b", "ns", true},
				{"unrelated-set", "secondary", "", "ns", true},
				{"unrelated-subnet", "", "vlan-c", "ns", true},
				{"vm-by-set", "default-pod", "", "ns", false},
				{"vm-by-subnet", "", "vlan-a", "ns", false},
				{"other-namespace", "default-pod", "", "other", true},
			} {
				sp := defaultPodPort()
				sp.Name, sp.Namespace = fixture.name, fixture.namespace
				sp.Spec.SubnetSet, sp.Spec.Subnet = fixture.subnetSet, fixture.subnet
				if !fixture.pod {
					sp.OwnerReferences = nil
				}
				objects = append(objects, sp)
			}
			r := &SubnetPortReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()}
			old, current := base.DeepCopy(), base.DeepCopy()
			if tc.change != nil {
				tc.change(old, current)
			}
			queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			defer queue.ShutDown()
			h := handler.EnqueueRequestsFromMapFunc(r.subnetSetMapFunc)
			ctx := context.Background()
			switch tc.event {
			case "create":
				e := event.CreateEvent{Object: current}
				if subnetSetPredicate.Create(e) {
					h.Create(ctx, e, queue)
				}
			case "delete":
				e := event.DeleteEvent{Object: old}
				if subnetSetPredicate.Delete(e) {
					h.Delete(ctx, e, queue)
				}
			case "update":
				e := event.UpdateEvent{ObjectOld: old, ObjectNew: current}
				if subnetSetPredicate.Update(e) {
					h.Update(ctx, e, queue)
				}
			}
			var names []string
			for queue.Len() > 0 {
				req, shutdown := queue.Get()
				require.False(t, shutdown)
				require.Equal(t, "ns", req.Namespace)
				names = append(names, req.Name)
				queue.Done(req)
			}
			require.ElementsMatch(t, tc.want, names)
		})
	}
}
