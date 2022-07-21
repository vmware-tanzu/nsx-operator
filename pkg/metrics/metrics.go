package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

const (
	MetricNamespace                 = "nsx"
	MetricSubsystem                 = "operator"
	HealthKey                       = "health_status"
	ControllerSyncTotalKey          = "controller_sync_total"
	ControllerUpdateTotalKey        = "controller_update_total"
	ControllerUpdateSuccessTotalKey = "controller_update_success_total"
	ControllerUpdateFailTotalKey    = "controller_update_fail_total"
	ControllerDeleteTotalKey        = "controller_delete_total"
	ControllerDeleteSuccessTotalKey = "controller_delete_success_total"
	ControllerDeleteFailTotalKey    = "controller_delete_fail_total"
	ScrapeTimeout                   = 30
)

var log = logf.Log.WithName("metrics")

var (
	NSXOperatorHealthStats = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: MetricSubsystem,
			Name:      HealthKey,
			Help:      "Last health status for NSX-Operator. 1 for 'status' label with current status.",
		},
	)
	ControllerSyncTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: MetricSubsystem,
			Name:      ControllerSyncTotalKey,
			Help:      "Total number K8s create, update and delete events syncronized by NSX Operator",
		},
		[]string{"res_type"},
	)
	ControllerUpdateTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: MetricSubsystem,
			Name:      ControllerUpdateTotalKey,
			Help:      "Total number K8s create, update events syncronized by NSX Operator",
		},
		[]string{"res_type"},
	)
	ControllerUpdateSuccessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: MetricSubsystem,
			Name:      ControllerUpdateSuccessTotalKey,
			Help:      "Total number K8s create, update events that are successfully syncronized by NSX Operator",
		},
		[]string{"res_type"},
	)
	ControllerUpdateFailTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: MetricSubsystem,
			Name:      ControllerUpdateFailTotalKey,
			Help:      "Total number K8s create, update events that are failed to be syncronized by NSX Operator",
		},
		[]string{"res_type"},
	)
	ControllerDeleteTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: MetricSubsystem,
			Name:      ControllerDeleteTotalKey,
			Help:      "Total number of K8s delete events syncronized by NSX Operator",
		},
		[]string{"res_type"},
	)
	ControllerDeleteSuccessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: MetricSubsystem,
			Name:      ControllerDeleteSuccessTotalKey,
			Help:      "Total number of K8s delete events that are successfully syncronized by NSX Operator",
		},
		[]string{"res_type"},
	)
	ControllerDeleteFailTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: MetricSubsystem,
			Name:      ControllerDeleteFailTotalKey,
			Help:      "Total number of K8s delete events that are failed to be syncronized by NSX Operator",
		},
		[]string{"res_type"},
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
	Register(
		NSXOperatorHealthStats,
		ControllerSyncTotal,
		ControllerUpdateTotal,
		ControllerUpdateSuccessTotal,
		ControllerUpdateFailTotal,
		ControllerDeleteTotal,
		ControllerDeleteSuccessTotal,
		ControllerDeleteFailTotal,
	)
}

func AreMetricsExposed(cf *config.NSXOperatorConfig) bool {
	if cf.EnforcementPoint == "vmc-enforcementpoint" {
		return true
	}
	return false
}

func CounterInc(cf *config.NSXOperatorConfig, counter *prometheus.CounterVec, res_type string) {
	if AreMetricsExposed(cf) {
		counter.WithLabelValues(res_type).Inc()
	}
}
