package subnetipreservation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestPredicateFuncsForSubnets(t *testing.T) {
	readySubnet1 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: "ns-1",
		},
		Status: v1alpha1.SubnetStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	readySubnet2 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 32,
		},
		Status: v1alpha1.SubnetStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	unreadySubnet := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: "ns-1",
		},
		Status: v1alpha1.SubnetStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:    v1alpha1.Ready,
					Status:  corev1.ConditionFalse,
					Message: "NSX Subnet creation failed",
					Reason:  "Failed to create NSX Subnet",
				},
			},
		},
	}

	createEvent := event.CreateEvent{
		Object: readySubnet1,
	}
	assert.False(t, PredicateFuncsForSubnets.CreateFunc(createEvent))
	updateEvent1 := event.UpdateEvent{
		ObjectOld: unreadySubnet,
		ObjectNew: readySubnet1,
	}
	assert.True(t, PredicateFuncsForSubnets.Update(updateEvent1))
	updateEvent2 := event.UpdateEvent{
		ObjectOld: readySubnet1,
		ObjectNew: readySubnet2,
	}
	assert.False(t, PredicateFuncsForSubnets.Update(updateEvent2))
	deleteEvent := event.DeleteEvent{
		Object: readySubnet1,
	}
	assert.False(t, PredicateFuncsForSubnets.Delete(deleteEvent))
	genericEvent := event.GenericEvent{
		Object: readySubnet1,
	}
	assert.False(t, PredicateFuncsForSubnets.GenericFunc(genericEvent))
}

func TestRequeueIPReservationBySubnet(t *testing.T) {
	myQueue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer myQueue.ShutDown()

	ipr := &v1alpha1.SubnetIPReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ipr-1",
			Namespace: "ns-1",
		},
		Spec: v1alpha1.SubnetIPReservationSpec{
			Subnet:      "subnet-1",
			NumberOfIPs: 10,
		},
	}
	subnet1 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnet-1",
			Namespace: "ns-1",
		},
	}
	subnet2 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "subnet-2",
			Namespace: "ns-1",
		},
	}

	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().
		WithScheme(newScheme).
		WithObjects(ipr).
		WithIndex(&v1alpha1.SubnetIPReservation{}, "spec.subnet", subnetIPReservationSubnetNameIndexFunc).
		Build()

	ctx := context.TODO()
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "ipr-1",
			Namespace: "ns-1",
		},
	}
	requeueIPReservationBySubnet(ctx, fakeClient, subnet1, subnet1, myQueue)
	require.Equal(t, 1, myQueue.Len())
	item, _ := myQueue.Get()
	assert.Equal(t, req, item)
	myQueue.Done(item)

	requeueIPReservationBySubnet(ctx, fakeClient, subnet2, subnet2, myQueue)
	require.Equal(t, 0, myQueue.Len())
}
