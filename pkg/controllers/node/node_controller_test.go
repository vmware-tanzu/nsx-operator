package node

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/golang/mock/gomock"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	mock_client "github.com/vmware-tanzu/nsx-operator/pkg/mock/controller-runtime/client"
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
			expectedResult: common.ResultNormal,
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
			expectedResult: common.ResultNormal,
			expectedErr:    false,
		},
		{
			name: "Worker node",
			existingNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "worker-node",
				},
			},
			expectedResult: common.ResultNormal,
			expectedErr:    false,
		},
		{
			name:           "Sync node error",
			existingNode:   nil,
			expectedResult: common.ResultNormal,
			expectedErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			nodeName := "test-node"

			if tt.existingNode != nil {
				nodeName = tt.existingNode.Name
				err := reconciler.Client.Create(ctx, tt.existingNode)
				assert.NoError(t, err)
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: nodeName,
				},
			}

			if tt.name != "Master Node" && tt.name != "Sync node error" {
				patchesSyncNodeStore := gomonkey.ApplyFunc((*node.NodeService).SyncNodeStore,
					func(n *node.NodeService, nodeName string, deleted bool) error {
						return nil
					})
				defer patchesSyncNodeStore.Reset()
			}

			if tt.name == "Sync node error" {
				patchesSyncNodeStore := gomonkey.ApplyFunc((*node.NodeService).SyncNodeStore,
					func(n *node.NodeService, nodeName string, deleted bool) error {
						return errors.New("Sync node error")
					})
				defer patchesSyncNodeStore.Reset()
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

func TestNodeReconciler_StartController(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithObjects().Build()
	nodeService := &node.NodeService{
		Service: servicecommon.Service{
			Client: fakeClient,
		},
	}
	mockMgr := &MockManager{scheme: runtime.NewScheme()}
	patches := gomonkey.ApplyFunc((*NodeReconciler).setupWithManager, func(r *NodeReconciler, mgr manager.Manager) error {
		return nil
	})
	patches.ApplyFunc(common.GenericGarbageCollector, func(cancel chan bool, timeout time.Duration, f func(ctx context.Context) error) {
		return
	})
	defer patches.Reset()
	r := NewNodeReconciler(mockMgr, nodeService)
	err := r.StartController(mockMgr, nil)
	assert.Nil(t, err)
}

func TestNodeReconciler_RestoreReconcile(t *testing.T) {
	mockCtl := gomock.NewController(t)
	k8sClient := mock_client.NewMockClient(mockCtl)
	defer mockCtl.Finish()
	k8sClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil).Do(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		podList := list.(*v1.NodeList)
		podList.Items = []v1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "node-3"}},
		}
		return nil
	})
	patches := gomonkey.ApplyFunc((*NodeReconciler).Reconcile, func(r *NodeReconciler, ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		if req.Name != "node-1" && req.Name != "node-2" && req.Name != "node-3" {
			assert.Failf(t, "Unexpected request", "req", req)
		}
		return common.ResultNormal, nil
	})
	defer patches.Reset()
	r := &NodeReconciler{
		Client: k8sClient,
	}
	err := r.RestoreReconcile()
	assert.Nil(t, err)
}

type MockManager struct {
	ctrl.Manager
	client client.Client
	scheme *runtime.Scheme
}

func (m *MockManager) GetClient() client.Client {
	return m.client
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *MockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return nil
}

func (m *MockManager) Add(runnable manager.Runnable) error {
	return nil
}

func (m *MockManager) Start(context.Context) error {
	return nil
}
