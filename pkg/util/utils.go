/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"k8s.io/client-go/util/workqueue"

	"github.com/vmware-tanzu/nsx-operator/pkg/util/locerrors"
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

func NormalizeLabels(matchLabels *map[string]string) *map[string]string {
	newLabels := make(map[string]string)
	for k, v := range *matchLabels {
		newLabels[NormalizeLabelKey(k)] = NormalizeName(v)
	}
	return &newLabels
}

func NormalizeLabelKey(key string) string {
	if len(key) <= MaxTagLength {
		return key
	}
	splitted := strings.Split(key, "/")
	key = splitted[len(splitted)-1]
	return NormalizeName(key)
}

func NormalizeName(name string) string {
	if len(name) <= MaxTagLength {
		return name
	}
	hashString := Sha1(name)
	nameLength := MaxTagLength - HashLength - 1
	newName := fmt.Sprintf("%s-%s", name[:nameLength], hashString[:HashLength])
	return newName
}

func Sha1(data string) string {
	h := sha1.New()
	h.Write([]byte(data))
	sum := h.Sum(nil)
	return fmt.Sprintf("%x", sum)
}
