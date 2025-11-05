package util

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	localhostIP           = "127.0.0.1"
	localhostIPv6         = "::1"
	defaultK8sServicePort = "6443"
	K8sServicePortEnv     = "KUBERNETES_SERVICE_PORT"
)

func GetConfig() (*rest.Config, error) {
	cfg := ctrl.GetConfigOrDie()
	cfg.Timeout = TCPReadTimeout
	healthy, err := getHealth(cfg)
	if err != nil {
		return nil, err
	} else if !healthy {
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
