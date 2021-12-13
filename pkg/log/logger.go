/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package log

import (
	"github.com/spf13/pflag"
	"k8s.io/klog/v2"
)

const (
	logLevelFlag           = "log_level"
	logDirFlag             = "log_dir"
	logFileFlag            = "log_file"
	maxLogFileSizeFlag     = "log_rotation_file_max_mb"
	maxLogFileCountFlag    = "log_rotation_backup_count"
	useSysLogFlag          = "use_syslog"
	defaultLogLevel        = "1"
	defaultLogDir          = "/var/log/nsx-op"
	defaultLogFile         = "nsx-operator.log"
	defaultMaxLogFileSize  = 100
	defaultMaxLogFileCount = 10
)

var (
	level     string
	logDir    string
	logFile   string
	maxSize   int
	maxCount  int
	useSysLog bool
)

func AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&level, logLevelFlag, defaultLogLevel, "NSX Operator log level. Default to 1")
}

func InitWithFlags(f *pflag.FlagSet) {
	var level string
	//var logDir string
	//var logFile string
	//var maxSize int
	//var maxCount int
	//var useSysLog bool
	level, err := f.GetString(logLevelFlag)
	if err != nil {
		return
	}
	if level == "" {
		level = defaultLogLevel
	}
	//TODO implement log file compression functions
	//logDir, err := f.GetString(logDirFlag)
	//if err != nil {
	//	return
	//}
	//logFile, err := f.GetString(logFileFlag)
	//if err != nil {
	//	return
	//}
	//maxSize, err := f.GetInt(maxLogFileSizeFlag)
	//if err != nil {
	//	return
	//}
	//if maxSize == 0 {
	//	maxSize = defaultMaxLogFileSize
	//}
	//maxCount, err := f.GetInt(maxLogFileCountFlag)
	//if maxCount == 0 {
	//	maxCount = defaultMaxLogFileCount
	//}
	//useSysLog, err := f.GetBool(useSysLogFlag)

	//TODO init log settings
	var l klog.Level
	l.Set(level)
}
