package subnet

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	ctlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestEnqueueRequestForNamespace_Create(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	e := &EnqueueRequestForNamespace{Client: client}
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	e.Create(context.TODO(), event.CreateEvent{}, queue)
	// No asserts here because Create does nothing, just ensuring no errors.
}

func TestEnqueueRequestForNamespace_Delete(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	e := &EnqueueRequestForNamespace{Client: client}
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	e.Delete(context.TODO(), event.DeleteEvent{}, queue)
	// No asserts here because Delete does nothing, just ensuring no errors.
}

func TestEnqueueRequestForNamespace_Generic(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	e := &EnqueueRequestForNamespace{Client: client}
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	e.Generic(context.TODO(), event.GenericEvent{}, queue)
	// No asserts here because Generic does nothing, just ensuring no errors.
}

func TestEnqueueRequestForNamespace_Update(t *testing.T) {
	// Prepare test data
	oldNamespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-namespace",
			Labels: map[string]string{"env": "test"},
		},
	}

	newNamespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-namespace",
			Labels: map[string]string{"env": "prod"},
		},
	}

	client := fake.NewClientBuilder().WithObjects(newNamespace).Build()
	e := &EnqueueRequestForNamespace{Client: client}
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	updateEvent := event.UpdateEvent{
		ObjectOld: oldNamespace,
		ObjectNew: newNamespace,
	}

	subnet := v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnet",
			Namespace: "test-namespace",
		},
	}
	subnetList := &v1alpha1.SubnetList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []v1alpha1.Subnet{subnet},
	}

	e.Update(context.TODO(), updateEvent, queue)
	assert.Equal(t, 0, queue.Len(), "Expected 1 item to be requeued")

	patches := gomonkey.ApplyFunc(listSubnet, func(c ctlclient.Client, ctx context.Context, options ...ctlclient.ListOption) (*v1alpha1.SubnetList, error) {
		return subnetList, nil
	})
	defer patches.Reset()

	e.Update(context.TODO(), updateEvent, queue)
	assert.Equal(t, 1, queue.Len(), "Expected 1 item to be requeued")
}

func TestPredicateFuncsNs_UpdateFunc(t *testing.T) {
	oldNamespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-namespace",
			Labels: map[string]string{"env": "test"},
		},
	}

	newNamespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-namespace",
			Labels: map[string]string{"env": "prod"},
		},
	}

	updateEvent := event.UpdateEvent{
		ObjectOld: oldNamespace,
		ObjectNew: newNamespace,
	}

	// Test the update function logic in PredicateFuncsNs
	result := PredicateFuncsNs.UpdateFunc(updateEvent)
	assert.True(t, result, "Expected update event to trigger requeue")

	// Test with no label change
	noChangeEvent := event.UpdateEvent{
		ObjectOld: oldNamespace,
		ObjectNew: oldNamespace,
	}

	result = PredicateFuncsNs.UpdateFunc(noChangeEvent)
	assert.False(t, result, "Expected no action when labels have not changed")

	res := PredicateFuncsNs.CreateFunc(event.CreateEvent{Object: newNamespace})
	assert.False(t, res, "Expected no action when labels have not changed")

	res = PredicateFuncsNs.DeleteFunc(event.DeleteEvent{Object: newNamespace})
	assert.False(t, res, "Expected no action when labels have not changed")
}

func TestRequeueSubnetSet(t *testing.T) {
	// Prepare test data
	subnet := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-subnet",
			Namespace: "test-namespace",
		},
	}

	scheme := clientgoscheme.Scheme
	v1alpha1.AddToScheme(scheme)

	// Test for empty namespace (no SubnetSets found)
	emptyClient := fake.NewClientBuilder().Build()
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	err := requeueSubnet(emptyClient, "empty-namespace", queue)
	assert.NoError(t, err, "Expected no error with empty namespace")
	assert.Equal(t, 0, queue.Len(), "Expected no items to be requeued for empty namespace")

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(subnet).Build()
	queue = workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	err = requeueSubnet(client, "test-namespace", queue)
	assert.NoError(t, err, "Expected no error while requeueing SubnetSets")
	assert.Equal(t, 1, queue.Len(), "Expected 1 item to be requeued")
}
