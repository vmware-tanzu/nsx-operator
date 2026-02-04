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
	// If the request is requeued will error=nil, e.g. "return ResultRequeueAfter60sec, nil", it won't be logged here.
	log.Debug("RateLimiter: Item has been requeued and will be delayed", "item", item, "requeues", requeues, "duration", duration)
	return duration
}
