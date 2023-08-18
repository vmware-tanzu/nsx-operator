/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha2"
	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/ippool"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/namespace"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/nsxserviceaccount"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/securitypolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/staticroute"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnetset"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/vpc"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	scheme               = runtime.NewScheme()
	log                  = logger.Log
	cf                   *config.NSXOperatorConfig
	nsxOperatorNamespace = "default"
)

func init() {
	var err error
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(v1alpha2.AddToScheme(scheme))
	config.AddFlags()

	logf.SetLogger(logger.ZapLogger())
	cf, err = config.NewNSXOperatorConfigFromFile()
	if err != nil {
		log.Error(err, "load config file error")
		os.Exit(1)
	}

	if os.Getenv("NSX_OPERATOR_NAMESPACE") != "" {
		nsxOperatorNamespace = os.Getenv("NSX_OPERATOR_NAMESPACE")
	}

	if cf.HAEnabled() {
		log.Info("HA mode enabled")
	} else {
		log.Info("HA mode disabled")
	}

	if metrics.AreMetricsExposed(cf) {
		metrics.InitializePrometheusMetrics()
	}
}

func main() {
	log.Info("starting NSX Operator")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		HealthProbeBindAddress:  config.ProbeAddr,
		MetricsBindAddress:      config.MetricsAddr,
		LeaderElection:          cf.HAEnabled(),
		LeaderElectionNamespace: nsxOperatorNamespace,
		LeaderElectionID:        "nsx-operator",
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

	//  Embed the common commonService to sub-services.
	commonService := common.Service{
		Client:    mgr.GetClient(),
		NSXClient: nsxClient,
		NSXConfig: cf,
	}

	if cf.CoeConfig.EnableVPCNetwork {
		// Check NSX version for VPC networking mode
		if !commonService.NSXClient.NSXCheckVersion(nsx.VPC) {
			log.Error(nil, "VPC mode cannot be enabled if NSX version is lower than 4.1.1")
			os.Exit(1)
		}
		log.Info("VPC mode is enabled")
		// Start VPC controllers
		if err := namespace.StartNamespaceController(mgr, commonService); err != nil {
			log.Error(err, "failed to start Namespace controller")
			os.Exit(1)
		}
		if err := vpc.StartVPCController(mgr, commonService); err != nil {
			log.Error(err, "failed to start VPC controller")
			os.Exit(1)
		}
		if err := subnet.StartSubnetController(mgr, commonService); err != nil {
			log.Error(err, "failed to start Subnet controller")
			os.Exit(1)
		}
		if err := subnetset.StartSubnetSetController(mgr, commonService); err != nil {
			log.Error(err, "failed to start SubnetSet controller")
			os.Exit(1)
		}
		if err := staticroute.StartStaticRouteController(mgr, commonService); err != nil {
			log.Error(err, "failed to start StaticRoute controller")
			os.Exit(1)
		}
		if err := subnetport.StartSubnetPortController(mgr, commonService); err != nil {
			log.Error(err, "failed to start SubnetPort controller")
			os.Exit(1)
		}
		if err := ippool.StartIPPoolController(mgr, commonService); err != nil {
			log.Error(err, "failed to start IPPool controller")
			os.Exit(1)
		}
	}

	// start controllers which are running on VPC and non-VPC mode
	if err := securitypolicy.StartSecurityPolicyController(mgr, commonService); err != nil {
		log.Error(err, "failed to start SecurityPolicy controller")
		os.Exit(1)
	}
	if cf.EnableAntreaNSXInterworking {
		if err := nsxserviceaccount.StartNSXServiceAccountController(mgr, commonService); err != nil {
			log.Error(err, "failed to start ServiceAccount controller")
			os.Exit(1)
		}
	}

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
