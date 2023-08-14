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
	commonctl "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	ippool2 "github.com/vmware-tanzu/nsx-operator/pkg/controllers/ippool"
	namespacecontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/namespace"
	nsxserviceaccountcontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/nsxserviceaccount"
	securitypolicycontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/securitypolicy"
	staticroutecontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/staticroute"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnet"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/subnetset"
	vpccontroller "github.com/vmware-tanzu/nsx-operator/pkg/controllers/vpc"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ippool"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/nsxserviceaccount"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/vpc"
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

func StartSecurityPolicyController(mgr ctrl.Manager, commonService common.Service) {
	securityReconcile := &securitypolicycontroller.SecurityPolicyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if securityService, err := securitypolicy.InitializeSecurityPolicy(commonService); err != nil {
		log.Error(err, "failed to initialize securitypolicy commonService", "controller", "SecurityPolicy")
		os.Exit(1)
	} else {
		securityReconcile.Service = securityService
		commonctl.ServiceMediator.SecurityPolicyService = securityService
	}
	if err := securityReconcile.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "SecurityPolicy")
		os.Exit(1)
	}
}

func StartNSXServiceAccountController(mgr ctrl.Manager, commonService common.Service) {
	log.Info("starting NSXServiceAccountController")
	nsxServiceAccountReconcile := &nsxserviceaccountcontroller.NSXServiceAccountReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if nsxServiceAccountService, err := nsxserviceaccount.InitializeNSXServiceAccount(commonService); err != nil {
		log.Error(err, "failed to initialize service", "controller", "NSXServiceAccount")
		os.Exit(1)
	} else {
		nsxServiceAccountReconcile.Service = nsxServiceAccountService
	}
	if err := nsxServiceAccountReconcile.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "NSXServiceAccount")
		os.Exit(1)
	}
}

func StartIPPoolController(mgr ctrl.Manager, commonService common.Service) {
	ippoolReconcile := &ippool2.IPPoolReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if ipPoolService, err := ippool.InitializeIPPool(commonService); err != nil {
		log.Error(err, "failed to initialize ippool commonService", "controller", "IPPool")
		os.Exit(1)
	} else {
		ippoolReconcile.Service = ipPoolService
	}

	// TODO: remove this after vpc is ready
	if vpcService, err := vpc.InitializeVPC(commonService); err != nil {
		log.Error(err, "failed to initialize vpc commonService", "controller", "vpc")
		os.Exit(1)
	} else {
		commonctl.ServiceMediator.VPCService = vpcService
	}
	if err := ippoolReconcile.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "IPPool")
		os.Exit(1)
	}
}

func StartVPCController(mgr ctrl.Manager, commonService common.Service) {
	vpcReconciler := &vpccontroller.VPCReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if vpcService, err := vpc.InitializeVPC(commonService); err != nil {
		log.Error(err, "failed to initialize vpc commonService", "controller", "VPC")
		os.Exit(1)
	} else {
		vpcReconciler.Service = vpcService
		commonctl.ServiceMediator.VPCService = vpcService
	}
	if err := vpcReconciler.Start(mgr); err != nil {
		log.Error(err, "failed to create vpc controller", "controller", "VPC")
		os.Exit(1)
	}
}

func StartNamespaceController(mgr ctrl.Manager, commonService common.Service) {
	nsReconciler := &namespacecontroller.NamespaceReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		NSXConfig: commonService.NSXConfig,
	}

	if err := nsReconciler.Start(mgr); err != nil {
		log.Error(err, "failed to create namespace controller", "controller", "Namespace")
		os.Exit(1)
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

	if cf.CoeConfig.EnableVPCNetwork && commonService.NSXClient.NSXCheckVersion(nsx.VPC) {
		log.V(1).Info("VPC mode enabled")
		// Start controllers which only supports VPC
		// Start subnet/subnetset controller.
		if err := subnet.StartSubnetController(mgr, commonService); err != nil {
			os.Exit(1)
		}
		if err := subnetset.StartSubnetSetController(mgr, commonService); err != nil {
			os.Exit(1)
		}

		staticroutecontroller.StartStaticRouteController(mgr, commonService)
		subnetport.StartSubnetPortController(mgr, commonService)

		StartNamespaceController(mgr, commonService)
		StartVPCController(mgr, commonService)
		StartIPPoolController(mgr, commonService)
	}

	// Start the security policy controller.
	StartSecurityPolicyController(mgr, commonService)

	// Start the NSXServiceAccount controller.
	if cf.EnableAntreaNSXInterworking {
		StartNSXServiceAccountController(mgr, commonService)
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
