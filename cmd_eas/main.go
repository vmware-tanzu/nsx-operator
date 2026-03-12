/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"flag"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas"
	"github.com/vmware-tanzu/nsx-operator/pkg/eas/server"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	pkgutil "github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	scheme         = runtime.NewScheme()
	configFilePath string
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(vpcv1alpha1.AddToScheme(scheme))
}

func main() {
	var logLevel int
	flag.StringVar(&configFilePath, "nsxconfig", "/etc/nsx-ujo/ncp.ini", "NSX Operator configuration file path")
	flag.IntVar(&logLevel, "log-level", 0, "Log verbosity level (0=info, 1=debug, 2=trace)")
	flag.Parse()

	config.UpdateConfigFilePath(configFilePath)
	cf, err := config.NewNSXOperatorConfigFromFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read config: %v\n", err)
		os.Exit(1)
	}

	log := logger.ZapCustomLogger(cf.DefaultConfig.Debug, logLevel)
	logger.Log = log
	// Register logger with controller-runtime to suppress "log.SetLogger(...) was never called" warning.
	logf.SetLogger(log.Logger)

	log.Info("Starting NSX Extension API Server")

	// Initialize NSX client.
	nsxClient := nsx.GetClient(cf)
	if nsxClient == nil {
		log.Error(nil, "Failed to get NSX client")
		os.Exit(1)
	}
	log.Info("NSX client initialized")

	// Get k8s config and create a manager.
	// EAS is read-only so all replicas serve concurrently (no leader election).
	cfg, err := pkgutil.GetConfig()
	if err != nil {
		log.Error(err, "Failed to get rest config for manager")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"}, // disable metrics, shares container with nsx-ncp
	})
	if err != nil {
		log.Error(err, "Failed to create manager")
		os.Exit(1)
	}

	vpcProvider := eas.NewK8sVPCInfoProvider(mgr.GetClient())
	srv := server.NewEASServer(nsxClient, vpcProvider)
	if err := mgr.Add(srv); err != nil {
		log.Error(err, "Failed to add EAS server to manager")
		os.Exit(1)
	}

	log.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "Failed to start manager")
		os.Exit(1)
	}
}
