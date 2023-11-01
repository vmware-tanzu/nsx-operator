package logger

import (
	"flag"
	"time"

	"github.com/go-logr/logr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	zapcr "sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
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
func getLogLevel(cf *config.NSXOperatorConfig) int {
	logLevel := 0
	if cf.DefaultConfig.Debug {
		logLevel = 1
	}
	realLogLevel := logLevel
	if config.LogLevel > logLevel {
		realLogLevel = config.LogLevel
	}
	return realLogLevel
}

func ZapLogger(cf *config.NSXOperatorConfig) logr.Logger {
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

	opts := zapcr.Options{
		Development:     true,
		Level:           zap.NewAtomicLevelAt(zap.InfoLevel),
		Encoder:         zapcore.NewConsoleEncoder(encoderConf),
		StacktraceLevel: zap.FatalLevel,
	}
	opts.BindFlags(flag.CommandLine)

	// In level.go of zapcore, higher levels are more important.
	// However, in logr.go, a higher verbosity level means a log message is less important.
	// So we need to reverse the order of the levels.
	logLevel := getLogLevel(cf)
	opts.Level = zapcore.Level(-1 * logLevel)
	opts.ZapOpts = append(opts.ZapOpts, zap.AddCaller(), zap.AddCallerSkip(0))
	if logLevel > 0 {
		opts.StacktraceLevel = zap.ErrorLevel
	}

	return zapcr.New(zapcr.UseFlagOptions(&opts))
}
