package subnetset

import (
	"context"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"

	"sigs.k8s.io/controller-runtime/pkg/predicate"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
)

// VPCHandler handles VPC event for SubnetSet:
// - VPC creation: create default SubnetSet for the VPC.
// - VPC deletion: delete all SubnetSets under the VPC.

var defaultSubnetSets = map[string]string{
	"default-vm-subnetset":  common.LabelDefaultVMSubnet,
	"default-pod-subnetset": common.LabelDefaultPodSubnetSet,
}

type VPCHandler struct {
	Client client.Client
}

func (h *VPCHandler) Create(e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	ns := e.Object.GetNamespace()
	log.Info("creating default Subnetset for VPC", "Namespace", ns, "Name", e.Object.GetName())
	for name, subnetSetType := range defaultSubnetSets {
		if err := retry.OnError(retry.DefaultRetry, func(err error) bool {
			return err != nil
		}, func() error {
			list := &v1alpha1.SubnetSetList{}
			label := client.MatchingLabels{
				common.LabelDefaultSubnetSet: subnetSetType,
			}
			nsOption := client.InNamespace(ns)
			if err := h.Client.List(context.Background(), list, label, nsOption); err != nil {
				return err
			}
			if len(list.Items) > 0 {
				// avoid creating when nsx-operator restarted if Subnetset exists.
				log.Info("default subnetset already exists", common.LabelDefaultSubnetSet, subnetSetType)
				return nil
			}
			obj := &v1alpha1.SubnetSet{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns,
					Name:      name,
					Labels: map[string]string{
						common.LabelDefaultSubnetSet: subnetSetType,
					},
				},
				Spec: v1alpha1.SubnetSetSpec{
					AdvancedConfig: v1alpha1.AdvancedConfig{
						StaticIPAllocation: v1alpha1.StaticIPAllocation{
							Enable: true,
						},
					},
				},
			}
			if err := h.Client.Create(context.Background(), obj); err != nil {
				return err
			}
			return nil
		}); err != nil {
			log.Error(err, "failed to create SubnetSet", "Namespace", ns, "Name", name)
		}
	}
}

func (h *VPCHandler) Delete(e event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	log.Info("cleaning default Subnetset for VPC", "Name", e.Object.GetName())
	for _, subnetSetType := range defaultSubnetSets {
		if err := retry.OnError(retry.DefaultRetry, func(err error) bool {
			return err != nil
		}, func() error {
			label := client.MatchingLabels{
				common.LabelDefaultSubnetSet: subnetSetType,
			}
			nsOption := client.InNamespace(e.Object.GetNamespace())
			obj := &v1alpha1.SubnetSet{}
			if err := h.Client.DeleteAllOf(context.Background(), obj, label, nsOption); err != nil {
				return client.IgnoreNotFound(err)
			}
			return nil
		}); err != nil {
			log.Error(err, "failed to delete SubnetSet", common.LabelDefaultSubnetSet, subnetSetType)
		}
	}
}

func (h *VPCHandler) Generic(_ event.GenericEvent, _ workqueue.RateLimitingInterface) {
	log.V(4).Info("VPC generic event, do nothing")
}

func (h *VPCHandler) Update(_ event.UpdateEvent, _ workqueue.RateLimitingInterface) {
	log.V(4).Info("VPC update event, do nothing")
}

var VPCPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return false
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return true
	},
	GenericFunc: func(genericEvent event.GenericEvent) bool {
		return false
	},
}
