/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package main

import (
	"os"
	"time"

	vmv1alpha1 "github.com/vmware-tanzu/vm-operator/api/v1alpha1"
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
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/ippool"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/namespace"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/node"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/nsxserviceaccount"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/pod"
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
	utilruntime.Must(vmv1alpha1.AddToScheme(scheme))
	config.AddFlags()

	cf, err = config.NewNSXOperatorConfigFromFile()
	if err != nil {
		os.Exit(1)
	}

	logf.SetLogger(logger.ZapLogger(cf))

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

	vpcReconciler := &vpc.VPCReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}
	nsxServiceAccountReconcile := &nsxserviceaccount.NSXServiceAccountReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}
	ippoolReconcile := &ippool.IPPoolReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}
	nsReconciler := &namespace.NamespaceReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), NSXConfig: commonService.NSXConfig}
	securityReconcile := &securitypolicy.SecurityPolicyReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}
	subnetReconciler := &subnet.SubnetReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}
	subnetsetReconciler := &subnetset.SubnetSetReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}
	nodePortReconciler := &node.NodeReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}
	staticRouteReconcile := &staticroute.StaticRouteReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}
	subnetPortReconciler := &subnetport.SubnetPortReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}
	podPortReconciler := &pod.PodReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}

	vpcCtrls := []commonctl.Ctrl{
		subnetPortReconciler,
		subnetReconciler,
		subnetsetReconciler,
		nodePortReconciler,
		staticRouteReconcile,
		podPortReconciler,
		nsReconciler,
		ippoolReconcile,
		vpcReconciler,
	}

	if cf.CoeConfig.EnableVPCNetwork && commonService.NSXClient.NSXCheckVersion(nsx.VPC) {
		log.V(1).Info("VPC mode enabled")
		// Start controllers which only supports VPC
		// Start subnet/subnetset controller.
		for _, ctr := range vpcCtrls {
			ctr.StartController(mgr, commonService)
		}
	}

	// Start the security policy controller.
	securityReconcile.StartController(mgr, commonService)

	// Start the NSXServiceAccount controller.
	if cf.EnableAntreaNSXInterworking {
		nsxServiceAccountReconcile.StartController(mgr, commonService)
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
