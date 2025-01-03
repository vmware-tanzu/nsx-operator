package util

import (
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

var NSXTRealizeRetry = wait.Backoff{
	Steps:    50,
	Duration: 1 * time.Second,
	Factor:   1.0,
	Jitter:   0.1,
}
