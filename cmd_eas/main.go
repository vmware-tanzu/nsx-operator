/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/server"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	pkgutil "github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// scheme holds only the types that the VPC info provider and EAS storage layer
// read from kube-apiserver (VPCNetworkConfiguration, Subnet, core types …).
// EAS API types (VPCIPAddressUsage etc.) are served entirely from NSX data
// and live in the generic API server's own scheme (pkg/eas/server/scheme.go).
var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(vpcv1alpha1.AddToScheme(scheme))
}

func main() {
	// config.AddFlags registers all flags (including -nsxconfig and -log-level)
	// and calls flag.Parse() internally.
	config.AddFlags()

	cf, err := config.NewNSXOperatorConfigFromFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read config: %v\n", err)
		os.Exit(1)
	}

	log := logger.ZapCustomLogger(cf.DefaultConfig.Debug, config.LogLevel)
	logger.Log = log
	// Register the logger with controller-runtime so that internal packages
	// (NSX client keepalive, etc.) that use logr / controller-runtime logging
	// emit through the same zap logger rather than printing the
	// "log.SetLogger was never called" warning.
	logf.SetLogger(log.Logger)

	log.Info("Starting NSX Extension API Server")

	// Generate TLS certificates for the EAS HTTPS server.
	// The generic API server (k8s.io/apiserver) uses dynamic certificate loading
	// via dynamiccertificates.NewDynamicServingContentFromFiles, so it
	// automatically picks up renewed cert files without a pod restart.
	// The returned CA PEM is injected into the APIService caBundle so that
	// kube-apiserver can verify the EAS TLS connection.
	caCert, err := pkgutil.GenerateEASCerts()
	if err != nil {
		log.Error(err, "Failed to generate EAS certificates")
		os.Exit(1)
	}
	log.Info("EAS certificates generated successfully")
	go refreshEASCertPeriodically()

	// Initialize NSX client.
	nsxClient := nsx.GetClient(cf)
	if nsxClient == nil {
		log.Error(nil, "Failed to get NSX client")
		os.Exit(1)
	}
	log.Info("NSX client initialized")

	// Build the k8s REST config.
	cfg, err := pkgutil.GetConfig()
	if err != nil {
		log.Error(err, "Failed to get k8s REST config")
		os.Exit(1)
	}

	// Build a direct (non-cached) k8s client.
	// EAS reads Namespaces, VPCNetworkConfiguration CRs, and Subnet CRs on
	// each request; the network round-trip to NSX dominates latency, so the
	// cost of uncached k8s reads is negligible.
	client, err := k8sclient.New(cfg, k8sclient.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "Failed to create k8s client")
		os.Exit(1)
	}

	vpcProvider := eas.NewK8sVPCInfoProvider(client)
	// Pass the kubeconfig file from ncp.ini [k8s].kubeconfig so the EAS server
	// uses it for delegated auth/authz calls instead of reading the
	// extension-apiserver-authentication ConfigMap from kube-system.
	srv := server.NewEASServer(nsxClient, vpcProvider, client, cfg, cf.K8sConfig.KubeConfigFile, caCert)

	// Run until SIGTERM or SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("Starting EAS server")
	if err := srv.Start(ctx); err != nil {
		log.Error(err, "EAS server exited with error")
		os.Exit(1)
	}
}

// refreshEASCertPeriodically regenerates the EAS TLS certificate every 30 days.
// The generic API server loads certs via dynamiccertificates.NewDynamicServingContentFromFiles,
// which watches the cert files for changes and reloads them automatically — so new
// certs are picked up without a pod restart.
func refreshEASCertPeriodically() {
	ticker := time.NewTicker(30 * 24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		logger.Log.Info("Refreshing EAS certificates...")
		if _, err := pkgutil.GenerateEASCerts(); err != nil {
			logger.Log.Error(err, "Failed to refresh EAS certificates")
		} else {
			logger.Log.Info("EAS certificates refreshed successfully")
		}
	}
}
