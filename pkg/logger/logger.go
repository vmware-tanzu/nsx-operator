package logger

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	logTmFmtWithMS = "2006-01-02T15:04:05.000Z"
)

var (
	Log               logr.Logger
	customTimeEncoder = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format(logTmFmtWithMS))
	}
	customCallerEncoder = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(fmt.Sprintf("nsx %d [nsx-op@%d caller=\"%s\"]", os.Getpid(), getGoroutineID(), caller.TrimmedPath()))
	}
)

func init() {
	Log = logf.Log.WithName("nsx-operator")
}

func InitLog(log *logr.Logger) {
	Log = *log
}

func getGoroutineID() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	idField := strings.Fields(strings.TrimPrefix(string(buf[:n]), "goroutine "))[0]
	id, err := strconv.Atoi(idField)
	if err != nil {
		panic(fmt.Sprintf("cannot get goroutine id: %v", err))
	}
	return id
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
		CallerKey:        "caller_line",
		LevelKey:         "level_name",
		MessageKey:       "msg",
		TimeKey:          "ts",
		StacktraceKey:    "stacktrace",
		LineEnding:       zapcore.DefaultLineEnding,
		EncodeTime:       customTimeEncoder,
		EncodeLevel:      zapcore.CapitalLevelEncoder,
		EncodeCaller:     customCallerEncoder,
		EncodeDuration:   zapcore.SecondsDurationEncoder,
		EncodeName:       zapcore.FullNameEncoder,
		ConsoleSeparator: " ",
	}
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConf),
		zapcore.AddSync(zapcore.Lock(os.Stdout)),
		zapcore.Level(-1*logLevel),
	)
	zapLogger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(0))

	defer zapLogger.Sync()

	return zapr.NewLogger(zapLogger)
}
