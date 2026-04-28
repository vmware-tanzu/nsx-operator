/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"sync"
	"time"
)

const ccpConnectionCapacityBackoffWindow = 5 * time.Minute

var (
	ccpConnCapMu   sync.Mutex
	ccpConnCapFull time.Time // zero value means no active backoff
)

// MarkCCPConnectionCapacityFull records the current time globally so NSXServiceAccount
// reconciles can avoid calling NSX until the backoff window expires.
func MarkCCPConnectionCapacityFull() {
	ccpConnCapMu.Lock()
	defer ccpConnCapMu.Unlock()
	ccpConnCapFull = time.Now()
}

// ClearCCPConnectionCapacityFull clears the backoff after a successful CCP Update.
func ClearCCPConnectionCapacityFull() {
	ccpConnCapMu.Lock()
	defer ccpConnCapMu.Unlock()
	ccpConnCapFull = time.Time{}
}

// CCPConnectionCapacityFullBackoffRequeue returns whether reconcile should skip all NSX
// calls and requeue. The duration is how long to wait until the backoff window ends.
// When the window has already passed, the stored mark is cleared.
func CCPConnectionCapacityFullBackoffRequeue(now time.Time) (requeueAfter time.Duration, skip bool) {
	ccpConnCapMu.Lock()
	defer ccpConnCapMu.Unlock()
	if ccpConnCapFull.IsZero() {
		return 0, false
	}
	remaining := ccpConnCapFull.Add(ccpConnectionCapacityBackoffWindow).Sub(now)
	if remaining <= 0 {
		ccpConnCapFull = time.Time{}
		return 0, false
	}
	if remaining < time.Second {
		return time.Second, true
	}
	return remaining, true
}
