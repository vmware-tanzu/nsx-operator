package common

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestEnqueueRequestForBindingMap(t *testing.T) {
	myQueue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer myQueue.ShutDown()

	requeueByCreate := func(ctx context.Context, _ client.Client, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: "create", Namespace: "default"}})
	}
	requeueByUpdate := func(ctx context.Context, _ client.Client, _, _ client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: "update", Namespace: "default"}})
	}
	requeueByDelete := func(ctx context.Context, _ client.Client, obj client.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: "delete", Namespace: "default"}})
	}

	obj1 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test",
		},
	}
	obj2 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test",
			Annotations: map[string]string{
				"update": "true",
			},
		},
	}
	enqueueRequest := &EnqueueRequestForDependency{
		ResourceType:    "fakeObject",
		RequeueByCreate: requeueByCreate,
		RequeueByDelete: requeueByDelete,
		RequeueByUpdate: requeueByUpdate,
	}
	createEvent := event.CreateEvent{
		Object: obj1,
	}
	updateEvent := event.UpdateEvent{
		ObjectOld: obj1,
		ObjectNew: obj2,
	}
	deleteEvent := event.DeleteEvent{
		Object: obj1,
	}
	genericEvent := event.GenericEvent{
		Object: obj1,
	}

	ctx := context.Background()
	enqueueRequest.Create(ctx, createEvent, myQueue)
	require.Equal(t, 1, myQueue.Len())
	item, _ := myQueue.Get()
	assert.Equal(t, "create", item.Name)
	myQueue.Done(item)

	enqueueRequest.Update(ctx, updateEvent, myQueue)
	require.Equal(t, 1, myQueue.Len())
	item, _ = myQueue.Get()
	assert.Equal(t, "update", item.Name)
	myQueue.Done(item)

	enqueueRequest.Delete(ctx, deleteEvent, myQueue)
	require.Equal(t, 1, myQueue.Len())
	item, _ = myQueue.Get()
	assert.Equal(t, "delete", item.Name)
	myQueue.Done(item)

	enqueueRequest.Generic(ctx, genericEvent, myQueue)
	require.Equal(t, 0, myQueue.Len())
}

func TestPredicateFuncsBindingMap(t *testing.T) {
	readyBM := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bm1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "child",
			TargetSubnetName: "parent1",
			VLANTrafficTag:   202,
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	unreadyBM := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bm1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "child",
			TargetSubnetName: "parent1",
			VLANTrafficTag:   201,
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionFalse,
				},
			},
		},
	}
	bmWithSubnet1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bm1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "child",
			TargetSubnetName: "parent1",
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	bmWithSubnet2 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bm1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "child",
			TargetSubnetName: "parent2",
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	bmWithSubnetSet1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bm1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			TargetSubnetSetName: "parent1",
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	bmWithSubnetSet2 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bm1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			TargetSubnetSetName: "parent2",
		},
		Status: v1alpha1.SubnetConnectionBindingMapStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:   v1alpha1.Ready,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	createEvent := event.CreateEvent{
		Object: readyBM,
	}
	assert.True(t, PredicateFuncsWithSubnetBindings.CreateFunc(createEvent))

	updateEventUnReadyToReady := event.UpdateEvent{
		ObjectOld: unreadyBM,
		ObjectNew: readyBM,
	}
	assert.False(t, PredicateFuncsWithSubnetBindings.Update(updateEventUnReadyToReady))
	updateEventTargetSubnetToTargetSubnetSet := event.UpdateEvent{
		ObjectOld: bmWithSubnet1,
		ObjectNew: bmWithSubnetSet1,
	}
	assert.True(t, PredicateFuncsWithSubnetBindings.Update(updateEventTargetSubnetToTargetSubnetSet))
	updateEventTargetSubnetSetToTargetSubnet := event.UpdateEvent{
		ObjectOld: bmWithSubnetSet1,
		ObjectNew: bmWithSubnet1,
	}
	assert.True(t, PredicateFuncsWithSubnetBindings.Update(updateEventTargetSubnetSetToTargetSubnet))
	updateEventTargetSubnetChange := event.UpdateEvent{
		ObjectOld: bmWithSubnet1,
		ObjectNew: bmWithSubnet2,
	}
	assert.True(t, PredicateFuncsWithSubnetBindings.Update(updateEventTargetSubnetChange))
	updateEventTargetSubnetSetChange := event.UpdateEvent{
		ObjectOld: bmWithSubnetSet1,
		ObjectNew: bmWithSubnetSet2,
	}
	assert.True(t, PredicateFuncsWithSubnetBindings.Update(updateEventTargetSubnetSetChange))
	deleteEvent := event.DeleteEvent{
		Object: readyBM,
	}
	assert.True(t, PredicateFuncsWithSubnetBindings.Delete(deleteEvent))
	genericEvent := event.GenericEvent{
		Object: readyBM,
	}
	assert.False(t, PredicateFuncsWithSubnetBindings.GenericFunc(genericEvent))
}

func TestIsObjectUpdateToReady(t *testing.T) {
	unreadyConditions := []v1alpha1.Condition{
		{
			Status: corev1.ConditionFalse,
			Type:   v1alpha1.Ready,
		},
	}
	readyConditions := []v1alpha1.Condition{
		{
			Status: corev1.ConditionTrue,
			Type:   v1alpha1.Ready,
		},
	}
	assert.True(t, IsObjectUpdateToReady(unreadyConditions, readyConditions))
	assert.False(t, IsObjectUpdateToReady(readyConditions, readyConditions))
	assert.False(t, IsObjectUpdateToReady(readyConditions, unreadyConditions))
	assert.True(t, IsObjectUpdateToUnready(readyConditions, unreadyConditions))
	assert.False(t, IsObjectUpdateToUnready(unreadyConditions, unreadyConditions))
	assert.False(t, IsObjectUpdateToUnready(readyConditions, readyConditions))
}
