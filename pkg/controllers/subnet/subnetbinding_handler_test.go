package subnet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

var (
	bm1 = &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "child1",
			TargetSubnetName: "parent",
			VLANTrafficTag:   101,
		},
	}

	bm2 = &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "child1",
			TargetSubnetName: "parent2",
			VLANTrafficTag:   102,
		},
	}

	bm3 = &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:          "child1",
			TargetSubnetSetName: "parentSet2",
			VLANTrafficTag:      101,
		},
	}

	bm4 = &v1alpha1.SubnetConnectionBindingMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding1",
			Namespace: "default",
		},
		Spec: v1alpha1.SubnetConnectionBindingMapSpec{
			SubnetName:       "child2",
			TargetSubnetName: "parent3",
			VLANTrafficTag:   101,
		},
	}

	subnet1 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "child1", Namespace: "default"},
	}
	subnet2 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "parent", Namespace: "default"},
	}
	subnet3 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "child2", Namespace: "default"},
	}
	subnet4 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "parent2", Namespace: "default"},
	}
	req1 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "child1",
			Namespace: "default",
		},
	}
	req2 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "parent",
			Namespace: "default",
		},
	}
	req3 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "child2",
			Namespace: "default",
		},
	}
	req4 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "parent2",
			Namespace: "default",
		},
	}
)

func TestRequeueSubnetBySubnetBinding(t *testing.T) {
	myQueue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer myQueue.ShutDown()

	ctx := context.TODO()
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(subnet1, subnet2, subnet3, subnet4).Build()

	requeueSubnetBySubnetBindingCreate(ctx, fakeClient, bm1, myQueue)
	require.Equal(t, 2, myQueue.Len())
	queueItemEquals(t, myQueue, req1)
	queueItemEquals(t, myQueue, req2)

	requeueSubnetBySubnetBindingCreate(ctx, fakeClient, bm4, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req3)

	requeueSubnetBySubnetBindingCreate(ctx, fakeClient, bm3, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req1)

	requeueSubnetBySubnetBindingUpdate(ctx, fakeClient, bm1, bm1, myQueue)
	require.Equal(t, 0, myQueue.Len())

	requeueSubnetBySubnetBindingUpdate(ctx, fakeClient, bm1, bm2, myQueue)
	require.Equal(t, 2, myQueue.Len())
	queueItemEquals(t, myQueue, req4)
	queueItemEquals(t, myQueue, req2)

	requeueSubnetBySubnetBindingUpdate(ctx, fakeClient, bm1, bm3, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req2)

	requeueSubnetBySubnetBindingUpdate(ctx, fakeClient, bm3, bm1, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req2)

	requeueSubnetBySubnetBindingDelete(ctx, fakeClient, bm1, myQueue)
	require.Equal(t, 2, myQueue.Len())
	queueItemEquals(t, myQueue, req1)
	queueItemEquals(t, myQueue, req2)
}

func queueItemEquals(t *testing.T, myQueue workqueue.TypedRateLimitingInterface[reconcile.Request], req reconcile.Request) {
	item, _ := myQueue.Get()
	assert.Equal(t, req, item)
	myQueue.Done(item)
}
