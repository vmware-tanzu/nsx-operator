package networkinfo

import (
	"context"
	"fmt"
	"sync"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	types "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipblocksinfo"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
)

func createVPCNetworkConfigurationHandler(objs []client.Object) *VPCNetworkConfigurationHandler {
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(v1alpha1.AddToScheme(newScheme))
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme).WithObjects(objs...).Build()

	vpcService := &vpc.VPCService{
		Service: types.Service{
			Client: fakeClient,
			NSXClient: &nsx.Client{
				VPCConnectivityProfilesClient: &fakeVPCConnectivityProfilesClient{},
				VpcAttachmentClient:           fakeAttachmentClient,
			},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint:   "vmc-enforcementpoint",
					UseAVILoadBalancer: false,
				},
			},
		},
	}

	ipBlocksInfoService := &ipblocksinfo.IPBlocksInfoService{
		Service: types.Service{
			Client: fakeClient,
			NSXClient: &nsx.Client{
				VPCConnectivityProfilesClient: &fakeVPCConnectivityProfilesClient{},
				VpcAttachmentClient:           fakeAttachmentClient,
			},

			NSXConfig: &config.NSXOperatorConfig{
				NsxConfig: &config.NsxConfig{
					EnforcementPoint:   "vmc-enforcementpoint",
					UseAVILoadBalancer: false,
				},
			},
		},
		SyncTask: nil,
	}

	return NewVPCNetworkConfigurationHandler(fakeClient, vpcService, ipBlocksInfoService)
}

func TestVPCNetworkConfigurationHandler_Create(t *testing.T) {
	testCases := []struct {
		name             string
		vpcNetworkConfig *v1alpha1.VPCNetworkConfiguration
	}{
		{
			name: "Create with invalid NSX project path",
			vpcNetworkConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: ""},
			},
		},
		{
			name: "Create with valid NSX project path",
			vpcNetworkConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(
				workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			handler := createVPCNetworkConfigurationHandler(nil)
			handler.Create(context.TODO(), event.CreateEvent{Object: tc.vpcNetworkConfig}, queue)
		})
	}
}

func TestVPCNetworkConfigurationHandler_Delete(t *testing.T) {
	testCases := []struct {
		name             string
		vpcNetworkConfig *v1alpha1.VPCNetworkConfiguration
	}{
		{
			name: "Delete VPCNetworkConfiguration",
			vpcNetworkConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(
				workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			handler := createVPCNetworkConfigurationHandler(nil)
			handler.Delete(context.TODO(), event.DeleteEvent{Object: tc.vpcNetworkConfig}, queue)
		})
	}
}

func TestVPCNetworkConfigurationHandler_Update(t *testing.T) {
	testCases := []struct {
		name                string
		vpcNetworkConfigOld *v1alpha1.VPCNetworkConfiguration
		vpcNetworkConfigNew *v1alpha1.VPCNetworkConfiguration
		existingCR          []client.Object
	}{
		{
			name: "Update VPCNetworkConfiguration with same Spec",
			vpcNetworkConfigOld: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
			vpcNetworkConfigNew: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
		},
		{
			name: "Update VPCNetworkConfiguration with diff Spec, and the new NSXProject is invalid",
			vpcNetworkConfigOld: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
			vpcNetworkConfigNew: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: ""},
			},
		},
		{
			name: "Update VPCNetworkConfiguration with diff Spec",
			vpcNetworkConfigOld: &v1alpha1.VPCNetworkConfiguration{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{Name: "testVPCNetworkConfig"},
				Spec:       v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
				Status:     v1alpha1.VPCNetworkConfigurationStatus{},
			},
			vpcNetworkConfigNew: &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "testVPCNetworkConfig"},
				Spec:       v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test", PrivateIPs: []string{"1.1.1.1"}},
			},
			existingCR: []client.Object{
				&v1alpha1.NetworkInfo{
					TypeMeta:   metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{Name: "testNetworkInfo", Namespace: "testNamespace"},
					VPCs:       nil,
				},
				&v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "testNamespace",
						Annotations: map[string]string{
							types.AnnotationVPCNetworkConfig: "testVPCNetworkConfig",
						},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(
				workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			var objs []client.Object
			if tc.existingCR != nil {
				objs = append(objs, tc.existingCR...)
			}
			handler := createVPCNetworkConfigurationHandler(objs)

			handler.Update(context.TODO(), event.UpdateEvent{ObjectOld: tc.vpcNetworkConfigOld, ObjectNew: tc.vpcNetworkConfigNew}, queue)
		})
	}
}

func TestVPCNetworkConfigurationHandler_Generic(t *testing.T) {
	testCases := []struct {
		name             string
		vpcNetworkConfig *v1alpha1.VPCNetworkConfiguration
	}{
		{
			name: "Delete VPCNetworkConfiguration",
			vpcNetworkConfig: &v1alpha1.VPCNetworkConfiguration{
				Spec: v1alpha1.VPCNetworkConfigurationSpec{NSXProject: "/orgs/default/projects/nsx_operator_e2e_test"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(
				workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			handler := createVPCNetworkConfigurationHandler(nil)
			handler.Generic(context.TODO(), event.GenericEvent{Object: tc.vpcNetworkConfig}, queue)
		})
	}
}

type mockIPBlocksInfoService struct {
	mu          sync.Mutex
	updatedVPCs []string
	syncCount   int
	resetCount  int
	processed   chan struct{}
}

func (m *mockIPBlocksInfoService) UpdateIPBlocksInfo(_ context.Context, vpcConfigCR *v1alpha1.VPCNetworkConfiguration) error {
	m.mu.Lock()
	m.updatedVPCs = append(m.updatedVPCs, vpcConfigCR.Name)
	m.mu.Unlock()
	if m.processed != nil {
		m.processed <- struct{}{}
	}
	return nil
}

func (m *mockIPBlocksInfoService) SyncIPBlocksInfo(_ context.Context) error {
	m.mu.Lock()
	m.syncCount++
	m.mu.Unlock()
	if m.processed != nil {
		m.processed <- struct{}{}
	}
	return nil
}

func (m *mockIPBlocksInfoService) ResetPeriodicSync() {
	m.mu.Lock()
	m.resetCount++
	m.mu.Unlock()
}

func TestVPCNetworkConfigurationHandler_QueueFIFOAndCompletion(t *testing.T) {
	const taskCount = 2000
	mock := &mockIPBlocksInfoService{
		processed: make(chan struct{}, taskCount),
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	objs := []client.Object{}
	for i := 0; i < taskCount; i++ {
		objs = append(objs, &v1alpha1.VPCNetworkConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("vpc-%d", i)},
		})
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	handler := NewVPCNetworkConfigurationHandler(fakeClient, nil, mock)

	for i := 0; i < taskCount; i++ {
		handler.enqueueTask(k8stypes.NamespacedName{Name: fmt.Sprintf("vpc-%d", i)})
	}

	for i := 0; i < taskCount; i++ {
		<-mock.processed
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.updatedVPCs) != taskCount {
		t.Fatalf("expected %d processed tasks, got %d", taskCount, len(mock.updatedVPCs))
	}
	for i := 0; i < taskCount; i++ {
		expectedName := fmt.Sprintf("vpc-%d", i)
		if mock.updatedVPCs[i] != expectedName {
			t.Errorf("FIFO violation at index %d: expected %s, got %s", i, expectedName, mock.updatedVPCs[i])
		}
	}

	if handler.queue.Len() != 0 {
		t.Errorf("expected empty tasks slice, got length %d", handler.queue.Len())
	}
}

func TestVPCNetworkConfigurationHandler_QueueConcurrentEnqueue(t *testing.T) {
	const goroutines = 20
	const tasksPerGoroutine = 100
	const totalTasks = goroutines * tasksPerGoroutine

	mock := &mockIPBlocksInfoService{
		processed: make(chan struct{}, totalTasks),
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	objs := []client.Object{}
	for g := 0; g < goroutines; g++ {
		for i := 0; i < tasksPerGoroutine; i++ {
			objs = append(objs, &v1alpha1.VPCNetworkConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("g%d-vpc-%d", g, i)},
			})
		}
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	handler := NewVPCNetworkConfigurationHandler(fakeClient, nil, mock)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			for i := 0; i < tasksPerGoroutine; i++ {
				handler.enqueueTask(k8stypes.NamespacedName{Name: fmt.Sprintf("g%d-vpc-%d", gID, i)})
			}
		}(g)
	}

	wg.Wait()

	for i := 0; i < totalTasks; i++ {
		<-mock.processed
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.updatedVPCs) != totalTasks {
		t.Fatalf("expected %d tasks processed, got %d", totalTasks, len(mock.updatedVPCs))
	}
}

func TestVPCNetworkConfigurationHandler_QueueTaskTypes(t *testing.T) {
	mock := &mockIPBlocksInfoService{
		processed: make(chan struct{}, 2),
	}

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	objs := []client.Object{
		&v1alpha1.VPCNetworkConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "test-update-vpc"},
		},
		// "test-delete-vpc" is NOT added to fakeClient, so Get() will return NotFound
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	handler := NewVPCNetworkConfigurationHandler(fakeClient, nil, mock)

	handler.enqueueTask(k8stypes.NamespacedName{Name: "test-update-vpc"})
	<-mock.processed

	handler.enqueueTask(k8stypes.NamespacedName{Name: "test-delete-vpc"})
	<-mock.processed

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.updatedVPCs) != 1 || mock.updatedVPCs[0] != "test-update-vpc" {
		t.Errorf("unexpected updated VPCs: %v", mock.updatedVPCs)
	}
	if mock.syncCount != 1 {
		t.Errorf("expected syncCount == 1, got %d", mock.syncCount)
	}
	if mock.resetCount != 1 {
		t.Errorf("expected resetCount == 1, got %d", mock.resetCount)
	}
}
