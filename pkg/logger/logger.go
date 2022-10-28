package logger

import (
	"flag"
	"sync"
	"time"

	argflag "github.com/vmware-tanzu/nsx-operator/flag"

	"github.com/go-logr/logr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	zapcr "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const logTmFmtWithMS = "2006-01-02 15:04:05.000"

var (
	customTimeEncoder = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format(logTmFmtWithMS))
	}
	customCallerEncoder = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(caller.TrimmedPath())
	}
	calcTotalOnce sync.Once
)

func Log() logr.Logger {
	calcTotalOnce.Do(func() {
		logf.SetLogger(ZapLogger())
	})
	return logf.Log.WithName("nsx-operator")
}

func ZapLogger() logr.Logger {
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
	opts.Level = zapcore.Level(-1 * argflag.LogLevel)
	opts.ZapOpts = append(opts.ZapOpts, zap.AddCaller(), zap.AddCallerSkip(0))
	if argflag.LogLevel > 0 {
		opts.StacktraceLevel = zap.ErrorLevel
	}

	return zapcr.New(zapcr.UseFlagOptions(&opts))
}
