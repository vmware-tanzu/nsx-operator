package util

import (
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

var NSXTDefaultRetry = wait.Backoff{
	Steps:    10,
	Duration: 500 * time.Millisecond,
	Factor:   1.0,
	Jitter:   0.1,
}

var NSXTLBVSDefaultRetry = wait.Backoff{
	Steps:    60,
	Duration: 500 * time.Millisecond,
	Factor:   1.0,
	Jitter:   0.1,
}
