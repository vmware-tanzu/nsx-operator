package logger

import (
	"testing"
	"time"
)

func TestZapLoggerLevels(t *testing.T) {
	// Test with different log levels
	testCases := []struct {
		name        string
		debug       bool
		logLevel    int
		description string
	}{
		{
			name:        "InfoLevel",
			debug:       false,
			logLevel:    0,
			description: "Default info level logging",
		},
		{
			name:        "DebugLevel",
			debug:       true,
			logLevel:    0,
			description: "Debug level logging enabled via debug flag",
		},
		{
			name:        "TraceLevel",
			debug:       false,
			logLevel:    2,
			description: "Trace level logging via log level parameter",
		},
		{
			name:        "HighDebugLevel",
			debug:       true,
			logLevel:    3,
			description: "High debug level overrides debug flag",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Testing: %s", tc.description)

			// Create logger with specified configuration
			logger := ZapCustomLogger(tc.debug, tc.logLevel).Logger

			// Test various log levels
			logger.Info("This is an info message", "test_case", tc.name, "timestamp", time.Now().Format("15:04:05"))

			if tc.debug || tc.logLevel > 0 {
				logger.V(1).Info("This is a debug message (V1)", "test_case", tc.name, "debug_enabled", true)
			}

			if tc.logLevel >= 2 {
				logger.V(2).Info("This is a trace message (V2)", "test_case", tc.name, "trace_enabled", true)
			}

			// Test error logging (always visible)
			logger.Error(nil, "This is an error message", "test_case", tc.name, "error_type", "test_error")

			t.Log("---")
		})
	}
}

func TestCustomLogger(t *testing.T) {
	t.Log("Testing CustomLogger with all log levels...")
	// Test CustomLogger wrapper
	customLogger := ZapCustomLogger(true, 2)

	// Test all five log levels
	customLogger.Trace("This is a trace message", "test_case", "trace_test", "timestamp", time.Now().Format("15:04:05"))
	customLogger.Debug("This is a debug message", "test_case", "debug_test", "timestamp", time.Now().Format("15:04:05"))
	customLogger.Warn("This is a warning message", "test_case", "warn_test", "timestamp", time.Now().Format("15:04:05"))
	customLogger.Info("This is an info message", "test_case", "info_test", "timestamp", time.Now().Format("15:04:05"))
	customLogger.Error(nil, "This is an error message", "test_case", "error_test", "timestamp", time.Now().Format("15:04:05"))

	t.Log("CustomLogger test completed - verify all log levels are displayed with proper formatting and colors")
}
