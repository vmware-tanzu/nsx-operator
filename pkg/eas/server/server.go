/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/storage"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

const (
	// APIBasePath is the URL prefix for all EAS API endpoints.
	APIBasePath = "/apis/eas.nsx.vmware.com/v1alpha1"

	// defaultPort must match build/yaml/eas/service.yaml spec.ports[].targetPort (kube-proxy → pod).
	defaultPort            = "9553"
	defaultShutdownTimeout = 5 * time.Second
	tlsCertFileEnv         = "TLS_CERT_FILE"
	tlsKeyFileEnv          = "TLS_KEY_FILE"
	easPortEnv             = "EAS_PORT"
	// easBindAddressEnv is optional. When empty, the server listens on all local addresses (equivalent to
	// "0.0.0.0" on IPv4 and appropriate dual-stack behavior). Set to 0.0.0.0 to force IPv4 all-interfaces,
	// or to POD_IP (via downward API) if your environment requires binding to the pod network interface.
	easBindAddressEnv = "EAS_BIND_ADDRESS"
)

var _ manager.Runnable = (*EASServer)(nil)

// EASServer holds all dependencies for the Extension API Server.
// EAS is read-only (fetches data from NSX), so all replicas serve
// concurrently without leader election.  The Kubernetes Service
// load-balances across Ready pods automatically.
type EASServer struct {
	vpcProvider     eas.VPCInfoProvider
	vpcIPUsage      *storage.VPCIPAddressUsageStorage
	ipBlockUsage    *storage.IPBlockUsageStorage
	subnetIPPools   *storage.SubnetIPPoolsStorage
	subnetDHCPStats *storage.SubnetDHCPStatsStorage
	handlers        map[string]resourceHandler // resource name → dispatch
	shutdownTimeout time.Duration
	// restConfig, when set, is used to self-register the apiregistration.k8s.io APIService with the API server.
	restConfig *rest.Config
}

// NewEASServer creates a new EAS server with all storage dependencies wired up.
// restConfig may be nil; when provided, Start registers (or updates) the apiregistration.k8s.io APIService
// for eas.nsx.vmware.com/v1alpha1 so the kube-apiserver aggregation layer can reach this process.
func NewEASServer(nsxClient *nsx.Client, vpcProvider eas.VPCInfoProvider, restConfig *rest.Config) *EASServer {
	return &EASServer{
		vpcProvider:     vpcProvider,
		vpcIPUsage:      storage.NewVPCIPAddressUsageStorage(nsxClient, vpcProvider),
		ipBlockUsage:    storage.NewIPBlockUsageStorage(nsxClient, vpcProvider),
		subnetIPPools:   storage.NewSubnetIPPoolsStorage(nsxClient, vpcProvider),
		subnetDHCPStats: storage.NewSubnetDHCPStatsStorage(nsxClient, vpcProvider),
		shutdownTimeout: defaultShutdownTimeout,
		restConfig:      restConfig,
	}
}

// NeedLeaderElection returns false — EAS is read-only and runs on every replica.
func (s *EASServer) NeedLeaderElection() bool {
	return false
}

// Start implements manager.Runnable and starts the HTTP/HTTPS listener.
func (s *EASServer) Start(ctx context.Context) error {
	log := logger.Log

	if s.restConfig != nil {
		if err := s.registerExtensionAPIService(ctx); err != nil {
			log.Error(err, "Failed to register eas APIService; ensure RBAC allows apiregistration.k8s.io/apiservices on the EAS service account, or install build/yaml/eas/apiservice.yaml")
		}
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.registerHealthEndpoints(mux)

	addr, certFile, keyFile, err := listenerConfigFromEnv()
	if err != nil {
		return err
	}

	log.Info("Starting EAS server", "addr", addr)
	return s.serveHTTP(ctx, addr, certFile, keyFile, mux)
}

func (s *EASServer) registerHealthEndpoints(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", serveOK)
	mux.HandleFunc("/readyz", serveOK)
}

func serveOK(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func listenerConfigFromEnv() (addr, certFile, keyFile string, err error) {
	certFile = os.Getenv(tlsCertFileEnv)
	keyFile = os.Getenv(tlsKeyFileEnv)
	if (certFile == "") != (keyFile == "") {
		return "", "", "", fmt.Errorf("%s and %s must be set together", tlsCertFileEnv, tlsKeyFileEnv)
	}

	port := os.Getenv(easPortEnv)
	if port == "" {
		port = defaultPort
	}
	host := os.Getenv(easBindAddressEnv)
	addr = net.JoinHostPort(host, port)
	return addr, certFile, keyFile, nil
}

func (s *EASServer) serveHTTP(ctx context.Context, addr, certFile, keyFile string, handler http.Handler) error {
	log := logger.Log
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error(err, "Failed to shutdown EAS server")
		}
	}()

	if certFile != "" && keyFile != "" {
		log.Info("Starting with TLS", "cert", certFile, "key", keyFile)
		err := server.ListenAndServeTLS(certFile, keyFile)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	log.Info("Starting without TLS (development mode)")
	err := server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
