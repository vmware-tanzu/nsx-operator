//go:build integration
// +build integration

package networkinfo

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

// mockAsyncIPBlocksInfoService is used to track calls and simulate delays
type mockAsyncIPBlocksInfoService struct {
	callCount int32
	delay     time.Duration
	failCount int32
	maxFails  int32
}

func (m *mockAsyncIPBlocksInfoService) UpdateIPBlocksInfo(ctx context.Context, vpcConfigCR *v1alpha1.VPCNetworkConfiguration) error {
	atomic.AddInt32(&m.callCount, 1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.maxFails > 0 {
		fails := atomic.AddInt32(&m.failCount, 1)
		if fails <= m.maxFails {
			return fmt.Errorf("simulated API failure %d/%d", fails, m.maxFails)
		}
	}
	return nil
}

func (m *mockAsyncIPBlocksInfoService) SyncIPBlocksInfo(ctx context.Context) error {
	return nil
}

func (m *mockAsyncIPBlocksInfoService) ResetPeriodicSync() {}

func setupEnvTest(t *testing.T) (*envtest.Environment, client.Client) {
	err := v1alpha1.AddToScheme(scheme.Scheme)
	require.NoError(t, err)

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "build", "yaml", "crd", "vpc")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	require.NoError(t, err)
	require.NotNil(t, k8sClient)

	return testEnv, k8sClient
}

func TestIntegration_VPCNetworkConfig_WorkqueueDeduplication(t *testing.T) {
	testEnv, k8sClient := setupEnvTest(t)
	defer testEnv.Stop()

	mockService := &mockAsyncIPBlocksInfoService{
		delay: 50 * time.Millisecond, // Add slight delay to allow queue to batch
	}

	handler := NewVPCNetworkConfigurationHandler(k8sClient, nil, mockService)

	// Start the background worker
	go handler.workerLoop()

	// Create CR
	cr := &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "test-vpc-dedup", Namespace: "default"},
	}
	err := k8sClient.Create(context.Background(), cr)
	require.NoError(t, err)

	// Rapidly update the CR 50 times
	for i := 0; i < 50; i++ {
		handler.enqueueTask(client.ObjectKeyFromObject(cr))
	}

	// Wait for queue to process
	time.Sleep(2 * time.Second)

	// Assert deduplication: 50 rapid enqueues should result in far fewer actual API calls
	actualCalls := atomic.LoadInt32(&mockService.callCount)
	assert.Less(t, actualCalls, int32(10), "Workqueue should deduplicate rapid updates")
}

func TestIntegration_VPCNetworkConfig_InformerNonBlocking(t *testing.T) {
	testEnv, k8sClient := setupEnvTest(t)
	defer testEnv.Stop()

	mockService := &mockAsyncIPBlocksInfoService{
		delay: 2 * time.Second, // Simulate a very slow NSX API call
	}

	handler := NewVPCNetworkConfigurationHandler(k8sClient, nil, mockService)

	// Start the background worker
	go handler.workerLoop()

	// Create CRs first
	for i := 0; i < 5; i++ {
		cr := &v1alpha1.VPCNetworkConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("test-vpc-%d", i), Namespace: "default"},
		}
		_ = k8sClient.Create(context.Background(), cr)
	}

	start := time.Now()

	// Enqueue 5 tasks. If it was synchronous, this would take 10 seconds and block.
	for i := 0; i < 5; i++ {
		handler.enqueueTask(client.ObjectKey{Name: fmt.Sprintf("test-vpc-%d", i), Namespace: "default"})
	}

	// The enqueue operations should be instant (non-blocking)
	duration := time.Since(start)
	assert.Less(t, duration, 100*time.Millisecond, "Enqueueing tasks should not block on slow API calls")

	// Wait for background worker to process at least one
	require.Eventually(t, func() bool {
		// Enqueue again just in case
		handler.enqueueTask(client.ObjectKey{Name: "test-vpc-0", Namespace: "default"})
		return atomic.LoadInt32(&mockService.callCount) >= 1
	}, 15*time.Second, 1*time.Second, "Background worker should process tasks")
}

func TestIntegration_VPCNetworkConfig_ExponentialBackoff(t *testing.T) {
	testEnv, k8sClient := setupEnvTest(t)
	defer testEnv.Stop()

	mockService := &mockAsyncIPBlocksInfoService{
		maxFails: 3, // Simulate 3 consecutive API failures
	}

	handler := NewVPCNetworkConfigurationHandler(k8sClient, nil, mockService)

	// Start the background worker
	go handler.workerLoop()

	// Create CR
	cr := &v1alpha1.VPCNetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "test-vpc-backoff", Namespace: "default"},
	}
	err := k8sClient.Create(context.Background(), cr)
	require.NoError(t, err)

	start := time.Now()
	handler.enqueueTask(client.ObjectKeyFromObject(cr))

	// Wait for it to eventually succeed after 3 failures
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&mockService.callCount) >= 4 // 3 fails + 1 success
	}, 10*time.Second, 100*time.Millisecond, "Task should eventually succeed after retries")

	duration := time.Since(start)
	// With exponential backoff (base delay is typically 5ms, then 10ms, 20ms...),
	// 3 retries should take at least a few milliseconds, but definitely less than 10 seconds.
	// This proves it didn't just spin-loop instantly.
	assert.Greater(t, duration, 10*time.Millisecond, "Retries should have some backoff delay")
}
