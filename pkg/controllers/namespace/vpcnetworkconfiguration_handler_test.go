package namespace

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// Helper function to create a test VPCNetworkConfiguration
func createTestVPCNetworkConfiguration(name string, subnets []string) *v1alpha1.VPCNetworkConfiguration {
	length := len(subnets)
	sharedSubnets := make([]v1alpha1.SharedSubnet, length)
	for i, path := range subnets {
		sharedSubnets[i] = v1alpha1.SharedSubnet{
			Path: path,
		}
	}
	return &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.VPCNetworkConfigurationSpec{
			Subnets: sharedSubnets,
		},
	}
}

func TestEnqueueRequestForVPCNetworkConfiguration_Create(t *testing.T) {
	// Create a fake client
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	// Create a test VPCNetworkConfiguration
	vpcNetConfig := createTestVPCNetworkConfiguration("test-vpc-net-config", []string{"/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1"})

	// Create a reconciler
	reconciler := createTestNamespaceReconciler(nil)

	// Create the handler
	e := &EnqueueRequestForVPCNetworkConfiguration{
		Reconciler: reconciler,
	}

	// Create a queue
	queue := workqueue.NewTypedRateLimitingQueue(
		workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())

	// Mock the requeueNamespacesByVPCNetworkConfiguration function
	patches := gomonkey.ApplyFunc(requeueNamespacesByVPCNetworkConfiguration,
		func(_ context.Context, _ *NamespaceReconciler, _ *v1alpha1.VPCNetworkConfiguration, q ReconcileQueue) {
			// Add a test request to the queue
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: "test-namespace",
				},
			}
			q.Add(req)
		})
	defer patches.Reset()

	// Call the Create method
	e.Create(context.TODO(), event.CreateEvent{Object: vpcNetConfig}, queue)

	// Assert that the queue has one item
	assert.Equal(t, 1, queue.Len(), "Expected 1 item to be requeued")
}

func TestEnqueueRequestForVPCNetworkConfiguration_Update(t *testing.T) {
	// Create a fake client
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	// Create test VPCNetworkConfigurations
	oldVpcNetConfig := createTestVPCNetworkConfiguration("test-vpc-net-config", []string{"/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1"})
	newVpcNetConfig := createTestVPCNetworkConfiguration("test-vpc-net-config", []string{"/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-2"})

	// Create a reconciler
	reconciler := createTestNamespaceReconciler(nil)

	// Create the handler
	e := &EnqueueRequestForVPCNetworkConfiguration{
		Reconciler: reconciler,
	}

	// Create a queue
	queue := workqueue.NewTypedRateLimitingQueue(
		workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())

	// Mock the requeueNamespacesByVPCNetworkConfiguration function
	patches := gomonkey.ApplyFunc(requeueNamespacesByVPCNetworkConfiguration,
		func(_ context.Context, _ *NamespaceReconciler, _ *v1alpha1.VPCNetworkConfiguration, q ReconcileQueue) {
			// Add a test request to the queue
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: "test-namespace",
				},
			}
			q.Add(req)
		})
	defer patches.Reset()

	// Call the Update method
	e.Update(context.TODO(), event.UpdateEvent{ObjectOld: oldVpcNetConfig, ObjectNew: newVpcNetConfig}, queue)

	// Assert that the queue has one item
	assert.Equal(t, 1, queue.Len(), "Expected 1 item to be requeued")
}

func TestEnqueueRequestForVPCNetworkConfiguration_Delete(t *testing.T) {
	// Create a fake client
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	// Create a test VPCNetworkConfiguration
	vpcNetConfig := createTestVPCNetworkConfiguration("test-vpc-net-config", []string{"/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1"})

	// Create a reconciler
	reconciler := createTestNamespaceReconciler(nil)

	// Create the handler
	e := &EnqueueRequestForVPCNetworkConfiguration{
		Reconciler: reconciler,
	}

	// Create a queue
	queue := workqueue.NewTypedRateLimitingQueue(
		workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())

	// Call the Delete method
	e.Delete(context.TODO(), event.DeleteEvent{Object: vpcNetConfig}, queue)

	// Assert that the queue is empty (Delete does nothing)
	assert.Equal(t, 0, queue.Len(), "Expected queue to be empty")
}

func TestEnqueueRequestForVPCNetworkConfiguration_Generic(t *testing.T) {
	// Create a fake client
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))

	// Create a reconciler
	reconciler := createTestNamespaceReconciler(nil)

	// Create the handler
	e := &EnqueueRequestForVPCNetworkConfiguration{
		Reconciler: reconciler,
	}

	// Create a queue
	queue := workqueue.NewTypedRateLimitingQueue(
		workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())

	// Call the Generic method
	e.Generic(context.TODO(), event.GenericEvent{}, queue)

	// Assert that the queue is empty (Generic does nothing)
	assert.Equal(t, 0, queue.Len(), "Expected queue to be empty")
}

func TestPredicateFuncsVPCNetworkConfig_CreateFunc(t *testing.T) {
	// Create a test VPCNetworkConfiguration
	vpcNetConfig := createTestVPCNetworkConfiguration("test-vpc-net-config", []string{"/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1"})

	// Create a create event
	createEvent := event.CreateEvent{
		Object: vpcNetConfig,
	}

	// Call the CreateFunc
	result := PredicateFuncsVPCNetworkConfig.CreateFunc(createEvent)

	// Assert that the result is true
	assert.True(t, result, "Expected CreateFunc to return true")
}

func TestPredicateFuncsVPCNetworkConfig_UpdateFunc(t *testing.T) {
	// Test cases
	tests := []struct {
		name     string
		oldObj   *v1alpha1.VPCNetworkConfiguration
		newObj   *v1alpha1.VPCNetworkConfiguration
		expected bool
	}{
		{
			name:     "Subnets changed",
			oldObj:   createTestVPCNetworkConfiguration("test-vpc-net-config", []string{"/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1"}),
			newObj:   createTestVPCNetworkConfiguration("test-vpc-net-config", []string{"/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-2"}),
			expected: true,
		},
		{
			name:     "Subnets not changed",
			oldObj:   createTestVPCNetworkConfiguration("test-vpc-net-config", []string{"/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1"}),
			newObj:   createTestVPCNetworkConfiguration("test-vpc-net-config", []string{"/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1"}),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create an update event
			updateEvent := event.UpdateEvent{
				ObjectOld: tt.oldObj,
				ObjectNew: tt.newObj,
			}

			// Call the UpdateFunc
			result := PredicateFuncsVPCNetworkConfig.UpdateFunc(updateEvent)

			// Assert the expected result
			assert.Equal(t, tt.expected, result, "Expected UpdateFunc to return %v", tt.expected)
		})
	}
}

func TestPredicateFuncsVPCNetworkConfig_DeleteFunc(t *testing.T) {
	// Create a test VPCNetworkConfiguration
	vpcNetConfig := createTestVPCNetworkConfiguration("test-vpc-net-config", []string{"/orgs/default/projects/proj-1/vpcs/vpc-1/subnets/subnet-1"})

	// Create a delete event
	deleteEvent := event.DeleteEvent{
		Object: vpcNetConfig,
	}

	// Call the DeleteFunc
	result := PredicateFuncsVPCNetworkConfig.DeleteFunc(deleteEvent)

	// Assert that the result is false
	assert.False(t, result, "Expected DeleteFunc to return false")
}

func TestRequeueNamespacesByVPCNetworkConfiguration(t *testing.T) {
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer queue.ShutDown()
	nc := createTestVPCNetworkConfiguration("config", nil)
	requeueNamespacesByVPCNetworkConfiguration(context.Background(), nil, nc, queue)
	assert.Zero(t, queue.Len())
	// No client/service is configured: the event handler must not perform API reads.
	requeueNamespacesByVPCNetworkConfiguration(context.Background(), &NamespaceReconciler{}, nc, queue)
	assert.Equal(t, 1, queue.Len())
	request, _ := queue.Get()
	defer queue.Done(request)
	assert.Equal(t, "config", request.Name)
	assert.Equal(t, networkConfigDependency, request.Namespace)
}
