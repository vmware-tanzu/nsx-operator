// Package logger provides logging functionality for the nsx-operator.
package logger

import (
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	logTmFmtWithMS = "2006-01-02 15:04:05.000"
	// Log level prefixes for message filtering
	warnPrefix  = "+warn"
	debugPrefix = "+debug"
	// ANSI escape codes for coloring
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorOrange = "\033[38;5;208m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
)

// CustomLogger is a wrapper around logr.Logger that provides more intuitive log level methods
type CustomLogger struct {
	logger logr.Logger
}

// Error logs an error message with the given error and key-value pairs
func (l *CustomLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	l.logger.Error(err, msg, keysAndValues...)
}

// Warn logs a warning message with the given key-value pairs
func (l *CustomLogger) Warn(msg string, keysAndValues ...interface{}) {
	// Use V(0) instead of V(1) to ensure warnings are displayed at log level 0
	// Adds a special prefix to the message that can be detected by the FilteredCore implementation
	l.logger.V(0).Info(warnPrefix+msg, keysAndValues...)
}

// Info logs an info message with the given key-value pairs
func (l *CustomLogger) Info(msg string, keysAndValues ...interface{}) {
	l.logger.Info(msg, keysAndValues...)
}

// Debug logs a debug message with the given key-value pairs
func (l *CustomLogger) Debug(msg string, keysAndValues ...interface{}) {
	// Don't know why l.logger.V(-1).Info is not working as Debug, hack it
	l.logger.V(-1).Info(debugPrefix+msg, keysAndValues...)
}

var (
	// Log is the original logr.Logger interface
	Log logr.Logger
	// CustomLog is a more intuitive wrapper around Log with methods like Error, Warn, Info, Debug, and V(level)
	CustomLog         CustomLogger
	customTimeEncoder = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format(logTmFmtWithMS))
	}
	customCallerEncoder = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(colorYellow + caller.TrimmedPath() + colorReset)
	}
	customLevelEncoder = func(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
		if level == -2 { // weird, -1 not works
			enc.AppendString(colorPurple + "DEBUG" + colorReset)
		} else if level == zapcore.InfoLevel {
			enc.AppendString(colorGreen + "INFO" + colorReset)
		} else if level == zapcore.WarnLevel {
			enc.AppendString(colorOrange + "WARN" + colorReset)
		} else if level == zapcore.ErrorLevel {
			enc.AppendString(colorRed + "ERROR" + colorReset)
		} else {
			// Use the default encoder for other levels with blue color
			levelStr := level.CapitalString()
			enc.AppendString(colorBlue + levelStr + colorReset)
		}
	}
)

func init() {
	Log = logf.Log.WithName("nsx-operator")
	CustomLog = CustomLogger{logger: Log}
}

func InitLog(log *logr.Logger) {
	Log = *log
	CustomLog = CustomLogger{logger: Log}
}

type FilteredCore struct {
	zapcore.Core
	filter func(zapcore.Entry) bool
}

func (c *FilteredCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.filter(entry) {
		return ce
	}

	// Check if the message has warned prefix
	if strings.HasPrefix(entry.Message, warnPrefix) {
		// Remove the prefix from the message
		entry.Message = strings.TrimPrefix(entry.Message, warnPrefix)
		// Change the log level to WARN
		entry.Level = zapcore.WarnLevel
	}
	// Check if the message has a debug prefix
	if strings.HasPrefix(entry.Message, debugPrefix) {
		// Remove the prefix from the message
		entry.Message = strings.TrimPrefix(entry.Message, debugPrefix)
		// Change the log level to Debug
		entry.Level = zapcore.DebugLevel
	}
	return c.Core.Check(entry, ce)
}

func (c *FilteredCore) With(fields []zapcore.Field) zapcore.Core {
	return &FilteredCore{
		Core:   c.Core.With(fields),
		filter: c.filter,
	}
}

func filterRecorderLogs(entry zapcore.Entry) bool {
	if strings.Contains(entry.Caller.File, "recorder/recorder.go") {
		return true
	}
	return false
}

func ZapLogger(cfDebug bool, cfLogLevel int) logr.Logger {
	logLevel := cfLogLevel
	if cfDebug {
		logLevel = 0
	}

	encoderConf := zapcore.EncoderConfig{
		CallerKey:      "caller_line",
		LevelKey:       "level_name",
		MessageKey:     "msg",
		TimeKey:        "ts",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     customTimeEncoder,
		EncodeLevel:    customLevelEncoder, // Use custom level encoder
		EncodeCaller:   customCallerEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeName:     zapcore.FullNameEncoder,
	}

	baseCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConf),
		zapcore.AddSync(zapcore.Lock(os.Stdout)),
		zapcore.Level(logLevel-1),
	)

	filteredCore := &FilteredCore{
		Core: baseCore,
		// Filter recorder/recorder.go log
		filter: filterRecorderLogs,
	}

	zapLogger := zap.New(filteredCore, zap.AddCaller(), zap.AddCallerSkip(1))

	defer func(zapLogger *zap.Logger) {
		_ = zapLogger.Sync()
	}(zapLogger)

	return zapr.NewLogger(zapLogger)
}
