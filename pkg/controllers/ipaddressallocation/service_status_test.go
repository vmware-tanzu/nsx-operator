package ipaddressallocation

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestServiceAllocationStatus(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	for _, test := range []struct {
		name          string
		ownerUID      string
		wantCondition bool
	}{
		{name: "owning Service", ownerUID: "svc-uid", wantCondition: true},
		{name: "recreated Service", ownerUID: "old-uid"},
		{name: "no Service owner"},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := &v1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "lb", Namespace: "test", UID: "svc-uid", Generation: 2},
				Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
				Status: v1.ServiceStatus{
					LoadBalancer: v1.LoadBalancerStatus{Ingress: []v1.LoadBalancerIngress{{IP: "192.0.2.1"}}},
					Conditions:   []metav1.Condition{{Type: "DNSReady", Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()}},
				},
			}
			r := &IPAddressAllocationReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(svc).WithObjects(svc).Build()}
			allocation := &v1alpha1.IPAddressAllocation{ObjectMeta: metav1.ObjectMeta{Namespace: svc.Namespace}}
			if test.ownerUID != "" {
				allocation.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Service", Name: svc.Name, UID: types.UID(test.ownerUID)}}
			}
			for _, allocationErr := range []error{errors.New("NSX rejected LB_FRONTEND allocation"), nil} {
				require.NoError(t, r.updateServiceAllocationStatus(ctx, allocation, allocationErr))
				got := &v1.Service{}
				require.NoError(t, r.Client.Get(ctx, client.ObjectKeyFromObject(svc), got))
				condition := meta.FindStatusCondition(got.Status.Conditions, serviceIPAllocatedCondition)
				if test.wantCondition {
					require.NotNil(t, condition)
					require.EqualValues(t, 2, condition.ObservedGeneration)
					if allocationErr != nil {
						require.Equal(t, metav1.ConditionFalse, condition.Status)
						require.Equal(t, allocationErr.Error(), condition.Message)
					} else {
						require.Equal(t, metav1.ConditionTrue, condition.Status)
					}
				} else {
					require.Nil(t, condition)
				}
				require.Equal(t, svc.Status.LoadBalancer, got.Status.LoadBalancer)
				require.NotNil(t, meta.FindStatusCondition(got.Status.Conditions, "DNSReady"))
			}
		})
	}
}
