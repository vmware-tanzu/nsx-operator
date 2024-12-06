package subnetbinding

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

func TestPredicateFuncsSubnets(t *testing.T) {
	name := "net1"
	namespace := "default"
	net1 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 64,
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
	net2 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 128,
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
	net3 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetSpec{
			IPv4SubnetSize: 64,
		},
		Status: v1alpha1.SubnetStatus{
			Conditions: []v1alpha1.Condition{
				{
					Type:    v1alpha1.Ready,
					Status:  corev1.ConditionFalse,
					Message: "old message",
					Reason:  "crNotFound",
				},
			},
		},
	}
	createEvent := event.CreateEvent{Object: net1}
	updateEvent1 := event.UpdateEvent{ObjectOld: net1, ObjectNew: net2}
	updateEvent2 := event.UpdateEvent{ObjectOld: net1, ObjectNew: net3}
	updateEvent3 := event.UpdateEvent{ObjectOld: net3, ObjectNew: net1}
	deleteEvent := event.DeleteEvent{Object: net1}
	genericEvent := event.GenericEvent{Object: net1}
	assert.False(t, PredicateFuncsForSubnets.CreateFunc(createEvent))
	assert.False(t, PredicateFuncsForSubnets.Update(updateEvent1))
	assert.True(t, PredicateFuncsForSubnets.Update(updateEvent2))
	assert.True(t, PredicateFuncsForSubnets.Update(updateEvent3))
	assert.False(t, PredicateFuncsForSubnets.Delete(deleteEvent))
	assert.False(t, PredicateFuncsForSubnets.GenericFunc(genericEvent))
}

func TestPredicateFuncsSubnetSets(t *testing.T) {
	name := "net1"
	namespace := "default"
	net1 := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetSetSpec{
			IPv4SubnetSize: 64,
		},
		Status: v1alpha1.SubnetSetStatus{
			Subnets: []v1alpha1.SubnetInfo{},
		},
	}
	net2 := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetSetSpec{
			IPv4SubnetSize: 128,
		},
		Status: v1alpha1.SubnetSetStatus{
			Subnets: []v1alpha1.SubnetInfo{},
		},
	}
	net3 := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.SubnetSetSpec{
			IPv4SubnetSize: 64,
		},
		Status: v1alpha1.SubnetSetStatus{
			Subnets: []v1alpha1.SubnetInfo{
				{
					NetworkAddresses: []string{
						"192.168.26.0/24",
					},
				},
			},
		},
	}
	createEvent := event.CreateEvent{Object: net1}
	updateEvent1 := event.UpdateEvent{ObjectOld: net1, ObjectNew: net2}
	updateEvent2 := event.UpdateEvent{ObjectOld: net1, ObjectNew: net3}
	deleteEvent := event.DeleteEvent{Object: net1}
	genericEvent := event.GenericEvent{Object: net1}
	assert.False(t, PredicateFuncsForSubnetSets.CreateFunc(createEvent))
	assert.False(t, PredicateFuncsForSubnetSets.Update(updateEvent1))
	assert.True(t, PredicateFuncsForSubnetSets.Update(updateEvent2))
	assert.False(t, PredicateFuncsForSubnetSets.Delete(deleteEvent))
	assert.False(t, PredicateFuncsForSubnetSets.GenericFunc(genericEvent))
}

func TestRequeueSubnetConnectionBindingMapsBySubnet(t *testing.T) {
	myQueue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer myQueue.ShutDown()

	ctx := context.TODO()
	crName := "binding1"
	crNS := "default"
	bm1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "binding-uuid",
			Namespace: crNS,
			Name:      crName,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "child",
			TargetSubnetName: "parent",
			VLANTrafficTag:   101,
		},
	}
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      crName,
			Namespace: crNS,
		},
	}
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(bm1).Build()

	subnet2 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "parent", Namespace: crNS},
	}
	subnet3 := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "child2", Namespace: crNS},
	}

	requeueBindingMapsBySubnetUpdate(ctx, fakeClient, subnet2, subnet2, myQueue)
	require.Equal(t, 1, myQueue.Len())
	item, _ := myQueue.Get()
	assert.Equal(t, req, item)
	myQueue.Done(item)

	requeueBindingMapsBySubnetUpdate(ctx, fakeClient, subnet3, subnet3, myQueue)
	require.Equal(t, 0, myQueue.Len())
}

func TestRequeueSubnetConnectionBindingMapsBySubnetSet(t *testing.T) {
	myQueue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer myQueue.ShutDown()

	ctx := context.TODO()
	crName := "binding1"
	crNS := "default"
	bm1 := &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "binding-uuid",
			Namespace: crNS,
			Name:      crName,
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child",
			TargetSubnetSetName: "parent",
			VLANTrafficTag:      101,
		},
	}
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(bm1).Build()

	subnetSet1 := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: crNS},
	}
	subnetSet2 := &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{Name: "parent", Namespace: crNS},
	}

	requeueBindingMapsBySubnetSetUpdate(ctx, fakeClient, subnetSet1, subnetSet1, myQueue)
	require.Equal(t, 0, myQueue.Len())
	requeueBindingMapsBySubnetSetUpdate(ctx, fakeClient, subnetSet2, subnetSet2, myQueue)
	require.Equal(t, 1, myQueue.Len())

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      crName,
			Namespace: crNS,
		},
	}
	item, _ := myQueue.Get()
	assert.Equal(t, req, item)
	myQueue.Done(item)
}
