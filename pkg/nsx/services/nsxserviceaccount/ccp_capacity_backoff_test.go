/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCCPConnectionCapacityFullBackoffRequeue(t *testing.T) {
	t.Cleanup(ClearCCPConnectionCapacityFull)

	ClearCCPConnectionCapacityFull()
	d, skip := CCPConnectionCapacityFullBackoffRequeue(time.Now())
	assert.False(t, skip)
	assert.Zero(t, d)

	MarkCCPConnectionCapacityFull()
	now := time.Now()
	d, skip = CCPConnectionCapacityFullBackoffRequeue(now)
	assert.True(t, skip)
	assert.GreaterOrEqual(t, d, time.Minute*4+time.Second*55)
	assert.LessOrEqual(t, d, time.Minute*5+time.Second*5)

	// Window expired: state is cleared so later reconciles are not affected.
	d, skip = CCPConnectionCapacityFullBackoffRequeue(now.Add(6 * time.Minute))
	assert.False(t, skip)
	assert.Zero(t, d)
	d, skip = CCPConnectionCapacityFullBackoffRequeue(time.Now())
	assert.False(t, skip)
	assert.Zero(t, d)

	ClearCCPConnectionCapacityFull()
	d, skip = CCPConnectionCapacityFullBackoffRequeue(time.Now())
	assert.False(t, skip)
	assert.Zero(t, d)
}

func TestCCPConnectionCapacityFullBackoffRequeue_SubSecondRemainingReturnsOneSecond(t *testing.T) {
	t.Cleanup(ClearCCPConnectionCapacityFull)
	ccpConnCapMu.Lock()
	// ~1s left in the 5-minute window: mark at now - 4m59s.
	ccpConnCapFull = time.Now().Add(-4*time.Minute - 59*time.Second)
	ccpConnCapMu.Unlock()

	d, skip := CCPConnectionCapacityFullBackoffRequeue(time.Now())
	assert.True(t, skip)
	assert.Equal(t, time.Second, d)
}
