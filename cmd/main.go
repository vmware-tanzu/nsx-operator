/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"flag"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	scheme                 = runtime.NewScheme()
	probeAddr, metricsAddr string
	log                    = logger.Log
	cf                     *config.NSXOperatorConfig
)

func init() {
	var err error
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8384", "The address the probe endpoint binds to.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8093", "The address the metrics endpoint binds to.")
	config.AddFlags()
	flag.Parse()

	logf.SetLogger(logger.ZapLogger())
	cf, err = config.NewNSXOperatorConfigFromFile()
	if err != nil {
		log.Error(err, "load config file error")
		os.Exit(1)
	}

	if metrics.AreMetricsExposed(cf) {
		metrics.InitializePrometheusMetrics()
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
	securityReconcile := &controllers.SecurityPolicyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	nsxClient := nsx.GetClient(cf)
	if nsxClient == nil {
		log.Error(err, "unable to get nsx client")
		os.Exit(1)
	}
	checkLicense(nsxClient, cf.LicenseValidationInterval)
	if service, err := services.InitializeSecurityPolicy(nsxClient, cf); err != nil {
		log.Error(err, "unable to initialize securitypolicy service", "controller", "SecurityPolicy")
		os.Exit(1)
	} else {
		securityReconcile.Service = service
	}

	if err = securityReconcile.Start(mgr); err != nil {
		log.Error(err, "unable to create controller", "controller", "SecurityPolicy")
		os.Exit(1)
	}

	if metrics.AreMetricsExposed(cf) {
		go updateHealthMetricsPeriodically(nsxClient)
	}

	if err := mgr.AddHealthzCheck("healthz", nsxClient.NSXChecker.CheckNSXHealth); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
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

func checkLicense(nsxClient *nsx.Client, interval int) {
	err := nsxClient.ValidateLicense(true)
	if err != nil {
		os.Exit(1)
	}
	// if there is no dfw license enabled, check license more frequently
	// if customer set it in config, use it, else use licenseTimeoutNoDFW
	if interval == 0 {
		if !util.IsLicensed(util.FeatureDFW) {
			interval = config.LicenseIntervalForDFW
		} else {
			interval = config.LicenseInterval
		}
	}
	go updateLicensePeriodically(nsxClient, time.Duration(interval)*time.Second)
}

func updateLicensePeriodically(nsxClient *nsx.Client, interval time.Duration) {
	for {
		select {
		case <-time.After(interval):
		}
		err := nsxClient.ValidateLicense(false)
		if err != nil {
			os.Exit(1)
		}
	}
}
