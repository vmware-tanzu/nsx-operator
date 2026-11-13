package ipaddressallocation

import (
	"context"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

const serviceIPAllocatedCondition = "LoadBalancerIPAllocated"

// updateServiceAllocationStatus reports allocation results to the owning Service.
func (r *IPAddressAllocationReconciler) updateServiceAllocationStatus(ctx context.Context, allocation *v1alpha1.IPAddressAllocation, allocationErr error) error {
	for _, owner := range allocation.OwnerReferences {
		if owner.APIVersion != "v1" || owner.Kind != "Service" {
			continue
		}
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			svc := &v1.Service{}
			if err := r.Client.Get(ctx, types.NamespacedName{Namespace: allocation.Namespace, Name: owner.Name}, svc); err != nil {
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}
			if svc.UID != owner.UID || svc.Spec.Type != v1.ServiceTypeLoadBalancer || !svc.DeletionTimestamp.IsZero() {
				return nil
			}
			condition := metav1.Condition{
				Type: serviceIPAllocatedCondition, Status: metav1.ConditionTrue,
				Reason: "IPAddressAllocationReady", Message: "LoadBalancer IP allocation succeeded",
				ObservedGeneration: svc.Generation,
			}
			if allocationErr != nil {
				condition.Status = metav1.ConditionFalse
				condition.Reason = "IPAddressAllocationFailed"
				condition.Message = allocationErr.Error()
			}
			if !meta.SetStatusCondition(&svc.Status.Conditions, condition) {
				return nil
			}
			return r.Client.Status().Update(ctx, svc)
		}); err != nil {
			return err
		}
	}
	return nil
}
