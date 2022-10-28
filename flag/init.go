package flag

import (
	"flag"
	"os"
)

var (
	ProbeAddr      string
	MetricsAddr    string
	LogLevel       int
	ConfigFilePath string
)

func init() {
	fs := flag.NewFlagSet("", flag.ExitOnError)
	probeAddr := fs.String("health-probe-bind-address", ":8384", "The address the probe endpoint binds to.")
	metricAddr := fs.String("metrics-bind-address", ":8093", "The address the metrics endpoint binds to.")
	logLevel := fs.Int("log-level", 0, "Use zap-core log system.")
	configFilePath := fs.String("nsxconfig", "/etc/nsx-operator/nsxop.ini", "NSX Operator configuration file path")
	hack(fs)
	fs.Parse(os.Args[1:])
	ProbeAddr, MetricsAddr, LogLevel, ConfigFilePath = *probeAddr, *metricAddr, *logLevel, *configFilePath
}

// go test will report error if there are no such options
func hack(fs *flag.FlagSet) {
	_ = fs.String("test.paniconexit0", "", "")
	_ = fs.String("test.coverprofile", "", "")
}
