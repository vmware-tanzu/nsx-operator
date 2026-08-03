package util

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	localhostIP             = "127.0.0.1"
	localhostIPv6           = "::1"
	defaultK8sServicePort   = "6443"
	K8sServicePortEnv       = "KUBERNETES_SERVICE_PORT"
	K8sClientQPSEnv         = "K8S_CLIENT_QPS"
	K8sClientBurstEnv       = "K8S_CLIENT_BURST"
	K8sClientTimeoutEnv     = "K8S_CLIENT_TIMEOUT"
	CacheSyncTimeoutEnv     = "CACHE_SYNC_TIMEOUT"
	DefaultK8sClientQPS     = float32(100)
	DefaultK8sClientBurst   = 200
	DefaultK8sClientTimeout = 2 * time.Minute
	DefaultCacheSyncTimeout = 5 * time.Minute
)

func GetK8sClientQPS() float32 {
	if val := os.Getenv(K8sClientQPSEnv); val != "" {
		if qps, err := strconv.ParseFloat(val, 32); err == nil && qps > 0 {
			return float32(qps)
		}
	}
	return DefaultK8sClientQPS
}

func GetK8sClientBurst() int {
	if val := os.Getenv(K8sClientBurstEnv); val != "" {
		if burst, err := strconv.Atoi(val); err == nil && burst > 0 {
			return burst
		}
	}
	return DefaultK8sClientBurst
}

func GetK8sClientTimeout() time.Duration {
	if val := os.Getenv(K8sClientTimeoutEnv); val != "" {
		if d, err := time.ParseDuration(val); err == nil && d > 0 {
			return d
		}
	}
	return DefaultK8sClientTimeout
}

func GetCacheSyncTimeout() time.Duration {
	if val := os.Getenv(CacheSyncTimeoutEnv); val != "" {
		if d, err := time.ParseDuration(val); err == nil && d > 0 {
			return d
		}
	}
	return DefaultCacheSyncTimeout
}

func GetConfig() (*rest.Config, error) {
	cfg := ctrl.GetConfigOrDie()
	if cfg.QPS <= 0 {
		cfg.QPS = GetK8sClientQPS()
	}
	if cfg.Burst <= 0 {
		cfg.Burst = GetK8sClientBurst()
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = GetK8sClientTimeout()
	}

	if cfg.RateLimiter == nil {
		cfg.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(cfg.QPS, cfg.Burst)
	}

	log.Info("Loaded Kubernetes client configuration", "QPS", cfg.QPS, "Burst", cfg.Burst, "Timeout", cfg.Timeout, "CacheSyncTimeout", GetCacheSyncTimeout())

	var healthy bool
	var getHealthErr error

	if err := retry.OnError(K8sClientRetry, func(err error) bool {
		return err != nil
	}, func() error {
		healthy, getHealthErr = getHealth(cfg)
		return getHealthErr
	}); err != nil {
		return nil, err
	}
	if !healthy {
		var localhostPort string
		if os.Getenv(K8sServicePortEnv) != "" {
			localhostPort = os.Getenv(K8sServicePortEnv)
		} else {
			localhostPort = defaultK8sServicePort
		}
		hosts := strings.Split(cfg.Host, ":")
		// cfg.Host is in the form of https://host:port
		if len(hosts) > 3 {
			cfg.Host = fmt.Sprintf("https://[%s]:%s", localhostIPv6, localhostPort)
		} else {
			cfg.Host = fmt.Sprintf("https://%s:%s", localhostIP, localhostPort)
		}
		log.Info("Failed to connect to configured Kubernetes API Server, set to loopback address", "host", cfg.Host)
	}
	return cfg, nil
}

func getHealth(cfg *rest.Config) (bool, error) {
	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		log.Error(err, "Failed to create client for config", "config", cfg)
		return false, err
	}

	healthUrl := cfg.Host + "/healthz"
	resp, err := client.Get(healthUrl)
	if err != nil {
		log.Error(err, "Failed to connect to Kubernetes API Server", "url", healthUrl)
		return false, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Error(nil, "Kubernetes healthz check failed", "status", resp.Status)
		return false, fmt.Errorf("Kubernetes API Server is unhealthy, status: %s", resp.Status)
	}
	log.Debug("Connection is healthy", "url", healthUrl)
	return true, nil
}
