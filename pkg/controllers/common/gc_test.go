package common

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
)

func TestGenericGarbageCollector(t *testing.T) {
	for _, tc := range []struct {
		name             string
		cancelBeforeTick bool
	}{
		{name: "periodic collection"},
		{name: "cancel before first tick", cancelBeforeTick: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const interval = 10 * time.Millisecond
			gcClock := &gcTestClock{
				FakeClock: clocktesting.NewFakeClock(time.Unix(0, 0)),
				ready:     make(chan struct{}),
			}
			calls := make(chan struct{}, 1)
			cancel := make(chan bool)
			done := make(chan struct{})
			stop := sync.OnceFunc(func() { close(cancel) })
			go func() {
				genericGarbageCollector(cancel, interval, func(context.Context) error {
					select {
					case calls <- struct{}{}:
					default:
					}
					return nil
				}, gcClock)
				close(done)
			}()
			t.Cleanup(func() {
				stop()
				waitForGCSignal(t, done, "GC did not exit after cancellation")
			})

			// Synchronize with the first select, so scheduler delays cannot hide an immediate call.
			waitForGCSignal(t, gcClock.ready, "GC did not start waiting for ticks")
			assert.Empty(t, calls, "GC should not run immediately")
			gcClock.Step(interval - time.Nanosecond)
			assert.Empty(t, calls, "GC should not run before its interval")

			if tc.cancelBeforeTick {
				stop()
				waitForGCSignal(t, done, "GC did not exit after cancellation")
				gcClock.Step(2 * interval)
				assert.Empty(t, calls, "GC should not run after cancellation")
				return
			}
			gcClock.Step(time.Nanosecond)
			waitForGCSignal(t, calls, "GC did not run on the first tick")
			gcClock.Step(interval)
			waitForGCSignal(t, calls, "GC did not run on the next tick")
		})
	}
}

func waitForGCSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

type gcTestClock struct {
	*clocktesting.FakeClock
	ready chan struct{}
}

func (c *gcTestClock) NewTicker(interval time.Duration) clock.Ticker {
	return &gcTestTicker{Ticker: c.FakeClock.NewTicker(interval), ready: c.ready}
}

type gcTestTicker struct {
	clock.Ticker
	ready chan struct{}
	once  sync.Once
}

func (t *gcTestTicker) C() <-chan time.Time {
	t.once.Do(func() { close(t.ready) })
	return t.Ticker.C()
}
