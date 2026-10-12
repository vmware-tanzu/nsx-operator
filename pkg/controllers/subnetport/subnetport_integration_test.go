//go:build integration
// +build integration

package subnetport

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func setupEnvTest(t *testing.T) (*envtest.Environment, client.Client, ctrl.Manager) {
	err := v1alpha1.AddToScheme(scheme.Scheme)
	require.NoError(t, err)
	err = corev1.AddToScheme(scheme.Scheme)
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

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	require.NoError(t, err)

	return testEnv, k8sClient, mgr
}

func TestIntegration_SubnetPort_vmMapFunc_Performance(t *testing.T) {
	testEnv, k8sClient, mgr := setupEnvTest(t)
	defer testEnv.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &SubnetPortReconciler{
		Client:    mgr.GetClient(),
		APIReader: k8sClient,
	}

	// Setup Field Indexer (This is what we are testing for performance)
	err := r.SetupFieldIndexers(mgr)
	require.NoError(t, err)

	// Start the manager cache in the background
	go func() {
		err = mgr.Start(ctx)
		require.NoError(t, err)
	}()
	require.True(t, mgr.GetCache().WaitForCacheSync(ctx))

	// Create 500 SubnetPorts in the EnvTest API Server
	for i := 0; i < 500; i++ {
		sp := &v1alpha1.SubnetPort{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("sp-%d", i),
				Namespace: "default",
			},
		}
		err := k8sClient.Create(ctx, sp)
		require.NoError(t, err)
	}

	// Create the target SubnetPort that matches our VM
	// Note: The annotation key must be servicecommon.AnnotationAttachmentRef ("nsx.vmware.com/attachment_ref")
	targetSP := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sp-target",
			Namespace: "default",
			Annotations: map[string]string{
				servicecommon.AnnotationAttachmentRef: "virtualmachine/target-vm/port1",
			},
		},
	}
	err = k8sClient.Create(ctx, targetSP)
	require.NoError(t, err)

	// Wait a bit for cache to populate
	require.Eventually(t, func() bool {
		// Also create the target SubnetPort again just in case it was lost
		targetSP := &v1alpha1.SubnetPort{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sp-target",
				Namespace: "default",
				Annotations: map[string]string{
					servicecommon.AnnotationAttachmentRef: "virtualmachine/target-vm/port1",
				},
			},
		}
		_ = k8sClient.Create(ctx, targetSP)
		
		// Force sync
		mgr.GetCache().WaitForCacheSync(ctx)
		
		requests := r.vmMapFunc(ctx, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "target-vm",
				Namespace: "default",
			},
		})
		
		return len(requests) >= 1
	}, 15*time.Second, 1*time.Second, "Cache failed to populate with target SubnetPort")

	vm := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target-vm",
			Namespace: "default",
		},
	}

	// Measure vmMapFunc execution time
	start := time.Now()
	requests := r.vmMapFunc(ctx, vm)
	duration := time.Since(start)

	// Assertions
	if assert.GreaterOrEqual(t, len(requests), 1, "Should find at least 1 matching SubnetPort") {
		assert.Equal(t, "sp-target", requests[0].Name)
	}

	// If it was O(N) full cluster list, it would take significantly longer.
	// With Field Indexer, it should be sub-millisecond. We assert < 50ms to be safe in CI.
	assert.Less(t, duration, 50*time.Millisecond, "vmMapFunc should be O(1) fast with Field Indexer")
}

func TestIntegration_SubnetPort_vmMapFunc_MalformedAnnotation(t *testing.T) {
	testEnv, k8sClient, mgr := setupEnvTest(t)
	defer testEnv.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &SubnetPortReconciler{
		Client:    mgr.GetClient(),
		APIReader: k8sClient,
	}

	err := r.SetupFieldIndexers(mgr)
	require.NoError(t, err)

	go func() {
		err = mgr.Start(ctx)
		require.NoError(t, err)
	}()
	require.True(t, mgr.GetCache().WaitForCacheSync(ctx))

	// Create a SubnetPort with malformed annotation (missing slash)
	malformedSP := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sp-malformed",
			Namespace: "default",
			Annotations: map[string]string{
				servicecommon.AnnotationAttachmentRef: "virtualmachine-target-vm-port1", // Malformed!
			},
		},
	}
	err = k8sClient.Create(ctx, malformedSP)
	require.NoError(t, err)
	
	// Create a SubnetPort with missing annotation
	missingSP := &v1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sp-missing",
			Namespace: "default",
		},
	}
	err = k8sClient.Create(ctx, missingSP)
	require.NoError(t, err)

	// Wait for cache
	mgr.GetCache().WaitForCacheSync(ctx)
	time.Sleep(1 * time.Second)

	vm := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target-vm",
			Namespace: "default",
		},
	}

	// The map func should safely ignore the malformed and missing annotations
	// and not panic, returning 0 requests.
	requests := r.vmMapFunc(ctx, vm)
	assert.Len(t, requests, 0, "Should safely ignore malformed annotations")
}
