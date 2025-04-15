package util

import (
	"fmt"
	"net/http"
	"strings"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	localhostIP   = "127.0.0.1"
	localhostIPv6 = "::1"
	localhostPort = "6443"
)

func GetConfig() *rest.Config {
	cfg := ctrl.GetConfigOrDie()
	cfg.Timeout = TCPReadTimeout
	if !getHealth(cfg) {
		hosts := strings.Split(cfg.Host, ":")
		// cfg.Host is in the form of https://host:port
		if len(hosts) > 3 {
			cfg.Host = fmt.Sprintf("https://[%s]:%s", localhostIPv6, localhostPort)
		} else {
			cfg.Host = fmt.Sprintf("https://%s:%s", localhostIP, localhostPort)
		}
		log.Info("Failed to connect to configured kubernetes API server, set to loopback address", "host", cfg.Host)
	}
	return cfg
}

func getHealth(cfg *rest.Config) bool {
	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		log.Error(err, "Failed to create client for config", "config", cfg)
		return false
	}

	healthUrl := cfg.Host + "/healthz"
	resp, err := client.Get(healthUrl)
	if err != nil {
		log.Error(err, "Failed to connect to Kubernetes API Server", "url", healthUrl)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Error(nil, "Kubernetes healthz check failed", "status", resp.Status)
		return false
	}
	log.Debug("Connection is health", "url", healthUrl)
	return true
}
