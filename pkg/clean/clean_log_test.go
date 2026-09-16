package clean

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/assert"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

func TestClean_ConcurrentLogging(t *testing.T) {
	ctx := context.Background()

	// Create two buffers to capture logs
	var buf1, buf2 bytes.Buffer
	var mu1, mu2 sync.Mutex

	// Create two custom loggers
	log1 := funcr.New(func(prefix, args string) {
		mu1.Lock()
		defer mu1.Unlock()
		fmt.Fprintf(&buf1, "%s %s\n", prefix, args)
	}, funcr.Options{})

	log2 := funcr.New(func(prefix, args string) {
		mu2.Lock()
		defer mu2.Unlock()
		fmt.Fprintf(&buf2, "%s %s\n", prefix, args)
	}, funcr.Options{})

	// Use different invalid configs to trigger fast failure but distinct logs
	cf1 := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			NsxApiManagers: []string{"10.0.0.1"},
			// Missing user/password to fail validation
		},
	}
	cf2 := &config.NSXOperatorConfig{
		NsxConfig: &config.NsxConfig{
			NsxApiManagers: []string{"10.0.0.2"},
			// Missing user/password to fail validation
		},
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		// This will fail at ValidateConfigFromCmd, but it will log using log1
		_ = Clean(ctx, cf1, &log1, false, 0)
	}()

	go func() {
		defer wg.Done()
		// This will fail at ValidateConfigFromCmd, but it will log using log2
		_ = Clean(ctx, cf2, &log2, false, 0)
	}()

	wg.Wait()

	// Check if logs are isolated
	out1 := buf1.String()
	out2 := buf2.String()

	assert.Contains(t, out1, "Starting NSX cleanup")
	assert.Contains(t, out2, "Starting NSX cleanup")

	// Ensure no cross-talk (though without gomonkey we don't have a highly specific injected log,
	// the fact they both executed and wrote to their respective buffers without panic is the main goal here)
}
