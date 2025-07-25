/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package health

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestHealthCheckHandlers tests the HealthCheckHandlers struct
func TestHealthCheckHandlers(t *testing.T) {
	t.Run("AddHandler and GetHandlers", func(t *testing.T) {
		handlers := NewHealthCheckHandlers()

		// Add a handler
		handlerName := "TestHandler"
		handlerFunc := func() error { return nil }
		handlers.AddHandler(handlerName, handlerFunc)

		// Get handlers
		result := handlers.GetHandlers()

		// Verify the handler was added
		assert.Contains(t, result, handlerName)
		assert.NotNil(t, result[handlerName])
	})
}

// TestClusterHealthChecker tests the ClusterHealthChecker struct
func TestClusterHealthChecker(t *testing.T) {
	t.Run("CheckClusterHealth - Multiple Checks", func(t *testing.T) {
		// Create a test health checker with custom handlers
		handlers := NewHealthCheckHandlers()

		// Add a handler that always succeeds
		handlers.AddHandler("AlwaysSucceed", func() error {
			return nil
		})

		// Add a handler that always fails
		handlers.AddHandler("AlwaysFail", func() error {
			return errors.New("always fails")
		})

		// Create a test implementation of ClusterHealthChecker
		checker := &ClusterHealthChecker{
			handlers: handlers,
		}

		// Check health
		status := checker.CheckClusterHealth()

		// Verify the status is down because one check failed
		assert.Equal(t, HealthStatusDown, status)
	})

	t.Run("CheckClusterHealth - All Healthy", func(t *testing.T) {
		// Create a test health checker with custom handlers
		handlers := NewHealthCheckHandlers()

		// Add a handler that always succeeds
		handlers.AddHandler("AlwaysSucceed", func() error {
			return nil
		})

		// Create a test implementation of ClusterHealthChecker
		checker := &ClusterHealthChecker{
			handlers: handlers,
		}

		// Check health
		status := checker.CheckClusterHealth()

		// Verify the status is healthy
		assert.Equal(t, HealthStatusHealthy, status)
	})
}

// TestSystemHealthReporter tests the SystemHealthReporter struct
func TestSystemHealthReporter(t *testing.T) {
	t.Run("extractIntervalFromResponse", func(t *testing.T) {
		// Create a test implementation of SystemHealthReporter
		reporter := &SystemHealthReporter{
			reportInterval: DefaultReportInterval,
		}

		// Test with a valid interval
		response := map[string]interface{}{
			"interval": float64(30),
		}
		interval := reporter.extractIntervalFromResponse(response)
		assert.Equal(t, 30, interval)

		// Test with a missing interval
		response = map[string]interface{}{}
		interval = reporter.extractIntervalFromResponse(response)
		assert.Equal(t, int(DefaultReportInterval/time.Second), interval)

		// Test with an invalid interval type
		response = map[string]interface{}{
			"interval": "invalid",
		}
		interval = reporter.extractIntervalFromResponse(response)
		assert.Equal(t, int(DefaultReportInterval/time.Second), interval)
	})

	t.Run("updateReportInterval", func(t *testing.T) {
		// Create a test implementation of SystemHealthReporter
		reporter := &SystemHealthReporter{
			reportInterval: DefaultReportInterval,
			ticker:         time.NewTicker(DefaultReportInterval),
		}

		// Update the report interval
		newInterval := 30
		reporter.updateReportInterval(newInterval)

		// Verify the interval was updated
		assert.Equal(t, time.Duration(newInterval)*time.Second, reporter.reportInterval)
	})
}
