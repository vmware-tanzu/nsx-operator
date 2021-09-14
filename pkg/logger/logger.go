// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package logger

import (
	"fmt"
	"log"
	"os"
	"time"
)

// TODO: this file will be replaced by klog related log function.

// Logger provides log functions.
type Logger interface {
	Debug(v ...interface{})
	Debugf(format string, v ...interface{})

	Error(v ...interface{})
	Errorf(format string, v ...interface{})

	Info(v ...interface{})
	Infof(format string, v ...interface{})

	Warning(v ...interface{})
	Warningf(format string, v ...interface{})

	Fatal(v ...interface{})
	Fatalf(format string, v ...interface{})

	Panic(v ...interface{})
	Panicf(format string, v ...interface{})
}

var Log *DefaultLogger

func init() {
	Log = createLogger("/tmp/logger.log")
	Log.EnableDebug()
	Log.EnableTimestamps()
}

// GetInstance returns a logger instance
func GetInstance() *DefaultLogger {
	return Log
}

func createLogger(fname string) *DefaultLogger {
	file, _ := os.OpenFile(fname, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0777)

	defaultLogger := &DefaultLogger{
		Logger: log.New(file, "", log.Lshortfile),
		debug:  true,
	}
	return defaultLogger
}

const (
	calldepth = 2
)

// DefaultLogger is a default implementation of the Logger interface.
type DefaultLogger struct {
	*log.Logger
	debug bool
}

func (l *DefaultLogger) EnableTimestamps() {
	l.SetFlags(l.Flags() | log.Ldate | log.Ltime)
}

func (l *DefaultLogger) EnableDebug() {
	l.debug = true
}

func (l *DefaultLogger) Debug(v ...interface{}) {
	if l.debug {
		l.Output(calldepth, header("DEBUG", fmt.Sprint(v...)))
	}
}

func (l *DefaultLogger) Debugf(format string, v ...interface{}) {
	if l.debug {
		l.Output(calldepth, header("DEBUG", fmt.Sprintf(format, v...)))
	}
}

func (l *DefaultLogger) Info(v ...interface{}) {
	l.Println(calldepth, header("INFO", fmt.Sprint(v...)))
}

func (l *DefaultLogger) Infof(format string, v ...interface{}) {
	l.Output(calldepth, header("INFO", fmt.Sprintf(format, v...)))
}

func (l *DefaultLogger) Error(v ...interface{}) {
	l.Output(calldepth, header("ERROR", fmt.Sprint(v...)))
}

func (l *DefaultLogger) Errorf(format string, v ...interface{}) {
	l.Output(calldepth, header("ERROR", fmt.Sprintf(format, v...)))
}

func (l *DefaultLogger) Warning(v ...interface{}) {
	l.Output(calldepth, header("WARN", fmt.Sprint(v...)))
}

func (l *DefaultLogger) Warningf(format string, v ...interface{}) {
	l.Output(calldepth, header("WARN", fmt.Sprintf(format, v...)))
}

func (l *DefaultLogger) Fatal(v ...interface{}) {
	l.Output(calldepth, header("FATAL", fmt.Sprint(v...)))
	os.Exit(1)
}

func (l *DefaultLogger) Fatalf(format string, v ...interface{}) {
	l.Output(calldepth, header("FATAL", fmt.Sprintf(format, v...)))
	os.Exit(1)
}

func (l *DefaultLogger) Panic(v ...interface{}) {
	l.Logger.Panic(v...)
}

func (l *DefaultLogger) Panicf(format string, v ...interface{}) {
	l.Logger.Panicf(format, v...)
}

func header(lvl, msg string) string {
	return fmt.Sprintf("%s %s: %s", time.Now().Format("2006/1/2 15:04:05.123z"), lvl, msg)
}
