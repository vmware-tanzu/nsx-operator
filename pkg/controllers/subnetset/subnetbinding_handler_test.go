package subnetset

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
			SubnetName:       "child",
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
			SubnetName:          "child1",
			TargetSubnetSetName: "parentSet",
			VLANTrafficTag:      102,
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
			VLANTrafficTag:      102,
		},
	}

	subnet1 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: "default"},
	}
	subnet2 = &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: "parent", Namespace: "default"},
	}
	subnetSet1 = &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{Name: "parentSet", Namespace: "default"},
	}
	subnetSet2 = &v1alpha1.SubnetSet{
		ObjectMeta: metav1.ObjectMeta{Name: "parentSet2", Namespace: "default"},
	}
	req1 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "parentSet",
			Namespace: "default",
		},
	}
	req2 = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "parentSet2",
			Namespace: "default",
		},
	}
)

func TestRequeueSubnetSetByBindingMap(t *testing.T) {
	myQueue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer myQueue.ShutDown()

	ctx := context.TODO()
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(subnet1, subnet2, subnetSet1, subnetSet2).Build()

	requeueSubnetSetBySubnetBindingCreate(ctx, fakeClient, bm1, myQueue)
	require.Equal(t, 0, myQueue.Len())

	requeueSubnetSetBySubnetBindingCreate(ctx, fakeClient, bm2, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req1)

	requeueSubnetSetBySubnetBindingUpdate(ctx, fakeClient, bm2, bm2, myQueue)
	require.Equal(t, 0, myQueue.Len())

	requeueSubnetSetBySubnetBindingUpdate(ctx, fakeClient, bm1, bm2, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req1)

	requeueSubnetSetBySubnetBindingUpdate(ctx, fakeClient, bm2, bm3, myQueue)
	require.Equal(t, 2, myQueue.Len())
	queueItemEquals(t, myQueue, req2)
	queueItemEquals(t, myQueue, req1)

	requeueSubnetSetBySubnetBindingUpdate(ctx, fakeClient, bm1, bm3, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req2)

	requeueSubnetSetBySubnetBindingUpdate(ctx, fakeClient, bm3, bm1, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req2)

	requeueSubnetSetBySubnetBindingDelete(ctx, fakeClient, bm1, myQueue)
	require.Equal(t, 0, myQueue.Len())

	requeueSubnetSetBySubnetBindingDelete(ctx, fakeClient, bm2, myQueue)
	require.Equal(t, 1, myQueue.Len())
	queueItemEquals(t, myQueue, req1)
}

func queueItemEquals(t *testing.T, myQueue workqueue.TypedRateLimitingInterface[reconcile.Request], req reconcile.Request) {
	item, _ := myQueue.Get()
	assert.Equal(t, req, item)
	myQueue.Done(item)
}
