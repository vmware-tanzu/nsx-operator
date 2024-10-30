package node

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/node"
)

func createMockNodeService() *node.NodeService {
	return &node.NodeService{
		Service: servicecommon.Service{
			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{},
			},
		},
	}
}

func TestNodeReconciler_Reconcile(t *testing.T) {
	// Create a patch for metrics.CounterInc
	patch := gomonkey.ApplyFunc(metrics.CounterInc, func(*config.NSXOperatorConfig, *prometheus.CounterVec, string) {
		// Do nothing
	})
	defer patch.Reset()

	// Create a fake client
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)

	// Create a NodeReconciler
	reconciler := &NodeReconciler{
		Client:  fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme:  scheme,
		Service: createMockNodeService(),
	}

	// Test cases
	tests := []struct {
		name           string
		existingNode   *v1.Node
		expectedResult ctrl.Result
		expectedErr    bool
	}{
		{
			name:           "Node not found",
			existingNode:   nil,
			expectedResult: ctrl.Result{},
			expectedErr:    false,
		},
		{
			name: "Master node",
			existingNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "master-node",
					Labels: map[string]string{
						"node-role.kubernetes.io/master": "",
					},
				},
			},
			expectedResult: ctrl.Result{},
			expectedErr:    false,
		},
		{
			name: "Worker node",
			existingNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "worker-node",
				},
			},
			expectedResult: ctrl.Result{},
			expectedErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			if tt.existingNode != nil {
				err := reconciler.Client.Create(ctx, tt.existingNode)
				assert.NoError(t, err)
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: "test-node",
				},
			}

			result, err := reconciler.Reconcile(ctx, req)

			if tt.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestPredicateFuncsNode(t *testing.T) {
	oldNode := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			ResourceVersion: "1",
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{
				{
					Type:   v1.NodeReady,
					Status: v1.ConditionTrue,
				},
			},
		},
	}

	newNode := oldNode.DeepCopy()
	newNode.ResourceVersion = "2"
	newNode.Status.Conditions[0].Status = v1.ConditionFalse

	assert.False(t, PredicateFuncsNode.UpdateFunc(event.UpdateEvent{
		ObjectOld: oldNode,
		ObjectNew: newNode,
	}))

	// Test when only resource version changes
	newNode.Status.Conditions[0].Status = v1.ConditionTrue
	assert.False(t, PredicateFuncsNode.UpdateFunc(event.UpdateEvent{
		ObjectOld: oldNode,
		ObjectNew: newNode,
	}))

	newNode.Status.Phase = v1.NodePending
	assert.True(t, PredicateFuncsNode.UpdateFunc(event.UpdateEvent{
		ObjectOld: oldNode,
		ObjectNew: newNode,
	}))

	assert.True(t, PredicateFuncsNode.CreateFunc(event.CreateEvent{}))
	assert.True(t, PredicateFuncsNode.DeleteFunc(event.DeleteEvent{}))
	assert.True(t, PredicateFuncsNode.GenericFunc(event.GenericEvent{}))
}
