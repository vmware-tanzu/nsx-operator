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

const logTmFmtWithMS = "2006-01-02 15:04:05.000"

var (
	Log               logr.Logger
	customTimeEncoder = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format(logTmFmtWithMS))
	}
	customCallerEncoder = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(caller.TrimmedPath())
	}
)

func init() {
	Log = logf.Log.WithName("nsx-operator")
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
		zapcore.Level(-1*logLevel),
	)
	zapLogger := zap.New(core)
	defer zapLogger.Sync()

	return zapr.NewLogger(zapLogger)
}
