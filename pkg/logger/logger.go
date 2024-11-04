package logger

import (
	"os"
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
	colorYellow = "\033[33m"
)

var (
	Log               logr.Logger
	customTimeEncoder = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format(logTmFmtWithMS))
	}
	customCallerEncoder = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(colorYellow + caller.TrimmedPath() + colorReset)
	}
)

func init() {
	Log = logf.Log.WithName("nsx-operator")
}

func InitLog(log *logr.Logger) {
	Log = *log
}

// If debug set in configmap, set log level to 1.
// If loglevel set in command line and greater than debug log level, set it to command line level.
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

	// Ensure log level is within acceptable bounds for zapcore.Level
	if logLevel < int(zapcore.DebugLevel) {
		logLevel = int(zapcore.DebugLevel)
	} else if logLevel > int(zapcore.FatalLevel) {
		logLevel = int(zapcore.FatalLevel)
	}

	encoderConf := zapcore.EncoderConfig{
		CallerKey:      "caller_line",
		LevelKey:       "level_name",
		MessageKey:     "msg",
		TimeKey:        "ts",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     customTimeEncoder,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder,
		EncodeCaller:   customCallerEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeName:     zapcore.FullNameEncoder,
	}
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConf),
		zapcore.AddSync(zapcore.Lock(os.Stdout)),
		// #nosec G115
		zapcore.Level(-1*zapcore.Level(logLevel)),
	)
	zapLogger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(0))

	defer zapLogger.Sync()

	return zapr.NewLogger(zapLogger)
}
