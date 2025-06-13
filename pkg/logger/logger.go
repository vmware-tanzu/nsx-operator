package logger

import (
	"fmt"
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
	// ANSI escape codes for coloring
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorOrange = "\033[38;5;208m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
)

var (
	Log               logr.Logger
	customTimeEncoder = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format(logTmFmtWithMS))
	}
	customCallerEncoder = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(colorYellow + caller.TrimmedPath() + colorReset)
	}
	customLevelEncoder = func(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
		if level == zapcore.InfoLevel-1 {
			enc.AppendString(colorOrange + "WARNING" + colorReset)
		} else if level == zapcore.InfoLevel-2 {
			enc.AppendString(colorPurple + "DEBUG" + colorReset)
		} else if level < zapcore.InfoLevel-2 {
			enc.AppendString(colorPurple + fmt.Sprintf("LEVEL(%d)", int(level)) + colorReset)
		} else if level == zapcore.ErrorLevel {
			enc.AppendString(colorRed + "ERROR" + colorReset)
		} else if level == zapcore.InfoLevel {
			enc.AppendString(colorGreen + "INFO" + colorReset)
		} else {
			// Use the default encoder for other levels with blue color
			levelStr := level.CapitalString()
			enc.AppendString(colorBlue + levelStr + colorReset)
		}
	}
)

func init() {
	Log = logf.Log.WithName("nsx-operator")
}

func InitLog(log *logr.Logger) {
	Log = *log
}

type FilteredCore struct {
	zapcore.Core
	filter func(zapcore.Entry) bool
}

func (c *FilteredCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.filter(entry) {
		return ce
	}
	return c.Core.Check(entry, ce)
}

func (c *FilteredCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	if c.filter(entry) {
		return nil
	}
	return c.Core.Write(entry, fields)
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

// If debug set in configmap, set the log level to 1.
// If loglevel set in the command line and greater than the debug log level, set it to command line level.
func getLogLevel(cfDebug bool, cfLogLevel int) int {
	logLevel := 0
	if cfDebug {
		logLevel = 1
	}
	realLogLevel := logLevel
	if cfLogLevel > logLevel {
		realLogLevel = cfLogLevel
	}
	return realLogLevel
}

func ZapLogger(cfDebug bool, cfLogLevel int) logr.Logger {
	logLevel := getLogLevel(cfDebug, cfLogLevel)
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
		zapcore.Level(-1*logLevel),
	)

	filteredCore := &FilteredCore{
		Core: baseCore,
		// Filter recorder/recorder.go log
		filter: filterRecorderLogs,
	}

	zapLogger := zap.New(filteredCore, zap.AddCaller(), zap.AddCallerSkip(0))

	defer func(zapLogger *zap.Logger) {
		_ = zapLogger.Sync()
	}(zapLogger)

	return zapr.NewLogger(zapLogger)
}
