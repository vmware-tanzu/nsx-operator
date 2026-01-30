package ratelimiter

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestLoggingRateLimiter_When(t *testing.T) {
	baseRateLimiter := workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]()

	loggingRateLimiter := &LoggingRateLimiter{
		TypedRateLimiter: baseRateLimiter,
	}

	testRequest := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "test-namespace",
			Name:      "test-name",
		},
	}

	duration1 := loggingRateLimiter.When(testRequest)
	assert.Greater(t, duration1, time.Duration(0), "First requeue duration should be greater than 0")

	numRequeues1 := loggingRateLimiter.NumRequeues(testRequest)
	assert.Equal(t, 1, numRequeues1, "Number of requeues should be 1 after first call")

	duration2 := loggingRateLimiter.When(testRequest)
	assert.Greater(t, duration2, duration1, "Second requeue duration should be greater than first")

	numRequeues2 := loggingRateLimiter.NumRequeues(testRequest)
	assert.Equal(t, 2, numRequeues2, "Number of requeues should be 2 after second call")
}
