package logger

import (
	"fmt"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/go-logr/zerologr"
	"github.com/rs/zerolog"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	logTmFmtWithMS = "2006-01-02 15:04:05.000"
	colorReset     = "\033[0m"
	colorYellow    = "\033[33m"
	colorBrightRed = "\033[91m"
	colorGreen     = "\033[32m"
	colorBlue      = "\033[34m"
	colorMagenta   = "\033[35m"
	colorWhite     = "\033[37m"
)

var (
	Log CustomLogger
)

func init() {
	logrLogger := logf.Log.WithName("nsx-operator")
	Log = NewCustomLogger(logrLogger)
}
func InitLog(log *logr.Logger) {
	Log = NewCustomLogger(*log)
}

// CustomLogger wraps a logr.Logger to provide TRACE, DEBUG, WARN, INFO, ERROR methods
type CustomLogger struct {
	logr.Logger
	zeroLogger *zerolog.Logger
}

// Trace logs a trace message (using V(2) level)
func (l CustomLogger) Trace(msg string, keysAndValues ...interface{}) {
	l.V(2).Info(msg, keysAndValues...)
}

// Debug logs a debug message (using V(1) level)
func (l CustomLogger) Debug(msg string, keysAndValues ...interface{}) {
	l.V(1).Info(msg, keysAndValues...)
}

// Warn logs a warning message
func (l CustomLogger) Warn(msg string, keysAndValues ...interface{}) {
	if l.zeroLogger != nil {
		// Use the zerolog directly to log at WARN level
		event := l.zeroLogger.Warn()
		for i := 0; i < len(keysAndValues); i += 2 {
			if i+1 < len(keysAndValues) {
				event = event.Interface(fmt.Sprintf("%v", keysAndValues[i]), keysAndValues[i+1])
			}
		}
		event.Msg(msg)
	} else {
		// Fallback to Info level with the prefix if zeroLogger is not available
		l.Info("[WARN] "+msg, keysAndValues...)
	}
}

// Info logs an info message (using base level)
func (l CustomLogger) Info(msg string, keysAndValues ...interface{}) {
	l.Logger.Info(msg, keysAndValues...)
}

// Error logs an error message
func (l CustomLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	l.Logger.Error(err, msg, keysAndValues...)
}

// NewCustomLogger creates a CustomLogger from a logr.Logger
func NewCustomLogger(logger logr.Logger) CustomLogger {
	return CustomLogger{Logger: logger, zeroLogger: nil}
}

// NewCustomLoggerWithZerolog creates a CustomLogger with both logr.Logger and zerolog.Logger
func NewCustomLoggerWithZerolog(logger logr.Logger, zeroLogger *zerolog.Logger) CustomLogger {
	return CustomLogger{Logger: logger, zeroLogger: zeroLogger}
}

// If debug set in configmap, set the log level to 2.
// If loglevel set in the command line and smaller than the debug log level, set it to command line level.
func getLogLevel(cfDebug bool, cfLogLevel int) int {
	logLevel := 0
	if cfDebug {
		logLevel = 2
	}
	realLogLevel := logLevel
	if cfLogLevel < logLevel {
		realLogLevel = cfLogLevel
	}
	return realLogLevel
}

// ZapLogger creates a logr.Logger using the zerolog with the same output format as the original zap logger
func ZapLogger(cfDebug bool, cfLogLevel int) logr.Logger {
	logLevel := getLogLevel(cfDebug, cfLogLevel)

	// Create the custom console writer with zap-like formatting
	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: logTmFmtWithMS,
		FormatLevel: func(i interface{}) string {
			levelStr := strings.ToUpper(fmt.Sprintf("%s", i))
			switch levelStr {
			case "TRACE":
				return colorWhite + levelStr + colorReset
			case "DEBUG":
				return colorBlue + levelStr + colorReset
			case "INFO":
				return colorGreen + levelStr + colorReset
			case "WARN":
				return colorMagenta + levelStr + colorReset
			case "ERROR":
				return colorBrightRed + levelStr + colorReset
			case "FATAL":
				return colorBrightRed + levelStr + colorReset
			case "PANIC":
				return colorBrightRed + levelStr + colorReset
			default:
				return levelStr
			}
		},
		FormatCaller: func(i interface{}) string {
			if i == nil {
				return ""
			}
			caller := fmt.Sprintf("%s", i)
			// Extract just the filename and line number like zap's TrimmedPath
			if idx := strings.LastIndex(caller, "/"); idx >= 0 {
				caller = caller[idx+1:]
			}
			return colorYellow + caller + colorReset
		},
		FormatFieldName: func(i interface{}) string {
			return fmt.Sprintf("%s=", i)
		},
		FormatFieldValue: func(i interface{}) string {
			return fmt.Sprintf("%s", i)
		},
		FieldsExclude: []string{},
	}

	// Set the log level based on the calculated level
	var zeroLogLevel zerolog.Level
	switch {
	case logLevel >= 2:
		zeroLogLevel = zerolog.TraceLevel
	case logLevel == 1:
		zeroLogLevel = zerolog.DebugLevel
	default:
		zeroLogLevel = zerolog.InfoLevel
	}

	// Create zerolog logger
	zeroLogger := zerolog.New(consoleWriter).
		Level(zeroLogLevel).
		With().
		Timestamp().
		CallerWithSkipFrameCount(1).
		Logger()

	// Convert to logr.Logger
	return zerologr.New(&zeroLogger)
}

// ZapCustomLogger creates a CustomLogger with both logr.Logger and zerolog.Logger using the same configuration as ZapLogger
func ZapCustomLogger(cfDebug bool, cfLogLevel int) CustomLogger {
	logLevel := getLogLevel(cfDebug, cfLogLevel)

	// Create the custom console writer with zap-like formatting
	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: logTmFmtWithMS,
		FormatLevel: func(i interface{}) string {
			levelStr := strings.ToUpper(fmt.Sprintf("%s", i))
			switch levelStr {
			case "TRACE":
				return colorWhite + levelStr + colorReset
			case "DEBUG":
				return colorBlue + levelStr + colorReset
			case "INFO":
				return colorGreen + levelStr + colorReset
			case "WARN":
				return colorMagenta + levelStr + colorReset
			case "ERROR":
				return colorBrightRed + levelStr + colorReset
			case "FATAL":
				return colorBrightRed + levelStr + colorReset
			case "PANIC":
				return colorBrightRed + levelStr + colorReset
			default:
				return levelStr
			}
		},
		FormatCaller: func(i interface{}) string {
			if i == nil {
				return ""
			}
			caller := fmt.Sprintf("%s", i)
			// Extract just the filename and line number like zap's TrimmedPath
			if idx := strings.LastIndex(caller, "/"); idx >= 0 {
				caller = caller[idx+1:]
			}
			return colorYellow + caller + colorReset
		},
		FormatFieldName: func(i interface{}) string {
			return fmt.Sprintf("%s=", i)
		},
		FormatFieldValue: func(i interface{}) string {
			return fmt.Sprintf("%s", i)
		},
		FieldsExclude: []string{},
	}

	// Set the log level based on the calculated level
	var zeroLogLevel zerolog.Level
	switch {
	case logLevel == 2:
		zeroLogLevel = zerolog.TraceLevel
	case logLevel == 1:
		zeroLogLevel = zerolog.DebugLevel
	default:
		zeroLogLevel = zerolog.InfoLevel
	}

	// Create zerolog logger
	zeroLogger := zerolog.New(consoleWriter).
		Level(zeroLogLevel).
		With().
		Timestamp().
		CallerWithSkipFrameCount(1).
		Logger()

	// Convert to logr.Logger and create CustomLogger with both
	logrLogger := zerologr.New(&zeroLogger)
	return NewCustomLoggerWithZerolog(logrLogger, &zeroLogger)
}
