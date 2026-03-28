package logger

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
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
			logger := ZapCustomLogger(tc.debug, tc.logLevel, false).Logger

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
	customLogger := ZapCustomLogger(true, 2, false)

	// Test all five log levels
	customLogger.Trace("This is a trace message", "test_case", "trace_test", "timestamp", time.Now().Format("15:04:05"))
	customLogger.Debug("This is a debug message", "test_case", "debug_test", "timestamp", time.Now().Format("15:04:05"))
	customLogger.Warn("This is a warning message", "test_case", "warn_test", "timestamp", time.Now().Format("15:04:05"))
	customLogger.Info("This is an info message", "test_case", "info_test", "timestamp", time.Now().Format("15:04:05"))
	customLogger.Error(nil, "This is an error message", "test_case", "error_test", "timestamp", time.Now().Format("15:04:05"))

	t.Log("CustomLogger test completed - verify all log levels are displayed with proper formatting and colors")
}

func TestGetLogLevel(t *testing.T) {
	testCases := []struct {
		name     string
		debug    bool
		logLevel int
		expected int
	}{
		{"default", false, 0, 0},
		{"debug_flag_only", true, 0, 2},
		{"log_level_1", false, 1, 1},
		{"log_level_3", false, 3, 3},
		{"debug_with_higher_level", true, 3, 3},
		{"debug_with_lower_level", true, 1, 2},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := getLogLevel(tc.debug, tc.logLevel)
			if got != tc.expected {
				t.Errorf("getLogLevel(%v, %d) = %d, want %d", tc.debug, tc.logLevel, got, tc.expected)
			}
		})
	}
}

func TestAnsiColorControlledByFlag(t *testing.T) {
	// Helper: create a console writer via the same logic as ZapCustomLogger,
	// but write to a buffer so we can inspect the output.
	buildWriter := func(enableColor bool) (*bytes.Buffer, zerolog.Logger) {
		buf := &bytes.Buffer{}
		cw := zerolog.ConsoleWriter{
			Out:        buf,
			NoColor:    !enableColor,
			TimeFormat: logTmFmtWithMS,
			FormatLevel: func(i interface{}) string {
				levelStr := strings.ToUpper(fmt.Sprintf("%s", i))
				if !enableColor {
					return levelStr
				}
				switch levelStr {
				case "INFO":
					return colorGreen + levelStr + colorReset
				case "WARN":
					return colorMagenta + levelStr + colorReset
				case "ERROR":
					return colorBrightRed + levelStr + colorReset
				default:
					return levelStr
				}
			},
		}
		l := zerolog.New(cw).Level(zerolog.InfoLevel)
		return buf, l
	}

	t.Run("color_disabled", func(t *testing.T) {
		buf, l := buildWriter(false)
		l.Info().Msg("hello")
		output := buf.String()
		if strings.Contains(output, "\033[") {
			t.Errorf("color=false should not contain ANSI codes, got: %q", output)
		}
	})

	t.Run("color_enabled", func(t *testing.T) {
		buf, l := buildWriter(true)
		l.Info().Msg("hello")
		output := buf.String()
		if !strings.Contains(output, "\033[") {
			t.Errorf("color=true should contain ANSI codes, got: %q", output)
		}
	})
}
