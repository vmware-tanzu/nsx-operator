package health

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var log = logger.Log

// HealthStatus represents the health status of the cluster
type HealthStatus string

const (
	// HealthStatusHealthy indicates the system is healthy
	HealthStatusHealthy HealthStatus = "HEALTHY"
	// HealthStatusDown indicates the system is down
	HealthStatusDown HealthStatus = "DOWN"
	// DefaultReportInterval is the default interval for health status reporting
	DefaultReportInterval = 60 * time.Second
)

// HealthCheckHandler defines the interface for health check handlers
type HealthCheckHandler func() error

// HealthCheckHandlers contains all the health check handlers
type HealthCheckHandlers struct {
	handlers map[string]HealthCheckHandler
}

// NewHealthCheckHandlers creates a new HealthCheckHandlers instance
func NewHealthCheckHandlers() *HealthCheckHandlers {
	return &HealthCheckHandlers{
		handlers: make(map[string]HealthCheckHandler),
	}
}

// AddHandler adds a health check handler
func (h *HealthCheckHandlers) AddHandler(name string, handler HealthCheckHandler) {
	h.handlers[name] = handler
}

// GetHandlers returns all registered handlers
func (h *HealthCheckHandlers) GetHandlers() map[string]HealthCheckHandler {
	handlers := make(map[string]HealthCheckHandler)
	for name, handler := range h.handlers {
		handlers[name] = handler
	}
	return handlers
}

// ClusterHealthChecker is the main structure for performing health checks
type ClusterHealthChecker struct {
	nsxClient         *nsx.Client
	k8sClient         client.Client
	handlers          *HealthCheckHandlers
	healthCheckCtx    context.Context
	healthCheckCancel context.CancelFunc
}

// NewClusterHealthChecker creates a new ClusterHealthChecker instance
func NewClusterHealthChecker(nsxClient *nsx.Client, k8sClient client.Client) *ClusterHealthChecker {
	ctx, cancel := context.WithCancel(context.Background())

	checker := &ClusterHealthChecker{
		nsxClient:         nsxClient,
		k8sClient:         k8sClient,
		handlers:          NewHealthCheckHandlers(),
		healthCheckCtx:    ctx,
		healthCheckCancel: cancel,
	}

	// Register default health check handlers
	checker.registerDefaultHandlers()

	return checker
}

// registerDefaultHandlers registers the default health check handlers
func (c *ClusterHealthChecker) registerDefaultHandlers() {
	// NSX health check handler
	c.handlers.AddHandler("NSX", c.checkNSXHealth)

	// Kubernetes API server health check handler
	c.handlers.AddHandler("Kubernetes", c.checkKubernetesHealth)

	// TODO add _nsx_restore_check
}

// checkNSXHealth checks the health of NSX
func (c *ClusterHealthChecker) checkNSXHealth() error {
	return c.nsxClient.NSXChecker.CheckNSXHealth(nil)
}

// checkKubernetesHealth checks the health of Kubernetes
func (c *ClusterHealthChecker) checkKubernetesHealth() error {
	if c.k8sClient == nil {
		return fmt.Errorf("kubernetes client not initialized")
	}

	// Simple health check by listing namespaces
	namespaceList := &corev1.NamespaceList{}
	err := c.k8sClient.List(context.TODO(), namespaceList)
	return err
}

// CheckClusterHealth performs a one-time health check and returns the overall status
func (c *ClusterHealthChecker) CheckClusterHealth() HealthStatus {
	handlers := c.handlers.GetHandlers()
	hasErrors := false

	for checkItem, checkHandler := range handlers {
		if err := checkHandler(); err != nil {
			log.Debug("Health check failed", "component", checkItem, "error", err)
			hasErrors = true
		}
	}

	if hasErrors {
		return HealthStatusDown
	}
	return HealthStatusHealthy
}

// SystemHealthReporter reports system health status to NSX
type SystemHealthReporter struct {
	healthChecker  *ClusterHealthChecker
	nsxClient      *nsx.Client
	clusterID      string
	reportCtx      context.Context
	reportCancel   context.CancelFunc
	reportInterval time.Duration
	ticker         *time.Ticker
}

// NewSystemHealthReporter creates a new SystemHealthReporter instance
func NewSystemHealthReporter(nsxClient *nsx.Client, clusterID string, k8sClient client.Client) *SystemHealthReporter {
	ctx, cancel := context.WithCancel(context.Background())
	return &SystemHealthReporter{
		healthChecker:  NewClusterHealthChecker(nsxClient, k8sClient),
		nsxClient:      nsxClient,
		clusterID:      clusterID,
		reportCtx:      ctx,
		reportCancel:   cancel,
		reportInterval: DefaultReportInterval,
	}
}

// Start initiates the system health reporting
func (r *SystemHealthReporter) Start() {
	go r.runHealthReport()
}

// Stop stops the health reporter
func (r *SystemHealthReporter) Stop() {
	if r.reportCancel != nil {
		r.reportCancel()
	}
	if r.ticker != nil {
		r.ticker.Stop()
	}
}

// runHealthReport periodically reports health status to NSX
func (r *SystemHealthReporter) runHealthReport() {
	r.ticker = time.NewTicker(r.reportInterval)
	defer r.ticker.Stop()

	for {
		select {
		case <-r.reportCtx.Done():
			log.Info("Health reporting stopped")
			return
		case <-r.ticker.C:
			r.processHealthReport()
		}
	}
}

// processHealthReport processes a single health report cycle
func (r *SystemHealthReporter) processHealthReport() {
	interval, err := r.reportHealthStatus()
	if err != nil {
		log.Error(err, "Failed to report health status")
	} else if interval > 0 && time.Duration(interval)*time.Second != r.reportInterval {
		// Update ticker with a new interval from NSX Manager
		r.updateReportInterval(interval)
	}
}

// updateReportInterval updates the reporting interval
func (r *SystemHealthReporter) updateReportInterval(interval int) {
	newInterval := time.Duration(interval) * time.Second
	r.ticker.Reset(newInterval)
	r.reportInterval = newInterval
	log.Debug("Updated health report interval", "interval", interval)
}

// reportHealthStatus reports the current health status to NSX and returns the reporting interval
func (r *SystemHealthReporter) reportHealthStatus() (int, error) {
	healthStatus := r.healthChecker.CheckClusterHealth()

	log.Debug("Reporting health status", "status", healthStatus, "cluster", r.clusterID)

	// Send health status to NSX Manager
	response, err := r.sendHealthStatusToNSX(healthStatus)
	if err != nil {
		return 0, err
	}

	// Extract and validate an interval from response
	interval := r.extractIntervalFromResponse(response)
	log.Debug("Health status reported", "cluster", r.clusterID, "status", healthStatus, "interval", interval)

	return interval, nil
}

// sendHealthStatusToNSX sends the health status to NSX Manager using REST API
func (r *SystemHealthReporter) sendHealthStatusToNSX(status HealthStatus) (map[string]interface{}, error) {
	// Convert HealthStatus to string for NSX API
	statusStr := string(status)

	// Create a request body
	requestBody := map[string]interface{}{
		"cluster_id": r.clusterID,
		"status":     statusStr,
	}

	// Health clients are now using REST API directly
	// Create the URL for the health status API
	url := "api/v1/systemhealth/container-cluster/ncp/status"

	// Use the HttpPost method from the Cluster instance
	responseBody, err := r.nsxClient.Cluster.HttpPost(url, requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to send health status: %v", err)
	}

	return responseBody, nil
}

// extractIntervalFromResponse extracts the interval from the NSX response
func (r *SystemHealthReporter) extractIntervalFromResponse(response map[string]interface{}) int {
	interval := int(DefaultReportInterval / time.Second)
	if intervalVal, ok := response["interval"]; ok {
		if intervalFloat, ok := intervalVal.(float64); ok {
			interval = int(intervalFloat)
		}
	}
	return interval
}

func Start(nsxClient *nsx.Client, cf *config.NSXOperatorConfig, k8sClient client.Client) {
	log.Info("System health reporter started")
	clusterUUID := util.GetClusterUUID(cf.Cluster).String()
	healthReporter := NewSystemHealthReporter(nsxClient, clusterUUID, k8sClient)
	healthReporter.Start()
}
