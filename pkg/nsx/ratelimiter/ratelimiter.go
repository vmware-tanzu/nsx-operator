/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ratelimiter

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// APIReduceRateCodes is http status code set which will trigger rate limiter adjust.
var (
	APIReduceRateCodes = [2]int{429, 503}
	log                = logf.Log.WithName("nsx").WithName("ratelimiter")
)

const (
	// KeepAlivePeriod is period checking if endpoint is alive.
	KeepAlivePeriod = 33

	// DEFAULTUPDATEPERIOD is the default period(seconds) to update rate.
	DEFAULTUPDATEPERIOD = 1.0

	// RateLimiterTimeout is timer waiting for a rate limiter token.
	RateLimiterTimeout = 10

	// APIWaitMinThreshold is threshold(second) which will trigger rate limiter adjust.
	APIWaitMinThreshold = float64(0.01)

	// MAXRATELIMIT means max rate for rate limiter.
	MAXRATELIMIT = 100
)

// Type is rate limiter type.
type Type int32

const (
	// FIXRATE is a limiter which has fix rate.
	FIXRATE Type = iota
	// AIMD is a limiter which rate will adjuct depending on wait time and http status code.
	AIMD Type = 1
)

// RateLimiter limits the REST API speed.
type RateLimiter interface {
	Wait()
	AdjustRate(time.Duration, int)
	rate() int
}

// FixRateLimiter is rate limiter which has fix rate.
type FixRateLimiter struct {
	l       *rate.Limiter
	disable bool
	max     int
}

// AIMDRateLimter is rate limiter which could adjuct its' rate depending on wait time and http status code.
type AIMDRateLimter struct {
	l              *rate.Limiter
	disable        bool
	max            int
	period         float64
	lastAdjuctRate time.Time
	pos            int
	neg            int
	sync.Mutex
}

type rateLimiter struct {
	apirateLimitPerEndpoint int
}

func (r *rateLimiter) SetMaxrate(maxrate int) {
	r.apirateLimitPerEndpoint = maxrate
}

// NewRateLimiter creates rate limeter based on RateLimiterType
func NewRateLimiter(rateLimiterType Type) RateLimiter {
	if rateLimiterType == FIXRATE {
		return NewFixRateLimiter(MAXRATELIMIT)
	}
	return NewAIMDRateLimiter(MAXRATELIMIT, DEFAULTUPDATEPERIOD)
}

// NewFixRateLimiter creates AIMD rate limiter.
// max ==0 disables rate limiter.
func NewFixRateLimiter(max int) RateLimiter {
	var m int
	if max > MAXRATELIMIT {
		m = MAXRATELIMIT
	} else {
		m = max
	}
	limiter := rate.NewLimiter(rate.Limit(m), 1)
	if max == 0 {
		return &FixRateLimiter{l: limiter, disable: true}
	}
	return &FixRateLimiter{l: limiter, max: m, disable: false}
}

// NewAIMDRateLimiter creates AIMD rate limiter.
// max ==0 disables rate limiter.
func NewAIMDRateLimiter(max int, period float64) RateLimiter {
	var m int
	if max > MAXRATELIMIT {
		m = MAXRATELIMIT
	} else {
		m = max
	}
	limiter := rate.NewLimiter(1, 1)
	if max == 0 {
		return &AIMDRateLimter{l: limiter, max: m, disable: true, period: period, lastAdjuctRate: time.Now()}
	}
	return &AIMDRateLimter{l: limiter, max: m, disable: false, period: period, lastAdjuctRate: time.Now()}
}

// Wait blocks the caller until a token is gained.
func (limiter *FixRateLimiter) Wait() {
	if limiter.disable {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*RateLimiterTimeout)
	defer cancel()
	err := limiter.l.WaitN(ctx, 1)
	if err != nil {
		log.V(1).Info("wait for token timeout", "error", err.Error())
		return
	}
}

func (limiter *FixRateLimiter) rate() int {
	if limiter.disable {
		return 0
	}
	return int(limiter.l.Limit())
}

// AdjustRate adjust upper limit for rate limiter, it's empty for FixRateLimiter.
func (limiter *FixRateLimiter) AdjustRate(waitTime time.Duration, statusCode int) {
}

// Wait blocks the caller until a token is gain.
func (limiter *AIMDRateLimter) Wait() {
	if limiter.disable {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*RateLimiterTimeout)
	defer cancel()
	err := limiter.l.WaitN(ctx, 1)
	if err != nil {
		log.V(1).Info("wait for token timeout", "error", err.Error())
		return
	}
}

// AdjustRate adjust upper limit for rate limiter.
func (limiter *AIMDRateLimter) AdjustRate(waitTime time.Duration, statusCode int) {
	if limiter.disable {
		return
	}
	limiter.Lock()
	defer limiter.Unlock()
	for _, v := range APIReduceRateCodes {
		if v == statusCode {
			limiter.neg++
		}
	}

	if waitTime.Seconds() > APIWaitMinThreshold {
		limiter.pos++
	}
	now := time.Now()
	if now.Sub(limiter.lastAdjuctRate).Seconds() >= limiter.period {
		r := int(limiter.l.Limit())
		if limiter.pos > 0 {
			if r < limiter.max {
				r++
				limiter.l.SetLimit(rate.Limit(r))
				log.V(1).Info("increasing API rate limit", "rateLimit", r, "statusCode", statusCode)
			}
		} else if limiter.neg > 0 {
			if r > 1 {
				r = r / 2
				limiter.l.SetLimit(rate.Limit(r))
				log.V(1).Info("decreasing API rate limit", "rateLimit", r, "statusCode", statusCode)
			}
		}
		limiter.lastAdjuctRate = now
		limiter.pos = 0
		limiter.neg = 0
	}
}

func (limiter *AIMDRateLimter) rate() int {
	if limiter.disable {
		return 0
	}
	return int(limiter.l.Limit())
}
