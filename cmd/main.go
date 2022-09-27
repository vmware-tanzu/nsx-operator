/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"flag"
	"os"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	securitypolicycontrollers "github.com/vmware-tanzu/nsx-operator/pkg/controllers/securitypolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
)

var (
	scheme                 = runtime.NewScheme()
	probeAddr, metricsAddr string
	log                    logr.Logger
	cf                     *config.NSXOperatorConfig
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8384", "The address the probe endpoint binds to.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8093", "The address the metrics endpoint binds to.")
	config.AddFlags()
	flag.Parse()
	var err error

	cf, err = config.NewNSXOperatorConfigFromFile()
	if err != nil {
		os.Exit(1)
	}
	logf.SetLogger(logger.ZapLogger())
	log = logf.Log.WithName("main")
	if metrics.AreMetricsExposed(cf) {
		metrics.InitializePrometheusMetrics()
	}
}

func StartSecurityPolicyController(mgr ctrl.Manager, service common.Service) {
	securityReconcile := &securitypolicycontrollers.Reconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if securityService, err := securitypolicy.InitializeSecurityPolicy(service); err != nil {
		log.Error(err, "failed to initialize securitypolicy service", "controller", "SecurityPolicy")
		os.Exit(1)
	} else {
		securityService.Client = mgr.GetClient()
		securityReconcile.Service = securityService
	}
	if err := securityReconcile.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "SecurityPolicy")
		os.Exit(1)
	}
}

func main() {
	log.Info("starting NSX Operator")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		MetricsBindAddress:     metricsAddr,
		LeaderElectionID:       "nsx-operator",
	})
	if err != nil {
		log.Error(err, "failed to init manager")
		os.Exit(1)
	}

	// nsxClient is used to interact with NSX API.
	nsxClient := nsx.GetClient(cf)
	if nsxClient == nil {
		log.Error(err, "failed to get nsx client")
		os.Exit(1)
	}

	//  Embed the common service to sub-services.
	var service = common.Service{
		NSXConfig: cf,
		NSXClient: nsxClient,
	}

	// Start the security policy controller.
	StartSecurityPolicyController(mgr, service)

	if metrics.AreMetricsExposed(cf) {
		go updateHealthMetricsPeriodically(nsxClient)
	}

	if err := mgr.AddHealthzCheck("healthz", nsxClient.NSXChecker.CheckNSXHealth); err != nil {
		log.Error(err, "failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "failed to set up ready check")
		os.Exit(1)
	}

	log.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "failed to start manager")
		os.Exit(1)
	}
}

// Function for fetching nsx health status and feeding it to the prometheus metric.
func getHealthStatus(nsxClient *nsx.Client) error {
	status := 1
	if err := nsxClient.NSXChecker.CheckNSXHealth(nil); err != nil {
		status = 0
	}
	// Record the new health status in metric.
	metrics.NSXOperatorHealthStats.Set(float64(status))
	return nil
}

// Periodically fetches health info.
func updateHealthMetricsPeriodically(nsxClient *nsx.Client) {
	for {
		if err := getHealthStatus(nsxClient); err != nil {
			log.Error(err, "failed to fetch health info")
		}
		select {
		case <-time.After(metrics.ScrapeTimeout):
		}
	}
}
