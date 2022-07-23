/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ratelimiter

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAdjustRate(t *testing.T) {
	assert := assert.New(t)

	max := 10
	waitTime := 20 * time.Millisecond
	limiter := NewAIMDRateLimiter(max, 0.1)
	// normal adjust case
	time.Sleep(100 * time.Millisecond)
	limiter.AdjustRate(waitTime, 200)
	re := limiter.rate()
	assert.Equal(re, 2, "Set rate error.")

	// the interval less than period, should not adjust
	limiter.AdjustRate(time.Millisecond, 200)
	re = limiter.rate()
	assert.Equal(re, 2, "Set rate error.")

	// the upper rate should be equal to max
	for i := 0; i < max+10; i++ {
		time.Sleep(100 * time.Millisecond)
		limiter.AdjustRate(waitTime, 201)
	}
	re = limiter.rate()
	assert.Equal(re, max, fmt.Sprintf("Rate should not be %d.\n", re))

	// decrease the rate
	time.Sleep(100 * time.Millisecond)
	limiter.AdjustRate(0, 429)
	re = limiter.rate()
	assert.Equal(re, max/2, "Set rate error.")
}

func TestWait(t *testing.T) {
	assert := assert.New(t)
	max := 100
	limiter := NewAIMDRateLimiter(max, 0.1)
	cancel := make(chan int, 2)
	done := make(chan int, 2)
	go func() {
		i := 1
		for j := 0; j < 20; j++ {
			limiter.AdjustRate(0, 1)
		}
		for {
			limiter.Wait()
			i++
			if i >= 30 {
				done <- i
				break
			}
			select {
			case _ = <-cancel:
				done <- 0
				break
			default:
			}
		}
	}()
	time.Sleep(time.Second)
	cancel <- 1
	re := <-done
	assert.False(re > 0, fmt.Sprintf("Too much token %d.\n", re))
}

func TestRateLimiter_NewFixRateLimiter(t *testing.T) {
	limiter := NewFixRateLimiter(120)
	assert.Equal(t, limiter.rate(), MAXRATELIMIT)

	limiter = NewFixRateLimiter(80)
	assert.Equal(t, limiter.rate(), 80)

	limiter = NewFixRateLimiter(0)
	l, ok := limiter.(*FixRateLimiter)
	assert.Equal(t, ok, true)
	assert.Equal(t, limiter.rate(), 0)
	assert.Equal(t, l.disable, true)
}

func TestRateLimiter_NewAIMDRateLimiter(t *testing.T) {
	limiter := NewAIMDRateLimiter(120, 1.0)
	assert.Equal(t, limiter.rate(), 1)

	limiter = NewAIMDRateLimiter(80, 1.0)
	assert.Equal(t, limiter.rate(), 1)

	limiter = NewAIMDRateLimiter(0, 1.0)
	l, ok := limiter.(*AIMDRateLimter)
	assert.Equal(t, ok, true)
	assert.Equal(t, limiter.rate(), 0)
	assert.Equal(t, l.disable, true)
}

func TestRateLimiter_FixRateLimiterWait(t *testing.T) {
	// disable rate limiter
	limiter := NewFixRateLimiter(0)
	before := time.Now()
	limiter.Wait()
	after := time.Now()
	d := after.Sub(before)
	assert.True(t, d < time.Millisecond)

	// enable rate limiter, the first token
	limiter = NewFixRateLimiter(10)
	before = time.Now()
	limiter.Wait()
	after = time.Now()
	d = after.Sub(before)
	assert.True(t, d < time.Millisecond)

	// enable rate limiter, the second token
	before = time.Now()
	limiter.Wait()
	after = time.Now()
	d = after.Sub(before)
	assert.True(t, d > time.Millisecond*10)
}
