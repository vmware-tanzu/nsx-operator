/* Copyright © 2021 VMware, Inc. All Rights Reserved.

   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"errors"
	"math"
	"time"

	"k8s.io/client-go/util/workqueue"

	"pkg/util/locerrors"
)

// The OnError function re-enqueue the object key based on predefined retry rate
// and max retry time
func OnError(queue workqueue.RateLimitingInterface, key string, err error) {
	var infiniteErrType *locerrors.InfiniteRetryError
	var retryableErrType *locerrors.RetryableError

	if !errors.As(err, &retryableErrType) {
		// Forget the key immediately if the error is not retryable
		queue.Forget(key)
		return
	}
	// Forget the key after max attempts if the error is not infinite-retryable
	retryableErr := err.(*locerrors.RetryableError)
	numReQueues := queue.NumRequeues(key)
	if !errors.As(err, &infiniteErrType) {
		maxAttempts := retryableErr.MaxRetryAttempts
		if numReQueues > maxAttempts {
			queue.Forget(key)
			return
		}
	}
	// Calculate the backoff time based on the settings per error
	// The default queue rate limiter will be used when the localized error does not have customized retry interval
	minRetryIntvPerErr, maxRetryIntvPerErr := retryableErr.MinRetryInterval, retryableErr.MaxRetryInterval
	linearRetry := retryableErr.LinearRetryAttempts
	if linearRetry != 0 && linearRetry > numReQueues {
		queue.AddAfter(key, minRetryIntvPerErr)
	} else if minRetryIntvPerErr != 0 && maxRetryIntvPerErr != 0 {
		backoff := time.Duration(float64(minRetryIntvPerErr.Nanoseconds()) * math.Pow(2, float64(numReQueues)))
		if backoff > maxRetryIntvPerErr {
			backoff = maxRetryIntvPerErr
		}
		queue.AddAfter(key, backoff)
	} else {
		queue.AddRateLimited(key)
	}
}
