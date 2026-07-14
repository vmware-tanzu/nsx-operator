package common

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGenericGarbageCollector(t *testing.T) {
	// Save original startup delay and restore it afterwards
	origDelay := GCStartupDelay
	defer func() {
		GCStartupDelay = origDelay
	}()

	// Set startup delay to 0 for testing immediate execution
	GCStartupDelay = 0

	var wg sync.WaitGroup
	wg.Add(1)

	var calledCount int32
	f := func(ctx context.Context) error {
		val := atomic.AddInt32(&calledCount, 1)
		if val == 1 {
			wg.Done()
		}
		return nil
	}

	cancel := make(chan bool)
	done := make(chan struct{})

	// Run GenericGarbageCollector with a short interval
	go func() {
		GenericGarbageCollector(cancel, 10*time.Millisecond, f)
		close(done)
	}()

	// Wait for the first immediate call to complete
	wg.Wait()

	// Wait a bit more to see if it ticks again
	time.Sleep(25 * time.Millisecond)

	assert.GreaterOrEqual(t, int(atomic.LoadInt32(&calledCount)), 2, "GC should have run at least twice (initial + ticks)")

	close(cancel)
	<-done
}

func TestGenericGarbageCollector_WithDelay(t *testing.T) {
	origDelay := GCStartupDelay
	defer func() {
		GCStartupDelay = origDelay
	}()

	// Set a short startup delay of 50ms for testing
	GCStartupDelay = 50 * time.Millisecond

	var calledCount int32
	f := func(ctx context.Context) error {
		atomic.AddInt32(&calledCount, 1)
		return nil
	}

	cancel := make(chan bool)
	done := make(chan struct{})

	go func() {
		GenericGarbageCollector(cancel, 10*time.Millisecond, f)
		close(done)
	}()

	// Immediately after starting, it should not have run yet due to the startup delay
	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, int32(0), atomic.LoadInt32(&calledCount), "GC should not have run during the startup delay")

	// Wait for the startup delay (50ms) to pass
	time.Sleep(60 * time.Millisecond)
	assert.GreaterOrEqual(t, int(atomic.LoadInt32(&calledCount)), 1, "GC should have run at least once after the startup delay")

	close(cancel)
	<-done
}
