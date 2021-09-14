// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package ratelimiter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gitlab.eng.vmware.com/nsx-container-plugin/vmware-nsxlib-go/pkg/logger"
	"golang.org/x/time/rate"
)

// APIReduceRateCodes is http status code set which will trigger rate limiter adjust.
var (
	APIReduceRateCodes = [2]int{429, 503}
)

// KeepAlivePeriod is period checking if endpoint is alive.
const KeepAlivePeriod = 33

// DEFAULTUPDATEPERIOD is the default period(seconds) to update rate.
const DEFAULTUPDATEPERIOD = 1.0

// RateLimiterTimeout is timer waiting for a rate limiter token.
const RateLimiterTimeout = 10

// APIWaitMinThreshold is threshold(second) which will trigger rate limiter adjust.
const APIWaitMinThreshold = float64(0.01)

// MAXRATELIMIT means max rate for rate limiter.
const MAXRATELIMIT = 100

// RateLimiterType is rate limiter type.
type RateLimiterType int32

const (
	// FIXRATE is a limiter which has fix rate.
	FIXRATE RateLimiterType = iota
	// AIMD is a limiter which rate will adjuct depending on wait time and http status code.
	AIMD RateLimiterType = 1
)

var (
	log = logger.GetInstance()
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

// CreateFixRateLimiter creates fix rate limiter.
// max ==0 disables rate limiter.
func CreateFixRateLimiter(max int) RateLimiter {
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

// CreateAIMDRateLimiter creates AIMD rate limiter.
// max ==0 disables rate limiter.
func CreateAIMDRateLimiter(max int, period float64) RateLimiter {
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
		log.Debug(fmt.Sprintf("Wait for token timeout: %s", err.Error()))
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
		log.Debug(fmt.Sprintf("Wait for token timeout: %s", err.Error()))
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
				log.Debug(fmt.Sprintf("Increasing API rate limit to %d with HTTP status code %d", r, statusCode))
			}
		} else if limiter.neg > 0 {
			if r > 1 {
				r = r / 2
				limiter.l.SetLimit(rate.Limit(r))
				log.Debug(fmt.Sprintf("Decreasing API rate limit to %d due to HTTP status code %d", r, statusCode))
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
