package ratelimiter

import (
	"time"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

var (
	log = logger.Log
)

type LoggingRateLimiter struct {
	workqueue.TypedRateLimiter[reconcile.Request]
}

func (l *LoggingRateLimiter) When(item reconcile.Request) time.Duration {
	duration := l.TypedRateLimiter.When(item)
	requeues := l.TypedRateLimiter.NumRequeues(item)
	log.Debug("RateLimiter: Item has been requeued and will be delayed", "item", item, "requeues", requeues, "duration", duration)
	return duration
}
