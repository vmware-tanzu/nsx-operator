// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

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
	limiter := CreateAIMDRateLimiter(max, 0.1)
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
	limiter := CreateAIMDRateLimiter(max, 0.1)
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
