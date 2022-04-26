package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

const (
	MetricNamespace = "nsx"
	MetricSubsystem = "operator"
	HealthKey       = "health_status"
	ScrapeTimeout   = 30
)

var (
	log = logf.Log.WithName("metrics")
)

var (
	NSXOperatorHealthStats = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: MetricSubsystem,
			Name:      HealthKey,
			Help:      "Last health status for NSX-Operator. 1 for 'status' label with current status.",
		},
	)

	rpcDurations = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "rpc_durations_seconds",
			Help:       "RPC latency distributions.",
			Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		},
		[]string{"service"},
	)
)

var registerMetrics sync.Once

// Register all metrics.
func Register(m ...prometheus.Collector) {
	registerMetrics.Do(func() {
		metrics.Registry.MustRegister(m...)
	})
}

// Initialize Prometheus metrics collection.
func InitializePrometheusMetrics() {
	log.Info("Initializing prometheus metrics")
	Register(NSXOperatorHealthStats, rpcDurations)
}

func AreMetricsExposed(cf *config.NSXOperatorConfig) bool {
	if cf.EnforcementPoint == "vmc-enforcementpoint" {
		return true
	}
	return false
}
